// Package cli, file prompt.go: stdin-TTY detection and typed-confirmation
// reader. Establishes niwa's first interactive-prompt primitives. Used
// by the reworked destroy command, but kept generic so future commands
// (e.g., a hypothetical irreversible operation) can reuse them.
package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// IsStdinTTY reports whether stdin is connected to a terminal. Used to
// gate interactive surfaces (the picker, typed-confirmation prompts)
// so they don't try to render or read user input from a pipe or
// CI environment.
//
// Exposed as a variable so tests can stub the result without touching
// real stdin. Default implementation reads term.IsTerminal at call
// time; do not capture at init() time because a test may rebind
// os.Stdin between init and the first call.
var IsStdinTTY = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// ReadConfirmation writes prompt to out, reads a single line from in,
// trims surrounding whitespace, and reports whether the result equals
// expected. EOF or read error returns (false, err).
//
// The function does NOT echo a newline after writing the prompt — the
// terminal is responsible for echoing the user's input including the
// trailing Enter. Callers may include the trailing whitespace they
// want in the prompt string itself (e.g., end with a colon and a
// space).
//
// Mismatch returns (false, nil), not an error: the caller decides
// whether mismatch is a hard error or a retry.
func ReadConfirmation(prompt, expected string, in io.Reader, out io.Writer) (bool, error) {
	if _, err := fmt.Fprint(out, prompt); err != nil {
		return false, fmt.Errorf("writing prompt: %w", err)
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil {
		// Treat EOF on a non-empty line as the line being available;
		// only a truly empty + EOF should propagate the error.
		if err == io.EOF && line != "" {
			return strings.TrimSpace(line) == expected, nil
		}
		return false, fmt.Errorf("reading confirmation: %w", err)
	}
	return strings.TrimSpace(line) == expected, nil
}
