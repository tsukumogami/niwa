package watch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Matcher strings for the PreToolUse hooks. A review hook's identity is its
// matcher AND its exact command, under the PreToolUse event, with a handler that
// holds only "type" and "command" (see preToolUseHasHook). ApplyReviewSettings
// dedupes on that identity and VerifyReviewSettings requires it, so an entry that
// keeps niwa's matcher but runs something else, carries an extra handler key, or
// sits under another event never stands in for niwa's hook. Two hooks may share a
// matcher (the post-guard and the session-command deny both match Bash); their
// commands tell them apart.
//
// How Claude Code compares a matcher (read from the 2.1.267 and 2.1.268
// bundles): a matcher made only of letters, digits, underscores, and "|" is split
// on "|" into an exact list of tool names, with aliases resolved. For PreToolUse
// it also accepts commas, spaces, and hyphens there, splitting on "|" or "," and
// trimming each token. A matcher with any other character, such as ".", "*", or
// "(", is treated as a regular expression. Every matcher below is in the
// exact-list form, so each token names exactly one tool, never a prefix or a
// substring.
const (
	// egressDenyMatcher matches the out-of-sandbox egress channels WebFetch and
	// WebSearch, plus the "mcp__" token written to cover every MCP tool. These make
	// network calls OUTSIDE the OS sandbox (which cages only Bash subprocesses), so
	// in sandbox mode they must be denied by a hook that fires even under
	// bypassPermissions. Under the exact-list comparison above, "mcp__" names only a
	// tool literally called mcp__, so on current Claude Code this hook does not
	// cover MCP tools (named mcp__<server>__<tool>). The matcher is deliberately
	// left unchanged here; widening it is a separate review-containment change.
	egressDenyMatcher = "WebFetch|WebSearch|mcp__"
	// fsGuardMatcher matches the built-in file-writing tools. Like the egress
	// channels these run OUTSIDE the OS sandbox (through the permission system,
	// which a dispatched session's bypassPermissions skips), so in sandbox mode a
	// write that resolves outside the instance must be denied by a hook. This is the
	// filesystem-escape counterpart to egressDenyMatcher. MultiEdit must be listed
	// alongside Edit: under exact-list comparison "Edit" does not cover "MultiEdit".
	fsGuardMatcher = "Write|Edit|MultiEdit|NotebookEdit"
	// postGuardMatcher matches Bash so the post-guard can inspect gh commands and
	// refuse a review/comment post. Applied in every mode (accident prevention).
	postGuardMatcher = "Bash"
	// autoAllowMatcher matches the normal review tools. In the operator-approval
	// posture the session runs under a non-bypass permission mode, so these tools --
	// which would otherwise prompt and hang a --bg session -- need an explicit allow
	// decision. Bash egress stays caged by the OS sandbox and posting stays blocked by
	// the post-guard (an explicit deny overrides this allow), so auto-allowing here does
	// not widen the boundary; it restores the autonomy bypassPermissions gave for free.
	// It shares the Bash channel with the post-guard as a distinct matcher entry.
	autoAllowMatcher = "Bash|Read|Glob|Grep"
	// sessionReachDenyMatcher matches the Claude Code tools that reach other
	// sessions: SendMessage (messages to another session), SendFile (files onto
	// another session's filesystem), RemoteTrigger (remote routines, whose
	// deliveries count as cross-session inbound), and ListAgents (session
	// discovery). The list comes from Claude Code 2.1.267's tool list. The matcher
	// uses only letters and "|", so Claude Code compares it as an exact list of
	// tool names, aliases included (ListPeers resolves to ListAgents), rather than
	// as a substring or a regex. Keep it to letters and "|": a character such as
	// ".", "*", or "(" would make Claude Code treat it as a regular expression
	// instead (see the comparison rule above).
	//
	// sessionReachDenyHook is applied and required in every containment mode, and
	// its identity is this matcher AND its command, so a workspace or overlay hook
	// that reuses the matcher with a different command can't stand in for niwa's.
	// The hook has limits:
	//
	//   - With watch_sandbox = off, Bash has full access, so the hook is accident
	//     prevention only, like the post-guard.
	//   - In sandbox mode it relies on noEgressSandboxStanza keeping unix sockets
	//     disallowed, which is what keeps Bash off Claude Code's local
	//     cross-session inbox.
	//   - Hooks load when a session starts, so it applies only after watch
	//     re-stages or resumes a review; a review staged before the hook existed
	//     runs without it until then.
	//   - A disableAllHooks setting turns it off, along with every other review
	//     hook. ApplyReviewSettings refuses one in the instance's settings.json or
	//     settings.local.json; one in a settings source outside the instance (the
	//     user's own, say) isn't checked.
	sessionReachDenyMatcher = "SendMessage|SendFile|RemoteTrigger|ListAgents"
	// sessionCommandDenyMatcher matches Bash so the session-command deny can refuse
	// a review Bash command that runs niwa's or Claude Code's session commands
	// (see sessionCommandDenyHook). It shares the matcher with the post-guard as a
	// distinct entry; the two are told apart by their commands.
	sessionCommandDenyMatcher = "Bash"
)

// sessionReachDenyMessage is the refusal line the session-reach deny hook writes
// to stderr, which Claude Code shows the session when the hook blocks a tool call.
// Keep it stable: tests and docs quote it verbatim.
const sessionReachDenyMessage = "niwa watch: review sessions don't reach other sessions"

// sessionCommandDenyMessage is the refusal line the session-command deny writes to
// stderr when a review Bash command names niwa's or Claude Code's session commands
// (niwa retask, resume or dispatch; claude --resume, -r, attach, stop or rm).
// Keep it stable: tests and docs quote it verbatim.
//
// The deny behind it is a string match on the command text. A script, a variable
// or quoting tricks get around it, so it is a second line that catches the plain
// cases, never a boundary. What keeps a sandboxed review's Bash off the Claude Code
// daemon is the OS sandbox denying all network, unix sockets included (see
// noEgressSandboxStanza).
const sessionCommandDenyMessage = "niwa watch: review sessions don't run session commands"

// guardBinPath returns the absolute path to the niwa binary the filesystem-guard
// and session-command deny hooks invoke (as `<niwa> watch guard-fs` and `<niwa>
// watch guard-bash`). In production it is the running executable; tests override
// it to point at a built binary. If the executable path cannot be determined it
// falls back to "niwa" (resolved on PATH) -- and the hook wrapper fails closed
// (deny) if even that is not found, so a bad path can never silently allow a call.
var guardBinPath = func() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "niwa"
}

// shellQuote single-quotes s for safe embedding in the hook command string.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// noEgressSandboxStanza is the no-egress OS sandbox profile merged into a
// dispatched instance's .claude/settings.json when sandbox mode is on. An EMPTY
// allowedDomains is the deny-all posture (the live adversarial test proves the
// harness honors it as deny-all, not allow-all). failIfUnavailable makes the
// harness REFUSE to run rather than silently disabling the sandbox, and
// allowUnsandboxedCommands=false removes the unsandboxed escape hatch; together
// they close the harness fail-open so a silent degradation cannot quietly drop
// the sandbox once niwa has decided to enforce it.
//
// The network block must keep unix sockets disallowed: it grants no unix-socket
// allowance of any kind. That is what keeps Bash in a sandboxed review off Claude
// Code's local cross-session inbox, the route to other sessions that
// sessionReachDenyHook does not cover.
func noEgressSandboxStanza() map[string]any {
	return map[string]any{
		"enabled": true,
		"network": map[string]any{
			"allowedDomains": []any{}, // deny-all; no unix-socket allowance
		},
		"failIfUnavailable":        true,  // refuse rather than run uncontained
		"allowUnsandboxedCommands": false, // no unsandboxed escape hatch
	}
}

// egressDenyHook returns the PreToolUse hook that denies the out-of-sandbox
// egress channels WebFetch and WebSearch; its "mcp__" token was meant to cover MCP
// tools but does not on current Claude Code (see egressDenyMatcher). Its refusal
// text still names MCP and is left unchanged. It is applied in sandbox mode only. The hook fires even under bypassPermissions -- unlike
// permissions.ask/deny -- which is why it, not a permission rule, is the closure
// for these channels. Exit 2 blocks the tool call.
func egressDenyHook() map[string]any {
	return map[string]any{
		"matcher": egressDenyMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "echo 'niwa watch: no-egress sandbox -- WebFetch/WebSearch/MCP are disabled' >&2; exit 2",
			},
		},
	}
}

// sessionReachDenyCommand returns the shell command of the session-reach deny
// hook: it writes sessionReachDenyMessage and a newline to stderr, nothing to
// stdout, and exits 2 (block) whatever stdin holds. shellQuote keeps the
// apostrophe in the message intact.
//
// The command reads stdin to the end before refusing. A hook that exits without
// reading leaves Claude Code writing the payload into a closed pipe once the
// payload outgrows the pipe buffer, and its hook runner reports that write error
// as a non-blocking result, so a large enough tool_input would let the call
// through. Draining keeps the exit-2 block. Draining is safe because Claude Code closes
// the hook's stdin after writing the payload, which the post-guard's grep already
// relies on; if stdin were ever held open, the hook would wait until its timeout
// rather than block.
//
// The command is part of the hook's identity (see preToolUseHasHook), so changing
// it leaves the old entry beside the new one in an instance staged before the
// change. That is harmless while both refuse.
func sessionReachDenyCommand() string {
	return `cat >/dev/null 2>&1; printf '%s\n' ` + shellQuote(sessionReachDenyMessage) + ` >&2; exit 2`
}

// sessionReachDenyHook returns the PreToolUse hook that refuses the tools able to
// reach other sessions (see sessionReachDenyMatcher). It is applied in every
// containment mode. Like the post-guard, egress-deny, and filesystem-guard hooks it
// blocks by exiting 2, which holds under every permission mode, including
// bypassPermissions.
func sessionReachDenyHook() map[string]any {
	return map[string]any{
		"matcher": sessionReachDenyMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": sessionReachDenyCommand(),
			},
		},
	}
}

// fsGuardHook returns the PreToolUse hook that guards a filesystem escape via the
// built-in Write/Edit/MultiEdit/NotebookEdit tools. It is applied in sandbox mode
// only. The OS sandbox confines Bash writes to the instance, but the built-in file
// tools run through the permission system, so this hook is the closure for that
// channel, the filesystem counterpart to egressDenyHook. It delegates the decision to
// `niwa watch guard-fs --root <instancePath>`, which resolves the target path against
// that explicit instance root. Baking the instance path into the hook (rather than
// letting the guard infer it from CLAUDE_PROJECT_DIR or cwd) makes the containment
// root niwa's own record, immune to an ambient value inherited wider than the instance.
//
// The guard runs under failClosedWrapper in both postures, which maps any non-zero
// exit, a missing binary included, to exit 2 (block). The posture changes what the
// guard decides:
//
//   - ask == false (hard-deny posture, the shipped floor): the guard exits 0 (inside)
//     or non-zero (outside / fail-closed). An out-of-instance write is a hard deny --
//     correct under bypassPermissions, where a hook's ask is inert.
//   - ask == true (operator-approval posture): the guard runs with --ask-outside and
//     prints an allow/ask PreToolUse decision on stdout and exits 0, or exits 2 for a
//     fail-closed deny. Under a non-bypass permission mode the harness honors the
//     decision, so an out-of-instance write surfaces an operator approval that fails
//     closed if unanswered, while an in-instance write is auto-approved.
//
// In both postures a write to the instance's own .claude/ tree or .mcp.json, through
// a symlink that can't be resolved, or to a file with more than one hard link is a
// hard deny, never an operator approval (see GuardFSDecision). The command, built by
// fsGuardCommand, names this instance and posture and is part of the hook's
// identity.
func fsGuardHook(instancePath string, ask bool) map[string]any {
	return map[string]any{
		"matcher": fsGuardMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": fsGuardCommand(instancePath, ask),
			},
		},
	}
}

// fsGuardCommand returns the filesystem guard's hook command for an instance and a
// posture. It is part of the hook's identity, so VerifyReviewSettings rebuilds it
// from the instance path and posture: an entry whose --root names another
// directory, or that was built for the other posture, is not niwa's guard.
func fsGuardCommand(instancePath string, ask bool) string {
	guard := fmt.Sprintf(`%s watch guard-fs --root %s`, shellQuote(guardBinPath()), shellQuote(instancePath))
	if ask {
		guard += " --ask-outside"
	}
	return guard + failClosedWrapper
}

// failClosedWrapper follows a guard invocation in a hook command. It passes exit 0
// through and turns any other exit into 2, the one code Claude Code treats as a
// block: a guard that denies, crashes, or isn't found (exit 127) all block. On the
// non-zero path it drains stdin first, because a guard that never ran left the
// payload unread, and an unread payload larger than a pipe buffer makes Claude
// Code's write fail, which its hook runner reports as a non-blocking result.
const failClosedWrapper = `; ec=$?; if [ "$ec" = "0" ]; then exit 0; else cat >/dev/null 2>&1; exit 2; fi`

// sessionCommandDenyCommand returns the session-command deny's hook command: it
// runs `<niwa> watch guard-bash` on the payload, under the fail-closed wrapper.
func sessionCommandDenyCommand() string {
	return shellQuote(guardBinPath()) + " watch guard-bash" + failClosedWrapper
}

// sessionCommandDenyHook returns the PreToolUse hook that refuses a review Bash
// command naming niwa's or Claude Code's session commands: niwa retask, niwa
// resume, niwa dispatch, claude --resume (or -r), claude attach, claude stop and
// claude rm. It is applied and required in every containment mode, for fresh
// stages and continuations alike. The decision is GuardBashDecision's, which
// exits 2 with sessionCommandDenyMessage on stderr; unreadable or unparseable
// input and a missing niwa binary block too.
//
// It is a string match on the command text, not a boundary: a script, a variable
// or quoting tricks get around it. It supplements the OS sandbox, whose denial of
// all network, unix sockets included, is what keeps a sandboxed review's Bash off
// the Claude Code daemon those commands talk to. With watch_sandbox = off it is
// accident prevention only, like the post-guard.
func sessionCommandDenyHook() map[string]any {
	return map[string]any{
		"matcher": sessionCommandDenyMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": sessionCommandDenyCommand(),
			},
		},
	}
}

// autoAllowHook returns the PreToolUse hook that auto-approves the normal review tools
// (Bash/Read/Glob/Grep) in the operator-approval posture. Under a non-bypass permission
// mode these tools would otherwise prompt and hang the --bg session; the hook emits an
// explicit allow decision on stdout so the review runs autonomously. It is applied only
// in the ask posture. The hard-deny posture neither adds it nor sets a permission mode:
// that session launches with no --permission-mode and runs in whatever mode its merged
// settings resolve to, which niwa leaves unset for a bypass or undeclared workspace.
func autoAllowHook() map[string]any {
	const decision = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"niwa watch review: in-instance tool auto-approved"}}`
	return map[string]any{
		"matcher": autoAllowMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "printf '%s' " + shellQuote(decision),
			},
		},
	}
}

// postGuardHook returns the PreToolUse hook that refuses an accidental review or
// comment post. It inspects the Bash tool's command payload and blocks a
// `gh pr review`/`gh pr comment`. Applied in BOTH modes: the session runs with
// the developer's real credentials, so this is accident prevention (posting is
// always a human act), NOT a security boundary. The prompt already tells the
// agent not to post; this catches a stray prompt-following.
func postGuardHook() map[string]any {
	return map[string]any{
		"matcher": postGuardMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": "grep -qE '\"command\":.*gh[[:space:]]+pr[[:space:]]+(review|comment)' && { echo 'niwa watch: posting is a human step -- draft the review and stop' >&2; exit 2; } || exit 0",
			},
		},
	}
}

// ApplyReviewSettings merges the review-session settings into a provisioned
// instance's .claude/settings.json and re-verifies they survived the merge,
// preserving any existing hooks and other keys. Three PreToolUse hooks are added in
// every mode: the post-guard, the session-reach deny (which refuses SendMessage,
// SendFile, RemoteTrigger and ListAgents) and the session-command deny (which
// refuses review Bash commands that run niwa's or Claude Code's session commands).
// When sandbox is true the no-egress sandbox stanza is written (fully owned, so no
// pre-existing sandbox config can relax the posture) and the egress-deny and
// filesystem-guard PreToolUse hooks are added -- the two channels the OS sandbox does
// not cage.
//
// Every hook is deduped by its identity, matcher AND exact command under PreToolUse
// with a handler holding only "type" and "command" (see preToolUseHasHook): an
// existing entry with niwa's matcher but a different command, or an extra handler
// key, is kept, and niwa's hook is added beside it. Re-applying to an unchanged
// instance leaves settings.json byte-identical, so the function can run before
// every wake of an existing review.
//
// A disableAllHooks set to anything but false, in either the instance's
// .claude/settings.json or its .claude/settings.local.json, turns every review hook
// off. ApplyReviewSettings refuses it before writing anything and leaves the key
// where it is: the review must not launch, and removing the key would hide what
// set it.
//
// The ask flag selects the out-of-instance-write posture (it is meaningful only when
// sandbox is true):
//
//   - ask == false (hard-deny posture, the shipped floor): permissions.defaultMode
//     is NOT set here: this function does not choose a mode and leaves any the
//     instance's settings already hold. No auto-allow hook is added, and the
//     filesystem guard denies an out-of-instance write.
//   - ask == true (operator-approval posture): niwa fully owns
//     permissions.defaultMode = "default" (so a hook ask is honored instead of
//     silently allowed), adds the Bash/Read/Glob/Grep auto-allow hook (so the
//     non-bypass session does not hang on the normal review tools), and wires the
//     filesystem guard with --ask-outside (out-of-instance = operator ask). The caller
//     seeds workspace trust separately; this function assembles the settings only.
//
// The re-verification is the per-instance check that runs before launch; a dropped or
// relaxed stanza or hook means the PR must not be launched.
func ApplyReviewSettings(instancePath string, sandbox, ask bool) error {
	claudeDir := filepath.Join(instancePath, ".claude")
	settingsPath := filepath.Join(claudeDir, "settings.json")
	settings := map[string]any{}
	if data, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("apply review settings: parsing %s: %w", settingsPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("apply review settings: reading %s: %w", settingsPath, err)
	}
	if err := refuseDisableAllHooks(settings); err != nil {
		return fmt.Errorf("apply review settings: %s: %w", settingsPath, err)
	}
	localPath := filepath.Join(claudeDir, "settings.local.json")
	if data, err := os.ReadFile(localPath); err == nil {
		local := map[string]any{}
		if err := json.Unmarshal(data, &local); err != nil {
			return fmt.Errorf("apply review settings: parsing %s: %w", localPath, err)
		}
		if err := refuseDisableAllHooks(local); err != nil {
			return fmt.Errorf("apply review settings: %s: %w", localPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("apply review settings: reading %s: %w", localPath, err)
	}

	if sandbox {
		// The sandbox stanza is fully owned -- overwrite it so no pre-existing
		// sandbox config can relax the no-egress posture.
		settings["sandbox"] = noEgressSandboxStanza()
	}

	// In the operator-approval posture niwa owns permissions.defaultMode so a hook's
	// ask decision is honored (under bypassPermissions it would be silently allowed).
	// Other permissions keys (e.g. a pre-existing deny list) are preserved.
	if sandbox && ask {
		perms, _ := settings["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
		}
		perms["defaultMode"] = "default"
		settings["permissions"] = perms
	}

	// Ensure hooks.PreToolUse is an array, preserving any existing entries and
	// other hook events.
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	preToolUse, _ := hooks["PreToolUse"].([]any)

	// Add each review hook this mode needs, deduped by matcher AND command so a
	// same-matcher entry running something else can't stand in for it.
	for _, h := range reviewHooks(instancePath, sandbox, ask) {
		if !preToolUseHasHook(preToolUse, h.matcher, h.command) {
			preToolUse = append(preToolUse, h.build())
		}
	}
	hooks["PreToolUse"] = preToolUse
	settings["hooks"] = hooks

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("apply review settings: encoding settings: %w", err)
	}
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return fmt.Errorf("apply review settings: creating .claude dir: %w", err)
	}
	if err := os.WriteFile(settingsPath, out, 0o644); err != nil {
		return fmt.Errorf("apply review settings: writing settings: %w", err)
	}

	// Re-read from disk and re-verify the settings survived the write/merge.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return fmt.Errorf("apply review settings: re-reading settings: %w", err)
	}
	var merged map[string]any
	if err := json.Unmarshal(data, &merged); err != nil {
		return fmt.Errorf("apply review settings: re-parsing settings: %w", err)
	}
	return VerifyReviewSettings(merged, instancePath, sandbox, ask)
}

// reviewHook is one PreToolUse hook a review session needs: its name (for error
// messages), its identity (matcher and exact command) and the entry that installs it.
type reviewHook struct {
	name             string
	matcher, command string
	build            func() map[string]any
}

// reviewHooks lists the PreToolUse hooks a review in this mode needs, in the order
// ApplyReviewSettings adds them. The post-guard, the session-reach deny and the
// session-command deny apply in every mode; the egress deny and the filesystem guard
// in sandbox mode; the auto-allow in the sandboxed operator-approval posture.
func reviewHooks(instancePath string, sandbox, ask bool) []reviewHook {
	hooks := []reviewHook{
		{"post-guard", postGuardMatcher, hookCommand(postGuardHook()), postGuardHook},
		{"session-reach deny", sessionReachDenyMatcher, sessionReachDenyCommand(), sessionReachDenyHook},
		{"session-command deny", sessionCommandDenyMatcher, sessionCommandDenyCommand(), sessionCommandDenyHook},
	}
	if sandbox {
		hooks = append(hooks, reviewHook{"egress-deny", egressDenyMatcher, hookCommand(egressDenyHook()), egressDenyHook})
		if ask {
			hooks = append(hooks, reviewHook{"auto-allow", autoAllowMatcher, hookCommand(autoAllowHook()), autoAllowHook})
		}
		fs := func() map[string]any { return fsGuardHook(instancePath, ask) }
		hooks = append(hooks, reviewHook{"filesystem-guard", fsGuardMatcher, fsGuardCommand(instancePath, ask), fs})
	}
	return hooks
}

// hookCommand returns the command of the single handler in a hook entry niwa builds.
func hookCommand(entry map[string]any) string {
	inner, _ := entry["hooks"].([]any)
	if len(inner) != 1 {
		return ""
	}
	h, _ := inner[0].(map[string]any)
	cmd, _ := h["command"].(string)
	return cmd
}

// refuseDisableAllHooks returns an error naming disableAllHooks when a settings
// document sets it to anything other than JSON false. Claude Code's
// disableAllHooks turns every hook off, the review's guards included.
func refuseDisableAllHooks(doc map[string]any) error {
	v, ok := doc["disableAllHooks"]
	if !ok {
		return nil
	}
	if b, isBool := v.(bool); isBool && !b {
		return nil
	}
	return fmt.Errorf("review settings check: disableAllHooks is set (%v); it turns off every review hook, so the review must not launch", v)
}

// VerifyReviewSettings checks a merged settings document for everything a review
// in this mode needs. Every review hook ApplyReviewSettings installs must be
// present under PreToolUse by its identity, matcher AND exact command with a
// handler holding only "type" and "command" (see preToolUseHasHook): the
// post-guard, the session-reach deny and the session-command deny in every mode;
// in sandbox mode the egress deny and the filesystem guard, whose command names
// instancePath and the posture; and in the operator-approval posture the
// auto-allow. A disableAllHooks set to anything but false fails, as does, in
// sandbox mode, a relaxed no-egress stanza (enabled, empty allowedDomains,
// failIfUnavailable, no unsandboxed escape hatch) and, when ask is true, a
// permissions.defaultMode other than "default".
func VerifyReviewSettings(merged map[string]any, instancePath string, sandbox, ask bool) error {
	if err := refuseDisableAllHooks(merged); err != nil {
		return err
	}
	if sandbox {
		sb, ok := merged["sandbox"].(map[string]any)
		if !ok {
			return fmt.Errorf("review settings check: sandbox stanza missing from merged settings")
		}
		enabled, _ := sb["enabled"].(bool)
		if !enabled {
			return fmt.Errorf("review settings check: sandbox.enabled is not true")
		}
		network, ok := sb["network"].(map[string]any)
		if !ok {
			return fmt.Errorf("review settings check: sandbox.network missing")
		}
		domains, ok := network["allowedDomains"].([]any)
		if !ok {
			return fmt.Errorf("review settings check: sandbox.network.allowedDomains missing")
		}
		if len(domains) != 0 {
			return fmt.Errorf("review settings check: allowedDomains must be empty (deny-all), got %d entries", len(domains))
		}
		// Fail-open closure: the harness must refuse rather than silently disable
		// the sandbox, and must not permit an unsandboxed escape hatch.
		if fail, _ := sb["failIfUnavailable"].(bool); !fail {
			return fmt.Errorf("review settings check: sandbox.failIfUnavailable must be true")
		}
		if allow, _ := sb["allowUnsandboxedCommands"].(bool); allow {
			return fmt.Errorf("review settings check: sandbox.allowUnsandboxedCommands must be false")
		}
		// The operator-approval posture additionally requires the non-bypass permission
		// mode, so a hook ask is honored.
		if ask {
			perms, ok := merged["permissions"].(map[string]any)
			if !ok {
				return fmt.Errorf("review settings check: permissions block missing under the operator-approval posture")
			}
			if mode, _ := perms["defaultMode"].(string); mode != "default" {
				return fmt.Errorf("review settings check: permissions.defaultMode must be \"default\" under the operator-approval posture, got %q", mode)
			}
		}
	}
	for _, h := range reviewHooks(instancePath, sandbox, ask) {
		if !hasPreToolUseHook(merged, h.matcher, h.command) {
			return fmt.Errorf("review settings check: %s PreToolUse hook (matcher %q) missing", h.name, h.matcher)
		}
	}
	return nil
}

// preToolUseEntries returns merged["hooks"]["PreToolUse"] as a []any, or nil when
// the hooks block or the PreToolUse array is missing or has the wrong shape.
func preToolUseEntries(merged map[string]any) []any {
	hooks, ok := merged["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	preToolUse, _ := hooks["PreToolUse"].([]any)
	return preToolUse
}

// hasPreToolUseHook walks merged["hooks"]["PreToolUse"] and reports whether some
// entry has the given matcher and a command hook running exactly the given command.
func hasPreToolUseHook(merged map[string]any, matcher, command string) bool {
	return preToolUseHasHook(preToolUseEntries(merged), matcher, command)
}

// preToolUseHasHook reports whether any entry in a PreToolUse array has the given
// matcher and, among its hooks, one holding exactly the keys "type" (equal to
// "command") and "command" (equal to the given string). It is the identity of every
// review hook: the matcher alone isn't enough, since an entry can keep niwa's matcher
// and run something else. Any extra handler key is refused because Claude Code
// honors fields such as async, once, if, and timeout that can keep a hook with the
// right command from blocking. Shared by the apply-time dedupe and the verify-time
// check.
func preToolUseHasHook(preToolUse []any, matcher, command string) bool {
	for _, entry := range preToolUse {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := m["matcher"].(string); s != matcher {
			continue
		}
		inner, _ := m["hooks"].([]any)
		for _, h := range inner {
			hm, ok := h.(map[string]any)
			if !ok || len(hm) != 2 {
				continue
			}
			typ, _ := hm["type"].(string)
			cmd, _ := hm["command"].(string)
			if typ == "command" && cmd == command {
				return true
			}
		}
	}
	return false
}
