package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/worktree"
)

// These tests cover the worktree sweep's job-keyed rule and its pre-removal
// gate. Every jobs directory, transcript and workspace is under t.TempDir(),
// and HOME points at a scratch directory, so the hook never reads the real
// Claude Code jobs directory.

const (
	heldSessionID  = "11111111-2222-3333-4444-555555555555"
	otherSessionID = "66666666-7777-8888-9999-aaaaaaaaaaaa"
)

// scratchJobsDir points HOME at a scratch directory and returns the Claude
// Code jobs directory under it, which the hook reads through defaultJobsDir.
func scratchJobsDir(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	jobs := filepath.Join(home, ".claude", "jobs")
	if err := os.MkdirAll(jobs, 0o755); err != nil {
		t.Fatal(err)
	}
	return jobs
}

// listJob writes a job entry for sessionID, the way Claude Code lists a
// background session, and returns its directory.
func listJob(t *testing.T, jobsDir, sessionID string) string {
	t.Helper()
	writeJobStateFile(t, jobsDir, sessionID[:8], sessionID, "", nil)
	return filepath.Join(jobsDir, sessionID[:8])
}

// createHookWorktreeFor makes a worktree through the WorktreeCreate hook for
// the given Claude session id and transcript.
func createHookWorktreeFor(t *testing.T, f *createFlowFixture, name, sessionID, transcript string) worktree.SessionLifecycleState {
	t.Helper()
	t.Setenv("CLAUDE_PROJECT_DIR", f.root)
	out, errOut, err := runFromHook(t, f.root, mustHookJSON(t, map[string]any{
		"hook_event_name": "WorktreeCreate",
		"session_id":      sessionID,
		"transcript_path": transcript,
		"cwd":             f.root,
		"name":            name,
	}))
	if err != nil {
		t.Fatalf("create: %v\nstderr:\n%s", err, errOut)
	}
	printed := strings.TrimSpace(out)
	states, err := worktree.ListSessionLifecycleStates(f.sessionsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range states {
		if st.WorktreePath == printed {
			return st
		}
	}
	t.Fatalf("no session recorded for %q", printed)
	return worktree.SessionLifecycleState{}
}

// idleTranscript writes a transcript last modified age ago.
func idleTranscript(t *testing.T, age time.Duration) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "transcript.jsonl")
	writeFile(t, p)
	old := time.Now().Add(-age)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	return p
}

// stripJobFields rewrites a record as one written before the hook recorded
// the Claude session id and the job entry.
func stripJobFields(t *testing.T, f *createFlowFixture, st worktree.SessionLifecycleState) {
	t.Helper()
	path := filepath.Join(f.sessionsDir, st.SessionID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	delete(doc, "claude_session_id")
	delete(doc, "claude_job_listed")
	if raw, err = json.Marshal(doc); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func sweep(t *testing.T, f *createFlowFixture, jobsDir string) (int, string) {
	t.Helper()
	var stderr bytes.Buffer
	n := sweepHookWorktreesIn(f.root, []string{f.root}, jobsDir, time.Now(), &stderr)
	return n, stderr.String()
}

// TestHookWorktree_RecordsClaudeSessionAndJob: the hook records a well-formed
// payload session id and whether that session had a job entry, and records
// neither for a malformed id.
func TestHookWorktree_RecordsClaudeSessionAndJob(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	jobs := scratchJobsDir(t)
	listJob(t, jobs, heldSessionID)

	held := createHookWorktreeFor(t, f, "held", heldSessionID, "")
	if held.ClaudeSessionID != heldSessionID || held.ClaudeJobListed == nil || !*held.ClaudeJobListed {
		t.Errorf("with a job entry: id %q, job listed %v; want %q and true", held.ClaudeSessionID, held.ClaudeJobListed, heldSessionID)
	}

	none := createHookWorktreeFor(t, f, "none", otherSessionID, "")
	if none.ClaudeSessionID != otherSessionID || none.ClaudeJobListed == nil || *none.ClaudeJobListed {
		t.Errorf("without a job entry: id %q, job listed %v; want %q and false", none.ClaudeSessionID, none.ClaudeJobListed, otherSessionID)
	}

	for _, bad := range []string{"claude-sid", "../" + heldSessionID, strings.ToUpper(otherSessionID), heldSessionID + "0"} {
		st := createHookWorktreeFor(t, f, "bad", bad, "")
		if st.ClaudeSessionID != "" || st.ClaudeJobListed != nil {
			t.Errorf("payload id %q: recorded id %q, job listed %v; want neither", bad, st.ClaudeSessionID, st.ClaudeJobListed)
		}
	}
}

// TestSweepHookWorktrees_HeldWorkerKeptWhileJobListed: a held worker's
// worktree, record and transcript both 48 hours old, stays while its job entry
// is listed and goes on the first sweep after the entry is removed.
func TestSweepHookWorktrees_HeldWorkerKeptWhileJobListed(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("NIWA_REAP_IDLE_GRACE", "24h")
	jobs := scratchJobsDir(t)
	entry := listJob(t, jobs, heldSessionID)

	st := createHookWorktreeFor(t, f, "held", heldSessionID, idleTranscript(t, 48*time.Hour))
	backdate(t, f, st, 48*time.Hour)

	for i := 0; i < 3; i++ {
		if n, out := sweep(t, f, jobs); n != 0 {
			t.Fatalf("sweep %d removed %d while the job is listed\nstderr:\n%s", i, n, out)
		}
		assertKept(t, f, st)
	}

	if err := os.RemoveAll(entry); err != nil {
		t.Fatal(err)
	}
	// An entry under the same prefix recorded for another session isn't this
	// one's.
	writeJobStateFile(t, jobs, heldSessionID[:8], heldSessionID[:8]+"-0000-0000-0000-000000000000", "", nil)
	if n, out := sweep(t, f, jobs); n != 1 {
		t.Fatalf("removed %d once the job entry is gone, want 1\nstderr:\n%s", n, out)
	}
	assertRemoved(t, f, st)
}

// TestSweepHookWorktrees_NoJobAndPreChangeFollowTranscript: a worktree
// recorded with no job entry, and one whose record predates the job fields,
// go once record and transcript are past the grace, and stay while the
// transcript is younger. With nothing declared the gate starts no process.
func TestSweepHookWorktrees_NoJobAndPreChangeFollowTranscript(t *testing.T) {
	for _, tc := range []struct {
		name    string
		age     time.Duration
		removed bool
	}{
		{"idle transcript", 48 * time.Hour, true},
		{"fresh transcript", time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCreateFlowFixture(t)
			resetSessionCreateFlags(t)
			defer resetSessionCreateFlags(t)
			t.Setenv("NIWA_REAP_IDLE_GRACE", "24h")
			jobs := scratchJobsDir(t)
			shellLog := stubShellLogging(t)

			noJob := createHookWorktreeFor(t, f, "no-job", otherSessionID, idleTranscript(t, tc.age))
			if noJob.ClaudeJobListed == nil || *noJob.ClaudeJobListed {
				t.Fatalf("no-job record: job listed %v, want false", noJob.ClaudeJobListed)
			}
			// Listed after create: the record says it had none, so the
			// transcript still decides.
			listJob(t, jobs, otherSessionID)
			preChange := createHookWorktreeFor(t, f, "pre-change", heldSessionID, idleTranscript(t, tc.age))
			stripJobFields(t, f, preChange)
			listJob(t, jobs, heldSessionID)
			backdate(t, f, noJob, 48*time.Hour)
			backdate(t, f, preChange, 48*time.Hour)

			n, out := sweep(t, f, jobs)
			if tc.removed {
				if n != 2 {
					t.Fatalf("removed %d, want 2\nstderr:\n%s", n, out)
				}
				assertRemoved(t, f, noJob)
				assertRemoved(t, f, preChange)
			} else {
				if n != 0 {
					t.Fatalf("removed %d with a fresh transcript, want 0\nstderr:\n%s", n, out)
				}
				assertKept(t, f, noJob)
				assertKept(t, f, preChange)
			}
			if _, err := os.Stat(shellLog); !os.IsNotExist(err) {
				t.Errorf("a gate process started with nothing declared: %v", err)
			}
		})
	}
}

// TestSweepHookWorktrees_JobKeyedYoungOrChangedKept: with its job entry gone,
// a job-keyed worktree is still kept while its record is younger than the
// grace, and when it has changed since creation.
func TestSweepHookWorktrees_JobKeyedYoungOrChangedKept(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("NIWA_REAP_IDLE_GRACE", "24h")
	jobs := scratchJobsDir(t)
	heldEntry := listJob(t, jobs, heldSessionID)
	otherEntry := listJob(t, jobs, otherSessionID)

	young := createHookWorktreeFor(t, f, "young", heldSessionID, idleTranscript(t, 48*time.Hour))
	backdate(t, f, young, time.Hour)
	changed := createHookWorktreeFor(t, f, "changed", otherSessionID, idleTranscript(t, 48*time.Hour))
	backdate(t, f, changed, 48*time.Hour)
	writeFile(t, filepath.Join(changed.WorktreePath, "work.txt"))

	for _, e := range []string{heldEntry, otherEntry} {
		if err := os.RemoveAll(e); err != nil {
			t.Fatal(err)
		}
	}
	if n, out := sweep(t, f, jobs); n != 0 {
		t.Fatalf("removed %d, want 0\nstderr:\n%s", n, out)
	}
	assertKept(t, f, young)
	assertKept(t, f, changed)
}

// TestSweepHookWorktrees_JobsDirMissingOrUnreadable: without a jobs directory
// it can read, or with an entry for the session it can't read, the sweep
// removes no job-keyed worktree.
func TestSweepHookWorktrees_JobsDirMissingOrUnreadable(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, jobs string) string
	}{
		{"missing", func(t *testing.T, jobs string) string {
			if err := os.RemoveAll(jobs); err != nil {
				t.Fatal(err)
			}
			return jobs
		}},
		{"unset", func(t *testing.T, jobs string) string { return "" }},
		{"unreadable", func(t *testing.T, jobs string) string {
			if os.Geteuid() == 0 {
				t.Skip("root reads a mode-000 directory")
			}
			if err := os.RemoveAll(filepath.Join(jobs, heldSessionID[:8])); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(jobs, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(jobs, 0o755) })
			return jobs
		}},
		{"entry unreadable", func(t *testing.T, jobs string) string {
			state := filepath.Join(jobs, heldSessionID[:8], "state.json")
			if err := os.WriteFile(state, []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
			return jobs
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCreateFlowFixture(t)
			resetSessionCreateFlags(t)
			defer resetSessionCreateFlags(t)
			t.Setenv("NIWA_REAP_IDLE_GRACE", "24h")
			jobs := scratchJobsDir(t)
			listJob(t, jobs, heldSessionID)

			st := createHookWorktreeFor(t, f, "held", heldSessionID, idleTranscript(t, 48*time.Hour))
			backdate(t, f, st, 48*time.Hour)
			jobsDir := tc.setup(t, jobs)

			if n, out := sweep(t, f, jobsDir); n != 0 {
				t.Fatalf("removed %d, want 0\nstderr:\n%s", n, out)
			}
			assertKept(t, f, st)
		})
	}
}

// TestSweepHookWorktrees_MalformedSessionIDFollowsTranscript: a payload id
// that isn't a well-formed session id records nothing, and the worktree goes
// by its transcript even while a job entry with that prefix is listed.
func TestSweepHookWorktrees_MalformedSessionIDFollowsTranscript(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("NIWA_REAP_IDLE_GRACE", "24h")
	jobs := scratchJobsDir(t)
	listJob(t, jobs, otherSessionID)
	bad := strings.ToUpper(otherSessionID)

	fresh := createHookWorktreeFor(t, f, "fresh", bad, idleTranscript(t, time.Minute))
	idle := createHookWorktreeFor(t, f, "idle", bad, idleTranscript(t, 48*time.Hour))
	for _, st := range []worktree.SessionLifecycleState{fresh, idle} {
		if st.ClaudeSessionID != "" || st.ClaudeJobListed != nil {
			t.Fatalf("payload id %q recorded id %q, job listed %v", bad, st.ClaudeSessionID, st.ClaudeJobListed)
		}
		backdate(t, f, st, 48*time.Hour)
	}

	if n, out := sweep(t, f, jobs); n != 1 {
		t.Fatalf("removed %d, want 1\nstderr:\n%s", n, out)
	}
	assertKept(t, f, fresh)
	assertRemoved(t, f, idle)
}

// TestRemovalGate_WorktreeSweepRefusalKeepsAndExitZeroRemoves: a worktree
// the sweep would remove is kept when the pre-removal command exits 1 for
// its path, which it sees with step worktree-sweep and the recorded session
// id; once the command exits 0 the sweep removes it.
func TestRemovalGate_WorktreeSweepRefusalKeepsAndExitZeroRemoves(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("NIWA_REAP_IDLE_GRACE", "24h")
	jobs := scratchJobsDir(t)
	entry := listJob(t, jobs, heldSessionID)

	st := createHookWorktreeFor(t, f, "held", heldSessionID, idleTranscript(t, 48*time.Hour))
	backdate(t, f, st, 48*time.Hour)
	if err := os.RemoveAll(entry); err != nil {
		t.Fatal(err)
	}

	base, err := os.ReadFile(workspaceConfigPath(f.root))
	if err != nil {
		t.Fatal(err)
	}
	logCmd, logPath := gateLogCommand(t)
	setBeforeRemoveCommand(t, f.root, string(base), fmt.Sprintf(
		`%s; if [ "$NIWA_REMOVE_PATH" = '%s' ]; then echo "kept by test"; exit 1; fi; exit 0`, logCmd, st.WorktreePath))

	n, out := sweep(t, f, jobs)
	if n != 0 {
		t.Fatalf("removed %d, want 0 while the command refuses\nstderr:\n%s", n, out)
	}
	assertKept(t, f, st)
	want := "niwa: kept " + filepath.Base(st.WorktreePath) + `: reason from before_remove: "kept by test"`
	if kept := keptLines(out); len(kept) != 1 || kept[0] != want {
		t.Errorf("spare lines = %q, want [%q]", kept, want)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(logged)); got != "worktree-sweep|"+heldSessionID {
		t.Errorf("command saw %q, want step worktree-sweep and the recorded session id", got)
	}

	setBeforeRemoveCommand(t, f.root, string(base), "exit 0")
	if n, out := sweep(t, f, jobs); n != 1 {
		t.Fatalf("removed %d once the command allows, want 1\nstderr:\n%s", n, out)
	}
	assertRemoved(t, f, st)
}

// TestRemovalGate_WorktreeSweepStartFailureKeeps: with a pre-removal shell
// that can't start, a worktree the sweep would remove is kept and its line
// says the command could not start.
func TestRemovalGate_WorktreeSweepStartFailureKeeps(t *testing.T) {
	f := newCreateFlowFixture(t)
	resetSessionCreateFlags(t)
	defer resetSessionCreateFlags(t)
	t.Setenv("NIWA_REAP_IDLE_GRACE", "24h")
	jobs := scratchJobsDir(t)

	st := createHookWorktreeFor(t, f, "no-job", otherSessionID, idleTranscript(t, 48*time.Hour))
	backdate(t, f, st, 48*time.Hour)
	appendBeforeRemoveConfig(t, f.root, `command = "exit 0"`)
	prev := onReapShell
	onReapShell = filepath.Join(t.TempDir(), "no-such-shell")
	t.Cleanup(func() { onReapShell = prev })

	n, out := sweep(t, f, jobs)
	if n != 0 {
		t.Fatalf("removed %d, want 0 when the command could not start\nstderr:\n%s", n, out)
	}
	assertKept(t, f, st)
	kept := keptLines(out)
	if len(kept) != 1 || !strings.HasPrefix(kept[0], "niwa: kept "+filepath.Base(st.WorktreePath)+": ") || !strings.Contains(kept[0], "could not start") {
		t.Errorf("spare lines = %q, want one naming the start failure", kept)
	}
}
