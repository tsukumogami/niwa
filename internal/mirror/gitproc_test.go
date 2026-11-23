package mirror

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Only a context WithDisposableGit marked makes a git process lead its own
// session with a cancel that reaches the whole group.
func TestMakeDisposable_OnlyUnderTheMark(t *testing.T) {
	plain := exec.CommandContext(context.Background(), "git", "version")
	makeDisposable(context.Background(), plain)()
	if plain.SysProcAttr != nil || plain.WaitDelay != 0 {
		t.Fatalf("an unmarked context changed the command: %+v, wait delay %s", plain.SysProcAttr, plain.WaitDelay)
	}

	ctx := WithDisposableGit(context.Background())
	marked := exec.CommandContext(ctx, "git", "version")
	makeDisposable(ctx, marked)()
	if marked.SysProcAttr == nil || !marked.SysProcAttr.Setsid {
		t.Fatal("a marked context left git in the caller's session")
	}
	if marked.Cancel == nil || marked.WaitDelay != gitWaitDelay {
		t.Fatalf("a marked context set no group cancel or wait delay (%s)", marked.WaitDelay)
	}
}

// A cancelled command that exits on SIGTERM gets no SIGKILL once its Wait
// has returned: the delayed kill is stopped, so it can't reach a process
// group whose id was handed on.
func TestMakeDisposable_NoKillAfterWaitReturns(t *testing.T) {
	var mu sync.Mutex
	sent := map[syscall.Signal]int{}
	real := killGroup
	killGroup = func(pgid int, sig syscall.Signal) error {
		mu.Lock()
		sent[sig]++
		mu.Unlock()
		return real(pgid, sig)
	}
	t.Cleanup(func() { killGroup = real })

	ctx, cancel := context.WithCancel(WithDisposableGit(context.Background()))
	cmd := exec.CommandContext(ctx, "sleep", "30")
	exited := makeDisposable(ctx, cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = cmd.Wait()
	exited()

	time.Sleep(gitKillDelay + 300*time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if sent[syscall.SIGTERM] != 1 {
		t.Fatalf("the cancelled group got %d SIGTERMs, want 1", sent[syscall.SIGTERM])
	}
	if sent[syscall.SIGKILL] != 0 {
		t.Fatalf("the group got a SIGKILL after Wait returned (%d)", sent[syscall.SIGKILL])
	}
}
