package infisical

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// captureOverrideWarning resets the once-per-run warning guard and
// sends the warning to a buffer for the rest of the test.
func captureOverrideWarning(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut := warningOut
	overrideWarning = sync.Once{}
	warningOut = &buf
	t.Cleanup(func() {
		warningOut = prevOut
		overrideWarning = sync.Once{}
	})
	return &buf
}

// quietOverride sets NIWA_TEST_VAULT_TIMEOUT for the test and keeps
// the warning it triggers off the test's stderr.
func quietOverride(t *testing.T, value string) {
	t.Helper()
	captureOverrideWarning(t)
	t.Setenv(testTimeoutEnv, value)
}

func TestCallBound(t *testing.T) {
	const def = 30 * time.Second
	cases := []struct {
		name  string
		value string
		unset bool
		want  time.Duration
	}{
		{name: "unset", unset: true, want: def},
		{name: "empty", value: "", want: def},
		{name: "unparseable", value: "soon", want: def},
		{name: "below floor", value: "10ms", want: def},
		{name: "exactly floor", value: "50ms", want: 50 * time.Millisecond},
		{name: "valid shorter", value: "2s", want: 2 * time.Second},
		{name: "equal to default", value: "30s", want: def},
		{name: "longer", value: "5m", want: def},
		{name: "negative", value: "-1s", want: def},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureOverrideWarning(t)
			if tc.unset {
				// t.Setenv records the old value for restore; then
				// remove the variable outright.
				t.Setenv(testTimeoutEnv, "")
				os.Unsetenv(testTimeoutEnv)
			} else {
				t.Setenv(testTimeoutEnv, tc.value)
			}
			if got := callBound(def); got != tc.want {
				t.Errorf("callBound(%s) with %s=%q = %s, want %s", def, testTimeoutEnv, tc.value, got, tc.want)
			}
			warned := buf.Len() > 0
			if wantWarn := tc.want != def; warned != wantWarn {
				t.Errorf("warning printed = %v, want %v (output %q)", warned, wantWarn, buf.String())
			}
		})
	}
}

// TestCallBound_PerBound checks that the override is compared against
// each call's own default: a value between the probe and export
// bounds shortens the export but not the probe.
func TestCallBound_PerBound(t *testing.T) {
	quietOverride(t, "20s")
	if got := callBound(exportTimeout); got != 20*time.Second {
		t.Errorf("export bound = %s, want 20s", got)
	}
	if got := callBound(loginTimeout); got != 20*time.Second {
		t.Errorf("login bound = %s, want 20s", got)
	}
	if got := callBound(probeTimeout); got != probeTimeout {
		t.Errorf("probe bound = %s, want the default %s", got, probeTimeout)
	}
}

func TestCallBound_WarnsOncePerRun(t *testing.T) {
	buf := captureOverrideWarning(t)
	t.Setenv(testTimeoutEnv, "200ms")
	for i := 0; i < 5; i++ {
		callBound(exportTimeout)
		callBound(probeTimeout)
		callBound(loginTimeout)
	}
	out := buf.String()
	if n := strings.Count(out, "\n"); n != 1 {
		t.Fatalf("warning printed %d times, want once:\n%s", n, out)
	}
	if !strings.Contains(out, testTimeoutEnv) || !strings.Contains(out, "200ms") {
		t.Errorf("warning %q should name %s and the bound in effect", out, testTimeoutEnv)
	}
}

func TestCallTimedOut(t *testing.T) {
	t.Run("own deadline", func(t *testing.T) {
		ctx, cancel := withCallDeadline(context.Background(), time.Millisecond)
		defer cancel()
		<-ctx.Done()
		if !callTimedOut(ctx, nil) {
			t.Error("callTimedOut = false after the call's own deadline fired")
		}
	})
	t.Run("wait delay", func(t *testing.T) {
		ctx, cancel := withCallDeadline(context.Background(), time.Hour)
		defer cancel()
		if !callTimedOut(ctx, fmt.Errorf("wrapped: %w", exec.ErrWaitDelay)) {
			t.Error("callTimedOut = false for exec.ErrWaitDelay")
		}
	})
	t.Run("caller cancelled", func(t *testing.T) {
		parent, cancelParent := context.WithCancel(context.Background())
		ctx, cancel := withCallDeadline(parent, time.Hour)
		defer cancel()
		cancelParent()
		if callTimedOut(ctx, nil) {
			t.Error("callTimedOut = true when the caller cancelled")
		}
		if callTimedOut(ctx, exec.ErrWaitDelay) {
			t.Error("callTimedOut = true for ErrWaitDelay after the caller cancelled")
		}
	})
	t.Run("caller deadline", func(t *testing.T) {
		parent, cancelParent := context.WithTimeout(context.Background(), time.Millisecond)
		defer cancelParent()
		ctx, cancel := withCallDeadline(parent, time.Hour)
		defer cancel()
		<-ctx.Done()
		if callTimedOut(ctx, nil) {
			t.Error("callTimedOut = true when the caller's own deadline fired")
		}
	})
	t.Run("normal return", func(t *testing.T) {
		ctx, cancel := withCallDeadline(context.Background(), time.Hour)
		defer cancel()
		if callTimedOut(ctx, errors.New("exit 1")) {
			t.Error("callTimedOut = true for an ordinary error")
		}
	})
}
