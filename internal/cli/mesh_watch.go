// Package cli: mesh watch daemon (Phase 4a + 4b — central event loop,
// spawn, and restart cap).
//
// The daemon owns a single central goroutine that:
//
//   - watches `.niwa/roles/<role>/inbox/` directories via fsnotify for
//     newly queued task envelopes,
//   - atomically claims each queued envelope by renaming
//     `inbox/<id>.json` → `inbox/in-progress/<id>.json` under the per-
//     task flock (taskstore.UpdateState transitions queued → running),
//   - spawns a worker (real `claude -p` or the NIWA_WORKER_SPAWN_COMMAND
//     override) with a fixed argv + niwa-owned env overrides + CWD set
//     to the role's repo dir (or instance root for coordinator),
//   - starts a per-task supervisor goroutine that calls cmd.Wait() and
//     reports back to the central loop via a taskEvent channel,
//   - on unexpected worker exit, classifies against the restart cap
//     (default 3 restarts / 4 total attempts) and either schedules a
//     backoff-delayed retry or abandons the task with
//     reason="retry_cap_exceeded" (Issue #5).
//
// Phase 4a scope (Issue #4): minimum daemon capable of claim → spawn →
// exit-notice.
//
// Phase 4b scope (Issue #5): restart cap with backoff + unexpected-exit
// classification. Stall watchdog (Issue #6), reconciliation and
// adopted-orphan polling (Issue #7), and test-harness pause hooks
// (Issue #8) build on this skeleton.

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/mcp"
)

var meshWatchInstanceRoot string

func init() {
	meshCmd.AddCommand(meshWatchCmd)
	meshWatchCmd.Flags().StringVar(&meshWatchInstanceRoot, "instance-root", "", "path to the workspace instance root (required)")
	_ = meshWatchCmd.MarkFlagRequired("instance-root")
}

var meshWatchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Run the mesh watch daemon",
	Long: `Run the mesh watch daemon that claims queued task envelopes
from per-role inboxes and spawns a worker process per task.

The daemon writes a PID file at <instance-root>/.niwa/daemon.pid and
logs to <instance-root>/.niwa/daemon.log. Send SIGTERM to request a
clean shutdown.`,
	RunE: runMeshWatch,
}

// daemonConfig holds parsed timing overrides read from env at daemon
// startup. Issue 4 consumes only SIGTermGrace (for Issue 6); the other
// fields are parsed and logged so Issue 5/6/7 can pick them up without
// changing the startup path.
type daemonConfig struct {
	RetryBackoffs []time.Duration // NIWA_RETRY_BACKOFF_SECONDS (default 30,60,90)
	StallWatchdog time.Duration   // NIWA_STALL_WATCHDOG_SECONDS (default 900)
	SIGTermGrace  time.Duration   // NIWA_SIGTERM_GRACE_SECONDS (default 5)
}

// bootstrapPromptTemplate is the fixed worker bootstrap prompt. The only
// substitution is <task-id>; no part of the task body is ever placed in
// argv (AC-D5 / DESIGN Decision 4).
const bootstrapPromptTemplate = "You are a worker for niwa task %s. Call niwa_check_messages to retrieve your task envelope."

// niwaMCPAllowedToolNames is the flag-formatted list passed to claude's
// --allowed-tools so workers can invoke the niwa MCP surface without a
// per-tool approval prompt. The prefix "mcp__niwa__" matches how Claude
// Code namespaces MCP tool names (server id "niwa" from .mcp.json). Must
// stay in sync with internal/mcp/server.go's tools/list response and the
// niwa-mesh skill's allowed-tools block.
var niwaMCPAllowedToolNames = []string{
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

// spawnTargetInfo captures the resolved spawn binary metadata logged at
// startup. The absolute path is reused verbatim for every subsequent
// spawn in this daemon's lifetime (no re-resolution).
type spawnTargetInfo struct {
	Path string
	UID  uint32
	Mode os.FileMode
}

// inboxEvent is the unit of work processed by the central event loop. It
// carries the role name and task id derived from an inbox path so every
// event site (catch-up scan, fsnotify) funnels through the same claim
// code path.
type inboxEvent struct {
	role     string
	taskID   string
	filePath string // absolute path to .niwa/roles/<role>/inbox/<id>.json
}

// supervisorExit is sent by a per-task supervisor goroutine when
// cmd.Wait() returns. It is a projection of mcp.taskEvent tailored to
// what the supervisor knows at exit time.
type supervisorExit struct {
	taskID   string
	exitCode int
	err      error // nil on clean exit (including non-zero exit code)
}

func runMeshWatch(cmd *cobra.Command, args []string) error {
	instanceRoot := meshWatchInstanceRoot
	if _, err := os.Stat(instanceRoot); os.IsNotExist(err) {
		return fmt.Errorf("instance root does not exist: %s", instanceRoot)
	}

	niwaDir := filepath.Join(instanceRoot, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o700); err != nil {
		return fmt.Errorf("creating .niwa directory: %w", err)
	}

	// Open daemon log (append). Everything below this point writes through
	// logger so a crash leaves an audit trail on disk.
	logPath := filepath.Join(niwaDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening daemon log: %w", err)
	}
	defer logFile.Close()
	logger := log.New(logFile, "", log.LstdFlags)
	logger.Printf("daemon starting pid=%d instance-root=%s", os.Getpid(), instanceRoot)

	// 1. Parse timing overrides.
	cfg := loadDaemonConfig(logger)
	logger.Printf(
		"config retry_backoffs=%s stall_watchdog=%s sigterm_grace=%s",
		formatDurations(cfg.RetryBackoffs), cfg.StallWatchdog, cfg.SIGTermGrace,
	)

	// 2. Resolve the spawn binary once. Startup fails if neither the
	// override nor the `claude` path resolves — the daemon can't run
	// without a spawn target.
	spawnInfo, err := resolveSpawnTarget()
	if err != nil {
		logger.Printf("fatal: cannot resolve worker spawn binary: %v", err)
		return fmt.Errorf("resolving worker spawn binary: %w", err)
	}
	logger.Printf(
		"spawn_target path=%s uid=%d mode=%04o",
		spawnInfo.Path, spawnInfo.UID, spawnInfo.Mode.Perm(),
	)

	// 3. Register fsnotify watchers on every .niwa/roles/<role>/inbox/
	// directory. Subdirectories (in-progress, cancelled, expired, read)
	// are daemon-managed holding areas and are NOT watched.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating fsnotify watcher: %w", err)
	}
	defer watcher.Close()

	rolesRoot := filepath.Join(niwaDir, "roles")
	watchedRoles, err := registerInboxWatches(watcher, rolesRoot, logger)
	if err != nil {
		return fmt.Errorf("registering inbox watches: %w", err)
	}
	logger.Printf("watched_roles count=%d", len(watchedRoles))

	// Context + signal handling set up BEFORE the central goroutine so
	// SIGTERM during startup still triggers a clean shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	// Stop notifying sigCh as soon as runMeshWatch returns. This is
	// placed BEFORE the defer that removes the PID file so any SIGTERM
	// arriving post-event-loop-exit is ignored (not swallowed by a
	// handler that has already been torn down).
	defer signal.Stop(sigCh)
	go func() {
		select {
		case sig := <-sigCh:
			logger.Printf("received signal %v, initiating shutdown", sig)
			cancel()
		case <-ctx.Done():
			// Context cancelled elsewhere (e.g. watcher closed) — exit goroutine.
		}
	}()

	// Per-task supervisor tracking — ensures we drain on shutdown.
	var supervisorWG sync.WaitGroup
	exitCh := make(chan supervisorExit, 32)

	// 4. Acquire the daemon.pid.lock flock BEFORE any code that mutates
	// state.json or transitions.log (reconciliation, fresh-retry hand-
	// off, catch-up claim). If two daemons race on EnsureDaemonRunning
	// the losing daemon must exit without having written anything — the
	// winner then owns the only set of crash-recovery entries. Acquiring
	// the lock here is what enforces "concurrent niwa apply never
	// produces duplicate reconciliation output" (AC-C3 / scenario-21).
	pidLockPath := filepath.Join(niwaDir, "daemon.pid.lock")
	pidLockFile, err := acquireDaemonPIDLock(pidLockPath)
	if err != nil {
		if errors.Is(err, errDaemonAlreadyRunning) {
			logger.Printf("another daemon is running; exiting")
			return nil
		}
		// Non-EWOULDBLOCK failure: log loudly so the daemon does not die
		// silently on a startup error (e.g. EACCES, read-only FS).
		logger.Printf("warning: acquire daemon.pid.lock failed: %v", err)
		return fmt.Errorf("acquiring daemon.pid.lock: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(pidLockFile.Fd()), syscall.LOCK_UN)
		_ = pidLockFile.Close()
	}()

	// 5. Reconciliation of pre-existing state.json entries. MUST run
	// AFTER the lock is held (so we don't double-write crash-recovery
	// entries on a lost race) and BEFORE the catch-up inbox scan so
	// adopted-live-orphan entries are classified before a fsnotify
	// CREATE tries to claim a re-queued envelope, and so that
	// crash-mid-spawn tasks get a fresh retry before the catch-up path
	// tries to re-claim their in-progress envelope.
	tasksDir := filepath.Join(niwaDir, "tasks")
	reconcileResult := reconcileRunningTasks(tasksDir, logger)

	// 6. Catch-up inbox scan. Run AFTER reconciliation so reconciliation
	// can re-hydrate orphan workers before the catch-up claim path runs.
	catchupEvents, err := scanExistingInboxes(rolesRoot, watchedRoles)
	if err != nil {
		logger.Printf("warning: catch-up scan failed: %v", err)
	}

	// 7. Write PID file atomically AFTER watches are registered and the
	// lock is held so EnsureDaemonRunning's "pid-file-appears" signal
	// means the daemon really can accept events.
	if err := writePIDFile(niwaDir); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	pidFilePath := filepath.Join(niwaDir, "daemon.pid")
	logger.Printf("daemon ready, PID file written")

	// 8. Central event loop. Everything state-changing flows through
	// this goroutine: fsnotify events, catch-up queue, per-task exits.
	spawnCtx := spawnContext{
		instanceRoot:  instanceRoot,
		niwaDir:       niwaDir,
		spawnBin:      spawnInfo.Path,
		logger:        logger,
		exitCh:        exitCh,
		wg:            &supervisorWG,
		shutdownCtx:   ctx,
		backoffs:      cfg.RetryBackoffs,
		stallWatchdog: cfg.StallWatchdog,
		sigTermGrace:  cfg.SIGTermGrace,
	}

	// Replay catch-up events through a channel so the central loop has a
	// single `select` point.
	catchupCh := make(chan inboxEvent, len(catchupEvents)+1)
	for _, evt := range catchupEvents {
		catchupCh <- evt
	}
	close(catchupCh)

	// Re-spawn any tasks whose state.json was written but whose
	// cmd.Start never completed. These are the "spawn_never_completed"
	// classifications from reconcileRunningTasks; the re-spawn happens
	// here, after the event loop's supervisor plumbing is in scope.
	for _, taskID := range reconcileResult.freshRetries {
		respawnFreshRetry(taskID, spawnCtx)
	}
	// Feed dead/PID-reuse classifications through the same exitCh that
	// real supervisor goroutines use. The central loop's
	// handleSupervisorExit branch is the single entry point into the
	// classifier — piping the startup hand-off through exitCh avoids
	// maintaining a second call site that could drift from the primary
	// path as Issue 5's classifier evolves.
	//
	// Sent from a goroutine so a larger-than-buffer reconcileResult
	// cannot deadlock startup: the central loop is not yet reading, and
	// exitCh's capacity (32) is a performance hint, not a contract on
	// the number of dead workers a recovered daemon might see.
	if len(reconcileResult.deadWorkers) > 0 {
		supervisorWG.Add(1)
		go func(deadWorkers []string) {
			defer supervisorWG.Done()
			for _, taskID := range deadWorkers {
				select {
				case exitCh <- supervisorExit{taskID: taskID, exitCode: -1}:
				case <-ctx.Done():
					return
				}
			}
		}(reconcileResult.deadWorkers)
	}

	// Orphan supervisor goroutine: owns the 2s orphan poll. Runs in
	// parallel with the central loop so a slow ReadState under flock
	// contention cannot stall fsnotify / exitCh / shutdown handling.
	if len(reconcileResult.orphans) > 0 {
		supervisorWG.Add(1)
		go func(orphans []orphanEntry) {
			defer supervisorWG.Done()
			runOrphanSupervisor(orphans, spawnCtx)
		}(reconcileResult.orphans)
	}

	logger.Printf("watch loop started orphans=%d fresh_retries=%d dead_workers=%d",
		len(reconcileResult.orphans), len(reconcileResult.freshRetries), len(reconcileResult.deadWorkers))
	runEventLoop(ctx, watcher, catchupCh, exitCh, spawnCtx)

	// Shutdown: stop accepting new events, let supervisors finish draining.
	logger.Printf("shutting down, draining in-flight supervisors (up to 5s)")
	drainSupervisors(&supervisorWG, 5*time.Second, logger)
	_ = os.Remove(pidFilePath)
	logger.Printf("daemon exiting")
	return nil
}

// spawnContext bundles the stable fields every claim→spawn path needs.
// Keeping them on one struct makes the central loop's call sites short.
//
// shutdownCtx is the daemon's root context. Supervisor goroutines use it
// to drop exit events intentionally during shutdown rather than blocking
// forever on a channel the central loop will never read again.
//
// backoffs is the retry-backoff slice (index = restart_count-1 on the
// upcoming attempt). Consumed by the retry scheduler in handleSupervisorExit;
// if the slice is shorter than the cap, the last value is reused for all
// remaining attempts (documented in the plan's backoff edge-case notes).
//
// stallWatchdog is the maximum silence (no last_progress.at advance) the
// watchdog tolerates before sending SIGTERM to a running worker. Zero or
// negative disables the watchdog (used by tests that focus on Issue 4/5
// behavior). sigTermGrace is the grace period between SIGTERM and SIGKILL
// and is reused for the defensive-reap timer (worker hung after calling
// niwa_finish_task). Both flow to the per-supervisor watchdog goroutine
// started inside spawnWorker.
type spawnContext struct {
	instanceRoot  string
	niwaDir       string
	spawnBin      string
	logger        *log.Logger
	exitCh        chan<- supervisorExit
	wg            *sync.WaitGroup
	shutdownCtx   context.Context
	backoffs      []time.Duration
	stallWatchdog time.Duration
	sigTermGrace  time.Duration
}

// orphanEntry tracks a live-orphan worker adopted at startup (Issue 7).
// The daemon did not parent the worker process, so there is no
// supervisor goroutine and no cmd.Wait signal. Instead the orphan
// supervisor goroutine (runOrphanSupervisor) polls each orphan every
// orphanPollInterval via the authoritative IsPIDAlive(pid, start_time)
// check and synthesizes a supervisorExit — delivered via exitCh — when
// the worker disappears or the recorded start_time diverges (PID reuse
// defense).
type orphanEntry struct {
	taskID    string
	pid       int
	startTime int64
}

// orphanPollInterval is the cadence at which runOrphanSupervisor polls
// adopted orphans for liveness. PRD spec is 2 s. Exposed as a var so
// tests can compress it via setOrphanPollIntervalForTest; production
// callers must not mutate it.
var orphanPollInterval = 2 * time.Second

// setOrphanPollIntervalForTest swaps orphanPollInterval for the duration
// of a test and returns a restore function. Only intended for use from
// *_test.go files.
func setOrphanPollIntervalForTest(d time.Duration) func() {
	prev := orphanPollInterval
	orphanPollInterval = d
	return func() { orphanPollInterval = prev }
}

// runEventLoop owns the central `select`. It returns only when ctx is
// cancelled (signal) or fsnotify closes its events channel.
//
// Adopted-orphan polling lives in runOrphanSupervisor (its own
// goroutine) so a flock contention on ReadState inside pollOrphans
// cannot stall the central loop.
func runEventLoop(
	ctx context.Context,
	watcher *fsnotify.Watcher,
	catchupCh <-chan inboxEvent,
	exitCh <-chan supervisorExit,
	spawnCtx spawnContext,
) {
	for {
		select {
		case <-ctx.Done():
			return

		case evt, ok := <-catchupCh:
			if !ok {
				// Replace with a nil channel so the select no longer
				// wakes on it; keeps the loop body simple.
				catchupCh = nil
				continue
			}
			handleInboxEvent(evt, spawnCtx)

		case fe, ok := <-watcher.Events:
			if !ok {
				spawnCtx.logger.Printf("fsnotify events channel closed")
				return
			}
			if !fe.Has(fsnotify.Create) {
				continue
			}
			name := filepath.Base(fe.Name)
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			// The parent dir is the inbox for a given role.
			role := roleFromInboxPath(fe.Name)
			if role == "" {
				continue
			}
			taskID := strings.TrimSuffix(name, ".json")
			handleInboxEvent(inboxEvent{
				role:     role,
				taskID:   taskID,
				filePath: fe.Name,
			}, spawnCtx)

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			spawnCtx.logger.Printf("fsnotify error: %v", err)

		case ex, ok := <-exitCh:
			if !ok {
				continue
			}
			handleSupervisorExit(ex, spawnCtx)
		}
	}
}

// pollOrphans is the pure per-tick classifier for adopted-orphan
// liveness. It reads each orphan's state.json and partitions the input
// into (remaining, deadExits):
//
//   - state is terminal → worker finished (via niwa_finish_task) or
//     was cancelled. Drop from list; no exit event.
//   - IsPIDAlive true AND start_time matches → worker still running.
//     Keep in remaining.
//   - IsPIDAlive false, OR start_time differs (PID reuse) → drop from
//     remaining and emit a synthetic supervisorExit so the central
//     loop's handleSupervisorExit classifier can run under the same
//     flock discipline as every other exit event.
//
// The function is intentionally side-effect-free (aside from logging):
// the caller — the orphan supervisor goroutine — is responsible for
// delivering deadExits to exitCh. Keeping the classifier pure lets
// tests exercise the decision logic without having to pump the event
// loop.
func pollOrphans(orphans []orphanEntry, s spawnContext) (remaining []orphanEntry, deadExits []supervisorExit) {
	if len(orphans) == 0 {
		return orphans, nil
	}
	remaining = orphans[:0]
	for _, o := range orphans {
		taskDir := filepath.Join(s.instanceRoot, ".niwa", "tasks", o.taskID)
		_, st, err := mcp.ReadState(taskDir)
		if err != nil {
			// Transient read failure (concurrent writer, disk pressure).
			// Keep polling; do not misclassify on a flaky read.
			remaining = append(remaining, o)
			continue
		}
		if stateIsTerminal(st.State) {
			s.logger.Printf("orphan_poll task=%s state=%s action=drop", o.taskID, st.State)
			continue
		}
		// Non-terminal: check liveness against the recorded identity.
		// If the stored worker.pid is zero for some reason (racy
		// reconciliation / legacy state), use the orphan record — it's
		// what we adopted.
		pid := st.Worker.PID
		if pid == 0 {
			pid = o.pid
		}
		startTime := st.Worker.StartTime
		if startTime == 0 {
			startTime = o.startTime
		}
		if mcp.IsPIDAlive(pid, startTime) {
			remaining = append(remaining, o)
			continue
		}
		// Dead or PID reuse. Synthesize a supervisorExit that matches
		// the shape a real supervisor goroutine would emit. ExitCode is
		// unavailable for orphans (we never had the child handle); use
		// -1 as a sentinel distinct from 0 / 1.
		s.logger.Printf("orphan_poll task=%s pid=%d action=unexpected_exit", o.taskID, pid)
		deadExits = append(deadExits, supervisorExit{
			taskID:   o.taskID,
			exitCode: -1,
		})
	}
	return remaining, deadExits
}

// runOrphanSupervisor is the goroutine that owns adopted-orphan
// polling. Moving the tick out of the central event loop was the fix
// for a starvation bug: a flock contention on ReadState inside
// pollOrphans would stall the central select indefinitely, so
// fsnotify events, supervisor exits, and shutdown signals would all
// queue behind a single slow orphan.
//
// The goroutine owns the orphan list internally. Startup-time
// additions happen before the goroutine starts (no concurrent access);
// removals happen only inside this goroutine. The mutex is defensive
// against a future code path that might want to add orphans at
// runtime — a low-cost safety net, not a correctness requirement
// today.
//
// Dead-worker exits are delivered to exitCh via the same channel real
// supervisor goroutines use. The select on shutdownCtx prevents a
// stuck send from blocking shutdown when the central loop has already
// stopped reading exitCh.
func runOrphanSupervisor(initial []orphanEntry, s spawnContext) {
	if len(initial) == 0 {
		return
	}
	var mu sync.Mutex
	list := initial

	ticker := time.NewTicker(orphanPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.shutdownCtx.Done():
			return
		case <-ticker.C:
			mu.Lock()
			snapshot := list
			mu.Unlock()
			if len(snapshot) == 0 {
				return
			}
			remaining, deadExits := pollOrphans(snapshot, s)
			mu.Lock()
			list = remaining
			empty := len(list) == 0
			mu.Unlock()
			for _, ex := range deadExits {
				select {
				case s.exitCh <- ex:
				case <-s.shutdownCtx.Done():
					return
				}
			}
			if empty {
				// No orphans left to poll; exit the goroutine so the
				// ticker channel does not keep waking us.
				return
			}
		}
	}
}

// testPausePollInterval is the cadence at which waitForTestPauseRelease
// polls for marker-file removal. Extracted as a var so tests can compress
// it; production callers must not mutate it.
var testPausePollInterval = 100 * time.Millisecond

// setTestPausePollIntervalForTest swaps testPausePollInterval for the
// duration of a test and returns a restore function. Only intended for
// use from *_test.go files.
func setTestPausePollIntervalForTest(d time.Duration) func() {
	prev := testPausePollInterval
	testPausePollInterval = d
	return func() { testPausePollInterval = prev }
}

// maybePauseAtClaimHook is invoked at the consumption-rename boundary
// before and after the claim rename. When the named env var is set to
// "1", it atomically creates a marker file under .niwa/.test/ (tmp +
// rename), then polls for the marker's removal before returning. A
// shutdown context-cancellation breaks the wait so daemon shutdown is
// never blocked by a stuck pause hook.
//
// When the env var is absent or != "1", the function is a zero-cost
// no-op: no file ops, no log noise. The marker directory (.niwa/.test/)
// is an untracked runtime artifact (not registered in
// InstanceState.ManagedFiles).
//
// hookName is one of "before_claim" or "after_claim"; envVar is the
// corresponding NIWA_TEST_PAUSE_* variable.
func maybePauseAtClaimHook(evt inboxEvent, s spawnContext, envVar, hookName string) {
	if os.Getenv(envVar) != "1" {
		return
	}
	testDir := filepath.Join(s.instanceRoot, ".niwa", ".test")
	if err := os.MkdirAll(testDir, 0o700); err != nil {
		s.logger.Printf("pause_hook role=%s task=%s hook=%s mkdir_err=%v",
			evt.role, evt.taskID, hookName, err)
		return
	}
	markerPath := filepath.Join(testDir, "paused_"+hookName)
	// Atomic write: tmp file in the same dir, then rename.
	tmpPath := markerPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(evt.taskID), 0o600); err != nil {
		s.logger.Printf("pause_hook role=%s task=%s hook=%s write_err=%v",
			evt.role, evt.taskID, hookName, err)
		return
	}
	if err := os.Rename(tmpPath, markerPath); err != nil {
		_ = os.Remove(tmpPath)
		s.logger.Printf("pause_hook role=%s task=%s hook=%s rename_err=%v",
			evt.role, evt.taskID, hookName, err)
		return
	}
	s.logger.Printf("pause_hook role=%s task=%s hook=%s action=paused",
		evt.role, evt.taskID, hookName)

	// Poll for removal. Break on shutdown so the daemon never hangs
	// waiting for a marker that a test will never remove.
	for {
		if _, err := os.Stat(markerPath); os.IsNotExist(err) {
			s.logger.Printf("pause_hook role=%s task=%s hook=%s action=released",
				evt.role, evt.taskID, hookName)
			return
		}
		if s.shutdownCtx != nil {
			select {
			case <-s.shutdownCtx.Done():
				s.logger.Printf("pause_hook role=%s task=%s hook=%s action=shutdown_escape",
					evt.role, evt.taskID, hookName)
				return
			case <-time.After(testPausePollInterval):
			}
		} else {
			time.Sleep(testPausePollInterval)
		}
	}
}

// daemonOwnsInboxFile decides whether the daemon should treat a freshly
// observed inbox file as a delegate envelope or leave it for the
// recipient's MCP server.
//
// A file is "owned" by the daemon when it looks like a delegate envelope:
//
//   - Body `type` is "task.delegate" (the canonical case), or
//   - Body `type` is unset/empty AND the filename matches the body's
//     `task_id` field (legacy delegates pre-dating the explicit type),
//   - Body `task_id` is empty (oldest legacy: filename was the only
//     correlator).
//
// Anything else — task.completed, task.abandoned, task.cancelled, ask
// replies, peer status updates — is ignored. Those messages live in role
// inboxes for the recipient's MCP server to consume; touching them from
// the daemon races the MCP watcher and drops wakeups.
//
// On read failure the file is treated as NOT owned (return false). A
// torn or unreadable file shouldn't get yanked into dangling/ where it
// could confuse the operator and obscure the underlying I/O fault; the
// MCP server's defensive read will skip it the same way and the file
// stays in place for diagnosis.
func daemonOwnsInboxFile(filePath, filenameTaskID string) bool {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	var peek struct {
		Type   string `json:"type"`
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(data, &peek); err != nil {
		// Malformed JSON: not the daemon's to clean up. Leave for ops.
		return false
	}
	if peek.Type == "task.delegate" {
		return true
	}
	if peek.Type == "" {
		// Legacy: no type field. Filename-as-task-id convention applies.
		// Accept when filename matches the body task_id (real delegate)
		// or when task_id is missing entirely (oldest convention).
		if peek.TaskID == "" || peek.TaskID == filenameTaskID {
			return true
		}
	}
	return false
}

// handleInboxEvent runs the claim → spawn flow for a single queued
// envelope. Failures are logged and do NOT abort the loop — the daemon
// must remain responsive to other inboxes.
//
// Inbox files come in two shapes that the daemon must distinguish:
//
//  1. Delegate envelopes — filename is the task_id, body type is
//     "task.delegate" (or empty for legacy). The daemon claims and spawns.
//  2. Peer messages — filename is a fresh msg_id; body type is a terminal
//     event (task.completed/abandoned/cancelled), an ask/answer, or any
//     other peer-to-peer type. These belong to the recipient's MCP server,
//     not the daemon. Touching them races the MCP server's watcher and
//     can drop terminal-event wakeups so niwa_await_task hangs until its
//     own timeout.
//
// The daemon only touches files of shape (1). Anything else is left alone.
func handleInboxEvent(evt inboxEvent, s spawnContext) {
	if !daemonOwnsInboxFile(evt.filePath, evt.taskID) {
		// Peer message routed through this role's inbox — recipient's MCP
		// server handles it. Silently ignore (no log, no rename); logging
		// every peer message would dwarf the useful daemon-event lines.
		return
	}
	taskDir := filepath.Join(s.instanceRoot, ".niwa", "tasks", evt.taskID)
	if _, err := os.Stat(filepath.Join(taskDir, "state.json")); err != nil {
		// Dangling delegate envelope — filename matches a task_id but no
		// task dir. Move out of the queued inbox so fsnotify doesn't
		// re-fire CREATE events for it on every daemon startup or sibling
		// write. The file lands in
		// `.niwa/roles/<role>/inbox/dangling/<task-id>.json` for operator
		// inspection; leaving it in place keeps triggering the same
		// "skip=dangling" code path indefinitely.
		s.logger.Printf("inbox_event role=%s task=%s skip=dangling path=%s", evt.role, evt.taskID, evt.filePath)
		danglingDir := filepath.Join(filepath.Dir(evt.filePath), "dangling")
		if err := os.MkdirAll(danglingDir, 0o700); err != nil {
			s.logger.Printf("inbox_event role=%s task=%s dangling_mkdir_err=%v", evt.role, evt.taskID, err)
			return
		}
		danglingPath := filepath.Join(danglingDir, filepath.Base(evt.filePath))
		if err := os.Rename(evt.filePath, danglingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.logger.Printf("inbox_event role=%s task=%s dangling_rename_err=%v", evt.role, evt.taskID, err)
		}
		return
	}

	// Test-harness pause hook (Issue 8): block before the claim so
	// race-window tests (cancel-vs-claim, update-vs-claim) can observe
	// the envelope in its pre-claim state. Env-gated; invisible to
	// production when NIWA_TEST_PAUSE_BEFORE_CLAIM is unset.
	maybePauseAtClaimHook(evt, s, "NIWA_TEST_PAUSE_BEFORE_CLAIM", "before_claim")

	// Transition state queued → running under the per-task flock. If the
	// state is already non-queued (claimed, cancelled) the mutator
	// returns an error and we skip the rename.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	var alreadyClaimed bool
	err := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		if cur.State != mcp.TaskStateQueued {
			alreadyClaimed = true
			return nil, nil, nil
		}
		next := *cur
		next.State = mcp.TaskStateRunning
		next.UpdatedAt = now
		next.StateTransitions = append(next.StateTransitions,
			mcp.StateTransition{From: mcp.TaskStateQueued, To: mcp.TaskStateRunning, At: now})
		next.Worker = mcp.TaskWorker{
			Role:           evt.role,
			SpawnStartedAt: now,
		}
		entry := &mcp.TransitionLogEntry{
			Kind:    "spawn",
			From:    mcp.TaskStateQueued,
			To:      mcp.TaskStateRunning,
			At:      now,
			Attempt: 1,
			Actor: &mcp.TransitionActor{
				Kind: "daemon",
				PID:  os.Getpid(),
			},
		}
		return &next, entry, nil
	})
	if alreadyClaimed {
		s.logger.Printf("inbox_event role=%s task=%s skip=not_queued", evt.role, evt.taskID)
		return
	}
	if err != nil {
		s.logger.Printf("inbox_event role=%s task=%s update_state_err=%v", evt.role, evt.taskID, err)
		return
	}

	// Atomic rename: inbox/<id>.json → inbox/in-progress/<id>.json.
	// The state transition above committed the claim; the rename is the
	// externally-visible signal so cancellation / observability paths
	// know the envelope has moved out of "queued".
	inProgressPath := filepath.Join(filepath.Dir(evt.filePath), "in-progress", filepath.Base(evt.filePath))
	if err := os.Rename(evt.filePath, inProgressPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Cancellation won the race.
			s.logger.Printf("inbox_event role=%s task=%s skip=rename_enoent", evt.role, evt.taskID)
			return
		}
		s.logger.Printf("inbox_event role=%s task=%s rename_err=%v", evt.role, evt.taskID, err)
		return
	}
	s.logger.Printf("inbox_event role=%s task=%s claim=ok", evt.role, evt.taskID)

	// Test-harness pause hook (Issue 8): block after the claim but
	// before the spawn so tests can observe the envelope in the
	// post-claim window (in-progress/ rename committed, worker.pid
	// still zero, niwa_cancel_task returns too_late).
	maybePauseAtClaimHook(evt, s, "NIWA_TEST_PAUSE_AFTER_CLAIM", "after_claim")

	// Spawn the worker.
	spawnWorker(evt, taskDir, s)
}

// spawnWorker constructs the exec.Command per the fixed argv contract
// (DESIGN Decision 4), starts the process, backfills pid + start_time
// into state.json, and kicks off a supervisor goroutine.
//
// Failure of cmd.Start moves the task to `abandoned` with reason
// "spawn_failed". No retry: a spawn failure indicates a fundamental
// problem (bad binary, permission denied, etc.) and Issue 5's retry
// pipeline intentionally does not cover this case.
func spawnWorker(evt inboxEvent, taskDir string, s spawnContext) {
	prompt := fmt.Sprintf(bootstrapPromptTemplate, evt.taskID)
	mcpConfigPath := filepath.Join(s.instanceRoot, ".claude", ".mcp.json")

	// --permission-mode=acceptEdits auto-approves file edits but does NOT
	// auto-approve MCP tool calls; a worker running in headless `-p` mode
	// therefore stalls on the first tool-call approval dialog and exits
	// without making progress. --allowed-tools whitelists each niwa MCP
	// tool by its mcp__<server>__<tool> name so the worker can call them
	// without prompting. The list must stay in sync with the MCP server's
	// tools/list response (internal/mcp/server.go) and the niwa-mesh skill
	// allowed-tools block (internal/workspace/channels.go).
	cmd := exec.Command(
		s.spawnBin,
		"-p", prompt,
		"--permission-mode=acceptEdits",
		"--mcp-config="+mcpConfigPath,
		"--strict-mcp-config",
		"--allowed-tools", strings.Join(niwaMCPAllowedToolNames, ","),
	)

	// Env: pass-through daemon's env, then niwa-owned last-wins
	// overrides. Go's exec.Cmd.Env uses "last wins" on duplicate keys.
	cmd.Env = append(os.Environ(),
		"NIWA_INSTANCE_ROOT="+s.instanceRoot,
		"NIWA_SESSION_ROLE="+evt.role,
		"NIWA_TASK_ID="+evt.taskID,
	)

	// CWD: role's repo dir (or instance root for coordinator).
	cmd.Dir = resolveRoleCWD(s.instanceRoot, evt.role)

	// Detach into a new session/process-group so SIGINT on the daemon's
	// controlling terminal does not cascade to the worker.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	// Per-task stderr log. Worker stderr is a per-task concern; the
	// daemon log captures daemon-level events only.
	//
	// IMPORTANT: exec.Cmd.Wait() does NOT close caller-supplied *os.File
	// values. The supervisor goroutine below closes stderrFile after
	// Wait returns; if cmd.Start fails we close it on the early-return
	// path. Missing either path leaks one fd per spawn and will
	// eventually exhaust the daemon's fd budget.
	stderrPath := filepath.Join(taskDir, "stderr.log")
	stderrFile, stderrErr := os.OpenFile(stderrPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if stderrErr == nil {
		cmd.Stderr = stderrFile
	} else {
		s.logger.Printf("spawn_warn role=%s task=%s stderr_open_err=%v", evt.role, evt.taskID, stderrErr)
	}

	if err := cmd.Start(); err != nil {
		// Close the stderr fd before returning — the supervisor goroutine
		// that normally owns closing it is never started on this path.
		if stderrFile != nil {
			_ = stderrFile.Close()
		}
		s.logger.Printf("spawn_err role=%s task=%s err=%v", evt.role, evt.taskID, err)
		// Transition task to abandoned with reason "spawn_failed". No
		// retry at this phase — see function comment.
		_ = mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
			if cur.State != mcp.TaskStateRunning {
				return nil, nil, nil
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			next := *cur
			next.State = mcp.TaskStateAbandoned
			next.UpdatedAt = now
			next.Reason = json.RawMessage(fmt.Sprintf(`{"error":"spawn_failed","detail":%q}`, err.Error()))
			next.StateTransitions = append(next.StateTransitions,
				mcp.StateTransition{From: mcp.TaskStateRunning, To: mcp.TaskStateAbandoned, At: now})
			entry := &mcp.TransitionLogEntry{
				Kind: "spawn_failed",
				From: mcp.TaskStateRunning,
				To:   mcp.TaskStateAbandoned,
				At:   now,
				Actor: &mcp.TransitionActor{
					Kind: "daemon",
					PID:  os.Getpid(),
				},
			}
			return &next, entry, nil
		})
		return
	}

	pid := cmd.Process.Pid
	startTime, _ := mcp.PIDStartTime(pid)

	// Backfill pid + start_time into state.json under the task flock.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_ = mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		next := *cur
		next.Worker.PID = pid
		next.Worker.StartTime = startTime
		next.UpdatedAt = now
		// No new transition entry — backfill does not change `state`.
		return &next, nil, nil
	})

	s.logger.Printf("spawn_ok role=%s task=%s pid=%d start_time=%d", evt.role, evt.taskID, pid, startTime)

	// waitDone closes when the supervisor goroutine observes cmd.Wait()
	// return. The watchdog selects on it so a natural exit releases the
	// watchdog without any polling, killing, or lock contention.
	waitDone := make(chan struct{})

	// Watchdog goroutine (Issue 6): polls state.json at 2 s and escalates
	// with SIGTERM → SIGKILL when progress stalls or the worker hangs
	// after calling niwa_finish_task. Disabled when stallWatchdog <= 0.
	if s.stallWatchdog > 0 {
		s.wg.Add(1)
		go func(c *exec.Cmd, taskID, taskDir string) {
			defer s.wg.Done()
			runWatchdog(c, taskID, taskDir, waitDone, s)
		}(cmd, evt.taskID, taskDir)
	}

	// Supervisor goroutine: wait for exit, signal watchdog teardown, close
	// the stderr fd we allocated above, then report back to the central
	// loop.
	//
	// The send is guarded by shutdownCtx rather than using a `default:`
	// branch. A full exitCh during normal operation is back-pressure,
	// not an error — blocking here pace-matches the central loop. Only
	// when the daemon is shutting down (shutdownCtx.Done() has fired)
	// do we intentionally drop the event: nothing reads exitCh at that
	// point, and the transition will be re-derived by Issue 7's
	// reconciliation on the next daemon start.
	s.wg.Add(1)
	go func(c *exec.Cmd, taskID string, stderrFile *os.File) {
		defer s.wg.Done()
		waitErr := c.Wait()
		// Signal the watchdog goroutine that cmd.Wait has returned. Both
		// natural exits and watchdog-triggered kills end up here; the
		// watchdog treats a closed waitDone as "process is gone, exit".
		close(waitDone)
		if stderrFile != nil {
			_ = stderrFile.Close()
		}
		exitCode := 0
		if c.ProcessState != nil {
			exitCode = c.ProcessState.ExitCode()
		}
		select {
		case s.exitCh <- supervisorExit{taskID: taskID, exitCode: exitCode, err: waitErr}:
		case <-s.shutdownCtx.Done():
			// Daemon is shutting down; the central loop has already
			// returned and nothing will read exitCh. Drop the event
			// intentionally — see function comment.
		}
	}(cmd, evt.taskID, stderrFile)
}

// runWatchdog enforces the per-supervisor stall watchdog (Issue 6). It
// polls state.json every 2 s and triggers the SIGTERM → SIGKILL
// escalation path when:
//
//  1. the task is still "running" and `last_progress.at` has not advanced
//     within `stallWatchdog`, OR
//  2. the task state is already terminal (completed/abandoned/cancelled)
//     but the worker process is still alive after `sigTermGrace` (the
//     defensive-reap path — worker hung after niwa_finish_task).
//
// In both cases escalateSignals sends SIGTERM to the worker's process
// group, waits up to `sigTermGrace` for cmd.Wait to return, and SIGKILLs
// on expiry. Both signal transitions append a `watchdog_signal` entry to
// transitions.log with the signal name so the audit trail shows exactly
// what the daemon did.
//
// The watchdog exits immediately when `waitDone` closes — that's the
// supervisor's signal that cmd.Wait has returned (natural exit, or an
// exit we already forced). No polling, no leaks.
func runWatchdog(cmd *exec.Cmd, taskID, taskDir string, waitDone <-chan struct{}, s spawnContext) {
	ticker := time.NewTicker(watchdogPollInterval)
	defer ticker.Stop()

	// stallTimer fires when no progress has been observed for
	// stallWatchdog. Reset on every detected progress advance.
	stallTimer := time.NewTimer(s.stallWatchdog)
	defer stallTimer.Stop()

	// Track the last observed progress timestamp and terminal-state
	// discovery time so we can detect advances and enforce the
	// defensive-reap grace window.
	var (
		lastProgressAt       string
		terminalObservedAt   time.Time // zero when state is not yet terminal
		defensiveReapTimer   *time.Timer
		defensiveReapTimerC  <-chan time.Time
		watchdogAlreadyFired bool
	)

	// Seed the baseline from the current state.json so a spawn that
	// completes its startup before the watchdog ticks cannot be flagged
	// as stalled on first poll.
	if _, st, err := mcp.ReadState(taskDir); err == nil {
		if st.LastProgress != nil {
			lastProgressAt = st.LastProgress.At
		}
	}

	for {
		select {
		case <-waitDone:
			// Natural exit, or we already killed the worker and cmd.Wait
			// returned. Either way, stop watching.
			return
		case <-s.shutdownCtx.Done():
			// Daemon is shutting down; let destroy-phase code (Issue 8)
			// drive the kill path. Stop watching.
			return
		case <-stallTimer.C:
			if watchdogAlreadyFired {
				continue
			}
			watchdogAlreadyFired = true
			s.logger.Printf("watchdog task=%s trigger=stall escalating=SIGTERM", taskID)
			escalateSignals(cmd, taskID, taskDir, waitDone, s, "stall")
			// After escalateSignals returns, cmd.Wait may still be
			// racing to report. Keep looping on waitDone.
		case <-defensiveReapTimerC:
			if watchdogAlreadyFired {
				continue
			}
			watchdogAlreadyFired = true
			s.logger.Printf("watchdog task=%s trigger=defensive_reap escalating=SIGTERM", taskID)
			escalateSignals(cmd, taskID, taskDir, waitDone, s, "defensive_reap")
		case <-ticker.C:
			_, st, err := mcp.ReadState(taskDir)
			if err != nil {
				// State read failures are transient (concurrent writer,
				// disk pressure). Keep polling; don't fire the watchdog
				// on a transient error.
				continue
			}
			// Progress-advance check: reset stallTimer when
			// last_progress.at has moved forward.
			curProgressAt := ""
			if st.LastProgress != nil {
				curProgressAt = st.LastProgress.At
			}
			if curProgressAt != "" && curProgressAt != lastProgressAt {
				lastProgressAt = curProgressAt
				resetTimer(stallTimer, s.stallWatchdog)
			}
			// Defensive-reap: state is terminal but the worker process
			// is still alive. Start a grace timer on first observation;
			// after it fires, escalate.
			if stateIsTerminal(st.State) {
				if terminalObservedAt.IsZero() {
					terminalObservedAt = time.Now()
					// Also stop the stallTimer — progress is no longer
					// relevant once state is terminal.
					if !stallTimer.Stop() {
						select {
						case <-stallTimer.C:
						default:
						}
					}
					// Start the defensive-reap grace timer.
					defensiveReapTimer = time.NewTimer(s.sigTermGrace)
					defensiveReapTimerC = defensiveReapTimer.C
				}
			}
		}
	}
}

// resetTimer stops timer, drains a pending fire, and resets it to d. The
// dance handles the documented time.Timer quirk where a concurrent fire
// can leave a value in the channel even after Stop returns false.
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}

// escalateSignals runs the SIGTERM → grace → SIGKILL ladder. Two
// transitions.log entries are appended: one when SIGTERM is sent, one
// (only if the grace window expires) when SIGKILL is sent. The Signal
// field carries the wire signal name.
//
// Sends to the process group (negative PID) because worker spawns use
// Setsid:true — SIGTERM to the leader alone can miss child processes.
func escalateSignals(cmd *exec.Cmd, taskID, taskDir string, waitDone <-chan struct{}, s spawnContext, trigger string) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}

	// Append SIGTERM entry to transitions.log. Non-fatal on error — we
	// still want to try killing the worker even if the log write fails.
	if err := appendWatchdogSignalEntry(taskDir, "SIGTERM", trigger); err != nil {
		s.logger.Printf("watchdog task=%s log_sigterm_err=%v", taskID, err)
	}

	// SIGTERM to the process group. -pid signals the whole group
	// created by Setsid:true.
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		s.logger.Printf("watchdog task=%s sigterm_err=%v", taskID, err)
		// Fall through to grace window anyway — the process may have
		// already exited between the check and the kill.
	} else {
		s.logger.Printf("watchdog task=%s sigterm=sent pgid=%d", taskID, pid)
	}

	// Wait up to sigTermGrace for the worker to exit. If cmd.Wait
	// returns, the supervisor goroutine closes waitDone and we're done.
	select {
	case <-waitDone:
		s.logger.Printf("watchdog task=%s exited_within_grace=true", taskID)
		return
	case <-time.After(s.sigTermGrace):
	}

	// Grace expired. Escalate to SIGKILL.
	if err := appendWatchdogSignalEntry(taskDir, "SIGKILL", trigger); err != nil {
		s.logger.Printf("watchdog task=%s log_sigkill_err=%v", taskID, err)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		s.logger.Printf("watchdog task=%s sigkill_err=%v", taskID, err)
	} else {
		s.logger.Printf("watchdog task=%s sigkill=sent pgid=%d", taskID, pid)
	}
	// Don't block here waiting for cmd.Wait — the supervisor goroutine
	// owns that and will signal waitDone when the kernel reaps the PID.
}

// appendWatchdogSignalEntry writes a `watchdog_signal` NDJSON entry to
// transitions.log with the given signal name. state.json is not mutated;
// the entry is audit-only.
//
// Uses mcp.AppendAuditEntry directly because mcp.UpdateState rejects
// mutations on terminal state — and the defensive-reap path fires
// precisely when state is already terminal. The audit-only API is
// allowed to append on any state under the per-task flock.
//
// WorkerPID is best-effort: the watchdog grabs it from the most recent
// state.json read. A missing PID is not a failure — the log entry is
// still useful for the audit trail.
func appendWatchdogSignalEntry(taskDir, signal, trigger string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	// Best-effort read of the current worker PID for the audit entry.
	// We don't require this to succeed — a stale or missing PID only
	// degrades the log detail, not the correctness of the signal path.
	workerPID := 0
	if _, st, err := mcp.ReadState(taskDir); err == nil {
		workerPID = st.Worker.PID
	}

	reasonPayload := fmt.Sprintf(`{"trigger":%q}`, trigger)
	entry := &mcp.TransitionLogEntry{
		Kind:      "watchdog_signal",
		At:        now,
		Signal:    signal,
		WorkerPID: workerPID,
		Reason:    json.RawMessage(reasonPayload),
		Actor: &mcp.TransitionActor{
			Kind: "daemon",
			PID:  os.Getpid(),
		},
	}
	return mcp.AppendAuditEntry(taskDir, entry)
}

// handleSupervisorExit processes a worker exit. The supervisor's return
// is classified against the authoritative state.json:
//
//   - Terminal state (completed/abandoned/cancelled): the worker called
//     niwa_finish_task (or delegator cancelled) before exit. Log and
//     return; the daemon has nothing to do.
//
//   - Still "running": this is an unexpected exit. Determine the next
//     restart_count (cur.RestartCount + 1) and compare against the cap
//     (cur.MaxRestarts). If over the cap, transition to abandoned with
//     reason="retry_cap_exceeded" and deliver a task.abandoned message
//     to the delegator. Otherwise, log a `retry_scheduled` entry and
//     schedule retrySpawn via time.AfterFunc(backoff).
//
// restart_count is NOT bumped at this point; it is bumped when the
// retry actually fires (inside retrySpawn). This preserves the
// invariant that state.json.worker.restart_count reflects "attempts
// started" rather than "attempts scheduled", which Issue 7's crash
// reconciliation depends on.
func handleSupervisorExit(ex supervisorExit, s spawnContext) {
	taskDir := filepath.Join(s.instanceRoot, ".niwa", "tasks", ex.taskID)
	_, st, err := mcp.ReadState(taskDir)
	if err != nil {
		s.logger.Printf("exit_event task=%s read_state_err=%v", ex.taskID, err)
		return
	}

	// Terminal state → worker called niwa_finish_task (or delegator
	// cancelled) before exit. Nothing for the daemon to do.
	if stateIsTerminal(st.State) {
		s.logger.Printf("exit_event task=%s state=%s exit_code=%d action=none", ex.taskID, st.State, ex.exitCode)
		return
	}

	// Unexpected exit: worker process returned while state was still
	// "running". Classify against the restart cap.
	code := ex.exitCode
	nextAttempt := st.RestartCount + 1
	restartCap := st.MaxRestarts
	if restartCap <= 0 {
		// Defensive: legacy state.json written before Issue 1 may not
		// carry max_restarts. Use the PRD default.
		restartCap = defaultMaxRestarts
	}

	// Envelope role is not stored on state.json directly (it's on
	// envelope.from / envelope.to); we only need the delegator role for
	// the abandoned message, which lives on state.json.DelegatorRole.
	delegatorRole := st.DelegatorRole
	workerRole := st.Worker.Role
	if workerRole == "" {
		// TargetRole is the task's target role, which is what the
		// worker role was spawned as. Fall back if Worker.Role was
		// never backfilled (extremely early failure).
		workerRole = st.TargetRole
	}

	if nextAttempt > restartCap {
		// Over the cap: abandon the task.
		now := time.Now().UTC().Format(time.RFC3339Nano)
		reasonJSON := fmt.Sprintf(`{"error":"retry_cap_exceeded","restart_count":%d,"max_restarts":%d}`, st.RestartCount, restartCap)
		updErr := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
			if cur.State != mcp.TaskStateRunning {
				// Another path (cancellation) raced us; abandon would be
				// invalid from a non-running state.
				return nil, nil, nil
			}
			next := *cur
			next.State = mcp.TaskStateAbandoned
			next.UpdatedAt = now
			next.Reason = json.RawMessage(reasonJSON)
			next.StateTransitions = append(next.StateTransitions,
				mcp.StateTransition{From: mcp.TaskStateRunning, To: mcp.TaskStateAbandoned, At: now})
			entry := &mcp.TransitionLogEntry{
				Kind:      "unexpected_exit",
				From:      mcp.TaskStateRunning,
				To:        mcp.TaskStateAbandoned,
				At:        now,
				WorkerPID: cur.Worker.PID,
				ExitCode:  &code,
				Reason:    json.RawMessage(reasonJSON),
				Attempt:   cur.RestartCount,
				Actor: &mcp.TransitionActor{
					Kind: "daemon",
					PID:  os.Getpid(),
				},
			}
			return &next, entry, nil
		})
		if updErr != nil {
			s.logger.Printf("exit_event task=%s abandon_err=%v", ex.taskID, updErr)
			return
		}
		s.logger.Printf("exit_event task=%s state=running exit_code=%d action=abandoned reason=retry_cap_exceeded restart_count=%d",
			ex.taskID, ex.exitCode, st.RestartCount)

		// Deliver task.abandoned to the delegator. Best-effort; a failed
		// write here does not change state.json (which is authoritative).
		if delegatorRole != "" {
			deliverAbandonedMessage(s, delegatorRole, workerRole, ex.taskID, st.RestartCount, restartCap)
		}
		return
	}

	// Within the cap: schedule a retry. Log `retry_scheduled` but do
	// NOT transition the state (task stays "running" — the supervisor
	// goroutine has already exited, but the task is still logically
	// in-flight awaiting the timer).
	backoff := backoffForAttempt(s.backoffs, nextAttempt)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	updErr := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		if cur.State != mcp.TaskStateRunning {
			return nil, nil, nil
		}
		next := *cur
		next.UpdatedAt = now
		entry := &mcp.TransitionLogEntry{
			Kind:      "unexpected_exit",
			At:        now,
			WorkerPID: cur.Worker.PID,
			ExitCode:  &code,
			Attempt:   cur.RestartCount,
			Actor: &mcp.TransitionActor{
				Kind: "daemon",
				PID:  os.Getpid(),
			},
		}
		return &next, entry, nil
	})
	if updErr != nil {
		s.logger.Printf("exit_event task=%s log_unexpected_exit_err=%v", ex.taskID, updErr)
		return
	}
	// Append a second transitions.log entry announcing the scheduled
	// retry. Kept as its own entry so the audit trail clearly separates
	// "what happened" (unexpected_exit) from "what we decided to do"
	// (retry_scheduled).
	_ = appendRetryScheduledEntry(taskDir, nextAttempt, backoff)

	s.logger.Printf("exit_event task=%s state=running exit_code=%d action=retry_scheduled attempt=%d backoff=%s",
		ex.taskID, ex.exitCode, nextAttempt, backoff)

	// Schedule the retry. Use a goroutine with a select on both the
	// timer and shutdownCtx so shutdown drains promptly instead of
	// waiting out the full backoff. The supervisor WG is held for the
	// lifetime of the scheduler goroutine so drainSupervisors covers it.
	s.wg.Add(1)
	go func(taskID, role string, delay time.Duration) {
		defer s.wg.Done()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			retrySpawn(taskID, role, s)
		case <-s.shutdownCtx.Done():
			// Daemon tearing down; drop the retry. Issue 7's
			// reconciliation will re-derive it on next startup.
			s.logger.Printf("retry_skipped task=%s reason=shutdown", taskID)
		}
	}(ex.taskID, workerRole, backoff)
}

// defaultMaxRestarts matches PRD Configuration Defaults (3 restarts → 4
// total attempts) and is applied when state.json.max_restarts is zero.
// Zero would otherwise abandon on the first unexpected exit, which is
// not what the defaults promise.
const defaultMaxRestarts = 3

// watchdogPollInterval is the cadence at which the per-supervisor
// watchdog polls state.json for progress advances and terminal-state
// transitions. PRD spec is 2 s (see Issue 6 plan). Exposed as a var so
// tests can compress it to sub-second values via
// setWatchdogPollIntervalForTest; production callers must not mutate it.
var watchdogPollInterval = 2 * time.Second

// setWatchdogPollIntervalForTest swaps watchdogPollInterval for the
// duration of a test and returns a restore function. Only intended for
// use from *_test.go files.
func setWatchdogPollIntervalForTest(d time.Duration) func() {
	prev := watchdogPollInterval
	watchdogPollInterval = d
	return func() { watchdogPollInterval = prev }
}

// backoffForAttempt returns the retry delay for the given attempt number
// (1-indexed: attempt=1 is the first retry after the initial spawn).
// When the slice is shorter than the attempt, the last value is reused
// for all remaining attempts — the documented clamp behavior for
// NIWA_RETRY_BACKOFF_SECONDS. A completely empty slice falls back to
// zero, meaning "retry immediately" (should never happen in practice
// because loadDaemonConfig defaults to 30,60,90).
func backoffForAttempt(backoffs []time.Duration, attempt int) time.Duration {
	if len(backoffs) == 0 {
		return 0
	}
	idx := attempt - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(backoffs) {
		idx = len(backoffs) - 1
	}
	return backoffs[idx]
}

// appendRetryScheduledEntry writes a "retry_scheduled" NDJSON line to
// transitions.log without touching state.json. The daemon uses a direct
// helper (not mcp.UpdateState) because the semantic is "audit-only": no
// mutator, no tmp+rename cycle, and the entry is allowed to follow
// another entry within the same critical section.
//
// Implementation: wraps mcp.UpdateState with a no-op mutator that emits
// the entry. Returning (cur, entry, nil) from the mutator when state is
// still running guarantees the append is sequenced after any prior
// transition write by the flock discipline.
func appendRetryScheduledEntry(taskDir string, attempt int, backoff time.Duration) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		if cur.State != mcp.TaskStateRunning {
			return nil, nil, nil
		}
		next := *cur
		next.UpdatedAt = now
		entry := &mcp.TransitionLogEntry{
			Kind:    "retry_scheduled",
			At:      now,
			Attempt: attempt,
			// Encode the backoff duration as a quick-to-parse reason
			// payload. Reviewers and tests can decode this without a
			// schema change.
			Reason: json.RawMessage(fmt.Sprintf(`{"backoff_seconds":%d}`, int(backoff/time.Second))),
			Actor: &mcp.TransitionActor{
				Kind: "daemon",
				PID:  os.Getpid(),
			},
		}
		return &next, entry, nil
	})
}

// deliverAbandonedMessage writes a task.abandoned Message to the
// delegator's inbox when the daemon abandons a task after retry-cap
// exhaustion. Best-effort: errors are logged but do not change
// state.json, which remains authoritative.
//
// Mirrors the shape of Server.sendTaskMessage (internal/mcp/handlers_task.go)
// so the delegator's niwa_check_messages / niwa_await_task path does not
// need to distinguish worker-authored from daemon-authored terminal
// messages.
func deliverAbandonedMessage(s spawnContext, delegatorRole, workerRole, taskID string, restartCount, restartCap int) {
	inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", delegatorRole, "inbox")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		s.logger.Printf("abandon_msg task=%s mkdir_err=%v", taskID, err)
		return
	}
	msgID := mcp.NewTaskID() // reuse UUIDv4 generator; task ID and message ID share the format
	body := map[string]any{
		"task_id":       taskID,
		"reason":        "retry_cap_exceeded",
		"restart_count": restartCount,
		"max_restarts":  restartCap,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		s.logger.Printf("abandon_msg task=%s marshal_err=%v", taskID, err)
		return
	}
	msg := mcp.Message{
		V:      1,
		ID:     msgID,
		Type:   "task.abandoned",
		From:   mcp.MessageFrom{Role: workerRole, PID: os.Getpid()},
		To:     mcp.MessageTo{Role: delegatorRole},
		TaskID: taskID,
		SentAt: time.Now().UTC().Format(time.RFC3339),
		Body:   bodyBytes,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		s.logger.Printf("abandon_msg task=%s marshal_msg_err=%v", taskID, err)
		return
	}
	tmp := filepath.Join(inboxDir, msgID+".json.tmp")
	dest := filepath.Join(inboxDir, msgID+".json")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		s.logger.Printf("abandon_msg task=%s write_err=%v", taskID, err)
		return
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		s.logger.Printf("abandon_msg task=%s rename_err=%v", taskID, err)
		return
	}
	s.logger.Printf("abandon_msg task=%s delegator=%s delivered=%s", taskID, delegatorRole, msgID)
}

// retrySpawn is the timer-fired entry point for a scheduled restart. It
// re-validates the task is still in a retryable state, bumps
// restart_count, refreshes worker.spawn_started_at, and re-enters the
// spawn path with the same argv/env/CWD contract as the initial spawn.
//
// The envelope file remains at .niwa/roles/<role>/inbox/in-progress/<id>.json
// across retries — we do NOT move it back to inbox/<id>.json and re-claim.
// The claim is one-shot; every attempt thereafter reuses the same
// in-progress envelope.
//
// If state is no longer "running" (e.g., delegator called
// niwa_cancel_task in the backoff window, or the task was abandoned by
// another code path), the retry is skipped silently. That's the correct
// behavior: terminal state wins.
func retrySpawn(taskID, role string, s spawnContext) {
	taskDir := filepath.Join(s.instanceRoot, ".niwa", "tasks", taskID)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var skip bool
	var attemptNumber int
	updErr := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		if cur.State != mcp.TaskStateRunning {
			// Task left the running state between schedule and fire
			// (cancellation, or another mechanism abandoned it).
			skip = true
			return nil, nil, nil
		}
		next := *cur
		next.RestartCount = cur.RestartCount + 1
		attemptNumber = next.RestartCount
		next.Worker = mcp.TaskWorker{
			Role:           role,
			SpawnStartedAt: now,
			// PID + StartTime zeroed — the fresh cmd.Start below will
			// backfill them. Crash reconciliation (Issue 7) reads
			// (pid==0 && spawn_started_at!="") as "spawn in flight" and
			// retries without double-counting.
			PID:       0,
			StartTime: 0,
		}
		next.UpdatedAt = now
		entry := &mcp.TransitionLogEntry{
			Kind:    "spawn",
			At:      now,
			Attempt: next.RestartCount,
			Actor: &mcp.TransitionActor{
				Kind: "daemon",
				PID:  os.Getpid(),
			},
		}
		return &next, entry, nil
	})
	if updErr != nil {
		s.logger.Printf("retry task=%s update_state_err=%v", taskID, updErr)
		return
	}
	if skip {
		s.logger.Printf("retry task=%s skip=not_running", taskID)
		return
	}
	s.logger.Printf("retry task=%s attempt=%d", taskID, attemptNumber)

	// Re-enter the spawn path. inboxEvent.filePath is only consumed by
	// handleInboxEvent for the claim+rename; spawnWorker itself uses
	// only the role + taskID + taskDir, so we can fabricate an
	// inboxEvent with the in-progress path for diagnostic purposes.
	evt := inboxEvent{
		role:     role,
		taskID:   taskID,
		filePath: filepath.Join(s.instanceRoot, ".niwa", "roles", role, "inbox", "in-progress", taskID+".json"),
	}
	spawnWorker(evt, taskDir, s)
}

// stateIsTerminal mirrors mcp's internal isTaskStateTerminal (unexported).
// Keep the predicate local so the cli package doesn't depend on a
// helper we'd otherwise need to export just for this one call site.
func stateIsTerminal(s string) bool {
	switch s {
	case mcp.TaskStateCompleted, mcp.TaskStateAbandoned, mcp.TaskStateCancelled:
		return true
	}
	return false
}

// drainSupervisors waits for wg to reach zero or the timeout to elapse,
// whichever comes first. The timeout cap prevents a stuck supervisor
// from wedging daemon exit.
func drainSupervisors(wg *sync.WaitGroup, timeout time.Duration, logger *log.Logger) {
	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
		logger.Printf("supervisors drained")
	case <-time.After(timeout):
		logger.Printf("supervisor drain timed out")
	}
}

// ---------------------------------------------------------------------
// Issue 7: startup reconciliation and adopted-orphan polling
// ---------------------------------------------------------------------

// reconcileResult summarizes the classification of pre-existing
// state.json entries at daemon startup. The caller wires each bucket
// into the appropriate follow-up path:
//
//   - orphans:       adopted live workers → handed to runOrphanSupervisor
//     for 2 s polling in its own goroutine.
//   - freshRetries:  crash-mid-spawn entries → respawnFreshRetry is
//     called for each (no restart_count bump).
//   - deadWorkers:   dead or PID-reuse entries → a synthetic
//     supervisorExit is pushed through exitCh so Issue 5's
//     classifier bumps restart_count and schedules a retry
//     or abandon via the same code path real exits use.
type reconcileResult struct {
	orphans      []orphanEntry
	freshRetries []string // task IDs classified as spawn_never_completed
	deadWorkers  []string // task IDs classified as dead / PID-reuse
}

// reconcileRunningTasks enumerates .niwa/tasks/*/state.json and
// classifies every task with state == "running" into one of three
// buckets. The classification is conservative: a state.json whose
// worker fields are inconsistent is mapped to dead-worker
// (unexpected-exit) rather than trusted; this matches the design's
// "fail closed" posture.
//
// Classification rules (DESIGN Decision / Issue 7 plan):
//
//	Case A — spawn_never_completed:
//	  worker.pid == 0 AND worker.spawn_started_at != ""
//	  ⇒ state.json was written but cmd.Start never ran (or its
//	    backfill never landed). Caller re-spawns without bumping
//	    restart_count.
//
//	Case B — live orphan:
//	  worker.pid > 0 AND IsPIDAlive(pid, start_time) == true
//	  ⇒ worker is still running under a dead daemon. Stamp
//	    worker.adopted_at, log "adoption", and enroll in the central
//	    loop's orphan poller.
//
//	Case C — dead worker / PID reuse:
//	  worker.pid > 0 AND IsPIDAlive == false (or start_time differs)
//	  ⇒ classify as unexpected_exit; Issue 5 pipeline picks it up.
//
// Tasks in non-running states are skipped — nothing to reconcile.
// Read errors are logged and the task is skipped; the catch-up inbox
// scan can re-surface a queued envelope if one exists.
func reconcileRunningTasks(tasksDir string, logger *log.Logger) reconcileResult {
	var result reconcileResult

	entries, err := os.ReadDir(tasksDir)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Printf("reconcile: read tasks dir err=%v", err)
		}
		return result
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		taskID := entry.Name()
		taskDir := filepath.Join(tasksDir, taskID)

		_, st, err := mcp.ReadState(taskDir)
		if err != nil {
			// Partially written or corrupted state.json. Log and skip;
			// there is no safe classification for unreadable state.
			logger.Printf("reconcile: task=%s read_state_err=%v action=skip", taskID, err)
			continue
		}
		if st.State != mcp.TaskStateRunning {
			continue
		}

		// Case A: spawn_never_completed.
		if st.Worker.PID == 0 && st.Worker.SpawnStartedAt != "" {
			logger.Printf("reconcile: task=%s classify=spawn_never_completed action=fresh_retry", taskID)
			if err := appendCrashRecoveryEntry(taskDir, "crash_recovery_fresh_retry", st.Worker.Role); err != nil {
				logger.Printf("reconcile: task=%s fresh_retry_log_err=%v", taskID, err)
			}
			result.freshRetries = append(result.freshRetries, taskID)
			continue
		}

		// Cases B / C: worker.pid is set. Verify liveness.
		if st.Worker.PID > 0 && mcp.IsPIDAlive(st.Worker.PID, st.Worker.StartTime) {
			// Live orphan: stamp adopted_at and add to the orphan list.
			if err := markAdopted(taskDir); err != nil {
				logger.Printf("reconcile: task=%s adopt_err=%v action=treat_as_dead", taskID, err)
				result.deadWorkers = append(result.deadWorkers, taskID)
				continue
			}
			logger.Printf("reconcile: task=%s classify=live_orphan pid=%d action=adopt", taskID, st.Worker.PID)
			result.orphans = append(result.orphans, orphanEntry{
				taskID:    taskID,
				pid:       st.Worker.PID,
				startTime: st.Worker.StartTime,
			})
			continue
		}

		// Case C: dead worker (IsPIDAlive false) or start_time divergence
		// (PID reuse). Either way: unexpected exit.
		logger.Printf("reconcile: task=%s classify=dead_worker pid=%d action=unexpected_exit", taskID, st.Worker.PID)
		result.deadWorkers = append(result.deadWorkers, taskID)
	}

	return result
}

// appendCrashRecoveryEntry writes a "crash_recovery_fresh_retry" NDJSON
// entry to transitions.log for tasks classified as spawn_never_completed.
// state.json is not mutated here — respawnFreshRetry will zero the
// stale spawn_started_at and rewrite worker fields when the retry runs.
func appendCrashRecoveryEntry(taskDir, kind, role string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	entry := &mcp.TransitionLogEntry{
		Kind: kind,
		At:   now,
		Actor: &mcp.TransitionActor{
			Kind: "daemon",
			PID:  os.Getpid(),
			Role: role,
		},
	}
	// Use UpdateState with a no-op mutator so the append runs under the
	// per-task exclusive flock, matching the write discipline every
	// other transition entry uses.
	return mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		if cur.State != mcp.TaskStateRunning {
			return nil, nil, nil
		}
		next := *cur
		return &next, entry, nil
	})
}

// markAdopted stamps worker.adopted_at on a live-orphan task and
// appends an "adoption" NDJSON entry to transitions.log. Runs under the
// per-task exclusive flock (via UpdateState) so concurrent readers see
// a consistent snapshot.
func markAdopted(taskDir string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		if cur.State != mcp.TaskStateRunning {
			return nil, nil, nil
		}
		next := *cur
		next.Worker.AdoptedAt = now
		next.UpdatedAt = now
		entry := &mcp.TransitionLogEntry{
			Kind:      "adoption",
			At:        now,
			WorkerPID: cur.Worker.PID,
			Actor: &mcp.TransitionActor{
				Kind: "daemon",
				PID:  os.Getpid(),
				Role: cur.Worker.Role,
			},
		}
		return &next, entry, nil
	})
}

// respawnFreshRetry re-enters the spawn path for a task whose previous
// spawn crashed before cmd.Start could land (Case A in
// reconcileRunningTasks). Unlike retrySpawn, this path does NOT bump
// restart_count — the prior attempt never actually started.
//
// The stored worker.role is the authoritative target; fall back to
// TargetRole when worker.role is empty (extremely early crash).
func respawnFreshRetry(taskID string, s spawnContext) {
	taskDir := filepath.Join(s.instanceRoot, ".niwa", "tasks", taskID)
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var role string
	var skip bool
	updErr := mcp.UpdateState(taskDir, func(cur *mcp.TaskState) (*mcp.TaskState, *mcp.TransitionLogEntry, error) {
		if cur.State != mcp.TaskStateRunning {
			skip = true
			return nil, nil, nil
		}
		role = cur.Worker.Role
		if role == "" {
			role = cur.TargetRole
		}
		next := *cur
		// Refresh the spawn marker: a new cmd.Start is about to run.
		// restart_count is intentionally NOT bumped — the prior spawn
		// never actually executed.
		next.Worker.Role = role
		next.Worker.PID = 0
		next.Worker.StartTime = 0
		next.Worker.SpawnStartedAt = now
		next.UpdatedAt = now
		entry := &mcp.TransitionLogEntry{
			Kind:    "spawn",
			At:      now,
			Attempt: cur.RestartCount, // same attempt number; not a retry bump
			Actor: &mcp.TransitionActor{
				Kind: "daemon",
				PID:  os.Getpid(),
				Role: role,
			},
		}
		return &next, entry, nil
	})
	if updErr != nil {
		s.logger.Printf("fresh_retry task=%s update_state_err=%v", taskID, updErr)
		return
	}
	if skip {
		s.logger.Printf("fresh_retry task=%s skip=not_running", taskID)
		return
	}
	s.logger.Printf("fresh_retry task=%s role=%s", taskID, role)

	evt := inboxEvent{
		role:     role,
		taskID:   taskID,
		filePath: filepath.Join(s.instanceRoot, ".niwa", "roles", role, "inbox", "in-progress", taskID+".json"),
	}
	spawnWorker(evt, taskDir, s)
}

// ---------------------------------------------------------------------
// Startup helpers
// ---------------------------------------------------------------------

// loadDaemonConfig reads the PRD timing overrides from env with the
// documented defaults. Parse errors fall back to the default with a
// log warning; the daemon never refuses to start over a bad override.
func loadDaemonConfig(logger *log.Logger) daemonConfig {
	cfg := daemonConfig{
		RetryBackoffs: []time.Duration{30 * time.Second, 60 * time.Second, 90 * time.Second},
		StallWatchdog: 900 * time.Second,
		SIGTermGrace:  5 * time.Second,
	}

	if raw := os.Getenv("NIWA_RETRY_BACKOFF_SECONDS"); raw != "" {
		parsed, err := parseDurationList(raw)
		if err != nil {
			logger.Printf("warning: NIWA_RETRY_BACKOFF_SECONDS=%q invalid (%v); using defaults", raw, err)
		} else {
			cfg.RetryBackoffs = parsed
		}
	}
	if raw := os.Getenv("NIWA_STALL_WATCHDOG_SECONDS"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			cfg.StallWatchdog = time.Duration(v) * time.Second
		} else {
			logger.Printf("warning: NIWA_STALL_WATCHDOG_SECONDS=%q invalid; using default", raw)
		}
	}
	if raw := os.Getenv("NIWA_SIGTERM_GRACE_SECONDS"); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil && v > 0 {
			cfg.SIGTermGrace = time.Duration(v) * time.Second
		} else {
			logger.Printf("warning: NIWA_SIGTERM_GRACE_SECONDS=%q invalid; using default", raw)
		}
	}
	return cfg
}

func parseDurationList(raw string) ([]time.Duration, error) {
	parts := strings.Split(raw, ",")
	out := make([]time.Duration, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q: %w", p, err)
		}
		if v <= 0 {
			return nil, fmt.Errorf("value %d must be positive", v)
		}
		out = append(out, time.Duration(v)*time.Second)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty list")
	}
	return out, nil
}

func formatDurations(ds []time.Duration) string {
	parts := make([]string, 0, len(ds))
	for _, d := range ds {
		parts = append(parts, d.String())
	}
	return strings.Join(parts, ",")
}

// resolveSpawnTarget resolves the spawn binary once per daemon
// lifetime. Precedence:
//
//  1. NIWA_WORKER_SPAWN_COMMAND — literal absolute path. Used verbatim.
//  2. exec.LookPath("claude") — production path.
//
// Failure to resolve either is a startup error (AC per Issue 4).
func resolveSpawnTarget() (spawnTargetInfo, error) {
	var path string
	if override := os.Getenv("NIWA_WORKER_SPAWN_COMMAND"); override != "" {
		if !filepath.IsAbs(override) {
			return spawnTargetInfo{}, fmt.Errorf("NIWA_WORKER_SPAWN_COMMAND must be absolute; got %q", override)
		}
		path = override
	} else {
		p, err := exec.LookPath("claude")
		if err != nil {
			return spawnTargetInfo{}, fmt.Errorf("claude not on PATH and NIWA_WORKER_SPAWN_COMMAND unset: %w", err)
		}
		path = p
	}

	info, err := os.Stat(path)
	if err != nil {
		return spawnTargetInfo{}, fmt.Errorf("stat %s: %w", path, err)
	}

	out := spawnTargetInfo{Path: path, Mode: info.Mode()}
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		out.UID = sys.Uid
	}
	return out, nil
}

// registerInboxWatches enumerates .niwa/roles/<role>/inbox/ and adds a
// watch on each. Returns the role names that were successfully
// registered so the caller can log the count and the catch-up scan can
// walk them.
func registerInboxWatches(watcher *fsnotify.Watcher, rolesRoot string, logger *log.Logger) ([]string, error) {
	entries, err := os.ReadDir(rolesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			// No roles/ directory means channels have not been provisioned
			// on this instance. We still run — the catch-up scan and
			// watcher loop will find nothing — so the daemon doesn't
			// need a second code path to handle unchanneled instances.
			return nil, nil
		}
		return nil, fmt.Errorf("reading roles dir: %w", err)
	}

	var registered []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		inboxDir := filepath.Join(rolesRoot, e.Name(), "inbox")
		if _, err := os.Stat(inboxDir); err != nil {
			logger.Printf("warning: role %s missing inbox dir: %v", e.Name(), err)
			continue
		}
		if err := watcher.Add(inboxDir); err != nil {
			logger.Printf("warning: could not watch inbox for role %s: %v", e.Name(), err)
			continue
		}
		registered = append(registered, e.Name())
	}
	return registered, nil
}

// scanExistingInboxes lists each role's inbox/ directory for
// pre-existing <task-id>.json envelopes. The returned events feed the
// central loop on the same path as fsnotify-driven events. Subdirs
// (in-progress, cancelled, expired, read) are skipped because they
// represent already-processed states.
func scanExistingInboxes(rolesRoot string, roles []string) ([]inboxEvent, error) {
	var events []inboxEvent
	for _, role := range roles {
		inboxDir := filepath.Join(rolesRoot, role, "inbox")
		entries, err := os.ReadDir(inboxDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			events = append(events, inboxEvent{
				role:     role,
				taskID:   strings.TrimSuffix(name, ".json"),
				filePath: filepath.Join(inboxDir, name),
			})
		}
	}
	return events, nil
}

// roleFromInboxPath parses role from .../niwa/roles/<role>/inbox/<id>.json.
// Returns "" if the path is not shaped like an inbox file (e.g., a
// file in a subdirectory of inbox).
func roleFromInboxPath(p string) string {
	parent := filepath.Dir(p)
	if filepath.Base(parent) != "inbox" {
		return ""
	}
	return filepath.Base(filepath.Dir(parent))
}

// resolveRoleCWD returns the absolute CWD for a worker of the given
// role. Coordinator workers run at the instance root; non-coordinators
// run in their repo directory, located by scanning groups (depth-2) for
// a directory matching the role name. Falls back to the instance root
// if no match is found (e.g., virtual peer).
func resolveRoleCWD(instanceRoot, role string) string {
	if role == "coordinator" {
		return instanceRoot
	}
	entries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return instanceRoot
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		groupDir := filepath.Join(instanceRoot, entry.Name())
		repoEntries, err := os.ReadDir(groupDir)
		if err != nil {
			continue
		}
		for _, repo := range repoEntries {
			if !repo.IsDir() {
				continue
			}
			if repo.Name() == role {
				return filepath.Join(groupDir, role)
			}
		}
	}
	return instanceRoot
}

// errDaemonAlreadyRunning is returned by acquireDaemonPIDLock when the
// exclusive flock is already held. Issue 7: two daemons must not coexist
// for the same instance; the loser exits cleanly with code 0 after
// logging a one-line notice.
var errDaemonAlreadyRunning = errors.New("another daemon is running")

// acquireDaemonPIDLock opens .niwa/daemon.pid.lock and takes an
// exclusive non-blocking flock. The caller holds this flock for the
// daemon's lifetime.
//
// Returns errDaemonAlreadyRunning when EWOULDBLOCK fires — another
// daemon already holds the exclusive lock for this instance. The caller
// treats that as a clean "another daemon wins" exit, not a startup
// error. Any other error is propagated as-is (fatal).
//
// `niwa apply` reads daemon.pid under a shared lock on the same file
// (see workspace.EnsureDaemonRunning); the exclusive-vs-shared pairing
// is what enforces "concurrent niwa apply never spawns two daemons"
// (AC-C3 / scenario-21).
func acquireDaemonPIDLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errDaemonAlreadyRunning
		}
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	return f, nil
}

// writePIDFile writes the current PID and start time to daemon.pid
// atomically (tmp + rename). Format is "<pid>\n<start-time>\n" where
// start-time is /proc/<pid>/stat field 22 (zero if unavailable).
func writePIDFile(niwaDir string) error {
	pid := os.Getpid()
	startTime, err := mcp.PIDStartTime(pid)
	if err != nil {
		startTime = 0
	}
	content := fmt.Sprintf("%d\n%d\n", pid, startTime)
	tmpPath := filepath.Join(niwaDir, "daemon.pid.tmp")
	finalPath := filepath.Join(niwaDir, "daemon.pid")
	if err := os.WriteFile(tmpPath, []byte(content), 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, finalPath)
}
