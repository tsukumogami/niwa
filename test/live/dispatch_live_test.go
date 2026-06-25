//go:build live

// Package live holds the gated end-to-end dispatch lifecycle test. It runs the
// REAL `claude` lifecycle against a local Claude subscription and is therefore
// compiled and run ONLY with the `live` build tag (`make test-live`); the normal
// `go test ./...` and CI never build it.
//
// Gating (PLAN Issue 7, R48): the build tag keeps the test out of the default
// build, and a `claude`-presence probe at test start skips ONLY when no usable
// `claude` is available. On a developer machine with a subscription the probe
// passes and the test RUNS -- it is never silently skipped there. In a
// credential-less CI environment the tag means it is not even built; if invoked
// anyway, the probe skips it.
//
// The test exercises the reaper-primary teardown the DESIGN specifies: a
// `claude stop` only drives the session's job state terminal; the instance is
// reclaimed by the NEXT `niwa reap`. So the lifecycle is dispatch -> assert
// well-constructed instance + registered session -> stop -> reap -> assert
// destroyed (DESIGN "Reclamation is reaper-primary").
//
// Assumptions:
//   - A real `claude` on PATH backed by a usable subscription (the gate enforces
//     presence; a credential failure surfaces as a test failure, not a skip,
//     because the operator opted in by running `make test-live`).
//   - The host has `git` for the offline bare-repo workspace fixture.
//   - The real `~/.claude/jobs` is where `claude --bg` writes job state and where
//     `niwa dispatch`/`niwa reap` read it. HOME is therefore NOT sandboxed: the
//     live claude needs the operator's real credentials. The workspace itself is
//     an isolated temp dir, and every instance/session this test creates is
//     reaped/stopped in t.Cleanup so a failed run leaves nothing behind.
package live

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// liveDispatchPrompt is the trivial task the dispatched worker runs. Keeping it
// to "reply OK and stop" minimizes subscription cost per run.
const liveDispatchPrompt = "reply with the single word OK and stop"

// requireLiveClaude is the gate. It returns the resolved claude path when a
// usable claude is present and otherwise SKIPS the test. The skip path is taken
// only when claude is genuinely unavailable, satisfying the "never silently skip
// when claude is usable" rule (R48): if LookPath succeeds we proceed and run.
func requireLiveClaude(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("live claude not available (claude not on PATH); skipping live dispatch test")
	}
	return bin
}

// TestDispatchLiveLifecycle runs the full real lifecycle. It is the feature's
// definition-of-done gate (R48): the operator runs `make test-live` on a clean
// build against a local subscription and records the result in the PR.
func TestDispatchLiveLifecycle(t *testing.T) {
	claudeBin := requireLiveClaude(t)

	niwaBin := buildNiwa(t)
	workspaceRoot := initLiveWorkspace(t, niwaBin)

	// Stop every session and reap every instance this test creates, even on
	// failure, so a failed run never leaves a real session or instance around.
	var startedSessions []string
	t.Cleanup(func() {
		for _, id := range startedSessions {
			_ = exec.Command(claudeBin, "stop", id).Run()
		}
		// A best-effort final reap reclaims anything stop drove terminal.
		cmd := exec.Command(niwaBin, "reap")
		cmd.Dir = workspaceRoot
		_ = cmd.Run()
	})

	// (1) Dispatch a background worker. --detach so the test process does not
	// attach a terminal; the worker runs headless and is managed via claude.
	dispatchOut := runNiwaLive(t, niwaBin, workspaceRoot, "dispatch", liveDispatchPrompt, "--detach")
	primaryID := parseDispatchedSessionID(t, dispatchOut)
	startedSessions = append(startedSessions, primaryID)
	t.Logf("dispatched primary session %s", primaryID)

	// (2) Assert a well-constructed dedicated instance: a <config>-disp-<hex>
	// directory under the workspace root carrying .niwa/instance.json and the
	// materialized Claude config.
	instancePath := assertDispatchInstance(t, workspaceRoot)

	// (3) Assert the ephemeral dispatch-origin mapping keyed on the session UUID
	// exists in the workspace's .niwa/sessions/.
	assertDispatchMapping(t, workspaceRoot, primaryID, instancePath)

	// (4) Assert the session is registered with claude.
	assertSessionRegistered(t, claudeBin, primaryID)

	// (5) Negative control (optional, cheap): a SECOND still-live dispatched
	// session must NOT be reclaimed by the reap that reclaims the stopped one.
	secondOut := runNiwaLive(t, niwaBin, workspaceRoot, "dispatch", liveDispatchPrompt, "--detach")
	secondID := parseDispatchedSessionID(t, secondOut)
	startedSessions = append(startedSessions, secondID)
	secondInstance := findInstanceForSession(t, workspaceRoot, secondID)
	t.Logf("dispatched negative-control session %s", secondID)
	assertSessionRegistered(t, claudeBin, secondID)

	// (6) Stop ONLY the primary session, then reap. Reclamation is
	// reaper-primary: stop drives the session terminal, the next reap reclaims.
	stopSession(t, claudeBin, primaryID)
	waitSessionTerminal(t, claudeBin, primaryID)
	runNiwaLive(t, niwaBin, workspaceRoot, "reap")

	// (7) The stopped session's instance and mapping are gone; the still-live
	// second session's instance and mapping survive.
	assertInstanceGone(t, instancePath)
	assertMappingGone(t, workspaceRoot, primaryID)
	assertInstanceExists(t, secondInstance)
	assertMappingExists(t, workspaceRoot, secondID)

	// (8) Tear down the negative-control session explicitly so the run leaves
	// nothing behind (the t.Cleanup is a backstop for the failure path).
	stopSession(t, claudeBin, secondID)
	waitSessionTerminal(t, claudeBin, secondID)
	runNiwaLive(t, niwaBin, workspaceRoot, "reap")
	assertInstanceGone(t, secondInstance)
	assertMappingGone(t, workspaceRoot, secondID)
}

// buildNiwa compiles the niwa binary into the test's temp dir and returns its
// path. Building from source keeps the live test self-contained -- it does not
// depend on a prebuilt artifact like the functional Makefile targets do.
func buildNiwa(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "niwa")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/tsukumogami/niwa/cmd/niwa")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("building niwa: %v\n%s", err, out)
	}
	return bin
}

// initLiveWorkspace stands up a minimal niwa workspace in a fresh temp dir using
// an offline local bare-repo as the config source, then returns the workspace
// root. It mirrors the functional harness's localGitServer/ConfigRepo pattern
// but inline (the live package cannot import test/functional). A single small
// source repo keeps the clone fast.
func initLiveWorkspace(t *testing.T, niwaBin string) string {
	t.Helper()

	gitRoot := t.TempDir()
	sourceURL := makeBareRepo(t, gitRoot, "app", map[string]string{".gitkeep": ""})

	workspaceTOML := `[workspace]
name = "live-disp"

[groups.apps]

[repos.app]
url = "` + sourceURL + `"
group = "apps"
`
	configURL := makeBareRepo(t, gitRoot, "live-disp", map[string]string{
		".niwa/workspace.toml": workspaceTOML,
	})

	// init --from with no name argument scaffolds in-place, so the workspace
	// root is the (empty) cwd we run from.
	workspaceRoot := t.TempDir()
	runNiwaLive(t, niwaBin, workspaceRoot, "init", "--from", configURL)
	return workspaceRoot
}

// makeBareRepo creates a bare git repo named <name>.git under root, commits the
// given files (relative path -> content) to its main branch, and returns its
// file:// URL. It is the inline analogue of the functional localGitServer.
func makeBareRepo(t *testing.T, root, name string, files map[string]string) string {
	t.Helper()
	barePath := filepath.Join(root, name+".git")
	runGit(t, "", "init", "--bare", barePath)
	runGit(t, barePath, "symbolic-ref", "HEAD", "refs/heads/main")

	// Build the commit in a throwaway working clone, then push to the bare repo.
	work := t.TempDir()
	runGit(t, "", "clone", barePath, work)
	runGit(t, work, "config", "user.email", "live-test@example.com")
	runGit(t, work, "config", "user.name", "live-test")
	runGit(t, work, "checkout", "-B", "main")
	for rel, content := range files {
		dst := filepath.Join(work, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("mkdir for fixture file %q: %v", rel, err)
		}
		if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
			t.Fatalf("writing fixture file %q: %v", rel, err)
		}
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "fixture")
	runGit(t, work, "push", "origin", "main")
	return "file://" + barePath
}

// runGit runs git in dir (or the process cwd when dir is "") and fails the test
// on any non-zero exit.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// runNiwaLive runs the niwa binary from workspaceRoot and returns its combined
// stdout, failing the test on a non-zero exit. HOME is intentionally NOT
// sandboxed: the dispatched worker is a real claude that needs the operator's
// real credentials and writes to the real ~/.claude/jobs that niwa reads.
func runNiwaLive(t *testing.T, niwaBin, workspaceRoot string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, niwaBin, args...)
	cmd.Dir = workspaceRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("niwa %s (cwd=%s): %v\nstdout:\n%s\nstderr:\n%s",
			strings.Join(args, " "), workspaceRoot, err, stdout.String(), stderr.String())
	}
	return stdout.String()
}

// parseDispatchedSessionID extracts the session UUID from dispatch's first
// output line ("Dispatched session <uuid>").
func parseDispatchedSessionID(t *testing.T, dispatchOut string) string {
	t.Helper()
	const prefix = "Dispatched session "
	for _, line := range strings.Split(dispatchOut, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			id := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if id == "" {
				t.Fatalf("dispatch printed an empty session id\noutput:\n%s", dispatchOut)
			}
			return id
		}
	}
	t.Fatalf("could not find %q line in dispatch output:\n%s", prefix, dispatchOut)
	return ""
}

// assertDispatchInstance asserts exactly one <config>-disp-<hex> instance exists
// under the workspace root with a parseable .niwa/instance.json, and returns its
// path. The materialized Claude config (the instance's .niwa tree) is created by
// the same provision path niwa create uses, so a well-formed instance.json plus
// the .niwa directory is the structural witness.
func assertDispatchInstance(t *testing.T, workspaceRoot string) string {
	t.Helper()
	inst := findDispatchInstance(t, workspaceRoot)
	statePath := filepath.Join(inst, ".niwa", "instance.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("reading instance state %s: %v", statePath, err)
	}
	var js map[string]any
	if err := json.Unmarshal(data, &js); err != nil {
		t.Fatalf("instance state %s is not well-formed JSON: %v", statePath, err)
	}
	return inst
}

// findDispatchInstance returns the single disp-* instance directory under
// workspaceRoot, failing if zero or more than one exists.
func findDispatchInstance(t *testing.T, workspaceRoot string) string {
	t.Helper()
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		t.Fatalf("reading workspace root %s: %v", workspaceRoot, err)
	}
	var found []string
	for _, e := range entries {
		if e.IsDir() && strings.Contains(e.Name(), "-disp-") {
			found = append(found, filepath.Join(workspaceRoot, e.Name()))
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one disp-* instance under %s, found %d: %v", workspaceRoot, len(found), found)
	}
	return found[0]
}

// findInstanceForSession returns the instance_path recorded in the session's
// mapping, resolving which disp-* directory belongs to that session.
func findInstanceForSession(t *testing.T, workspaceRoot, sessionID string) string {
	t.Helper()
	m := readMapping(t, workspaceRoot, sessionID)
	if m.InstancePath == "" {
		t.Fatalf("mapping for session %s has empty instance_path", sessionID)
	}
	return m.InstancePath
}

// liveMapping is the subset of the session mapping the live assertions read.
type liveMapping struct {
	SessionID    string `json:"session_id"`
	InstancePath string `json:"instance_path"`
	Ephemeral    bool   `json:"ephemeral"`
	Origin       string `json:"origin"`
}

// readMapping reads and decodes the mapping at .niwa/sessions/<id>.json.
func readMapping(t *testing.T, workspaceRoot, sessionID string) liveMapping {
	t.Helper()
	path := filepath.Join(workspaceRoot, ".niwa", "sessions", sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading session mapping %s: %v", path, err)
	}
	var m liveMapping
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parsing session mapping %s: %v", path, err)
	}
	return m
}

// assertDispatchMapping asserts the ephemeral, origin:"dispatch" mapping keyed
// on sessionID exists and points at instancePath.
func assertDispatchMapping(t *testing.T, workspaceRoot, sessionID, instancePath string) {
	t.Helper()
	m := readMapping(t, workspaceRoot, sessionID)
	if !m.Ephemeral {
		t.Errorf("mapping for %s is not ephemeral; want ephemeral:true", sessionID)
	}
	if m.Origin != "dispatch" {
		t.Errorf("mapping for %s origin = %q; want \"dispatch\"", sessionID, m.Origin)
	}
	if m.InstancePath != instancePath {
		t.Errorf("mapping for %s instance_path = %q; want %q", sessionID, m.InstancePath, instancePath)
	}
}

// assertSessionRegistered asserts claude lists the session id. It prefers a
// machine-readable `claude agents --json` and falls back to scanning the plain
// `claude agents` text, so it tolerates either output mode.
func assertSessionRegistered(t *testing.T, claudeBin, sessionID string) {
	t.Helper()
	if jsonOut, ok := claudeAgentsJSON(t, claudeBin); ok {
		if !strings.Contains(jsonOut, sessionID) {
			t.Errorf("claude agents --json does not list session %s\noutput:\n%s", sessionID, jsonOut)
		}
		return
	}
	out, err := exec.Command(claudeBin, "agents").CombinedOutput()
	if err != nil {
		t.Fatalf("claude agents: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), sessionID) {
		t.Errorf("claude agents does not list session %s\noutput:\n%s", sessionID, out)
	}
}

// claudeAgentsJSON runs `claude agents --json` and returns its output and true
// when the --json mode is supported (exit 0 with non-empty output); otherwise
// returns ("", false) so the caller falls back to the text mode.
func claudeAgentsJSON(t *testing.T, claudeBin string) (string, bool) {
	t.Helper()
	out, err := exec.Command(claudeBin, "agents", "--json").CombinedOutput()
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

// stopSession drives the session terminal with `claude stop <id>`. Per the
// reaper-primary teardown, this does not destroy the instance; the next reap
// does.
func stopSession(t *testing.T, claudeBin, sessionID string) {
	t.Helper()
	if out, err := exec.Command(claudeBin, "stop", sessionID).CombinedOutput(); err != nil {
		t.Fatalf("claude stop %s: %v\n%s", sessionID, err, out)
	}
}

// waitSessionTerminal polls the session's ~/.claude/jobs state until it is no
// longer listed by `claude agents` (or a bounded timeout elapses). It bounds the
// race between `claude stop` returning and the daemon recording the terminal
// state, so the subsequent reap sees a dead session.
func waitSessionTerminal(t *testing.T, claudeBin, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command(claudeBin, "agents").CombinedOutput()
		if err == nil && !strings.Contains(string(out), sessionID) {
			return
		}
		time.Sleep(time.Second)
	}
	// Not fatal on its own: the reaper's job-state liveness rule also treats a
	// stopped session as dead. Log so a flake is diagnosable.
	t.Logf("session %s still listed by claude agents after stop+wait; proceeding to reap", sessionID)
}

// assertInstanceGone asserts the instance directory no longer exists.
func assertInstanceGone(t *testing.T, instancePath string) {
	t.Helper()
	if _, err := os.Stat(instancePath); err == nil {
		t.Errorf("instance %s should have been reaped but still exists", instancePath)
	} else if !os.IsNotExist(err) {
		t.Errorf("stat instance %s: %v", instancePath, err)
	}
}

// assertInstanceExists asserts the instance directory still exists.
func assertInstanceExists(t *testing.T, instancePath string) {
	t.Helper()
	if _, err := os.Stat(instancePath); err != nil {
		t.Errorf("instance %s should still exist (its session is live): %v", instancePath, err)
	}
}

// assertMappingGone asserts the session mapping was deleted.
func assertMappingGone(t *testing.T, workspaceRoot, sessionID string) {
	t.Helper()
	path := filepath.Join(workspaceRoot, ".niwa", "sessions", sessionID+".json")
	if _, err := os.Stat(path); err == nil {
		t.Errorf("mapping %s should have been deleted but still exists", path)
	} else if !os.IsNotExist(err) {
		t.Errorf("stat mapping %s: %v", path, err)
	}
}

// assertMappingExists asserts the session mapping still exists.
func assertMappingExists(t *testing.T, workspaceRoot, sessionID string) {
	t.Helper()
	path := filepath.Join(workspaceRoot, ".niwa", "sessions", sessionID+".json")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("mapping %s should still exist (its session is live): %v", path, err)
	}
}
