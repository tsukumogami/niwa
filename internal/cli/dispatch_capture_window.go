package cli

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// openCaptureWindow writes the instance's capture-pending file (this
// dispatch, the agent it is about to launch, this process and the launch
// time) and returns the function that closes the window: it removes the file.
// The close is safe to call more than once, so the caller defers it and also
// calls it as soon as the mapping is written.
//
// `niwa authority check` binds a session through the agent's own session
// records only while the file exists and the process it names is alive and
// started no later than the launch time, so a window this process stopped
// keeping can't bind a session started later.
//
// Once the worker is launched niwa no longer handles SIGINT or SIGTERM (the
// interrupt handler is released before launch), so a signal would end niwa
// by its default action and leave the file behind. Until the close, a signal
// is caught only to remove the file; niwa then ends by the same signal, as it
// would have, and leaves the instance as a kill always has.
func openCaptureWindow(instancePath, dispatchID string, ag agent.Agent) (func(), error) {
	pending := workspace.CapturePending{
		DispatchID: dispatchID,
		Agent:      string(ag),
		PID:        os.Getpid(),
		LaunchedAt: time.Now(),
	}
	if err := workspace.WriteCapturePending(instancePath, pending); err != nil {
		return nil, err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, dispatchInterruptSignals...)
	done := make(chan struct{})
	go func() {
		select {
		case s := <-signals:
			_ = workspace.RemoveCapturePending(instancePath)
			sig, _ := s.(syscall.Signal)
			dispatchInterruptExit(&dispatchInterruptedError{sig: sig})
		case <-done:
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(signals)
			close(done)
			_ = workspace.RemoveCapturePending(instancePath)
		})
	}, nil
}
