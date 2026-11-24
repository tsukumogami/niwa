package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/removal"
)

// The pass lists Claude Code's sessions for every unit with a mapped Claude
// session. A test that doesn't stand in for the listing gets one that lists
// nothing, the answer Claude Code gives once every session is deleted, so no
// test reaches a claude binary through it. A test that wants the real lister
// sets it explicitly, as before.
func init() {
	listClaudeSessionsFunc = func(context.Context) ([]claudeSessionRow, error) { return nil, nil }
}

// vetHome points HOME at a fresh directory with ~/.claude/jobs/<id> in it,
// every directory 0700, holding a state file and scratch/out, and clears
// CLAUDE_CONFIG_DIR. It returns the home, the jobs root and the job
// directory.
func vetHome(t *testing.T, id string) (home, jobs, jobDir string) {
	t.Helper()
	home = canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	jobs = filepath.Join(home, ".claude", "jobs")
	jobDir = filepath.Join(jobs, id)
	if err := os.MkdirAll(filepath.Join(jobDir, "scratch"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(home, ".claude"), jobs, jobDir, filepath.Join(jobDir, "scratch")} {
		if err := os.Chmod(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(jobDir, "state.json"), filepath.Join(jobDir, "scratch", "out")} {
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, jobs, jobDir
}

// sessionJobDir creates the job directory of the session Claude Code lists
// under short, 0700, under the jobs root the test's HOME and
// CLAUDE_CONFIG_DIR give, as Claude Code keeps one for every listed
// session. destroy vets it before it passes the session to claude rm.
func sessionJobDir(t *testing.T, short string) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := removal.ResolveJobsRoot(os.LookupEnv, home)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(jobs, short)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{jobs, dir} {
		if err := os.Chmod(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// innerLink replaces jobDir/scratch/out with a link to a directory outside
// the home holding a marker file, and returns the link's path and the
// marker's.
func innerLink(t *testing.T, jobDir string) (link, marker string) {
	t.Helper()
	outside := filepath.Join(canonicalTempDir(t), "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	marker = filepath.Join(outside, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link = filepath.Join(jobDir, "scratch", "out")
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	return link, marker
}

// loggingClaude puts on PATH, alone, a claude that appends "<cwd>|<args>"
// to a log for every call, prints out and exits with code. It returns the
// log's path.
func loggingClaude(t *testing.T, out string, code int) string {
	t.Helper()
	dir := canonicalTempDir(t)
	log := filepath.Join(dir, "claude.log")
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s|%%s\\n' \"$(pwd)\" \"$*\" >> '%s'\nprintf '%%s\\n' '%s' >&2\nexit %d\n", log, out, code)
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return log
}

// names reports whether printed text names path as niwa prints it: through
// removal.ScrubEvidence, which can redact a test directory's random part.
func names(text, path string) bool {
	return strings.Contains(text, path) || strings.Contains(text, removal.ScrubEvidence(path, 400))
}

func claudeLog(t *testing.T, log string) []string {
	t.Helper()
	b, err := os.ReadFile(log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestDecodeClaudeRowsIsStrict(t *testing.T) {
	for _, bad := range []string{"", "null", "{}", "[null]", "[1]", `["x"]`, "[] []", "[]x", `[{"id":1}]`, `[{"pid":"x"}]`} {
		if rows, err := decodeClaudeRows([]byte(bad)); err == nil {
			t.Errorf("decoded %q as %+v", bad, rows)
		}
	}
	rows, err := decodeClaudeRows([]byte(" [] \n"))
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("an empty listing: %+v %v", rows, err)
	}
	rows, err = decodeClaudeRows([]byte(`[{"id":"abcdef12","sessionId":"s","kind":"background","state":"done"}]`))
	if err != nil || len(rows) != 1 || rows[0].ID != "abcdef12" {
		t.Fatalf("a listing with fields niwa doesn't read: %+v %v", rows, err)
	}
}

func TestRealRemoveClaudeSessionRefusesWithoutItsCheckedDirectory(t *testing.T) {
	log := loggingClaude(t, "", 0)
	if _, err := realRemoveClaudeSession(context.Background(), "abcdef12"); err == nil {
		t.Fatal("claude rm ran with no checked directory to run from")
	}
	if got := claudeLog(t, log); got != nil {
		t.Fatalf("claude ran: %q", got)
	}
}

// abcdefSID is the full session id of the session Claude Code lists as
// abcdef12.
const abcdefSID = "abcdef12-0000-4000-8000-000000000000"

// listedForRemoval stands in for the listing realRemoveClaudeSession takes
// right before claude rm: it returns rows, or err, and fails the test when
// the call carries no checked directory.
func listedForRemoval(t *testing.T, err error, rows ...claudeSessionRow) {
	t.Helper()
	prev := listClaudeSessionsFunc
	listClaudeSessionsFunc = func(ctx context.Context) ([]claudeSessionRow, error) {
		if claudeCallsFrom(ctx) == nil {
			t.Error("the sessions were listed without the checked directory")
		}
		return rows, err
	}
	t.Cleanup(func() { listClaudeSessionsFunc = prev })
}

func TestRealRemoveClaudeSessionVetsThenRunsFromTheScratchDirectory(t *testing.T) {
	home, _, jobDir := vetHome(t, "abcdef12")
	listedForRemoval(t, nil, claudeSessionRow{ID: "abcdef12", SessionID: abcdefSID})
	log := loggingClaude(t, "removed", 0)
	root := canonicalTempDir(t)
	cc := newClaudeCalls(root)
	defer cc.close()
	ctx := withRemovalSession(withClaudeCalls(context.Background(), cc), abcdefSID)

	if _, err := realRemoveClaudeSession(ctx, "abcdef12"); err != nil {
		t.Fatalf("claude rm on a clean job directory: %v", err)
	}
	calls := claudeLog(t, log)
	if len(calls) != 1 {
		t.Fatalf("claude calls %q; want one", calls)
	}
	cwd, args, _ := strings.Cut(calls[0], "|")
	if args != "rm abcdef12" {
		t.Errorf("claude was passed %q", args)
	}
	if !strings.HasPrefix(cwd, filepath.Join(home, ".niwa")+string(filepath.Separator)) || filepath.Base(cwd) != "claude" || !strings.Contains(cwd, string(filepath.Separator)+"scratch"+string(filepath.Separator)) {
		t.Errorf("claude ran from %s; want the scratch claude directory", cwd)
	}

	link, marker := innerLink(t, jobDir)
	_, err := realRemoveClaudeSession(ctx, "abcdef12")
	if err == nil || !strings.Contains(err.Error(), link) {
		t.Fatalf("claude rm with a link in the job directory: %v; want the link named", err)
	}
	if got := claudeLog(t, log); len(got) != 1 {
		t.Fatalf("claude ran after a failing vet: %q", got)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the link's target changed: %v", err)
	}
}

func TestRealRemoveClaudeSessionReportsClaudesMessage(t *testing.T) {
	vetHome(t, "abcdef12")
	listedForRemoval(t, nil, claudeSessionRow{ID: "abcdef12", SessionID: abcdefSID})
	loggingClaude(t, "error: the worktree of the session holds changes", 1)
	cc := newClaudeCalls(canonicalTempDir(t))
	defer cc.close()
	_, err := realRemoveClaudeSession(withRemovalSession(withClaudeCalls(context.Background(), cc), abcdefSID), "abcdef12")
	if err == nil || !strings.Contains(err.Error(), "the worktree of the session holds changes") {
		t.Fatalf("a failing claude rm: %v; want Claude Code's message", err)
	}
}

func TestRealRemoveClaudeSessionRefusesAnInvalidConfigDirectory(t *testing.T) {
	vetHome(t, "abcdef12")
	log := loggingClaude(t, "", 0)
	for _, v := range []string{"relative/cfg", "/tmp/../cfg", "/tmp/cfg/", "~/cfg"} {
		t.Setenv("CLAUDE_CONFIG_DIR", v)
		cc := newClaudeCalls(canonicalTempDir(t))
		_, err := realRemoveClaudeSession(withClaudeCalls(context.Background(), cc), "abcdef12")
		cc.close()
		if err == nil || !strings.Contains(err.Error(), "CLAUDE_CONFIG_DIR") {
			t.Errorf("CLAUDE_CONFIG_DIR=%q: %v; want the variable named", v, err)
		}
	}
	if got := claudeLog(t, log); got != nil {
		t.Fatalf("claude ran: %q", got)
	}
}

// Right before claude rm, realRemoveClaudeSession lists the sessions again:
// a session that now reads running, one the listing no longer holds, one
// listed twice under its id, and a listing that can't be read each refuse,
// with no claude call, though the job directory is clean.
func TestRealRemoveClaudeSessionChecksTheListingAgainRightBeforeClaudeRm(t *testing.T) {
	stopped := claudeSessionRow{ID: "abcdef12", SessionID: abcdefSID}
	running := stopped
	running.PID = 4242
	resumed := stopped
	resumed.Status = "busy"
	other := claudeSessionRow{ID: "abcdef12", SessionID: "abcdef12-1111-4111-8111-111111111111"}
	cases := []struct {
		name string
		rows []claudeSessionRow
		err  error
		want string
	}{
		{"a live pid", []claudeSessionRow{running}, nil, "still running"},
		{"a status", []claudeSessionRow{resumed}, nil, "still running"},
		{"gone from the listing", nil, nil, "no longer lists"},
		{"listed twice under its id", []claudeSessionRow{stopped, other}, nil, "more than one session"},
		{"a listing that fails", nil, errors.New("claude agents: exit status 2"), "could not be listed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vetHome(t, "abcdef12")
			listedForRemoval(t, tc.err, tc.rows...)
			log := loggingClaude(t, "removed", 0)
			cc := newClaudeCalls(canonicalTempDir(t))
			defer cc.close()
			_, err := realRemoveClaudeSession(withRemovalSession(withClaudeCalls(context.Background(), cc), abcdefSID), "abcdef12")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want it to say %q", err, tc.want)
			}
			if got := claudeLog(t, log); got != nil {
				t.Fatalf("claude ran: %q", got)
			}
		})
	}
}

// The vet finds the job directory by the full session id, the id the rule
// walks, not the short id claude rm takes: with only a group-writable
// directory under the full id, both the vet before the first removal step
// and the one inside realRemoveClaudeSession check the full id's and keep,
// with no claude call.
func TestTheVetFindsTheJobDirectoryByTheFullSessionID(t *testing.T) {
	_, jobs, short := vetHome(t, "abcdef12")
	if err := os.RemoveAll(short); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(jobs, abcdefSID)
	if err := os.Mkdir(full, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(full, 0o757); err != nil {
		t.Fatal(err)
	}
	row := claudeSessionRow{ID: "abcdef12", SessionID: abcdefSID}
	listedForRemoval(t, nil, row)
	log := loggingClaude(t, "removed", 0)
	cc := newClaudeCalls(canonicalTempDir(t))
	defer cc.close()
	ctx := withRemovalSession(withClaudeCalls(context.Background(), cc), abcdefSID)

	if err := vetBeforeRemoval(ctx, cc, []claudeSessionRow{row}); err == nil || !names(err.Error(), full) {
		t.Fatalf("the vet before removal: %v; want %s named", err, full)
	}
	if _, err := realRemoveClaudeSession(ctx, "abcdef12"); err == nil || !names(err.Error(), full) {
		t.Fatalf("realRemoveClaudeSession: %v; want %s named", err, full)
	}
	if got := claudeLog(t, log); got != nil {
		t.Fatalf("claude ran: %q", got)
	}
}

// Claude Code writes the job directory under the short id. With a link in
// it and an empty directory under the full id beside it, which anything
// running as the person can make, both vets keep, naming the jobs root,
// with no claude call, rather than pass the empty directory while claude rm
// deletes the short id's.
func TestTheVetRefusesAFullIDDirectoryBesideTheShortIDOne(t *testing.T) {
	_, jobs, short := vetHome(t, "abcdef12")
	_, marker := innerLink(t, short)
	decoy := filepath.Join(jobs, abcdefSID)
	if err := os.Mkdir(decoy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(decoy, 0o700); err != nil {
		t.Fatal(err)
	}
	row := claudeSessionRow{ID: "abcdef12", SessionID: abcdefSID}
	listedForRemoval(t, nil, row)
	log := loggingClaude(t, "removed", 0)
	cc := newClaudeCalls(canonicalTempDir(t))
	defer cc.close()
	ctx := withRemovalSession(withClaudeCalls(context.Background(), cc), abcdefSID)

	want := "more than one job directory in " + jobs
	if err := vetBeforeRemoval(ctx, cc, []claudeSessionRow{row}); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("the vet before removal: %v; want %q", err, want)
	}
	if _, err := realRemoveClaudeSession(ctx, "abcdef12"); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("realRemoveClaudeSession: %v; want %q", err, want)
	}
	if got := claudeLog(t, log); got != nil {
		t.Fatalf("claude ran: %q", got)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the link's target changed: %v", err)
	}
}

// Right before claude rm, the fresh row listed under the short id must
// carry the full session id the caller chose; a row under another full id,
// and a call that names no full id, refuse with no claude call, though the
// job directory is clean.
func TestRealRemoveClaudeSessionConfirmsTheFullSessionID(t *testing.T) {
	other := claudeSessionRow{ID: "abcdef12", SessionID: "abcdef12-1111-4111-8111-111111111111"}
	cases := []struct {
		name    string
		rows    []claudeSessionRow
		withSID bool
		want    string
	}{
		{"listed under another full id", []claudeSessionRow{other}, true, "under full id " + other.SessionID},
		{"no full id named", []claudeSessionRow{{ID: "abcdef12", SessionID: abcdefSID}}, false, "no full session id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vetHome(t, "abcdef12")
			listedForRemoval(t, nil, tc.rows...)
			log := loggingClaude(t, "removed", 0)
			cc := newClaudeCalls(canonicalTempDir(t))
			defer cc.close()
			ctx := withClaudeCalls(context.Background(), cc)
			if tc.withSID {
				ctx = withRemovalSession(ctx, abcdefSID)
			}
			_, err := realRemoveClaudeSession(ctx, "abcdef12")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v; want it to say %q", err, tc.want)
			}
			if got := claudeLog(t, log); got != nil {
				t.Fatalf("claude ran: %q", got)
			}
		})
	}
}

func TestClaudeCallsVetKeepsWhenItsContextEnds(t *testing.T) {
	vetHome(t, "abcdef12")
	cc := newClaudeCalls(canonicalTempDir(t))
	defer cc.close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cc.vet(ctx, "abcdef12"); err == nil || !strings.Contains(err.Error(), "ran out of time") {
		t.Fatalf("a vet under an ended context: %v", err)
	}
	// Its own bound is ten seconds, whatever the caller's.
	if claudeVetTimeout != 10*time.Second {
		t.Fatalf("the vet's own bound is %s", claudeVetTimeout)
	}
}

func TestNewClaudeCallsMakesNoScratchDirectoryWithoutAClaudeCall(t *testing.T) {
	home, _, _ := vetHome(t, "abcdef12")
	cc := newClaudeCalls(canonicalTempDir(t))
	if err := cc.vet(context.Background(), "abcdef12"); err != nil {
		t.Fatal(err)
	}
	cc.close()
	if _, err := os.Lstat(filepath.Join(home, ".niwa")); !os.IsNotExist(err) {
		t.Fatalf("a vet alone made %s: %v", filepath.Join(home, ".niwa"), err)
	}
}

// passRow is the listing's row for the pass fixture's session.
func passRow() claudeSessionRow {
	return claudeSessionRow{ID: passSessionID[:8], SessionID: passSessionID}
}

// passJobDir makes the pass fixture's session's job directory, 0700, under
// its jobs root.
func passJobDir(t *testing.T, f passFixture) string {
	t.Helper()
	if err := os.Chmod(f.jobsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.jobsDir, passSessionID[:8])
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPassSessionStep_AListingThatFailsKeepsTheUnit(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	fake := fakeClaudeSessions{listErr: errors.New("claude agents: exit status 2")}
	installFakeClaudeSessions(t, &fake)
	keptAtActing(t, f, "Claude Code's sessions could not be listed: claude agents: exit status 2")
	if len(fake.removed) != 0 {
		t.Fatalf("claude rm ran: %q", fake.removed)
	}
}

func TestPassSessionStep_ARunningListedSessionKeepsTheUnit(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	row := passRow()
	row.PID = 4242
	fake := fakeClaudeSessions{rows: []claudeSessionRow{row}}
	installFakeClaudeSessions(t, &fake)
	keptAtActing(t, f, "Claude session "+row.ID+" is still running")
	if len(fake.removed) != 0 {
		t.Fatalf("claude rm ran: %q", fake.removed)
	}
}

func TestPassSessionStep_AFailingVetKeepsTheUnitWithNothingRemoved(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	dir := passJobDir(t, f)
	// Group and other write: the rule's walk doesn't judge modes, so the
	// vet before the first removal step is what keeps.
	if err := os.Chmod(dir, 0o757); err != nil {
		t.Fatal(err)
	}
	fake := fakeClaudeSessions{rows: []claudeSessionRow{passRow()}}
	installFakeClaudeSessions(t, &fake)
	keptAtActing(t, f, filepath.Base(dir)+" is writable by group or others")
	if !names(f.stderr.String(), dir) {
		t.Fatalf("the kept line doesn't name %s:\n%s", dir, f.stderr.String())
	}
	if len(fake.removed) != 0 {
		t.Fatalf("claude rm ran: %q", fake.removed)
	}
}

func TestPassSessionStep_AFailingClaudeRmKeepsTheUnit(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	passJobDir(t, f)
	log := filepath.Join(t.TempDir(), "on-reap.log")
	appendOnReapConfig(t, f.root, fmt.Sprintf(`command = "echo ran >> '%s'"`, log))
	fake := fakeClaudeSessions{rows: []claudeSessionRow{passRow()}, rmOut: "error: the worktree holds changes\n", rmErr: errors.New("exit status 1")}
	installFakeClaudeSessions(t, &fake)
	keptAtActing(t, f, "Claude session "+passSessionID[:8]+" was not removed: error: the worktree holds changes")
	if len(fake.removed) != 1 {
		t.Fatalf("claude rm calls %q; want one", fake.removed)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("the on-reap command ran after a failed claude rm: %v", err)
	}
}

func TestPassSessionStep_RemovesEachListedSessionThroughTheVettedCall(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	passJobDir(t, f)
	var vetted []string
	prevList, prevRemove := listClaudeSessionsFunc, removeClaudeSessionFunc
	listCalls := 0
	listClaudeSessionsFunc = func(ctx context.Context) ([]claudeSessionRow, error) {
		listCalls++
		if claudeCallsFrom(ctx) == nil {
			t.Error("the pass listed sessions without its checked directory")
		}
		return []claudeSessionRow{passRow()}, nil
	}
	removeClaudeSessionFunc = func(ctx context.Context, id string) (string, error) {
		cc := claudeCallsFrom(ctx)
		if cc == nil {
			return "", errors.New("no claude calls on the context")
		}
		if err := cc.vet(ctx, id); err != nil {
			return "", err
		}
		vetted = append(vetted, id)
		return "removed", nil
	}
	t.Cleanup(func() { listClaudeSessionsFunc, removeClaudeSessionFunc = prevList, prevRemove })
	removed := realRemoval(t)

	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 1 || len(*removed) != 1 {
		t.Fatalf("reaped %d, removed %v; want the unit removed\nstderr:\n%s", res.Reaped, *removed, f.stderr.String())
	}
	if len(vetted) != 1 || vetted[0] != passSessionID[:8] {
		t.Fatalf("claude rm calls %q; want one for %s", vetted, passSessionID[:8])
	}
	if listCalls != 1 {
		t.Fatalf("listed %d times; want once per pass", listCalls)
	}
}

func TestPassSessionStep_ASessionAbsentFromTheListingGetsNoCall(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	fake := fakeClaudeSessions{rows: []claudeSessionRow{{ID: "99999999", SessionID: "99999999-9999-4999-8999-999999999999"}}}
	installFakeClaudeSessions(t, &fake)
	removed := realRemoval(t)
	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 1 || len(*removed) != 1 {
		t.Fatalf("reaped %d; want the unit removed\nstderr:\n%s", res.Reaped, f.stderr.String())
	}
	if len(fake.removed) != 0 {
		t.Fatalf("claude rm ran for a session the listing doesn't hold: %q", fake.removed)
	}
}

// destroyVetSetup is a workspace with instances alpha and beta, alpha mapped
// to ownerSID, HOME holding ownerSID's job directory, and the listing
// faked to list the session, stopped. It returns the root, the job
// directory and the fake.
func destroyVetSetup(t *testing.T, force, wholeWorkspace bool, confirm string) (string, string, *fakeClaudeSessions) {
	t.Helper()
	_, _, jobDir := vetHome(t, ownerSID[:8])
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	mapNamedSession(t, root, ownerSID, filepath.Join(root, "alpha"), "fix-thing")
	fake := &fakeClaudeSessions{rows: []claudeSessionRow{{ID: ownerSID[:8], SessionID: ownerSID}}}
	installFakeClaudeSessions(t, fake)
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, force, wholeWorkspace, confirm)
	return root, jobDir, fake
}

func setKeepSession(t *testing.T, keep bool) {
	t.Helper()
	prev := destroyKeepSession
	destroyKeepSession = keep
	t.Cleanup(func() { destroyKeepSession = prev })
}

func TestDestroyVet_AFailingJobDirectoryStopsDestroyEvenUnderForce(t *testing.T) {
	root, jobDir, fake := destroyVetSetup(t, true, false, "")
	if err := os.Chmod(jobDir, 0o777); err != nil {
		t.Fatal(err)
	}
	err := runDestroy(destroyCmd, []string{"alpha"})
	if err == nil || !strings.Contains(err.Error(), "nothing was destroyed") || !names(err.Error(), jobDir) {
		t.Fatalf("err = %v; want destroy stopped with %s named", err, jobDir)
	}
	if len(fake.removed) != 0 {
		t.Fatalf("claude rm ran: %q", fake.removed)
	}
	assertInstancesExist(t, root, "alpha", "beta")
	if !mappingExists(t, root, ownerSID) {
		t.Fatal("the session's mapping is gone")
	}
}

func TestDestroyVet_AnInnerLinkStopsTheWholeWorkspace(t *testing.T) {
	root, jobDir, fake := destroyVetSetup(t, false, true, "testws")
	link, marker := innerLink(t, jobDir)
	err := runDestroy(destroyCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing was destroyed") || !names(err.Error(), link) {
		t.Fatalf("err = %v; want destroy --workspace stopped with %s named", err, link)
	}
	if len(fake.removed) != 0 {
		t.Fatalf("claude rm ran: %q", fake.removed)
	}
	assertInstancesExist(t, root, "alpha", "beta")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the link's target changed: %v", err)
	}
}

func TestDestroyVet_KeepSessionVetsNothing(t *testing.T) {
	root, jobDir, fake := destroyVetSetup(t, true, false, "")
	setKeepSession(t, true)
	if err := os.Chmod(jobDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
		t.Fatalf("destroy --keep-session: %v", err)
	}
	if len(fake.removed) != 0 {
		t.Fatalf("claude rm ran: %q", fake.removed)
	}
	if _, err := os.Stat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("alpha should be gone: %v", err)
	}
}

func TestDestroyVet_ACleanJobDirectoryLetsDestroyRemoveTheSession(t *testing.T) {
	root, _, fake := destroyVetSetup(t, true, false, "")
	if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if len(fake.removed) != 1 || fake.removed[0] != ownerSID[:8] {
		t.Fatalf("claude rm calls %q; want one for %s", fake.removed, ownerSID[:8])
	}
	if _, err := os.Stat(filepath.Join(root, "alpha")); !os.IsNotExist(err) {
		t.Fatalf("alpha should be gone: %v", err)
	}
}

func TestDestroyVet_AnInvalidConfigDirectoryStopsDestroy(t *testing.T) {
	root, _, fake := destroyVetSetup(t, true, false, "")
	t.Setenv("CLAUDE_CONFIG_DIR", "relative/cfg")
	err := runDestroy(destroyCmd, []string{"alpha"})
	if err == nil || !strings.Contains(err.Error(), "CLAUDE_CONFIG_DIR") {
		t.Fatalf("err = %v; want destroy stopped with the variable named", err)
	}
	if len(fake.removed) != 0 {
		t.Fatalf("claude rm ran: %q", fake.removed)
	}
	assertInstancesExist(t, root, "alpha", "beta")
}

func TestRetaskRemoveCopy_AFailingVetLeavesTheCopyWithThePathNamed(t *testing.T) {
	_, _, jobDir := vetHome(t, "cccccccc")
	link, marker := innerLink(t, jobDir)
	log := loggingClaude(t, "", 0)
	root := canonicalTempDir(t)
	inst := filepath.Join(root, "inst")
	if err := os.Mkdir(inst, 0o755); err != nil {
		t.Fatal(err)
	}
	copyRow := claudeSessionRow{ID: "cccccccc", SessionID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", Cwd: inst}
	fake := fakeClaudeSessions{rows: []claudeSessionRow{copyRow}}
	installFakeClaudeSessions(t, &fake)
	removeClaudeSessionFunc = realRemoveClaudeSession

	cc := newClaudeCalls(root)
	defer cc.close()
	r := &retaskRun{
		ctx: withClaudeCalls(context.Background(), cc),
		v:   workerView{SessionID: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", ShortID: "dddddddd", InstancePath: inst, Root: root},
	}
	ok, why := r.removeCopy(copyRow, map[string]bool{})
	if ok || !names(why, link) {
		t.Fatalf("removeCopy = %v, %q; want the copy left with %s named", ok, why, link)
	}
	if got := claudeLog(t, log); got != nil {
		t.Fatalf("claude ran: %q", got)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the link's target changed: %v", err)
	}
}

// A named pipe in a job directory keeps destroy the same way a link does.
func TestDestroyVet_ANamedPipeStopsDestroy(t *testing.T) {
	root, jobDir, fake := destroyVetSetup(t, true, false, "")
	pipe := filepath.Join(jobDir, "scratch", "out")
	if err := os.Remove(pipe); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	err := runDestroy(destroyCmd, []string{"alpha"})
	if err == nil || !strings.Contains(err.Error(), "a named pipe at ") || !names(err.Error(), pipe) {
		t.Fatalf("err = %v; want the pipe named", err)
	}
	if len(fake.removed) != 0 {
		t.Fatalf("claude rm ran: %q", fake.removed)
	}
	assertInstancesExist(t, root, "alpha")
}
