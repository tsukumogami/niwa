package mirror

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/tsukumogami/niwa/internal/hostdir"
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
	// caller so this package never enumerates instances itself: every clone
	// on disk (hostdir.Clones), kept and half-created instances included.
	// The check, the restore's probe and Recorded read it; nothing is ever
	// removed on its strength.
	Instances []string
	// Scan is the strict walk of every clone on disk (hostdir.StrictClones),
	// called under the mirror's lock at the moment a store's removal is
	// decided, so its answer is never older than the lock. It's the only
	// list that can authorize removing a retired store. A nil Scan removes
	// nothing that was ever linked, and an error from it removes nothing.
	Scan func() ([]string, error)
	// LockWait bounds the wait for a mirror's lock; zero means
	// DefaultLockWait.
	LockWait time.Duration
	// Command names the niwa command in the mirror log: create, apply,
	// dispatch, from-hook (the SessionStart hook) or worktree.
	Command string
	// Refresh brings a mirror the check finds healthy up to date from the
	// remote, pruning nothing. The commands that build instances set it; the
	// ones that only check, such as the hook that provisions nothing and
	// niwa worktree create, never fetch.
	Refresh bool
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
	// FaultRepoUnreadable: the store's HEAD or config is gone or isn't a
	// regular file, its packed-refs isn't a regular file, or HEAD doesn't
	// resolve to a commit git can read (git refuses to run, or refuses the
	// store's repository format version).
	FaultRepoUnreadable Fault = "repo-unreadable"
	// FaultURLChanged: the workspace config now names another remote than
	// the one the state file records the store was cloned from.
	FaultURLChanged Fault = "url-changed"
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
	// Unrecoverable are the borrower refs a restore left unreadable: built
	// on commits the remote deleted after they were pushed, which the lost
	// mirror held and the remote no longer serves. They don't make the
	// restore a failure, and the next command finds the mirror healthy and
	// doesn't report them again.
	Unrecoverable []LostRef
	// LogErr is the first error writing this mirror's lines to the mirror
	// log, healthy results included. Every other field is what it would be
	// with a working log: a log that can't be written is reported, never a
	// reason to change what the check or restore did.
	LogErr error
}

// elapsedNow is the clock the durations a Result and the log carry are read
// from: check_ms, a restore's took_ms and a refresh's took_ms. Nothing else
// reads it; the log's time field, store names and the state file's
// timestamps read time.Now. The tests replace it with a fake clock to get
// exact figures without timing anything.
var elapsedNow = time.Now

// Usable reports whether borrowers can read the mirror after the call.
func (r Result) Usable() bool {
	return r.Outcome == OutcomeHealthy || r.Outcome == OutcomeRestored
}

// noticeEffects says, per outcome, what a notice tells the reader the result
// means for the clones that borrow from the mirror.
var noticeEffects = map[Outcome]string{
	OutcomeRestored:      "It was rebuilt from the remote, and the clones that borrow from it read normally.",
	OutcomeRestoreFailed: "It could not be rebuilt, so clones that borrow from it may fail to read objects until a later niwa command restores it.",
	OutcomeBusy:          "Another niwa command held it for the whole wait, so nothing was changed; the next niwa command checks it again.",
}

// Notice is the one line a command shows a reader who can't see its stderr,
// such as a session that gets the SessionStart hook's context: the repo id,
// the fault, the outcome, how many borrower refs the restore left
// unrecoverable when there are any, and where the mirror log is. When the
// log couldn't be written (LogErr), the line says so in place of naming the
// log, so a failed write reaches that reader as a warning does on stderr.
// It's "" for a healthy mirror whose log was written. It's built from those
// values and fixed text only, never from Err or LogErr, which can carry text
// git, the system or the remote produced, nor from a lost ref's names, which
// a remote or a person chose: the count says the mirror log names them, and
// when the log couldn't be written it says only the count, since the log
// names nothing. Any control character in it is dropped, so a hostile home
// path can't break the line.
func (r Result) Notice() string {
	healthy := r.Outcome == OutcomeHealthy || r.Outcome == ""
	if healthy && r.LogErr == nil {
		return ""
	}
	var line string
	if healthy {
		line = fmt.Sprintf("niwa mirror %s: result healthy.", r.RepoID)
	} else {
		line = fmt.Sprintf("niwa mirror %s: fault %s, result %s.", r.RepoID, r.Fault, r.Outcome)
	}
	if effect := noticeEffects[r.Outcome]; effect != "" {
		line += " " + effect
	}
	switch n := len(r.Unrecoverable); {
	case n == 1:
		line += " Except 1 ref in those clones, which is unrecoverable: it was built on commits the remote no longer serves."
		if r.LogErr == nil {
			line += " The mirror log names it."
		}
	case n > 1:
		line += fmt.Sprintf(" Except %d refs in those clones, which are unrecoverable: they were built on commits the remote no longer serves.", n)
		if r.LogErr == nil {
			line += " The mirror log names them."
		}
	}
	if r.StablePath != "" {
		log := logPath(filepath.Dir(r.StablePath))
		if r.LogErr != nil {
			line += " The mirror log at " + log + " could not be written."
		} else {
			line += " Mirror log: " + log
		}
	}
	return strings.Map(func(c rune) rune {
		if unicode.IsControl(c) {
			return -1
		}
		return c
	}, line)
}

// storeIDPattern takes the repo id out of a store directory's name. The id is
// matched greedily, so "app-extra-<stamp>-<rand>" gives "app-extra".
var storeIDPattern = regexp.MustCompile(`^(.+)-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)

// Recorded lists the repo ids that have a mirror or should, found from what's
// on disk alone, for a caller that has no repo list of its own: a state file
// or a store record in .meta/, an entry at a stable path, a store directory
// in .store/, or a stable path that the alternates of one of clones names.
// Only ids ValidRepoID accepts are returned, sorted. It only reads: a
// workspace with no mirror directory gets nothing returned and nothing
// written.
func Recorded(home, workspaceRoot string, clones []string) []string {
	return recordedIn(mirrorsDir(home, workspaceRoot), clones)
}

// recordedIn is Recorded for the mirrors directory mdir, so the teardown can
// list ids once the workspace root its path came from is gone.
func recordedIn(mdir string, clones []string) []string {
	found := map[string]bool{}
	add := func(id string) {
		if ValidRepoID(id) {
			found[id] = true
		}
	}

	if entries, err := os.ReadDir(metaDir(mdir)); err == nil {
		for _, e := range entries {
			// A leftover temporary state file or record is
			// ".<id>.json-<rand>" or ".<id>.stores-<rand>", which has neither
			// suffix and starts with a dot ValidRepoID rejects.
			if id, ok := strings.CutSuffix(e.Name(), ".json"); ok {
				add(id)
			}
			if id, ok := strings.CutSuffix(e.Name(), ".stores"); ok {
				add(id)
			}
		}
	}
	if entries, err := os.ReadDir(mdir); err == nil {
		// niwa's own entries (.meta, .store, .tmp, .link-*) start with a dot,
		// which no repo id does.
		for _, e := range entries {
			add(e.Name())
		}
	}
	if entries, err := os.ReadDir(filepath.Join(mdir, ".store")); err == nil {
		for _, e := range entries {
			if m := storeIDPattern.FindStringSubmatch(e.Name()); m != nil {
				add(m[1])
			}
		}
	}
	for _, clone := range clones {
		for _, line := range alternatesLines(filepath.Join(clone, ".git", "objects", "info", "alternates")) {
			if filepath.Base(line) == "objects" && filepath.Dir(filepath.Dir(line)) == mdir {
				add(filepath.Base(filepath.Dir(line)))
			}
		}
	}

	ids := make([]string, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
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
// stable's objects directory. The file niwa dissociate sets aside while it
// checks a clone (hostdir.AlternatesAside) counts too: until the dissociate
// removes it, the clone may still need what it names, and a run interrupted
// there puts it back when its check fails.
func hasBorrower(clones []string, stable string) bool {
	want := filepath.Join(stable, "objects")
	for _, clone := range clones {
		info := filepath.Join(clone, ".git", "objects", "info")
		if alternatesName(filepath.Join(info, "alternates"), want) ||
			alternatesName(filepath.Join(info, hostdir.AlternatesAside), want) {
			return true
		}
	}
	return false
}

// BorrowsFrom reports whether any of clones borrows from repoID's mirror
// through its stable path (hasBorrower). A line naming a store directly
// doesn't count. A repo with borrowing off keeps its mirror checked only
// while this holds (PRD R6).
func BorrowsFrom(clones []string, home, workspaceRoot, repoID string) bool {
	return hasBorrower(clones, StablePath(home, workspaceRoot, repoID))
}

// alternatesName reports whether the alternates file at path has a line equal
// to want once cleaned. A missing or unreadable file names nothing.
func alternatesName(path, want string) bool {
	return slices.Contains(alternatesLines(path), want)
}

// alternatesLines returns the non-empty lines of the alternates file at path,
// each cleaned. A missing or unreadable file has none.
func alternatesLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(io.LimitReader(f, alternatesMax))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			lines = append(lines, filepath.Clean(line))
		}
	}
	return lines
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

// Seed builds repo's first mirror, before any clone borrows from it, and
// returns the same kind of Result a check gives: a caller clones through the
// mirror only when Usable() is true, and in full otherwise.
//
// A repo whose id ValidRepoID rejects, or whose URL is empty, can't take a
// mirror. Seed refuses it before it touches the disk and before any check:
// the Result has fault missing, outcome restore-failed and Err set,
// StablePath is empty for a rejected id, and nothing is made under ~/.niwa/.
// So a repo that already has a mirror but whose URL is empty gets
// restore-failed without its mirror being looked at.
//
// Otherwise Seed does what EnsureHealthy does for one repo, without the
// Mirrored filter that keeps EnsureHealthy from building a mirror no clone
// borrows yet. A first build is a restore of a mirror that's missing: it runs
// under the mirror's lock, clones into .tmp/ with the fixed config and writes
// the state file under a temporary name before the link is swapped in, so a
// Seed that fails before the swap leaves nothing behind and the next Seed or
// EnsureHealthy tries again, and a second Seed racing the first finds the
// mirror healthy and builds nothing. A failure after the swap, at recording
// the state file, returns restored with Err set and leaves the new link in
// place: the next check finds no state file naming the linked store,
// rebuilds, and records what it builds. Seed doesn't refresh what it just
// cloned, even with opts.Refresh set. On a repo that already has a mirror
// and a URL, Seed returns what EnsureHealthy returns for it.
//
// Seed writes nothing in any clone, alternates included: pointing a new clone
// at StablePath(...) + "/objects" is the caller's job, never
// git clone --reference, which records the store's resolved path and breaks
// once a restore replaces that store.
func Seed(ctx context.Context, opts Options, repo Repo) Result {
	if !ValidRepoID(repo.ID) {
		return Result{
			RepoID:  repo.ID,
			Fault:   FaultMissing,
			Outcome: OutcomeRestoreFailed,
			Err:     fmt.Errorf("repo id %q can't name a mirror", repo.ID),
		}
	}
	if repo.URL == "" {
		return Result{
			RepoID:     repo.ID,
			StablePath: StablePath(opts.Home, opts.WorkspaceRoot, repo.ID),
			Fault:      FaultMissing,
			Outcome:    OutcomeRestoreFailed,
			Err:        errors.New("the workspace config gives no URL to build the mirror from"),
		}
	}
	return ensureOne(ctx, opts, repo)
}

// ensureOne checks one mirror, restores it when the check finds a fault, and
// logs a check line and, after a restore, a restore line. Both lines carry
// the final result, so the check line of a mirror that was restored says
// "restored", and each line's time is when its event started. Both are
// written once the restore is over. A restore that found, once it held the
// lock, that another command had already fixed the mirror restored nothing
// and logs no restore line: its check line says the fault it saw and
// "healthy". Each borrower ref the restore left unrecoverable gets an
// unrecoverable line after the restore line. The restore line lists the
// borrowers the restore's probe read (probed), and each store the restore
// removed or kept on the way gets a store line after the rest. A failed log
// write is kept as the Result's LogErr and changes nothing else in it.
//
// A mirror the check finds healthy is refreshed when opts.Refresh is set,
// once its check line is written (see refresh). A mirror this call restored
// isn't: the restore's clone has just fetched.
//
// check_ms times the check alone, read from elapsedNow just before and just
// after it; a restore's took_ms is read the same way around the restore,
// its probe of the borrowers included.
//
// Before the check it prepares ~/.niwa/empty-hooks (prepareEmptyHooks) and
// puts its path on the context every git call of this mirror runs under,
// then tightens the modes of niwa's own entries for the mirror
// (tightenModes). An empty-hooks directory that can't be made safe isn't an
// error here: gitRun checks it again on every call and refuses to run git,
// so the check finds the store unreadable and the restore can't clone.
//
// Last, for every outcome but busy, it sweeps the mirror's retired stores
// (sweep) and logs what the sweep removed or kept after the call's other
// lines, so a store line never comes before the restore line whose probe it
// followed.
func ensureOne(ctx context.Context, opts Options, r Repo) Result {
	res := checkAndRestore(ctx, opts, r)
	if res.Outcome == OutcomeBusy {
		return res
	}
	mdir := mirrorsDir(opts.Home, opts.WorkspaceRoot)
	base := logLine{Command: opts.Command, Mirror: r.ID}
	if lines := sweep(opts, mdir, r, base); len(lines) > 0 {
		if err := appendLog(mdir, lines...); err != nil && res.LogErr == nil {
			res.LogErr = err
		}
	}
	return res
}

// checkAndRestore is ensureOne's check, restore and refresh of one mirror,
// before the sweep.
func checkAndRestore(ctx context.Context, opts Options, r Repo) Result {
	_ = prepareEmptyHooks(opts.Home)
	ctx = withEmptyHooks(ctx, emptyHooksDir(opts.Home))
	ctx = withNetworkNotes(ctx)
	tightenModes(opts.Home, opts.WorkspaceRoot, r.ID)
	mdir := mirrorsDir(opts.Home, opts.WorkspaceRoot)
	res := Result{RepoID: r.ID, StablePath: filepath.Join(mdir, r.ID)}
	logged := func(err error) {
		if err != nil && res.LogErr == nil {
			res.LogErr = err
		}
	}
	start := time.Now()
	checkStart := elapsedNow()
	res.Fault = check(ctx, mdir, r)
	res.CheckDuration = elapsedNow().Sub(checkStart)
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
		logged(appendLog(mdir, checkLine))
		clearLeftoversIfIdle(opts, mdir, r.ID)
		if opts.Refresh {
			return refresh(ctx, opts, mdir, r, res)
		}
		return res
	}
	restoreStart := time.Now()
	tookStart := elapsedNow()
	rb := restore(ctx, opts, r)
	res.Outcome, res.Unrecoverable, res.Err = rb.outcome, rb.lost, rb.err
	took := elapsedNow().Sub(tookStart).Milliseconds()
	checkLine.Result = res.Outcome
	if res.Outcome == OutcomeHealthy {
		logged(appendLog(mdir, checkLine))
		return res
	}
	restoreLine := checkLine
	restoreLine.Time = logTime(restoreStart)
	restoreLine.Event = eventRestore
	restoreLine.TookMs = &took
	restoreLine.SalvagedFrom = rb.salvaged
	restoreLine.NetworkNotes = networkNotesFrom(ctx)
	restoreLine.Probed = rb.probed
	lines := []any{checkLine, restoreLine}
	lines = append(lines, unrecoverableLines(restoreLine, res.Unrecoverable)...)
	logged(appendLog(mdir, append(lines, storeLines(restoreLine, rb.events)...)...))
	return res
}
