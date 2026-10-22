package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
)

const (
	ownerSID = "11111111-1111-4111-8111-111111111111"
	otherSID = "22222222-2222-4222-8222-222222222222"
)

// fakeClaudeSessions swaps both claude seams for the test. rows is what the
// agents listing returns (or listErr); rmErr and rmOut shape every rm. It
// returns pointers to the recorded calls.
type fakeClaudeSessions struct {
	rows      []claudeSessionRow
	listErr   error
	rmOut     string
	rmErr     error
	listCalls int
	removed   []string
}

func installFakeClaudeSessions(t *testing.T, f *fakeClaudeSessions) {
	t.Helper()
	prevList, prevRemove := listClaudeSessionsFunc, removeClaudeSessionFunc
	listClaudeSessionsFunc = func(context.Context) ([]claudeSessionRow, error) {
		f.listCalls++
		return f.rows, f.listErr
	}
	removeClaudeSessionFunc = func(_ context.Context, id string) (string, error) {
		f.removed = append(f.removed, id)
		return f.rmOut, f.rmErr
	}
	t.Cleanup(func() { listClaudeSessionsFunc, removeClaudeSessionFunc = prevList, prevRemove })
}

func writeTestMapping(t *testing.T, root, sid, instanceDir, agentName string) {
	t.Helper()
	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    sid,
		InstanceName: filepath.Base(instanceDir),
		InstancePath: instanceDir,
		Created:      time.Now(),
		Ephemeral:    true,
		Agent:        agentName,
	}); err != nil {
		t.Fatal(err)
	}
}

func mappingExists(t *testing.T, root, sid string) bool {
	t.Helper()
	_, err := workspace.ReadSessionMapping(root, sid)
	return err == nil
}

func TestRemoveOwningSessions(t *testing.T) {
	cases := []struct {
		name        string
		agent       string
		mapOther    bool // map the session to a different instance
		fake        fakeClaudeSessions
		wantList    bool
		wantRemoved []string
		wantMapping bool
		wantOut     []string
	}{
		{
			name:        "finished session is removed",
			fake:        fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}},
			wantList:    true,
			wantRemoved: []string{"11111111"},
			wantMapping: false,
			wantOut:     []string{"removed Claude session 11111111; its transcript is kept: claude --resume " + ownerSID},
		},
		{
			name:        "session with a live pid is kept",
			fake:        fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID, PID: 4242, Status: "idle"}}},
			wantList:    true,
			wantMapping: true,
			wantOut:     []string{"kept Claude session 11111111: it is still running; once it stops (claude stop 11111111), remove it with claude rm 11111111"},
		},
		{
			name:        "session with only a status is kept",
			fake:        fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID, Status: "busy"}}},
			wantList:    true,
			wantMapping: true,
			wantOut:     []string{"kept Claude session 11111111"},
		},
		{
			name:        "session already gone only loses its mapping",
			fake:        fakeClaudeSessions{rows: []claudeSessionRow{{ID: "22222222", SessionID: otherSID}}},
			wantList:    true,
			wantMapping: false,
		},
		{
			name: "failed rm is reported and keeps the mapping",
			fake: fakeClaudeSessions{
				rows:  []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}},
				rmOut: "kept 11111111 — 2 unpushed commits on feature/x\nmore detail\n",
				rmErr: errors.New("exit status 1"),
			},
			wantList:    true,
			wantRemoved: []string{"11111111"},
			wantMapping: true,
			wantOut:     []string{"warning: could not remove Claude session 11111111: kept 11111111 — 2 unpushed commits on feature/x; remove it with claude rm 11111111"},
		},
		{
			name: "failed rm with no output reports the error",
			fake: fakeClaudeSessions{
				rows:  []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}},
				rmErr: errors.New("exit status 1"),
			},
			wantList:    true,
			wantRemoved: []string{"11111111"},
			wantMapping: true,
			wantOut:     []string{"could not remove Claude session 11111111: exit status 1"},
		},
		{
			name:        "unreadable listing leaves the session alone",
			fake:        fakeClaudeSessions{listErr: errors.New("claude binary not found in PATH")},
			wantList:    true,
			wantMapping: true,
			wantOut:     []string{"warning: could not check the Claude sessions these instances own (claude binary not found in PATH); left them in place"},
		},
		{
			name:        "unsafe id is never passed to rm",
			fake:        fakeClaudeSessions{rows: []claudeSessionRow{{ID: "--discard-unpushed x", SessionID: ownerSID}}},
			wantList:    true,
			wantMapping: true,
			wantOut:     []string{"unusable id"},
		},
		{
			name:        "codex mapping is not a Claude session",
			agent:       "codex",
			fake:        fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}},
			wantMapping: true,
		},
		{
			name:        "mapping for another instance is ignored",
			mapOther:    true,
			fake:        fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}},
			wantMapping: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := destroyTestSetup(t, []string{"alpha", "beta"})
			instance := filepath.Join(root, "alpha")
			mapped := instance
			if tc.mapOther {
				mapped = filepath.Join(root, "beta")
			}
			writeTestMapping(t, root, ownerSID, mapped, tc.agent)

			fake := tc.fake
			installFakeClaudeSessions(t, &fake)
			var out bytes.Buffer
			removeOwningSessions(workspace.NewReporter(&out), root, []string{instance})

			if got := fake.listCalls > 0; got != tc.wantList {
				t.Errorf("listed sessions = %v, want %v", got, tc.wantList)
			}
			if strings.Join(fake.removed, ",") != strings.Join(tc.wantRemoved, ",") {
				t.Errorf("removed %v, want %v", fake.removed, tc.wantRemoved)
			}
			if got := mappingExists(t, root, ownerSID); got != tc.wantMapping {
				t.Errorf("mapping exists = %v, want %v", got, tc.wantMapping)
			}
			for _, w := range tc.wantOut {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output missing %q; got:\n%s", w, out.String())
				}
			}
			if len(tc.wantOut) == 0 && out.Len() != 0 {
				t.Errorf("expected no output; got:\n%s", out.String())
			}
		})
	}
}

// TestRemoveOwningSessions_OnReap covers the on-reap command in destroy: it
// runs after a session's removal succeeded and for a session already gone,
// from the workspace root whether or not the instance directory exists, and
// the mapping goes only once it succeeds. A failed or timed-out run keeps the
// mapping with one warning line; a failed removal, a running session and an
// unreadable configuration run nothing that succeeds.
func TestRemoveOwningSessions_OnReap(t *testing.T) {
	cases := []struct {
		name         string
		fake         fakeClaudeSessions
		command      string
		breakConfig  bool
		removeDir    bool
		wantRuns     int
		wantMapping  bool
		wantWarnings int
		wantWarning  string
	}{
		{name: "removed session runs the command", fake: fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}}, wantRuns: 1},
		{name: "already gone runs the command", fake: fakeClaudeSessions{rows: nil}, wantRuns: 1},
		{name: "instance directory already gone", fake: fakeClaudeSessions{rows: nil}, removeDir: true, wantRuns: 1},
		{name: "failed run keeps the mapping", fake: fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}}, command: "exit 1", wantRuns: 1, wantMapping: true, wantWarnings: 1, wantWarning: "exited with status 1"},
		{name: "timed-out run keeps the mapping", fake: fakeClaudeSessions{rows: nil}, command: "sleep 10", wantRuns: 1, wantMapping: true, wantWarnings: 1, wantWarning: "timed out after 1s"},
		{name: "failed removal runs nothing", fake: fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}, rmErr: errors.New("exit status 1")}, wantRuns: 0, wantMapping: true},
		{name: "running session runs nothing", fake: fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID, PID: 9}}}, wantRuns: 0, wantMapping: true},
		{name: "unreadable configuration is a failed run", fake: fakeClaudeSessions{rows: nil}, breakConfig: true, wantRuns: 0, wantMapping: true, wantWarnings: 1, wantWarning: "could not read [session.on_reap]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := destroyTestSetup(t, []string{"alpha"})
			instance := filepath.Join(root, "alpha")
			writeTestMapping(t, root, ownerSID, instance, "")
			logPath := filepath.Join(t.TempDir(), "runs.log")
			cwdPath := filepath.Join(t.TempDir(), "cwd")
			command := fmt.Sprintf(`echo "$NIWA_SESSION_ID $NIWA_REAP_KEY" >> '%s'; pwd -P > '%s'`, logPath, cwdPath)
			if tc.command != "" {
				command += "; " + tc.command
			}
			appendOnReapConfig(t, root, fmt.Sprintf("command = %q\ntimeout = \"1s\"", command))
			if tc.breakConfig {
				if err := os.WriteFile(workspaceConfigPath(root), []byte("[workspace\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.removeDir {
				if err := os.RemoveAll(instance); err != nil {
					t.Fatal(err)
				}
			}
			fake := tc.fake
			installFakeClaudeSessions(t, &fake)

			var out bytes.Buffer
			start := time.Now()
			removeOwningSessions(workspace.NewReporterWithTTY(&out, false), root, []string{instance})
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Errorf("destroy's session step took %s", elapsed)
			}

			runs := onReapRuns(t, logPath)
			if len(runs) != tc.wantRuns {
				t.Fatalf("runs = %q, want %d", runs, tc.wantRuns)
			}
			if tc.wantRuns > 0 {
				if runs[0] != ownerSID+" "+ownerSID {
					t.Errorf("run = %q, want the session id as its key", runs[0])
				}
				cwd, _ := os.ReadFile(cwdPath)
				wantCwd, _ := filepath.EvalSymlinks(root)
				if strings.TrimSpace(string(cwd)) != wantCwd {
					t.Errorf("ran in %q, want the workspace root %q", strings.TrimSpace(string(cwd)), wantCwd)
				}
			}
			if got := mappingExists(t, root, ownerSID); got != tc.wantMapping {
				t.Errorf("mapping exists = %v, want %v", got, tc.wantMapping)
			}
			warnings := onReapWarnings(out.String())
			if len(warnings) != tc.wantWarnings {
				t.Errorf("warnings = %q, want %d", warnings, tc.wantWarnings)
			}
			if tc.wantWarning != "" && len(warnings) == 1 && !strings.Contains(warnings[0], tc.wantWarning) {
				t.Errorf("warning = %q, want it to say %q", warnings[0], tc.wantWarning)
			}
		})
	}
}

// TestRemoveOwningSessions_FailedRunThenReapSucceeds: a destroy whose on-reap
// run fails keeps the mapping, and the next reap runs the command again and,
// on success, drops it. destroy itself never retries.
func TestRemoveOwningSessions_FailedRunThenReapSucceeds(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	instance := filepath.Join(root, "alpha")
	writeTestMapping(t, root, ownerSID, instance, "")
	flag := filepath.Join(t.TempDir(), "succeed")
	logPath := filepath.Join(t.TempDir(), "runs.log")
	appendOnReapConfig(t, root, fmt.Sprintf(`command = "echo run >> '%s'; [ -e '%s' ]"`, logPath, flag))
	installFakeClaudeSessions(t, &fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}})

	var out bytes.Buffer
	removeOwningSessions(workspace.NewReporterWithTTY(&out, false), root, []string{instance})
	if !mappingExists(t, root, ownerSID) {
		t.Fatal("a failed run must keep the mapping")
	}
	if err := os.RemoveAll(instance); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(flag, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	captureReapStderr(t)
	if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if runs := onReapRuns(t, logPath); len(runs) != 2 {
		t.Errorf("runs = %d, want 2 (destroy's failed run and the reap's)", len(runs))
	}
	if mappingExists(t, root, ownerSID) {
		t.Error("the reap's successful run did not drop the mapping")
	}
}

// TestRemoveOwningSessions_NoMappingNeverCallsClaude pins the lazy listing:
// an instance no session owns must not run claude at all, so destroying an
// ordinary instance costs nothing and never depends on Claude Code.
func TestRemoveOwningSessions_NoMappingNeverCallsClaude(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	fake := fakeClaudeSessions{}
	installFakeClaudeSessions(t, &fake)
	var out bytes.Buffer
	removeOwningSessions(workspace.NewReporter(&out), root, []string{filepath.Join(root, "alpha")})
	if fake.listCalls != 0 || len(fake.removed) != 0 {
		t.Errorf("claude was called for an unowned instance: list=%d rm=%v", fake.listCalls, fake.removed)
	}
}

// TestRemoveOwningSessions_EveryOwnerOfTheInstance covers an instance named by
// more than one mapping (each dispatch into it writes one): every finished
// owner is removed, and a running one is kept.
func TestRemoveOwningSessions_EveryOwnerOfTheInstance(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	instance := filepath.Join(root, "alpha")
	writeTestMapping(t, root, ownerSID, instance, "")
	writeTestMapping(t, root, otherSID, instance, "claude")
	fake := fakeClaudeSessions{rows: []claudeSessionRow{
		{ID: "11111111", SessionID: ownerSID},
		{ID: "22222222", SessionID: otherSID, PID: 7},
	}}
	installFakeClaudeSessions(t, &fake)
	var out bytes.Buffer
	removeOwningSessions(workspace.NewReporter(&out), root, []string{instance})
	if fake.listCalls != 1 {
		t.Errorf("listed %d times, want once", fake.listCalls)
	}
	if strings.Join(fake.removed, ",") != "11111111" {
		t.Errorf("removed %v, want only the finished session", fake.removed)
	}
	if mappingExists(t, root, ownerSID) || !mappingExists(t, root, otherSID) {
		t.Errorf("want the finished session's mapping gone and the running one's kept")
	}
}

// TestRunDestroy_WorkspaceRemovesEveryOwningSession covers `niwa destroy
// --workspace`: each instance's owning session is removed, with one listing,
// before the workspace goes.
func TestRunDestroy_WorkspaceRemovesEveryOwningSession(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	writeTestMapping(t, root, ownerSID, filepath.Join(root, "alpha"), "")
	writeTestMapping(t, root, otherSID, filepath.Join(root, "beta"), "")
	fake := fakeClaudeSessions{rows: []claudeSessionRow{
		{ID: "11111111", SessionID: ownerSID},
		{ID: "22222222", SessionID: otherSID},
	}}
	installFakeClaudeSessions(t, &fake)
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, false, true, "testws")

	if err := runDestroy(destroyCmd, nil); err != nil {
		t.Fatalf("destroy --workspace: %v", err)
	}
	if fake.listCalls != 1 {
		t.Errorf("listed %d times, want once", fake.listCalls)
	}
	if strings.Join(fake.removed, ",") != "11111111,22222222" {
		t.Errorf("removed %v, want both sessions", fake.removed)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("workspace should be gone: %v", err)
	}
}

// TestRunDestroy_FailedRemovalStillDestroys pins the fail-safe contract at
// the command level: a claude rm or listing that fails is reported, and the
// instance is removed anyway.
func TestRunDestroy_FailedRemovalStillDestroys(t *testing.T) {
	for name, fake := range map[string]fakeClaudeSessions{
		"rm fails":   {rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}, rmErr: errors.New("exit status 1")},
		"list fails": {listErr: errors.New("claude binary not found in PATH")},
	} {
		t.Run(name, func(t *testing.T) {
			root := destroyTestSetup(t, []string{"alpha", "beta"})
			instance := filepath.Join(root, "alpha")
			writeTestMapping(t, root, ownerSID, instance, "")
			installFakeClaudeSessions(t, &fake)
			chdirTo(t, root)
			stderr := quietDestroyCmd()
			setDestroyFlags(t, true, false, "")

			if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
				t.Fatalf("destroy should succeed despite the session failure: %v", err)
			}
			if _, err := os.Stat(instance); !os.IsNotExist(err) {
				t.Errorf("instance should be gone: %v", err)
			}
			if !strings.Contains(stderr.String(), "warning: could not") {
				t.Errorf("stderr should carry the warning; got:\n%s", stderr.String())
			}
			if !mappingExists(t, root, ownerSID) {
				t.Errorf("a session that wasn't removed should keep its mapping")
			}
		})
	}
}

// TestRunDestroy_RefusedDestroyNeverReachesClaude pins the ordering: a
// destroy that is refused before anything is removed must not have removed
// the session either.
func TestRunDestroy_RefusedDestroyNeverReachesClaude(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	writeTestMapping(t, root, ownerSID, filepath.Join(root, "alpha"), "")
	fake := fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}}
	installFakeClaudeSessions(t, &fake)
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, false, true, "not-the-workspace")

	if err := runDestroy(destroyCmd, nil); err == nil {
		t.Fatal("a mismatched --confirm should refuse")
	}
	if fake.listCalls != 0 || len(fake.removed) != 0 {
		t.Errorf("a refused destroy reached claude: list=%d rm=%v", fake.listCalls, fake.removed)
	}
	assertInstancesExist(t, root, "alpha")
}

// TestRunDestroy_UnpushedRefusalKeepsSession pins the settled order: the
// unpushed-work check runs first, so a destroy it refuses (no --force, no
// terminal to confirm on) leaves both the instance and its owning session.
func TestRunDestroy_UnpushedRefusalKeepsSession(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	instance := filepath.Join(root, "alpha")
	repoDir := filepath.Join(instance, "myrepo")
	scanInitGitRepoForCLITest(t, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "second.txt"), []byte("ahead\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRunForCLITest(t, repoDir, "add", "second.txt")
	gitRunForCLITest(t, repoDir, "commit", "-m", "ahead")

	writeTestMapping(t, root, ownerSID, instance, "")
	fake := fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}}
	installFakeClaudeSessions(t, &fake)
	chdirTo(t, root)
	quietDestroyCmd()
	setDestroyFlags(t, false, false, "")

	err := runDestroy(destroyCmd, []string{"alpha"})
	if err == nil || !strings.Contains(err.Error(), "unpushed work") {
		t.Fatalf("destroy should refuse over unpushed work; got: %v", err)
	}
	if fake.listCalls != 0 || len(fake.removed) != 0 {
		t.Errorf("an unpushed refusal reached claude: list=%d rm=%v", fake.listCalls, fake.removed)
	}
	if !mappingExists(t, root, ownerSID) {
		t.Errorf("an unpushed refusal should keep the session's mapping")
	}
	assertInstancesExist(t, root, "alpha")
}

// TestRunDestroy_RemovesOwningSession drives `niwa destroy <name>` from the
// workspace root: the owning session is removed before the instance goes,
// and --keep-session skips the step without asking claude anything.
func TestRunDestroy_RemovesOwningSession(t *testing.T) {
	for _, keep := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "keep-session"}[keep], func(t *testing.T) {
			root := destroyTestSetup(t, []string{"alpha", "beta"})
			instance := filepath.Join(root, "alpha")
			writeTestMapping(t, root, ownerSID, instance, "")
			fake := fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}}
			installFakeClaudeSessions(t, &fake)
			chdirTo(t, root)
			stderr := quietDestroyCmd()
			setDestroyFlags(t, true, false, "")
			prevKeep := destroyKeepSession
			destroyKeepSession = keep
			t.Cleanup(func() { destroyKeepSession = prevKeep })

			if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
				t.Fatalf("destroy: %v", err)
			}
			if _, err := os.Stat(instance); !os.IsNotExist(err) {
				t.Errorf("instance should be gone: %v", err)
			}
			if keep {
				if fake.listCalls != 0 || len(fake.removed) != 0 {
					t.Errorf("--keep-session called claude: list=%d rm=%v", fake.listCalls, fake.removed)
				}
				if !mappingExists(t, root, ownerSID) {
					t.Errorf("--keep-session should keep the mapping")
				}
				return
			}
			if strings.Join(fake.removed, ",") != "11111111" {
				t.Errorf("removed %v, want [11111111]", fake.removed)
			}
			if !strings.Contains(stderr.String(), "removed Claude session 11111111") {
				t.Errorf("stderr should report the removal; got:\n%s", stderr.String())
			}
			if mappingExists(t, root, ownerSID) {
				t.Errorf("the removed session's mapping should be gone")
			}
		})
	}
}
