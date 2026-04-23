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

	// 4. Catch-up inbox scan. Run BEFORE the event loop so pre-existing
	// envelopes that arrived while the daemon was down are queued through
	// the same claim code path as fsnotify-driven events.
	catchupEvents, err := scanExistingInboxes(rolesRoot, watchedRoles)
	if err != nil {
		logger.Printf("warning: catch-up scan failed: %v", err)
	}

	// 5. Acquire the daemon.pid.lock flock BEFORE writing the PID file.
	// Issue 7 uses this sidecar file to gate concurrent `niwa apply`
	// invocations; Issue 4 just establishes the file and holds it for
	// our own write.
	pidLockPath := filepath.Join(niwaDir, "daemon.pid.lock")
	pidLockFile, err := acquireDaemonPIDLock(pidLockPath)
	if err != nil {
		return fmt.Errorf("acquiring daemon.pid.lock: %w", err)
	}
	defer func() {
		_ = syscall.Flock(int(pidLockFile.Fd()), syscall.LOCK_UN)
		_ = pidLockFile.Close()
	}()

	// 6. Write PID file atomically AFTER watches are registered so
	// EnsureDaemonRunning's "pid-file-appears" signal means the daemon
	// really can accept events.
	if err := writePIDFile(niwaDir); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}
	pidFilePath := filepath.Join(niwaDir, "daemon.pid")
	logger.Printf("daemon ready, PID file written")

	// 7. Central event loop. Everything state-changing flows through
	// this goroutine: fsnotify events, catch-up queue, per-task exits.
	spawnCtx := spawnContext{
		instanceRoot: instanceRoot,
		niwaDir:      niwaDir,
		spawnBin:     spawnInfo.Path,
		logger:       logger,
		exitCh:       exitCh,
		wg:           &supervisorWG,
		shutdownCtx:  ctx,
		backoffs:     cfg.RetryBackoffs,
	}

	// Replay catch-up events through a channel so the central loop has a
	// single `select` point.
	catchupCh := make(chan inboxEvent, len(catchupEvents)+1)
	for _, evt := range catchupEvents {
		catchupCh <- evt
	}
	close(catchupCh)

	logger.Printf("watch loop started")
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
type spawnContext struct {
	instanceRoot string
	niwaDir      string
	spawnBin     string
	logger       *log.Logger
	exitCh       chan<- supervisorExit
	wg           *sync.WaitGroup
	shutdownCtx  context.Context
	backoffs     []time.Duration
}

// runEventLoop owns the central `select`. It returns only when ctx is
// cancelled (signal) or fsnotify closes its events channel.
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

// handleInboxEvent runs the claim → spawn flow for a single queued
// envelope. Failures are logged and do NOT abort the loop — the daemon
// must remain responsive to other inboxes.
func handleInboxEvent(evt inboxEvent, s spawnContext) {
	taskDir := filepath.Join(s.instanceRoot, ".niwa", "tasks", evt.taskID)
	if _, err := os.Stat(filepath.Join(taskDir, "state.json")); err != nil {
		// Dangling inbox file — no task dir. Move the envelope out of the
		// queued inbox so fsnotify does not re-fire CREATE events for it
		// on every daemon startup or sibling write. The file lands in
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

	cmd := exec.Command(
		s.spawnBin,
		"-p", prompt,
		"--permission-mode=acceptEdits",
		"--mcp-config="+mcpConfigPath,
		"--strict-mcp-config",
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

	// Supervisor goroutine: wait for exit, close the stderr fd we
	// allocated above, then report back to the central loop.
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

// acquireDaemonPIDLock opens .niwa/daemon.pid.lock and takes an
// exclusive flock. Issue 7 uses the same sidecar for `niwa apply`
// coordination; Issue 4 just establishes the file and holds it for the
// daemon's own write.
func acquireDaemonPIDLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
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
