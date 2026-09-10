# Lead: Launch-time mechanisms for pre-approving a tool

Round 1. Investigated 2026-09-09 against Claude Code 2.1.267 (`claude --version` on this machine) and the official docs at code.claude.com. Where a claim is marked "verified against a local agent source reference" it was checked in a local copy of coding-agent source; no path or quote is reproduced here.

## Findings

### 0. The premise needs correcting first: `SendMessage` is not a permission-prompting tool

This changes which mechanisms are even candidates, so it comes before the mechanism survey.

**VERIFIED (docs).** The tools reference table has a "Permission required" column. The `SendMessage` row reads **No**, as does `ListAgents`. <https://code.claude.com/docs/en/tools-reference>

**VERIFIED (local agent source reference).** The `SendMessage` tool's own permission check returns an unconditional *allow* for a same-machine target. It returns *ask* on exactly one branch: when the address resolves to a cross-machine "bridge" target (a Remote Control or cloud session). That ask is tagged internally as a safety check rather than an ordinary permission decision, is explicitly marked as not approvable by the auto-mode classifier, and is guarded ahead of the `bypassPermissions` short-circuit -- it is deliberately bypass-immune.

**VERIFIED (docs).** The same shape is documented from the other side. "Actions no mode auto-approves" lists "the cross-session messaging safeguards" alongside ask rules and `AskUserQuestion`, and says no mode, including `bypassPermissions`, auto-approves them. <https://code.claude.com/docs/en/permission-modes#actions-no-mode-auto-approves>

There are exactly two such safeguards, both documented under bypassPermissions:

1. **`isolatePeerMachines`** -- approval before a `SendMessage` reaches a session beyond this machine. Sender-side. "The approval prompt appears even in `bypassPermissions` mode."
2. **The inbound hold default** -- when no `crossSessionInbound` value applies, Claude Code "holds an inbound message from another of your sessions for your approval, and delivers without asking only when the sending session identifies itself as also bypassing permission prompts." Receiver-side.

<https://code.claude.com/docs/en/permission-modes#skip-all-checks-with-bypasspermissions-mode>

**So for a same-machine dispatch the approval prompt is almost certainly the receiver-side inbound hold dialog, not a tool-permission prompt on the sender.** Everything below is ranked on that basis. The sibling agent is confirming which side prompts; if it turns out to be a cross-machine send instead, mechanism 6 is the relevant knob and nothing in mechanisms 1-5 will help, because that path is bypass-immune by construction.

The documented default, restated as a matrix (receiver behavior when no `crossSessionInbound` applies). Plan mode in a session where bypass is available counts as bypassing; `auto`, `acceptEdits`, and `dontAsk` count as prompting:

| Receiver class | Sender class | Result |
|---|---|---|
| prompts | prompts | delivered |
| prompts | bypasses | HELD -- approval dialog |
| bypasses | prompts | HELD -- approval dialog |
| bypasses | bypasses | delivered |

Note this machine's user settings set `permissions.defaultMode` to `plan` with no bypass flag, so the user's own interactive terminal is in the prompts class, while a niwa worker launched with bypassPermissions is in the bypasses class. Worker to user's own terminal lands in row 2 of that matrix. That is the exact configuration that produces a prompt, and it explains the reported symptom without any tool permission being involved. INFERRED, but the inputs are all verified.

### 1. Why the current bypassPermissions baseline fails -- two independent reasons

**Reason A (VERIFIED, docs).** `permissions.defaultMode` no longer takes effect from project or local settings for the two permissive modes:

> **Scope**: `Any file`. `auto` and `bypassPermissions` don't take effect from project or local settings, so set them in `~/.claude/settings.json` instead. Before v2.1.257, `bypassPermissions` took effect from any file.

<https://code.claude.com/docs/en/settings-reference#permissions-defaultmode>

niwa materializes `permissions.defaultMode` of `bypassPermissions` into the instance's `.claude/settings.json`, which is project scope. On 2.1.267 that value is **silently ignored**. It worked before 2.1.257. Only the forwarded `--permission-mode` flag is actually doing anything today. This is worth fixing on its own merits regardless of the messaging question.

The agent-view docs restate the rule for dispatched sessions and add a second constraint: Claude Code also "refuses a `defaultMode` from the project's `.claude/settings.json` or `.claude/settings.local.json` that selects a more permissive mode than the session you came from was in." <https://code.claude.com/docs/en/agent-view#permission-mode-model-and-effort>

**Reason B (VERIFIED, docs).** Even with bypass genuinely in effect, it does not help. Per the matrix above, bypass on the receiver makes the default stricter, not looser: a bypassing receiver holds every message unless the sender also bypasses. So "just set bypassPermissions" can turn a working delivery into a held one. This is the counterintuitive core of the problem.

### 2. crossSessionInbound -- the mechanism that actually addresses this

**VERIFIED (docs).** <https://code.claude.com/docs/en/settings-reference#crosssessioninbound> and <https://code.claude.com/docs/en/cross-session-messaging#control-inbound-messages>

Values: `"accept"` (deliver), `"hold"` (notice, no delivery), `"refuse"` (drop). Default: unset, which triggers the per-message matrix above. Requires 2.1.224 or later; this machine has 2.1.267.

Setting it to `accept` on the **receiving** session removes the approval dialog entirely, in every permission mode, with no effect on any other tool.

```json
{
  "crossSessionInbound": "accept"
}
```

**The precedence rule for this key is unusual and is the single most important implementation constraint.** From the settings reference:

> **Scope**: `Any file`. A project or local value applies only when it's stricter than the value managed settings, the `--settings` flag, or user settings give.
>
> Claude Code reads managed settings first, then the `--settings` flag, then user settings, and applies the first value found. `refuse` is stricter than `hold`, and `hold` is stricter than `accept`. When none of the trusted sources sets a value, a project or local `hold` or `refuse` still applies, replacing the per-message default.

Consequences for niwa:

- **Writing `crossSessionInbound` of `accept` into the instance's `.claude/settings.json` DOES NOTHING.** `accept` is the least strict value, and project or local scope can only ratchet tighter. This is the mechanism niwa would reach for by analogy with how it writes `defaultMode` today, and it is exactly the one that cannot work. Same trap as reason A above, different key and different rule.
- The only three sources that can grant `accept` are **managed settings**, **the `--settings` flag**, and **user settings at `~/.claude/settings.json`**.
- A malformed value in any of user, project, local, or `--settings` forces `hold` and warns, "even when a source that takes precedence sets `accept`". A typo in a niwa-generated value therefore degrades to holding everything. Validate before writing.

Exact launch form, which is what niwa should emit:

```
claude --settings '{"crossSessionInbound":"accept"}' --permission-mode bypassPermissions -p "..."
```

The CLI reference describes `--settings` as "Path to a settings JSON file or an inline JSON string. Values you set here override the same keys in your `settings.json` files for this session. Keys you omit keep their file-based values. The file must be a regular file no larger than 2 MiB." <https://code.claude.com/docs/en/cli-reference> Inline JSON is supported and merges key by key rather than replacing the file. Writing a small per-instance settings file and passing its path is equally valid and easier to inspect after the fact.

**VERIFIED (docs), and directly on point:** "To let a `-p` worker take messages unattended, start it with `crossSessionInbound` set to `accept` in its `--settings` value. An `accept` in your user settings also works but applies to every session you run." <https://code.claude.com/docs/en/cross-session-messaging#non-interactive-sessions>

That sentence is the documented recipe for precisely niwa's situation.

Related, for a `-p` worker: a default-held message expires after `dialogExpiry` (five minutes by default) and is reported back to the sender as expired. `dialogExpiry` is user-or-managed scope only. A message held by an explicit `hold` never expires.

### 3. permissions.allow rules for SendMessage

**VERIFIED (docs).** Rule syntax is `Tool` or `Tool(specifier)`. A bare tool name matches all uses of that tool. <https://code.claude.com/docs/en/permissions#permission-rule-syntax>

**VERIFIED (docs).** For these two tools specifically, the cross-session messaging page states: add permission deny rules naming `SendMessage` and `ListAgents`, and "Both take the bare tool name with no specifier." <https://code.claude.com/docs/en/cross-session-messaging#turn-off-cross-session-messaging>

**VERIFIED (local agent source reference).** The `SendMessage` tool implements no rule-specifier or permission-suggestion surface, consistent with the docs. So:

- The rule string is exactly `"SendMessage"`.
- **There is no `SendMessage(recipient)` form.** A rule cannot name the recipient. Anything in parentheses would not match a call.
- There is correspondingly no way to narrow a rule to one peer. It is all-or-nothing across local sessions, cross-machine sessions, subagents, and agent-team teammates, since one tool serves all four.

```json
{
  "permissions": {
    "allow": ["SendMessage", "ListAgents"]
  }
}
```

`permissions.allow` is `Any file` scope and **list keys merge across scopes** rather than overriding (<https://code.claude.com/docs/en/settings#lists-merge-instead-of-overriding>), so unlike `defaultMode` and `crossSessionInbound`, an allow rule written into the instance's project `.claude/settings.json` is honored, subject to folder trust (see mechanism 8).

**But it is a no-op for the problem at hand.** Since `SendMessage` never prompts on the send side for a local target, allowing it changes nothing there, and an allow rule is a sender-side tool-permission construct that the receiver-side inbound dialog does not consult at all. It has exactly one real use: in `dontAsk` mode, which auto-denies anything not pre-approved, an explicit allow for `SendMessage` and `ListAgents` is what keeps messaging working. If niwa ever moves workers to `dontAsk` (a better fit for unattended workers than bypass, and it does not perturb the inbound matrix the way bypass does, since `dontAsk` counts as prompting), these two entries become mandatory.

Also verified: deny beats ask beats allow, first match in that order wins, rule specificity is irrelevant, and a matching deny at any level cannot be overridden by any allow at any level including `--allowedTools`. <https://code.claude.com/docs/en/permissions#manage-permissions>

### 4. allowedTools, disallowedTools, tools

**VERIFIED (docs, CLI reference).**

- `--allowedTools`, alias `--allowed-tools`: "Tools that execute without prompting for permission." Same rule syntax, space-separated, for example `claude --allowedTools "SendMessage" "ListAgents" "Bash(git log *)"`. Command-line scope, above local, project and user, below managed.
- `--disallowedTools`, alias `--disallowed-tools`: deny rules. A bare name removes the tool from Claude's context entirely.
- `--tools`: restricts which built-in tools exist at all, for example `"Bash,Edit,Read"`. Unrelated to prompting.

Same verdict as mechanism 3: correct syntax, wrong layer. Passing `--allowedTools "SendMessage"` suppresses nothing that is currently prompting.

### 5. permission-mode, dangerously-skip-permissions, allow-dangerously-skip-permissions

**VERIFIED (docs, CLI reference).**

- `--permission-mode` accepts `default`, `acceptEdits`, `plan`, `auto`, `dontAsk`, `bypassPermissions`, and `manual`. It "Overrides `defaultMode` from settings files." This flag does work from the command line, unlike the project-settings key.
- `--dangerously-skip-permissions` is exactly equivalent to `--permission-mode bypassPermissions`.
- `--allow-dangerously-skip-permissions` only adds bypass to the Shift+Tab mode cycle without starting in it. It grants nothing at launch.
- Configuration flags including `--settings`, `--permission-mode`, `--mcp-config` and `--add-dir` carry through when a session is backgrounded. <https://code.claude.com/docs/en/agent-view#carry-flags-into-a-backgrounded-session>

**This is the blunt instrument the brief warns about, and here it is worse than blunt, it is counterproductive.** It grants every permission in the session, still does not clear the messaging prompt, and by putting the worker in the bypass class it can create holds against prompting peers. Flag it as the mechanism not to reach for.

### 6. isolatePeerMachines -- relevant only if the prompt is cross-machine

**VERIFIED (docs).** Setting `isolatePeerMachines` to `true` requires explicit approval before any `SendMessage` reaches a session beyond this machine, "even in `bypassPermissions` mode". Scope is `Any file`, and "A `true` from any settings scope applies, so a checked-in project file can turn the requirement on but not off" -- another tighten-only ratchet. <https://code.claude.com/docs/en/cross-session-messaging#require-approval-for-cross-machine-messages>

Setting it to `false` is not a way to disable a cross-machine prompt. Per the source reference, the cross-machine ask is a built-in safety check independent of this key; `isolatePeerMachines` adds a requirement, it does not remove the built-in one. VERIFIED against a local agent source reference; the docs describe the key but do not state this negative, so treat the "cannot be turned off" half as strongly supported rather than documented.

Practical consequence: if a niwa worker is ever addressed to a Remote Control or cloud peer, no launch-time mechanism in this document will suppress that prompt. Keep dispatched peers same-machine.

### 7. A PreToolUse hook returning permissionDecision allow

**VERIFIED (docs).** The event exists, matches on tool name, and supports `permissionDecision` of `allow`, `deny`, `ask`, or `defer` inside `hookSpecificOutput`. The matcher is a regex over the tool name, so `"SendMessage"` selects it. <https://code.claude.com/docs/en/hooks#pretooluse>

Shape of the config and a working script, for completeness:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "SendMessage",
        "hooks": [
          { "type": "command", "command": "/abs/path/allow-sendmessage.sh" }
        ]
      }
    ]
  }
}
```

The script reads the hook input JSON on stdin, where `tool_name` is `SendMessage` and `tool_input` holds that tool's own fields (`to`, `message`, and optionally `summary` and `notify_when_idle`), and prints one JSON object on stdout:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "allow",
    "permissionDecisionReason": "niwa dispatch peer messaging"
  }
}
```

**Viable? No, for three separate reasons, each sufficient on its own:**

1. It is sender-side. PreToolUse fires before this session's tool call. The dialog in question is raised by the receiving session when a message arrives, which is not a tool call there at all. No hook event covers inbound message delivery; the documented event list has no such event.
2. Even sender-side, an allow decision is explicitly powerless against this class of prompt. The docs say allow "skips the permission prompt, except for the actions no mode auto-approves", and the cross-session messaging safeguards are on that list.
3. `SendMessage` does not prompt locally anyway, so there is nothing to allow.

`PermissionRequest` hooks are the closer relative. They fire "when Claude Code is about to ask you for permission to use a tool", can return `behavior` of `allow` on the user's behalf, and in sessions that cannot show a prompt Claude Code runs them and denies if no hook decides -- genuinely useful for unattended sessions. But the event is still keyed to tool permission requests, and the same "actions no mode auto-approves" carve-out applies. INFERRED that it cannot answer the inbound dialog; the docs neither promise nor deny it, and I found no event for that dialog.

One live caution: this instance's project settings already register a PreToolUse hook matched on `Bash`. If anyone extends that matcher to `SendMessage` expecting it to fix this, it will silently not work.

### 8. Managed settings, folder trust, environment variables, per-agent grants

**Managed settings (VERIFIED).** Highest precedence: "no other level, including command line arguments, can override a managed permission rule." <https://code.claude.com/docs/en/managed-settings> They can grant `crossSessionInbound` of `accept`, being first in that key's trusted-source order. Rejected as niwa's mechanism: they are an administrator artifact, typically at a root-owned system path, machine-wide across every session and every tool, and not something a user-level CLI should write on a developer machine. Directly contrary to "narrow and off by default".

**Folder trust (VERIFIED, and a real hazard here).** Permission allow rules and additional directories from a repository-supplied settings file are withheld until you trust the folder. `.claude/settings.local.json` counts as repository-supplied when it is tracked in git or when `.claude` is a symlink. <https://code.claude.com/docs/en/permissions#trust-a-folder> Since niwa generates `.claude/` inside instance directories, whether those rules apply on first launch depends on the trust prompt having been answered. Another argument for `--settings`, which is command-line scope and carries no trust step.

**Environment variables (VERIFIED).** "Environment variables aren't a level in this stack." Which one wins is decided per key-and-variable pair, and neither `crossSessionInbound` nor the permission keys have a documented variable pair. There is no environment variable for this. <https://code.claude.com/docs/en/settings#settings-precedence>

**Per-agent tool grants (VERIFIED).** Subagent definitions support a `permissionMode` frontmatter field and a tools list. Irrelevant here: niwa dispatches whole sessions, not subagents, and the inbound dialog is a session-level concern. Note that `permissions.disableBypassPermissionsMode` makes Claude Code ignore a subagent's `permissionMode` of `bypassPermissions`.

**`--restricted` (VERIFIED).** Loads only managed settings and `--settings`, ignoring user and project settings, and refuses bypassPermissions. Not applicable, but it confirms the standing of `--settings` as the trusted non-file channel.

### 9. Full settings precedence, as verified

<https://code.claude.com/docs/en/settings#settings-precedence>, highest first:

1. Managed settings
2. Command line arguments, including `--settings`
3. Project local `.claude/settings.local.json`
4. Shared project `.claude/settings.json`
5. User `~/.claude/settings.json`

With three documented exceptions that matter to this work:

- **List keys merge instead of overriding**, so `permissions.allow` from every scope is unioned.
- **`crossSessionInbound` uses its own order**: managed, then `--settings`, then user, first value found, and project or local can only apply a stricter value. The general stack does not describe this key.
- **`permissions.defaultMode`** ignores `auto` and `bypassPermissions` from project and local scope entirely, since 2.1.257.

The pattern across all three: **the two keys niwa needs are precisely the two that project-scope settings cannot grant.** A settings key being writable is not the same as it being effective, and the documented scope label alone does not tell you which.

## Implications

**Use `--settings` carrying `crossSessionInbound` of `accept`, passed by `niwa dispatch` at launch, as an inline JSON object or a generated per-instance settings file path.** Ranked against the alternatives for a CLI that must configure this programmatically at instance-creation time:

1. **`--settings` with `crossSessionInbound` accept -- recommended.** It is one of only three sources that can grant `accept`; it is command-line scope so it beats every file except managed; it carries no folder-trust step; it carries through into backgrounded sessions; it is scoped to exactly the session niwa launched and expires with it; it grants nothing beyond inbound message delivery, so it is as narrow as the requirement; and it is trivially gated behind a per-machine niwa config plus a `niwa dispatch` flag, defaulting off by simply not emitting the flag. The docs name this exact approach for unattended `-p` workers. niwa already forwards `--permission-mode`, so the plumbing for another launch flag exists.
2. **`crossSessionInbound` accept in `~/.claude/settings.json` -- the necessary complement, not a substitute.** niwa can only pass `--settings` to sessions it launches. If the peer that prompts is the user's own interactive terminal, which is the likely case, only user settings or managed settings can fix that side, and niwa should not silently edit a user's global config. Correct treatment: have the new flag, or a niwa doctor-style check, detect the missing user setting and print the one-line remedy -- edit `~/.claude/settings.json`, or select "Messages from your other sessions" in Claude Code's `/config`, which writes the same key to user settings.
3. **`permissions.allow` with `SendMessage` and `ListAgents` in the instance's project settings -- cheap insurance, wrong layer for today's symptom.** Allow lists merge across scopes so it genuinely applies, and it becomes load-bearing the moment workers run in `dontAsk`. Include it; do not expect it to fix the prompt.
4. **Fix the dead `permissions.defaultMode` while you are in here.** The instance's project `.claude/settings.json` sets bypassPermissions and has been ignored since 2.1.257. Either move it into the `--settings` payload, where it is honored, or drop it and rely solely on the forwarded `--permission-mode`. Leaving a key that looks authoritative but does nothing is how the next investigation loses a day.
5. **`--dangerously-skip-permissions` or `--permission-mode bypassPermissions` -- flagged as the over-broad option, and additionally ineffective.** It grants every permission in the session, does not clear the messaging prompt, and by placing the worker in the bypass class it can cause holds when messaging prompting peers. Strictly worse than option 1 on both breadth and correctness.
6. **Managed settings -- flagged as over-broad.** Machine-wide, administrator-owned, not removable by the user. Wrong tool for a per-dispatch opt-in.
7. **PreToolUse and PermissionRequest hooks -- not viable.** Sender-side only, powerless against the "actions no mode auto-approves" class, and there is no hook event for inbound message delivery. Do not build on this.

Worth considering as a follow-on: **`dontAsk` may be a better worker mode than `bypassPermissions`** for niwa generally. It auto-denies anything not pre-approved, so it needs an explicit `permissions.allow` list that niwa is well placed to generate; it fails closed rather than open; and unlike bypass it counts as prompting in the inbound matrix, so it does not provoke holds at prompting peers. That is a design question for a sibling thread, not this one.

## Surprises

- **bypassPermissions makes inbound messaging stricter, not looser.** A bypassing receiver holds every message unless the sender also bypasses. The mode most people reach for to remove prompts is one of the two ways to create this one.
- **The instance's `permissions.defaultMode` of bypassPermissions has been inert since 2.1.257** because it lives in project scope. That is an unrelated live bug in the current niwa setup, found incidentally.
- **`crossSessionInbound` can only be ratcheted tighter from project or local scope.** The obvious implementation, writing `accept` into the instance's `.claude/settings.json` next to the other keys niwa already writes, is silently a no-op. Both keys that matter here share this trap, by two different rules.
- **A malformed `crossSessionInbound` value forces `hold` and overrides an `accept` from a higher-precedence source.** A generated typo degrades to holding everything rather than to the documented default. niwa must validate the value it emits.
- `SendMessage` accepts no rule specifier at all. No `SendMessage(peer)` form exists, so a permission rule can never be narrowed to one recipient, in either direction.
- The cross-machine send prompt is a bypass-immune safety check that no allow rule, no hook, and no setting removes. Setting `isolatePeerMachines` to `false` does not disable it.

## Open Questions

1. **Which side is actually prompting?** The sibling agent's lead. If it is the receiver's inbound dialog, option 1 is right. If it is a cross-machine send, nothing here helps and the fix is to keep dispatched peers same-machine. The distinguishing evidence is the dialog's wording: "Message from" a session name with a preview is the inbound hold; text about a Remote Control session and Anthropic's servers is the cross-machine safety check.
2. **Is the prompting peer a niwa-launched instance or the user's own terminal?** This determines whether `--settings` alone suffices or whether the user must also set the key in `~/.claude/settings.json`. Given this machine's user-level `defaultMode` of `plan`, I expect the latter, and the design should account for a remedy niwa can only advise, not apply.
3. **Does niwa dispatch via `claude --bg`, `claude agents`, or a bare `claude -p`?** All three accept `--settings`, but the plumbing differs. Not settled here.
4. **Should workers move from bypassPermissions to dontAsk?** Would need the explicit allow list from mechanism 3 and a survey of what workers actually call. Out of scope for this lead.
5. Whether a PermissionRequest hook can answer the inbound dialog is an inferred negative rather than a documented one. Cheap to falsify empirically if anyone wants certainty.

## Summary

`SendMessage` is documented as requiring no permission, and its own check allows same-machine sends unconditionally, so the prompt is not a tool-permission prompt at all: it is the receiver-side inbound hold that fires when exactly one of the two sessions is in the bypassPermissions class, which is why setting bypassPermissions made things worse rather than better. The fix is `crossSessionInbound` set to `accept`, which project-scope settings can never grant because that key only ratchets stricter from project or local scope, so it must come from `--settings`, user settings, or managed settings, making an inline `--settings` value at launch the right mechanism for niwa: narrow, session-scoped, trust-free, and off by default by simply not emitting the flag. The biggest open question is whether the prompting peer is a niwa-launched instance, which niwa can fix entirely, or the user's own terminal, which niwa can only advise the user to fix in `~/.claude/settings.json`.
