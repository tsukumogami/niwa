package cli

import (
	"strconv"

	"github.com/tsukumogami/niwa/internal/config"
)

// keepAliveArmingInstruction is the fixed, niwa-authored self-arm nudge
// prepended to the dispatched task prompt when keep-alive is on for a
// remote-control worker (DESIGN-niwa-session-keep-alive, Decision B).
//
// Channel note: the design prefers the SessionStart additionalContext channel
// (B1) with the task-prompt prepend (B2) as fallback, and leaves the choice to
// an implementation-time confirmation. B1 is not viable for a dispatched
// worker: the `niwa instance from-hook` SessionStart entry is materialized
// only into the WORKSPACE-ROOT .claude/settings.json (root_materializer.go),
// which a `claude --bg` worker rooted in the instance directory does not
// load -- and that hook's guard deliberately no-ops for a cwd inside an
// instance (the re-entrancy check in instance_from_hook.go). The prompt
// prepend is the one channel niwa controls end to end for a dispatched
// worker, so B2 is the shipped channel.
//
// The instruction is a fixed constant with no untrusted input, and it rides
// the SAME single argv element as the task prompt (buildClaudeBgArgs), so it
// preserves the no-shell-interpolation guard (DESIGN-instance-dispatch
// Decision 8): nothing in it can smuggle a claude flag. It directs the agent
// to create exactly ONE session-scoped, sub-hourly, non-visible no-op
// self-wake and to no-op when one is already present (idempotent arming).
// The 30-minute interval is the design's fixed cadence: well under the ~1h
// idle supervisor stop, with margin for scheduling jitter, and it is not
// user-configurable.
const keepAliveArmingInstruction = `Keep-alive (from niwa): before starting the task below, arm this session's keep-alive. Schedule exactly one recurring, session-scoped self-wake that fires every 30 minutes and whose action is a strict no-op: on each wake do nothing -- run no tools, change no files or state, and add no visible output to the conversation; keep the wake non-visible and out of the main context. If this session already has a keep-alive self-wake scheduled, do not create another. Its only purpose is to keep the session's remote connection from idling out; it must never do task work. Arm it once, do not mention it again, and then proceed with the task.

`

// keepAliveNonRCWarning is the one-line reason printed when keep-alive was
// requested but the worker is not starting with the Remote Control bridge.
// Keep-alive exists to keep an RC bridge from idling out, so without RC there
// is nothing to keep alive; the request degrades to a warning plus no arming,
// and the dispatch still launches (a warning, never an error).
const keepAliveNonRCWarning = "keep-alive was requested, but this worker is not starting with remote control; keep-alive only applies to remote-control sessions, so the worker will start without it"

// triBoolValue adapts a **bool target into a pflag Value so a boolean flag
// can distinguish unset (nil) from an explicit true/false. The keep-alive
// flag needs that tri-state -- it must override the host default in BOTH
// directions, so "not given" and "explicitly false" cannot collapse into one
// value. No existing flag in this codebase carries the pattern (--model uses
// a string empty-check and remote-control has no per-dispatch flag), so this
// is the net-new mechanism the design calls out.
//
// Registration pairs it with NoOptDefVal = "true" so a bare `--keep-alive`
// means explicit true while `--keep-alive=false` is an explicit off.
type triBoolValue struct {
	target **bool
}

func (v triBoolValue) String() string {
	if v.target == nil || *v.target == nil {
		return ""
	}
	return strconv.FormatBool(**v.target)
}

func (v triBoolValue) Set(s string) error {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	*v.target = &b
	return nil
}

func (v triBoolValue) Type() string { return "bool" }

// resolveDispatchKeepAlive decides whether this dispatch opts into keep-alive.
// Precedence is flag > downstream > host-default, default off:
//
//   - flag is the tri-state --keep-alive value; when given it wins in BOTH
//     directions (force-on when the host default is off, force-off when on).
//   - The downstream layer is the instance's materialized [claude.settings]
//     keepAliveOnDispatch value; like the remote-control resolver, a decided
//     downstream value is respected and the host default never overrides it.
//   - global.KeepAliveOnDispatch is the host-level default-fill; nil or false
//     means today's behavior (off).
//
// Unlike resolveDispatchRemoteControl this returns the resolved opt-in itself,
// not an injection decision: a downstream "on" still needs niwa to act (the
// arming is a launch-time prompt prepend; there is no settings key the worker
// could honor by itself). The RC gate (remoteControlEnabled) is applied by the
// caller, not here -- resolution and eligibility are separate questions.
//
// inst may be nil (settings unreadable); that is treated as "downstream unset".
func resolveDispatchKeepAlive(flag *bool, global config.GlobalSettings, inst *instanceSettings) bool {
	if flag != nil {
		return *flag
	}
	if inst != nil && inst.KeepAliveOnDispatch != nil {
		return *inst.KeepAliveOnDispatch
	}
	if global.KeepAliveOnDispatch != nil {
		return *global.KeepAliveOnDispatch
	}
	return false
}

// remoteControlEnabled reports whether the dispatched worker will start with
// the Remote Control bridge on: either niwa injected the RC settings flag for
// this dispatch (rcInjected), or the instance's own materialized
// settings.json set remoteControlAtStartup true (the downstream opt-in the
// worker honors by itself, which is exactly the case where the RC resolver
// injects nothing). Keep-alive arms only when this holds -- the self-wake
// exists to keep an RC bridge warm, so a worker without RC has nothing to
// keep alive.
func remoteControlEnabled(rcInjected bool, inst *instanceSettings) bool {
	if rcInjected {
		return true
	}
	return inst != nil && inst.RemoteControlAtStartup != nil && *inst.RemoteControlAtStartup
}
