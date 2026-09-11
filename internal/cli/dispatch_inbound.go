package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/config"
)

// dispatchAcceptSessionMessages holds the tri-state --accept-session-messages
// value: nil when the flag was not given, otherwise a pointer to the explicit
// true or false (see triBoolValue in dispatch_keepalive.go). It is read in one
// place, runDispatch's step (9c), and nowhere on the launcher's or `niwa
// watch`'s path: the behavior reaches a worker only as a key in the launch
// settings map runDispatch hands to dispatchLaunch.
var dispatchAcceptSessionMessages *bool

// acceptSessionMessagesFlagName and acceptSessionMessagesFlagUsage register the
// flag. The usage text is the design's wording and a test pins it, so a change
// to it is a change to the command's documented interface.
const (
	acceptSessionMessagesFlagName  = "accept-session-messages"
	acceptSessionMessagesFlagUsage = "accept messages from other Claude Code sessions without an approval prompt; overrides the [global] accept_session_messages_on_dispatch machine setting in either direction"
)

// inboundGuideURL is the guide the audit line points at, in the blob/main form
// niwa already uses for guide links (see internal/workspace/scaffold.go). The
// path is the design's and is fixed ahead of the guide itself, so keep it as is
// rather than repointing it at an existing page. It is package-level so any
// other message about this behavior can point at the same page.
const inboundGuideURL = "https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md"

// crossSessionInboundAccept is the value config.CrossSessionInboundKey takes in
// the launch settings document when the behavior is on. Off is expressed by
// leaving the key out, never by another value, so the worker keeps whatever
// the agent's own default is.
const crossSessionInboundAccept = "accept"

// The two values inboundResolution.source takes: which input decided.
const (
	inboundSourceFlag    = "--accept-session-messages"
	inboundSourceMachine = "machine setting"
)

// The stderr lines this behavior prints. Three of the four are whole lines,
// each including its "niwa dispatch: " prefix and without a trailing newline,
// which the caller adds; inboundMachineSourceDetail is a fragment spliced into
// the first. The wording is the design's and TestInboundLinesExactText pins it,
// so a rewording is a change to the command's output, not a refactor.
const (
	// inboundAuditFormat is printed once the session mapping is durable, when
	// the behavior took effect. The first verb is the source detail
	// (inboundSourceFlag, or inboundMachineSourceDetail), the second the guide.
	inboundAuditFormat = "niwa dispatch: this worker accepts messages from other sessions without asking (source: %s); see %s"

	// inboundMachineSourceDetail names the machine setting in the audit line,
	// so a developer who did not pass the flag can find what turned it on.
	inboundMachineSourceDetail = "machine setting accept_session_messages_on_dispatch"

	// inboundOverrideLine is printed at the audit line's place when
	// --accept-session-messages=false turned off a machine setting that would
	// have applied.
	inboundOverrideLine = "niwa dispatch: this worker keeps Claude Code's default for messages from other sessions (--accept-session-messages=false overrides the machine setting)"

	// inboundUndeliverableFormat is printed at step (9c) when the flag asked
	// for the behavior and the dispatched agent cannot receive it. The verbs
	// are the agent's name and its declaration's reason.
	inboundUndeliverableFormat = "niwa dispatch: --accept-session-messages does not apply to the %q agent and was ignored. %s"
)

// inboundResolution is what the flag and the machine setting decide, before
// anything asks whether the dispatched agent can receive it.
type inboundResolution struct {
	// on reports that this dispatch asks for the behavior.
	on bool
	// source names the input that decided: inboundSourceFlag or
	// inboundSourceMachine. It is empty when neither was set, which is off.
	source string
	// overrodeMachineOn reports that --accept-session-messages=false turned
	// off a machine setting that was true.
	overrodeMachineOn bool
}

// resolveDispatchInboundAcceptance combines the tri-state flag with the
// machine setting. The flag wins whenever it is given, in either direction;
// otherwise a non-nil machine setting decides; otherwise the behavior is off.
//
// There is no downstream layer, unlike keep-alive and remote control: no
// workspace, instance, or repository settings source is consulted, because
// whether a worker takes messages from other sessions is the developer's call
// and not a cloned repository's. global is the zero-on-failure host settings,
// so an unreadable or malformed config.toml reads as an absent machine setting
// and the flag still applies.
func resolveDispatchInboundAcceptance(flag *bool, global config.GlobalSettings) inboundResolution {
	machine := global.AcceptSessionMessagesOnDispatch
	if flag != nil {
		return inboundResolution{
			on:                *flag,
			source:            inboundSourceFlag,
			overrodeMachineOn: !*flag && machine != nil && *machine,
		}
	}
	if machine != nil {
		return inboundResolution{on: *machine, source: inboundSourceMachine}
	}
	return inboundResolution{}
}

// inboundAuditLine renders the audit line for the input that turned the
// behavior on. source is inboundSourceFlag or inboundSourceMachine: runDispatch
// calls this only when inboundApplied is true, which requires the resolution to
// be on, and an on resolution always names its source. The machine setting is
// spelled out by its key so a developer who passed no flag can find what turned
// it on. Whether to print it is runDispatch's decision, made from
// inboundApplied alone.
func inboundAuditLine(source string) string {
	detail := inboundSourceFlag
	if source == inboundSourceMachine {
		detail = inboundMachineSourceDetail
	}
	return fmt.Sprintf(inboundAuditFormat, detail, inboundGuideURL)
}

// inboundNoticeMarker is the file whose presence means the explanation below
// has already been shown at a terminal. It lives beside config.toml -- in the
// directory part of the path config.GlobalConfigPath() returns, so it follows
// XDG_CONFIG_HOME -- and it is empty: the name is the whole record. Deleting it
// shows the explanation again; creating it by hand suppresses it.
//
// It is a file rather than a key in config.toml because niwa never rewrites a
// developer's configuration to record something it printed, and because the
// record is per configuration directory rather than per instance.
const inboundNoticeMarker = "accept-session-messages-notice"

// The one-time explanation, as three constants a reader can diff against the
// design and a guide can quote. The body is the same either way; which closing
// sentence follows it depends on whether niwa is about to remember having shown
// it, which is a terminal and a resolvable configuration directory together.
//
// The point of the whole paragraph is the asymmetry a developer would otherwise
// discover the hard way: this dispatch settles what the worker ACCEPTS, and
// says nothing about what happens when the worker speaks first into a session
// that was launched without the same setting.
const (
	// inboundExplanationBody is every sentence before the closing one,
	// including the "niwa dispatch: note: " prefix and ending in a period, so
	// a caller joins it to a closing sentence with a single space.
	//
	// It names an agent, its /config screen and its settings file, in a file
	// the dispatch-path layout scan covers. The scan compares whole literal
	// values, so a paragraph does not trip it -- and should not, because this
	// is advice to a reader about their OWN sessions, not a delivery decision:
	// nothing branches on it. It is honest only while that agent is the only
	// one declaring DispatchInboundAcceptance IMPLEMENTED (another declares it
	// unavailable), which TestInboundExplanationNamesTheOnlyImplementedAgent
	// holds still. A second implemented agent needs the wording generalized.
	inboundExplanationBody = `niwa dispatch: note: accepting messages without asking is inbound only. A message this worker sends into a session launched without it, such as a coordinator dispatched earlier, one dispatched with the behavior off, or one another tool started, still waits for approval there when the two run in different permission modes; dispatching that session again with the behavior on clears it. Your own interactive Claude Code sessions are one such case, and they are governed by your Claude Code user settings, which niwa doesn't change. To accept there too, set "Messages from your other sessions" to accept in Claude Code's /config, or add "crossSessionInbound": "accept" to ~/.claude/settings.json. That change applies to every Claude Code session you run and to messages from any session able to reach yours, on this machine or elsewhere.`

	// inboundExplanationTerminalClose closes the line when stderr is a
	// terminal, which is also when niwa tries to write the marker. It promises
	// silence niwa may not manage to deliver -- a write that fails leaves the
	// paragraph showing again -- which is the safe direction for the promise
	// to be wrong in. Its verb is the guide URL.
	inboundExplanationTerminalClose = "niwa won't show this again; it's also at %s"

	// inboundExplanationNonTerminalClose closes the line when stderr is not a
	// terminal, or when the configuration directory cannot be resolved. In
	// both cases nothing is remembered, and saying so is more honest than
	// promising silence niwa cannot deliver. Its verb is the guide URL.
	inboundExplanationNonTerminalClose = "niwa will show this again until it's been shown at a terminal; it's also at %s"
)

// inboundExplanationLine renders the whole explanation as one line, without its
// trailing newline. terminal picks the closing sentence.
func inboundExplanationLine(terminal bool) string {
	closing := inboundExplanationNonTerminalClose
	if terminal {
		closing = inboundExplanationTerminalClose
	}
	return inboundExplanationBody + " " + fmt.Sprintf(closing, inboundGuideURL)
}

// showInboundExplanation prints the one-time explanation to w when the marker
// in dir is absent, and then, only if isTTY reports a terminal, remembers it by
// creating the marker. It is the durable half of this feature as much as the
// printed one, so a caller cannot treat it as output alone.
//
// isTTY must report on w, not on some other stream. The whole correctness of
// the marker rests on that: a true answer is what authorizes writing down "the
// developer has seen this", and a caller that passed a log file or a captured
// buffer alongside the real terminal check would burn the one-time notice on
// output nobody read.
//
// It returns nothing, deliberately. Every call site runs after the dispatch has
// already succeeded and its mapping is durable, so there is no failure here
// worth turning into a non-zero exit: a marker niwa could not write costs the
// developer one repeated paragraph on the next dispatch, which is the safe
// direction to err in.
//
// dir is the directory holding config.toml and dirErr is the error from
// resolving it. Either a non-nil dirErr or an empty dir means there is no
// directory to remember anything in, so the explanation prints with its
// non-terminal closing sentence and isTTY is never consulted -- asking would
// only produce a promise niwa cannot keep. An empty dir is folded in here
// rather than left to the caller because a relative marker path would make the
// presence check below resolve against the process working directory, where an
// unrelated file of that name would silence the notice for good.
//
// Presence is os.Lstat rather than os.Stat: a dangling symlink at the marker
// path counts as present, which is what the exclusive create below would find
// anyway, and a symlink is never followed or written through. Any error at all,
// including a permission error on an unsearchable directory, counts as absent,
// so the explanation shows rather than being swallowed by a directory niwa
// cannot read.
func showInboundExplanation(w io.Writer, dir string, dirErr error, isTTY func() bool) {
	if dirErr != nil || dir == "" {
		fmt.Fprintln(w, inboundExplanationLine(false))
		return
	}

	marker := filepath.Join(dir, inboundNoticeMarker)
	if _, err := os.Lstat(marker); err == nil {
		return
	}

	terminal := isTTY()
	fmt.Fprintln(w, inboundExplanationLine(terminal))
	if !terminal {
		// Nobody is reading this stream, so it does not count as shown.
		return
	}

	// 0o755 is the mode the config writer already uses for this same
	// directory, so a marker written before the first `niwa config set` does
	// not leave a directory the writer would have made differently.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	// O_EXCL is what keeps this create from following a symlink planted at the
	// marker path between the Lstat above and this open. The Lstat comment's
	// "a symlink is never followed or written through" rests on it: without it
	// the create would resolve through the link and create or open its target
	// instead, at a path niwa did not choose. The
	// concurrent case needs no flag to come out right -- nothing is written,
	// so parallel creates of an empty file agree either way -- but with O_EXCL
	// exactly one caller creates it and the rest get an "exists" error,
	// ignored along with every other error for the reason above. The file
	// stays empty: its name is the record.
	f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_ = f.Close()
}

// showInboundExplanationBesideConfig is the production call, used at both of
// runDispatch's sites: it resolves the directory holding config.toml and pairs
// it with IsStderrTTY, the check that reports on the stream runDispatch passes
// as w. w must be that stream; see showInboundExplanation for what goes wrong
// otherwise.
//
// config.GlobalConfigPath() is the source, not config.GlobalConfigDir(): the
// latter returns the overlay clone directory, which a [global_config] clone
// owns and can replace wholesale, and a notice remembered there would be
// forgotten by the next clone.
func showInboundExplanationBesideConfig(w io.Writer) {
	path, err := config.GlobalConfigPath()
	dir := ""
	if err == nil {
		dir = filepath.Dir(path)
	}
	showInboundExplanation(w, dir, err, IsStderrTTY)
}
