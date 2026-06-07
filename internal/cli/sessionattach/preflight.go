// Package sessionattach implements the niwa worktree attach and detach
// commands. These let an operator step into a worktree interactively,
// resume Claude Code with the worker's full transcript history, work
// interactively, and detach cleanly so the worktree returns to being
// available.
package sessionattach

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/worktree"
)

// EncodeProjectDir applies Claude Code's project-dir encoding rule: every
// character that is not [A-Za-z0-9] is replaced with '-'. The leading '/'
// of an absolute CWD becomes a leading '-' under this rule. Empirically
// verified against claude v2.1.138.
func EncodeProjectDir(cwd string) string {
	var b strings.Builder
	b.Grow(len(cwd))
	for _, r := range cwd {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// TranscriptPath returns the deterministic path Claude Code writes a
// session's transcript to:
// <homeDir>/.claude/projects/<encoded(workerCWD)>/<convID>.jsonl.
func TranscriptPath(homeDir, workerCWD, convID string) string {
	return filepath.Join(
		homeDir, ".claude", "projects",
		EncodeProjectDir(workerCWD),
		convID+".jsonl",
	)
}

// PreflightCase identifies which of the three failure modes Preflight
// detected. Used by the user-visible error formatter and by tests asserting
// on specific cases.
type PreflightCase rune

const (
	// CaseA: SessionLifecycleState.ClaudeConversationID is empty (the session
	// never recorded a claude conversation id, e.g. it was never run).
	CaseA PreflightCase = 'A'
	// CaseB: the deterministic transcript path does not exist.
	CaseB PreflightCase = 'B'
	// CaseC: the transcript file exists but is zero bytes.
	CaseC PreflightCase = 'C'
)

// PreflightError represents a pre-flight validation failure. The Error()
// method returns the verbatim user-visible message specified in PRD R4.
type PreflightError struct {
	Case      PreflightCase
	SessionID string
	Path      string // for cases B and C; the transcript path
}

func (e *PreflightError) Error() string {
	switch e.Case {
	case CaseA:
		return fmt.Sprintf(
			"niwa: error: session %s has no captured claude conversation id "+
				"(the session never recorded one, so there is no transcript to resume; inspect with "+
				"`niwa session list --status active` or remove with `niwa session destroy %s`).",
			e.SessionID, e.SessionID,
		)
	case CaseB:
		return fmt.Sprintf(
			"niwa: error: claude transcript missing for session %s "+
				"(expected: %s). Claude may have purged the transcript or the "+
				"worktree was moved. Start a fresh session with `niwa session create` "+
				"or remove with `niwa session destroy %s`.",
			e.SessionID, e.Path, e.SessionID,
		)
	case CaseC:
		return fmt.Sprintf(
			"niwa: error: claude transcript is empty for session %s "+
				"(path: %s). The transcript was started but no records were written. "+
				"Start a fresh session with `niwa session create`.",
			e.SessionID, e.Path,
		)
	default:
		return fmt.Sprintf("niwa: error: unknown preflight case %c for session %s", e.Case, e.SessionID)
	}
}

// PreflightOptions configures Preflight. Exposing HomeDir and WorkerCWD as
// fields keeps the function pure for tests (no HOME env reads).
type PreflightOptions struct {
	HomeDir   string
	WorkerCWD string
}

// Preflight validates that a session is attachable by computing the
// deterministic transcript path and stat'ing it. Returns nil on success;
// returns a *PreflightError on the three known failure modes.
//
// Preflight is for UX, not safety. claude --resume already fails loudly
// (exit 1) on every failure mode tested empirically. Pre-flight exists so
// niwa can emit niwa-shaped error messages with three actionable cases
// instead of claude's opaque "No conversation found with session ID: <uuid>".
func Preflight(state worktree.SessionLifecycleState, opts PreflightOptions) error {
	if state.ClaudeConversationID == "" {
		return &PreflightError{Case: CaseA, SessionID: state.SessionID}
	}
	path := TranscriptPath(opts.HomeDir, opts.WorkerCWD, state.ClaudeConversationID)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &PreflightError{Case: CaseB, SessionID: state.SessionID, Path: path}
		}
		return fmt.Errorf("niwa: error: stat transcript %s: %w", path, err)
	}
	if info.Size() == 0 {
		return &PreflightError{Case: CaseC, SessionID: state.SessionID, Path: path}
	}
	return nil
}
