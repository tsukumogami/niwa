package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// dispatchInterruptSignals are the signals that stop a dispatch while it is
// still preparing its instance: what a terminal's Ctrl-C sends, and what a
// caller's deadline sends.
var dispatchInterruptSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}

// dispatchInterruptGrace is how long a signalled dispatch has to clean up
// before niwa ends by the signal regardless. The git commands it stops take at
// most gitWaitDelay (internal/workspace) once killed, and the rollback is local
// work, so this is room for those with a margin, not for a stage that ignores
// the signal. A variable so tests can shorten it.
var dispatchInterruptGrace = 10 * time.Second

// dispatchInterruptExit is what the grace period ends with. A variable so a
// test can watch it fire without the test binary being killed.
var dispatchInterruptExit = (*dispatchInterruptedError).exit

// dispatchInterruptedError is the cause of a dispatch context cancelled by a
// signal, and the error such a dispatch returns. Execute recognises it and ends
// niwa by the same signal once the dispatch has cleaned up after itself.
type dispatchInterruptedError struct {
	sig syscall.Signal
}

func (e *dispatchInterruptedError) Error() string {
	name := "SIGTERM"
	if e.sig == syscall.SIGINT {
		name = "SIGINT"
	}
	return "interrupted by " + name
}

// exit ends niwa with the signal's default action, so a caller reads the same
// status it read before niwa handled the signal: a shell sees 128+n, and a
// shell loop that stops when a child dies of SIGINT still stops.
func (e *dispatchInterruptedError) exit() {
	signal.Reset(e.sig)
	_ = syscall.Kill(os.Getpid(), e.sig)
	// The kill can return before the process ends: Go's runtime takes the
	// signal on a thread of its own and only then applies the default
	// action. Exiting at once would beat it (measured on Linux), so wait for
	// it, and fall back to the status a shell would have reported.
	time.Sleep(time.Second)
	os.Exit(128 + int(e.sig))
}

// interruptOnSignal returns a context derived from parent that the first
// SIGINT or SIGTERM cancels, with a *dispatchInterruptedError as its cause, and
// a release function that stops listening.
//
// Without it a signal ends niwa through Go's default action: no deferred
// cleanup runs and the context its git commands were started under is never
// cancelled, so a clone in flight goes on writing into an instance nothing will
// finish or remove.
//
// The first signal also puts the default action back, so a second one ends
// niwa at once, as one did before this handler existed. And it starts a grace
// period: not every stage of a dispatch watches the context (a setup script
// runs to its end), and a caller that sends one TERM and waits must not wait on
// such a stage, so if the dispatch hasn't released the handler within
// dispatchInterruptGrace of the signal, niwa ends by the signal there and then,
// cleaned up or not, as it did before.
//
// release stops listening and stops the grace period. A signal that arrives as
// it runs still cancels the context, so after release ctx.Err() says whether
// one came. Calling it more than once is safe.
func interruptOnSignal(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, dispatchInterruptSignals...)

	var mu sync.Mutex
	var grace *time.Timer
	interrupt := func(sig os.Signal) {
		signal.Stop(ch)
		die := &dispatchInterruptedError{sig: sig.(syscall.Signal)}
		cancel(die)
		mu.Lock()
		defer mu.Unlock()
		grace = time.AfterFunc(dispatchInterruptGrace, func() { dispatchInterruptExit(die) })
	}
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		select {
		case sig := <-ch:
			interrupt(sig)
		case <-done:
		}
	}()

	var once sync.Once
	release := func() {
		once.Do(func() {
			signal.Stop(ch)
			close(done)
			<-exited
			// The goroutine may have taken done over a signal already in the
			// channel; nothing else reads it now.
			select {
			case sig := <-ch:
				interrupt(sig)
			default:
			}
			mu.Lock()
			defer mu.Unlock()
			if grace != nil {
				grace.Stop()
			}
		})
	}
	return ctx, release
}

// dispatchInterrupted reports whether a signal cancelled ctx, as opposed to
// its parent ending it.
func dispatchInterrupted(ctx context.Context) bool {
	var die *dispatchInterruptedError
	return errors.As(context.Cause(ctx), &die)
}

// interruptedDispatchError wraps the cause of a dispatch that a signal stopped,
// saying at which point it stopped and what was left behind.
func interruptedDispatchError(ctx context.Context, when string) error {
	return fmt.Errorf("niwa: error: dispatch %w %s", context.Cause(ctx), when)
}
