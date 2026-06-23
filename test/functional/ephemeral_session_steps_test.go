package functional

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"
)

// ephemeral_session_steps_test.go holds step definitions for the
// ephemeral-per-session-instance integration (DESIGN-ephemeral-session-instances,
// PLAN Issue 9). The runtime steps drive `niwa instance from-hook` with synthetic
// Claude SessionStart/SessionEnd JSON on stdin and `niwa reap` against the
// workspace root, exercising the end-to-end provision/teardown and orphan-reaper
// paths WITHOUT a real Claude.
//
// The whole feature is hermetic: the SessionStart guard and the reaper read the
// session's Claude Code job state from $HOME/.claude/jobs/<session-id>/state.json
// (via defaultJobsDir, which resolves ~/.claude/jobs). The functional sandbox
// points HOME into the per-scenario sandbox, so seeding and removing a job-state
// fixture there controls the guard and the liveness rule without touching the
// developer's real ~/.claude.

// aBackgroundJobStateForSession seeds the Claude Code job state that marks a
// session as a dispatched background worker: $HOME/.claude/jobs/<sid>/state.json
// with template "bg" and a non-terminal state. This is the fixture the
// SessionStart guard reads to confirm a worker (DESIGN Decision 3) and the same
// source the reaper's liveness rule keys on. Present state.json => the session
// is live; removing it (see aDeadSessionForSession) makes the session dead.
func aBackgroundJobStateForSession(ctx context.Context, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	jobDir := filepath.Join(s.homeDir, ".claude", "jobs", sessionID)
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return ctx, fmt.Errorf("creating job-state dir %q: %w", jobDir, err)
	}
	state := fmt.Sprintf(`{"sessionId":%q,"template":"bg","state":"running"}`, sessionID)
	if err := os.WriteFile(filepath.Join(jobDir, "state.json"), []byte(state), 0o644); err != nil {
		return ctx, fmt.Errorf("writing job-state file: %w", err)
	}
	return ctx, nil
}

// aDeadSessionForSession makes a session dead by removing its Claude Code job
// state under $HOME/.claude/jobs/<sid>/. A gone job entry is the reaper's
// primary liveness signal (DESIGN Decision 6, R11): the session ended without a
// clean SessionEnd, so the ephemeral instance it provisioned is now an orphan.
func aDeadSessionForSession(ctx context.Context, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	jobDir := filepath.Join(s.homeDir, ".claude", "jobs", sessionID)
	if err := os.RemoveAll(jobDir); err != nil {
		return ctx, fmt.Errorf("removing job-state dir %q: %w", jobDir, err)
	}
	return ctx, nil
}

// iPipeASessionStartHookForSession drives `niwa instance from-hook` with a
// SessionStart hook JSON on stdin for the given session id. The hook's cwd is
// the workspace root (the launch root a dispatched session reports) so
// resolveHookWorkspaceRoot resolves it; the binary runs from the workspace root
// too. On a passing guard the command provisions an ephemeral instance, writes
// the session->instance mapping, and emits additionalContext.
func iPipeASessionStartHookForSession(ctx context.Context, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	payload := fmt.Sprintf(
		`{"hook_event_name":"SessionStart","session_id":%q,"cwd":%q,"transcript_path":%q,"source":"startup"}`,
		sessionID, s.workspaceRoot, filepath.Join(s.tmpDir, sessionID+".jsonl"),
	)
	return ctx, runNiwaWithStdin(s, s.workspaceRoot, "niwa instance from-hook", payload)
}

// iPipeASessionEndHookForSession drives `niwa instance from-hook` with a
// SessionEnd hook JSON on stdin for the given session id. SessionEnd resolves
// the instance from the mapping BY session_id (never from cwd) and
// force-destroys it when the mapping is marked ephemeral, then deletes the
// mapping entry.
func iPipeASessionEndHookForSession(ctx context.Context, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	payload := fmt.Sprintf(
		`{"hook_event_name":"SessionEnd","session_id":%q,"cwd":%q}`,
		sessionID, s.workspaceRoot,
	)
	return ctx, runNiwaWithStdin(s, s.workspaceRoot, "niwa instance from-hook", payload)
}

// iRunNiwaReapFromTheWorkspaceRoot runs `niwa reap` from the workspace root so
// ClassifyCwd resolves the workspace root and the reaper sweeps its instances.
func iRunNiwaReapFromTheWorkspaceRoot(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, runNiwa(s, s.workspaceRoot, "niwa reap")
}

// theSessionMappingExistsForSession asserts that the workspace-root mapping
// store carries an entry for the session at .niwa/sessions/<sid>.json (proving
// SessionStart wrote it).
func theSessionMappingExistsForSession(ctx context.Context, sessionID string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "sessions", sessionID+".json")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("expected session mapping at %s: %w\nstdout:\n%s\nstderr:\n%s", path, err, s.stdout, s.stderr)
	}
	return nil
}

// theSessionMappingDoesNotExistForSession asserts the mapping entry for the
// session is absent (proving SessionEnd / reap removed it).
func theSessionMappingDoesNotExistForSession(ctx context.Context, sessionID string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "sessions", sessionID+".json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("expected session mapping %s to be removed, but it still exists", path)
	}
	return nil
}

// registerEphemeralSessionSteps wires the ephemeral-session steps into the
// scenario context. Called from initializeScenario.
func registerEphemeralSessionSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a background job state exists for session "([^"]*)"$`, aBackgroundJobStateForSession)
	ctx.Step(`^the session "([^"]*)" has ended without firing SessionEnd$`, aDeadSessionForSession)
	ctx.Step(`^I pipe a SessionStart hook for session "([^"]*)"$`, iPipeASessionStartHookForSession)
	ctx.Step(`^I pipe a SessionEnd hook for session "([^"]*)"$`, iPipeASessionEndHookForSession)
	ctx.Step(`^I run niwa reap from the workspace root$`, iRunNiwaReapFromTheWorkspaceRoot)
	ctx.Step(`^the session mapping exists for session "([^"]*)"$`, theSessionMappingExistsForSession)
	ctx.Step(`^the session mapping does not exist for session "([^"]*)"$`, theSessionMappingDoesNotExistForSession)
}
