package cli

import (
	"slices"
	"strings"
	"unicode"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/watch"
)

// This file is the whole of re-entry: every command niwa builds or prints to
// step back into a session it launched is assembled here and nowhere else.
//
// That is a rule with an enforcement rather than a convention. The launch path
// grants a worker the posture niwa intends for a directory it owns by
// overriding trust for that one process (LaunchSpec.WorkdirGrantArgs), and the
// grant dies with the process it was passed to. Every way back in starts a new
// process, so every way back in has to carry the grant again -- and a way back
// in that was added later and forgot to would fail silently, which is exactly
// how the defect this file fixes reached a release.
//
// So this file is the only non-test file in the package permitted to read
// ResumeArgs or HintVerbs, and the scan rule that lands with it in
// dispatch_layout_test.go fails any other that does. Both fields are covered,
// not just the first: the hint block is built
// from HintVerbs alone, so a rule naming only ResumeArgs would leave a whole
// surface able to emit a grantless command without tripping anything.
//
// The grant itself is still per invocation and deliberately so. niwa writes
// nothing into the developer's own Codex configuration here, exactly as it
// writes nothing there at launch: an override vouches for the one process it
// is handed to, where a persisted entry would vouch for anything anybody ever
// runs in that directory afterwards. Measured against codex-cli 0.149.0: a
// resumed session carrying the override records workspace-write, the same
// posture as the launch turn, and the isolated configuration directory it ran
// against was left without a configuration file at all.

// reentryArgs returns the argv, excluding the binary, that steps back into a
// dispatched session: the agent's own resume arguments, then the workdir grant
// formatted for the session's instance directory, then the handle.
//
// An agent that declares no way back in gets nil rather than a bare handle,
// and the check lives here rather than at a call site because ResumeArgs is a
// field only this file may read. An agent that declares no grant, or a session
// whose instance directory is not recorded, gets the argv it would have had
// before this file existed -- formatWorkdirGrant yields nothing in both cases,
// so the degraded shape is inherited rather than re-implemented, and a grant
// naming no directory is never built.
func reentryArgs(spec agentplan.LaunchSpec, handle, workdir string) []string {
	if len(spec.ResumeArgs) == 0 {
		return nil
	}
	args := slices.Clone(spec.ResumeArgs)
	args = append(args, formatWorkdirGrant(spec, workdir)...)
	return append(args, handle)
}

// shellSafeToken is the set of bytes a token may be built from and still be
// handed to a POSIX shell unquoted. It is an allowlist rather than a denylist
// on purpose: a denylist that misses one byte prints that byte unquoted, and
// the whole point of quoting here is that the grant's value carries braces,
// double quotes and equals signs.
//
// `~` and `!` are deliberately absent. A leading tilde is expanded by the
// shell, and `!` is history expansion in an interactive one -- neither is
// dangerous, both would silently hand the binary something other than what was
// printed.
const shellSafeToken = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_@%+=:,./-"

// shellToken renders one argv element for a command a person will paste into a
// POSIX shell. A token made only of shell-safe bytes passes through bare, so
// the common case stays readable; anything else is single-quoted, which stops
// word splitting, every expansion, and command separators alike.
//
// The empty string is quoted rather than passed through: bare, it would vanish
// from the command line instead of arriving as an empty argument.
func shellToken(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !strings.ContainsRune(shellSafeToken, r)
	}) < 0 {
		return s
	}
	// The standard escape: close the quoted run, emit a backslash-escaped
	// quote, reopen it.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// printableToken reports whether a token can appear in a command niwa prints
// for a person to read and paste.
//
// Quoting handles the shell; this handles the terminal. A token carrying a
// carriage return or an escape sequence redraws the line it is printed on, so
// what the developer reads is not what they would paste -- and single quotes do
// nothing about that, because the rewriting happens before the shell is
// involved at all.
//
// The answer is to refuse rather than to strip. The repository has a stripper
// for this threat already, but stripping produces a command that still looks
// runnable and no longer does what it says; refusing produces no command, which
// is a state every caller here already handles.
//
// Worth knowing which token this actually guards. The instance directory
// arrives already neutralised, but only because both declared grant verbs
// render it with a quoting format verb, which turns a control byte into the
// text of an escape rather than the escape itself. A future declaration that
// substituted the path raw would hand this gate the first token that could
// really redraw a line, and that is the case it is here for -- along with the
// binary name and the resume arguments, which reach the line unquoted.
func printableToken(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// reentryCommand returns the single command that steps back into a session,
// quoted for a POSIX shell -- or "" when niwa cannot render one it is sure
// works.
//
// It fails closed on four counts: an agent with no binary, an agent that
// declares no way back into a session, a handle that does not pass the safe-
// handle check, and any token that could not be printed faithfully. The handle
// check matters because the handle is the one part of the command that comes
// out of the agent's own record store rather than out of niwa: it is recorded
// unvalidated, and it is the token the command actually carries.
func reentryCommand(spec agentplan.LaunchSpec, handle, workdir string) string {
	if spec.Binary == "" || !watch.IsSafeHandle(handle) {
		return ""
	}
	args := reentryArgs(spec, handle, workdir)
	if len(args) == 0 {
		return ""
	}
	parts := append([]string{spec.Binary}, args...)
	rendered := make([]string, 0, len(parts))
	for _, p := range parts {
		if !printableToken(p) {
			return ""
		}
		rendered = append(rendered, shellToken(p))
	}
	return strings.Join(rendered, " ")
}

// reentryHints returns the block of management hints printed after a successful
// dispatch, one line per verb the agent declares.
//
// One of those verbs is the way back into the session, and that line is the
// whole command -- grant included -- so a developer who pastes it lands in the
// posture niwa launched the worker with. The others are printed as they always
// were: a verb that reads logs or stops a worker starts no session, so a grant
// on it would vouch for nothing and give the developer more to paste around.
//
// Which verb is which is read from the declaration rather than named here, so
// this function knows no agent. An agent whose resume line cannot be rendered
// gets no hints at all rather than a block missing its most useful line.
func reentryHints(spec agentplan.LaunchSpec, handle, workdir string) []string {
	if spec.Binary == "" || len(spec.HintVerbs) == 0 || len(spec.ResumeArgs) == 0 {
		return nil
	}
	if !watch.IsSafeHandle(handle) {
		return nil
	}
	resumeVerb := spec.ResumeArgs[0]
	lines := make([]string, 0, len(spec.HintVerbs))
	for _, verb := range spec.HintVerbs {
		line := spec.Binary + " " + verb + " " + handle
		if verb == resumeVerb {
			line = reentryCommand(spec, handle, workdir)
		}
		// Every line is printed, so every line passes the print gate -- not
		// just the one that happens to route through reentryCommand. A block
		// whose other verbs skipped the gate would be a hole that opened
		// whenever no hint verb matched the resume verb.
		if line == "" || !printableToken(line) {
			return nil
		}
		lines = append(lines, line)
	}
	return lines
}
