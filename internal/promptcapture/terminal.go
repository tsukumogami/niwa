package promptcapture

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

// IsAvailable reports whether an interactive capture can run: the capture reads
// standard input and renders to standard error, so both must be terminals.
//
// Standard output is deliberately not consulted. It carries the command's own
// session hints, which a caller may redirect without giving up the capture.
func IsAvailable() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
}

// stdin and stderr are indirected so tests never touch the real descriptors.
var (
	stdin  = func() io.Reader { return os.Stdin }
	stderr = func() io.Writer { return os.Stderr }
)

const (
	enableBracketedPaste  = "\x1b[?2004h"
	disableBracketedPaste = "\x1b[?2004l"
)

// enterRaw puts the terminal into raw mode, enables bracketed paste, and
// installs the handlers that restore both on the paths a deferred call cannot
// reach. The returned function restores everything and removes the handlers; it
// is safe to call more than once.
//
// Raw mode is applied to the standard input descriptor. The picker in
// internal/tui applies it to standard error instead, which works only because
// the two usually name the same device; a reader should be deliberate about
// which descriptor it is changing.
//
// Clearing ISIG (which term.MakeRaw does) is what lets a pasted 0x03, 0x1c or
// 0x1a be captured as payload instead of killing, dumping core from, or
// suspending the process mid-capture.
func enterRaw() (func(), error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, errors.New("promptcapture: standard input is not a terminal")
	}

	saved, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("promptcapture: entering raw mode: %w", err)
	}
	fmt.Fprint(os.Stderr, enableBracketedPaste)

	ch := make(chan os.Signal, 1)
	// SIGQUIT is in the set for terminal restoration, like the rest.
	//
	// An earlier comment here justified it on the grounds that its default
	// action dumps core carrying the captured prompt. That is not what happens
	// to a Go binary: the runtime installs its own SIGQUIT handler, prints
	// goroutine stacks to stderr, and exits with status 2. A core appears only
	// under GOTRACEBACK=crash. What the default disposition actually leaks is
	// those stacks onto the developer's terminal -- smaller, and still a reason
	// to handle it, just not the reason previously given.
	signal.Notify(ch, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM,
		syscall.SIGHUP, syscall.SIGTSTP, syscall.SIGCONT)

	done := make(chan struct{})
	restored := false

	restore := func() {
		if restored {
			return
		}
		restored = true
		close(done)
		signal.Stop(ch)
		fmt.Fprint(os.Stderr, disableBracketedPaste)
		_ = term.Restore(fd, saved)
	}

	go func() {
		for {
			select {
			case <-done:
				return
			case sig := <-ch:
				switch sig {
				case syscall.SIGTSTP:
					// Resetting SIGTSTP to its default disposition does not
					// reliably suspend the process, so raise SIGSTOP directly
					// and leave re-entry to the SIGCONT branch.
					fmt.Fprint(os.Stderr, disableBracketedPaste)
					_ = term.Restore(fd, saved)
					_ = syscall.Kill(os.Getpid(), syscall.SIGSTOP)
				case syscall.SIGCONT:
					// Only re-enter raw mode if we are the foreground process
					// group. A capture resumed in the background must not stamp
					// raw settings onto the terminal the shell is using.
					if isForeground(fd) {
						if _, err := term.MakeRaw(fd); err == nil {
							fmt.Fprint(os.Stderr, enableBracketedPaste)
						}
					}
				default:
					restore()
					signal.Reset(sig)
					_ = syscall.Kill(os.Getpid(), sig.(syscall.Signal))
					return
				}
			}
		}
	}()

	return restore, nil
}

// isForeground reports whether this process group owns the terminal.
func isForeground(fd int) bool {
	pgrp, err := unixTcgetpgrp(fd)
	if err != nil {
		return false
	}
	return pgrp == syscall.Getpgrp()
}
