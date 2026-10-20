package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// claudeSessionRow is the subset of one `claude agents --json --all` row that
// destroy reads. ID is the short id Claude Code's verbs (rm, stop, attach)
// take; SessionID is the full id a session mapping records.
//
// PID and Status are present only while the session's process is alive, so
// either one set means the session is running. State is not consulted: Claude
// Code reports "done" for a session that finished its task whether or not its
// process has exited, and a finished session can sit at done/idle with a live
// process.
type claudeSessionRow struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	PID       int    `json:"pid"`
	Status    string `json:"status"`
}

func (r claudeSessionRow) running() bool {
	return r.PID != 0 || r.Status != ""
}

// Seams over the two claude invocations, so tests never reach a real Claude
// Code install. Production wires them to the real binary.
var (
	listClaudeSessionsFunc  = realListClaudeSessions
	removeClaudeSessionFunc = realRemoveClaudeSession
)

const (
	claudeListTimeout   = 30 * time.Second
	claudeRemoveTimeout = 60 * time.Second
)

// realListClaudeSessions reads every background session Claude Code knows
// about, finished ones included.
func realListClaudeSessions(ctx context.Context) ([]claudeSessionRow, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return nil, errors.New("claude binary not found in PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, claudeListTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "agents", "--json", "--all").Output()
	if err != nil {
		return nil, fmt.Errorf("claude agents: %w", err)
	}
	var rows []claudeSessionRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("reading claude agents output: %w", err)
	}
	return rows, nil
}

// realRemoveClaudeSession runs `claude rm <id>` and returns its combined
// output. It never passes --discard-unpushed or --force-remove-worktree: when
// Claude Code refuses to delete a session's worktree, that refusal stands and
// is reported, rather than niwa overriding it.
func realRemoveClaudeSession(ctx context.Context, id string) (string, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return "", errors.New("claude binary not found in PATH")
	}
	ctx, cancel := context.WithTimeout(ctx, claudeRemoveTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "rm", id).CombinedOutput()
	return string(out), err
}

// removeOwningSessions removes the Claude Code background sessions that own
// the given instances, so destroying an instance doesn't leave its session
// listed in the agents view against a directory that no longer exists.
//
// An instance's owners are the sessions the workspace's session store maps to
// it (written by the SessionStart hook and by dispatch). Every mapping that
// names the instance counts, not only the newest: each dispatch into an
// instance writes one, and an older session is just as stale a row once the
// directory is gone. A session started by hand inside an instance has no
// mapping and is never touched.
//
// This lives here rather than in workspace.DestroyInstance because it needs
// the workspace root, for the session store, and because the reaper calls
// DestroyInstance too: the reaper only reclaims an instance once its session
// is already gone, so it has nothing for this step to do.
//
// For each owner:
//   - gone from `claude agents` already: its mapping is deleted;
//   - still running (a live process): it is kept, and the line says how to
//     remove it once it stops. destroy never stops a session;
//   - otherwise: `claude rm <id>` removes it and its mapping is deleted.
//     Claude Code keeps the transcript, so the line names the resume command.
//
// Every failure is reported and swallowed, the same contract as destroy's
// plugin-record and trust cleanup: by the time this runs the user has asked
// for the instance to go and niwa's unpushed-work check has passed, so a
// leftover session never blocks the teardown. Callers run it before the
// instance directory is removed, while Claude Code can still inspect any
// worktree the session made inside it.
func removeOwningSessions(reporter *workspace.Reporter, workspaceRoot string, instanceDirs []string) {
	owners := owningClaudeMappings(reporter, workspaceRoot, instanceDirs)
	if len(owners) == 0 {
		return
	}

	rows, err := listClaudeSessionsFunc(context.Background())
	if err != nil {
		reporter.Warn("could not check the Claude sessions these instances own (%v); left them in place", err)
		return
	}
	byID := make(map[string]claudeSessionRow, len(rows))
	for _, r := range rows {
		if r.SessionID != "" {
			byID[r.SessionID] = r
		}
	}

	for _, m := range owners {
		row, listed := byID[m.SessionID]
		if !listed {
			// Already deleted from the agents view; only the mapping is left.
			deleteMappingReporting(reporter, workspaceRoot, m.SessionID)
			continue
		}
		// The id comes from another program's output and becomes an argv
		// element, so a value that could read as a flag is refused.
		if !watch.IsSafeHandle(row.ID) {
			reporter.Warn("could not remove Claude session %s: claude reported an unusable id %q", m.SessionID, row.ID)
			continue
		}
		if row.running() {
			reporter.Log("kept Claude session %s: it is still running; once it stops (claude stop %s), remove it with claude rm %s", row.ID, row.ID, row.ID)
			continue
		}
		out, err := removeClaudeSessionFunc(context.Background(), row.ID)
		if err != nil {
			reporter.Warn("could not remove Claude session %s: %s; remove it with claude rm %s", row.ID, firstLine(out, err), row.ID)
			continue
		}
		reporter.Log("removed Claude session %s; its transcript is kept: claude --resume %s", row.ID, m.SessionID)
		deleteMappingReporting(reporter, workspaceRoot, m.SessionID)
	}
}

// owningClaudeMappings returns the Claude session mappings that name one of
// instanceDirs. Mappings for another agent are left out: only Claude Code has
// the background-session row this removes.
func owningClaudeMappings(reporter *workspace.Reporter, workspaceRoot string, instanceDirs []string) []workspace.SessionMapping {
	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil {
		reporter.Warn("could not read the session store (%v); left any Claude sessions these instances own in place", err)
		return nil
	}
	dirs := make(map[string]bool, len(instanceDirs))
	for _, d := range instanceDirs {
		dirs[filepath.Clean(d)] = true
	}
	var owners []workspace.SessionMapping
	for _, m := range mappings {
		if !dirs[filepath.Clean(m.InstancePath)] {
			continue
		}
		if ag, err := agent.ParseAgent(m.Agent); err != nil || ag != agent.AgentClaude {
			continue
		}
		owners = append(owners, m)
	}
	return owners
}

func deleteMappingReporting(reporter *workspace.Reporter, workspaceRoot, sessionID string) {
	if err := workspace.DeleteSessionMapping(workspaceRoot, sessionID); err != nil {
		reporter.Warn("could not delete the session record for %s: %v", sessionID, err)
	}
}

// firstLine returns the first non-empty line of a command's output, falling
// back to the error when the command printed nothing.
func firstLine(out string, err error) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return err.Error()
}
