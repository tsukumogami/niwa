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
	// Command names the niwa command in the mirror log: create, apply.
	Command string
}

// Fault is what the check found wrong with a mirror; empty when nothing.
type Fault string

// The faults this package reports.
const (
	FaultNone Fault = ""
	// FaultMissing: no usable link at the stable path, a link whose target
	// isn't a store directory niwa made, or no state file recording that
	// store.
	FaultMissing Fault = "missing"
	// FaultObjectsEmpty: the store's objects/ is gone or holds neither a file
	// under objects/pack/ nor a loose object.
	FaultObjectsEmpty Fault = "objects-empty"
	// FaultPackMissing: a pack the manifest lists, or its .idx, is gone.
	FaultPackMissing Fault = "pack-missing"
	// FaultPackChanged: a pack the manifest lists has another size or
	// trailing checksum, or its ctime moved and git verify-pack fails on it.
	FaultPackChanged Fault = "pack-changed"
	// FaultRepoUnreadable: the store's HEAD or config is gone, or HEAD
	// doesn't resolve to a commit git can read.
	FaultRepoUnreadable Fault = "repo-unreadable"
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

// Mirrored keeps the candidates that have a mirror or should: a state file, a
// link or any entry at the stable path, a store directory of the repo, or a
// borrower among instances whose alternates name the stable path. It only
// reads; a workspace with no borrowers gets nothing written and nothing kept.
func Mirrored(home, workspaceRoot string, instances []string, candidates []Repo) []Repo {
	var kept []Repo
	mdir := mirrorsDir(home, workspaceRoot)
	for _, r := range candidates {
		if !ValidRepoID(r.ID) {
			continue
		}
		stable := StablePath(home, workspaceRoot, r.ID)
		if _, err := os.Lstat(stable); err == nil ||
			hasStateFile(mdir, r.ID) ||
			hasStore(mdir, r.ID) ||
			hasBorrower(instances, stable) {
			kept = append(kept, r)
		}
	}
	return kept
}

// hasStateFile reports whether .meta/ has an entry for repoID's state file.
func hasStateFile(mdir, repoID string) bool {
	_, err := os.Lstat(statePath(mdir, repoID))
	return err == nil
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

// ensureOne checks one mirror, restores it when the check finds a fault, and
// logs a check line and, after a restore, a restore line. Both lines carry
// the final result, so the check line of a mirror that was restored says
// "restored", and each line's time is when its event started. Both are
// written once the restore is over. A failed log write is dropped: it never
// changes the Result.
func ensureOne(ctx context.Context, opts Options, r Repo) Result {
	mdir := mirrorsDir(opts.Home, opts.WorkspaceRoot)
	res := Result{RepoID: r.ID, StablePath: filepath.Join(mdir, r.ID)}
	start := time.Now()
	res.Fault = check(ctx, mdir, r.ID)
	res.CheckDuration = time.Since(start)
	checkLine := logLine{
		Time:    logTime(start),
		Command: opts.Command,
		Mirror:  r.ID,
		Event:   eventCheck,
		Fault:   res.Fault,
		CheckMs: res.CheckDuration.Milliseconds(),
	}
	if res.Fault == FaultNone {
		res.Outcome = OutcomeHealthy
		checkLine.Result = res.Outcome
		_ = appendLog(mdir, checkLine)
		return res
	}
	restoreStart := time.Now()
	res.Outcome, res.Err = restore(ctx, opts, r)
	took := time.Since(restoreStart).Milliseconds()
	checkLine.Result = res.Outcome
	restoreLine := checkLine
	restoreLine.Time = logTime(restoreStart)
	restoreLine.Event = eventRestore
	restoreLine.TookMs = &took
	_ = appendLog(mdir, checkLine, restoreLine)
	return res
}
