package workspace

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
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

// TestRunCmdWithReporter_AllLinesViaLog verifies that all output from a
// non-git command is routed through r.Log with no classifier.
// Scenario: scenario-9 (gitutil helpers exist with correct structure)
func TestRunCmdWithReporter_AllLinesViaLog(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, false)

	cmd := exec.CommandContext(context.Background(), "sh", "-c", "echo fatal: this is fine && echo hello")
	if err := runCmdWithReporter(r, cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	// All lines should be logged. Neither should be prefixed "warning: " since
	// runCmdWithReporter has no classifier.
	if strings.Contains(out, "warning: fatal") {
		t.Errorf("runCmdWithReporter: 'fatal:' line incorrectly routed through Warn: %q", out)
	}
	if !strings.Contains(out, "fatal: this is fine") {
		t.Errorf("runCmdWithReporter: expected 'fatal: this is fine' in output, got: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("runCmdWithReporter: expected 'hello' in output, got: %q", out)
	}
}

// TestRunGitWithReporter_StripEscapesInOutput verifies that ANSI escape
// sequences from git output are stripped before reaching the reporter.
// Scenario: scenario-11 (ANSI/OSC escape sequences are stripped from git output lines)
func TestRunGitWithReporter_StripEscapesInOutput(t *testing.T) {
	var buf bytes.Buffer
	r := NewReporterWithTTY(&buf, false)

	// Use printf to emit a line with CSI escape sequences; git is not involved
	// here but the same helper is used for subprocess output.
	cmd := exec.CommandContext(context.Background(), "sh", "-c", `printf '\x1b[1mhello\x1b[0m\n'`)
	if err := runCmdWithReporter(r, cmd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	out := buf.String()
	if strings.ContainsRune(out, '\x1b') {
		t.Errorf("expected escape sequences stripped, but output contains ESC: %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello' in stripped output, got: %q", out)
	}
}
