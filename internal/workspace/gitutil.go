package workspace

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
)

// csiPattern matches ANSI/VT100 CSI escape sequences: ESC [ ... letter.
var csiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

// oscPattern matches OSC (Operating System Command) escape sequences: ESC ] ... BEL.
var oscPattern = regexp.MustCompile(`\x1b\][^\x07]*\x07`)

// stripEscapes removes ANSI CSI and OSC escape sequences from s.
func stripEscapes(s string) string {
	s = csiPattern.ReplaceAllString(s, "")
	s = oscPattern.ReplaceAllString(s, "")
	return s
}

// isGitErrorLine reports whether line is a git diagnostic that warrants a
// warning. It trims leading whitespace before checking the prefix so
// indented diagnostic lines are also matched.
func isGitErrorLine(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(trimmed, "fatal:") ||
		strings.HasPrefix(trimmed, "error:") ||
		strings.HasPrefix(trimmed, "warning:")
}

// runGitWithReporter runs cmd and routes its combined stdout+stderr through r.
// Lines that begin with a git diagnostic prefix ("fatal:", "error:",
// "warning:") are routed through r.Warn; all other lines are discarded.
// Discarding non-diagnostic lines keeps output clean: niwa emits its own
// curated completion messages ("cloned X", "synced X") so git's internal
// progress lines ("Cloning into '...'", "Already up to date.") are noise.
// ANSI and OSC escape sequences are stripped unconditionally before routing.
//
// When cmd.Run() fails and at least one error-classified line was captured,
// the returned error embeds those lines instead of the generic "exit status N"
// message.
//
// Goroutine lifecycle:
//   - defer pw.Close() is placed immediately after io.Pipe() so the write
//     end is closed even on panic or early return.
//   - pr.Close() is called inside the goroutine after the scanner loop exits
//     to prevent the git process from blocking on a write if the scanner
//     exits early (e.g., due to a token-too-long condition).
func runGitWithReporter(r *Reporter, cmd *exec.Cmd) error {
	pr, pw := io.Pipe()
	defer pw.Close()

	cmd.Stdout = pw
	cmd.Stderr = pw

	var errorLines []string
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := stripEscapes(scanner.Text())
			if isGitErrorLine(line) {
				r.Warn("%s", line)
				errorLines = append(errorLines, line)
			}
			// non-diagnostic git lines (e.g. "Cloning into '...'") are discarded;
			// niwa emits its own completion messages.
		}
		pr.Close()
	}()

	runErr := cmd.Run()
	pw.Close()
	<-done

	if runErr != nil && len(errorLines) > 0 {
		return fmt.Errorf("%w\n%s", runErr, strings.Join(errorLines, "\n"))
	}
	return runErr
}

// runCmdWithReporter runs cmd and routes its combined stdout+stderr through
// r.Status. ANSI and OSC escape sequences are stripped unconditionally.
// Unlike runGitWithReporter there is no line classifier — all output is
// treated as transient progress (used for setup scripts whose output format is
// not predictable). On non-TTY output Status is a no-op, so script output is
// silent in piped/CI contexts.
func runCmdWithReporter(r *Reporter, cmd *exec.Cmd) error {
	pr, pw := io.Pipe()
	defer pw.Close()

	cmd.Stdout = pw
	cmd.Stderr = pw

	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			line := stripEscapes(scanner.Text())
			r.Status(line)
		}
		pr.Close()
	}()

	runErr := cmd.Run()
	pw.Close()
	<-done

	return runErr
}
