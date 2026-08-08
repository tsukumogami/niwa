package workspace

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"

	"github.com/tsukumogami/niwa/internal/secret"
)

// csiPattern matches ANSI/VT100 CSI escape sequences: ESC [, parameter bytes
// (0x30-0x3F, which covers the private-use markers ? > < = as well as digits
// and ;), intermediate bytes (0x20-0x2F), and a final byte (0x40-0x7E).
var csiPattern = regexp.MustCompile(`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]`)

// stringEscapePattern matches the escape sequences that carry a string
// argument -- OSC, DCS, SOS, PM and APC -- each introduced by ESC plus one of
// ] P X ^ _ and terminated by BEL or ST (ESC \). An unterminated sequence runs
// to the end of the line, which is the safe reading: a terminal would swallow
// the rest of the line too.
var stringEscapePattern = regexp.MustCompile(`\x1b[\]P^_X][^\x07\x1b]*(?:\x07|\x1b\\)?`)

// escPattern matches whatever escape sequences remain after the two patterns
// above: two-character forms such as ESC c (RIS, a full terminal reset), an
// ESC carrying only intermediates, and a lone trailing ESC.
var escPattern = regexp.MustCompile(`\x1b[\x20-\x2f]*[\x30-\x7e]?`)

// controlPattern matches the C0 control bytes and DEL. Tab is deliberately
// excluded -- it is ordinary layout in subprocess output. ESC is in this range
// too, which is why control stripping runs last, after the escape patterns
// have had their chance to consume whole sequences.
var controlPattern = regexp.MustCompile("[\x00-\x08\x0a-\x1f\x7f]")

// stripEscapes removes escape sequences and control bytes from s, leaving text
// that renders as itself.
//
// This is a security control, not a cosmetic one. Both callers print
// repo-controlled bytes: an embedded carriage return overwrites the line
// already drawn, so a script could erase the `[<repo>/<script>]` prefix that
// distinguishes its output from niwa's own and forge a line such as
// `setup incomplete for 0 repos`. And an escape sequence interleaved inside a
// secret defeats the redactor's substring match while the terminal still
// renders the secret contiguously.
//
// Newlines need no special handling here: both callers scan line by line, so
// the splitter has already consumed them by the time this runs. They are
// stripped anyway, along with the rest of C0, since a line that reaches here
// carrying one is not a line the scanner produced.
func stripEscapes(s string) string {
	s = csiPattern.ReplaceAllString(s, "")
	s = stringEscapePattern.ReplaceAllString(s, "")
	s = escPattern.ReplaceAllString(s, "")
	s = controlPattern.ReplaceAllString(s, "")
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

// maxCmdLineLen is the largest single line runCmdWithReporter will read, 1 MB.
// bufio.Scanner's 64 KB default is far too small here: exceeding it ends the
// scan, which closes the pipe, SIGPIPEs the subprocess, and loses every line
// from that point on — so one long blob would turn into a broken pipe with no
// output at all, the exact failure this helper exists to prevent.
const maxCmdLineLen = 1 << 20

// runCmdWithReporter runs cmd and routes its combined stdout+stderr through
// r.Log, one permanent line per line of output, each carrying prefix. Escape
// sequences and control bytes are stripped and red (if non-nil) scrubs
// registered secrets before anything is printed.
//
// Unlike runGitWithReporter there is no line classifier: a setup script's
// output format is not predictable, so every line is passed through. Because
// the routing is Log rather than Status, output is durable in both run modes —
// visible under a spinner on a TTY and equally present in a piped or CI log.
// On a TTY the spinner is torn down once per command rather than once per
// line, since Log stops it and the next Status call restarts it.
func runCmdWithReporter(r *Reporter, cmd *exec.Cmd, prefix string, red *secret.Redactor) error {
	pr, pw := io.Pipe()
	defer pw.Close()

	cmd.Stdout = pw
	cmd.Stderr = pw

	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(pr)
		scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxCmdLineLen)
		for scanner.Scan() {
			// The order below is load-bearing: strip, then scrub, then
			// prefix. Stripping first is what stops an escape sequence
			// interleaved inside a secret from defeating the redactor —
			// the redactor matches plain substrings, so a colorized
			// `set -x` trace that splits a token with a color reset
			// would slip past a scrub applied to the raw line while the
			// terminal still renders the secret contiguously. Stripping
			// rejoins the fragment before the match runs. The prefix is
			// applied last because it is niwa's own text: feeding it
			// through the sanitizer or the redactor would be pointless
			// at best, and applying it before the strip would let a
			// leading carriage return in the script's own output erase
			// it.
			line := stripEscapes(scanner.Text())
			if red != nil {
				line = red.Scrub(line)
			}
			r.Log("%s%s", prefix, line)
		}
		if err := scanner.Err(); err != nil {
			// Surfaced rather than dropped: silently ending the scan is
			// how output goes missing without anyone noticing.
			r.Warn("%sreading output failed: %v", prefix, err)
		}
		pr.Close()
	}()

	runErr := cmd.Run()
	pw.Close()
	<-done

	return runErr
}
