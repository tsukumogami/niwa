package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// The per-instance lock.
//
// Several commands decide something about an instance and then act on it:
// resume checks the session isn't running and launches into the instance,
// destroy checks nothing needs the instance and removes its session and its
// directory, the reaper reads a session as gone and destroys its instance, and
// watch's discard reads a review as dead or stale and destroys it. Each one
// takes the instance's lock before the check that justifies its action and
// holds it until the action is done, so no other niwa command can act on the
// same instance in between. Trying the lock and releasing it before acting
// would not do: the lock is free again the moment it's released.
//
// The lock is taken at command level only, never inside destroyInstanceFunc,
// removeOwningSessionsListed, workspace.DestroyInstance or any stop helper.
// A command that destroys what it just restored (resume's rollback) runs that
// destroy under its own hold; taking the lock in the seam would make it block
// on itself.
//
// The attempt is a single non-blocking try. Nobody waits: a command that finds
// the lock held refuses (resume, destroy) or spares the instance for this pass
// (reap, watch's discard), naming the holder.

// tryInstanceLockFunc takes the lock on instancePath for holder, returning the
// release. A seam so a test can report the lock held or unsupported, or run a
// hook when it is granted or released.
var tryInstanceLockFunc = tryInstanceLock

// tryInstanceLock is the production lock: workspace.TryFileLock on the
// instance's lock file under niwa's lock directory in the home directory.
func tryInstanceLock(instancePath, holder string) (func(), error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil, errors.New("the home directory can't be resolved, so the instance's lock can't be taken")
	}
	return workspace.TryFileLock(workspace.InstanceLockPath(home, instancePath), holder)
}

// instanceLockHolder is the line a holder records in the lock file, which a
// refused taker reports: the command and this process's id.
func instanceLockHolder(command string) string {
	return fmt.Sprintf("niwa %s (pid %d)", command, os.Getpid())
}

// takeInstanceLock takes instancePath's lock for command and returns the
// release, which is safe to call once.
//
// On a platform without flock (workspace.ErrLockUnsupported) it returns a
// release that does nothing and no error: the command goes on unlocked, as it
// did before the lock existed. Any other error, a held lock included, is
// returned for the caller to refuse or spare on; instanceLockReason words it.
func takeInstanceLock(instancePath, command string) (func(), error) {
	release, err := tryInstanceLockFunc(instancePath, instanceLockHolder(command))
	if errors.Is(err, workspace.ErrLockUnsupported) {
		return func() {}, nil
	}
	if err != nil {
		return nil, err
	}
	if release == nil {
		release = func() {}
	}
	return release, nil
}

// reportInstanceLockKept prints the line a pass that spares an instance on its
// lock prints, in the shape the removal gate's spare lines have.
func reportInstanceLockKept(w io.Writer, instancePath string, err error) {
	fmt.Fprintf(w, "niwa: kept %s: %s\n", sanitizeRemovalGateText(filepath.Base(instancePath), 80), instanceLockReason(err))
}

// instanceLockReason says why the lock wasn't taken, in words that fit after
// "cannot resume ...:" or "kept <name>:". A held lock names its holder, which
// workspace.TryFileLock puts in its error ("another niwa command" when the
// holder hasn't written its line yet).
func instanceLockReason(err error) string {
	if errors.Is(err, workspace.ErrLockHeld) {
		return fmt.Sprintf("another niwa command is acting on it (instance %v)", err)
	}
	return fmt.Sprintf("could not take its instance lock: %v", err)
}
