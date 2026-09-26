package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// keepalive_steps_test.go holds step definitions for the dispatch keep-alive
// workflow (DESIGN-niwa-session-keep-alive). They ride the dispatch suite's
// fake claude: its --bg records the full launch argv (including the prompt,
// which carries the arming instruction when keep-alive armed) to
// $HOME/dispatch-launch-argv, and writes the live job entry that makes the
// session read as live for `niwa list`.

// keepAliveArmingMarker is a distinctive fragment of the fixed arming
// instruction dispatch prepends to the task prompt. The full constant lives
// unexported in internal/cli (dispatch_keepalive.go); this marker asserts its
// presence end to end without duplicating the whole text.
const keepAliveArmingMarker = "arm this session's keep-alive"

// launchedClaudeArgv reads the argv line the fake claude recorded on --bg.
func launchedClaudeArgv(s *testState) (string, error) {
	path := filepath.Join(s.homeDir, "dispatch-launch-argv")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading launched claude argv %s: %w\nstdout:\n%s\nstderr:\n%s", path, err, s.stdout, s.stderr)
	}
	return string(data), nil
}

// theLaunchedClaudePromptContainsArming asserts the launch carried the fixed
// keep-alive arming instruction (dispatch prepends it to the prompt).
func theLaunchedClaudePromptContainsArming(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	argv, err := launchedClaudeArgv(s)
	if err != nil {
		return err
	}
	if !strings.Contains(argv, keepAliveArmingMarker) {
		return fmt.Errorf("launched claude argv does not carry the keep-alive arming instruction (want substring %q):\n%s", keepAliveArmingMarker, argv)
	}
	return nil
}

// theLaunchedClaudePromptDoesNotContainArming asserts the launch carried NO
// arming instruction (the non-RC and non-opted cases).
func theLaunchedClaudePromptDoesNotContainArming(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	argv, err := launchedClaudeArgv(s)
	if err != nil {
		return err
	}
	if strings.Contains(argv, keepAliveArmingMarker) {
		return fmt.Errorf("launched claude argv must not carry the arming instruction:\n%s", argv)
	}
	return nil
}

// readMappingKeepAlive decodes the session mapping and returns its keep_alive
// value plus the raw JSON (for the omission assertion).
func readMappingKeepAlive(s *testState, sessionID string) (bool, string, error) {
	path := filepath.Join(s.workspaceRoot, ".niwa", "sessions", sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "", fmt.Errorf("expected dispatch mapping at %s: %w\nstdout:\n%s\nstderr:\n%s", path, err, s.stdout, s.stderr)
	}
	var m struct {
		KeepAlive bool `json:"keep_alive"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return false, "", fmt.Errorf("parsing mapping %s: %w", path, err)
	}
	return m.KeepAlive, string(data), nil
}

// theDispatchMappingRecordsKeepAlive asserts the durable mapping carries
// keep_alive:true for the armed session.
func theDispatchMappingRecordsKeepAlive(ctx context.Context, sessionID string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	ka, raw, err := readMappingKeepAlive(s, sessionID)
	if err != nil {
		return err
	}
	if !ka {
		return fmt.Errorf("mapping for %s does not record keep-alive:\n%s", sessionID, raw)
	}
	return nil
}

// theDispatchMappingDoesNotRecordKeepAlive asserts the mapping omits the
// keep_alive key entirely (an unarmed dispatch stays byte-identical to a
// non-opted one).
func theDispatchMappingDoesNotRecordKeepAlive(ctx context.Context, sessionID string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	ka, raw, err := readMappingKeepAlive(s, sessionID)
	if err != nil {
		return err
	}
	if ka || strings.Contains(raw, "keep_alive") {
		return fmt.Errorf("mapping for %s must omit keep_alive:\n%s", sessionID, raw)
	}
	return nil
}

// theListJSONReportsDispatchInstanceKeptAlive parses the last command's stdout
// as `niwa list --json` output and asserts the dispatch instance's record
// carries keep_alive:true.
func theListJSONReportsDispatchInstanceKeptAlive(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	var records []struct {
		Name      string `json:"name"`
		Ephemeral bool   `json:"ephemeral"`
		KeepAlive bool   `json:"keep_alive"`
	}
	if err := json.Unmarshal([]byte(s.stdout), &records); err != nil {
		return fmt.Errorf("parsing niwa list --json output: %w\nstdout:\n%s", err, s.stdout)
	}
	for _, r := range records {
		if dispatchInstanceNameRe.MatchString(r.Name) {
			if !r.KeepAlive {
				return fmt.Errorf("dispatch instance %s is not reported kept-alive:\n%s", r.Name, s.stdout)
			}
			return nil
		}
	}
	return fmt.Errorf("no dispatch instance in niwa list --json output:\n%s", s.stdout)
}

// registerKeepAliveSteps wires the keep-alive workflow steps into the scenario
// context. Called from initializeScenario.
func registerKeepAliveSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the launched claude prompt contains the keep-alive arming instruction$`, theLaunchedClaudePromptContainsArming)
	ctx.Step(`^the launched claude prompt does not contain the keep-alive arming instruction$`, theLaunchedClaudePromptDoesNotContainArming)
	ctx.Step(`^the dispatch mapping for session "([^"]*)" records keep-alive$`, theDispatchMappingRecordsKeepAlive)
	ctx.Step(`^the dispatch mapping for session "([^"]*)" does not record keep-alive$`, theDispatchMappingDoesNotRecordKeepAlive)
	ctx.Step(`^the list JSON reports the dispatch instance as kept alive$`, theListJSONReportsDispatchInstanceKeptAlive)
}
