---
schema: prd/v1
status: In Progress
problem: |
  Since Claude Code 2.1.257, the `bypassPermissions` value niwa writes into
  generated project settings for a `permissions = "bypass"` workspace doesn't
  take effect. It wins the settings merge and is downgraded to `default`,
  overriding the developer's own posture. The `ask` declaration writes
  `askPermissions`, which isn't a valid mode and makes Claude Code discard
  the whole generated file, and each file shows a permissions setting that
  governs nothing.
goals: |
  A workspace's declared posture reaches the workers `niwa dispatch` starts
  through a route that takes effect, developers keep their own posture in
  sessions they open themselves, an `ask` declaration keeps a person in the
  loop, and every permission value in a generated settings file is one Claude
  Code honors from that file.
upstream: docs/briefs/BRIEF-inert-defaultmode-key.md
---

# PRD: inert-defaultmode-key

## Status

In Progress

## Problem Statement

A workspace can declare how much its sessions may do before stopping to ask,
through `permissions` under `[claude.settings]` in `workspace.toml`, with
`"bypass"` and `"ask"` as the accepted values. The declaration can come from
the workspace itself, from the workspace overlay, from a developer's personal
overlay, or from an `[instance.claude.settings]` or
`[repos.<name>.claude.settings]` override. niwa turns it into
`permissions.defaultMode` inside the settings documents it generates: the
instance-root `.claude/settings.json`, each repo's
`.claude/settings.local.json`, each worktree's `.claude/settings.local.json`,
and the workspace-root `.claude/settings.json`.

Both values are broken, in different ways.

**`bypass` writes a value that makes sessions more restrictive, not less.**
From Claude Code 2.1.257, `bypassPermissions` no longer takes effect from a
project or local settings file; `auto` stopped taking effect from those files
at 2.1.142. The ignored value isn't skipped over in favor of the layer below.
Measured on 2.1.267, it wins the settings merge and is then downgraded to
`default`, so a developer whose own user settings say `acceptEdits` or `plan`
gets `default` in every niwa-managed session. A bypass workspace therefore
prompts more than it would if niwa had written nothing. niwa's dispatch
separately forwards `--permission-mode bypassPermissions` on the worker's
command line, which does take effect, so unattended dispatched workers get the
posture while every other session in the workspace loses the developer's own.

**`ask` writes a value that voids the whole file.** The `ask` declaration has
produced `askPermissions` since the mapping was first written, and
`askPermissions` has never been a Claude Code permission mode. Measured on
2.1.267, a settings file carrying it is discarded as a whole: a `model` key in
the same file stopped taking effect, while the same key applied normally next
to a valid mode. A repo or instance that declared `ask` has been getting none
of niwa's generated settings in that scope, and the prompting it asked for
comes only from whatever the developer's own settings happen to say.

**The files say otherwise.** Each generated document shows a permissions block
that reads as the thing deciding the session's posture. A person or an agent
investigating an unexpected prompt opens that file, finds a confident answer,
and starts from a false premise. niwa's own dispatch reads the same value back
out of the file to decide what to forward, so the one route that works depends
on a value that doesn't.

This matters now because the regression is live in every instance of every
bypass-declaring workspace, and the committed guides still promise the old
behavior.

## Goals

- A maintainer who declares `bypass` gets it in every worker `niwa dispatch`
  starts for them, including one they later resume, without typing a flag.
- A developer who opens their own session inside an instance keeps the posture
  their own settings give them. This holds everywhere except where the
  session's settings document resolves to `ask`, where the maintainer's
  explicit choice to keep a person in the loop wins by design.
- A maintainer who declares `ask` gets sessions that ask in that scope, and
  the rest of the generated settings in that scope take effect.
- Every permission value in a file niwa generates is one Claude Code honors
  from that file, so reading the file tells the truth.
- The supervised review keeps working exactly as it does today.

## User Stories

- **US1.** As a workspace maintainer who declared `bypass`, I want a worker I
  dispatch with no permission flag to run without stopping for approval, so
  that background work finishes unattended.
- **US2.** As a developer whose own settings accept edits without prompting, I
  want an interactive session I open inside an instance of a bypass workspace
  to behave the way my settings say, so that niwa doesn't make my sessions
  stricter than they are anywhere else.
- **US3.** As a developer or agent investigating an unexpected prompt, I want
  every permission value in an instance's generated settings to be one that
  actually applies, so that the investigation leads to the real cause.
- **US4.** As a maintainer who declared `ask` for a sensitive repo inside a
  bypass workspace, I want sessions started inside that repo to ask before
  acting and the repo's other generated settings (hooks, deny rules) to take
  effect, so that the opt-out keeps a person in the loop without silently
  dropping everything else.
- **US5.** As an operator running a supervised review, I want the review to
  keep asking for approval as it does today, so that the change doesn't
  reopen a path for unapproved actions.
- **US6.** As a maintainer who resumes a dispatched worker later, I want it to
  come back in the posture it was dispatched with, so that the resumed session
  doesn't stall on prompts the original never showed.
- **US7.** As a developer who declares `bypass` for a workspace in my personal
  overlay, I want the workers I dispatch in that workspace to run unattended,
  so that my own preference works without editing the team's config.

## Requirements

### How each document's posture is resolved

Each generated document resolves its effective posture from the inputs niwa
already uses for that document. This feature doesn't change which inputs a
document uses; it changes what niwa writes once the posture is resolved.
Precedence runs from lowest to highest, with the highest present value
winning:

| Document | Posture inputs, lowest to highest | Rewritten by |
|---|---|---|
| Workspace-root `settings.json` | workspace `[claude.settings]`, then `[instance.claude.settings]` (neither overlay) | `niwa init`; `niwa apply` run from the workspace root |
| Instance-root `settings.json` | workspace overlay, workspace, personal overlay, then `[instance.claude.settings]` | every instance create and apply |
| Repo `settings.local.json` | workspace overlay, workspace, personal overlay, then `[repos.<name>.claude.settings]` | every instance create and apply |
| Worktree `settings.local.json` | the same inputs as its repo; a worktree created by `niwa worktree create`, `niwa worktree apply`, or the WorktreeCreate hook omits the personal overlay until the next instance apply | the worktree paths named, and every instance apply |

The personal overlay is the developer's own `niwa.toml` in their personal
niwa config directory (`~/.config/niwa/global` by default), declared under
`[global.claude.settings]` or `[workspaces.<name>.claude.settings]`. It can
declare a posture only at workspace level, and it wins over the team's
workspace-level value but loses to any instance or repo override in the team
config.

The **instance's effective posture**, used by the dispatch derivation, is the
posture the instance-root document resolves from.

### Functional

- **R1. Bypass reaches dispatched workers.** When the instance's effective
  posture is `bypass` and the operator supplied no `--permission-mode`, the
  Claude worker's launch command carries `--permission-mode bypassPermissions`.
  This holds for every form of `niwa dispatch`: detached, attached, and with
  remote control enabled.
- **R2. An explicit flag wins.** When the operator supplies
  `--permission-mode`, the launch command carries that value and no other
  `--permission-mode`, whatever the declared posture.
- **R3. The derivation tracks the declaration.** Whether dispatch forwards
  `--permission-mode bypassPermissions` is decided by the instance's effective
  posture and nothing else. It forwards the flag for `bypass` and forwards no
  `--permission-mode` for `ask` or undeclared. A per-repo override doesn't
  change the decision. The contents of a generated settings document don't
  change it either: a stale or hand-edited document that says
  `bypassPermissions` doesn't grant bypass, and a document that carries no
  permission value doesn't withhold it.
- **R4. No ignored or invalid value is ever written.** No settings document
  niwa generates carries a `permissions.defaultMode` of `bypassPermissions`,
  `auto`, or `askPermissions`, under any declared posture, at any of the four
  locations in the table above.
- **R5. Only recognized values are written.** Every `permissions.defaultMode`
  value niwa writes is a member of the set Claude Code honors from project
  scope, measured on 2.1.267: `default`, `acceptEdits`, `plan`, `dontAsk`.
- **R6. `bypass` writes no permission mode.** A document whose effective
  posture is `bypass` carries no `permissions.defaultMode`. The posture
  reaches dispatched workers through R1, and sessions a developer opens
  themselves get the developer's own settings.
- **R7. `ask` writes `default`.** A document whose effective posture is `ask`
  carries `permissions.defaultMode: "default"`, and every other key niwa
  writes into that document is the same as it would be with the posture
  undeclared.
- **R8. Undeclared writes nothing.** A document with no effective posture
  carries no `permissions.defaultMode`, unchanged from today.
- **R9. Re-apply repairs existing documents.** After `niwa apply` runs over
  documents written by an earlier niwa, every document that apply rewrites
  matches R4 through R8. A retired value (`bypassPermissions`,
  `askPermissions`) and a hand-set `permissions.defaultMode` in a
  niwa-generated document don't survive the rewrite, and the document's other
  generated keys are unchanged. The workspace-root document is repaired by an
  apply run from the workspace root, which is the only apply that rewrites it.
- **R10. Invalid declarations are rejected.** A declared `permissions` value
  other than `bypass` or `ask`, at any level including the personal overlay,
  fails the operation with an error naming the accepted values, and no
  document is written carrying it.
- **R11. Resume keeps the launch posture.** A worker re-entered through a
  re-entry command niwa builds or prints comes back in the posture its
  dispatch launched it with. Those commands are dispatch's own final attach,
  the attach hint dispatch prints, the attach-failure fallback, and the resume
  column of `niwa list`. Each is `claude attach <handle>`, which accepts no
  options. Claude Code preserves a worker's launch flags for re-entry, so R1's
  flag on the launch command is what satisfies this; no re-entry command is
  required to carry a flag of its own.
- **R12. Launch paths are accounted for.** `niwa dispatch` is the only niwa
  command that derives a posture for a Claude session it starts. A
  `niwa watch` review launch, both a fresh review and a continuation, carries
  no derived `--permission-mode`, because a command-line flag outranks the
  `"default"` the review writes. A Codex dispatch carries no
  `--permission-mode`, and its `--sandbox` handling is unchanged. The
  SessionStart hook runs after a session starts and carries no posture.
- **R13. The supervised review is untouched.** `niwa watch`'s operator-approval
  posture still writes `permissions.defaultMode: "default"` into the instance
  root, and its verification still passes, on top of an instance materialized
  under `bypass`, `ask`, and undeclared.
- **R14. No reader of the retired value remains.** The unused
  `WorkerPermissionMode` reader in `internal/workspace/permissions.go` is
  removed with its test, and the dispatch derivation no longer reads a
  permission mode out of a generated settings document. `niwa watch` verifying
  its own write is the one remaining reader of `permissions.defaultMode`.
- **R15. The committed docs stop promising the retired mechanism.** These
  documents are corrected:
  - `docs/guides/ephemeral-session-instances.md` stops saying a root-level
    bypass posture applies to every session launched at the root. It states
    that sessions a developer starts there, including ephemeral workers, get
    the developer's own settings, and that `--permission-mode` on the
    developer's own launch is the route to bypass.
  - `docs/guides/file-distribution.md` stops saying the setting "maps to
    Claude Code's `bypassPermissions` mode", and states that `bypass` reaches
    dispatched workers through `niwa dispatch`.
  - `docs/designs/current/DESIGN-workspace-root-claude.md` marks its
    `bypassPermissions` experimental finding as superseded.
  - `docs/designs/current/DESIGN-mcp-root-instance-distribution.md` records
    that its MCP trust-prompt decision rested on behavior Claude Code 2.1.257
    removed.
  - `docs/designs/current/DESIGN-agent-capability-contract.md` notes that
    Claude's posture now travels on the `--permission-mode` dispatch flag.
  - `docs/designs/current/DESIGN-dispatch-permission-mode.md` corrects the
    version to 2.1.257 and records that its "read the materialized settings,
    never `workspace.toml`" requirement is superseded by this PRD's R3.
  - The comment block above the permission-mode scenarios in
    `test/functional/features/dispatch.feature` names 2.1.257 and describes
    the derivation as reading the declared posture.

  Archived designs and existing PRDs are historical records and aren't
  edited.

### Non-functional

- **R16. No configuration change for existing workspaces.** Every existing
  declaration of `permissions = "bypass"` or `"ask"`, at any level, keeps
  working with no edit. No declaration key is added, renamed, or removed.
- **R17. Derivation tests start from a declaration.** A test that asserts what
  dispatch forwards takes the declared posture from a `workspace.toml` (or
  overlay) declaration run through real materialization, never from a
  hand-written settings document. A tamper test that edits a document after a
  real materialization, to show the edit is ignored, is exempt.
- **R18. The document matrix is tested with every location present.** R4
  through R8 are asserted by a test whose fixture contains at least one repo
  and at least one worktree, which first asserts that all four documents exist
  and parse, and which covers every scenario in the expected-value table
  below.

### Expected values

"none" means the document carries no `permissions.defaultMode`. Repo and
worktree columns show a repo that has no override of its own, except where
the scenario names one.

| Scenario | Workspace root | Instance root | Repo | Worktree | Dispatch argv |
|---|---|---|---|---|---|
| S1: workspace `bypass` | none | none | none | none | `--permission-mode bypassPermissions` |
| S2: workspace `ask` | `default` | `default` | `default` | `default` | no `--permission-mode` |
| S3: undeclared | none | none | none | none | no `--permission-mode` |
| S4: workspace `bypass`, repo X `ask` | none | none | X: `default`; others: none | X's: `default` | `--permission-mode bypassPermissions` |
| S5: workspace `bypass`, `[instance]` `ask` | `default` | `default` | none | none | no `--permission-mode` |
| S6: workspace `ask`, `[instance]` `bypass` | none | none | `default` | `default` | `--permission-mode bypassPermissions` |
| S7: workspace `ask`, repo X `bypass` | `default` | `default` | X: none; others: `default` | X's: none | no `--permission-mode` |
| S8: undeclared, personal overlay `bypass` | none | none | none | none | `--permission-mode bypassPermissions` |
| S9: workspace `bypass`, personal overlay `ask` | none | `default` | `default` | `default` after instance apply | no `--permission-mode` |

## Acceptance Criteria

Dispatch:

- [ ] **AC1 (R1).** A `@critical` functional scenario for S1: `niwa dispatch
  <task> --detach` with no permission flag, and the recorded launch argv
  contains `--permission-mode bypassPermissions`. A second scenario for S1 with
  `[global].remote_control_on_dispatch = true` asserts the argv contains both
  `--permission-mode bypassPermissions` and the remote-control `--settings`
  argument.
- [ ] **AC2 (R2).** A `@critical` functional scenario for S1 with
  `--permission-mode acceptEdits`: the argv contains `--permission-mode
  acceptEdits`, contains exactly one `--permission-mode`, and doesn't contain
  `bypassPermissions`.
- [ ] **AC3 (R2).** A functional scenario for S2 with `--permission-mode
  bypassPermissions`: the argv contains `--permission-mode bypassPermissions`
  exactly once.
- [ ] **AC4 (R3).** Functional scenarios for S3, S5, and S7, with the
  `[instance.claude.settings]` and `[repos.<name>.claude.settings]` tables
  written in `workspace.toml`: in each, the argv contains no
  `--permission-mode`.
- [ ] **AC5 (R3).** Functional scenarios for S4 and S6: in each, the argv
  contains `--permission-mode bypassPermissions`.
- [ ] **AC6 (R3).** Tests for S8 and S9 with a personal overlay `niwa.toml`:
  S8's argv contains `--permission-mode bypassPermissions`, and S9's contains
  no `--permission-mode`.
- [ ] **AC7 (R3, R17).** A unit-level tamper test: materialize S3 from a real
  declaration, overwrite the instance-root document with
  `permissions.defaultMode: "bypassPermissions"`, run the derivation, and
  assert it forwards no `--permission-mode`. A second case materializes S1,
  deletes the instance-root document, and asserts it forwards
  `--permission-mode bypassPermissions`.
- [ ] **AC8 (R12).** A functional scenario for S1 dispatched with `--harness
  codex`, no permission flag, and the existing fake codex: the argv contains
  neither `--permission-mode` nor `--sandbox`.
- [ ] **AC9 (R12).** Tests assert that a `niwa watch` fresh-review launch and
  a continuation launch, each built for S1, carry no `--permission-mode`.

Documents:

- [ ] **AC10 (R4-R8, R18).** A test materializes each of S1 through S9 over a
  fixture with at least one repo and one worktree created through
  `niwa worktree create`. It first asserts that all four documents exist and
  parse as JSON, then asserts that each document's `permissions.defaultMode`
  equals its cell in the expected-value table, or is absent where the cell
  says none. S9's worktree cell is checked after an instance apply.
- [ ] **AC11 (R4, R5).** Across every document produced by AC10, no
  `permissions.defaultMode` equals `bypassPermissions`, `auto`, or
  `askPermissions`, and every value present is one of `default`,
  `acceptEdits`, `plan`, or `dontAsk`.
- [ ] **AC12 (R7).** For each of the four locations, the document produced
  under S2 equals the document produced under S3 plus
  `permissions.defaultMode: "default"`, with every other key identical.
- [ ] **AC13 (R9).** A test seeds an instance and workspace root with the
  documents an earlier niwa wrote (`bypassPermissions` at the instance root,
  `askPermissions` in a repo's document, and a hand-set `defaultMode` in
  another repo's), runs `niwa apply` from the workspace root, and asserts every
  document matches the expected-value table with its other generated keys
  unchanged.
- [ ] **AC14 (R10).** For each of the workspace, instance, repo, and personal
  overlay levels, declaring `permissions = "bypassPermissions"`, `"auto"`, or
  `"acceptEdits"` fails apply with an error that names `bypass` and `ask`, and
  no document is written carrying the value.

Resume, review, and cleanup:

- [ ] **AC15 (R11).** Tests assert that dispatch's final attach, the printed
  attach hint, the attach-failure fallback, and the `niwa list` resume column
  each contain the command `claude attach <handle>` with no further arguments,
  with `<handle>` equal to the handle recorded for the dispatched session.
- [ ] **AC16 (R13).** A test materializes an instance under S1, S2, and S3,
  applies `niwa watch`'s review settings in both its operator-approval and
  hard-deny postures, and asserts that verification passes in each. The
  operator-approval posture leaves `permissions.defaultMode: "default"` in the
  instance root. The hard-deny posture leaves the instance root's value as the
  expected-value table gives it: none for S1 and S3, `default` for S2.
- [ ] **AC17 (R14).** `git grep -n WorkerPermissionMode` returns nothing,
  `internal/workspace/permissions.go` doesn't exist, and the `instanceSettings`
  projection in `internal/cli/dispatch_plugins.go` has no permissions field.

Docs and compatibility:

- [ ] **AC18 (R15).** Each of these holds:
  - `docs/guides/file-distribution.md` doesn't contain "maps to Claude Code's".
  - `docs/guides/ephemeral-session-instances.md` doesn't contain "applies to
    **every** session launched at the root" and does contain
    `--permission-mode`.
  - `docs/designs/current/DESIGN-workspace-root-claude.md` contains
    "superseded" within the same section as its `bypassPermissions` finding.
  - `docs/designs/current/DESIGN-mcp-root-instance-distribution.md` contains
    "2.1.257".
  - `docs/designs/current/DESIGN-agent-capability-contract.md` contains
    `--permission-mode`.
  - `docs/designs/current/DESIGN-dispatch-permission-mode.md` doesn't contain
    "2.1.258" and contains "superseded".
  - `test/functional/features/dispatch.feature` doesn't contain "2.1.258".
- [ ] **AC19 (R16).** The change modifies no `workspace.toml` body in a
  functional scenario that existed before it. A unit test parses
  `permissions = "bypass"` and `permissions = "ask"` at the workspace,
  instance, repo, and personal-overlay levels without error.
- [ ] **AC20 (R17).** No test that asserts on a forwarded `--permission-mode`
  takes its declared posture from a hand-written settings document, apart
  from the tamper cases in AC7.
- [ ] **AC21.** `go test ./...` and `make test-functional-critical` pass.

## Out of Scope

- **The declaration surface.** The `permissions` key, its accepted values, and
  its override levels stay as they are (R16).
- **Which inputs each document resolves from.** The workspace-root document
  ignoring both overlays, and a worktree created outside apply omitting the
  personal overlay, are how niwa resolves those documents today. This feature
  writes the right value for whatever posture each document resolves to; it
  doesn't change the resolution.
- **The declared posture in sessions a developer starts.** Interactive
  sessions, root sessions, and ephemeral workers launched from `claude agents`
  get the developer's own settings, except where their settings document
  resolves to `ask`. niwa has no launch step that could pass a flag to them.
  The guide tells developers how to get bypass there (R15).
- **`niwa worktree attach`.** It resumes a conversation id that no production
  code writes, so no user can reach it today.
- **Fixing `niwa watch`'s hard-deny posture.** Its code assumes review
  sessions run in `bypassPermissions`, but it never passes the flag and the
  file value is downgraded, so those sessions run in `default` today. R13
  keeps watch unchanged. The mismatch is tracked as a separate follow-up.
- **Guarding a live review against a concurrent `niwa apply`.** See Known
  Limitations.
- **Restoring the MCP trust-prompt skip for root and instance sessions** that
  `DESIGN-mcp-root-instance-distribution.md` relied on. R15 records that the
  decision's premise expired; choosing a replacement belongs to that design.
- **Moving remote control off `--settings`**, and building a shared builder
  for the inline settings document. This feature writes nothing new there,
  and R4 applies to the four on-disk documents only.
- **Containment for unattended workers.** The gap is named as a follow-up in
  `DESIGN-dispatch-permission-mode.md` and exists with or without this
  feature.
- **Other tools that write the same key**, and **the posture niwa generates
  for Codex**, which uses separate keys and a separate trust mechanism.
- **A real-Claude merge gate for the clobbering behavior.** It's Claude Code
  behavior and can only be observed with the real binary. An
  `@claude-integration` check can serve as a drift canary, but it depends on a
  secret that forked pull requests don't get, so it isn't an acceptance
  criterion.

## Known Limitations

- **A per-repo `ask` doesn't restrain a worker dispatched under an instance
  `bypass`.** Dispatch decides from the instance's effective posture, and the
  worker launches at the instance root, so a repo's `settings.local.json`
  never governed it. The flag also outranks the repo's `"default"` for any
  file the worker touches in that repo. A maintainer who wants dispatched
  workers to ask declares `ask` at the instance level (S5).
- **niwa can guarantee its bytes, not Claude Code's resolution.** R4 through R8
  are checked against what niwa writes. What Claude Code does with those bytes
  was measured on 2.1.267 and can change in a later release. The recognized set
  in R5 is dated for that reason.
- **Developers on Claude Code older than 2.1.257 lose file-borne bypass in
  sessions they start.** On those versions the written `bypassPermissions`
  still worked; R6 stops writing it. Dispatched workers keep bypass on any
  version through the flag. niwa declares no minimum Claude Code version, and
  it can't know a developer's version at apply time, so it can't write a
  version-conditional file. The guide points those developers to
  `--permission-mode`.
- **A `niwa apply` during a live review overwrites the review's settings.**
  Nothing stops apply from rewriting the instance root while a `niwa watch`
  review runs in it, which already drops the review's sandbox stanza and hooks
  today. After this change, a `bypass` instance's rewritten file falls through
  to the developer's own mode for the rest of the review, rather than the
  `default` it lands on today by accident. It's tracked as a follow-up with the
  hard-deny mismatch.
- **Some documents don't see every override.** A personal-overlay `ask` never
  reaches the workspace-root document, an `[instance]` override never reaches
  repo or worktree documents, and a worktree created outside apply picks up a
  personal-overlay value only at the next instance apply (S9).
- **Resume replay is inferred, not measured end to end.** Claude Code records
  the launch flags on each current niwa dispatch, and a reopened idle job
  replays them per the CLI's documented behavior. One real reopen hasn't been
  observed.
- **Jobs dispatched before niwa forwarded the flag lose bypass on respawn.**
  Their recorded launch flags carry no permission mode; they got bypass from
  the settings file, which no longer grants it.
- **`ask` overrides a developer's permissive personal setting in that scope.**
  That's the maintainer's explicit choice and the declared intent of `ask`,
  and it's the one place a generated value deliberately outranks the
  developer's own.

## Decisions and Trade-offs

- **`ask` produces `defaultMode: "default"` (closes the BRIEF's first open
  question).** The documented intent of `ask`, in
  `PRD-config-distribution.md` US2 and R10, is an opt-out from `bypass` that
  keeps approval prompting. An explicit `"default"` is honored from project
  scope and delivers that even over a permissive personal setting. The
  alternative, writing nothing, makes `ask` a no-op apart from cancelling a
  workspace `bypass`, which would leave a declaration that governs nothing,
  the class of defect this feature exists to remove.
- **An `ask` instance forwards no `--permission-mode`.** The instance-root
  document already carries `"default"`, which the worker honors. Forwarding
  `--permission-mode default` would duplicate it as a flag that outranks the
  file. Undeclared forwards nothing, as it does today.
- **A per-repo `ask` doesn't change what dispatch forwards.** Withholding the
  flag whenever any repo declared `ask` would disable unattended work for the
  whole instance because of one repo, which isn't what a per-repo opt-out
  says. The instance-level `ask` is the declaration that governs a dispatched
  worker, and the limitation is stated rather than hidden.
- **Sessions niwa doesn't start are owed accurate docs, not the posture
  (closes the second open question).** No session niwa starts runs at the
  workspace root, and niwa has no seam that could carry a flag to root or
  ephemeral sessions; the SessionStart hook runs after startup and can only add
  context. Stopping the permissive write there restores the developer's own
  posture, which is US2 applied at the root.
- **A resume counts as a session niwa starts (closes the third open
  question).** Every re-entry form is `claude attach <handle>`, which takes no
  options, so the requirement is placed on the launch command whose flags
  Claude Code preserves. This matches the existing rule for Codex dispatch,
  where every re-entry form returns the worker in its launch posture.
- **The declared posture, not a generated file, decides what dispatch
  forwards.** R3 supersedes the "read the materialized settings, never
  `workspace.toml`" requirement in `DESIGN-dispatch-permission-mode.md`. That
  requirement made a value Claude Code no longer honors the input to the one
  route that works, and let a stale or hand-edited file change a worker's
  posture.
- **`bypass` writes no permission mode rather than a substitute.** No value
  honored from project scope expresses bypass, and any permissive substitute
  such as `acceptEdits` would override the developer's own posture the same way
  the current value does.
- **Re-apply is the migration.** Apply already overwrites each generated
  document whole, so the next apply brings an existing instance into line
  without a separate migration step; R9 makes that behavior a requirement
  rather than an accident.
- **Accept the loss on older Claude Code.** A version-conditional file would
  need niwa to know each developer's Claude Code version at apply time, which
  it doesn't, and niwa declares no supported floor to cite.
- **Test rigor over test count.** R17 and R18 exist because the current tests
  would pass a broken change. The fixture-based derivation tests feed a value
  niwa doesn't produce, and per-location equality checks can't show that a
  value is absent everywhere or that a location exists at all.
