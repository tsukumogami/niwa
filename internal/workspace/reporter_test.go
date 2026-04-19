package workspace

import (
	"bytes"
	"strings"
	"testing"
)

// TestReporterNonTTYStatus verifies that Status is a no-op on non-TTY output.
// Scenario: scenario-1 (Reporter non-TTY output — no ANSI sequences)
func TestReporterNonTTYStatus(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, false)
	r.Status("working...")
	if buf.Len() != 0 {
		t.Errorf("Status on non-TTY wrote %q, want empty", buf.String())
	}
}

// TestReporterNonTTYLog verifies that Log appends a line with no ANSI sequences.
// Scenario: scenario-1 (Reporter non-TTY output — no ANSI sequences)
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
// Scenario: scenario-1 (Reporter non-TTY output — no ANSI sequences)
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
// Scenario: scenario-3 (Reporter.Writer() routes through Log)
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

// TestReporterTTYStatus verifies that Status writes a CR+erase sequence on TTY.
// Scenario: scenario-2 (Reporter TTY output — carriage-return status line)
func TestReporterTTYStatus(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.Status("cloning foo...")
	got := buf.String()
	want := "\r\033[Kcloning foo..."
	if got != want {
		t.Errorf("Status TTY: got %q, want %q", got, want)
	}
	if !r.needsClear {
		t.Error("Status TTY: needsClear should be true after Status call")
	}
}

// TestReporterTTYLogClearsStatus verifies that Log clears the status line when
// needsClear is set.
// Scenario: scenario-2 (Reporter TTY output — carriage-return status line)
func TestReporterTTYLogClearsStatus(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.Status("cloning foo...")
	buf.Reset() // ignore Status output for the assertion below
	r.Log("cloned foo")
	got := buf.String()
	want := "\r\033[Kcloned foo\n"
	if got != want {
		t.Errorf("Log after Status TTY: got %q, want %q", got, want)
	}
	if r.needsClear {
		t.Error("Log TTY: needsClear should be false after Log call")
	}
}

// TestReporterTTYLogNoClearWhenNotNeeded verifies that Log does not write the
// CR+erase prefix when needsClear is false.
// Scenario: scenario-2 (Reporter TTY output — carriage-return status line)
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

// TestReporterTTYWarn verifies that Warn clears status and prefixes "warning: ".
// Scenario: scenario-2 (Reporter TTY output — carriage-return status line)
func TestReporterTTYWarn(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.Status("doing work...")
	buf.Reset()
	r.Warn("oops: %s", "detail")
	got := buf.String()
	want := "\r\033[Kwarning: oops: detail\n"
	if got != want {
		t.Errorf("Warn after Status TTY: got %q, want %q", got, want)
	}
}

// TestReporterTTYWriter verifies that Writer() routes writes through Log on TTY,
// including that a preceding Status line is cleared.
// Scenario: scenario-3 (Reporter.Writer() routes through Log)
func TestReporterTTYWriter(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, true)
	r.Status("working...")
	buf.Reset()
	w := r.Writer()
	_, err := w.Write([]byte("output line\n"))
	if err != nil {
		t.Fatalf("Writer.Write: %v", err)
	}
	got := buf.String()
	want := "\r\033[Koutput line\n"
	if got != want {
		t.Errorf("Writer TTY: got %q, want %q", got, want)
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
