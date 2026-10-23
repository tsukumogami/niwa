package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The on-reap command: [session.on_reap] in the workspace configuration, run
// once for each session niwa removes.
//
// It runs where niwa removes a session and nowhere else: the mapped reap
// (which `niwa reap` runs, and `niwa dispatch`, `niwa create` and `niwa watch`
// run first, idle-grace reclaim included) and its walk over stored mappings,
// `niwa destroy` when it removes a session or finds it already gone, and
// `niwa watch`'s discard of a staged review whose session is gone. Not
// `niwa reset`, `niwa destroy --keep-session`, the reap of an unmapped
// instance, or a failed dispatch's rollback: none of those removes a session
// niwa knows by id.
//
// A run never changes the outcome of the step it runs in. A failure, a
// timeout or a skip prints one warning line and keeps the session's mapping,
// so the next reap that finds the session gone runs the command again; after
// onReapFailureLimit failed runs the mapping is dropped with a final warning.

const (
	// onReapFailureLimit is the failed run that drops a mapping: it is kept
	// through the first nine and dropped, with a final warning, on the tenth.
	onReapFailureLimit = 10

	// onReapWaitDelay bounds how long a run waits, once its shell has exited
	// or been killed, for the pipes a leftover grandchild may hold open. It is
	// what keeps a run within its timeout plus one second.
	onReapWaitDelay = 500 * time.Millisecond

	// maxOnReapReasonBytes bounds the reason a warning line prints.
	maxOnReapReasonBytes = 200

	// maxOnReapStderrBytes bounds how much of a run's stderr is kept to quote
	// in its warning.
	maxOnReapStderrBytes = 4096
)

// onReapStepBudget is the wall time all of one step's runs may take together.
// A run the budget can't start is skipped. A variable so a test can shorten it.
var onReapStepBudget = 30 * time.Second

// onReapShell is the shell the command runs under, by absolute path so the
// PATH niwa runs with can't substitute another one.
var onReapShell = "/bin/sh"

// workspaceConfigPath is the workspace configuration niwa reads at dispatch
// and at reap.
func workspaceConfigPath(workspaceRoot string) string {
	return filepath.Join(workspaceRoot, workspace.StateDir, workspace.WorkspaceConfigFile)
}

// loadWorkspaceConfig loads the workspace configuration under workspaceRoot.
// A missing file returns an error that satisfies errors.Is(err,
// fs.ErrNotExist), which a caller may read as "nothing declared".
func loadWorkspaceConfig(workspaceRoot string) (*config.WorkspaceConfig, error) {
	res, err := config.Load(workspaceConfigPath(workspaceRoot))
	if err != nil {
		return nil, err
	}
	return res.Config, nil
}

// reapKeyFor recomputes a session's reap key from its mapping: the dispatch id
// when the mapping has one, the session id otherwise. It is the value the
// session was given at launch or by its SessionStart hook.
func reapKeyFor(m workspace.SessionMapping) string {
	if m.DispatchID != "" {
		return m.DispatchID
	}
	return m.SessionID
}

// removedSession is what the on-reap command is told about one session.
type removedSession struct {
	SessionID    string
	ReapKey      string
	InstancePath string
}

// onReapStep is one niwa step's on-reap runs. It loads [session.on_reap] once,
// on the first run that needs it, and holds the step's budget.
type onReapStep struct {
	workspaceRoot string
	// out takes the command's standard output and every warning line.
	out io.Writer

	loaded bool
	cfg    config.SessionOnReapConfig
	cfgErr error

	started time.Time
}

func newOnReapStep(workspaceRoot string, out io.Writer) *onReapStep {
	return &onReapStep{workspaceRoot: workspaceRoot, out: out}
}

// load reads [session.on_reap] once. A workspace with no configuration file
// at all declares no command. A file that exists and can't be read or parsed
// is an error, and every run in the step fails on it.
func (s *onReapStep) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	cfg, err := loadWorkspaceConfig(s.workspaceRoot)
	switch {
	case err == nil:
		s.cfg = cfg.Session.OnReap
	case errors.Is(err, fs.ErrNotExist):
		// No configuration, so no command.
	default:
		s.cfgErr = err
	}
}

// warn prints the one warning line a run that didn't succeed gets.
func (s *onReapStep) warn(sessionID, reason string) {
	fmt.Fprintf(s.out, "niwa: warning: on-reap for session %s: %s\n",
		sanitizeOnReapText(sessionID, 80), sanitizeOnReapText(reason, maxOnReapReasonBytes))
}

// run runs the command for one removed session and reports whether it
// succeeded. A workspace that declares no command succeeds without running
// anything. Anything else that keeps the command from succeeding has printed
// its warning line by the time this returns false.
func (s *onReapStep) run(r removedSession) bool {
	s.load()
	if s.cfgErr != nil {
		s.warn(r.SessionID, fmt.Sprintf("could not read [session.on_reap] from %s: %v", workspaceConfigPath(s.workspaceRoot), s.cfgErr))
		return false
	}
	command := strings.TrimSpace(s.cfg.Command)
	if command == "" {
		return true
	}
	// Both values reach the command's environment, and a command is free to
	// put them on a command line of its own, so each is checked here, at
	// reap, whatever was checked when it was written.
	if !validReapKey(r.ReapKey) {
		s.warn(r.SessionID, "its reap key is not a plain identifier, so the command was not run")
		return false
	}
	if !validReapKey(r.SessionID) {
		s.warn(r.SessionID, "its session id is not a plain identifier, so the command was not run")
		return false
	}

	if s.started.IsZero() {
		s.started = time.Now()
	}
	remaining := onReapStepBudget - time.Since(s.started)
	if remaining <= 0 {
		s.warn(r.SessionID, fmt.Sprintf("skipped: this step's %s on-reap budget is spent; a later reap runs it", onReapStepBudget))
		return false
	}
	timeout := s.cfg.TimeoutDuration()
	if timeout > remaining {
		timeout = remaining
	}

	if reason := s.exec(command, timeout, r); reason != "" {
		s.warn(r.SessionID, reason)
		return false
	}
	return true
}

// exec runs command once and returns "" on success or the reason it failed.
//
// The command runs under sh -c from the workspace root, in its own process
// group, with stdin at /dev/null. On timeout the whole group is killed. Its
// standard output goes to s.out; its standard error is kept, bounded, so the
// warning can quote its last line. cmd.WaitDelay stops Wait from waiting on a
// pipe a grandchild still holds, including one that left the group with
// setsid and so outlived the kill.
func (s *onReapStep) exec(command string, timeout time.Duration, r removedSession) string {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	stderr := &tailBuffer{max: maxOnReapStderrBytes}
	cmd := exec.CommandContext(ctx, onReapShell, "-c", command)
	cmd.Dir = s.workspaceRoot
	cmd.Env = onReapEnv(s.cfg.Env, r)
	cmd.Stdout = s.out
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// The group id is the shell's pid (Setpgid with Pgid 0).
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = onReapWaitDelay

	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return ""
	case ctx.Err() != nil:
		return fmt.Sprintf("timed out after %s", timeout)
	case errors.Is(err, exec.ErrWaitDelay):
		// The shell exited 0 and something it started kept a pipe open.
		return ""
	case errors.As(err, &exitErr):
		reason := fmt.Sprintf("exited with status %d", exitErr.ExitCode())
		if line := lastLine(stderr.String()); line != "" {
			reason += ": " + line
		}
		return reason
	default:
		return fmt.Sprintf("could not start: %v", err)
	}
}

// onReapEnv is the command's whole environment: PATH and HOME from niwa's,
// the three values that name the removed session, and each name the
// workspace lists that is set in niwa's environment. Nothing else from niwa's
// environment reaches it.
func onReapEnv(declared []string, r removedSession) []string {
	var env []string
	seen := map[string]bool{}
	add := func(name, value string) {
		if seen[name] || strings.ContainsRune(value, 0) {
			return
		}
		seen[name] = true
		env = append(env, name+"="+value)
	}
	for _, name := range []string{"PATH", "HOME"} {
		if v, ok := os.LookupEnv(name); ok {
			add(name, v)
		}
	}
	add(config.ReapKeyEnv, r.ReapKey)
	add("NIWA_SESSION_ID", r.SessionID)
	add("NIWA_INSTANCE_PATH", r.InstancePath)
	for _, name := range declared {
		if v, ok := os.LookupEnv(name); ok {
			add(name, v)
		}
	}
	return env
}

// settle runs the command for a removed session's mapping and settles the
// mapping: deleted when the run succeeds, kept with its failure count raised
// when it doesn't, and dropped with a final warning once the count reaches
// onReapFailureLimit.
func (s *onReapStep) settle(m workspace.SessionMapping) {
	if s.run(removedSession{SessionID: m.SessionID, ReapKey: reapKeyFor(m), InstancePath: m.InstancePath}) {
		s.deleteMapping(m.SessionID)
		return
	}
	m.OnReapFailures++
	if m.OnReapFailures >= onReapFailureLimit {
		s.warn(m.SessionID, fmt.Sprintf("gave up after %d failed runs; dropped its session record", onReapFailureLimit))
		s.deleteMapping(m.SessionID)
		return
	}
	if err := workspace.WriteSessionMapping(s.workspaceRoot, m); err != nil {
		fmt.Fprintf(s.out, "niwa: warning: recording the failed on-reap run for session %s: %v\n", sanitizeOnReapText(m.SessionID, 80), err)
	}
}

func (s *onReapStep) deleteMapping(sessionID string) {
	if err := workspace.DeleteSessionMapping(s.workspaceRoot, sessionID); err != nil {
		fmt.Fprintf(s.out, "niwa: warning: deleting session mapping %s: %v\n", sanitizeOnReapText(sessionID, 80), err)
	}
}

// sanitizeOnReapText makes text safe for one terminal line: invalid UTF-8 and
// control characters become spaces, runs of spaces collapse, and the result
// is cut to at most max bytes, with "..." marking a cut.
func sanitizeOnReapText(text string, max int) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToValidUTF8(text, " ") {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			if !space {
				b.WriteByte(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if len(out) <= max {
		return out
	}
	cut := max - len("...")
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return out[:cut] + "..."
}

// lastLine returns the last non-blank line of s.
func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

// tailBuffer keeps the last max bytes written to it. It is written from the
// copying goroutine exec starts, so it locks.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[over:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
