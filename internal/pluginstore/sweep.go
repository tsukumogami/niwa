package pluginstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/hostdir"
)

// defaultSweepLockWait is the sweep's wait for a candidate's lock when
// Store.SweepLockWait is zero.
const defaultSweepLockWait = 2 * time.Second

// SweepResult is what one Sweep pass did, each list in release-id order.
type SweepResult struct {
	// Removed were held, not the newest of their source, and linked by no
	// instance on disk under their lock; they are gone from the store.
	Removed []Release
	// Busy were candidates whose lock another command held past the wait;
	// they were left for the next command's sweep.
	Busy []Release
}

// SweepError is every error Sweep returns. It says which side failed: the
// store couldn't be used, with Cond naming its condition, or the scan of the
// instances on disk failed, with Cond empty. Its message is Err's.
type SweepError struct {
	Cond Condition
	Err  error
}

func (e *SweepError) Error() string { return e.Err.Error() }

func (e *SweepError) Unwrap() error { return e.Err }

// ScanFailed reports whether the scan of the instances failed, rather than
// the store.
func (e *SweepError) ScanFailed() bool { return e.Cond == CondNone }

// sweepErr wraps a non-nil err as a SweepError with cond, and leaves nil as
// nil.
func sweepErr(cond Condition, err error) error {
	if err == nil {
		return nil
	}
	return &SweepError{Cond: cond, Err: err}
}

// sweepLockWait is SweepLockWait or its default.
func (s Store) sweepLockWait() time.Duration {
	if s.SweepLockWait > 0 {
		return s.SweepLockWait
	}
	return defaultSweepLockWait
}

// Sweep deletes the held releases of this store that no instance on disk
// links to, except the newest held release of each source. It never deletes
// a release an instance on disk links to, and it deletes nothing when the
// scan can't see every instance: a root or an existing kept/ that can't be
// listed, or an instance's .niwa or .niwa/marketplaces that exists but can't
// be read, returns the error and leaves the store as it is.
//
// scan lists the instances on disk; nil means hostdir.InstanceDirs, which is
// the one place a new instance location must be added for the sweep, and for
// repair, to see its links.
//
// A release counts only when Held reports it held and its mark's commit
// time parses. The newest of a source is the one with the latest commit time
// among those, a tie going to the greater commit hash. A <commit> directory
// that doesn't count is never the newest and is never deleted here.
//
// Each candidate is deleted under its own lock, after a second scan finds no
// instance linking it, so a command linking it meanwhile keeps it. A lock
// held past SweepLockWait skips the candidate. A deleted release is moved
// into .trash/ and removed there, so a reader sees the whole release or
// nothing; its .tmp/ and .trash/ entries go with it, and so do the owner and
// repo directories it leaves empty. A store with no plugins directory is left
// alone and never created.
//
// Every error is a *SweepError, which says which of two kinds it is. The
// store couldn't be used: plugins/ or a level under it can't be inspected or
// listed (CondUnreadable), or a candidate's lock, .trash/ or move into it
// failed (CondUnwritable). Or the scan of the instances failed, the first or
// a candidate's rescan, and Cond is empty.
func (s Store) Sweep(ctx context.Context, scan func() ([]string, error)) (SweepResult, error) {
	var res SweepResult
	if scan == nil {
		scan = func() ([]string, error) { return hostdir.InstanceDirs(s.Home, s.WorkspaceRoot) }
	}
	candidates, exists, err := s.sweepCandidates()
	if err != nil || !exists {
		return res, sweepErr(CondUnreadable, err)
	}
	linked, err := linkedStrict(scan)
	if err != nil {
		return res, sweepErr(CondNone, err)
	}
	for _, r := range candidates {
		if linked[r.ID()] {
			continue
		}
		unlock, err := filelock.WaitFileLock(ctx, LockPath(s.Home, s.WorkspaceRoot, r), fmt.Sprintf("niwa pid %d sweep", os.Getpid()), s.sweepLockWait())
		if err != nil {
			if errors.Is(err, filelock.ErrLockHeld) {
				res.Busy = append(res.Busy, r)
				continue
			}
			return res, sweepErr(CondUnwritable, fmt.Errorf("locking release %s: %w", r.Label(), err))
		}
		removed, err := s.sweepRelease(r, scan)
		unlock()
		if err != nil {
			return res, err
		}
		if removed {
			res.Removed = append(res.Removed, r)
		}
	}
	return res, nil
}

// sweepRelease deletes r when a strict rescan finds no instance linking it.
// The caller holds r's lock. Its errors are SweepErrors.
func (s Store) sweepRelease(r Release, scan func() ([]string, error)) (bool, error) {
	linked, err := linkedStrict(scan)
	if err != nil {
		return false, sweepErr(CondNone, err)
	}
	if linked[r.ID()] {
		return false, nil
	}
	pdir := s.pluginsDir()
	trash := filepath.Join(pdir, trashDirName)
	if err := hostdir.CreatePrivateDir(trash); err != nil {
		return false, sweepErr(CondUnwritable, err)
	}
	if err := s.trashRelease(r); err != nil {
		return false, sweepErr(CondUnwritable, err)
	}
	clearStaging(filepath.Join(pdir, tmpDirName), r)
	clearStaging(trash, r)
	// A level still holding another release stays; one that can't be
	// removed is harmless and left for a later pass.
	repoDir := filepath.Dir(releaseDir(pdir, r))
	_ = os.Remove(repoDir)
	_ = os.Remove(filepath.Dir(repoDir))
	return true, nil
}

// linkedStrict is the set of release ids the instances scan lists link to,
// read by the strict scan. Any error from scan, or from reading an instance,
// is returned with no set.
func linkedStrict(scan func() ([]string, error)) (map[string]bool, error) {
	instances, err := scan()
	if err != nil {
		return nil, err
	}
	linked, err := scanLinks(instances, true)
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(linked))
	for _, l := range linked {
		ids[l.Release.ID()] = true
	}
	return ids, nil
}

// countedRelease is a held release with the commit time its mark records.
type countedRelease struct {
	release Release
	at      time.Time
}

// sweepCandidates walks plugins/github.com/<owner>/<repo>/<commit> by Lstat,
// real directories with valid names only, and returns every counted release
// that is not the newest of its source, in release-id order. exists is false,
// with no error, when there is no plugins directory. A level that exists but
// can't be listed is an error naming its path.
func (s Store) sweepCandidates() (candidates []Release, exists bool, err error) {
	pdir := s.pluginsDir()
	info, err := os.Lstat(pdir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("inspecting %s: %w", pdir, err)
	}
	if !info.IsDir() {
		return nil, true, fmt.Errorf("%s is not a directory", pdir)
	}
	if _, err := os.ReadDir(pdir); err != nil {
		return nil, true, fmt.Errorf("listing %s: %w", pdir, err)
	}

	forge := filepath.Join(pdir, Forge)
	owners, err := listDirs(forge)
	if err != nil {
		return nil, true, err
	}
	bySource := map[string][]countedRelease{}
	for _, owner := range owners {
		if !ValidOwner(owner) {
			continue
		}
		repos, err := listDirs(filepath.Join(forge, owner))
		if err != nil {
			return nil, true, err
		}
		for _, repo := range repos {
			if !ValidRepo(repo) {
				continue
			}
			commits, err := listDirs(filepath.Join(forge, owner, repo))
			if err != nil {
				return nil, true, err
			}
			for _, commit := range commits {
				if !commitPattern.MatchString(commit) {
					continue
				}
				r := Release{Owner: owner, Repo: repo, Commit: commit}
				if held, _ := s.Held(r); !held {
					continue
				}
				m, err := s.ReadMark(r)
				if err != nil {
					continue
				}
				at, err := time.Parse(time.RFC3339, m.CommitTime)
				if err != nil {
					continue
				}
				r.Ref, r.Tag = SafeRef(m.Ref), SafeRef(m.Tag)
				bySource[r.Source()] = append(bySource[r.Source()], countedRelease{release: r, at: at})
			}
		}
	}

	for _, releases := range bySource {
		newest := 0
		for i, c := range releases {
			n := releases[newest]
			if c.at.After(n.at) || (c.at.Equal(n.at) && c.release.Commit > n.release.Commit) {
				newest = i
			}
		}
		for i, c := range releases {
			if i != newest {
				candidates = append(candidates, c.release)
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID() < candidates[j].ID() })
	return candidates, true, nil
}

// listDirs lists the names of the real directories in dir, by Lstat
// semantics. A dir that doesn't exist has none; one that can't be listed is
// an error naming it.
func listDirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
