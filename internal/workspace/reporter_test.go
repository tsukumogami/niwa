package workspace

import (
	"bytes"
	"strings"
	"testing"
)

// TestReporterNonTTYStatus verifies that Status is a no-op on non-TTY output.
func TestReporterNonTTYStatus(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, false)
	r.Status("working...")
	if buf.Len() != 0 {
		t.Errorf("Status on non-TTY wrote %q, want empty", buf.String())
	}
}

// TestReporterNonTTYLog verifies that Log appends a line with no ANSI sequences.
func TestReporterNonTTYLog(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, false)
	r.Log("hello %s", "world")
	got := buf.String()
	if got != "hello world\n" {
		t.Errorf("Log non-TTY: got %q, want %q", got, "hello world\n")
	}
	if strings.ContainsAny(got, "\r\x1b") {
		t.Errorf("Log non-TTY: output contains ANSI/CR sequences: %q", got)
	}
}

// TestReporterNonTTYWarn verifies that Warn prefixes with "warning: " on non-TTY.
func TestReporterNonTTYWarn(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, false)
	r.Warn("something broke: %v", "err")
	got := buf.String()
	want := "warning: something broke: err\n"
	if got != want {
		t.Errorf("Warn non-TTY: got %q, want %q", got, want)
	}
	if strings.ContainsAny(got, "\r\x1b") {
		t.Errorf("Warn non-TTY: output contains ANSI/CR sequences: %q", got)
	}
}

// TestReporterNonTTYWriter verifies that Writer() routes through Log on non-TTY.
func TestReporterNonTTYWriter(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, false)
	w := r.Writer()
	_, err := w.Write([]byte("via writer\n"))
	if err != nil {
		t.Fatalf("Writer.Write: %v", err)
	}
	got := buf.String()
	if got != "via writer\n" {
		t.Errorf("Writer non-TTY: got %q, want %q", got, "via writer\n")
	}
}

// TestReporterTTYStatus verifies that Status starts the spinner goroutine and
// sets needsClear on TTY output.
func TestReporterTTYStatus(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.Status("cloning foo...")
	if !r.needsClear {
		t.Error("Status TTY: needsClear should be true after Status call")
	}
	r.mu.Lock()
	active := r.spinStop != nil
	r.mu.Unlock()
	if !active {
		t.Error("Status TTY: spinner goroutine should be running after Status call")
	}
	r.Log("done") // stop goroutine before test exits
}

// TestReporterTTYLogClearsStatus verifies that Log stops the spinner and the
// permanent log line appears cleanly at the end of output.
func TestReporterTTYLogClearsStatus(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.Status("cloning foo...")
	r.Log("cloned foo")
	got := buf.String()
	// Output ends with the clear sequence + log line, regardless of how many
	// spinner frames the goroutine wrote before being stopped.
	want := "\r\033[Kcloned foo\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("Log after Status TTY: got %q, want suffix %q", got, want)
	}
	if r.needsClear {
		t.Error("Log TTY: needsClear should be false after Log call")
	}
}

// TestReporterTTYLogNoClearWhenNotNeeded verifies that Log does not write the
// CR+erase prefix when no spinner is active.
func TestReporterTTYLogNoClearWhenNotNeeded(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.Log("direct log")
	got := buf.String()
	want := "direct log\n"
	if got != want {
		t.Errorf("Log TTY no clear: got %q, want %q", got, want)
	}
}

// TestReporterTTYWarn verifies that Warn stops the spinner and prepends "warning: ".
func TestReporterTTYWarn(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.Status("doing work...")
	r.Warn("oops: %s", "detail")
	got := buf.String()
	want := "\r\033[Kwarning: oops: detail\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("Warn after Status TTY: got %q, want suffix %q", got, want)
	}
}

// TestReporterTTYWriter verifies that Writer() routes writes through Log on TTY,
// stopping the spinner first.
func TestReporterTTYWriter(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.Status("working...")
	w := r.Writer()
	_, err := w.Write([]byte("output line\n"))
	if err != nil {
		t.Fatalf("Writer.Write: %v", err)
	}
	got := buf.String()
	want := "\r\033[Koutput line\n"
	if !strings.HasSuffix(got, want) {
		t.Errorf("Writer TTY: got %q, want suffix %q", got, want)
	}
}

// TestReporterWriterNoDoubleNewline verifies that Writer strips trailing
// newline from input to avoid double-newlines in output.
func TestReporterWriterNoDoubleNewline(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, false)
	w := r.Writer()
	w.Write([]byte("line\n"))
	got := buf.String()
	if strings.Count(got, "\n") != 1 {
		t.Errorf("Writer: got %d newlines, want 1: %q", strings.Count(got, "\n"), got)
	}
}

// TestNewReporterNonFileWriter verifies that NewReporter sets isTTY=false
// when the writer is not an *os.File.
func TestNewReporterNonFileWriter(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporter(&buf)
	if r.isTTY {
		t.Error("NewReporter with bytes.Buffer: expected isTTY=false")
	}
}

// TestReporterSpinnerTickFormat verifies that doTick writes the correct
// spinner frame format: CR+erase + frame + space + message.
// Called directly (no goroutine) to avoid timing dependencies.
func TestReporterSpinnerTickFormat(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.spinMsg = "cloning foo..."
	r.spinFrame = 0
	r.doTick()
	got := buf.String()
	wantPrefix := "\r\033[K"
	wantSuffix := " cloning foo..."
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("doTick: got %q, want prefix %q", got, wantPrefix)
	}
	if !strings.HasSuffix(got, wantSuffix) {
		t.Errorf("doTick: got %q, want suffix %q", got, wantSuffix)
	}
	if r.spinFrame != 1 {
		t.Errorf("spinFrame after one tick: got %d, want 1", r.spinFrame)
	}
}

// TestReporterSpinnerFrameCycles verifies that the spinner cycles through all
// frames and wraps back to the start.
func TestReporterSpinnerFrameCycles(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.spinMsg = "working..."
	n := len(spinFrames)
	for i := 0; i < n+1; i++ {
		r.doTick()
	}
	if r.spinFrame != n+1 {
		t.Errorf("spinFrame after %d ticks: got %d, want %d", n+1, r.spinFrame, n+1)
	}
	// The (n+1)th tick uses spinFrames[(n+1-1) % n] = spinFrames[n % n] = spinFrames[0]
	wantFrame := spinFrames[0]
	lastTick := buf.String()
	// Extract the last tick output (last \r\033[K... segment)
	parts := strings.Split(lastTick, "\r\033[K")
	last := parts[len(parts)-1]
	if !strings.HasPrefix(last, wantFrame) {
		t.Errorf("after full cycle, frame should wrap to %q, got segment %q", wantFrame, last)
	}
}

// TestReporterSpinnerNoOpWhenMsgEmpty verifies that doTick is a no-op when
// spinMsg is empty (e.g., after stopSpinner clears it).
func TestReporterSpinnerNoOpWhenMsgEmpty(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.spinMsg = ""
	r.doTick()
	if buf.Len() != 0 {
		t.Errorf("doTick with empty spinMsg wrote %q, want empty", buf.String())
	}
}

// TestReporterMultipleStatusUpdates verifies that updating the message while the
// spinner is active shows the latest message after Log stops the goroutine.
func TestReporterMultipleStatusUpdates(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.Status("step 1")
	r.Status("step 2")
	r.Status("step 3")
	r.Log("complete")
	got := buf.String()
	if !strings.HasSuffix(got, "\r\033[Kcomplete\n") {
		t.Errorf("after multiple Status + Log: got %q, want suffix %q", got, "\r\033[Kcomplete\n")
	}
	if r.needsClear {
		t.Error("needsClear should be false after Log")
	}
}
