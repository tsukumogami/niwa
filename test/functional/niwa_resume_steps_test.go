package functional

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
)

// theClaudeTranscriptOfTheDispatchSessionIsKept writes the transcript Claude
// Code keeps for a session under the sandboxed home, keyed by the dispatch
// instance's directory, so `niwa resume` finds a session to resume after the
// instance is gone. The fake claude writes no transcript of its own.
//
// The directory is written under both the recorded path and its resolved
// form when they differ, since niwa derives the key from the workspace root
// it resolves and a temp directory can sit behind a symlink.
func theClaudeTranscriptOfTheDispatchSessionIsKept(ctx context.Context, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return ctx, fmt.Errorf("no dispatch instance path recorded")
	}
	paths := []string{s.lastDispatchInstancePath}
	if parent, err := filepath.EvalSymlinks(filepath.Dir(s.lastDispatchInstancePath)); err == nil {
		if resolved := filepath.Join(parent, filepath.Base(s.lastDispatchInstancePath)); resolved != paths[0] {
			paths = append(paths, resolved)
		}
	}
	for _, p := range paths {
		transcript := sessionattach.TranscriptPath(s.homeDir, p, sessionID)
		if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
			return ctx, err
		}
		if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

// theFakeClaudesLastLaunchResumedSession asserts the fake claude's most recent
// --bg launch was `claude --bg --resume <session id> ...`.
func theFakeClaudesLastLaunchResumedSession(ctx context.Context, sessionID string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	data, err := os.ReadFile(filepath.Join(s.homeDir, "dispatch-launch-argv-elements"))
	if err != nil {
		return fmt.Errorf("reading the fake claude's last launch: %w", err)
	}
	elems := bytes.Split(bytes.TrimSuffix(data, []byte{0}), []byte{0})
	if len(elems) < 3 || string(elems[0]) != "--bg" || string(elems[1]) != "--resume" || string(elems[2]) != sessionID {
		return fmt.Errorf("the last launch did not resume %s: %q", sessionID, elems)
	}
	return nil
}

// registerNiwaResumeSteps wires the `niwa resume` steps. Called from
// initializeScenario.
func registerNiwaResumeSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the Claude transcript of the dispatch session "([^"]*)" is kept$`, theClaudeTranscriptOfTheDispatchSessionIsKept)
	ctx.Step(`^the fake claude's last launch resumed session "([^"]*)"$`, theFakeClaudesLastLaunchResumedSession)
}
