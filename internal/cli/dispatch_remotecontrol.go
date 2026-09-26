package cli

import (
	"fmt"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// remoteControlSettingsJSON is the launch settings document with remote
// control's key alone: the exact bytes niwa passes via `claude --settings` to
// start a dispatched worker with the Remote Control bridge on, when no other
// contributor adds a key. Dispatch no longer appends it directly -- step (9c)
// of runDispatch renders one map with renderLaunchSettings -- and this value
// stays as the pinned rendering that output is checked against byte for byte.
// It is built from config.RemoteControlAtStartupKey, never from user input, so
// this pinned value, the key step (9c) adds, the materializer, and the
// read-back share one spelling.
var remoteControlSettingsJSON = fmt.Sprintf("{%q:true}", config.RemoteControlAtStartupKey)

// apiKeyForcedWarning is the one-line reason printed when the host wants
// remote-control on a dispatched worker but ANTHROPIC_API_KEY is set, which
// forces API-key auth and definitively precludes Claude Code Remote.
const apiKeyForcedWarning = "remote-control on dispatch is enabled, but ANTHROPIC_API_KEY is set, which forces API-key auth; Claude Code Remote requires a claude.ai login, so the worker will start without remote-control"

// resolveDispatchRemoteControl decides whether `niwa dispatch` should add the
// Claude Code Remote key to a worker's launch settings, and returns a one-line warning
// when the host default wants remote-control on but the launch environment
// definitively precludes it.
//
// The host preference (global.RemoteControlOnDispatch) is a DEFAULT-FILL, never a
// forced override: niwa injects only when the host default is on AND the
// downstream config left remoteControlAtStartup unset. When a downstream
// [claude.settings] decided the value (true or false), the worker honors it via
// its own materialized settings.json, so niwa injects nothing -- this is what
// keeps a downstream "off" winning even though `claude --settings` outranks the
// project settings.json (spike Variant C).
//
// inst may be nil (settings unreadable); that is treated as "downstream unset".
func resolveDispatchRemoteControl(global config.GlobalSettings, inst *instanceSettings, env []string) (inject bool, warning string) {
	if global.RemoteControlOnDispatch == nil || !*global.RemoteControlOnDispatch {
		// Host default off or unset: never inject, exactly today's behavior.
		return false, ""
	}
	if inst != nil && inst.RemoteControlAtStartup != nil {
		// Downstream config decided; the worker honors it directly. No inject.
		return false, ""
	}
	if apiKeyAuthForced(env) {
		return false, apiKeyForcedWarning
	}
	return true, ""
}

// apiKeyAuthForced reports whether the launch environment carries a non-empty
// ANTHROPIC_API_KEY, which forces Claude Code into API-key auth and rules out
// Claude Code Remote (a first-party claude.ai login is required). env is the
// list of "KEY=VALUE" entries the worker will inherit (os.Environ() shape).
func apiKeyAuthForced(env []string) bool {
	for _, e := range env {
		v, ok := strings.CutPrefix(e, "ANTHROPIC_API_KEY=")
		if ok && v != "" {
			return true
		}
	}
	return false
}
