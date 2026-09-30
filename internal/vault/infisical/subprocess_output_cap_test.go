package infisical

// Tests for the output caps defaultCommander puts on a subprocess's
// stdout and stderr. Like subprocess_bounds_test.go, every subprocess
// here is a /bin/sh stub in a t.TempDir().

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault"
)

// shrinkOutputCaps lowers both caps for the rest of the test.
func shrinkOutputCaps(t *testing.T, stdout, stderr int) {
	t.Helper()
	origOut, origErr := maxStdoutBytes, maxStderrBytes
	maxStdoutBytes, maxStderrBytes = stdout, stderr
	t.Cleanup(func() { maxStdoutBytes, maxStderrBytes = origOut, origErr })
}

func TestCappedBuffer(t *testing.T) {
	b := &cappedBuffer{limit: 5}
	for _, chunk := range []string{"ab", "cd", "efgh", "ij"} {
		n, err := b.Write([]byte(chunk))
		if n != len(chunk) || err != nil {
			t.Fatalf("Write(%q) = (%d, %v), want (%d, nil)", chunk, n, err, len(chunk))
		}
	}
	if string(b.buf) != "abcde" || !b.truncated {
		t.Errorf("buffer = %q truncated=%v, want %q truncated=true", b.buf, b.truncated, "abcde")
	}

	exact := &cappedBuffer{limit: 3}
	_, _ = exact.Write([]byte("abc"))
	if exact.truncated {
		t.Error("a write that exactly fills the cap marked the buffer truncated")
	}
}

// The real caps: a stub that writes past 32 MiB of stdout comes back
// truncated to the cap, with its real exit code and errOutputTooLarge,
// and a large stderr is truncated with no error of its own.
func TestDefaultCommander_RealCaps(t *testing.T) {
	stub := writeInfisicalStub(t, "head -c 33554500 /dev/zero\nhead -c 1048600 /dev/zero >&2\nexit 0")
	stdout, stderr, code, err := defaultCommander{}.Run(context.Background(), stub, nil)
	if !errors.Is(err, errOutputTooLarge) {
		t.Fatalf("err = %v, want errOutputTooLarge", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if len(stdout) != 32<<20 {
		t.Errorf("stdout = %d bytes, want %d", len(stdout), 32<<20)
	}
	if len(stderr) != 1<<20 {
		t.Errorf("stderr = %d bytes, want %d", len(stderr), 1<<20)
	}
}

func TestDefaultCommander_StderrOverCapIsNotAnError(t *testing.T) {
	shrinkOutputCaps(t, 64, 8)
	stub := writeInfisicalStub(t, "echo ok\necho 'a long complaint on stderr' >&2\nexit 4")
	stdout, stderr, code, err := defaultCommander{}.Run(context.Background(), stub, nil)
	if err != nil || code != 4 {
		t.Fatalf("Run = (%d, %v), want (4, nil)", code, err)
	}
	if string(stdout) != "ok\n" || string(stderr) != "a long c" {
		t.Errorf("stdout %q stderr %q, want %q and %q", stdout, stderr, "ok\n", "a long c")
	}
}

// An export that exits 0 but prints more than the cap is an answered
// failure, like output niwa can't parse: never a panic, never "client
// not installed", never a lapse the store could serve for.
func TestRunInfisicalExport_OversizedOutputIsAnswered(t *testing.T) {
	shrinkOutputCaps(t, 16, 1<<20)
	putStubOnPath(t, writeInfisicalStub(t, probeAnswersAtOnce+`echo '{"API_KEY":"a value longer than the cap"}'`))

	_, _, err := runInfisicalExport(context.Background(), nil, "proj", "dev", "/", "")
	if err == nil {
		t.Fatal("export returned no error for output past the cap")
	}
	var class *vault.FailureClass
	if !errors.As(err, &class) || class.Class != vault.ClassAnswered {
		t.Fatalf("err = %v (class %v), want an answered failure", err, class)
	}
	if errors.Is(err, vault.ErrProviderUnreachable) {
		t.Errorf("oversized output matches ErrProviderUnreachable: %v", err)
	}
	if !strings.Contains(err.Error(), "exceeded 16 bytes") {
		t.Errorf("error %q does not name the cap", err)
	}
}

// A non-zero export with oversized stdout is classified as usual from
// what was kept: here a 403 with no probe answer is a lapse.
func TestRunInfisicalExport_OversizedFailureIsClassifiedAsUsual(t *testing.T) {
	shrinkOutputCaps(t, 16, 1<<20)
	putStubOnPath(t, writeInfisicalStub(t, probeAnswersAtOnce+
		"echo 'more stdout than the cap allows'\necho 'Response Code: 403' >&2\nexit 1"))

	_, _, err := runInfisicalExport(context.Background(), nil, "proj", "dev", "/", "")
	var class *vault.FailureClass
	if !errors.As(err, &class) || class.Class != vault.ClassUnauthenticated {
		t.Fatalf("err = %v (class %v), want unauthenticated", err, class)
	}
}

// A probe whose stdout passes the cap is no answer, even when what it
// printed is a verified session: a 403 then falls to rule 4 (a lapse)
// instead of rule 3 (a real refusal). The kept prefix is the whole
// JSON object plus trailing spaces, which would parse on its own, so
// only errOutputTooLarge keeps it from counting.
func TestRunProbe_OversizedOutputIsNoAnswer(t *testing.T) {
	shrinkOutputCaps(t, 256, 1<<20)
	t.Setenv(tokenEnvVar, "")
	stub := writeInfisicalStub(t, `if [ "$1" = login ]; then
  printf '%s' '{"sessions":[{"status":"authenticated","verification":{"state":"verified"}}]}'
  head -c 4096 /dev/zero | tr '\0' ' '
  exit 0
fi
echo 'Response Code: 403' >&2
exit 1`)
	putStubOnPath(t, stub)

	if r := runProbe(context.Background(), defaultCommander{}); r.parsed {
		t.Errorf("an oversized probe parsed as an answer: %+v", r)
	}
	_, _, err := runInfisicalExport(context.Background(), nil, "proj", "dev", "/", "")
	var class *vault.FailureClass
	if !errors.As(err, &class) || class.Class != vault.ClassUnauthenticated {
		t.Fatalf("err = %v (class %v), want unauthenticated", err, class)
	}
}
