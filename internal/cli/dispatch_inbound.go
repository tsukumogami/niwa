package cli

import (
	"fmt"

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

// The stderr lines this behavior prints. Each is a whole line including its
// "niwa dispatch: " prefix and without a trailing newline, which the caller
// adds. The wording is the design's and TestInboundLinesExactText pins it, so a
// rewording is a change to the command's output, not a refactor.
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
