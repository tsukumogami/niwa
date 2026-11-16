package mirror

import (
	"bufio"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultLockWait is how long a restore waits for another command's restore
// of the same mirror before it reports the mirror busy and touches nothing.
const DefaultLockWait = 5 * time.Minute

// alternatesMax bounds how much of a clone's alternates file is read when
// looking for borrowers. A real one holds a line or two.
const alternatesMax = 64 << 10

// Repo is one repo a call site may hold a mirror of. ID is the repo's name as
// the workspace config keys it; URL is the clone URL the config gives, the
// only URL a restore ever clones from.
type Repo struct{ ID, URL string }

// Options says where the mirrors are and who borrows from them.
type Options struct {
	// Home is the user's home directory; mirrors live under Home/.niwa.
	Home string
	// WorkspaceRoot keys the mirrors: each workspace has its own.
	WorkspaceRoot string
	// Instances are the instance clone paths that may borrow, listed by the
	// caller so this package never enumerates instances itself.
	Instances []string
	// LockWait bounds the wait for a mirror's lock; zero means
	// DefaultLockWait.
	LockWait time.Duration
}

// Fault is what the check found wrong with a mirror; empty when nothing.
type Fault string

// The faults this package reports.
const (
	FaultNone Fault = ""
	// FaultMissing: no usable link at the stable path, or a link whose target
	// isn't a store directory niwa made.
	FaultMissing Fault = "missing"
)

// Outcome is what EnsureHealthy did about a mirror.
type Outcome string

// The outcomes EnsureHealthy reports.
const (
	OutcomeHealthy       Outcome = "healthy"
	OutcomeRestored      Outcome = "restored"
	OutcomeRestoreFailed Outcome = "restore-failed"
	// OutcomeBusy: another command held the mirror's lock for the whole
	// wait, so nothing was touched.
	OutcomeBusy Outcome = "busy"
)

// Result is one mirror's check and what followed it.
type Result struct {
	RepoID, StablePath string
	Fault              Fault
	Outcome            Outcome
	Err                error
	CheckDuration      time.Duration
}

// Usable reports whether borrowers can read the mirror after the call.
func (r Result) Usable() bool {
	return r.Outcome == OutcomeHealthy || r.Outcome == OutcomeRestored
}

// Mirrored keeps the candidates that have a mirror or should: a link or any
// entry at the stable path, a store directory of the repo, or a borrower among
// instances whose alternates name the stable path. It only reads; a workspace
// with no borrowers gets nothing written and nothing kept.
func Mirrored(home, workspaceRoot string, instances []string, candidates []Repo) []Repo {
	var kept []Repo
	for _, r := range candidates {
		if !ValidRepoID(r.ID) {
			continue
		}
		stable := StablePath(home, workspaceRoot, r.ID)
		if _, err := os.Lstat(stable); err == nil ||
			hasStore(mirrorsDir(home, workspaceRoot), r.ID) ||
			hasBorrower(instances, stable) {
			kept = append(kept, r)
		}
	}
	return kept
}

// hasStore reports whether .store/ holds a store directory of repoID.
func hasStore(mdir, repoID string) bool {
	entries, err := os.ReadDir(filepath.Join(mdir, ".store"))
	if err != nil {
		return false
	}
	re := storeNamePattern(repoID)
	for _, e := range entries {
		if re.MatchString(e.Name()) {
			return true
		}
	}
	return false
}

// hasBorrower reports whether any clone's alternates file has a line naming
// stable's objects directory.
func hasBorrower(clones []string, stable string) bool {
	want := filepath.Join(stable, "objects")
	for _, clone := range clones {
		if alternatesName(filepath.Join(clone, ".git", "objects", "info", "alternates"), want) {
			return true
		}
	}
	return false
}

// alternatesName reports whether the alternates file at path has a line equal
// to want once cleaned. A missing or unreadable file names nothing.
func alternatesName(path, want string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, alternatesMax))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" && filepath.Clean(line) == want {
			return true
		}
	}
	return false
}

// EnsureHealthy checks each of repos that has a mirror (see Mirrored) and
// restores any that's broken. It returns one Result per mirror it checked.
// Nothing it returns should fail the caller's command: a mirror that can't be
// restored is reported, and the command carries on.
func EnsureHealthy(ctx context.Context, opts Options, repos []Repo) []Result {
	var results []Result
	for _, r := range Mirrored(opts.Home, opts.WorkspaceRoot, opts.Instances, repos) {
		results = append(results, ensureOne(ctx, opts, r))
	}
	return results
}

func ensureOne(ctx context.Context, opts Options, r Repo) Result {
	mdir := mirrorsDir(opts.Home, opts.WorkspaceRoot)
	res := Result{RepoID: r.ID, StablePath: filepath.Join(mdir, r.ID)}
	start := time.Now()
	res.Fault = check(mdir, r.ID)
	res.CheckDuration = time.Since(start)
	if res.Fault == FaultNone {
		res.Outcome = OutcomeHealthy
		return res
	}
	res.Outcome, res.Err = restore(ctx, opts, r)
	return res
}

// check runs the offline check of one mirror. It reads local files only.
func check(mdir, repoID string) Fault {
	stable := filepath.Join(mdir, repoID)
	info, err := os.Lstat(stable)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return FaultMissing
	}
	target, err := os.Readlink(stable)
	if err != nil || !validLinkTarget(repoID, target) {
		return FaultMissing
	}
	st, err := os.Lstat(filepath.Join(mdir, target))
	if err != nil || !st.IsDir() || !ownedByUser(st) {
		return FaultMissing
	}
	return FaultNone
}
