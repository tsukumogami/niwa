package workspace

import (
	"context"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// neverAnsweringRemote listens on a local port, accepts every connection and
// never writes to it, so a TLS handshake against it waits until something
// gives up. It returns an https URL for it and a channel closed once the
// first connection is accepted.
func neverAnsweringRemote(t *testing.T) (string, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{})
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
			once.Do(func() { close(accepted) })
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return "https://" + ln.Addr().String() + "/stuck.git", accepted
}

// processesMentioning lists the processes whose command line contains s, as
// "pid args" lines. git, git's remote-https and git-remote-https all carry the
// remote URL in their arguments.
func processesMentioning(t *testing.T, s string) []string {
	t.Helper()
	out, err := exec.Command("ps", "-ww", "-A", "-o", "pid=,args=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	var found []string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, s) {
			found = append(found, strings.TrimSpace(line))
		}
	}
	return found
}

// waitForProcesses polls until cond holds for the processes mentioning s, or
// the bound passes, and returns the last listing.
func waitForProcesses(t *testing.T, s string, bound time.Duration, cond func([]string) bool) ([]string, bool) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		procs := processesMentioning(t, s)
		if cond(procs) {
			return procs, true
		}
		if time.Now().After(deadline) {
			return procs, false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A clone stuck on an https remote that never answers, under a context marked
// by WithDisposableGit, is stopped completely when the context is cancelled:
// the clone returns promptly and neither git nor its https remote helper is
// left running. Without the mark, exec.CommandContext kills git alone; the
// helper keeps running and holds git's stderr open, so the clone doesn't even
// return until the helper's own connect timeout, minutes later.
func TestGitCommand_DisposableCloneStopsWithItsRemoteHelper(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	url, accepted := neverAnsweringRemote(t)
	target := filepath.Join(t.TempDir(), "stuck")

	ctx, cancel := context.WithCancel(WithDisposableGit(context.Background()))
	defer cancel()
	done := make(chan error, 1)
	go func() {
		var c Cloner
		_, err := c.CloneWith(ctx, url, target, CloneOptions{}, NewReporterWithTTY(&strings.Builder{}, false))
		done <- err
	}()

	select {
	case <-accepted:
	case err := <-done:
		t.Fatalf("the clone ended before reaching the remote: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the clone never connected to the remote")
	}
	// The connection is the helper's, so it is running; make sure ps sees it
	// before cancelling, or an empty listing afterwards would prove nothing.
	if procs, ok := waitForProcesses(t, url, 5*time.Second, func(p []string) bool {
		for _, line := range p {
			if strings.Contains(line, "remote-https") {
				return true
			}
		}
		return false
	}); !ok {
		t.Fatalf("no https remote helper found for %s; processes: %q", url, procs)
	}

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled clone returned no error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the clone did not return within 10s of its context being cancelled")
	}
	if procs, ok := waitForProcesses(t, url, 5*time.Second, func(p []string) bool { return len(p) == 0 }); !ok {
		t.Fatalf("git processes for %s outlived the cancelled clone: %q", url, procs)
	}
}

// Without the mark a git command is exactly what exec.CommandContext returns,
// so create, apply and every other caller keep git on the terminal as before.
func TestGitCommand_UnmarkedContextIsPlainCommandContext(t *testing.T) {
	cmd := gitCommand(context.Background(), "status")
	if cmd.SysProcAttr != nil || cmd.Env != nil || cmd.WaitDelay != 0 {
		t.Errorf("unmarked git command: SysProcAttr=%v Env set=%v WaitDelay=%v, want all unset",
			cmd.SysProcAttr, cmd.Env != nil, cmd.WaitDelay)
	}
	marked := gitCommand(WithDisposableGit(context.Background()), "status")
	if marked.SysProcAttr == nil || !marked.SysProcAttr.Setsid {
		t.Error("marked git command does not lead a session of its own")
	}
	if !strings.Contains(strings.Join(marked.Env, "\n"), "GIT_TERMINAL_PROMPT=0") {
		t.Error("marked git command can still prompt")
	}
}
