# Lead: External readers of the materialized permissions key

The lead agent for this round ran read-only and could not write its own file.
Its findings are transcribed here by the orchestrator, with the private-repo
material reduced to what can be stated in a public artifact.

## Findings

**Headline: nothing outside the `niwa` repo reads `permissions.defaultMode`.**
A workspace-wide sweep for `defaultMode`, `bypassPermissions`, `acceptEdits`,
`askPermissions`, `--permission-mode`, `permissionMode`, and for any code that
opens a `.claude/settings.json` or `.claude/settings.local.json` at all,
returned no reader in any repo other than niwa. That covers code, hook scripts,
skill and command markdown, CI configuration, and prose.

### public/koto

No reader. The only mentions are structural: a `.gitignore` negation for
`.claude/settings.json`, and one doc-name exception string in a test. The two
hook scripts parse hook stdin via `jq` and open no settings file. koto's
committed `.claude/settings.json` carries no `permissions` key.

### public/shirabe

No code reader; the committed settings file carries no `permissions` key. Two
preflight scripts mention `.claude/settings.json`, but only as an untrusted
source of session *environment* (they reason about who can set
`SHIRABE_PREFLIGHT_ROOTS`), never as a source of permissions.

The only non-niwa `defaultMode` prose in the whole workspace is in shirabe's
`docs/designs/current/DESIGN-skill-preflight-checks.md`, which gates a rollout
phase on validating against a host whose `permissions.defaultMode` is not
`auto`, because a user-scope `"defaultMode": "auto"` masks a pattern mismatch.
That is user scope and a different key instance; a rename of niwa's internal
signal would not touch it.

`docs/designs/current/DESIGN-pr-template-gate.md` rejects an `ask` hook
decision on the grounds that "the session runs under `bypassPermissions` and
dispatched/headless agents have no human to prompt". That is a design premise
*about the posture niwa's materialization is supposed to produce*, held outside
niwa, and it is load-bearing for a decision that shipped. It does not read the
key; it assumes the outcome.

### public/tsuku

No reader. Two things worth carrying forward:

- `docs/designs/current/DESIGN-tsuku-ai-skills.md` has a settings-key ownership
  table placing `bypassPermissions` in the gitignored `settings.local.json`
  rather than the committed file.
- `docs/prds/PRD-tsuku-ai-skills.md` states as acceptance criteria that the
  committed `settings.json` "does not contain env, hooks, permissions, or
  mcpServers keys". That is a checked negative constraint, and it fences off
  any option that would relocate niwa's signal into the committed per-repo
  settings file.

### public/dot-niwa -- the live declaration

`.niwa/workspace.toml` declares:

```toml
[claude.settings]
permissions = "bypass"
```

This is the live input for the `tsukumogami` workspace, and it is the only
`[claude.settings]` declaration anywhere in the checkout. It materializes into
`.claude/settings.local.json` in every instance repo -- confirmed by reading
the materialized files, whose tails carry
`"permissions": { "defaultMode": "bypassPermissions" }`.

The two hook scripts dot-niwa ships parse only hook stdin. One carries a
premise comment -- "Works in bypassPermissions mode because hooks are external
to the permission system" -- which is a correct statement about hooks but
presupposes the session is in bypass. Same false-premise class as the four
already found inside niwa, in a public repo, outside niwa.

### private repos

Reported at the level of what exists, without private content:

- The private workspace overlay declares **no** `[claude.settings]` table and
  no permissions key. The single bypass declaration governing this workspace
  sits in the *public* config repo.
- One private repo ships a legacy installer, predating niwa, that independently
  writes the same `permissions.defaultMode: bypassPermissions` block into each
  repo's `settings.local.json`, with the same "the hook is the real gate"
  rationale. It is a **writer, not a reader** -- nothing there parses the key
  back -- and it carries the same dead-value problem while being outside the
  reach of any niwa change. Three design documents in that repo state the same
  premise in prose.
- Agent definitions in that repo carry `permissionMode: acceptEdits` in
  frontmatter. That is Claude Code subagent frontmatter, a separate mechanism
  with no relationship to the settings key.
- One private repo is a source tree of the Claude Code CLI itself, so it is the
  upstream whose behavior change made the key inert rather than a consumer of
  niwa's output. It is not an external reader.
- Private planning documents record the dispatch-forwarding behavior and the
  staleness of the `permissions = "bypass"` declaration, but no script,
  harness, or CI file there parses a settings file for permissions.

### What was not found anywhere outside niwa

- No source file in any language that opens a materialized settings document
  and reads a key from it.
- No hook script that parses a settings file at all -- every hook parses hook
  stdin.
- No CI workflow that inspects the materialized settings.
- No skill or command markdown instructing an agent to read
  `permissions.defaultMode`.
- No `[claude.settings]` declaration other than the one in `dot-niwa`.

## Implications

**The mechanism's blast radius is one repo.** The round trip is niwa's
materializer writing the key and niwa's dispatch flow reading it back, with no
third party in between. Renaming the key, moving it out of the settings
document, or replacing it with a first-class internal signal breaks nothing
outside niwa and stays fully covered by niwa's own tests.

**The blast radius of the *input surface* is wider, and it is a separate
decision.** `permissions = "bypass"` under `[claude.settings]` in
`workspace.toml` is a public, user-facing config key with one live declaration
in this checkout and no visibility into third-party workspaces. Changing the
TOML surface is a config-compatibility break for real workspaces; changing only
the JSON key it materializes into is not. The two decisions can and should be
taken separately, and this exploration's scope explicitly excludes changing
what a workspace may declare.

**One relocation option is fenced off from outside.** Moving the signal into
the committed per-repo `.claude/settings.json` would contradict a documented
acceptance criterion in a sibling public repo. Whatever shape wins, it must not
land there.

**False-premise surfaces exist outside niwa but are shallow.** Three public
ones and three private ones, none of them a dependency -- each is a sentence
that reads as wrong once the key is known to be inert. Only shirabe's
pr-template-gate rationale is load-bearing for a decision that shipped.

## Surprises

1. **The per-repo materialized file is `settings.local.json`, not
   `settings.json`.** Every instance repo has both: a committed,
   permissions-free `settings.json` owned by the repo, and a gitignored
   `settings.local.json` written by niwa carrying `permissions.defaultMode`.
   Some niwa docs name the wrong one. Note this does not contradict round 1:
   the *instance root* receives `settings.json` (which is what the dispatch
   derivation reads), and *per-repo* directories receive `settings.local.json`.
   Both are project-scope-family files for Claude Code's purposes, and both
   carry the inert permissive value.

2. **A second, independent producer of the identical key still exists** in a
   private legacy installer, with the same rationale and the same dead-value
   problem, outside the reach of any niwa change.

3. **The private overlay declares nothing.** The single bypass declaration
   governing this whole workspace sits in the public config repo.

4. **The Claude Code CLI source is present in-workspace**, so the deprecation
   behavior is readable directly rather than inferred from release notes --
   though it cannot be cited in a public artifact.

## Open Questions

- Whether third-party workspaces declare `[claude.settings] permissions` with
  values other than `"bypass"`. Only the one workspace is visible here.
- Whether the shirabe preflight `auto`-masking claim is still accurate given
  the `auto` deprecation. That doc reasons about a user-scope setting, which
  this lead could not settle.

## Summary

Nothing outside the `niwa` repo reads `permissions.defaultMode`, parses a
materialized settings document, or acts on the value -- verified across every
repo in the workspace, covering code, hook scripts, skill and command markdown,
CI configs and prose. The producer/consumer graph is therefore closed within
niwa, so a rename or relocation of the internal signal breaks nothing
externally and stays covered by niwa's tests; but the *input* surface is not
closed, since `[claude.settings] permissions = "bypass"` in the public
workspace config is the live declaration for a real workspace, and a legacy
installer elsewhere independently writes the same dead key. Two corrections for
the write-up: per-repo materialization lands in `settings.local.json` rather
than `settings.json`, and a sibling public repo's acceptance criteria forbid a
`permissions` key in the committed `settings.json`, which rules out relocating
the signal there.
