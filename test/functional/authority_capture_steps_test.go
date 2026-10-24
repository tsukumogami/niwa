package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cucumber/godog"
)

// The before-capture scenarios: a dispatched worker runs `niwa authority
// check` before dispatch has captured its session id and written its mapping.
// Dispatch is held at one of two test seams (testfault's hold), so a step can
// run the check in the gap, or end the dispatch there.

// checkAtLaunch is the shell the checking fake claude runs once it has
// written its job record, as a real session would by its first command: the
// check with its own session id against its own working directory, kept in
// HOME.
const checkAtLaunch = `  ( "$FAKE_AUTHORITY_NIWA" authority check --json --session "$sid" -C "$cwd" --point merge-pr ) > "$HOME/authority-check-launch.out" 2> "$HOME/authority-check-launch.err"
  echo $? > "$HOME/authority-check-launch.exit"
  printf 'backgrounded · %s\n' "$short"`

func registerAuthorityCaptureSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a fake claude for dispatch that checks its authority once it starts, with session "([^"]*)"$`, aCheckingFakeClaude)
	ctx.Step(`^a fake codex for dispatch that records no session$`, aFakeCodexThatRecordsNoSession)
	ctx.Step(`^I run "niwa dispatch ([^"]*)" from the workspace root, held (before capture|before the mapping)$`, iRunADispatchHeld)
	ctx.Step(`^the held dispatch is killed with SIGTERM$`, theHeldDispatchIsKilled)
	ctx.Step(`^the worker's own check at launch is bound through "([^"]*)" holding "([^"]*)"$`, theWorkersOwnCheckIsBound)
	ctx.Step(`^the check reads "([^"]*)" with (binding|reason) "([^"]*)"$`, theCheckReads)
	ctx.Step(`^the check's grant holds "([^"]*)"$`, theChecksGrantHolds)
	ctx.Step(`^a session record for "([^"]*)" claims the dispatch instance$`, aSessionRecordClaimsTheInstance)
	ctx.Step(`^the dispatch instance holds no capture-pending file$`, theInstanceHoldsNoCapturePendingFile)
	ctx.Step(`^the dispatch instance holds a capture-pending file$`, theInstanceHoldsACapturePendingFile)
	ctx.Step(`^the capture-pending file names a process started after its launch$`, thePendingFileNamesALaterProcess)
	ctx.Step(`^I remember the worker's session mapping and the workspace's instances$`, iRememberTheWorkersMapping)
	ctx.Step(`^the worker's session mapping is byte-identical and no instance was added$`, theWorkersMappingIsUnchanged)
}

func aCheckingFakeClaude(ctx context.Context, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if err := installDispatchFakeClaude(s, "ok"); err != nil {
		return ctx, err
	}
	script := dispatchFakeClaudeScript("ok")
	const tail = `  printf 'backgrounded · %s\n' "$short"`
	if !strings.Contains(script, tail) {
		return ctx, fmt.Errorf("the fake claude's launch no longer ends with its backgrounded line")
	}
	script = strings.Replace(script, tail, checkAtLaunch, 1)
	if err := os.WriteFile(filepath.Join(s.pathPrefix, "claude"), []byte(script), 0o755); err != nil {
		return ctx, err
	}
	s.envOverrides["FAKE_AUTHORITY_NIWA"] = s.binPath
	s.envOverrides["FAKE_CLAUDE_SESSION_ID"] = sessionID
	return ctx, nil
}

func aFakeCodexThatRecordsNoSession(ctx context.Context) (context.Context, error) {
	ctx, err := aFakeCodexForDispatchWithSession(ctx, "01d00000-0000-7000-8000-00000000a11c")
	if err != nil {
		return ctx, err
	}
	getState(ctx).envOverrides["FAKE_CODEX_NO_RECORD"] = "1"
	return ctx, nil
}

// iRunADispatchHeld starts a dispatch from the workspace root and returns
// once it sits at the named seam, with the instance it made recorded as the
// dispatch instance. "the held dispatch is released and ends" lets it go on.
func iRunADispatchHeld(ctx context.Context, args, where string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	label := "dispatch-before-capture"
	if where == "before the mapping" {
		label = "dispatch-before-mapping"
	}
	h := &heldDispatch{release: filepath.Join(s.sandbox, "release-"+label), done: make(chan error, 1)}
	h.cmd = exec.Command(s.binPath, append([]string{"dispatch"}, strings.Fields(args)...)...)
	h.cmd.Dir = s.workspaceRoot
	h.cmd.Env = append(s.buildEnv(), "NIWA_TEST_FAULT=hold:"+h.release+"@"+label)
	h.cmd.Stdout = &h.stdout
	h.cmd.Stderr = &h.stderr
	if err := h.cmd.Start(); err != nil {
		return ctx, err
	}
	go func() { h.done <- h.cmd.Wait() }()
	treeState(s).held = h
	// A scenario that fails while the dispatch is held must not leave it
	// running into the next one; a process that already ended ignores this.
	s.cleanups = append(s.cleanups, func() { _ = h.cmd.Process.Kill() })
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(h.release + ".held"); err == nil {
			s.lastDispatchInstancePath = findDispatchInstance(s.workspaceRoot)
			if s.lastDispatchInstancePath == "" {
				return ctx, fmt.Errorf("the dispatch is held but no dispatch instance exists")
			}
			return ctx, nil
		}
		select {
		case err := <-h.done:
			return ctx, fmt.Errorf("the dispatch ended before reaching %s (%v):\n%s%s", label, err, h.stdout.String(), h.stderr.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	_ = h.cmd.Process.Kill()
	return ctx, fmt.Errorf("the dispatch never reached %s:\n%s%s", label, h.stdout.String(), h.stderr.String())
}

func theHeldDispatchIsKilled(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	h := treeState(s).held
	if h == nil {
		return ctx, fmt.Errorf("no dispatch is held")
	}
	if err := h.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return ctx, err
	}
	select {
	case <-h.done:
	case <-time.After(30 * time.Second):
		_ = h.cmd.Process.Kill()
		return ctx, fmt.Errorf("the held dispatch didn't end within 30s of SIGTERM")
	}
	treeState(s).held = nil
	return ctx, nil
}

// checkDoc parses one check document.
func checkDoc(out string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return nil, fmt.Errorf("not one check document (%v):\n%s", err, out)
	}
	if m["schema"] != "niwa.authority.check/v1" {
		return nil, fmt.Errorf("schema %v:\n%s", m["schema"], out)
	}
	return m, nil
}

func grantHolds(m map[string]any, authorityName string) bool {
	grant, _ := m["grant"].([]any)
	for _, g := range grant {
		if e, ok := g.(map[string]any); ok && e["authority"] == authorityName {
			return true
		}
	}
	return false
}

func theWorkersOwnCheckIsBound(ctx context.Context, binding, authorityName string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	base := filepath.Join(s.homeDir, "authority-check-launch")
	code, err := os.ReadFile(base + ".exit")
	if err != nil {
		return fmt.Errorf("the fake worker made no check: %w", err)
	}
	out, _ := os.ReadFile(base + ".out")
	if strings.TrimSpace(string(code)) != "0" {
		errOut, _ := os.ReadFile(base + ".err")
		return fmt.Errorf("the worker's check exited %s\n%s%s", strings.TrimSpace(string(code)), out, errOut)
	}
	m, err := checkDoc(string(out))
	if err != nil {
		return err
	}
	if m["state"] != "bound" || m["binding"] != binding || !grantHolds(m, authorityName) {
		return fmt.Errorf("the worker's own check read state %v binding %v, want bound through %s holding %s:\n%s", m["state"], m["binding"], binding, authorityName, out)
	}
	return nil
}

func theCheckReads(ctx context.Context, state, key, value string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.exitCode != 0 {
		return fmt.Errorf("the check exited %d\n%s%s", s.exitCode, s.stdout, s.stderr)
	}
	m, err := checkDoc(s.stdout)
	if err != nil {
		return err
	}
	if m["state"] != state || m[key] != value {
		return fmt.Errorf("the check read state %v %s %v, want %s %s:\n%s", m["state"], key, m[key], state, value, s.stdout)
	}
	return nil
}

func theChecksGrantHolds(ctx context.Context, authorityName string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	m, err := checkDoc(s.stdout)
	if err != nil {
		return err
	}
	if !grantHolds(m, authorityName) {
		return fmt.Errorf("the grant doesn't hold %s:\n%s", authorityName, s.stdout)
	}
	return nil
}

// aSessionRecordClaimsTheInstance writes the job record a Claude session
// started in the dispatch instance would write: a session started by hand
// there, beside or after the worker.
func aSessionRecordClaimsTheInstance(ctx context.Context, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return ctx, fmt.Errorf("no dispatch instance recorded")
	}
	doc, err := json.Marshal(map[string]string{"sessionId": sessionID, "template": "bg", "state": "running", "cwd": s.lastDispatchInstancePath})
	if err != nil {
		return ctx, err
	}
	dir := filepath.Join(s.homeDir, ".claude", "jobs", sessionID[:8])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, err
	}
	return ctx, os.WriteFile(filepath.Join(dir, "state.json"), doc, 0o644)
}

func capturePendingPath(s *testState) string {
	return filepath.Join(s.lastDispatchInstancePath, ".niwa", "capture-pending.json")
}

func theInstanceHoldsNoCapturePendingFile(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return fmt.Errorf("no dispatch instance recorded")
	}
	if _, err := os.Stat(capturePendingPath(s)); !os.IsNotExist(err) {
		return fmt.Errorf("the dispatch instance holds a capture-pending file (%v)", err)
	}
	return nil
}

func theInstanceHoldsACapturePendingFile(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if _, err := os.Stat(capturePendingPath(s)); err != nil {
		return fmt.Errorf("no capture-pending file in the dispatch instance: %w", err)
	}
	return nil
}

// thePendingFileNamesALaterProcess points the pending file at a live process
// started now, after the launch time the file records: a process id reused
// once the dispatch that wrote the file is gone.
func thePendingFileNamesALaterProcess(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path := capturePendingPath(s)
	data, err := os.ReadFile(path)
	if err != nil {
		return ctx, err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return ctx, err
	}
	// Linux reads a start time up to a second early, so start the stand-in
	// well after the launch.
	time.Sleep(1500 * time.Millisecond)
	later := exec.Command("sleep", "120")
	if err := later.Start(); err != nil {
		return ctx, err
	}
	go func() { _ = later.Wait() }()
	s.cleanups = append(s.cleanups, func() { _ = later.Process.Kill() })
	doc["pid"] = later.Process.Pid
	out, err := json.Marshal(doc)
	if err != nil {
		return ctx, err
	}
	return ctx, os.WriteFile(path, out, 0o600)
}

type rememberedMapping struct {
	path      string
	body      []byte
	instances []string
}

func iRememberTheWorkersMapping(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	sid, err := mappingFor(s, s.lastDispatchInstancePath)
	if err != nil {
		return ctx, err
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "sessions", sid+".json")
	body, err := os.ReadFile(path)
	if err != nil {
		return ctx, err
	}
	instances, err := workspaceInstances(s)
	if err != nil {
		return ctx, err
	}
	s.remembered = &rememberedMapping{path: path, body: body, instances: instances}
	return ctx, nil
}

func workspaceInstances(s *testState) ([]string, error) {
	entries, err := os.ReadDir(s.workspaceRoot)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if _, err := os.Stat(filepath.Join(s.workspaceRoot, e.Name(), ".niwa", "instance.json")); err == nil {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func theWorkersMappingIsUnchanged(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	r := s.remembered
	if r == nil {
		return fmt.Errorf("no mapping remembered")
	}
	body, err := os.ReadFile(r.path)
	if err != nil {
		return err
	}
	if string(body) != string(r.body) {
		return fmt.Errorf("the worker's mapping changed:\nbefore %s\nafter  %s", r.body, body)
	}
	instances, err := workspaceInstances(s)
	if err != nil {
		return err
	}
	if strings.Join(instances, ",") != strings.Join(r.instances, ",") {
		return fmt.Errorf("instances before %v, after %v", r.instances, instances)
	}
	return nil
}
