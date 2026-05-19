<!-- decision:start id="init-bootstrap-channels-activation" status="assumed" -->
### Decision: How `--bootstrap` ensures channels are enabled for the create pipeline

**Context**

niwa's roles directory `.niwa/roles/<repo>/` is created by
`InstallChannelInfrastructure` (`internal/workspace/channels.go:241`),
which short-circuits to a no-op when `cfg.Channels.Mesh == nil`.
`niwa session create` hard-gates on that directory at
`internal/mcp/handlers_session.go:200-203` and returns `UNKNOWN_ROLE`
if it is absent. Decision T1 commits `--bootstrap` to running
init -> create -> session-create as one atomic chain, so the chain
fails on the session-create step unless `cfg.Channels.IsEnabled()`
returns true at the moment the chained create runs.

`--bootstrap` authors the scaffolded `workspace.toml` itself, so the
bootstrap path controls what gets written. The four channel-activation
mechanisms that exist today (`--no-channels` > `--channels` >
`[channels.mesh]` config section > `NIWA_CHANNELS=1`) can be combined
with the scaffold writer in four shapes: scaffold the section (C1),
synthesize channels for one run (C2), scaffold + reject `--no-channels`
(C3), or scaffold + emit a stderr note (C4).

The user framed `--bootstrap` as "intro to niwa's capabilities."
Channels are the niwa-distinctive feature — mesh task delegation, role
inboxes, session worktrees. An intro that turns channels off would be
self-defeating, and a scaffold that requires every collaborator to
re-enable channels manually after cloning compounds friction. The
scaffold is durable: it is committed and pushed on the bootstrap
commit, so anyone cloning the workspace inherits whatever the scaffold
encoded.

**Assumptions**

- The scaffolded `workspace.toml` writes `[channels.mesh]` as a compact
  two-line block: the header plus a short inline comment explaining
  that `--bootstrap` enabled it and how to remove it. If wrong: a bare
  header still works; the comment is pure UX polish.
- A future `--bootstrap --no-channels` shape that intentionally stops
  the chain after create (skipping session create) is not v1 scope.
  If wrong: this decision still permits `--no-channels` to override
  the scaffold for one invocation; session create's `UNKNOWN_ROLE`
  becomes the diagnostic that documents the truncated chain.
- Workspace.toml is the single source of truth that collaborators see
  when they clone a bootstrapped workspace. Personal global overlays
  (`~/.config/niwa/niwa.toml`) do not enable channels by default for
  fresh collaborators. If wrong, C1's collaborator-inheritance
  advantage shrinks but does not disappear.
- The Decision S2 "minimal-ideal, user-pickable sections stay
  commented" convention has a justified exception for sections that
  `--bootstrap` depends on. `[channels.mesh]` qualifies under
  Decision T1's turnkey chain.

**Chosen: C1 (Scaffold includes `[channels.mesh]`)**

The `workspace.toml` written by `--bootstrap` declares `[channels.mesh]`
as an active, uncommented section (with an inline comment explaining
its purpose and removal path):

```toml
[channels.mesh]
# Enabled by `niwa init --bootstrap`. Required for session worktrees
# and the niwa mesh. Comment out or delete to opt out.
```

The bootstrap-internal create pipeline reads this just-scaffolded
config and sees `cfg.Channels.Mesh != nil`, so
`resolveChannelsActivation` returns no synthesis (priority rule 3),
no one-time hint fires, `InstallChannelInfrastructure` installs the
roles layout, and the chained `niwa session create` passes the
role-directory gate. Future `niwa apply` runs against the workspace
— on any machine, by any collaborator — inherit channels enabled
because the section is durable and was pushed with the bootstrap
commit. `--no-channels` is still accepted on `--bootstrap` (no hard
error), but it will cause the chained session create to fail with
`UNKNOWN_ROLE`. The PRD documents this as the natural diagnostic
when a user has explicitly opted out of the feature that bootstrap
exists to showcase.

**Rationale**

C1 is the only option that puts the channels-on bit in the artifact
whose purpose is to encode the bootstrapped workspace's shape. The
scaffolded `workspace.toml` is what every collaborator reads when
they clone the repo. Encoding "this workspace uses channels" in that
file is direct; encoding it in the bootstrap CLI invocation (C2)
forces every collaborator to rediscover the choice through trial,
error, or out-of-band documentation.

C2's collaborator-inheritance regression is the strongest evidence
against the synthesis approach. The one-time hint fires per workspace
instance, not per workspace, so each fresh clone re-emits the hint on
first apply until the user persists `[channels.mesh]`. The user who
ran `--bootstrap` only sees the hint once on their machine, but their
collaborators each pay the same hint-and-persist cost — the friction
is paid N times where N is the team size, asymmetrically against the
user who didn't make the choice. C1 pays the persistence cost once,
at bootstrap time, by the user who is already configuring the
workspace.

C3's hard error on `--bootstrap --no-channels` provides no safety
benefit over C1's soft override. Session create's `UNKNOWN_ROLE` is
already a clear, contextual diagnostic when the role directory is
missing. Replacing a runtime error with a flag-validation error
trades one explicit message for another with no behavioral
difference at the failure point. C3 can be added later as a
flag-combination polish; doing so first preempts a recoverable case
without evidence the soft-override UX is wanting.

C4's stderr note duplicates information that an inline comment in
the just-scaffolded `workspace.toml` carries more naturally. The
user lands in the workspace directory immediately after bootstrap
(Decision T1) and the comment lives in the file they read first.
Adding a new one-time notice channel for the same message is
infrastructure for marginal educational value.

The Decision S2 "user-pickable sections stay commented" convention
admits a justified exception here: channels are user-pickable in
principle, but `--bootstrap` exists to showcase the features
channels enable. The scaffold's "minimal-ideal" goal is preserved
for every other user-pickable section — `[channels.mesh]` is the
one section bootstrap depends on, and the inline comment makes the
removal path explicit.

**Alternatives Considered**

- **C2 (Synthesize channels for the bootstrap pipeline only)**:
  Scaffold omits `[channels.mesh]`; bootstrap injects channels-on
  for the in-process create. Rejected because future `niwa apply`
  runs against the same workspace — by collaborators on fresh clones
  or by the user on other machines — see no `[channels.mesh]` and
  re-hit the synthesized-channels hint until somebody persists the
  section. The friction is paid N times across the collaborator
  surface, asymmetrically against people who didn't make the choice.
  C2 also misframes the workspace.toml: the artifact says "channels
  off" while the workspace was bootstrapped specifically to showcase
  channels.

- **C3 (Reject `--bootstrap --no-channels`)**: C1's scaffold plus a
  flag-validation error when both flags are passed. Rejected because
  session create's `UNKNOWN_ROLE` already diagnoses the failure
  clearly at the chain step where the conflict materializes. C3
  replaces a runtime error with a flag-validation error for no
  behavioral gain, and preempts the recoverable case where a user
  passes `--no-channels` intending to truncate the chain at create.
  Adding the flag-combination check later is a one-line change if
  the soft-override UX proves wanting.

- **C4 (C1 + stderr note)**: C1 plus a one-time `note:` to stderr
  explaining channels are enabled by default. Rejected because an
  inline comment inside the scaffolded `workspace.toml` carries the
  same message in the file the user reads first after bootstrap.
  A new notice channel and disclosure-tracking code path is
  infrastructure for what a comment line handles. C4 can be added
  later if comment-only proves insufficient in practice.

**Consequences**

What gets easier:

- The bootstrap-internal create pipeline works without special-casing
  the channels flag inside the bootstrap implementation. The create
  step reads the just-scaffolded config like any other create, sees
  `[channels.mesh]`, and proceeds through `InstallChannelInfrastructure`
  exactly as it would for a hand-authored config.
- Collaborators who clone the bootstrapped workspace inherit channels
  enabled with zero per-machine setup. `niwa apply` Just Works on
  every clone, no `NIWA_CHANNELS=1` required, no one-time hint to
  acknowledge.
- The scaffolded `workspace.toml` becomes self-documenting: the
  presence of `[channels.mesh]` with its inline comment teaches the
  reader what the section does and how to opt out, all in the same
  glance.
- Future Decision T1-derived chained commands (`niwa session create`
  for the session step, plus any future PR-creation flag layered on
  session create) inherit a config that already enables the
  infrastructure they need.

What gets harder:

- The scaffold writer must produce `[channels.mesh]` as an active
  section, not a commented placeholder. This is a small scaffold-
  template change but it must be tested: a functional test must
  verify the scaffolded `workspace.toml` parses with
  `cfg.Channels.Mesh != nil` and that the bootstrap-internal create
  installs the roles directory without synthesis (no one-time hint
  in the bootstrap output stream).
- The PRD must explicitly call out that `--bootstrap --no-channels`
  is a valid combination but causes the chained session create to
  fail with `UNKNOWN_ROLE`. The bootstrap output stream should
  format the failure clearly: "session create cannot proceed
  because --no-channels was passed; run `niwa session create
  <repo> <purpose>` after enabling channels via `[channels.mesh]`
  in workspace.toml." This is documentation and error-message work,
  not new code.
- The S2 allow-list scaffold convention now carries one named
  exception. The PRD should record `[channels.mesh]` as the only
  active section bootstrap writes by default (alongside whatever
  other top-level sections S2 mandates), and the rationale for the
  exception (Decision T1's chain depends on it).
- Removing channels from a bootstrapped workspace is a deliberate
  one-line edit to `workspace.toml`, not a no-op. This is the
  inverse of the symmetric reversibility note: both directions
  cost one line, but C1 starts users on the channels-on side, which
  is the side bootstrap exists to put them on.
<!-- decision:end -->
