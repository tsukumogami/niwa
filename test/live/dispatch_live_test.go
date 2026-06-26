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
// TIMING NOTE: `claude stop` (and a self-completing session) reaches the job
// state `state=done` only after a few-second async lag. A single `niwa reap`
// fired during that lag correctly SPARES the still-non-terminal session, so the
// teardown assertion must tolerate the lag. The test therefore polls reap in a
// bounded loop rather than reaping once and asserting immediately. The
// deterministic "a live session is spared" case is covered by the OFFLINE
// functional test (which fabricates fresh job state), so this live test does not
// run a fragile real "stay-live" negative control.
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
	"regexp"
	"strings"
	"testing"
	"time"
)

// liveDispatchInstanceNameRe mirrors the CLI's isDispatchInstanceName signature:
// a "+" (end-of-config marker), an optional dash-free slug, a "-", then 8
// lowercase hex digits at the end. It matches "<config>+-<8hex>" (no-name) and
// "<config>+<slug>-<8hex>" (named); there is no "disp" literal.
var liveDispatchInstanceNameRe = regexp.MustCompile(`\+[a-z0-9_]*-[0-9a-f]{8}$`)

// liveDispatchPrompt is the trivial task the dispatched worker runs. Keeping it
// to "reply OK and stop" minimizes subscription cost per run. The session
// self-completes quickly, which (together with an explicit `claude stop`) drives
// its job state terminal so the reaper can reclaim the instance.
const liveDispatchPrompt = "reply with the single word OK and stop"

// teardownBudget bounds how long the timing-robust teardown poll runs. The
// dispatch + a self-completing trivial session + the stop->done lag all fit
// comfortably inside this window; it is generous so the few-second async lag
// after `claude stop` never causes a false failure.
const teardownBudget = 90 * time.Second

// teardownInterval is the gap between successive `niwa reap` attempts in the
// teardown poll. Each iteration reaps and then checks for destruction, so this
// is the retry cadence while the session settles to terminal.
const teardownInterval = 3 * time.Second

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
//
// Steps:
//  1. Stand up the minimal offline workspace and build the niwa binary.
//  2. `niwa dispatch <prompt> --detach` from the workspace root.
//  3. Assert exactly one well-formed `<config>+-<8hex>` instance with a
//     parseable instance.json, and an ephemeral Origin="dispatch" mapping keyed
//     on the full session UUID.
//  4. Assert the session is registered: a `claude agents --json` record whose
//     cwd is the instance dir (capture its short id and full sessionId).
//  5. `claude stop <shortId>` to exercise the stop path (tolerate already-done).
//  6. Timing-robust teardown: poll up to teardownBudget, each iteration running
//     `niwa reap` then checking whether the instance dir and mapping are gone;
//     succeed on the first clean iteration, sleep teardownInterval otherwise.
//  7. t.Cleanup stops any still-running session and removes nothing the test
//     framework does not already remove; it is best-effort and idempotent.
func TestDispatchLiveLifecycle(t *testing.T) {
	claudeBin := requireLiveClaude(t)

	niwaBin := buildNiwa(t)
	workspaceRoot := initLiveWorkspace(t, niwaBin)
	t.Logf("workspace root: %s", workspaceRoot)

	// `claude attach/logs/stop` are keyed on a session's SHORT id (the
	// ~/.claude/jobs/<short> basename), NOT the full UUID -- `claude stop <uuid>`
	// returns "No job matching ...". The cleanup collects SHORT ids so the
	// backstop teardown actually stops anything still live. Cleanup is
	// best-effort and idempotent: stopping an already-stopped session is a no-op,
	// so a failed run never leaves a live session behind.
	var startedShortIDs []string
	t.Cleanup(func() {
		for _, short := range startedShortIDs {
			_ = exec.Command(claudeBin, "stop", short).Run()
		}
		// A best-effort final reap reclaims anything stop drove terminal.
		cmd := exec.Command(niwaBin, "reap")
		cmd.Dir = workspaceRoot
		_ = cmd.Run()
	})

	// (1) Dispatch a background worker. --detach so the test process does not
	// attach a terminal; the worker runs headless and is managed via claude.
	dispatchOut := runNiwaLive(t, niwaBin, workspaceRoot, "dispatch", liveDispatchPrompt, "--detach")
	sessionID := parseDispatchedSessionID(t, dispatchOut)
	t.Logf("dispatched session %s", sessionID)

	// (2) Assert a well-constructed dedicated instance: exactly one
	// <config>+-<8hex> directory under the workspace root carrying a
	// parseable .niwa/instance.json.
	instancePath := assertDispatchInstance(t, workspaceRoot)
	t.Logf("dispatch instance: %s", instancePath)

	// (3) Assert the ephemeral dispatch-origin mapping keyed on the session UUID
	// exists in the workspace's .niwa/sessions/ and points at the instance.
	assertDispatchMapping(t, workspaceRoot, sessionID, instancePath)
	t.Logf("verified ephemeral dispatch mapping for %s", sessionID)

	// (4) Assert the session is registered with claude. Prefer matching by cwd
	// (the instance dir) and capture the short id used by stop. Resolving by cwd
	// is the most direct correlation between the instance and its session.
	shortID := resolveSessionByCwd(t, claudeBin, instancePath, sessionID)
	startedShortIDs = append(startedShortIDs, shortID)
	t.Logf("session registered with claude (short id %s, cwd %s)", shortID, instancePath)

	// (5) Stop the session to exercise the stop path. Tolerate it already being
	// terminal (a trivial prompt may self-complete first): a stop of a done
	// session is a no-op, not a failure.
	stopSession(t, claudeBin, shortID)
	t.Logf("issued claude stop %s", shortID)

	// (6) Timing-robust teardown. Poll up to teardownBudget: each iteration runs
	// `niwa reap` from the workspace root, then checks whether the instance dir
	// and its mapping file are gone. The stop->done transition is async (a
	// few-second lag), so an early reap correctly spares the still-non-terminal
	// session; the loop retries until the session settles terminal and the reap
	// reclaims it. This directly tests reaper-primary teardown using the
	// product's own liveness logic, tolerating the lag.
	reapUntilGone(t, niwaBin, workspaceRoot, instancePath, sessionID)
	t.Logf("instance and mapping reclaimed; reaper-primary teardown verified")
}

// reapUntilGone polls reaper-primary teardown to completion. It runs `niwa reap`
// then checks for destruction, retrying on teardownInterval until both the
// instance dir and the mapping are gone or teardownBudget elapses. It fails only
// if either still exists after the budget, so the few-second stop->done lag
// never causes a false failure.
func reapUntilGone(t *testing.T, niwaBin, workspaceRoot, instancePath, sessionID string) {
	t.Helper()
	mappingPath := filepath.Join(workspaceRoot, ".niwa", "sessions", sessionID+".json")
	deadline := time.Now().Add(teardownBudget)
	attempt := 0
	for {
		attempt++
		runNiwaLive(t, niwaBin, workspaceRoot, "reap")
		instGone := notExists(t, instancePath)
		mapGone := notExists(t, mappingPath)
		if instGone && mapGone {
			t.Logf("reap attempt %d reclaimed instance and mapping", attempt)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %s and %d reap attempts, teardown incomplete: instance gone=%v (%s), mapping gone=%v (%s)",
				teardownBudget, attempt, instGone, instancePath, mapGone, mappingPath)
		}
		t.Logf("reap attempt %d: instance gone=%v, mapping gone=%v; session not yet terminal, retrying in %s",
			attempt, instGone, mapGone, teardownInterval)
		time.Sleep(teardownInterval)
	}
}

// notExists reports whether path is absent. A stat error other than
// not-exist fails the test, since it means the filesystem is in an unexpected
// state rather than the path simply being gone.
func notExists(t *testing.T, path string) bool {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		return false
	} else if os.IsNotExist(err) {
		return true
	} else {
		t.Fatalf("stat %s: %v", path, err)
		return false
	}
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

// assertDispatchInstance asserts exactly one <config>+-<hex> instance exists
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

// findDispatchInstance returns the single dispatch instance directory under
// workspaceRoot, failing if zero or more than one exists.
func findDispatchInstance(t *testing.T, workspaceRoot string) string {
	t.Helper()
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		t.Fatalf("reading workspace root %s: %v", workspaceRoot, err)
	}
	var found []string
	for _, e := range entries {
		// The no-name dispatch instance is "<config>+-<hex>"; a named one is
		// "<config>+<slug>-<hex>". The structural regex matches both and is not
		// fooled by a config name that itself contains a dash (e.g. "live-disp"),
		// since it anchors on the "+...-<8hex>" tail.
		if e.IsDir() && liveDispatchInstanceNameRe.MatchString(e.Name()) {
			found = append(found, filepath.Join(workspaceRoot, e.Name()))
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly one dispatch instance under %s, found %d: %v", workspaceRoot, len(found), found)
	}
	return found[0]
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

// resolveSessionByCwd asserts the session is registered with claude and returns
// the SHORT id `claude attach/logs/stop` accept (the ~/.claude/jobs/<short>
// basename). It reads `claude agents --json` and prefers matching the record
// whose "cwd" is the instance dir -- the most direct correlation between the
// instance and its session -- falling back to matching on the full sessionId. It
// returns that record's "id". It does NOT assume the short id is the first 8
// chars of the UUID; it reads the authoritative handle claude reports. It fails
// the test if the json mode is unavailable or no record matches, because the
// live teardown depends on stopping by the correct short id.
func resolveSessionByCwd(t *testing.T, claudeBin, instancePath, fullSessionID string) string {
	t.Helper()
	records := claudeAgentRecords(t, claudeBin)

	// Prefer a record whose cwd is the instance dir.
	for _, rec := range records {
		cwd, _ := rec["cwd"].(string)
		if cwd != "" && samePath(cwd, instancePath) {
			short, _ := rec["id"].(string)
			if short == "" {
				t.Fatalf("claude agents record with cwd %s has no \"id\" (short handle)\nrecord:\n%+v", instancePath, rec)
			}
			return short
		}
	}

	// Fall back to matching on the full session id.
	for _, rec := range records {
		sid, _ := rec["sessionId"].(string)
		if sid != fullSessionID {
			continue
		}
		short, _ := rec["id"].(string)
		if short == "" {
			t.Fatalf("claude agents record for session %s has no \"id\" (short handle)", fullSessionID)
		}
		return short
	}

	t.Fatalf("no claude agents record with cwd %s or sessionId %s; the session is not registered\nrecords:\n%+v",
		instancePath, fullSessionID, records)
	return ""
}

// samePath reports whether two paths refer to the same location, comparing
// cleaned absolute forms. Claude may report a cwd with a trailing slash or a
// symlinked prefix; EvalSymlinks normalizes both when possible, and a plain
// Clean comparison is the fallback.
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return filepath.Clean(ra) == filepath.Clean(rb)
	}
	return false
}

// claudeAgentRecords runs `claude agents --json` and decodes it into a slice of
// loosely-typed records. The shape is either a top-level array of records or an
// object wrapping one (e.g. {"agents":[...]}); both are handled. It fails the
// test if the json mode is unavailable, because every live correlation depends
// on it.
func claudeAgentRecords(t *testing.T, claudeBin string) []map[string]any {
	t.Helper()
	out, err := exec.Command(claudeBin, "agents", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("claude agents --json: %v\n%s", err, out)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		t.Fatalf("claude agents --json returned empty output")
	}

	var records []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &records); err == nil {
		return records
	}

	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &wrapper); err != nil {
		t.Fatalf("claude agents --json is neither an array nor an object: %v\noutput:\n%s", err, trimmed)
	}
	for _, raw := range wrapper {
		if err := json.Unmarshal(raw, &records); err == nil && len(records) > 0 {
			return records
		}
	}
	t.Fatalf("claude agents --json object has no array of records\noutput:\n%s", trimmed)
	return nil
}

// stopSession drives the session terminal with `claude stop <short>`. It MUST
// be passed the SHORT id (the claude stop handle), not the full UUID -- the full
// UUID yields "No job matching ...". Per the reaper-primary teardown, this does
// not destroy the instance; the next reap does. A stop of an already-terminal
// session (a trivial prompt may self-complete) is tolerated: the error is logged,
// not fatal, since the teardown poll reaps it either way.
func stopSession(t *testing.T, claudeBin, shortID string) {
	t.Helper()
	if out, err := exec.Command(claudeBin, "stop", shortID).CombinedOutput(); err != nil {
		t.Logf("claude stop %s reported %v (tolerated; session may already be done)\n%s", shortID, err, out)
	}
}
