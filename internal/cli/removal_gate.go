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
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The removal gate: [session.before_remove] in the workspace configuration,
// run immediately before niwa removes a path a session may still need.
//
// It is how something outside niwa -- a workflow engine that knows which runs
// are bound under a directory -- keeps that directory on disk, without niwa
// learning anything about the engine. niwa hands the command the path and the
// sessions it maps there and reads one thing back: the exit status. Exit 0
// lets the removal go ahead. Any other exit, a run past its timeout, a command
// that can't start, or a configuration that can't be read keeps the path for
// this removal, and niwa prints one line naming the instance and why. The
// mapping is left exactly as it was, so the next removal pass asks again.
//
// Every failure keeps the path on purpose. A gate that removed when it couldn't
// get an answer would delete exactly the directories it exists to keep; a gate
// that keeps on failure can at worst keep directories until it is fixed, and
// says so on every pass.
//
// The command's output never reaches niwa's own streams. Both are captured,
// bounded, and only the last non-empty line is printed, sanitized and quoted,
// so a command can't put terminal escapes or text that reads like niwa's own
// words into niwa's output.

// Step names, which the command reads as NIWA_REMOVE_STEP.
const (
	removeStepReap = "reap"
)

const (
	// maxRemovalGateReasonBytes bounds the reason a spare line quotes.
	maxRemovalGateReasonBytes = 200
	// maxRemovalGateOutputBytes bounds how much of a run's output is kept to
	// find the reason in.
	maxRemovalGateOutputBytes = 4096
)

// removalGateBudget is the wall time one pass's runs may take together. A run
// that would start after it is spent keeps its path without running. A
// variable so a test can shorten it.
var removalGateBudget = 30 * time.Second

// removalGate is one removal pass's pre-removal runs. It loads
// [session.before_remove] once, on the first ask that needs it, and holds the
// pass's budget. Each pass builds its own, so each has its own budget.
type removalGate struct {
	workspaceRoot string
	step          string
	// out takes every spare line. The command's own output never goes here.
	out io.Writer

	loaded bool
	cfg    config.SessionBeforeRemoveConfig
	cfgErr error

	started time.Time
}

func newRemovalGate(workspaceRoot, step string, out io.Writer) *removalGate {
	return &removalGate{workspaceRoot: workspaceRoot, step: step, out: out}
}

// load reads [session.before_remove] once. A workspace with no configuration
// file declares no command. A file that exists and can't be read or parsed is
// an error, and every ask in the pass keeps its path on it.
func (g *removalGate) load() {
	if g.loaded {
		return
	}
	g.loaded = true
	cfg, err := loadWorkspaceConfig(g.workspaceRoot)
	switch {
	case err == nil:
		g.cfg = cfg.Session.BeforeRemove
	case errors.Is(err, fs.ErrNotExist):
		// No configuration, so no command.
	default:
		g.cfgErr = err
	}
}

// active reports whether asking can do anything but allow: a command is
// declared, or the declaration couldn't be read. A caller uses it to skip
// work it only does for the gate.
func (g *removalGate) active() bool {
	g.load()
	return g.cfgErr != nil || strings.TrimSpace(g.cfg.Command) != ""
}

// ask runs the command for one path and reports whether the removal may go
// ahead. With no command declared it allows without starting anything. When
// it keeps the path it has printed the spare line by the time it returns.
func (g *removalGate) ask(path string, sessionIDs []string) bool {
	if !g.active() {
		return true
	}
	name := filepath.Base(path)
	if g.cfgErr != nil {
		g.spare(name, fmt.Sprintf("could not read [session.before_remove] from %s: %v", workspaceConfigPath(g.workspaceRoot), g.cfgErr))
		return false
	}

	if g.started.IsZero() {
		g.started = time.Now()
	}
	remaining := removalGateBudget - time.Since(g.started)
	if remaining <= 0 {
		g.spare(name, fmt.Sprintf("this step's %s before_remove time ran out before it could ask", removalGateBudget))
		return false
	}
	timeout := g.cfg.TimeoutDuration()
	if timeout > remaining {
		timeout = remaining
	}

	allowed, reason := g.exec(strings.TrimSpace(g.cfg.Command), timeout, path, sessionIDs)
	if !allowed {
		g.spare(name, reason)
	}
	return allowed
}

// spare prints the one line a kept path gets.
func (g *removalGate) spare(name, reason string) {
	fmt.Fprintf(g.out, "niwa: kept %s: %s\n", sanitizeRemovalGateText(name, 80), reason)
}

// exec runs command once. It returns true when the command exited 0, and
// otherwise false with the reason the spare line prints.
//
// The command runs under sh -c from the workspace root, in its own process
// group, with stdin at /dev/null. On timeout the whole group is killed.
// cmd.WaitDelay stops Wait from waiting on a pipe a grandchild still holds.
func (g *removalGate) exec(command string, timeout time.Duration, path string, sessionIDs []string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	output := &tailBuffer{max: maxRemovalGateOutputBytes}
	cmd := exec.CommandContext(ctx, onReapShell, "-c", command)
	cmd.Dir = g.workspaceRoot
	cmd.Env = removalGateEnv(g.cfg.Env, path, g.step, sessionIDs)
	cmd.Stdout = output
	cmd.Stderr = output
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
		return true, ""
	case ctx.Err() != nil:
		return false, fmt.Sprintf("before_remove timed out after %s", timeout)
	case errors.Is(err, exec.ErrWaitDelay):
		// The shell exited 0 and something it started kept a pipe open.
		return true, ""
	case errors.As(err, &exitErr):
		if line := lastLine(output.String()); line != "" {
			return false, "reason from before_remove: " + strconv.Quote(sanitizeRemovalGateText(line, maxRemovalGateReasonBytes))
		}
		return false, fmt.Sprintf("before_remove exited with status %d", exitErr.ExitCode())
	default:
		return false, "before_remove could not start: " + sanitizeRemovalGateText(err.Error(), maxRemovalGateReasonBytes)
	}
}

// removalGateEnv is the command's whole environment: PATH and HOME from
// niwa's, the three values that say what is about to be removed and by which
// step, and each name the workspace lists that is set in niwa's environment.
// Nothing else from niwa's environment reaches it. NIWA_SESSION_IDS carries
// only ids that are well-formed session ids, space-separated, and is empty
// when niwa maps no session to the path.
func removalGateEnv(declared []string, path, step string, sessionIDs []string) []string {
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
	var ids []string
	for _, id := range sessionIDs {
		if workspace.ValidSessionID(id) {
			ids = append(ids, id)
		}
	}
	add("NIWA_REMOVE_PATH", path)
	add("NIWA_REMOVE_STEP", step)
	add("NIWA_SESSION_IDS", strings.Join(ids, " "))
	for _, name := range declared {
		if v, ok := os.LookupEnv(name); ok {
			add(name, v)
		}
	}
	return env
}

// sanitizeRemovalGateText is sanitizeOnReapText with Unicode's bidirectional
// controls also turned into spaces. unicode.IsControl covers the C0 and C1
// ranges but not these, and an override would let a command's text print in
// an order other than the one it was written in.
func sanitizeRemovalGateText(text string, max int) string {
	text = strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Bidi_Control, r) {
			return ' '
		}
		return r
	}, text)
	return sanitizeOnReapText(text, max)
}

// sessionIDsByInstance groups the workspace's mapped session ids by instance
// path, for the NIWA_SESSION_IDS a removal hands the gate.
func sessionIDsByInstance(workspaceRoot string) map[string][]string {
	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil {
		return nil
	}
	byPath := make(map[string][]string, len(mappings))
	for _, m := range mappings {
		if m.InstancePath != "" {
			byPath[m.InstancePath] = append(byPath[m.InstancePath], m.SessionID)
		}
	}
	return byPath
}
