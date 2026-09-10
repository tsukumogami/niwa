---
schema: prd/v1
status: Draft
problem: |
  Since Claude Code 2.1.257, the `bypassPermissions` value niwa writes into
  generated project settings for a `permissions = "bypass"` workspace doesn't
  take effect. It wins the settings merge and is downgraded to `default`,
  overriding the developer's own posture. The `ask` declaration writes
  `askPermissions`, which isn't a valid mode and makes Claude Code discard
  the whole generated file, and each file shows a permissions setting that
  governs nothing.
goals: |
  A workspace's declared posture reaches every session niwa starts through a
  route that takes effect, developers keep their own posture in sessions they
  open themselves, an `ask` declaration keeps a person in the loop, and every
  permission value in a generated settings file is one Claude Code honors
  from that file.
upstream: docs/briefs/BRIEF-inert-defaultmode-key.md
---

# PRD: inert-defaultmode-key

## Status

Draft

## Problem Statement

A workspace can declare how much its sessions may do before stopping to ask,
through `[claude.settings] permissions` in `workspace.toml`, with `"bypass"`
and `"ask"` as the accepted values and per-repo and per-instance overrides of
the workspace value. niwa turns the declaration into `permissions.defaultMode`
inside the settings documents it generates: the instance-root
`.claude/settings.json`, each repo's `.claude/settings.local.json` (and each
worktree's), and the workspace-root `.claude/settings.json`.

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

- A maintainer who declares `bypass` gets it in every session niwa starts for
  them, including a resumed one, without typing a flag.
- A developer who opens their own session inside an instance keeps the posture
  their own settings give them. This holds everywhere except a scope that
  declared `ask`, where the maintainer's explicit choice to keep a person in
  the loop wins by design.
- A maintainer who declares `ask` gets sessions that ask, and the rest of the
  generated settings in that scope take effect.
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
  bypass workspace, I want sessions in that repo to ask before acting and the
  repo's other generated settings (hooks, plugins, deny rules) to take effect,
  so that the opt-out keeps a person in the loop without silently dropping
  everything else.
- **US5.** As an operator running a supervised review, I want the review to
  keep asking for approval as it does today, so that the change doesn't
  reopen a path for unapproved actions.
- **US6.** As a maintainer who resumes a dispatched worker later, I want it to
  come back in the posture it was dispatched with, so that the resumed session
  doesn't stall on prompts the original never showed.

## Requirements

### Functional

- **R1. Bypass reaches dispatched workers.** When the effective posture of the
  instance a Claude worker is dispatched into is `bypass`, and the operator
  supplied no `--permission-mode`, the worker's launch command carries
  `--permission-mode bypassPermissions`.
- **R2. An explicit flag wins.** An operator-supplied `--permission-mode`
  reaches the worker exactly as typed, whatever the declared posture.
- **R3. The derivation tracks the declaration.** Whether dispatch forwards
  `--permission-mode bypassPermissions` is decided by the instance's effective
  declared posture: the workspace value after instance-level overrides and the
  personal overlay are applied. The contents of a generated settings document
  don't change that decision. A hand-edited or stale document that says
  `bypassPermissions` doesn't grant bypass, and a document that carries no
  permission value doesn't withhold it.
- **R4. No ignored value is ever written.** No settings document niwa
  generates carries a `permissions.defaultMode` of `bypassPermissions` or
  `auto`, for any declared posture, in any of the four locations: the
  instance-root `settings.json`, a repo's `settings.local.json`, a worktree's
  `settings.local.json`, and the workspace-root `settings.json`.
- **R5. Only recognized values are written.** Every `permissions.defaultMode`
  value niwa writes is a member of the set Claude Code honors from project
  scope, measured on 2.1.267: `default`, `acceptEdits`, `plan`, `dontAsk`.
- **R6. `bypass` writes no permission mode.** For a scope whose effective
  posture is `bypass`, the generated document carries no
  `permissions.defaultMode`. The posture reaches niwa-started sessions through
  R1, and sessions a developer opens themselves get the developer's own
  settings.
- **R7. `ask` writes `default`.** For a scope whose effective posture is
  `ask`, the generated document carries `permissions.defaultMode: "default"`.
- **R8. Undeclared writes nothing.** For a scope with no declared posture, the
  generated document carries no `permissions.defaultMode`, unchanged from
  today.
- **R9. Resume keeps the launch posture.** A worker re-entered through any
  command niwa builds or prints comes back in the posture its dispatch launched
  it with. Claude Code preserves a worker's launch flags for re-entry, so R1's
  flag on the launch command is what satisfies this. No re-entry command is
  required to carry a flag of its own.
- **R10. The supervised review is untouched.** `niwa watch`'s operator-approval
  posture still writes `permissions.defaultMode: "default"` into the instance
  root, and its verification still passes, on top of an instance materialized
  under every declared posture. A `niwa watch` review launch never carries a
  derived `--permission-mode`, since a command-line flag outranks the
  `"default"` the review writes.
- **R11. No reader of the retired value remains.** No niwa code path decides a
  posture by comparing a Claude Code permission mode read out of a generated
  settings document, except `niwa watch` verifying its own write. The unused
  reader that predates the current dispatch flow is removed.
- **R12. The committed docs stop promising the retired mechanism.** These
  documents are corrected:
  - `docs/guides/ephemeral-session-instances.md` stops saying a root-level
    bypass posture applies to every session launched at the root. It states
    that sessions a developer starts there, including ephemeral workers, get
    the developer's own settings, and that `--permission-mode` on the
    developer's own launch is the route to bypass.
  - `docs/guides/file-distribution.md` stops saying the setting "maps to
    Claude Code's `bypassPermissions` mode".
  - `docs/designs/current/DESIGN-workspace-root-claude.md` marks its
    `bypassPermissions` experimental finding as superseded.
  - `docs/designs/current/DESIGN-mcp-root-instance-distribution.md` records
    that its MCP trust-prompt decision rested on the retired behavior.
  - `docs/designs/current/DESIGN-agent-capability-contract.md` notes that
    Claude's posture now travels on the dispatch flag.
  - `docs/designs/current/DESIGN-dispatch-permission-mode.md` corrects the
    version to 2.1.257 and records that its "read the materialized settings,
    never `workspace.toml`" requirement is superseded by R3.
  - The comment block above the permission-mode scenarios in
    `test/functional/features/dispatch.feature`.

  Archived designs and existing PRDs are historical records and aren't
  edited.

### Non-functional

- **R13. No configuration change for existing workspaces.** Every existing
  `workspace.toml` that declares `permissions = "bypass"` or `"ask"` at any
  level keeps working with no edit. No declaration key is added, renamed, or
  removed.
- **R14. Derivation tests start from a declaration.** Every test that asserts
  what dispatch forwards starts from a `workspace.toml` declaration run through
  real materialization, never from a hand-written settings document.
- **R15. The document invariant is tested across the matrix.** R4 and R5 are
  asserted by a check that covers all four document locations under each of
  `bypass`, `ask`, and undeclared, rather than by per-case equality checks at
  single locations.

## Acceptance Criteria

- [ ] **AC1 (R1).** A `@critical` functional scenario: a workspace declaring
  `permissions = "bypass"`, `niwa dispatch <task> --detach` with no permission
  flag, and the recorded launch argv contains
  `--permission-mode bypassPermissions`.
- [ ] **AC2 (R2).** A `@critical` functional scenario: the same workspace,
  `niwa dispatch <task> --permission-mode acceptEdits --detach`, and the argv
  contains `--permission-mode acceptEdits` and doesn't contain
  `bypassPermissions`.
- [ ] **AC3 (R3).** A functional scenario: a workspace declaring `bypass` with
  `[instance.claude.settings] permissions = "ask"`, and the dispatched argv
  doesn't contain `bypassPermissions`.
- [ ] **AC4 (R3).** A functional scenario: a workspace with no declared
  posture, dispatch, and the argv doesn't contain `--permission-mode`.
- [ ] **AC5 (R3).** A test where the instance-root `settings.json` on disk
  carries `permissions.defaultMode: "bypassPermissions"` but the effective
  declaration is undeclared, and the forwarded argv doesn't contain
  `bypassPermissions`.
- [ ] **AC6 (R4, R15).** A test materializes a workspace under each of
  `bypass`, `ask`, and undeclared, and asserts that none of the four document
  locations carries `permissions.defaultMode` equal to `bypassPermissions` or
  `auto`.
- [ ] **AC7 (R5).** The same test asserts that every `permissions.defaultMode`
  value present is one of `default`, `acceptEdits`, `plan`, `dontAsk`.
- [ ] **AC8 (R6).** Under `bypass`, the same test asserts that no generated
  document carries a `permissions.defaultMode` key.
- [ ] **AC9 (R7).** Under a workspace `bypass` with one repo overridden to
  `ask`, that repo's `settings.local.json` carries
  `permissions.defaultMode: "default"` and the other repos' documents carry no
  `permissions.defaultMode`.
- [ ] **AC10 (R7).** Under `ask`, the generated document carries the other
  keys niwa writes for that scope (hooks, and the worktree-delegation deny
  rules where they apply) alongside `permissions.defaultMode: "default"`.
- [ ] **AC11 (R8).** Under undeclared, no generated document carries a
  `permissions.defaultMode` key.
- [ ] **AC12 (R9).** Every re-entry command niwa builds or prints for a Claude
  worker is `claude attach <handle>`, and AC1's launch argv is the dispatch
  that command re-enters. No test requires a flag on the attach command.
- [ ] **AC13 (R10).** A test materializes an instance under each of `bypass`,
  `ask`, and undeclared, applies `niwa watch`'s review settings in both its
  operator-approval and hard-deny postures, and asserts that verification
  passes and that the operator-approval posture leaves
  `permissions.defaultMode: "default"` in the instance root.
- [ ] **AC14 (R10).** A test asserts that a `niwa watch` review launch built
  for a `bypass` workspace carries no `--permission-mode` argument.
- [ ] **AC15 (R11).** `git grep WorkerPermissionMode` returns nothing, and no
  non-test code outside `internal/watch` reads
  `permissions.defaultMode` from a generated settings document to decide a
  posture.
- [ ] **AC16 (R12).** Each document listed in R12 no longer states that the
  generated setting governs a session's posture, and
  `DESIGN-dispatch-permission-mode.md` names 2.1.257.
- [ ] **AC17 (R13).** The existing functional suite passes with its
  `workspace.toml` fixtures unchanged, including every fixture that declares
  `bypass` or `ask`.
- [ ] **AC18 (R14).** No test that asserts on a forwarded `--permission-mode`
  writes a settings document by hand as its input.
- [ ] **AC19.** `go test ./...` and `make test-functional-critical` pass.

## Out of Scope

- **The declaration surface.** The `[claude.settings] permissions` key, its
  accepted values, and its override levels stay as they are (R13). Changing
  them is a compatibility decision for every existing workspace.
- **The declared posture in sessions a developer starts.** Interactive
  sessions, root sessions, and ephemeral workers launched from `claude agents`
  get the developer's own settings; niwa has no launch step that could pass a
  flag to them. The guide tells developers how to get bypass there (R12).
  The exception is an `ask` scope, where R7's `"default"` applies to every
  session.
- **Fixing `niwa watch`'s hard-deny posture.** Its code assumes review
  sessions run in `bypassPermissions`, but it never passes the flag and the file
  value is now downgraded, so those sessions run in `default`. R10 keeps watch
  unchanged. The mismatch is tracked as a separate follow-up.
- **Restoring the MCP trust-prompt skip for root and instance sessions** that
  `DESIGN-mcp-root-instance-distribution.md` relied on. R12 records that the
  decision's premise expired; choosing a replacement belongs to that design.
- **Moving remote control off `--settings`**, and building a shared builder
  for the inline settings document. This feature writes nothing new there.
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

- **niwa can guarantee its bytes, not Claude Code's resolution.** R4 through R8
  are checked against what niwa writes. What Claude Code does with those bytes
  was measured on 2.1.267 and can change in a later release. The recognized set
  in R5 is dated for that reason.
- **Resume replay is inferred, not measured end to end.** Claude Code records
  the launch flags on each current niwa dispatch, and a reopened idle job
  replays them per the CLI's documented behavior. One real reopen hasn't been
  observed.
- **Jobs dispatched before niwa forwarded the flag lose bypass on respawn.**
  Their recorded launch flags carry no permission mode; they got bypass from
  the settings file, which no longer grants it.
- **`ask` overrides a developer's permissive personal setting in that
  scope.** That's the maintainer's explicit choice and the declared intent of
  `ask`, and it's the one place a generated value deliberately outranks the
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
  posture. The alternative, keeping a file-borne signal under a new name, keeps
  the file in the decision path and is left to the design only insofar as R3's
  acceptance criteria still pass.
- **`bypass` writes no permission mode rather than a substitute.** No value
  honored from project scope expresses bypass, and any permissive substitute
  such as `acceptEdits` would override the developer's own posture the same way
  the current value does.
- **Test rigor over test count.** R14 and R15 exist because the current tests
  would pass a broken change. The fixture-based derivation tests feed a value
  niwa doesn't produce, and per-location equality checks can't show that a
  value is absent everywhere.
