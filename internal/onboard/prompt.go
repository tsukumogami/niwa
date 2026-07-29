// Package onboard implements the `niwa onboard` wizard engine: setup
// and topology detection, the interactive prompt kit, the api_url
// entry gate, and (in later issues) the team/individual step runners,
// verification, and exit-code construction.
package onboard

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

// IsStdinTTY reports whether stdin is connected to a terminal.
//
// This duplicates internal/cli's IsStdinTTY rather than importing it:
// the command layer (a later issue) imports this package to wire the
// wizard into `internal/cli/onboard.go`, so importing the other
// direction here would create a cycle. Exposed as a var, like its
// internal/cli counterpart, so tests can stub the result without a
// real TTY.
var IsStdinTTY = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// ErrNonInteractive is returned by Gate when stdin is not a terminal
// and no override was supplied. This is a typed condition, not a
// plain error string, so a caller can map it to an exit code via
// errors.Is rather than string-matching.
var ErrNonInteractive = errors.New("onboard: stdin is not a terminal and no override was supplied")

// Gate is a generic TTY-or-override primitive: given a single "do I
// already have enough non-interactive input to proceed" bool, it
// re-derives IsStdinTTY() itself and fails closed when neither holds.
//
// wizard.go's Run does NOT call Gate -- AC-30's actual non-interactive
// precondition is more specific than a single bool (a missing setup
// override always fails, but a missing topology override only matters
// once the setup is or would resolve to individual), so Run implements
// that rule directly via checkNonInteractivePrecondition, which takes
// the already-decided interactive bool as a parameter instead of
// re-deriving it -- re-deriving here would call IsStdinTTY() a second
// time per invocation, splitting the "single TTY gate at entry"
// Decision 3 calls for across two independent checks. Gate remains
// available for any future caller whose override condition really is
// a single bool.
func Gate(override bool) error {
	if override || IsStdinTTY() {
		return nil
	}
	return ErrNonInteractive
}

// Option is one selectable choice for Select. Label is what's
// displayed (sanitized before display); Value is what's returned when
// the operator picks it.
type Option struct {
	Label string
	Value string
}

// ConfirmFunc is the prompt-kit hook the api_url gate
// (CheckAPIURL) and the detection funnel (ConfirmSetup,
// ConfirmTopology) use to ask for explicit acknowledgment. Bound to
// this package's own Confirm over real stdin/stdout in production;
// tests and the non-interactive path substitute their own.
type ConfirmFunc func(prompt string, defaultYes bool) (bool, error)

// readLine writes prompt to out, then reads one line from br. This is
// the one shared step every prompt-kit primitive's re-prompt loop
// calls, generalized from promptBootstrap's and ReadConfirmation's EOF
// handling (internal/cli/init.go, internal/cli/prompt.go): EOF on a
// non-empty buffered line is a valid final answer -- the terminal
// closed right after Enter -- while EOF on empty input propagates as
// an error. Ownership of what the line MEANS (yes/no, a numbered
// choice, any content at all) stays with the caller.
//
// br must be constructed once by the caller and reused across
// re-prompt iterations within a single primitive call -- a fresh
// bufio.Reader per iteration would silently discard whatever the
// previous instance had already buffered from in.
func readLine(prompt string, br *bufio.Reader, out io.Writer) (string, error) {
	if _, err := fmt.Fprint(out, prompt); err != nil {
		return "", fmt.Errorf("writing prompt: %w", err)
	}
	line, err := br.ReadString('\n')
	if err != nil {
		if err == io.EOF && line != "" {
			return strings.TrimSpace(line), nil
		}
		return "", fmt.Errorf("reading input: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// Confirm asks a yes/no question with a stated default, generalizing
// promptBootstrap's Y/n-with-default-on-Enter loop. Any input other
// than a recognized yes/no answer re-prompts on the same writer.
//
// prompt passes through Sanitize before display, so a config- or
// response-sourced value interpolated into it by the caller can't
// smuggle control bytes or ANSI sequences onto the terminal.
func Confirm(prompt string, defaultYes bool, in io.Reader, out io.Writer) (bool, error) {
	suffix := " [Y/n] "
	if !defaultYes {
		suffix = " [y/N] "
	}
	full := Sanitize(prompt) + suffix

	br := bufio.NewReader(in)
	for {
		line, err := readLine(full, br, out)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(line) {
		case "":
			return defaultYes, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		}
		// Anything else re-prompts, sharing the same loop.
	}
}

// Select presents a numbered one-of-N choice, re-prompting on
// out-of-range or unparseable input. It returns the chosen option's
// Value.
//
// Every displayed label passes through Sanitize, same as Confirm.
func Select(prompt string, options []Option, in io.Reader, out io.Writer) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("onboard: Select requires at least one option")
	}

	var menu strings.Builder
	fmt.Fprintln(&menu, Sanitize(prompt))
	for i, opt := range options {
		fmt.Fprintf(&menu, "  %d) %s\n", i+1, Sanitize(opt.Label))
	}
	menu.WriteString("> ")

	br := bufio.NewReader(in)
	next := menu.String()
	for {
		line, err := readLine(next, br, out)
		if err != nil {
			return "", err
		}
		if n, convErr := strconv.Atoi(line); convErr == nil && n >= 1 && n <= len(options) {
			return options[n-1].Value, nil
		}
		next = fmt.Sprintf("Enter a number from 1 to %d: ", len(options))
	}
}

// Pause reads and discards one line, used only to gate on an external
// action (a dashboard step, a login switch). It validates nothing --
// any line, including an empty one, satisfies it. It still shares
// readLine's EOF handling: a closed stdin before any line arrives is
// an error, not a silent pass.
func Pause(prompt string, in io.Reader, out io.Writer) error {
	br := bufio.NewReader(in)
	_, err := readLine(Sanitize(prompt), br, out)
	return err
}
