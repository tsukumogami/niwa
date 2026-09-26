package workspace

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
)

// TestIsGitErrorLine verifies the prefix-based classifier.
// Scenario: scenario-10 (isGitErrorLine classifies git diagnostic prefixes correctly)
func TestIsGitErrorLine(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		// Error prefixes
		{"fatal: repository 'https://example.com/' not found", true},
		{"error: pathspec 'main' did not match", true},
		{"warning: detached HEAD state", true},
		// Leading whitespace variants
		{"  fatal: something", true},
		{"\terror: something", true},
		{"\t warning: something", true},
		// Normal informational lines
		{"remote: Enumerating objects: 5, done.", false},
		{"Cloning into 'repo'...", false},
		{"Already up to date.", false},
		{"From https://github.com/org/repo", false},
		{"", false},
		{"  ", false},
		// Partial matches (not at start of trimmed text)
		{"note: fatal: not at start", false},
		{"info: error: not at start", false},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got := isGitErrorLine(tt.line)
			if got != tt.want {
				t.Errorf("isGitErrorLine(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

// TestStripEscapes verifies that CSI and OSC sequences are removed.
// Scenario: scenario-11 (ANSI/OSC escape sequences are stripped from git output lines)
func TestStripEscapes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no escapes",
			input: "plain text",
			want:  "plain text",
		},
		{
			name:  "CSI bold",
			input: "\x1b[1mfatal: repo not found\x1b[0m",
			want:  "fatal: repo not found",
		},
		{
			name:  "CSI color",
			input: "\x1b[31merror: failed\x1b[0m",
			want:  "error: failed",
		},
		{
			name:  "OSC sequence",
			input: "\x1b]0;title\x07plain",
			want:  "plain",
		},
		{
			name:  "mixed CSI and OSC",
			input: "\x1b[1m\x1b]0;title\x07text\x1b[0m",
			want:  "text",
		},
		{
			name:  "CSI with multiple params",
			input: "\x1b[38;5;200mcolored\x1b[0m",
			want:  "colored",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "private-parameter CSI (hide cursor)",
			input: "before\x1b[?25lafter",
			want:  "beforeafter",
		},
		{
			name:  "CSI with intermediate bytes",
			input: "a\x1b[0 qb",
			want:  "ab",
		},
		{
			name:  "ST-terminated OSC",
			input: "\x1b]0;title\x1b\\plain",
			want:  "plain",
		},
		{
			name:  "bare ESC c terminal reset",
			input: "before\x1bcafter",
			want:  "beforeafter",
		},
		{
			name:  "DCS sequence",
			input: "a\x1bP1;2|payload\x1b\\b",
			want:  "ab",
		},
		{
			name:  "APC sequence",
			input: "a\x1b_command\x1b\\b",
			want:  "ab",
		},
		{
			name:  "PM sequence",
			input: "a\x1b^message\x1b\\b",
			want:  "ab",
		},
		{
			name:  "lone trailing ESC",
			input: "text\x1b",
			want:  "text",
		},
		{
			name:  "carriage return",
			input: "safe\rforged",
			want:  "safeforged",
		},
		{
			name:  "backspace",
			input: "safe\b\b\b\bfake",
			want:  "safefake",
		},
		{
			name:  "BEL",
			input: "ding\x07dong",
			want:  "dingdong",
		},
		{
			name:  "NUL",
			input: "a\x00b",
			want:  "ab",
		},
		{
			name:  "DEL",
			input: "a\x7fb",
			want:  "ab",
		},
		{
			name:  "tab survives",
			input: "col1\tcol2",
			want:  "col1\tcol2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripEscapes(tt.input)
			if got != tt.want {
				t.Errorf("stripEscapes(%q) = %q, want %q", tt.input, got, tt.want)
			}
			// Verify no escape bytes remain.
			if strings.ContainsRune(got, '\x1b') {
				t.Errorf("stripEscapes: output still contains ESC byte: %q", got)
			}
		})
	}
}

// TestRunGitWithReporter_RoutesLinesThrough verifies that informational git
// output is routed through r.Log and error-prefixed lines through r.Warn.
// Scenario: scenario-9 (gitutil helpers exist with correct structure)
func TestRunGitWithReporter_RoutesLinesThrough(t *testing.T) {
	bareDir, localDir := setupBareAndClone(t)
	_ = bareDir

	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, false)

	// git status --short produces informational output.
	cmd := exec.CommandContext(context.Background(), "git", "-C", localDir, "status", "--short")
	if err := runGitWithReporter(r, cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No fatal/error lines expected; output routes through Log (no "warning: " prefix).
	if strings.Contains(buf.String(), "warning: fatal") {
		t.Errorf("expected no warning prefix for informational output, got: %q", buf.String())
	}
}

// TestRunGitWithReporter_EmbedsDiagnostic verifies that when a git command fails
// and emits fatal/error lines, the returned error embeds that text rather than
// the generic "exit status N" string.
// Scenario: scenario-9 (gitutil helpers exist with correct structure)
func TestRunGitWithReporter_EmbedsDiagnostic(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, false)

	// Attempt to clone a nonexistent path; git will emit "fatal: ..." on stderr.
	cmd := exec.CommandContext(context.Background(), "git", "clone", "/nonexistent/path/repo", t.TempDir()+"/dest")
	err := runGitWithReporter(r, cmd)
	if err == nil {
		t.Fatal("expected error cloning nonexistent path, got nil")
	}

	// The returned error should NOT be just "exit status N".
	if err.Error() == "exit status 128" || err.Error() == "exit status 1" {
		t.Errorf("error is generic exit-status string, want embedded git diagnostic: %v", err)
	}
}

// TestRunCmdWithReporter_AllLinesViaLog verifies that every line of a non-git
// command's output is routed through r.Log — durable, prefixed, and with no
// classifier applied. This test replaces one that asserted the opposite: it
// pinned the routing through r.Status, which is what made setup-script output
// invisible off a TTY and transient on one (issue #239).
// Scenario: scenario-9 (gitutil helpers exist with correct structure)
func TestRunCmdWithReporter_AllLinesViaLog(t *testing.T) {
	var buf syncBuffer
	r := NewReporterWithTTY(&buf, true) // TTY mode so a spinner is in play

	cmd := exec.CommandContext(context.Background(), "sh", "-c", "echo fatal: this is fine && echo hello")
	if err := runCmdWithReporter(r, cmd, "[myapp/01-run.sh] ", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	perm := permanentOutput(buf.String())
	// There is no classifier: a "fatal:" line is just another line, not a warning.
	if strings.Contains(perm, "warning: fatal") {
		t.Errorf("runCmdWithReporter: 'fatal:' line incorrectly routed through Warn: %q", perm)
	}
	for _, want := range []string{
		"[myapp/01-run.sh] fatal: this is fine\n",
		"[myapp/01-run.sh] hello\n",
	} {
		if !strings.Contains(perm, want) {
			t.Errorf("runCmdWithReporter: missing durable line %q in: %q", want, perm)
		}
	}
}

// TestRunCmdWithReporter_ScrubsSecrets verifies the redactor is applied to
// every emitted line, and that a nil redactor is tolerated.
func TestRunCmdWithReporter_ScrubsSecrets(t *testing.T) {
	const plaintext = "hunter2-hunter2-hunter2"

	red := secret.NewRedactor()
	red.Register([]byte(plaintext))

	var buf syncBuffer
	r := NewReporterWithTTY(&buf, false)
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "echo token="+plaintext)
	if err := runCmdWithReporter(r, cmd, "", red); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	perm := permanentOutput(buf.String())
	if strings.Contains(perm, plaintext) {
		t.Errorf("secret was not scrubbed: %q", perm)
	}
	if !strings.Contains(perm, "token=***\n") {
		t.Errorf("expected the redacted placeholder, got: %q", perm)
	}
}

// TestRunCmdWithReporter_LongLineDoesNotTruncateStream proves the scanner no
// longer dies on a line over bufio.Scanner's 64 KB default. Exceeding it ended
// the scan, closed the pipe, and SIGPIPEd the script — so a script emitting one
// long blob was reported as a broken pipe with none of its output shown.
func TestRunCmdWithReporter_LongLineDoesNotTruncateStream(t *testing.T) {
	var buf syncBuffer
	r := NewReporterWithTTY(&buf, false)

	// 100_000 bytes on one line, then a short identifiable line after it.
	script := `awk 'BEGIN { s=""; for (i=0;i<100000;i++) s = s "x"; print s }'; echo AFTER-THE-BLOB`
	cmd := exec.CommandContext(context.Background(), "sh", "-c", script)
	if err := runCmdWithReporter(r, cmd, "", nil); err != nil {
		t.Fatalf("unexpected error (a broken pipe here is the bug): %v", err)
	}

	perm := permanentOutput(buf.String())
	if !strings.Contains(perm, strings.Repeat("x", 100000)) {
		t.Errorf("the long line was truncated or dropped (got %d bytes of output)", len(perm))
	}
	if !strings.Contains(perm, "AFTER-THE-BLOB\n") {
		t.Error("output after the long line was lost")
	}
	if strings.Contains(perm, "warning:") {
		t.Errorf("unexpected scanner warning: %q", perm)
	}
}

// TestRunCmdWithReporter_CarriageReturnCannotEraseThePrefix proves the prefix
// survives a script that tries to overwrite the line it is rendering on. The
// prefix is what distinguishes script output from niwa's own lines, so
// anything that can erase it defeats that control.
func TestRunCmdWithReporter_CarriageReturnCannotEraseThePrefix(t *testing.T) {
	var buf syncBuffer
	r := NewReporterWithTTY(&buf, false)

	cmd := exec.CommandContext(context.Background(), "sh", "-c", `printf 'safe\rforged\n'`)
	if err := runCmdWithReporter(r, cmd, "[myapp/01-run.sh] ", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	perm := permanentOutput(buf.String())
	if !strings.Contains(perm, "[myapp/01-run.sh] safeforged\n") {
		t.Errorf("carriage return was not neutralized: %q", perm)
	}
	if strings.ContainsRune(perm, '\r') {
		t.Errorf("carriage return reached the terminal: %q", perm)
	}
}

// TestRunGitWithReporter_StripEscapesInOutput verifies that ANSI escape
// sequences from git output are stripped before reaching the reporter.
// Scenario: scenario-11 (ANSI/OSC escape sequences are stripped from git output lines)
func TestRunGitWithReporter_StripEscapesInOutput(t *testing.T) {
	var buf syncBuffer
	r := NewReporterWithTTY(&buf, true) // TTY mode so spinner runs

	// Use printf to emit a line with CSI escape sequences; git is not involved
	// here but the same helper is used for subprocess output.
	cmd := exec.CommandContext(context.Background(), "sh", "-c", `printf '\033[1mhello\033[0m\n'`)
	if err := runCmdWithReporter(r, cmd, "", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	perm := permanentOutput(buf.String())
	// The reporter itself writes ANSI status sequences; check the INPUT bold
	// escape was stripped, not just that no ESC byte exists.
	if strings.Contains(perm, "\x1b[1m") {
		t.Errorf("expected input bold escape stripped, but \\x1b[1m present in output: %q", perm)
	}
	if !strings.Contains(perm, "hello\n") {
		t.Errorf("expected 'hello' in stripped output, got: %q", perm)
	}
}
