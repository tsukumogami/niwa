package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/mirror"
	"github.com/tsukumogami/niwa/internal/testhook"
)

// borrowEnabled reports whether repo's new clone borrows objects from the
// host mirror. Every borrowing entry point asks this one function, so the
// release gate can't be skipped by a caller that reads the setting itself:
// the share_git_history cascade's answer, ANDed with borrowReleased, which is
// false in every release build.
func borrowEnabled(cfg *config.WorkspaceConfig, repo string) bool {
	return borrowReleased && cfg.ShareGitHistoryEnabled(repo)
}

// The reasons a new clone that was to borrow from its host mirror is made in
// full instead. The create prints one warning per such repo with its reason.
// A repo whose setting turns borrowing off has no reason: it's a full clone
// by the user's choice, and nothing is printed for it.
const (
	// fallbackMirrorBusy: another command held the mirror's lock for the
	// whole wait, at the check or at the clone.
	fallbackMirrorBusy = "mirror-busy"
	// fallbackRestoreFailed: the mirror was broken and couldn't be restored.
	fallbackRestoreFailed = "mirror-restore-failed"
	// fallbackSeedFailed: the repo had no mirror and the first build failed.
	fallbackSeedFailed = "mirror-seed-failed"
	// fallbackBorrowCloneFailed: the borrowing clone failed, left no
	// alternates file, or its rewritten alternates line didn't read back.
	fallbackBorrowCloneFailed = "borrow-clone-failed"
	// fallbackStoreChanged: the mirror's link named another store after the
	// borrowing clone than before it, on the retry as well.
	fallbackStoreChanged = "store-changed"
	// fallbackInvalidRepoID: the repo's name can't name a mirror
	// (mirror.ValidRepoID).
	fallbackInvalidRepoID = "invalid-repo-id"
)

// borrowSource is the host mirror a new clone borrows from.
type borrowSource struct {
	// repoID names the mirror; home and workspaceRoot place it, its lock
	// (mirror.LockPath) and its link (mirror.LinkedStore).
	repoID, home, workspaceRoot string
	// stable is the mirror's stable path (mirror.StablePath). The clone's
	// alternates file holds the one line stable + "/objects".
	stable string
	// lockWait bounds the wait for the mirror's lock, before the test
	// build's override (testhook.MirrorLockWait).
	lockWait time.Duration
}

// borrowChoice is what ensureMirrors decided for one repo's new clone: borrow
// from source, or clone in full with fallback as the reason. A repo with
// neither is a full clone with no notice.
type borrowChoice struct {
	source   *borrowSource
	fallback string
}

// The points of a borrowing clone borrowStage is called at, all while the
// mirror's lock is held.
const (
	// borrowStageLinked: the stable link has just been read, before git runs.
	borrowStageLinked = "linked"
	// borrowStageCloned: git clone exited 0, so its alternates file names the
	// resolved store, and the stable-path line isn't written yet.
	borrowStageCloned = "cloned"
	// borrowStageRewritten: the stable-path line has been renamed into place,
	// before it's read back.
	borrowStageRewritten = "rewritten"
	// borrowStageWritten: the line read back, before the link is read again.
	borrowStageWritten = "written"
)

// borrowStage, when a test sets it, runs at each point named above. The
// tests use it to change the link, the alternates file or the mirror at an
// exact point, and to show what a sweep can see there. It's nil outside them.
var borrowStage func(stage string)

func atBorrowStage(stage string) {
	if borrowStage != nil {
		borrowStage(stage)
	}
}

// cloneNew clones job's repo, borrowing from its host mirror when
// ensureMirrors chose that, and reports whether a clone was made, the
// transient retries the full clone spent, whether the clone borrows, and the
// reason a clone that was to borrow was made in full instead.
//
// A repo directory that already holds a clone is left as it is, borrowing or
// not: only a new clone borrows. A borrowing attempt that fails never fails
// the command. Its clone, when git made one, is removed and the repo is
// cloned in full through cloneWithRetry, the same path a repo that doesn't
// borrow takes; only a failure there fails the command, as it always has.
func (a *Applier) cloneNew(ctx context.Context, job cloneJob, r *Reporter) (cloned bool, retries int, borrowed bool, fallback string, err error) {
	fallback = job.borrow.fallback
	if job.borrow.source != nil && !repoAlreadyCloned(job.targetDir) {
		reason := a.borrowingClone(ctx, job, r)
		if reason == "" {
			return true, 0, true, "", nil
		}
		fallback = reason
	}
	cloned, retries, err = a.cloneWithRetry(ctx, job, r)
	if !cloned {
		// Nothing was cloned in full: the repo was already there, or the
		// clone failed and the command fails with it.
		fallback = ""
	}
	return cloned, retries, false, fallback, err
}

// borrowingClone makes job's new clone borrow from its host mirror, or
// returns the reason it couldn't with no clone left at the target.
//
// It holds the mirror's lock (mirror.LockPath, the lock a restore, a refresh
// and the retired-store sweep take) from reading the stable link until the
// clone's alternates file holds the stable-path line on disk. A restore can't
// swap the store and a sweep can't scan in that time, so the sweep's strict
// scan either finishes before the link is read or sees the new clone, which
// is built at its final path where the strict walk (hostdir.StrictClones)
// looks. The lock is waited for as long as a restore waits; a wait that runs
// out is mirror-busy.
//
// The clone is git clone --reference-if-able through the stable path, which
// fetches only what the mirror lacks and records the store's resolved path
// in the alternates file. That file is then replaced with the one line
// stable + "/objects" and read back (writeStableAlternates), so a clone never
// keeps a line that names a store directly. The link is read before the
// clone and again after the rewrite. When the two differ the clone is
// removed and tried once more, and a second difference is store-changed.
func (a *Applier) borrowingClone(ctx context.Context, job cloneJob, r *Reporter) string {
	src := job.borrow.source
	if !filepath.IsAbs(src.stable) || strings.ContainsAny(src.stable, "\n\r") {
		return fallbackBorrowCloneFailed
	}
	wait := src.lockWait
	if wait <= 0 {
		wait = mirror.DefaultLockWait
	}
	wait = testhook.MirrorLockWait(wait)
	holder := fmt.Sprintf("niwa pid %d cloning %s from its mirror", os.Getpid(), src.repoID)
	release, err := filelock.WaitFileLock(ctx, mirror.LockPath(src.home, src.workspaceRoot, src.repoID), holder, wait)
	if errors.Is(err, filelock.ErrLockHeld) {
		return fallbackMirrorBusy
	}
	if err != nil {
		return fallbackBorrowCloneFailed
	}
	defer release()

	for attempt := 0; ; attempt++ {
		before, err := mirror.LinkedStore(src.home, src.workspaceRoot, src.repoID)
		if err != nil {
			return fallbackBorrowCloneFailed
		}
		atBorrowStage(borrowStageLinked)
		if err := a.borrowOnce(ctx, job, r); err != nil {
			return fallbackBorrowCloneFailed
		}
		atBorrowStage(borrowStageWritten)
		after, err := mirror.LinkedStore(src.home, src.workspaceRoot, src.repoID)
		if err == nil && after == before {
			return ""
		}
		_ = os.RemoveAll(job.targetDir)
		if attempt > 0 {
			return fallbackStoreChanged
		}
	}
}

// borrowOnce runs one borrowing clone of job into its target and rewrites the
// clone's alternates file to the stable-path line. On any failure after git
// made anything, what it made is removed. A target that already holds a
// clone is never removed: git then makes nothing and borrowOnce returns an
// error.
func (a *Applier) borrowOnce(ctx context.Context, job cloneJob, r *Reporter) error {
	src := job.borrow.source
	_, statErr := os.Lstat(job.targetDir)
	existed := statErr == nil
	cloned, err := a.Cloner.CloneWith(ctx, job.cloneURL, job.targetDir, CloneOptions{Ref: job.branch, Reference: src.stable}, r)
	if err == nil && !cloned {
		return errors.New("the repo directory already holds a clone")
	}
	if err == nil {
		atBorrowStage(borrowStageCloned)
		err = testhook.Fault(testhook.LabelBorrowClone)
	}
	if err == nil {
		err = writeStableAlternates(job.targetDir, src.stable)
	}
	if err != nil {
		// The target had no clone before this attempt, so a directory that
		// wasn't there, or a .git in one that was (empty, as git requires),
		// is this attempt's own.
		if !existed || repoAlreadyCloned(job.targetDir) {
			_ = os.RemoveAll(job.targetDir)
		}
		return err
	}
	return nil
}

// writeStableAlternates replaces the alternates file a borrowing clone of
// clone got from git with the one line stable + "/objects", and reads it
// back. The file is written under a temporary name in the same directory,
// synced and renamed over the old one, and the directory is synced, so a
// crash leaves either git's line or the new one, never a partial file. A
// missing alternates file (git found nothing to borrow at the stable path),
// one that isn't a regular file, and a read-back that doesn't match are
// errors.
func writeStableAlternates(clone, stable string) error {
	info := filepath.Join(clone, ".git", "objects", "info")
	path := filepath.Join(info, "alternates")
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("the borrowing clone has no alternates file: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return errors.New("the borrowing clone's alternates is not a regular file")
	}
	want := filepath.Join(stable, "objects") + "\n"

	tmp, err := os.CreateTemp(info, ".alternates-*")
	if err != nil {
		return fmt.Errorf("writing the alternates file: %w", err)
	}
	tmpName := tmp.Name()
	_, werr := tmp.WriteString(want)
	if werr == nil {
		werr = tmp.Chmod(0o644)
	}
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmpName, path)
	}
	if werr != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("writing the alternates file: %w", werr)
	}
	if d, err := os.Open(info); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	atBorrowStage(borrowStageRewritten)

	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading the alternates file back: %w", err)
	}
	if string(got) != want {
		return errors.New("the alternates file read back differs from what was written")
	}
	return nil
}

// fallbackWarning is the warning a create prints for a repo cloned in full
// instead of borrowing: the repo's name, cleaned (mirror.CleanText), and the
// reason. A name that can't name a mirror isn't printed at all, since it can
// hold anything a config can.
func fallbackWarning(repo, reason string) string {
	name := mirror.CleanText(repo)
	if reason == fallbackInvalidRepoID {
		name = "a repo whose name niwa can't mirror"
	}
	return fmt.Sprintf("%s was cloned in full instead of borrowing from the host mirror (%s)", name, reason)
}
