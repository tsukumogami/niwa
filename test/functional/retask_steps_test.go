package functional

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/testutil/claudestub"
)

// retask_steps_test.go holds the steps for `niwa retask`, seen from the
// holder of a dispatch handle. They run the compiled binary against the
// copy-rule stub of Claude Code (internal/testutil/claudestub), which starts a
// copy of a session whenever a resume carries a flag, runs before a stop
// settles, or targets a running session, and continues the same session
// otherwise. So a retask that rotated the session id, passed a flag on the
// wake, skipped the settle or left a second job entry fails these steps.

// retaskScenario is what a retask scenario remembers between steps: the stub,
// and the worker as its dispatch recorded it.
type retaskScenario struct {
	stub *claudestub.Stub

	instancePath string // the instance, symlinks resolved
	instanceName string
	sessionID    string
	handle       string
	dispatchID   string
	sessionName  string
	mappingBytes []byte

	// mark is how many stub calls the log held before the last retask.
	mark int
}

func retaskState(ctx context.Context) (*testState, *retaskScenario, error) {
	s := getState(ctx)
	if s == nil {
		return nil, nil, fmt.Errorf("no test state")
	}
	if s.retask == nil {
		return nil, nil, fmt.Errorf("the copy-rule claude stub is not installed")
	}
	return s, s.retask, nil
}

// theCopyRuleClaudeStubIsOnPath puts the stub first on PATH for every niwa
// subprocess.
func theCopyRuleClaudeStubIsOnPath(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	stub, err := claudestub.New(filepath.Join(s.sandbox, "claudestub"), s.homeDir)
	if err != nil {
		return ctx, err
	}
	s.pathPrefix = stub.BinDir
	for _, kv := range stub.Env() {
		k, v, _ := strings.Cut(kv, "=")
		s.envOverrides[k] = v
	}
	s.retask = &retaskScenario{stub: stub}
	return ctx, nil
}

// theCopyRuleClaudeStubArmsKeepAliveWorkers makes every session the stub
// starts read as an armed keep-alive worker: agents-row status idle, job
// state working, one session_cron task in flight.
func theCopyRuleClaudeStubArmsKeepAliveWorkers(ctx context.Context) (context.Context, error) {
	_, rs, err := retaskState(ctx)
	if err != nil {
		return ctx, err
	}
	return ctx, rs.stub.SetKnob("ARMED", "1")
}

// sessionMappingFiles reads every session mapping under the workspace root,
// keyed by file name.
func sessionMappingFiles(s *testState) (map[string][]byte, error) {
	dir := filepath.Join(s.workspaceRoot, ".niwa", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out[e.Name()] = data
	}
	return out, nil
}

// mappingsNaming returns the mappings that name the instance.
func mappingsNaming(s *testState, instanceName string) (map[string][]byte, error) {
	all, err := sessionMappingFiles(s)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for name, data := range all {
		var m struct {
			InstanceName string `json:"instance_name"`
		}
		if json.Unmarshal(data, &m) == nil && m.InstanceName == instanceName {
			out[name] = data
		}
	}
	return out, nil
}

// listRecordsNamed runs `niwa list --json` and returns the records for the
// instance, putting the last command's results back afterwards.
func listRecordsNamed(s *testState, instanceName string) ([]map[string]any, error) {
	stdout, stderr, code := s.stdout, s.stderr, s.exitCode
	defer func() { s.stdout, s.stderr, s.exitCode = stdout, stderr, code }()
	if err := runNiwa(s, s.workspaceRoot, "niwa list --json"); err != nil {
		return nil, err
	}
	if s.exitCode != 0 {
		return nil, fmt.Errorf("niwa list --json exited %d:\n%s", s.exitCode, s.stderr)
	}
	var records []map[string]any
	if err := json.Unmarshal([]byte(s.stdout), &records); err != nil {
		return nil, fmt.Errorf("parsing niwa list --json: %w\n%s", err, s.stdout)
	}
	var out []map[string]any
	for _, r := range records {
		if r["name"] == instanceName {
			out = append(out, r)
		}
	}
	return out, nil
}

// iDispatchTheRetaskWorkerWith runs a dispatch and records the worker it
// started: its instance, session id, short id, mapping bytes, dispatch id and
// session name.
func iDispatchTheRetaskWorkerWith(ctx context.Context, command string) (context.Context, error) {
	s, rs, err := retaskState(ctx)
	if err != nil {
		return ctx, err
	}
	if err := runNiwa(s, s.workspaceRoot, command); err != nil {
		return ctx, err
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("%s exited %d:\nstdout:\n%s\nstderr:\n%s", command, s.exitCode, s.stdout, s.stderr)
	}
	inst := findDispatchInstance(s.workspaceRoot)
	if inst == "" {
		return ctx, fmt.Errorf("no dispatch instance after %s", command)
	}
	s.lastDispatchInstancePath = inst
	resolved, err := filepath.EvalSymlinks(inst)
	if err != nil {
		return ctx, err
	}
	rs.instancePath, rs.instanceName = resolved, filepath.Base(inst)

	mappings, err := mappingsNaming(s, rs.instanceName)
	if err != nil {
		return ctx, err
	}
	if len(mappings) != 1 {
		return ctx, fmt.Errorf("%d mappings name %s after the dispatch, want 1", len(mappings), rs.instanceName)
	}
	for name, data := range mappings {
		var m struct {
			SessionID string `json:"session_id"`
			Handle    string `json:"handle"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			return ctx, err
		}
		if m.SessionID == "" || m.Handle == "" || name != m.SessionID+".json" {
			return ctx, fmt.Errorf("mapping %s records session %q and handle %q", name, m.SessionID, m.Handle)
		}
		rs.sessionID, rs.handle, rs.mappingBytes = m.SessionID, m.Handle, data
	}

	records, err := listRecordsNamed(s, rs.instanceName)
	if err != nil {
		return ctx, err
	}
	if len(records) != 1 {
		return ctx, fmt.Errorf("niwa list --json holds %d records for %s after the dispatch, want 1", len(records), rs.instanceName)
	}
	rs.dispatchID, _ = records[0]["dispatch_id"].(string)
	rs.sessionName, _ = records[0]["session_name"].(string)
	if rs.dispatchID == "" {
		return ctx, fmt.Errorf("niwa list --json records no dispatch id for %s", rs.instanceName)
	}
	return ctx, nil
}

// iRetaskTheWorkerByInstanceNameWith runs `niwa retask <instance> <prompt>`.
func iRetaskTheWorkerByInstanceNameWith(ctx context.Context, prompt string) (context.Context, error) {
	s, rs, err := retaskState(ctx)
	if err != nil {
		return ctx, err
	}
	calls, err := rs.stub.Calls()
	if err != nil {
		return ctx, err
	}
	rs.mark = len(calls)
	return ctx, runNiwa(s, s.workspaceRoot, "niwa retask "+rs.instanceName+" "+prompt)
}

// theRetaskWorkerIsStoppedWithItsJobEntryKept takes the worker's process
// away: its row loses its pid and status, and its job entry stays listed.
func theRetaskWorkerIsStoppedWithItsJobEntryKept(ctx context.Context) (context.Context, error) {
	_, rs, err := retaskState(ctx)
	if err != nil {
		return ctx, err
	}
	return ctx, rs.stub.SetAlive(rs.handle, false, "")
}

func (rs *retaskScenario) transcriptPrompts(s *testState) ([]string, error) {
	return claudestub.TranscriptPrompts(sessionattach.TranscriptPath(s.homeDir, rs.instancePath, rs.sessionID))
}

func theWorkersTranscriptHoldsThen(ctx context.Context, first, second string) error {
	s, rs, err := retaskState(ctx)
	if err != nil {
		return err
	}
	prompts, err := rs.transcriptPrompts(s)
	if err != nil {
		return err
	}
	at := -1
	for i, p := range prompts {
		if strings.Contains(p, first) {
			at = i
			break
		}
	}
	if at < 0 || at+1 >= len(prompts) || prompts[at+1] != second {
		return fmt.Errorf("session %s's transcript holds %q, want %q then %q", rs.sessionID, prompts, first, second)
	}
	return nil
}

func theWorkersTranscriptEndsWith(ctx context.Context, last string) error {
	s, rs, err := retaskState(ctx)
	if err != nil {
		return err
	}
	prompts, err := rs.transcriptPrompts(s)
	if err != nil {
		return err
	}
	if len(prompts) == 0 || prompts[len(prompts)-1] != last {
		return fmt.Errorf("session %s's transcript holds %q, want it to end with %q", rs.sessionID, prompts, last)
	}
	return nil
}

// theLastRetaskStoppedNothing asserts the last retask ran no `claude stop`.
func theLastRetaskStoppedNothing(ctx context.Context) error {
	_, rs, err := retaskState(ctx)
	if err != nil {
		return err
	}
	calls, err := rs.stub.Calls()
	if err != nil {
		return err
	}
	if n := claudestub.Count(calls[rs.mark:], "stop"); n != 0 {
		return fmt.Errorf("the last retask ran claude stop %d times on a stopped worker:\n%s", n, claudestub.Quote(calls[rs.mark:]))
	}
	return nil
}

// theWorkerIsStillOneSessionUnderItsDispatchHandle asserts, after a
// follow-up, that the worker is the session its dispatch started and nothing
// else is: one list record with its dispatch id and session name, the resume
// line under its short id, one mapping with unchanged bytes, one job entry
// rooted at the instance, one transcript, and every wake flagless.
func theWorkerIsStillOneSessionUnderItsDispatchHandle(ctx context.Context) error {
	s, rs, err := retaskState(ctx)
	if err != nil {
		return err
	}

	records, err := listRecordsNamed(s, rs.instanceName)
	if err != nil {
		return err
	}
	if len(records) != 1 {
		return fmt.Errorf("niwa list --json holds %d records for %s, want 1", len(records), rs.instanceName)
	}
	if id, _ := records[0]["dispatch_id"].(string); id != rs.dispatchID {
		return fmt.Errorf("niwa list --json reports dispatch %q for %s, want %q", id, rs.instanceName, rs.dispatchID)
	}
	if name, _ := records[0]["session_name"].(string); name != rs.sessionName {
		return fmt.Errorf("niwa list --json reports session name %q for %s, want %q", name, rs.instanceName, rs.sessionName)
	}

	stdout, stderr, code := s.stdout, s.stderr, s.exitCode
	if err := runNiwa(s, s.workspaceRoot, "niwa list"); err != nil {
		return err
	}
	listed := s.stdout
	s.stdout, s.stderr, s.exitCode = stdout, stderr, code
	if !strings.Contains(listed, "resume: claude attach "+rs.handle+"\n") {
		return fmt.Errorf("niwa list doesn't print resume: claude attach %s:\n%s", rs.handle, listed)
	}

	mappings, err := mappingsNaming(s, rs.instanceName)
	if err != nil {
		return err
	}
	if len(mappings) != 1 {
		return fmt.Errorf("%d mappings name %s, want 1", len(mappings), rs.instanceName)
	}
	if got, ok := mappings[rs.sessionID+".json"]; !ok || !bytes.Equal(got, rs.mappingBytes) {
		return fmt.Errorf("the mapping %s.json changed or is gone:\n before %s\n after  %s", rs.sessionID, rs.mappingBytes, got)
	}

	shorts, err := rs.stub.JobEntries()
	if err != nil {
		return err
	}
	var rooted []string
	for _, short := range shorts {
		cwd, err := rs.stub.Cwd(short)
		if err != nil {
			return err
		}
		if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
			cwd = resolved
		}
		if cwd == rs.instancePath || strings.HasPrefix(cwd, rs.instancePath+string(os.PathSeparator)) {
			sid, _ := rs.stub.SessionID(short)
			rooted = append(rooted, short+"="+sid)
		}
	}
	if len(rooted) != 1 || rooted[0] != rs.handle+"="+rs.sessionID {
		return fmt.Errorf("job entries rooted at the instance are %q, want only %s=%s", rooted, rs.handle, rs.sessionID)
	}

	projectDir := filepath.Dir(sessionattach.TranscriptPath(s.homeDir, rs.instancePath, rs.sessionID))
	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != rs.sessionID+".jsonl" {
			return fmt.Errorf("a transcript other than session %s's exists for the instance: %s", rs.sessionID, e.Name())
		}
	}

	calls, err := rs.stub.Calls()
	if err != nil {
		return err
	}
	for _, c := range calls {
		if !claudestub.HasPrefix(c, "--bg", "--resume") {
			continue
		}
		if len(c) != 5 || c[2] != rs.sessionID || c[3] != "--" {
			return fmt.Errorf("a wake is not exactly --bg --resume %s -- <prompt>: %q", rs.sessionID, c)
		}
	}
	return nil
}

// theWorkersSessionCronTaskIsStillListed asserts the armed worker's job state
// still holds its session_cron self-wake.
func theWorkersSessionCronTaskIsStillListed(ctx context.Context) error {
	s, rs, err := retaskState(ctx)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(s.homeDir, ".claude", "jobs", rs.handle, "state.json"))
	if err != nil {
		return err
	}
	var js struct {
		InFlight struct {
			Kinds []string `json:"kinds"`
		} `json:"inFlight"`
	}
	if err := json.Unmarshal(data, &js); err != nil {
		return err
	}
	for _, k := range js.InFlight.Kinds {
		if k == "session_cron" {
			return nil
		}
	}
	return fmt.Errorf("the worker's job state lists no session_cron task: %s", data)
}

// registerRetaskSteps wires the retask steps. Called from initializeScenario.
func registerRetaskSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the copy-rule claude stub is on PATH$`, theCopyRuleClaudeStubIsOnPath)
	ctx.Step(`^the copy-rule claude stub arms keep-alive workers$`, theCopyRuleClaudeStubArmsKeepAliveWorkers)
	ctx.Step(`^I dispatch the retask worker with "([^"]*)"$`, iDispatchTheRetaskWorkerWith)
	ctx.Step(`^I retask the worker by instance name with "([^"]*)"$`, iRetaskTheWorkerByInstanceNameWith)
	ctx.Step(`^the retask worker is stopped with its job entry kept$`, theRetaskWorkerIsStoppedWithItsJobEntryKept)
	ctx.Step(`^the worker's transcript holds "([^"]*)" then "([^"]*)"$`, theWorkersTranscriptHoldsThen)
	ctx.Step(`^the worker's transcript ends with "([^"]*)"$`, theWorkersTranscriptEndsWith)
	ctx.Step(`^the last retask stopped nothing$`, theLastRetaskStoppedNothing)
	ctx.Step(`^the worker is still one session under its dispatch handle$`, theWorkerIsStillOneSessionUnderItsDispatchHandle)
	ctx.Step(`^the worker's session_cron task is still listed$`, theWorkersSessionCronTaskIsStillListed)
}
