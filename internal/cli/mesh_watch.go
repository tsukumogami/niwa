package cli

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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
	Long: `Run the mesh watch daemon that monitors session inboxes and
auto-resumes dead Claude sessions when new messages arrive.

The daemon writes a PID file at <instance-root>/.niwa/daemon.pid and
logs to <instance-root>/.niwa/daemon.log. Send SIGTERM to request a
clean shutdown.`,
	RunE: runMeshWatch,
}

func runMeshWatch(cmd *cobra.Command, args []string) error {
	instanceRoot := meshWatchInstanceRoot

	// Validate instance root exists.
	if _, err := os.Stat(instanceRoot); os.IsNotExist(err) {
		return fmt.Errorf("instance root does not exist: %s", instanceRoot)
	}

	niwaDir := filepath.Join(instanceRoot, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o700); err != nil {
		return fmt.Errorf("creating .niwa directory: %w", err)
	}

	// Open log file (append mode).
	logPath := filepath.Join(niwaDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening daemon log: %w", err)
	}
	defer logFile.Close()

	logger := log.New(logFile, "", log.LstdFlags)
	logger.Printf("daemon starting pid=%d instance-root=%s", os.Getpid(), instanceRoot)

	sessionsDir := filepath.Join(niwaDir, "sessions")

	// Set up fsnotify watcher before writing PID file (AC3: rename only after
	// watch loop is established).
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating fsnotify watcher: %w", err)
	}
	defer watcher.Close()

	// Watch existing inbox directories.
	if err := watchExistingInboxes(watcher, sessionsDir, logger); err != nil {
		logger.Printf("warning: could not watch initial inbox dirs: %v", err)
	}

	// Write PID file atomically after watcher is ready.
	if err := writePIDFile(niwaDir); err != nil {
		return fmt.Errorf("writing PID file: %w", err)
	}

	pidFilePath := filepath.Join(niwaDir, "daemon.pid")
	logger.Printf("daemon ready, PID file written")

	// SIGTERM handler for clean shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)

	// Semaphore to limit in-flight resume goroutines (5-second drain on shutdown).
	var wg sync.WaitGroup
	done := make(chan struct{})

	go func() {
		sig := <-sigCh
		logger.Printf("received signal %v, initiating shutdown", sig)
		close(done)
	}()

	logger.Printf("watch loop started")

	// Ticker for periodic existence check of the sessions directory.
	// This ensures the daemon exits promptly even when no fsnotify events arrive.
	existenceTicker := time.NewTicker(500 * time.Millisecond)
	defer existenceTicker.Stop()

	for {
		select {
		case <-existenceTicker.C:
			// Per-iteration existence check: exit cleanly if sessions dir is removed.
			if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
				logger.Printf("sessions directory removed, exiting cleanly")
				wg.Wait()
				_ = os.Remove(pidFilePath)
				return nil
			}

		case <-done:
			logger.Printf("shutting down, draining in-flight work (up to 5s)")
			waitDone := make(chan struct{})
			go func() {
				wg.Wait()
				close(waitDone)
			}()
			select {
			case <-waitDone:
				logger.Printf("all work drained")
			case <-time.After(5 * time.Second):
				logger.Printf("drain timeout exceeded")
			}
			_ = os.Remove(pidFilePath)
			logger.Printf("daemon exiting")
			return nil

		case event, ok := <-watcher.Events:
			if !ok {
				logger.Printf("watcher events channel closed, exiting")
				wg.Wait()
				_ = os.Remove(pidFilePath)
				return nil
			}

			if !event.Has(fsnotify.Create) {
				continue
			}

			name := filepath.Base(event.Name)
			if !strings.HasSuffix(name, ".json") {
				info, err := os.Stat(event.Name)
				if err != nil || !info.IsDir() {
					continue
				}
				if name == "inbox" {
					// New inbox dir: watch it for message files.
					_ = watcher.Add(event.Name)
					logger.Printf("added new inbox dir to watcher: %s", event.Name)
				} else {
					// New session UUID dir: watch it so we see the inbox when created.
					_ = watcher.Add(event.Name)
					// If inbox already exists (race), add it directly.
					inboxDir := filepath.Join(event.Name, "inbox")
					if _, err := os.Stat(inboxDir); err == nil {
						_ = watcher.Add(inboxDir)
					}
				}
				continue
			}

			// New message file: handle in goroutine so watcher loop keeps running.
			path := event.Name
			wg.Add(1)
			go func() {
				defer wg.Done()
				handleNewMessage(path, sessionsDir, logger)
			}()

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			logger.Printf("watcher error: %v", err)
		}
	}
}

// watchExistingInboxes adds all currently-existing session inbox directories
// to the watcher. New session inboxes created after startup are picked up
// when we detect a directory creation event in the parent sessions dir.
func watchExistingInboxes(watcher *fsnotify.Watcher, sessionsDir string, logger *log.Logger) error {
	entries, err := os.ReadDir(sessionsDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	// Also watch the sessions dir itself to detect new session directories.
	if err := watcher.Add(sessionsDir); err != nil {
		logger.Printf("warning: could not watch sessions dir: %v", err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		inboxDir := filepath.Join(sessionsDir, e.Name(), "inbox")
		if _, err := os.Stat(inboxDir); err == nil {
			if err := watcher.Add(inboxDir); err != nil {
				logger.Printf("warning: could not watch inbox %s: %v", inboxDir, err)
			}
		}
	}
	return nil
}

// handleNewMessage processes a new message file event. It reads sessions.json
// (with advisory lock), finds the target session entry, checks liveness, and
// if dead, calls `claude --resume <session-id>`.
func handleNewMessage(msgPath, sessionsDir string, logger *log.Logger) {
	claudeBin, _ := exec.LookPath("claude")
	// Brief pause to let the write complete before reading.
	time.Sleep(10 * time.Millisecond)

	// Parse the message to get ID and type for logging (never log body content).
	data, err := os.ReadFile(msgPath)
	if err != nil {
		logger.Printf("could not read message file %s: %v", msgPath, err)
		return
	}

	var msg mcp.Message
	if err := json.Unmarshal(data, &msg); err != nil {
		logger.Printf("could not parse message file %s: %v", msgPath, err)
		return
	}

	logger.Printf("new message id=%s type=%s", msg.ID, msg.Type)

	// Determine which session this inbox belongs to (parent dir of inbox is session UUID).
	// Path structure: .niwa/sessions/<session-uuid>/inbox/<msg>.json
	sessionDir := filepath.Dir(filepath.Dir(msgPath))
	sessionUUID := filepath.Base(sessionDir)

	// Read sessions.json with advisory lock.
	lockPath := filepath.Join(filepath.Dir(sessionsDir), ".sessions.lock")
	lockFile, err := tryLockFileWithTimeout(lockPath, time.Second)
	if err != nil {
		logger.Printf("could not acquire sessions lock for message %s: %v (skipping)", msg.ID, err)
		return
	}
	defer func() {
		_ = lockFile.Close()
	}()

	sessionsJSON := filepath.Join(sessionsDir, "sessions.json")
	regData, err := os.ReadFile(sessionsJSON)
	if err != nil {
		logger.Printf("could not read sessions.json: %v", err)
		return
	}

	var registry mcp.SessionRegistry
	if err := json.Unmarshal(regData, &registry); err != nil {
		logger.Printf("could not parse sessions.json: %v", err)
		return
	}

	// Find the entry for this session UUID.
	for _, entry := range registry.Sessions {
		if entry.ID != sessionUUID {
			continue
		}

		alive := mcp.IsPIDAlive(entry.PID, entry.StartTime)
		if alive {
			logger.Printf("session %s (role=%s) is alive, no resume needed", sessionUUID, entry.Role)
			return
		}

		if entry.ClaudeSessionID == "" {
			logger.Printf("session %s (role=%s) is dead but has no claude_session_id, cannot resume", sessionUUID, entry.Role)
			return
		}

		if claudeBin == "" {
			logger.Printf("session %s (role=%s) is dead but claude not on PATH, cannot resume", sessionUUID, entry.Role)
			return
		}

		logger.Printf("session %s (role=%s) is dead, resuming claude_session_id=%s", sessionUUID, entry.Role, entry.ClaudeSessionID)

		// Release lock before spawning claude.
		_ = lockFile.Close()

		go func(sessionID string) {
			resumeCmd := exec.Command(claudeBin, "--resume", sessionID)
			if err := resumeCmd.Start(); err != nil {
				logger.Printf("failed to start claude --resume %s: %v", sessionID, err)
				return
			}
			logger.Printf("spawned claude --resume %s (pid=%d)", sessionID, resumeCmd.Process.Pid)
			// Detach: don't Wait() so we don't block.
		}(entry.ClaudeSessionID)

		return
	}

	logger.Printf("no session entry found for uuid %s", sessionUUID)
}

// tryLockFileWithTimeout attempts to acquire an exclusive advisory lock on
// the file at path. It retries for up to timeout before returning an error.
func tryLockFileWithTimeout(path string, timeout time.Duration) (*os.File, error) {
	deadline := time.Now().Add(timeout)
	for {
		f, err := tryLockFile(path)
		if err == nil {
			return f, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out acquiring lock on %s: %w", path, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// tryLockFile attempts to acquire an exclusive advisory lock on the file at
// path. Returns immediately with error if the lock is held by another process.
func tryLockFile(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// writePIDFile writes the current PID and start time to daemon.pid atomically.
// Format: "<pid>\n<start-jiffies>\n"
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

// ReadPIDFile reads the daemon PID file at <niwaDir>/daemon.pid and returns
// the pid and startTime. Returns (0, 0, nil) if the file does not exist.
func ReadPIDFile(niwaDir string) (pid int, startTime int64, err error) {
	pidPath := filepath.Join(niwaDir, "daemon.pid")
	data, err := os.ReadFile(pidPath)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}

	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 {
		return 0, 0, fmt.Errorf("daemon.pid: empty file")
	}

	var p int
	if _, err := fmt.Sscanf(lines[0], "%d", &p); err != nil {
		return 0, 0, fmt.Errorf("daemon.pid: invalid pid: %w", err)
	}

	var st int64
	if len(lines) >= 2 {
		if _, err := fmt.Sscanf(lines[1], "%d", &st); err != nil {
			st = 0
		}
	}

	return p, st, nil
}
