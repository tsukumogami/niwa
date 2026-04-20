package workspace

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// spinFrames is the braille spinner sequence used by the background tick goroutine.
var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Reporter writes progress and log output to an underlying writer. It
// supports two modes:
//
//   - TTY mode (isTTY == true): Status starts a background goroutine that
//     redraws the current status line with an advancing spinner at ~100 ms
//     intervals. Log/Warn stop the goroutine and clear the line before
//     appending a permanent line.
//   - Non-TTY mode (isTTY == false): Status is a no-op; Log/Warn append
//     lines directly with no ANSI sequences.
type Reporter struct {
	w          io.Writer
	isTTY      bool
	needsClear bool     // true while spinner goroutine is active
	deferred   []string // messages held for FlushDeferred

	mu        sync.Mutex
	spinMsg   string
	spinFrame int
	spinStop  chan struct{} // closed to signal goroutine to exit
	spinDone  chan struct{} // closed by goroutine on exit
}

// NewReporter constructs a Reporter whose TTY mode is detected from w.
// If w is an *os.File, term.IsTerminal is called on its file descriptor.
// Otherwise isTTY defaults to false.
func NewReporter(w io.Writer) *Reporter {
	isTTY := false
	if f, ok := w.(*os.File); ok {
		isTTY = term.IsTerminal(int(f.Fd()))
	}
	return &Reporter{w: w, isTTY: isTTY}
}

// NewReporterWithTTY constructs a Reporter with the given TTY flag,
// bypassing auto-detection. Used when the caller already knows the
// terminal state (e.g., after checking --no-progress or piping).
func NewReporterWithTTY(w io.Writer, isTTY bool) *Reporter {
	return &Reporter{w: w, isTTY: isTTY}
}

// Status updates the current transient status message and starts the
// spinner goroutine if not already running. The goroutine redraws the
// status line with an advancing spinner frame every ~100 ms so the
// display stays live even when no new Status calls arrive (e.g., during
// a long git clone where all output is discarded). On non-TTY output
// this method is a no-op.
func (r *Reporter) Status(msg string) {
	if !r.isTTY {
		return
	}
	r.mu.Lock()
	r.spinMsg = msg
	r.needsClear = true
	if r.spinStop == nil {
		stop := make(chan struct{})
		done := make(chan struct{})
		r.spinStop = stop
		r.spinDone = done
		go r.spinLoop(stop, done)
	}
	r.mu.Unlock()
}

// spinLoop is the background goroutine started by Status. stop and done are
// passed as parameters rather than read from r.spinStop/r.spinDone to avoid
// selecting on a nil channel after stopSpinner clears those fields. It ticks
// immediately on startup (so the status appears without waiting 100 ms), then
// ticks every ~100 ms until stop is closed.
func (r *Reporter) spinLoop(stop, done chan struct{}) {
	defer close(done)
	r.doTick() // immediate first render; no 100 ms delay on initial display
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			r.doTick()
		}
	}
}

// doTick writes one spinner frame for the current status message. It is
// called by the spinner goroutine and may be called directly from tests
// when no goroutine is running to verify the frame format without timing
// dependencies.
func (r *Reporter) doTick() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.spinMsg == "" {
		return
	}
	frame := spinFrames[r.spinFrame%len(spinFrames)]
	r.spinFrame++
	fmt.Fprintf(r.w, "\r\033[K%s %s", frame, r.spinMsg)
}

// stopSpinner stops the spinner goroutine and clears the spinner line.
// It is a no-op when no goroutine is running.
func (r *Reporter) stopSpinner() {
	r.mu.Lock()
	if r.spinStop == nil {
		r.mu.Unlock()
		return
	}
	stop := r.spinStop
	done := r.spinDone
	r.spinStop = nil
	r.spinDone = nil
	r.spinMsg = ""
	r.needsClear = false
	r.mu.Unlock()
	close(stop)
	<-done
	fmt.Fprint(r.w, "\r\033[K") // clear the last spinner line
}

// Log writes a permanent log line. On TTY output the spinner goroutine is
// stopped and its line cleared first. The format string follows
// fmt.Sprintf conventions; a newline is always appended.
func (r *Reporter) Log(format string, a ...any) {
	if r.isTTY {
		r.stopSpinner()
	}
	fmt.Fprintf(r.w, format+"\n", a...)
}

// Warn writes a permanent warning line. It behaves like Log but
// prepends "warning: " to the format string.
func (r *Reporter) Warn(format string, a ...any) {
	r.Log("warning: "+format, a...)
}

// Defer queues an informational message to be printed by FlushDeferred.
// Use for notices and non-critical information that should appear after
// the operation summary rather than inline during execution.
func (r *Reporter) Defer(format string, a ...any) {
	r.deferred = append(r.deferred, fmt.Sprintf(format, a...))
}

// DeferWarn queues a warning message for FlushDeferred (prepends "warning: ").
func (r *Reporter) DeferWarn(format string, a ...any) {
	r.deferred = append(r.deferred, fmt.Sprintf("warning: "+format, a...))
}

// FlushDeferred prints all deferred messages in order and clears the buffer.
// Call after the operation summary line so messages appear as a clean block
// below the summary.
func (r *Reporter) FlushDeferred() {
	for _, msg := range r.deferred {
		r.Log("%s", msg)
	}
	r.deferred = nil
}

// Writer returns an io.Writer whose Write calls are routed through Log.
// Each Write invocation is treated as a single log message (trailing
// newlines in the input are stripped to avoid double-newlines).
func (r *Reporter) Writer() io.Writer {
	return &logWriter{r: r}
}

// logWriter adapts io.Writer calls to Reporter.Log.
type logWriter struct {
	r *Reporter
}

func (lw *logWriter) Write(p []byte) (int, error) {
	// Strip a trailing newline to avoid double-newlines since Log appends one.
	s := string(p)
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	if s != "" {
		lw.r.Log("%s", s)
	}
	return len(p), nil
}
