package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DaemonHealth captures the runtime liveness of a session's per-worktree
// daemon for surfacing through niwa_list_sessions. The shape is intentionally
// minimal — alive answers "is this session usable?", which is the primary
// motivation in #111. Richer fleet-observability fields (last_claim_at,
// last_progress_at, watcher_count) are deferred to #116 (needs-prd) so they
// can land with a heartbeat infrastructure designed against a clear PRD.
//
// All fields are computed at API call time from <worktreePath>/.niwa/daemon.pid
// plus IsPIDAlive — no persisted state changes when the daemon dies, which
// preserves the lifecycle Status field's single-writer invariant.
type DaemonHealth struct {
	Alive     bool   `json:"alive"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
}

// daemonHealthFor reads <worktreePath>/.niwa/daemon.pid and returns the
// computed DaemonHealth. A missing or empty PID file (the placeholder
// scaffoldWorktreeNiwa creates before the daemon writes its real PID)
// produces {Alive=false, PID=0, StartedAt=""}, matching what callers see
// for sessions whose daemon never reached steady state.
func daemonHealthFor(worktreePath string) DaemonHealth {
	pid, startTime, err := readDaemonPIDFile(filepath.Join(worktreePath, ".niwa"))
	if err != nil || pid == 0 {
		return DaemonHealth{Alive: false}
	}
	dh := DaemonHealth{
		Alive: IsPIDAlive(pid, startTime),
		PID:   pid,
	}
	if startTime > 0 {
		// /proc/<pid>/stat starttime is jiffies since boot. Without the
		// boot time + jiffies/sec we can't recover wall-clock; report the
		// daemon's process start time using the file's mtime as a proxy
		// (it's written once at startup).
		if info, statErr := os.Stat(filepath.Join(worktreePath, ".niwa", "daemon.pid")); statErr == nil {
			dh.StartedAt = info.ModTime().UTC().Format(time.RFC3339)
		}
	}
	return dh
}

// readDaemonPIDFile reads <niwaDir>/daemon.pid and returns (pid, startTime, err).
// Returns (0, 0, nil) for the missing-file case and the empty-placeholder case
// (scaffoldWorktreeNiwa creates an empty daemon.pid that the real daemon
// overwrites later). Mirrors workspace.ReadPIDFile but lives in mcp/ so
// daemon-health probes don't pull in the workspace package (cyclic).
func readDaemonPIDFile(niwaDir string) (pid int, startTime int64, err error) {
	pidPath := filepath.Join(niwaDir, "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		// Scaffold placeholder; daemon hasn't written yet.
		return 0, 0, nil
	}
	lines := strings.Split(trimmed, "\n")
	if _, err := fmt.Sscanf(lines[0], "%d", &pid); err != nil {
		return 0, 0, fmt.Errorf("daemon.pid: invalid pid: %w", err)
	}
	if len(lines) >= 2 {
		_, _ = fmt.Sscanf(lines[1], "%d", &startTime)
	}
	return pid, startTime, nil
}
