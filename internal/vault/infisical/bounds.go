package infisical

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

// Deadlines for the calls that sit on the provisioning path. Nothing
// on that path had a bound before, so a CLI that waited on a prompt
// or a server that accepted the connection and never answered could
// hang a session-start hook indefinitely. Read these only through
// callBound, which applies the test override.
const (
	// exportTimeout bounds one `infisical export` subprocess.
	exportTimeout = 30 * time.Second
	// probeTimeout bounds one session probe (`infisical login status`
	// or the equivalent) run to classify a failed export.
	probeTimeout = 15 * time.Second
	// loginTimeout bounds niwa's own universal-auth login request.
	loginTimeout = 30 * time.Second
)

// Output caps for every subprocess defaultCommander runs. The deadline
// alone bounds how long a CLI can write, not how much it writes in that
// time, so without these a runaway CLI could fill memory before its
// deadline. Variables only so tests can shrink them.
var (
	// maxStdoutBytes caps the captured stdout. Past it, the rest is
	// discarded and Run reports errOutputTooLarge.
	maxStdoutBytes = 32 << 20
	// maxStderrBytes caps the captured stderr. Past it, the rest is
	// discarded silently, along with the line the cap cut (see
	// wholeLines): stderr only feeds messages and the classifier's
	// markers, which a truncated tail doesn't change in any way that
	// matters.
	maxStderrBytes = 1 << 20
)

// errOutputTooLarge is the error Run returns, alongside the process's
// real exit code and the truncated stdout, when stdout passed
// maxStdoutBytes.
var errOutputTooLarge = errors.New("infisical: CLI output exceeded the size cap")

// cappedBuffer keeps the first limit bytes written to it and discards
// the rest, recording that it did. Write never fails, so the child
// never sees a broken pipe and exits the way it would have otherwise.
type cappedBuffer struct {
	limit     int
	buf       []byte
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	room := b.limit - len(b.buf)
	if room >= len(p) {
		b.buf = append(b.buf, p...)
		return len(p), nil
	}
	if room > 0 {
		b.buf = append(b.buf, p[:room]...)
	}
	b.truncated = true
	return len(p), nil
}

// wholeLines returns the kept bytes, minus the last line when the cap cut
// it short. A secret the cap split in half wouldn't match the scrubber's
// fragments, so a text stream that gets interpolated into messages
// (stderr) must never end in a cut line.
func (b *cappedBuffer) wholeLines() []byte {
	if !b.truncated {
		return b.buf
	}
	i := bytes.LastIndexByte(b.buf, '\n')
	if i < 0 {
		return nil
	}
	return b.buf[:i+1]
}

// testTimeoutEnv names the test-only override for every deadline in
// the const block above.
// It exists so functional tests can exercise a timeout without
// waiting 30 seconds per scenario.
const testTimeoutEnv = "NIWA_TEST_VAULT_TIMEOUT"

// minTestTimeout is the floor below which the override is ignored. A
// bound that short would turn every call into a timeout.
const minTestTimeout = 50 * time.Millisecond

// errCallDeadline is the cause attached to the deadline each call
// site derives. Checking for it (rather than for
// context.DeadlineExceeded) keeps a caller's own deadline or
// cancellation from being reported as this call's timeout.
var errCallDeadline = errors.New("infisical: call deadline reached")

// overrideWarning guards the once-per-run warning printed while the
// override is in effect. warningOut is where it goes; tests swap both.
var (
	overrideWarning sync.Once
	warningOut      io.Writer = os.Stderr
)

// callBound returns the deadline to apply to a call whose default is
// def. NIWA_TEST_VAULT_TIMEOUT replaces def only when it parses as a
// Go duration of at least minTestTimeout and shorter than def, so a
// stray setting can shorten a bound but never lengthen one. While the
// override is in effect, one warning goes to stderr per run.
func callBound(def time.Duration) time.Duration {
	raw := os.Getenv(testTimeoutEnv)
	if raw == "" {
		return def
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d < minTestTimeout || d >= def {
		return def
	}
	overrideWarning.Do(func() {
		fmt.Fprintf(warningOut,
			"niwa: warning: %s=%s is shortening vault call deadlines; it is meant for tests only\n",
			testTimeoutEnv, d)
	})
	return d
}

// withCallDeadline derives the context a bounded call runs under. The
// returned context carries errCallDeadline as its cause once the
// bound elapses, which is what callTimedOut looks for.
func withCallDeadline(ctx context.Context, bound time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(ctx, bound, errCallDeadline)
}

// callTimedOut reports whether a bounded call ran out of time: either
// the deadline withCallDeadline set on callCtx fired, or the
// subprocess exited but something it left behind kept its output
// pipes open until WaitDelay cut them (exec.ErrWaitDelay). A
// cancellation or deadline inherited from the caller's own context
// is not a timeout of this call.
//
// Call it right after the call returns, before any other branch on
// its result: a timed-out subprocess comes back as a killed process
// with exit code -1, which would otherwise read as a generic failure.
func callTimedOut(callCtx context.Context, err error) bool {
	if errors.Is(context.Cause(callCtx), errCallDeadline) {
		return true
	}
	return errors.Is(err, exec.ErrWaitDelay) && callCtx.Err() == nil
}
