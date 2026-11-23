package mirror

import (
	"context"
	"os/exec"
	"testing"
)

// Only a context WithDisposableGit marked makes a git process lead its own
// session with a cancel that reaches the whole group.
func TestMakeDisposable_OnlyUnderTheMark(t *testing.T) {
	plain := exec.CommandContext(context.Background(), "git", "version")
	makeDisposable(context.Background(), plain)
	if plain.SysProcAttr != nil || plain.WaitDelay != 0 {
		t.Fatalf("an unmarked context changed the command: %+v, wait delay %s", plain.SysProcAttr, plain.WaitDelay)
	}

	ctx := WithDisposableGit(context.Background())
	marked := exec.CommandContext(ctx, "git", "version")
	makeDisposable(ctx, marked)
	if marked.SysProcAttr == nil || !marked.SysProcAttr.Setsid {
		t.Fatal("a marked context left git in the caller's session")
	}
	if marked.Cancel == nil || marked.WaitDelay != gitWaitDelay {
		t.Fatalf("a marked context set no group cancel or wait delay (%s)", marked.WaitDelay)
	}
}
