package functional

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cucumber/godog"
)

// dispatch_interrupt_steps_test.go holds the steps for
// features/dispatch-interrupt.feature: a niwa dispatch started without waiting
// for it, signalled partway through, and the processes it leaves behind (or
// doesn't) afterwards.

// backgroundNiwa is a niwa command a scenario started and has not yet waited
// for. done is closed once it has exited; err and the two streams are only
// read after that.
type backgroundNiwa struct {
	cmd    *exec.Cmd
	done   chan struct{}
	err    error
	stdout strings.Builder
	stderr strings.Builder
}

// interruptFakes is what the interrupt scenarios' fakes report back through.
type interruptFakes struct {
	// remoteURL is the never-answering remote's https URL, and reached is
	// closed when it accepts its first connection.
	remoteURL string
	reached   chan struct{}
}

// Each fake's own marker files, under the scenario's HOME.
const (
	detachedWorkerPidFile = "detached-worker-pid"
	handoffStartedFile    = "handoff-started"
	handoffFinishedFile   = "handoff-finished"
)

// interruptMarkerWait bounds how long a step waits for something a dispatch
// does before it is signalled. Provisioning comes first, so this is generous.
const interruptMarkerWait = 60 * time.Second

// aNeverAnsweringHTTPSRemoteIsListening listens on a local port that accepts
// connections and never writes to them, and registers its https URL as the
// repo name's {repo:<name>} substitution. A git clone of it waits on its TLS
// handshake, inside git's https remote helper, until something stops it.
func aNeverAnsweringHTTPSRemoteIsListening(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ctx, fmt.Errorf("listening for the never-answering remote: %w", err)
	}
	reached := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			once.Do(func() { close(reached) })
		}
	}()
	s.cleanups = append(s.cleanups, func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	s.interrupt.remoteURL = "https://" + ln.Addr().String() + "/" + name + ".git"
	s.interrupt.reached = reached
	s.repoURLs[name] = s.interrupt.remoteURL
	return ctx, nil
}

// iStartCommandFromTheWorkspaceRootInTheBackground starts a niwa command from
// the workspace root and returns without waiting for it. The After hook kills
// it if the scenario ends with it still running.
func iStartCommandFromTheWorkspaceRootInTheBackground(ctx context.Context, command string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	args := strings.Fields(command)
	if len(args) > 0 && args[0] == "niwa" {
		args[0] = s.binPath
	}
	bg := &backgroundNiwa{done: make(chan struct{})}
	bg.cmd = exec.Command(args[0], args[1:]...)
	bg.cmd.Dir = s.workspaceRoot
	bg.cmd.Env = s.buildEnv()
	bg.cmd.Stdout = &bg.stdout
	bg.cmd.Stderr = &bg.stderr
	if err := bg.cmd.Start(); err != nil {
		return ctx, fmt.Errorf("starting %q: %w", command, err)
	}
	go func() {
		bg.err = bg.cmd.Wait()
		close(bg.done)
	}()
	s.background = bg
	s.cleanups = append(s.cleanups, func() {
		select {
		case <-bg.done:
		default:
			_ = bg.cmd.Process.Kill()
			<-bg.done
		}
	})
	return ctx, nil
}

// theNeverAnsweringRemoteHasBeenReached waits until something connected to the
// never-answering remote: git's https remote helper is then running, which is
// the process a TERM to niwa used to leave behind.
func theNeverAnsweringRemoteHasBeenReached(ctx context.Context) error {
	s := getState(ctx)
	if s == nil || s.background == nil || s.interrupt.reached == nil {
		return fmt.Errorf("no background niwa or never-answering remote in this scenario")
	}
	select {
	case <-s.interrupt.reached:
		return nil
	case <-s.background.done:
		return fmt.Errorf("niwa exited before it reached the remote: %v\nstderr:\n%s", s.background.err, s.background.stderr.String())
	case <-time.After(interruptMarkerWait):
		return fmt.Errorf("nothing connected to the never-answering remote within %s", interruptMarkerWait)
	}
}

// niwaIsSentSIGTERM sends SIGTERM to the background niwa's own pid, not its
// process group, the way a caller's deadline does.
func niwaIsSentSIGTERM(ctx context.Context) error {
	s := getState(ctx)
	if s == nil || s.background == nil {
		return fmt.Errorf("no background niwa in this scenario")
	}
	return s.background.cmd.Process.Signal(syscall.SIGTERM)
}

// niwaExitsWithin waits for the background niwa to exit and records its
// streams, so the shared output steps can read them.
func niwaExitsWithin(ctx context.Context, seconds int) error {
	s := getState(ctx)
	if s == nil || s.background == nil {
		return fmt.Errorf("no background niwa in this scenario")
	}
	select {
	case <-s.background.done:
	case <-time.After(time.Duration(seconds) * time.Second):
		return fmt.Errorf("niwa was still running %ds after the signal", seconds)
	}
	s.stdout = s.background.stdout.String()
	s.stderr = s.background.stderr.String()
	s.exitCode = s.background.cmd.ProcessState.ExitCode()
	return nil
}

// niwaWasEndedBySIGTERM asserts niwa died of SIGTERM rather than exiting with
// a status of its own, which is what a caller saw before dispatch handled the
// signal.
func niwaWasEndedBySIGTERM(ctx context.Context) error {
	s := getState(ctx)
	if s == nil || s.background == nil || s.background.cmd.ProcessState == nil {
		return fmt.Errorf("no finished background niwa in this scenario")
	}
	ws, ok := s.background.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
		return fmt.Errorf("niwa ended with %v, want death by SIGTERM\nstderr:\n%s", s.background.cmd.ProcessState, s.stderr)
	}
	return nil
}

// noProcessForTheNeverAnsweringRemoteRemainsWithin polls the process table
// for anything carrying the remote's URL in its arguments, which git, git's
// remote-https and git-remote-https all do.
func noProcessForTheNeverAnsweringRemoteRemainsWithin(ctx context.Context, seconds int) error {
	s := getState(ctx)
	if s == nil || s.interrupt.remoteURL == "" {
		return fmt.Errorf("no never-answering remote in this scenario")
	}
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for {
		procs, err := processesMentioning(s.interrupt.remoteURL)
		if err != nil {
			return err
		}
		if len(procs) == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("processes for %s outlived niwa by %ds: %q", s.interrupt.remoteURL, seconds, procs)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// processesMentioning lists the processes whose command line contains text,
// as "pid args" lines.
func processesMentioning(text string) ([]string, error) {
	out, err := exec.Command("ps", "-ww", "-A", "-o", "pid=,args=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var found []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, text) {
			found = append(found, strings.TrimSpace(line))
		}
	}
	return found, nil
}

// dispatchFakeCodexLingeringScript is a codex whose `exec` worker records its
// pid and then keeps running without writing a session record, so niwa is
// still waiting to capture it when the scenario signals niwa.
const dispatchFakeCodexLingeringScript = `#!/bin/sh
case "$1" in
  exec)
    echo $$ > "$HOME/` + detachedWorkerPidFile + `"
    exec sleep 120
    ;;
  *)
    echo "fake codex: unsupported invocation: $*" >&2
    exit 1
    ;;
esac
`

// aFakeCodexForDispatchWhoseWorkerKeepsRunning installs the lingering codex.
// The worker is killed in the After hook.
func aFakeCodexForDispatchWhoseWorkerKeepsRunning(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	binDir := filepath.Join(s.homeDir, "fake-codex-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return ctx, fmt.Errorf("mkdir fake-codex-bin: %w", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "codex"), []byte(dispatchFakeCodexLingeringScript), 0o755); err != nil {
		return ctx, fmt.Errorf("writing fake codex script: %w", err)
	}
	s.pathPrefix = binDir
	s.envOverrides["CODEX_HOME"] = filepath.Join(s.homeDir, ".codex")
	s.cleanups = append(s.cleanups, func() {
		if pid, err := detachedWorkerPid(s); err == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	return ctx, nil
}

func detachedWorkerPid(s *testState) (int, error) {
	data, err := os.ReadFile(filepath.Join(s.homeDir, detachedWorkerPidFile))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// theDetachedWorkerHasStarted waits for the lingering codex to record its pid.
func theDetachedWorkerHasStarted(ctx context.Context) error {
	s := getState(ctx)
	if s == nil || s.background == nil {
		return fmt.Errorf("no background niwa in this scenario")
	}
	return waitForMarker(s, detachedWorkerPidFile, func() error {
		_, err := detachedWorkerPid(s)
		return err
	})
}

// theDetachedWorkerIsStillRunning asserts the worker niwa launched survived
// the signal niwa was sent.
func theDetachedWorkerIsStillRunning(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	pid, err := detachedWorkerPid(s)
	if err != nil {
		return fmt.Errorf("reading the detached worker's pid: %w", err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		return fmt.Errorf("the detached worker (pid %d) is gone after niwa was signalled: %v", pid, err)
	}
	return nil
}

// dispatchFakeClaudeSlowHandoffScript is a claude whose --bg hand-off marks
// that it started, takes the given number of seconds, then marks that it
// finished, standing for a hand-off that is still backgrounding its worker
// when niwa is signalled.
func dispatchFakeClaudeSlowHandoffScript(seconds int) string {
	return fmt.Sprintf(`#!/bin/sh
case "$1" in
  --bg)
    : > "$HOME/%s"
    sleep %d
    : > "$HOME/%s"
    exit 0
    ;;
  *)
    echo "fake claude: unsupported invocation: $*" >&2
    exit 1
    ;;
esac
`, handoffStartedFile, seconds, handoffFinishedFile)
}

// aFakeClaudeForDispatchWhoseHandoffTakes installs the slow hand-off claude.
func aFakeClaudeForDispatchWhoseHandoffTakes(ctx context.Context, seconds int) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	binDir := filepath.Join(s.homeDir, "fake-claude-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return ctx, fmt.Errorf("mkdir fake-claude-bin: %w", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude"), []byte(dispatchFakeClaudeSlowHandoffScript(seconds)), 0o755); err != nil {
		return ctx, fmt.Errorf("writing fake claude script: %w", err)
	}
	s.pathPrefix = binDir
	return ctx, nil
}

// theHandoffHasStarted waits for the slow hand-off to mark its start.
func theHandoffHasStarted(ctx context.Context) error {
	s := getState(ctx)
	if s == nil || s.background == nil {
		return fmt.Errorf("no background niwa in this scenario")
	}
	return waitForMarker(s, handoffStartedFile, nil)
}

// theHandoffFinishesWithin waits for the slow hand-off to mark its end, which
// it can only do if nothing killed it.
func theHandoffFinishesWithin(ctx context.Context, seconds int) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.homeDir, handoffFinishedFile)
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the hand-off did not finish within %ds of niwa being signalled", seconds)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// waitForMarker waits for a fake's marker file under HOME to exist and, when
// check is given, to pass it, failing early if the background niwa exits
// first.
func waitForMarker(s *testState, name string, check func() error) error {
	path := filepath.Join(s.homeDir, name)
	deadline := time.Now().Add(interruptMarkerWait)
	for {
		if _, err := os.Stat(path); err == nil && (check == nil || check() == nil) {
			return nil
		}
		select {
		case <-s.background.done:
			return fmt.Errorf("niwa exited before %s appeared: %v\nstderr:\n%s", name, s.background.err, s.background.stderr.String())
		default:
		}
		if time.Now().After(deadline) {
			return errors.New(name + " did not appear within " + interruptMarkerWait.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func registerDispatchInterruptSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a never-answering https remote "([^"]*)" is listening$`, aNeverAnsweringHTTPSRemoteIsListening)
	ctx.Step(`^I start "([^"]*)" from the workspace root in the background$`, iStartCommandFromTheWorkspaceRootInTheBackground)
	ctx.Step(`^the never-answering remote has been reached$`, theNeverAnsweringRemoteHasBeenReached)
	ctx.Step(`^niwa is sent SIGTERM$`, niwaIsSentSIGTERM)
	ctx.Step(`^niwa exits within (\d+) seconds$`, niwaExitsWithin)
	ctx.Step(`^niwa was ended by SIGTERM$`, niwaWasEndedBySIGTERM)
	ctx.Step(`^no process for the never-answering remote remains within (\d+) seconds$`, noProcessForTheNeverAnsweringRemoteRemainsWithin)
	ctx.Step(`^a fake codex for dispatch whose worker keeps running$`, aFakeCodexForDispatchWhoseWorkerKeepsRunning)
	ctx.Step(`^the detached worker has started$`, theDetachedWorkerHasStarted)
	ctx.Step(`^the detached worker is still running$`, theDetachedWorkerIsStillRunning)
	ctx.Step(`^a fake claude for dispatch whose hand-off takes (\d+) seconds$`, aFakeClaudeForDispatchWhoseHandoffTakes)
	ctx.Step(`^the hand-off has started$`, theHandoffHasStarted)
	ctx.Step(`^the hand-off finishes within (\d+) seconds$`, theHandoffFinishesWithin)
}
