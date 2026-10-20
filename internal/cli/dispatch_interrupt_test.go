package cli

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

// signalSelf sends sig to this test process. It is only safe while a handler
// for sig is registered: with the default action it ends the test binary.
func signalSelf(t *testing.T, sig syscall.Signal) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), sig); err != nil {
		t.Fatalf("signalling self: %v", err)
	}
}

// waitDone waits for ctx to end, failing the test if it doesn't within a bound.
func waitDone(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the context was not cancelled within 5s of the signal")
	}
}

func TestInterruptOnSignal_CancelsWithTheSignal(t *testing.T) {
	for _, tc := range []struct {
		sig  syscall.Signal
		want string
	}{
		{syscall.SIGTERM, "interrupted by SIGTERM"},
		{syscall.SIGINT, "interrupted by SIGINT"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			ctx, release := interruptOnSignal(context.Background())
			defer release()

			signalSelf(t, tc.sig)
			waitDone(t, ctx)

			if !dispatchInterrupted(ctx) {
				t.Fatalf("cause = %v, want a *dispatchInterruptedError", context.Cause(ctx))
			}
			if got := context.Cause(ctx).Error(); got != tc.want {
				t.Errorf("cause = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInterruptOnSignal_ReleaseWithoutASignalLeavesTheContextLive(t *testing.T) {
	ctx, release := interruptOnSignal(context.Background())
	release()
	release()
	if err := ctx.Err(); err != nil {
		t.Fatalf("ctx.Err() = %v after a release with no signal, want nil", err)
	}
	if dispatchInterrupted(ctx) {
		t.Fatal("a released context with no signal reads as interrupted")
	}
}

// A signal while the instance is being provisioned stops the dispatch there:
// provisioning sees its context cancelled, nothing is launched, and since the
// provisioner returned no instance there is nothing for the rollback to remove
// (a real Create removes its own directory on failure).
func TestDispatch_SignalDuringProvisioning_StopsBeforeAnInstanceExists(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	provisionInstanceFunc = func(ctx context.Context, _, _, _, _ string, _ int) (provisionResult, error) {
		f.provisionCalled++
		signalSelf(t, syscall.SIGTERM)
		waitDone(t, ctx)
		return provisionResult{}, ctx.Err()
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	var die *dispatchInterruptedError
	if !errors.As(err, &die) {
		t.Fatalf("err = %v, want a *dispatchInterruptedError", err)
	}
	if die.sig != syscall.SIGTERM {
		t.Errorf("signal = %v, want SIGTERM", die.sig)
	}
	if f.launchCalled != 0 {
		t.Errorf("launch called %d times after an interrupted provisioning, want 0", f.launchCalled)
	}
	if f.destroyCalled != 0 {
		t.Errorf("destroy called %d times with no instance created, want 0", f.destroyCalled)
	}
}

// A signal after provisioning finished but before the launch is read at the
// launch, and the rollback removes the instance before any worker exists in
// it.
func TestDispatch_SignalBeforeLaunch_RollsBackAndLaunchesNothing(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	provision := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, root, cwd, namePrefix, sep string, n int) (provisionResult, error) {
		res, err := provision(ctx, root, cwd, namePrefix, sep, n)
		signalSelf(t, syscall.SIGTERM)
		waitDone(t, ctx)
		return res, err
	}

	_, _, err := runDispatchCmd(t, "do a thing")
	if !errors.As(err, new(*dispatchInterruptedError)) {
		t.Fatalf("err = %v, want a *dispatchInterruptedError", err)
	}
	if f.launchCalled != 0 {
		t.Errorf("launch called %d times after a signal, want 0", f.launchCalled)
	}
	if f.destroyCalled != 1 || f.destroyedPath != f.instancePath {
		t.Errorf("destroy called %d times on %q, want once on %q", f.destroyCalled, f.destroyedPath, f.instancePath)
	}
}

// The launch is given the command's own context, which the handler can't
// cancel. That is what keeps a backgrounded hand-off, started with
// exec.CommandContext, from being killed by a signal sent to niwa.
func TestDispatch_LaunchContextIsOutsideTheHandler(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	var launchCtx context.Context
	dispatchLaunch = func(ctx context.Context, _ launchRequest) error {
		f.launchCalled++
		launchCtx = ctx
		return nil
	}
	dispatchDetach = true

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("runDispatch: %v", err)
	}
	if f.launchCalled != 1 {
		t.Fatalf("launch called %d times, want 1", f.launchCalled)
	}
	// runDispatchCmd's command context is context.Background(), whose Done
	// channel is nil. Any context derived from the handler's would have one.
	if launchCtx.Done() != nil {
		t.Error("the launch was given a cancellable context; a signal handled during the dispatch could reach the hand-off")
	}
}
