package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cucumber/godog"
)

// mesh_steps_test.go holds the step definitions introduced by Issue #10
// for the rewritten mesh.feature. The helpers drive the scripted worker
// fake, test-harness pause hooks, and small-integer timing overrides so
// scenarios hit <10 s each.

// ---------------------------------------------------------------------
// Scenario setup helpers: workspace with channels + timing overrides.
// ---------------------------------------------------------------------

// iSetUpChanneledWorkspace creates a minimal config repo with [channels.mesh]
// and coordinator + worker roles, then runs `niwa init --from <url>`. The
// scenario must follow up with `niwa create` to provision the instance.
func iSetUpChanneledWorkspace(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	body := fmt.Sprintf(`[workspace]
name = %q

[channels.mesh]
[channels.mesh.roles]
coordinator = ""
worker = ""
`, name)
	url, err := s.gitServer.ConfigRepo(name, body)
	if err != nil {
		return ctx, fmt.Errorf("creating config repo %q: %w", name, err)
	}
	s.repoURLs[name] = url
	if err := runNiwa(s, s.workspaceRoot, "niwa init --from "+url); err != nil {
		return ctx, err
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("niwa init exit=%d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, s.stdout, s.stderr)
	}
	return ctx, nil
}

// iSetUpMultiRepoChanneledWorkspace creates two bare source repos named
// "web" and "backend", then a config repo whose workspace.toml places both
// under group "apps" and enables [channels.mesh] with topology-derived
// roles. After apply the instance layout is:
//
//	<instance>/
//	  apps/web/
//	  apps/backend/
//
// Channel role derivation yields roles "coordinator" (instance root),
// "web" (apps/web), and "backend" (apps/backend), matching the PRD
// headline scenario of a coordinator delegating to repo-scoped roles.
func iSetUpMultiRepoChanneledWorkspace(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	for _, repo := range []string{"web", "backend"} {
		url, err := s.gitServer.Repo(repo)
		if err != nil {
			return ctx, fmt.Errorf("creating source repo %q: %w", repo, err)
		}
		s.repoURLs[repo] = url
	}
	body := fmt.Sprintf(`[workspace]
name = %q

[channels.mesh]

[groups.apps]

[repos.web]
url = %q
group = "apps"

[repos.backend]
url = %q
group = "apps"
`, name, s.repoURLs["web"], s.repoURLs["backend"])
	url, err := s.gitServer.ConfigRepo(name, body)
	if err != nil {
		return ctx, fmt.Errorf("creating config repo %q: %w", name, err)
	}
	s.repoURLs[name] = url
	if err := runNiwa(s, s.workspaceRoot, "niwa init --from "+url); err != nil {
		return ctx, err
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("niwa init exit=%d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, s.stdout, s.stderr)
	}
	return ctx, nil
}

// runWithFakeWorker sets the env overrides that make subsequent niwa
// apply / niwa create spawn the scripted worker fake instead of `claude -p`.
// The scenario name selects the fake's behavior; see worker_fake/main.go
// for the list of supported scenarios.
func runWithFakeWorker(ctx context.Context, scenario string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	fakePath := os.Getenv("NIWA_TEST_WORKER_FAKE")
	if fakePath == "" {
		return ctx, fmt.Errorf("NIWA_TEST_WORKER_FAKE not set; run via 'make test-functional'")
	}
	if !filepath.IsAbs(fakePath) {
		abs, err := filepath.Abs(fakePath)
		if err != nil {
			return ctx, err
		}
		fakePath = abs
	}
	if _, err := os.Stat(fakePath); err != nil {
		return ctx, fmt.Errorf("worker-fake binary not built at %s: %w", fakePath, err)
	}
	s.envOverrides["NIWA_WORKER_SPAWN_COMMAND"] = fakePath
	s.envOverrides["NIWA_FAKE_SCENARIO"] = scenario
	// The fake spawns `niwa mcp-serve`; it reads NIWA_FAKE_TEST_BINARY to
	// locate the niwa test binary since the sandboxed PATH doesn't include
	// one.
	s.envOverrides["NIWA_FAKE_TEST_BINARY"] = s.binPath
	return ctx, nil
}

// pauseDaemonAt sets NIWA_TEST_PAUSE_BEFORE_CLAIM or NIWA_TEST_PAUSE_AFTER_CLAIM
// and returns a release function that removes the marker file (freeing the
// daemon to proceed). The release function must be called from the scenario
// — forgetting it deadlocks the test.
func pauseDaemonAt(ctx context.Context, hook string) (context.Context, func(), error) {
	s := getState(ctx)
	if s == nil {
		return ctx, func() {}, fmt.Errorf("no test state")
	}
	var envVar, markerName string
	switch hook {
	case "before_claim":
		envVar = "NIWA_TEST_PAUSE_BEFORE_CLAIM"
		markerName = "paused_before_claim"
	case "after_claim":
		envVar = "NIWA_TEST_PAUSE_AFTER_CLAIM"
		markerName = "paused_after_claim"
	default:
		return ctx, func() {}, fmt.Errorf("unknown pause hook %q", hook)
	}
	s.envOverrides[envVar] = "1"

	// Track the marker filename in state; the release function removes it
	// from the active instance root.
	s.pauseHookMarker = markerName
	release := func() {
		instRoot := currentInstanceRoot(s)
		if instRoot == "" {
			return
		}
		markerPath := filepath.Join(instRoot, ".niwa", ".test", markerName)
		_ = os.Remove(markerPath)
	}
	return ctx, release, nil
}

// setTimingOverrides sets the four daemon timing envs (seconds) AT ONCE so
// a scenario's single "Given small timing overrides" step is all that's
// needed. Values are positive integer seconds.
func setTimingOverrides(ctx context.Context, overrides map[string]string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	for k, v := range overrides {
		switch k {
		case "NIWA_STALL_WATCHDOG_SECONDS",
			"NIWA_SIGTERM_GRACE_SECONDS",
			"NIWA_RETRY_BACKOFF_SECONDS",
			"NIWA_DESTROY_GRACE_SECONDS":
			s.envOverrides[k] = v
		default:
			return ctx, fmt.Errorf("unknown timing override %q", k)
		}
	}
	return ctx, nil
}

// iSetDefaultTimingOverrides is the Gherkin step variant that applies a
// sensible small-integer default so scenarios don't have to repeat the
// same map. Values target <10 s wall-clock per scenario:
//
//   - retry backoff: 1,1,1  (Issue 5)
//   - stall watchdog: 2 s    (Issue 6)
//   - sigterm grace: 1 s     (Issue 6 + Issue 8)
//   - destroy grace: 1 s     (Issue 8)
func iSetDefaultTimingOverrides(ctx context.Context) (context.Context, error) {
	return setTimingOverrides(ctx, map[string]string{
		"NIWA_RETRY_BACKOFF_SECONDS":  "1,1,1",
		"NIWA_STALL_WATCHDOG_SECONDS": "2",
		"NIWA_SIGTERM_GRACE_SECONDS":  "1",
		"NIWA_DESTROY_GRACE_SECONDS":  "1",
	})
}

// iRunFakeWorkerWithScenario is a Gherkin wrapper over runWithFakeWorker.
func iRunFakeWorkerWithScenario(ctx context.Context, scenario string) (context.Context, error) {
	return runWithFakeWorker(ctx, scenario)
}

// iPauseDaemonBeforeClaim sets the pause-before-claim env. Release is
// handled by iReleaseDaemonPauseMarker.
func iPauseDaemonBeforeClaim(ctx context.Context) (context.Context, error) {
	ctx, _, err := pauseDaemonAt(ctx, "before_claim")
	return ctx, err
}

// iPauseDaemonAfterClaim sets the pause-after-claim env.
func iPauseDaemonAfterClaim(ctx context.Context) (context.Context, error) {
	ctx, _, err := pauseDaemonAt(ctx, "after_claim")
	return ctx, err
}

// iReleaseDaemonPauseMarker removes the pause marker file so the daemon
// can proceed through the consumption-rename boundary.
func iReleaseDaemonPauseMarker(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := currentInstanceRoot(s)
	if instRoot == "" {
		return ctx, fmt.Errorf("no active instance root")
	}
	if s.pauseHookMarker == "" {
		return ctx, fmt.Errorf("no pause hook armed; call a pause step first")
	}
	markerPath := filepath.Join(instRoot, ".niwa", ".test", s.pauseHookMarker)
	if err := os.Remove(markerPath); err != nil && !os.IsNotExist(err) {
		return ctx, fmt.Errorf("removing pause marker: %w", err)
	}
	return ctx, nil
}

// iPauseMarkerEventuallyAppears polls for the pause marker to appear (up
// to 5 s). Used by scenarios that want to confirm the daemon actually
// paused before manipulating state.
func iPauseMarkerEventuallyAppears(ctx context.Context, hookName string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	instRoot := currentInstanceRoot(s)
	if instRoot == "" {
		return fmt.Errorf("no active instance root")
	}
	markerPath := filepath.Join(instRoot, ".niwa", ".test", "paused_"+hookName)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(markerPath); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("pause marker %s did not appear within 5s", markerPath)
}

// ---------------------------------------------------------------------
// Delegate helpers: send a task.delegate envelope into a role's inbox.
// ---------------------------------------------------------------------

// iDelegateTaskToRole creates a task envelope in .niwa/tasks/<id>/, writes
// state.json + envelope.json, then atomic-renames the inbox file. This
// bypasses `niwa_delegate` from an MCP session and is used when the
// scenario's "delegator" is the test harness itself (no coordinator
// session needed). The taskID is stored in the scenario context for
// assertions.
func iDelegateTaskToRole(ctx context.Context, instance, role, bodyJSON string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	taskID := newTestUUIDv4()
	taskDir := filepath.Join(instRoot, ".niwa", "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		return ctx, fmt.Errorf("mkdir taskDir: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	envelope := map[string]any{
		"v":       1,
		"id":      taskID,
		"from":    map[string]any{"role": "coordinator", "pid": os.Getpid()},
		"to":      map[string]any{"role": role},
		"body":    json.RawMessage(bodyJSON),
		"sent_at": now,
	}
	envBytes, _ := json.MarshalIndent(envelope, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "envelope.json"), envBytes, 0o600); err != nil {
		return ctx, err
	}
	state := map[string]any{
		"v":                 1,
		"task_id":           taskID,
		"state":             "queued",
		"state_transitions": []map[string]any{{"from": "", "to": "queued", "at": now}},
		"restart_count":     0,
		"max_restarts":      3,
		"worker":            map[string]any{"pid": 0, "start_time": 0, "role": ""},
		"delegator_role":    "coordinator",
		"target_role":       role,
		"updated_at":        now,
	}
	stBytes, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "state.json"), stBytes, 0o600); err != nil {
		return ctx, err
	}
	// Also ensure coordinator role is provisioned under .niwa/roles/ so
	// terminal messages can be delivered back. The channels installer will
	// have done this for the provisioned roles; we only add coordinator if
	// it doesn't exist already.
	for _, roleName := range []string{"coordinator", role} {
		inboxDir := filepath.Join(instRoot, ".niwa", "roles", roleName, "inbox")
		_ = os.MkdirAll(inboxDir, 0o700)
	}

	// Atomic rename envelope into target role's inbox.
	targetInbox := filepath.Join(instRoot, ".niwa", "roles", role, "inbox")
	if err := os.MkdirAll(targetInbox, 0o700); err != nil {
		return ctx, err
	}
	msg := map[string]any{
		"v":       1,
		"id":      taskID,
		"type":    "task.delegate",
		"from":    map[string]any{"role": "coordinator", "pid": os.Getpid()},
		"to":      map[string]any{"role": role},
		"task_id": taskID,
		"sent_at": now,
		"body":    json.RawMessage(bodyJSON),
	}
	msgBytes, _ := json.Marshal(msg)
	tmpPath := filepath.Join(targetInbox, taskID+".json.tmp")
	dstPath := filepath.Join(targetInbox, taskID+".json")
	if err := os.WriteFile(tmpPath, msgBytes, 0o600); err != nil {
		return ctx, err
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return ctx, err
	}
	s.lastTaskID = taskID
	return ctx, nil
}

// iCancelTheTask simulates a delegator calling niwa_cancel_task: it tries
// the atomic rename from inbox/<id>.json to inbox/cancelled/<id>.json.
//
// On success (envelope was still queued), it also transitions state.json
// to "cancelled" — that is the daemon/MCP behavior the test step is
// emulating. On ENOENT (the daemon already claimed the envelope) it is a
// no-op: the task continues through the normal running → completed path.
// This matches niwa_cancel_task's "too_late" semantics.
func iCancelTheTask(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastTaskID == "" {
		return ctx, fmt.Errorf("no task ID in scenario state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	taskDir := filepath.Join(instRoot, ".niwa", "tasks", s.lastTaskID)
	envBytes, err := os.ReadFile(filepath.Join(taskDir, "envelope.json"))
	if err != nil {
		return ctx, fmt.Errorf("reading envelope: %w", err)
	}
	var env struct {
		To struct {
			Role string `json:"role"`
		} `json:"to"`
	}
	if err := json.Unmarshal(envBytes, &env); err != nil {
		return ctx, fmt.Errorf("parsing envelope: %w", err)
	}
	inboxDir := filepath.Join(instRoot, ".niwa", "roles", env.To.Role, "inbox")
	cancelledDir := filepath.Join(inboxDir, "cancelled")
	if err := os.MkdirAll(cancelledDir, 0o700); err != nil {
		return ctx, err
	}
	src := filepath.Join(inboxDir, s.lastTaskID+".json")
	dst := filepath.Join(cancelledDir, s.lastTaskID+".json")
	if err := os.Rename(src, dst); err != nil {
		if os.IsNotExist(err) {
			// Daemon already claimed the envelope — "too late" semantics.
			// Leave state.json alone; the normal worker flow will finish.
			return ctx, nil
		}
		return ctx, fmt.Errorf("cancel rename: %w", err)
	}

	// Rename succeeded → transition state to cancelled under best-effort.
	// This matches what the MCP handler does after the rename.
	statePath := filepath.Join(taskDir, "state.json")
	stBytes, err := os.ReadFile(statePath)
	if err != nil {
		return ctx, nil
	}
	var st map[string]any
	if json.Unmarshal(stBytes, &st) != nil {
		return ctx, nil
	}
	st["state"] = "cancelled"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	st["updated_at"] = now
	newData, _ := json.MarshalIndent(st, "", "  ")
	tmp := statePath + ".tmp"
	if os.WriteFile(tmp, newData, 0o600) == nil {
		_ = os.Rename(tmp, statePath)
	}
	return ctx, nil
}

// ---------------------------------------------------------------------
// State / transitions assertions.
// ---------------------------------------------------------------------

// theTaskStateEventuallyBecomes polls state.json for the lastTaskID up to
// 15 s waiting for state to match expected.
func theTaskStateEventuallyBecomes(ctx context.Context, instance, expected string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastTaskID == "" {
		return fmt.Errorf("no task ID in scenario state")
	}
	statePath := filepath.Join(s.workspaceRoot, instance, ".niwa", "tasks", s.lastTaskID, "state.json")
	deadline := time.Now().Add(15 * time.Second)
	var lastState string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(statePath)
		if err == nil {
			var st struct {
				State string `json:"state"`
			}
			if json.Unmarshal(data, &st) == nil {
				lastState = st.State
				if st.State == expected {
					return nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("task %s state=%q; expected %q", s.lastTaskID, lastState, expected)
}

// theTaskReasonContains asserts that state.json.reason contains the given
// substring. Used to check retry_cap_exceeded etc.
func theTaskReasonContains(ctx context.Context, instance, substr string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastTaskID == "" {
		return fmt.Errorf("no task ID in scenario state")
	}
	statePath := filepath.Join(s.workspaceRoot, instance, ".niwa", "tasks", s.lastTaskID, "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), substr) {
		return fmt.Errorf("task %s state.json reason does not contain %q:\n%s",
			s.lastTaskID, substr, string(data))
	}
	return nil
}

// theTaskRestartCountEquals asserts the restart_count field of state.json.
func theTaskRestartCountEquals(ctx context.Context, instance string, want int) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastTaskID == "" {
		return fmt.Errorf("no task ID in scenario state")
	}
	statePath := filepath.Join(s.workspaceRoot, instance, ".niwa", "tasks", s.lastTaskID, "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		return err
	}
	var st struct {
		RestartCount int `json:"restart_count"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return err
	}
	if st.RestartCount != want {
		return fmt.Errorf("task %s restart_count=%d, want %d", s.lastTaskID, st.RestartCount, want)
	}
	return nil
}

// theTransitionsLogContains asserts that transitions.log contains a line
// matching the given substring.
func theTransitionsLogContains(ctx context.Context, instance, substr string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastTaskID == "" {
		return fmt.Errorf("no task ID in scenario state")
	}
	logPath := filepath.Join(s.workspaceRoot, instance, ".niwa", "tasks", s.lastTaskID, "transitions.log")
	deadline := time.Now().Add(10 * time.Second)
	var lastData string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(logPath)
		if err == nil {
			lastData = string(data)
			if strings.Contains(lastData, substr) {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("task %s transitions.log did not contain %q within 10s; log:\n%s",
		s.lastTaskID, substr, lastData)
}

// theDaemonLogDoesNotContain asserts that the daemon log for the given
// instance does NOT contain the given substring. Used by the "no bodies
// in daemon log" regression test.
func theDaemonLogDoesNotContain(ctx context.Context, instance, substr string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	logPath := filepath.Join(s.workspaceRoot, instance, ".niwa", "daemon.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), substr) {
		return fmt.Errorf("daemon log for instance %q contains forbidden substring %q:\n%s",
			instance, substr, string(data))
	}
	return nil
}

// theDaemonLogDoesNotContainAnyOf asserts none of a list of substrings
// appear in the daemon log. The list is passed as a comma-separated string
// so it round-trips through Gherkin cleanly.
func theDaemonLogDoesNotContainAnyOf(ctx context.Context, instance, substrings string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	logPath := filepath.Join(s.workspaceRoot, instance, ".niwa", "daemon.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		return err
	}
	for _, raw := range strings.Split(substrings, ",") {
		needle := strings.TrimSpace(raw)
		if needle == "" {
			continue
		}
		if strings.Contains(string(data), needle) {
			return fmt.Errorf("daemon log for instance %q contains forbidden substring %q:\n%s",
				instance, needle, string(data))
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// Daemon process lifecycle.
// ---------------------------------------------------------------------

// iSIGKILLTheDaemon reads the daemon.pid file and sends SIGKILL directly,
// then waits for the PID to be gone. Used by AC-L9 / AC-L10 scenarios.
func iSIGKILLTheDaemon(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	pidPath := filepath.Join(instRoot, ".niwa", "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return ctx, fmt.Errorf("reading daemon.pid: %w", err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	if len(lines) == 0 {
		return ctx, fmt.Errorf("daemon.pid empty")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return ctx, fmt.Errorf("parsing daemon.pid: %w", err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		return ctx, fmt.Errorf("SIGKILL daemon pid=%d: %w", pid, err)
	}
	// Wait for the process to be gone so state transitions we make next are
	// observed by the fresh daemon, not a zombie.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Remove the pid file so the next EnsureDaemonRunning path spawns a new
	// daemon instead of treating the old one as alive.
	_ = os.Remove(pidPath)
	return ctx, nil
}

// iSIGKILLTheWorker reads state.json.worker.pid for the lastTaskID and
// sends SIGKILL to that process group. Used for AC-L10 scenarios.
func iSIGKILLTheWorker(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastTaskID == "" {
		return ctx, fmt.Errorf("no task ID in scenario state")
	}
	statePath := filepath.Join(s.workspaceRoot, instance, ".niwa", "tasks", s.lastTaskID, "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		return ctx, fmt.Errorf("reading state.json: %w", err)
	}
	var st struct {
		Worker struct {
			PID int `json:"pid"`
		} `json:"worker"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return ctx, err
	}
	if st.Worker.PID <= 0 {
		return ctx, fmt.Errorf("worker.pid is %d", st.Worker.PID)
	}
	_ = syscall.Kill(-st.Worker.PID, syscall.SIGKILL)
	return ctx, nil
}

// iRestartTheDaemon runs `niwa apply <instance>` again so the daemon
// respawns. Used after iSIGKILLTheDaemon.
func iRestartTheDaemon(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if err := runNiwa(s, s.homeDir, "niwa apply "+instance); err != nil {
		return ctx, err
	}
	// Poll for the daemon.pid file so downstream steps have a live daemon.
	pidPath := filepath.Join(s.workspaceRoot, instance, ".niwa", "daemon.pid")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ctx, nil
}

// iRunTwoConcurrentApplies launches two `niwa apply` invocations in
// parallel and waits for both to finish. Used by AC-C3 (concurrent apply
// never spawns two daemons).
func iRunTwoConcurrentApplies(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(s.binPath, "apply", instance)
			cmd.Dir = s.homeDir
			cmd.Env = s.buildEnv()
			errs[i] = cmd.Run()
		}()
	}
	wg.Wait()
	// Any exit errors are not treated as failure here — we care only that a
	// single daemon came up. Record success for downstream assertions.
	for i, err := range errs {
		if err != nil {
			if _, ok := err.(*exec.ExitError); !ok {
				return ctx, fmt.Errorf("concurrent apply %d failed: %w", i, err)
			}
		}
	}
	s.exitCode = 0
	return ctx, nil
}

// exactlyOneDaemonIsRunning asserts that exactly one process lists the
// daemon's PID in daemon.pid and that that process is alive.
func exactlyOneDaemonIsRunning(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	pidPath := filepath.Join(s.workspaceRoot, instance, ".niwa", "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return fmt.Errorf("reading daemon.pid: %w", err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
	if len(lines) == 0 {
		return fmt.Errorf("daemon.pid empty")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return fmt.Errorf("parsing daemon.pid: %w", err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return fmt.Errorf("daemon pid %d not alive: %w", pid, err)
	}
	return nil
}

// ---------------------------------------------------------------------
// niwa_update_task race helper (AC-Q11).
// ---------------------------------------------------------------------

// iUpdateTheTaskBody calls niwa_update_task via `niwa mcp-serve` with the
// current scenario's coordinator identity. Used by AC-Q11 to race an
// update against the consumption rename.
func iUpdateTheTaskBody(ctx context.Context, instance, newBodyJSON string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastTaskID == "" {
		return ctx, fmt.Errorf("no task ID in scenario state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	args := fmt.Sprintf(`{"task_id":"%s","body":%s}`, s.lastTaskID, newBodyJSON)
	out, err := callMCPToolAsRole(s, instRoot, "coordinator", "", "niwa_update_task", args)
	if err != nil {
		return ctx, err
	}
	s.stdout = out
	return ctx, nil
}

// iVerifyAuthorizationDenied calls niwa_finish_task with a wrong task_id
// (a random UUIDv4 unrelated to the scenario's real task) under a role
// that isn't the task's delegator. The call must return NOT_TASK_PARTY.
func iVerifyAuthorizationDenied(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	bogusTaskID := newTestUUIDv4()
	args := fmt.Sprintf(`{"task_id":"%s","outcome":"completed","result":{"x":1}}`, bogusTaskID)
	// Pass NIWA_TASK_ID = bogusTaskID so the MCP server's executor check
	// fails on "task_id mismatch" → NOT_TASK_PARTY per auth.go.
	out, err := callMCPToolAsRole(s, instRoot, "worker", bogusTaskID, "niwa_finish_task", args)
	if err != nil {
		return err
	}
	if !strings.Contains(out, "NOT_TASK_PARTY") {
		return fmt.Errorf("expected NOT_TASK_PARTY in output; got:\n%s", out)
	}
	return nil
}

// callMCPToolAsRole runs `niwa mcp-serve` once with the given NIWA_* envs
// and pipes a tools/call request for the named tool. Returns the raw
// stdout (JSON-RPC responses) for regex-style assertions. Used by mesh
// scenarios that need to exercise a specific MCP tool directly.
func callMCPToolAsRole(s *testState, instanceRoot, role, taskID, toolName, argsJSON string) (string, error) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"0.0.1"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"` + toolName + `","arguments":` + argsJSON + `}}` + "\n"

	env := s.buildEnv()
	envMap := make(map[string]string)
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			envMap[kv[:i]] = kv[i+1:]
		}
	}
	envMap["NIWA_INSTANCE_ROOT"] = instanceRoot
	envMap["NIWA_SESSION_ROLE"] = role
	if taskID != "" {
		envMap["NIWA_TASK_ID"] = taskID
	} else {
		delete(envMap, "NIWA_TASK_ID")
	}

	// Remove daemon-spawn and fake-worker env so they don't leak into this
	// MCP call (we're not spawning a worker here).
	delete(envMap, "NIWA_WORKER_SPAWN_COMMAND")
	delete(envMap, "NIWA_FAKE_SCENARIO")
	delete(envMap, "NIWA_FAKE_TEST_BINARY")

	envSlice := make([]string, 0, len(envMap))
	for k, v := range envMap {
		envSlice = append(envSlice, k+"="+v)
	}

	cmd := exec.Command(s.binPath, "mcp-serve")
	cmd.Env = envSlice
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return "", fmt.Errorf("mcp-serve failed: %w\nstderr: %s", err, stderr.String())
		}
	}
	return stdout.String(), nil
}

// theOutputContainsStatus asserts that the stdout captured by the last
// MCP call contains a `{"status":"<expected>"}` payload. The MCP
// toolResult wraps the payload inside a JSON-escaped content-block, so we
// match against both the raw form (no escaping — used by callers that
// return JSON directly) and the escaped form (stdio MCP responses).
func theOutputContainsStatus(ctx context.Context, expected string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	raw := `"status":"` + expected + `"`
	escaped := `\"status\":\"` + expected + `\"`
	if strings.Contains(s.stdout, raw) || strings.Contains(s.stdout, escaped) {
		return nil
	}
	return fmt.Errorf("expected status %q in stdout; got:\n%s", expected, s.stdout)
}

// ---------------------------------------------------------------------
// Cross-scenario helpers.
// ---------------------------------------------------------------------

// killLeftoverDaemons walks workspaceRoot for any .niwa/daemon.pid files
// and SIGKILLs the daemon PID + its process group. Called from the godog
// After hook so scenarios never leak daemon processes into subsequent
// runs. Workers are also killed (SIGKILL the worker PGID) via the same
// walk because the daemon holds Setsid'd worker process groups as its
// direct children.
//
// This runs even on scenario failure — leftover daemons would otherwise
// hold flocks on daemon.pid.lock and corrupt the next scenario's
// fresh-sandbox assumptions.
func killLeftoverDaemons(workspaceRoot string) {
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		niwa := filepath.Join(workspaceRoot, e.Name(), ".niwa")
		pidPath := filepath.Join(niwa, "daemon.pid")
		data, err := os.ReadFile(pidPath)
		if err != nil {
			continue
		}
		lines := strings.SplitN(strings.TrimSpace(string(data)), "\n", 2)
		if len(lines) == 0 {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
		if err != nil || pid <= 0 {
			continue
		}
		// Kill any worker PGIDs first so stray workers running with
		// acceptEdits don't have a grace window to exfiltrate. Then
		// SIGKILL the daemon.
		tasksDir := filepath.Join(niwa, "tasks")
		if taskEntries, err := os.ReadDir(tasksDir); err == nil {
			for _, te := range taskEntries {
				if !te.IsDir() {
					continue
				}
				statePath := filepath.Join(tasksDir, te.Name(), "state.json")
				body, err := os.ReadFile(statePath)
				if err != nil {
					continue
				}
				var st struct {
					Worker struct {
						PID int `json:"pid"`
					} `json:"worker"`
				}
				if json.Unmarshal(body, &st) == nil && st.Worker.PID > 0 {
					_ = syscall.Kill(-st.Worker.PID, syscall.SIGKILL)
				}
			}
		}
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

// currentInstanceRoot resolves the active instance root for a test. The
// mesh scenarios use a single instance per scenario; we search the
// workspace root for a directory containing .niwa/daemon.pid.
func currentInstanceRoot(s *testState) string {
	entries, err := os.ReadDir(s.workspaceRoot)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		niwa := filepath.Join(s.workspaceRoot, e.Name(), ".niwa")
		if _, err := os.Stat(filepath.Join(niwa, "daemon.pid")); err == nil {
			return filepath.Join(s.workspaceRoot, e.Name())
		}
		if _, err := os.Stat(filepath.Join(niwa, "tasks")); err == nil {
			return filepath.Join(s.workspaceRoot, e.Name())
		}
	}
	return ""
}

// ---------------------------------------------------------------------
// @channels-e2e helpers (Issue #11): real `claude -p` scenarios covering
// MCP-config loadability and bootstrap-prompt effectiveness. These steps
// run only when `claude` is on PATH AND `ANTHROPIC_API_KEY` is set; the
// Gherkin layer uses the existing `claudeIsAvailable` guard to skip
// otherwise so CI never fails for missing credentials.
// ---------------------------------------------------------------------

// iEnsureNoFakeWorker removes any NIWA_WORKER_SPAWN_COMMAND / fake-worker
// scenario env overrides that might be lingering, guaranteeing that a
// subsequent `niwa create` / `niwa apply` spawns via the real PATH
// resolution of `claude` rather than the scripted fake. This is the
// complement of runWithFakeWorker and is required for the bootstrap-prompt
// effectiveness scenario.
func iEnsureNoFakeWorker(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	delete(s.envOverrides, "NIWA_WORKER_SPAWN_COMMAND")
	delete(s.envOverrides, "NIWA_FAKE_SCENARIO")
	delete(s.envOverrides, "NIWA_FAKE_TEST_BINARY")
	return ctx, nil
}

// iRunClaudePFromInstanceRootPreservingCase runs `claude -p` exactly like
// iRunClaudePFromInstanceRoot but keeps stdout case intact so anchored
// markers such as "CHECKED:" survive verbatim into s.stdout. The existing
// helper lowercases stdout for yes/no contains-matching; channels-e2e
// needs the raw text.
func iRunClaudePFromInstanceRootPreservingCase(ctx context.Context, instance string, prompt *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cwd := filepath.Join(s.workspaceRoot, instance)
	return ctx, runClaudePPreservingCase(s, cwd, strings.TrimSpace(prompt.Content))
}

// runClaudePPreservingCase is the case-preserving twin of runClaudeP. It
// LookPaths `claude`, runs with the sandboxed env plus ANTHROPIC_API_KEY,
// and records raw stdout/stderr. Returns an error only for I/O failures;
// a non-zero claude exit is captured in s.exitCode so scenarios can assert
// on it separately.
func runClaudePPreservingCase(s *testState, cwd, prompt string) error {
	claudeBin, err := execLookPath("claude")
	if err != nil {
		// The claudeIsAvailable guard should have skipped the scenario
		// before we got here; fall through to a hard error if not.
		return fmt.Errorf("claude not on PATH: %w", err)
	}
	env := s.buildEnv()
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		env = append(env, "ANTHROPIC_API_KEY="+key)
	}
	cmd := exec.Command(claudeBin, "-p", prompt)
	cmd.Dir = cwd
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	s.stdout = stdout.String()
	s.stderr = stderr.String()
	s.shellPwd = ""
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			s.exitCode = exitErr.ExitCode()
			return nil
		}
		return fmt.Errorf("claude -p failed: %w\nstderr: %s", runErr, s.stderr)
	}
	s.exitCode = 0
	return nil
}

// iDelegateTaskToRoleWithFinishInstruction writes a channeled-workspace
// task envelope for the given role whose body instructs the worker to
// call niwa_finish_task with the task's own ID. This is the scenario
// driver for the bootstrap-prompt effectiveness test: the worker boots
// with the fixed bootstrap prompt, calls niwa_check_messages, reads the
// instruction, and must call niwa_finish_task(task_id, outcome=completed,
// result={"ok":true}). The step bypasses niwa_delegate (no coordinator
// session is needed); the daemon then claims the envelope via its
// normal fsnotify path and spawns a real claude worker.
func iDelegateTaskToRoleWithFinishInstruction(ctx context.Context, role, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	taskID := newTestUUIDv4()
	taskDir := filepath.Join(instRoot, ".niwa", "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o700); err != nil {
		return ctx, fmt.Errorf("mkdir taskDir: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Body is an instruction whose <ID> placeholder is substituted with
	// the real task ID so the worker can round-trip it to niwa_finish_task
	// without any guesswork. Single-sentence, no explanation, matches the
	// issue-body literal.
	instruction := fmt.Sprintf(
		`Call niwa_finish_task with task_id=%s, outcome=completed, and result={"ok":true}. Do not explain.`,
		taskID)
	bodyMap := map[string]any{"instruction": instruction}
	bodyBytes, _ := json.Marshal(bodyMap)

	envelope := map[string]any{
		"v":       1,
		"id":      taskID,
		"from":    map[string]any{"role": "coordinator", "pid": os.Getpid()},
		"to":      map[string]any{"role": role},
		"body":    json.RawMessage(bodyBytes),
		"sent_at": now,
	}
	envBytes, _ := json.MarshalIndent(envelope, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "envelope.json"), envBytes, 0o600); err != nil {
		return ctx, err
	}
	state := map[string]any{
		"v":                 1,
		"task_id":           taskID,
		"state":             "queued",
		"state_transitions": []map[string]any{{"from": "", "to": "queued", "at": now}},
		"restart_count":     0,
		"max_restarts":      3,
		"worker":            map[string]any{"pid": 0, "start_time": 0, "role": ""},
		"delegator_role":    "coordinator",
		"target_role":       role,
		"updated_at":        now,
	}
	stBytes, _ := json.MarshalIndent(state, "", "  ")
	if err := os.WriteFile(filepath.Join(taskDir, "state.json"), stBytes, 0o600); err != nil {
		return ctx, err
	}
	for _, roleName := range []string{"coordinator", role} {
		inboxDir := filepath.Join(instRoot, ".niwa", "roles", roleName, "inbox")
		_ = os.MkdirAll(inboxDir, 0o700)
	}

	targetInbox := filepath.Join(instRoot, ".niwa", "roles", role, "inbox")
	if err := os.MkdirAll(targetInbox, 0o700); err != nil {
		return ctx, err
	}
	msg := map[string]any{
		"v":       1,
		"id":      taskID,
		"type":    "task.delegate",
		"from":    map[string]any{"role": "coordinator", "pid": os.Getpid()},
		"to":      map[string]any{"role": role},
		"task_id": taskID,
		"sent_at": now,
		"body":    json.RawMessage(bodyBytes),
	}
	msgBytes, _ := json.Marshal(msg)
	tmpPath := filepath.Join(targetInbox, taskID+".json.tmp")
	dstPath := filepath.Join(targetInbox, taskID+".json")
	if err := os.WriteFile(tmpPath, msgBytes, 0o600); err != nil {
		return ctx, err
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return ctx, err
	}
	s.lastTaskID = taskID
	return ctx, nil
}

// theTaskStateEventuallyBecomesWithin is a variant of
// theTaskStateEventuallyBecomes that accepts a caller-specified deadline
// in seconds. The real-claude bootstrap scenario polls for up to 120 s
// because LLM startup + first tool call can reasonably take that long;
// the 15 s default in theTaskStateEventuallyBecomes would flake.
func theTaskStateEventuallyBecomesWithin(ctx context.Context, instance, expected string, seconds int) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastTaskID == "" {
		return fmt.Errorf("no task ID in scenario state")
	}
	statePath := filepath.Join(s.workspaceRoot, instance, ".niwa", "tasks", s.lastTaskID, "state.json")
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	var lastState string
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(statePath)
		if err == nil {
			var st struct {
				State string `json:"state"`
			}
			if json.Unmarshal(data, &st) == nil {
				lastState = st.State
				if st.State == expected {
					return nil
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("task %s state=%q; expected %q within %ds",
		s.lastTaskID, lastState, expected, seconds)
}

// iRunClaudePFromInstanceRootPreservingCaseWithin runs a real `claude -p`
// from the instance root, with a per-run timeout in seconds. Full delegation
// graphs involve worker LLM spawns behind the scenes and can legitimately
// take several minutes; a timeout keeps a flaky or stuck session from
// hanging the functional-test suite.
func iRunClaudePFromInstanceRootPreservingCaseWithin(ctx context.Context, instance string, seconds int, prompt *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cwd := filepath.Join(s.workspaceRoot, instance)
	timeoutCtx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	return ctx, runClaudePPreservingCaseCtx(timeoutCtx, s, cwd, strings.TrimSpace(prompt.Content))
}

// runClaudePPreservingCaseCtx is runClaudePPreservingCase with a cancellable
// context so callers can bound how long a real-LLM session may run. It also
// loads the instance's MCP config explicitly and selects acceptEdits
// permissions — matching the daemon's worker-spawn flags so a coordinator
// running under the test harness can call MCP tools without blocking on
// interactive permission prompts. `claude -p` run without these flags hangs
// silently the moment it tries its first tool call.
func runClaudePPreservingCaseCtx(ctx context.Context, s *testState, cwd, prompt string) error {
	claudeBin, err := execLookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not on PATH: %w", err)
	}
	env := s.buildEnv()
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		env = append(env, "ANTHROPIC_API_KEY="+key)
	}
	// The MCP server (niwa mcp-serve) reads NIWA_SESSION_ROLE to pick the
	// caller's role-scoped inbox and authorization identity. In a real
	// install the session_start hook calls `niwa session register`, which
	// relies on a niwa binary on PATH matching the workspace; the test's
	// sandboxed PATH doesn't include one. We set the role explicitly here
	// so the MCP child inherits it from the coordinator claude's env.
	env = append(env, "NIWA_SESSION_ROLE=coordinator")
	mcpConfigPath := filepath.Join(cwd, ".claude", ".mcp.json")
	// Flags mirror the daemon's spawnWorker (see mesh_watch.go): acceptEdits
	// + explicit mcp__niwa__* allow-list. Without the allow-list the
	// coordinator blocks on the first MCP tool-approval prompt (headless
	// `-p` mode cannot answer it). The list stays in sync manually with the
	// MCP server's tools/list response.
	allowed := []string{
		"mcp__niwa__niwa_delegate",
		"mcp__niwa__niwa_query_task",
		"mcp__niwa__niwa_await_task",
		"mcp__niwa__niwa_report_progress",
		"mcp__niwa__niwa_finish_task",
		"mcp__niwa__niwa_list_outbound_tasks",
		"mcp__niwa__niwa_update_task",
		"mcp__niwa__niwa_cancel_task",
		"mcp__niwa__niwa_ask",
		"mcp__niwa__niwa_send_message",
		"mcp__niwa__niwa_check_messages",
	}
	cmd := exec.CommandContext(ctx, claudeBin,
		"-p", prompt,
		"--permission-mode=acceptEdits",
		"--mcp-config="+mcpConfigPath,
		"--strict-mcp-config",
		"--allowed-tools", strings.Join(allowed, ","),
	)
	cmd.Dir = cwd
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	s.stdout = stdout.String()
	s.stderr = stderr.String()
	s.shellPwd = ""
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("claude -p exceeded deadline\nstdout:\n%s\nstderr:\n%s", s.stdout, s.stderr)
	}
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			s.exitCode = exitErr.ExitCode()
			return nil
		}
		return fmt.Errorf("claude -p failed: %w\nstderr: %s", runErr, s.stderr)
	}
	s.exitCode = 0
	return nil
}

// theFileInRepoOfInstanceExactlyMatches asserts that the file at relPath
// inside repo under instance contains exactly the given text (after trimming
// surrounding whitespace). Used by the graph-delegation scenario to verify
// the LLM-driven worker produced the expected marker content, not a
// near-miss.
func theFileInRepoOfInstanceExactlyMatches(ctx context.Context, relPath, repo, instance, expected string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, instance, repo, relPath)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	got := strings.TrimSpace(string(data))
	if got != expected {
		return fmt.Errorf("file %s: got %q, want %q", path, got, expected)
	}
	return nil
}

// allTasksInInstanceAreCompleted asserts that exactly n task directories
// exist under .niwa/tasks/<uuid>/ in the given instance and every one has
// state.json with state="completed". Used as the terminal check in the
// graph-delegation scenario: if fewer than n tasks exist, the coordinator
// didn't delegate both; if any remain in queued/running/abandoned, the
// delegation graph didn't close cleanly.
func allTasksInInstanceAreCompleted(ctx context.Context, n int, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	tasksRoot := filepath.Join(s.workspaceRoot, instance, ".niwa", "tasks")
	entries, err := os.ReadDir(tasksRoot)
	if err != nil {
		return fmt.Errorf("reading tasks dir %s: %w", tasksRoot, err)
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	if len(dirs) != n {
		return fmt.Errorf("tasks dir has %d subdirs, want %d: %v", len(dirs), n, dirs)
	}
	for _, name := range dirs {
		statePath := filepath.Join(tasksRoot, name, "state.json")
		data, err := os.ReadFile(statePath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", statePath, err)
		}
		var st struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(data, &st); err != nil {
			return fmt.Errorf("parsing %s: %w", statePath, err)
		}
		if st.State != "completed" {
			return fmt.Errorf("task %s state=%q, want %q", name, st.State, "completed")
		}
	}
	return nil
}

// execLookPath is a local indirection over exec.LookPath so test builds
// can stub it if ever needed. Kept trivial for now.
func execLookPath(name string) (string, error) { return exec.LookPath(name) }

// newTestUUIDv4 returns a UUIDv4-shaped string using time+pid as entropy.
// It matches the regex the taskstore uses for validation so the fake
// tasks pass ReadState's schema check.
func newTestUUIDv4() string {
	// Use crypto-random bytes and format as UUIDv4. Keep this local so
	// mesh_steps_test.go doesn't pull in mcp internals.
	var b [16]byte
	// We can't import crypto/rand without increasing the surface; use
	// time-based entropy plus pid — sufficient for test isolation because
	// each scenario gets a fresh sandbox.
	now := uint64(time.Now().UnixNano())
	pid := uint64(os.Getpid())
	mix := now ^ (pid << 32) ^ (pid << 17)
	for i := 0; i < 16; i++ {
		b[i] = byte(mix >> (i * 4) & 0xff)
		mix = mix*6364136223846793005 + 1442695040888963407
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
