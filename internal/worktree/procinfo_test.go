package worktree

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

// TestPIDStartedAtIsThisProcessesStart reads this test process's own start:
// it began before this call and, where there is a reader, within the last
// hour.
func TestPIDStartedAtIsThisProcessesStart(t *testing.T) {
	started, err := PIDStartedAt(os.Getpid())
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		if err == nil {
			t.Fatalf("PIDStartedAt on %s returned %v, want an error", runtime.GOOS, started)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if started.After(now) {
		t.Errorf("started %v is after now %v", started, now)
	}
	if now.Sub(started) > time.Hour {
		t.Errorf("started %v is more than an hour before now %v", started, now)
	}
}

// TestPIDStartedAtOrdersProcesses checks that a process started after this
// one reads as starting later, give or take Linux's one-second boot-time
// rounding.
func TestPIDStartedAtOrdersProcesses(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no process start reader on " + runtime.GOOS)
	}
	before := time.Now()
	cmd := exec.Command("sleep", "5")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	started, err := PIDStartedAt(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if started.Before(before.Add(-time.Second)) {
		t.Errorf("a process started at about %v reads as starting at %v", before, started)
	}
}

func TestPIDStartedAtRefusesAnExitedProcess(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if started, err := PIDStartedAt(cmd.Process.Pid); err == nil {
		t.Errorf("PIDStartedAt for an exited process returned %v, want an error", started)
	}
}
