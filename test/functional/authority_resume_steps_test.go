package functional

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// The resume steps start a dispatched worker again the way each resume path
// does: claude attach, claude --bg --resume and codex resume all start a
// fresh agent process in the instance, under a new session id, and niwa takes
// no part. The reading fakes' prologue makes that process's first read, so a
// scenario can compare it with the launch's and check the stored record was
// left byte for byte as it was.

func registerAuthorityResumeSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the worker is resumed with "([^"]*)" under session "([^"]*)"$`, theWorkerIsResumedWith)
	ctx.Step(`^the worker's "([^"]*)" read matches its first read and ran under session "([^"]*)"$`, theWorkersReadMatchesItsFirstRead)
}

// theWorkerIsResumedWith runs the fake agent's binary with the resume
// arguments from the instance, with a new session id in the session id
// variable and the rest of the environment a scenario's niwa runs get.
func theWorkerIsResumedWith(ctx context.Context, argv, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return ctx, fmt.Errorf("no dispatch instance to resume in")
	}
	args := strings.Fields(argv)
	bin := filepath.Join(s.pathPrefix, args[0])
	if _, err := os.Stat(bin); err != nil {
		return ctx, fmt.Errorf("no fake %s on the scenario's PATH: %w", args[0], err)
	}
	cmd := exec.Command(bin, args[1:]...)
	cmd.Dir = s.lastDispatchInstancePath
	cmd.Env = append(s.buildEnv(), "CLAUDE_CODE_SESSION_ID="+sessionID)
	if out, err := cmd.CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("resuming with %s: %v\n%s", argv, err, out)
	}
	return ctx, nil
}

func theWorkersReadMatchesItsFirstRead(ctx context.Context, label, sessionID string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	first, err := workerRead(s, "launch")
	if err != nil {
		return err
	}
	got, err := workerRead(s, label)
	if err != nil {
		return err
	}
	if got != first {
		return fmt.Errorf("after the resume show printed\n%s\nbefore it\n%s", got, first)
	}
	ran, err := os.ReadFile(filepath.Join(s.homeDir, "authority-read-"+label+".session"))
	if err != nil {
		return err
	}
	if string(ran) != sessionID {
		return fmt.Errorf("the %q read ran under session %q, want %q", label, ran, sessionID)
	}
	return nil
}
