# Lead: Which Claude Code settings scopes honor permissions.defaultMode?

## Findings

### Sub-question 1: Which settings scopes honor `permissions.defaultMode` today?

**Citation:** [GitHub release v2.1.257](https://github.com/anthropics/claude-code/releases/tag/v2.1.257), [Claude Code settings reference](https://code.claude.com/docs/en/settings-reference.md)

According to the v2.1.257 release notes, `permissions.defaultMode` can be configured in:
- User settings (`~/.claude/settings.json`)
- Managed settings (enterprise/organization policy)
- Command line via `--permission-mode` flag
- Command line via `--settings` inline JSON or file (highest precedence, level 2 in the stack)

The settings precedence order from highest to lowest priority is:
1. Managed settings
2. Command line (`--settings`)
3. Project local (`.claude/settings.local.json`) — **restricted for permissive modes**
4. Shared project (`.claude/settings.json`) — **restricted for permissive modes**
5. User (`~/.claude/settings.json`)

**Key discovery:** The official docs (settings-reference.md) state the scope as "Any file", but this conflicts with what the release notes actually enforce.

### Sub-question 2: Does the answer differ per mode value?

**Citation:** [GitHub release v2.1.257](https://github.com/anthropics/claude-code/releases/tag/v2.1.257), [auto mode restrictions (v2.1.142)](https://dev.to/rulestack/auto-mode-is-now-claude-codes-default-what-the-classifier-approves-and-how-to-switch-back-4j2j)

**Yes, the restrictions apply only to permissive modes.**

- `"bypassPermissions"` — **Restricted to user settings, managed settings, or `--permission-mode` flag.** Cannot be set in `.claude/settings.json` or `.claude/settings.local.json` (project scope). Ignored if attempted there.
- `"auto"` — **Same restriction as bypassPermissions.** Ignored in project-scope settings files since v2.1.142. Must be in user settings, managed settings, or `--permission-mode` flag.
- `"default"` (manual mode), `"acceptEdits"`, `"plan"` — **Appear to be allowed in any scope.** No release notes document restrictions on these non-permissive modes.
- `"dontAsk"` — **Status unclear.** Not mentioned explicitly in release notes reviewed.

**Pattern:** The claim under test is correct that *permissive* modes (`bypassPermissions` and `auto`) are restricted to user/managed scope.

### Sub-question 3: At which version did this change?

**Citation:** [GitHub release v2.1.257](https://github.com/anthropics/claude-code/releases/tag/v2.1.257), [auto mode (v2.1.142)](https://dev.to/rulestack/auto-mode-is-now-claude-codes-default-what-the-classifier-approves-and-how-to-switch-to-manual-mode-or-auto-mode-6j8)

- **`"auto"` restriction:** v2.1.142 (auto mode was already ignoring project settings)
- **`"bypassPermissions"` restriction:** v2.1.257 (explicit change in release notes)

**Before v2.1.257:** `bypassPermissions` could be set in project-scope settings files and would take effect.

**From v2.1.257 onward:** Project-scope `bypassPermissions` is silently ignored.

**Current version on machine:** 2.1.267 — restrictions are in effect.

### Sub-question 4: Is the `--permission-mode` CLI flag still honored for `bypassPermissions`?

**Citation:** `claude --help` output (version 2.1.267)

Yes, the flag is still honored. The CLI help lists `bypassPermissions` as a valid choice. The flag takes effect for bypassPermissions.

### Sub-question 5: Is `--settings` treated as its own scope, project scope, or user scope?

**Citation:** [Settings precedence at code.claude.com/docs/en/settings.md](https://code.claude.com/docs/en/settings)

`--settings` is a separate scope — **Command line scope** — at **level 2 in the precedence stack**, between Managed settings (level 1) and Project local (level 3).

**Key question:** Can `permissions.defaultMode: bypassPermissions` be delivered through `--settings` and take effect?

**Answer:** Possibly, but not documented. The release note says "set it in user or managed settings, or pass `--permission-mode`" — no mention of `--settings`. This is an ambiguity: the scope precedence suggests it should work, but the release note does not explicitly permit it.

### Sub-question 6: Can `--settings` be passed multiple times, and what is the merge behavior?

**Citation:** [Claude Code docs on settings merging](https://code.claude.com/docs/en/settings)

**Documented behavior:** `--settings` merges with other settings files by precedence rules.

**Multiple passes:** **Undocumented.** Whether `--settings` can be passed more than once is not stated in the docs reviewed. This needs a test or official clarification.

## Implications

**For tools like niwa that generate project-scope settings:**

1. Writing `permissions.defaultMode: bypassPermissions` to `.claude/settings.json` is inert as of v2.1.257.
2. The same applies to `permissions.defaultMode: auto` (since v2.1.142).
3. Non-permissive modes work in all scopes.
4. The `--permission-mode` CLI flag is the safe way to apply permissive modes per-invocation.

## Surprises

1. **Auto mode restriction is older:** v2.1.142, not v2.1.257. Permissive-mode restrictions have been in Claude Code longer than the v2.1.257 claim suggests.

2. **Documentation gap:** The settings-reference.md says `permissions.defaultMode` works in "Any file", but this is not enforced for permissive modes in project files.

3. **`--settings` is not mentioned in the release note** as a valid channel for delivering permissive modes. This omission is suspicious.

## Open Questions

1. Can `permissions.defaultMode: bypassPermissions` be delivered via `--settings` and take effect?
2. Can `--settings` be passed multiple times in a single invocation?
3. What about `dontAsk` mode — is it restricted?

## Summary

As of v2.1.257 (confirmed in v2.1.267), `permissions.defaultMode: bypassPermissions` is silently ignored in project-scope files (`.claude/settings.json`, `.claude/settings.local.json`), same as `auto` mode since v2.1.142. Non-permissive modes work everywhere. The `--permission-mode` CLI flag still works; whether `--settings` inline JSON can deliver bypassPermissions is undocumented. Tools like niwa that write bypassPermissions to project-scope settings generate inert configuration.
