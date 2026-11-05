package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// deliverer hands a worker the core classified its next task. Accepts can only
// narrow what the core proceeds on: the core asks it only about a state it
// already classified as one it can act on, so no deliverer can make the core
// wake a worker the core refused.
type deliverer interface {
	Accepts(state workerState, callerKind string) bool
	Deliver(ctx context.Context, s workerSnapshot, prompt string) error
}

// retaskWakeFunc runs the wake: bin with args, from dir, with env. A seam so a
// test can see the wake without a binary; the retask tests use the real one
// against the copy-rule stub on PATH.
var retaskWakeFunc = realRetaskWake

// defaultRetaskDeliverer returns the deliverer an agent declares, or nil.
func defaultRetaskDeliverer(spec agentplan.LaunchSpec) deliverer {
	if spec.RetaskDelivery == agentplan.RetaskFlaglessWake {
		return resumeWakeDeliverer{spec: spec}
	}
	return nil
}

// resumeWakeDeliverer is the flagless wake: the agent's leading arguments, its
// resume-launch arguments, the session id, a bare "--" and the prompt, run from
// the instance, and nothing else. Claude Code keeps a stopped background
// session's launch options with its job and re-applies them on this wake; any
// flag of niwa's own would make it start a copy instead. So the argv is built
// through reentryResumeArgs with only the session id, the instance and the
// prompt set, never through a launch path that adds flags.
type resumeWakeDeliverer struct {
	spec agentplan.LaunchSpec
}

// Accepts takes a live idle worker (which the core stops and settles first) or
// a stopped worker whose job entry is listed, for either caller.
func (d resumeWakeDeliverer) Accepts(state workerState, callerKind string) bool {
	if callerKind != retaskCallerCLI && callerKind != retaskCallerWatch {
		return false
	}
	return state == liveIdle || state == stoppedWithEntry
}

// Deliver runs the wake.
func (d resumeWakeDeliverer) Deliver(ctx context.Context, s workerSnapshot, prompt string) error {
	argv := resumeWakeArgs(d.spec, s.View.SessionID, s.View.InstancePath, prompt)
	if argv == nil {
		return errors.New("the agent declares no way to wake a session")
	}
	bin, err := lookAgentBinary(d.spec.Binary)
	if err != nil {
		return fmt.Errorf("%s binary not found in PATH", d.spec.Binary)
	}
	// The process environment is the caller's, less the names a dispatch
	// keeps from a worker. The worker's own environment is the one Claude
	// Code saved with the job.
	env := resumeEnv(os.Environ(), s.View.Unset, nil)
	return retaskWakeFunc(ctx, bin, argv, s.View.InstancePath, env)
}

// resumeWakeArgs is the wake's argv after the binary.
func resumeWakeArgs(spec agentplan.LaunchSpec, sessionID, workdir, prompt string) []string {
	return reentryResumeArgs(spec, resumeLaunch{
		SessionID: sessionID,
		Workdir:   workdir,
		Prompt:    prompt,
	})
}

// realRetaskWake runs the agent's binary, which backgrounds the session and
// exits, and reads its exit status. Its output is not passed on: the outcome
// is read from the listing, and the output could echo the saved options.
func realRetaskWake(ctx context.Context, bin string, args []string, dir string, env []string) error {
	ctx, cancel := context.WithTimeout(ctx, claudeRemoveTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s exited: %w", filepath.Base(bin), err)
	}
	return nil
}
