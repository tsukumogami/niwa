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
	if got := string(exact.wholeLines()); got != "abc" {
		t.Errorf("wholeLines of an untruncated buffer = %q, want %q", got, "abc")
	}

	lines := &cappedBuffer{limit: 8}
	_, _ = lines.Write([]byte("one\ntwo-cut-here"))
	if got := string(lines.wholeLines()); got != "one\n" {
		t.Errorf("wholeLines = %q, want %q", got, "one\n")
	}
	if b.wholeLines() != nil {
		t.Errorf("wholeLines with no complete line = %q, want nil", b.wholeLines())
	}
}

// The real caps: a stub that writes past 32 MiB of stdout comes back
// truncated to the cap, with its real exit code and errOutputTooLarge,
// and a large stderr is truncated with no error of its own.
func TestDefaultCommander_RealCaps(t *testing.T) {
	stub := writeInfisicalStub(t, "head -c 33554500 /dev/zero\nyes x | head -c 1048600 >&2\nexit 0")
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
	shrinkOutputCaps(t, 64, 12)
	stub := writeInfisicalStub(t, "echo ok\necho 'first' >&2\necho 'a long complaint cut by the cap' >&2\nexit 4")
	stdout, stderr, code, err := defaultCommander{}.Run(context.Background(), stub, nil)
	if err != nil || code != 4 {
		t.Fatalf("Run = (%d, %v), want (4, nil)", code, err)
	}
	// The cut second line is dropped whole, so no half of a value in it
	// can slip past the scrubber.
	if string(stdout) != "ok\n" || string(stderr) != "first\n" {
		t.Errorf("stdout %q stderr %q, want %q and %q", stdout, stderr, "ok\n", "first\n")
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

// Past the cap the buffer keeps nothing but the first status line,
// rebuilt from its number. A status the cap cut in half, a prefix in
// the middle of a line, and an overlong line never count, and the cut
// line itself stays dropped.
func TestCappedBufferKeepsStatusLinePastCap(t *testing.T) {
	cases := []struct {
		name   string
		writes []string
		want   string
	}{
		{"status after the cap", []string{"head\nsecret-cut", "-here\nnoise\nResponse Code: 404\nResponse Code: 500\n"}, "head\nResponse Code: 404\n"},
		{"split across writes", []string{"head\nxxxxxxx", "\nResp", "onse Co", "de: 403\n"}, "head\nResponse Code: 403\n"},
		{"no trailing newline", []string{"head\nxx", "xxxxxx\nResponse Code: 401"}, "head\nResponse Code: 401\n"},
		{"cut line is not a status", []string{"head\nResponse Co", "de: 404\n"}, "head\n"},
		{"prefix mid-line", []string{"head\nxxxx", "xxxxx\nerror Response Code: 404\n"}, "head\n"},
		{"overlong line", []string{"head\nxxxxx", "\nResponse Code: 404" + strings.Repeat(" ", 80) + "\n"}, "head\n"},
		{"cap on a line boundary", []string{"head\nabc\n", "Response Code: 404\n"}, "head\nabc\nResponse Code: 404\n"},
		{"line boundary inside one write", []string{"head\nabc\nResponse Code: 404\n"}, "head\nabc\nResponse Code: 404\n"},
		{"CRLF past the cap", []string{"head\nxxxxxxx\r\nResponse Code: 404\r\n"}, "head\nResponse Code: 404\n"},
		{"writes after the status", []string{"head\nxxxxxxx\nResponse Code: 404\n", "Response Code: 500\n", "more noise\n"}, "head\nResponse Code: 404\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := &cappedBuffer{limit: 9, keepStatusLine: true}
			for _, w := range tc.writes {
				if n, err := b.Write([]byte(w)); n != len(w) || err != nil {
					t.Fatalf("Write(%q) = (%d, %v)", w, n, err)
				}
			}
			if got := string(b.wholeLines()); got != tc.want {
				t.Errorf("wholeLines = %q, want %q", got, tc.want)
			}
			if len(b.pending) > maxStatusLineLen {
				t.Errorf("pending grew to %d bytes", len(b.pending))
			}
		})
	}

	// The cap falls right before a status line's newline: the line is
	// whole, so it still counts, once, as the status.
	for _, writes := range [][]string{
		{"x\nResponse Code: 404\nrest\n"},
		{"x\nResponse Code: 404", "\nrest\n"},
	} {
		edge := &cappedBuffer{limit: 20, keepStatusLine: true}
		for _, w := range writes {
			_, _ = edge.Write([]byte(w))
		}
		if got := string(edge.wholeLines()); got != "x\nResponse Code: 404\n" {
			t.Errorf("writes %q: wholeLines = %q, want %q", writes, got, "x\nResponse Code: 404\n")
		}
	}

	plain := &cappedBuffer{limit: 5}
	_, _ = plain.Write([]byte("head\nResponse Code: 404\n"))
	if got := string(plain.wholeLines()); got != "head\n" {
		t.Errorf("without keepStatusLine, wholeLines = %q, want %q", got, "head\n")
	}
}

// A status line printed after more than the real 1 MiB stderr cap is
// still classified from its status: a 404 is answered, not served from
// the store. The noise is whole lines (about 1.03 MiB), so the status
// line starts a line of its own.
func TestRunInfisicalExport_StatusPastStderrCapIsAnswered(t *testing.T) {
	putStubOnPath(t, writeInfisicalStub(t, probeAnswersAtOnce+
		"yes 'a noisy line of CLI output' | head -n 40000 >&2\necho 'Response Code: 404' >&2\nexit 1"))

	_, _, err := runInfisicalExport(context.Background(), nil, "proj", "dev", "/", "")
	var class *vault.FailureClass
	if !errors.As(err, &class) || class.Class != vault.ClassAnswered || class.HTTPStatus != 404 {
		t.Fatalf("class %+v, want answered with status 404 (err nil: %v)", class, err == nil)
	}
}
