---
status: Draft
problem: |
  A user adopting a freshly-created GitHub repository as a niwa-managed
  workspace today must run three separate commands (`niwa init`, `niwa
  create`, `niwa apply`) AND hand-author `.niwa/workspace.toml` before
  niwa can help them. The friction discourages first-time adoption and
  means a new niwa user's first interaction is "edit a TOML file you've
  never seen before." There is no path that produces a usable niwa
  workspace from a fresh remote in one step.
goals: |
  Collapse the workspace bootstrap experience into a single command
  that lands the user inside a real niwa worktree with a scaffolded
  workspace config committed on a feature branch ready to push.
  Bootstrap serves as the intro experience for new niwa adopters and
  removes the chicken-and-egg between needing a workspace config to
  use niwa and needing to use niwa to know what the config looks like.
---

# PRD: init bootstrap from empty source

## Status

Draft

## Problem Statement

A user who creates a fresh GitHub repository and wants to manage it as
a niwa workspace today must:

1. Clone the empty remote outside any workspace.
2. Hand-author `.niwa/workspace.toml`, knowing the schema cold.
3. Push the configured remote.
4. Run `niwa init <name> --from <slug>` to materialize the workspace.
5. Run `niwa create` to provision an instance.
6. Run `niwa apply` (often) to install channel infrastructure.
7. Run `niwa session create <repo> <purpose>` to land in a worktree.

The friction is concentrated in the first interaction, when the user
knows niwa least. Step 2 demands schema knowledge that doesn't exist
yet for first-time adopters; steps 4-7 are independent commands the
user must sequence correctly. The chicken-and-egg is real: niwa's
value is in the workspace lifecycle it manages, but the lifecycle
won't start until the workspace.toml exists, and the workspace.toml is
the artifact the lifecycle is supposed to manage.

The affected audience is every first-time niwa user adopting a new
GitHub repository: solo developers starting a project, teams claiming
a new repo for a workspace-managed initiative, anyone whose first
contact with niwa is "I have a fresh repo and I want niwa to help me."

The trigger for acting now: niwa's onboarding surface is a recurring
adoption blocker, and the existing materialize path returns a generic
"no niwa config found" error that gives the user no path forward
beyond reading the manual.

## Goals

1. **One command, full setup.** `niwa init <name> --from <slug>
   --bootstrap` produces a usable niwa workspace, an instance, a
   committed scaffold, and a worktree the user can `cd` into — no
   intermediate manual steps.
2. **Self-revealing.** The standard output names every niwa artifact
   the command produces (workspace root, instance root, branch,
   worktree path, registry entry) so the user learns niwa's layout by
   reading the command's output.
3. **Recoverable.** Partial-failure leaves the user in a state where
   the existing standalone commands (`niwa create`, `niwa session
   create`, `niwa destroy`) can finish or reset the work.
4. **Adjacent-failure clarity.** Every failure mode the materialize
   path reaches (401, 403, 404, ambiguous markers, no-marker without
   `--bootstrap`) gets a case-specific Detail+Suggestion message
   instead of today's opaque wrap.
5. **No surprises.** The bootstrap pipeline run clones exactly the
   repo the user named — never the user's whole org by accident.
6. **No automatic push.** Bootstrap commits locally; the user controls
   when and whether the branch reaches the remote.

## User Stories

- **As a solo developer starting a new project**, I want to run one
  command against my just-created GitHub repository and end up inside
  a working niwa workspace, so that I can start coding without
  learning niwa's config schema first.
- **As a developer onboarding to niwa**, I want the bootstrap command
  to name every artifact it produces (workspace, instance, branch,
  worktree, registry), so that I learn niwa's layout by doing instead
  of by reading documentation.
- **As a team lead adopting a new repository in an established
  org**, I want bootstrap to clone exactly the repo I named — not
  every repo in my org — so that the first-run setup is fast and the
  bootstrap commit is reviewable.
- **As a user hitting an auth or 404 error during bootstrap**, I want
  the error message to tell me exactly what's wrong and how to fix
  it, so that I don't have to file an issue or read the niwa source
  to recover.
- **As a user who ran `--bootstrap` against the wrong slug**, I want
  the partial state on disk to be discoverable and tearable-down via
  `niwa destroy`, so that I can start over without surgery.

## Requirements

### Functional

**R1.** `niwa init <name> --from <github-slug> --bootstrap` shall
execute the lifecycle init → create → session-create as a single
chained command. On success, the user shall be left with a niwa
workspace at `<cwd>/<name>/`, an instance under that workspace, a
cloned bootstrap repo under the instance's group folder, a session
worktree at `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`, and a
committed bootstrap branch inside the worktree containing the
scaffolded `.niwa/workspace.toml`.

**R2.** `niwa init --from <github-slug> --bootstrap` (no positional
`<name>`) shall derive the workspace name from the slug's repo
basename. For example, `--from owner/foo --bootstrap` produces a
workspace at `<cwd>/foo/`. This behavior is scoped to `--bootstrap`;
today's behavior of `niwa init --from <slug>` (no positional, no
`--bootstrap`) materializing in cwd is unchanged.

**R3.** The bootstrap scaffold shall include `[channels.mesh]` as an
active section in `.niwa/workspace.toml`, with a one-line inline
comment explaining that bootstrap enabled it and how to remove it.
This ensures the create pipeline installs channel infrastructure
(without which session-create cannot land a worktree) and ensures
collaborators cloning the workspace inherit channels enabled.

**R4.** The bootstrap scaffold's `[[sources]]` block shall include
`repos = ["<bootstrap-repo>"]` to restrict the first pipeline run to
the single bootstrap repo. This allow-list ensures bootstrap remains
fast and predictable regardless of the source org's size. The user
removes the `repos = [...]` line later when ready to onboard the full
org.

**R5.** The bootstrap branch shall be named `niwa-bootstrap/<sid>`
where `<sid>` is the 8-hex-char session identifier produced by
`niwa session create`. The branch name shall be deterministic per
session-id, collision-safe across retries, and stored in session
state so push-hint warnings and future PR-creation flags can read it.

**R6.** The chained command's success criteria for each step shall
match the existing standalone command for that step. If
`niwa session create <repo> <purpose>` succeeds standalone, the same
sequence shall succeed inside `--bootstrap`. The chain shall not
introduce new success preconditions.

**R7.** The chained command shall apply stepwise rollback on failure:

- **init step fails**: remove `<cwd>/<name>/`, no registry entry. (Same
  as today's `niwa init` failure cleanup.)
- **create step fails after init succeeded**: tear down the instance
  directory (matches `niwa destroy --instance` behavior) and any
  daemons it spawned. Keep `<cwd>/<name>/.niwa/workspace.toml`, the
  workspace directory, and the registry entry so the user can retry
  with `niwa create`.
- **session-create step fails after create succeeded**: keep the
  instance intact. Session-create's own internal rollback (worktree
  add, branch, state file) covers session-step artifacts. The error
  message shall point at `niwa session create <repo> bootstrap` for
  retry.

**R8.** When `niwa init <name> --from <slug> --bootstrap` is invoked
against an existing target (workspace already exists, registry name
already in use, or `<cwd>/<name>/` is a non-niwa file/dir), niwa
shall refuse with bootstrap-specific Detail+Suggestion text. The text
shall name `niwa destroy <name>` as the canonical "start over" path
and, when an existing workspace is detected, also point at
`niwa session create <repo> bootstrap` as a way to land in a fresh
worktree without re-running init/create.

**R9.** When the source URL's host is not GitHub, niwa shall refuse
before any git invocation with an error stating "v1 supports GitHub
sources only; file a follow-up if you need `<host>`." No partial
state shall be written to disk.

**R10.** When the source remote returns HTTP 401 or 403, niwa shall
surface a case-specific Detail+Suggestion message naming the
`GH_TOKEN` scope guidance ("verify GH_TOKEN scopes; fine-grained PATs
need Contents: read, classic PATs need repo scope").

**R11.** When the source remote returns HTTP 404, niwa shall surface
a case-specific Detail+Suggestion message covering the three
plausible causes: slug typo, private repo without credentials, and
brand-new zero-commit repo. The message shall include the explicit
remediation "If the repo is brand new and has no commits yet, push
at least one commit (an empty README is enough) and retry with
`--bootstrap`."

**R12.** When the source remote contains both `.niwa/workspace.toml`
and a root-level `workspace.toml`, niwa shall surface the existing
`*config.AmbiguousMarkersError` remediation text (specifying both
file paths and the `--from <slug>:<subpath>` escape hatch).

**R13.** When the source remote is reachable but contains no niwa
config (the `*config.NoMarkerError` case), niwa's behavior shall
depend on the flag combination and stdin TTY state:

- TTY + `--bootstrap` set: proceed with bootstrap without prompting.
- TTY + neither flag set: prompt
  `"Remote has no .niwa/workspace.toml. Scaffold a minimal config and
  stage it on a niwa-bootstrap branch? [Y/n]"` and proceed only on
  explicit Y.
- TTY + `--no-bootstrap` set: fail-fast with the existing no-marker
  text plus the explicit-decline reason.
- Non-TTY + `--bootstrap` set: proceed with bootstrap without
  prompting.
- Non-TTY + neither flag set: fail-fast with "remote has no
  `.niwa/workspace.toml` and stdin is not a terminal; re-run with
  `--bootstrap` to scaffold."
- Non-TTY + `--no-bootstrap`: fail-fast with the explicit-decline
  reason.

**R14.** The scaffolded `.niwa/workspace.toml` shall include exactly
the following active sections, in this order:

1. `[workspace]` block with `name = "<derived>"` and
   `content_dir = "claude"`. No `default_branch` line.
2. `[[sources]]` block with `org = "<derived-from-slug>"` and
   `repos = ["<bootstrap-repo>"]`.
3. `[groups.<vis>]` block with `visibility = "<derived-from-Repo.Private>"`.
4. `[channels.mesh]` block with the inline comment described in R3.

A commented `[claude.content.workspace] source = "workspace.md"` hint
and a single schema-doc-link footer shall follow the active sections.

**R15.** The bootstrap scaffold shall write an empty
`.niwa/claude/.gitkeep` file so the content directory pushes cleanly
when the user later uncomments `[claude.content.workspace]`.

**R16.** The visibility value in the scaffolded `[groups.<vis>]`
block shall derive from the `Private` bool field returned by
`GET /repos/{owner}/{repo}`, never from the `Visibility` string field.
This is a security invariant against TOML-injection via a malicious
GitHub API host.

**R17.** When the visibility lookup fails (network error, auth error,
404 against the metadata endpoint), niwa shall default the scaffold's
group block to `[groups.public]` and emit a stderr `note:` line
explaining the fallback. Bootstrap shall NOT fail just because
visibility lookup failed.

**R18.** The bootstrap branch's commit shall use the user's normal
git identity from `user.name` / `user.email`. The commit invocation
shall not pass `--author`, shall not set `GIT_AUTHOR_*` or
`GIT_COMMITTER_*` environment variables, and shall use a fixed,
meaningful commit message ("Initial niwa workspace config").

**R19.** On full success, niwa shall print a prominent stderr block
matching the prominence of the existing `--rebind` warning at
`internal/cli/init.go:351-359`. The block shall include:

- The bootstrap branch name (`niwa-bootstrap/<sid>`)
- A `Worktree:` line with the absolute filesystem path of the session
  worktree.
- A `Workspace:` line with the absolute filesystem path of the
  workspace root.
- An `Instance:` line with the absolute filesystem path of the
  instance root.
- A "Next steps" section listing: inspect via `git show HEAD`, push
  via `git push -u origin niwa-bootstrap/<sid>`, then `niwa apply`
  (if the user makes further config edits before push) or skip
  directly to publish.

**R20.** The shell-wrapper landing-path mechanism shall direct the
user inside the session worktree (not the workspace root or instance
root) on success, so the user's `cd` lands them where the next
manual step (`git push`) will execute.

**R21.** The host-validation check (R9) shall run before any git
invocation inside the bootstrap orchestrator. Failed host check shall
not produce any partial state on disk and shall not invoke git.

**R22.** All git invocations within the bootstrap orchestrator shall
use `exec.CommandContext("git", args...)` with arguments as separate
elements. No shell, no string interpolation. The niwa-controlled args
(branch name pattern, commit message, fixed flags) shall be fixed
strings, not user-derived.

**R23.** The chained command's exit code shall be 0 on full success
and non-zero on any step failure. The non-zero exit shall be
accompanied by a stderr error message that identifies which step
failed and what state survives (per R7).

**R24.** niwa shall NOT push the bootstrap branch to the remote. The
user is responsible for `git push -u origin niwa-bootstrap/<sid>`
after inspection. Auto-push is out of scope for v1.

**R25.** A `--no-bootstrap` flag shall exist as a mutually-exclusive
partner to `--bootstrap`. Passing both shall produce a flag-validation
error matching the wording pattern at
`internal/cli/init.go:135-137` for `--overlay` / `--no-overlay`.

### Non-functional

**N1.** First-pipeline-run latency shall be dominated by the single
source-repo clone (per R4's allow-list), not by source-org-wide
discovery. Bootstrap shall complete in time proportional to one
shallow clone plus one session-create on the user's network.

**N2.** Adjacent failure-mode classification (R10, R11, R12) shall be
implemented via a typed `*github.StatusError` value carrying the
HTTP status code and an `errors.As`-based classifier seam at the
`runInit` materialize boundary. String-matching against error text
is not acceptable.

**N3.** Workspace-level error sentinels (`ErrSourceConfigMalformed`,
`ErrSourceAuthFailed`, `ErrSourceNotFound`) are deferred to a
follow-up. v1 ships typed GitHub status errors and case-specific
classifier output only.

**N4.** The bootstrap branch name format (`niwa-bootstrap/<sid>`) is a
durable user-facing contract. Future changes to the format shall be
treated as breaking.

**N5.** No new user secrets shall be written to disk by the
bootstrap path. The visibility lookup (R16) uses the existing
`resolveGitHubToken()` path; the token is not persisted.

## Acceptance Criteria

- [ ] **Happy path with positional name**: `niwa init my-project --from owner/my-project --bootstrap`
      against an auto-init repo produces (all on disk):
      - `<cwd>/my-project/.niwa/workspace.toml` matching the literal expected TOML body
      - `<cwd>/my-project/<instanceName>/.niwa/instance.json` (instance state, schema v4)
      - `<cwd>/my-project/<instanceName>/.niwa/roles/my-project/` directory (channels infra)
      - `<cwd>/my-project/<instanceName>/<group>/my-project/.git` (cloned source repo)
      - `<cwd>/my-project/<instanceName>/.niwa/worktrees/my-project-<sid>/` worktree
      - Bootstrap branch `niwa-bootstrap/<sid>` with exactly one commit authored by the
        user's configured git identity (not "niwa")
      - Registry entry pointing at the workspace root
      - Shell-wrapper landing-path file contains the worktree path

- [ ] **Happy path no positional name**: `niwa init --from owner/foo --bootstrap` lands the
      workspace at `<cwd>/foo/` with all the same artifacts as above.

- [ ] **401/403 auth error**: `niwa init bar --from owner/private-repo --bootstrap` against a
      private repo without GH_TOKEN produces the GH_TOKEN scope guidance message; no partial
      state on disk; exit non-zero.

- [ ] **404 missing repo**: `niwa init bar --from owner/nonexistent --bootstrap` produces the
      404 message naming all three causes (typo, private no-creds, zero-commit-empty); no
      partial state; exit non-zero.

- [ ] **404 zero-commit case**: `niwa init bar --from owner/empty-no-readme --bootstrap`
      against a brand-new zero-commit repo produces the message including "push at least one
      commit and retry."

- [ ] **Ambiguous markers**: `niwa init bar --from owner/has-both --bootstrap` against a repo
      with both `.niwa/workspace.toml` AND root `workspace.toml` produces the existing
      `*config.AmbiguousMarkersError` remediation.

- [ ] **Non-GitHub source**: `niwa init bar --from gitlab.com/owner/repo --bootstrap` refuses
      with "v1 supports GitHub sources only" before any git command runs; no partial state;
      exit non-zero.

- [ ] **TTY prompt happy path**: in a TTY, `niwa init bar --from owner/foo` (neither flag)
      prompts. User typing Y proceeds as if `--bootstrap` were set; resulting state matches
      the happy-path AC.

- [ ] **TTY prompt decline**: same as above but user types N. niwa exits clean (exit 0 or a
      well-defined "user declined" non-zero); no scaffolding runs; no partial state.

- [ ] **Non-TTY no-flag refusal**: in non-TTY (piped stdin), `niwa init bar --from owner/foo`
      fails-fast with "remote has no `.niwa/workspace.toml` and stdin is not a terminal;
      re-run with `--bootstrap`"; no partial state; exit non-zero.

- [ ] **--no-bootstrap suppression**: `niwa init bar --from owner/foo --no-bootstrap`
      produces the explicit-decline message; no scaffolding runs; no partial state.

- [ ] **Mutual exclusion**: `niwa init bar --from owner/foo --bootstrap --no-bootstrap`
      refuses with the "mutually exclusive" wording pattern; exit non-zero.

- [ ] **Stepwise rollback at init step**: a forced failure during init produces zero state
      on disk; no registry entry; no instance.

- [ ] **Stepwise rollback at create step**: a forced failure during create (e.g., source
      clone fails) leaves `<cwd>/<name>/.niwa/workspace.toml` and registry entry intact, no
      instance dir; error message points at `niwa create` for retry.

- [ ] **Stepwise rollback at session-create step**: a forced failure during session-create
      leaves the instance intact; error message points at
      `niwa session create <repo> bootstrap` for retry.

- [ ] **Idempotency / re-run conflict**: with a complete prior bootstrap on disk, re-running
      `niwa init <name> --from owner/foo --bootstrap` produces the bootstrap-specific refuse
      message naming both `niwa destroy <name>` and `niwa session create <repo> bootstrap`.

- [ ] **Visibility from Repo.Private**: an adversarial GitHub-API fixture returning
      `Private: true, Visibility: "public"` (mismatched) AND
      `Private: false, Visibility: "<toml-metacharacter>"` proves the scaffold derives
      `[groups.<vis>]` from the bool and never interpolates the string.

- [ ] **Visibility-lookup soft-fail**: a fixture returning a network error for
      `GET /repos/{owner}/{repo}` produces a scaffold with `[groups.public]` and a stderr
      `note:` line; bootstrap still succeeds.

- [ ] **Worktree label in success message**: success stderr contains a `Worktree:` line
      whose value is the absolute path returned by `git worktree list --porcelain` for the
      session branch.

- [ ] **Allow-list scoping**: against a source org with multiple repos, bootstrap clones
      exactly the bootstrap repo into the instance's group folder and no other repos.

- [ ] **Channels enabled in scaffold**: parsing the scaffolded `workspace.toml` confirms
      `Channels.IsEnabled() == true`.

- [ ] **Commit identity preserved**: the bootstrap commit's `author` and `committer`
      match `git config user.name` / `user.email` (asserted at the argv level: the commit
      invocation contains no `--author` and no `GIT_AUTHOR_*` / `GIT_COMMITTER_*` env
      override).

- [ ] **Host-check ordering**: a unit test using an injected exec invoker confirms that for
      a non-GitHub `src`, RunBootstrap records zero git invocations and returns the GitHub-
      only refusal.

- [ ] **Classifier ordering**: a table-driven test constructs error chains satisfying
      multiple classifier arms (e.g., wrapped `*NoMarkerError` whose inner cause is also
      `*StatusError{404}`) and asserts the classifier picks the most-specific arm.

## Out of Scope

- **Future flag for niwa to open a PR on the user's behalf.** Deferred to a follow-up
  feature. The v1 branch-name format (`niwa-bootstrap/<sid>`) is chosen specifically so
  this future flag can layer on top without renaming branches retroactively.
- **Zero-commit (truly empty) remotes.** GitHub returns 404 from the tarball endpoint
  for repos with no HEAD ref. v1 surfaces the case-specific hint asking the user to push
  a first commit; no `repos/get` disambiguation is added in v1.
- **Non-GitHub remotes.** Bootstrap is GitHub-only in v1 (R9). `file://`, GitLab, Gitea,
  and self-hosted SSH remotes get the existing raw `git clone` stderr.
- **Workspace-level error sentinels.** `ErrSourceConfigMalformed`,
  `ErrSourceAuthFailed`, and `ErrSourceNotFound` workspace-level sentinels are deferred
  to a follow-up. v1 ships the typed `*github.StatusError` and per-class classifier
  messages only.
- **Adopting an already-configured remote.** That's today's clone path
  (`niwa init <name> --from <slug>` against a remote with `.niwa/workspace.toml`) and
  stays unchanged. Bootstrap fires only on `*config.NoMarkerError` plus explicit user
  intent.
- **Auto-resume after partial-failure.** R7's stepwise rollback gives the user real niwa
  commands to retry from. A bootstrap-internal transactional log or auto-resume mechanism
  is deferred. If the user wants to retry, they invoke the documented next-step command.
- **Scaffolding more than one repo's group classification.** The user's bootstrap repo is
  classified into one group based on its visibility. Multi-group workspaces, fancy filter
  rules, and the `[repos.<name>]` per-repo override are out of scope; the user adds them
  by editing the scaffold after bootstrap.
- **TUI / interactive scaffold customization.** Niwa proposes the minimal-ideal scaffold
  non-interactively. The user edits the file in the worktree before push if they want
  something different.

## Decisions and Trade-offs

Seven decisions were made via `/shirabe:decision` during the PRD's scoping and discovery
phases. Each entry below names the decision, the alternatives considered, and why the
chosen option won. Full decision reports live in `wip/` during this branch's lifetime;
the summary here is the durable record.

### Decision 1: Lifecycle framing (T1 chosen)

**Chose**: T1 — `--bootstrap` chains init → create → session-create as one turnkey
command. The user lands inside `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/` on the
session branch when the command exits. Confidence: high.

**Rejected**: T2 (init scaffold + prompt user to run create+session) leaves no worktree
after `--bootstrap` returns, failing the worktree-visibility UX goal. T3 (init only,
defer everything) produces neither an instance nor a worktree — defeats the "intro to
niwa" framing. T4 (init + create, no session-create) requires the user to run
`niwa session create` themselves to land in the worktree; pays most of T1's cost
without T1's payoff.

### Decision 2: Source-org pipeline scope (S2 chosen)

**Chose**: S2 — the scaffolded `[[sources]]` block includes `repos = ["<bootstrap-repo>"]`
to restrict the first pipeline run to a single repo. The user removes the allow-list
later when ready to onboard the full org. Confidence: high.

**Rejected**: S1 (trust workspace.toml, clone everything in the source org) — behavior
depends on org size; surprise clones for 2-9 repo orgs and hard-fail above
`DefaultMaxRepos=10` at bootstrap time leave the user with a broken half-committed
branch. S3 (skip pipeline, clone directly) — introduces a parallel clone path that
diverges from the pipeline contract; the next `niwa apply` would discover the rest of
the org, only shifting the surprise. S4 (inline prompt at clone-count threshold) —
prompt-design surface is larger than the problem, niwa's only prompt precedent is
destructive-only, and behavior would be non-deterministic as the org grows.

### Decision 3: Workspace-name derivation (N1 chosen)

**Chose**: N1 — derive the workspace name from the slug's repo basename when no
positional `<name>` is given. `--from owner/foo` produces a workspace at `<cwd>/foo/`.
Confidence: high.

**Rejected**: N2 (require positional name; refuse without it) — imposes friction at the
worst moment and makes the intro command longer than `git clone`. N3 (derive from cwd
basename, like `git init`) — ties workspace identity to a movable filesystem path,
breaks when cwd is generic (`~/work/`). N4 (slug default + TTY prompt) — introduces
niwa's first interactive prompt for polish a positional override already provides.

### Decision 4: Channels behavior (C1 chosen)

**Chose**: C1 — the scaffolded `workspace.toml` includes `[channels.mesh]` as an active
section, with an inline comment explaining bootstrap enabled it and how to remove it.
Confidence: high.

**Rejected**: C2 (synthesize channels for the bootstrap pipeline run only) — collaborators
on fresh clones re-hit the synthesized-channels hint until somebody persists
`[channels.mesh]`; misframes the workspace as channels-off when bootstrap exists to
showcase channels. C3 (require `--channels` and refuse `--no-channels`) — session-create's
own `UNKNOWN_ROLE` already diagnoses the failure; C3 replaces a runtime error with a
flag-validation error for no behavioral gain. C4 (C1 + stderr note) — an inline comment
in the just-scaffolded file carries the same message in the artifact the user reads
first; a new notice channel is infrastructure for what a comment handles.

### Decision 5: Branch name format (B3 chosen)

**Chose**: B3 — `niwa-bootstrap/<sid>` where `<sid>` is the 8-hex-char session id.
Branch name is stored in session state for back-compat with an empty-field fallback to
`session/<sid>`. Confidence: medium.

**Rejected**: B1 (keep `session/<sid>`) — opaque internal-looking artifact in git
history forever. B2 (`niwa-bootstrap` deterministic) — cleanest name but bootstrap
retry collides and forces user to run `git branch -D niwa-bootstrap` manually. B4
(`niwa-bootstrap/<repo>`) — suffix is redundant with workspace name; inherits B2's
collision problem.

### Decision 6: Stepwise rollback contract (R2 chosen)

**Chose**: R2 — stepwise rollback. init-fail removes the workspace dir; create-fail
removes only the instance dir and keeps the workspace; session-fail keeps the instance
intact and points the user at `niwa session create <repo> bootstrap` for retry.
Confidence: high.

**Rejected**: R1 (full rollback on any failure) — overpunishes session-create failures
by discarding just-cloned repos. R3 (no rollback) — leaves a first-time user staring at
half-state. R4 (R2 + transactional log) — adds a parallel state machine for resume
benefits R2 already delivers via existing lifecycle commands.

### Decision 7: Multi-run idempotency (I3 chosen)

**Chose**: I3 — bootstrap-aware preflight error messaging. When the target name is in
conflict, refuse with bootstrap-specific Detail+Suggestion text pointing at
`niwa destroy <name>` for full reset and (for the workspace-exists case)
`niwa session create <repo> bootstrap` for fresh worktree. No silent auto-resume.
Confidence: medium.

**Rejected**: I1 (inherit preflight unchanged) — leaves actively misleading remediation
text ("use niwa apply" / "use --rebind") in place. I2 (auto-resume from failed step) —
silent on-disk introspection can misidentify contaminated state. I4 (resume +
interactive destroy prompt) — fat-finger keypress could wipe a workspace with unpushed
work.

## Known Limitations

- **Org membership requirement.** R4's allow-list approach requires the bootstrap repo
  to be in the same GitHub org as the `[[sources]] org = "..."` entry. For repos in
  forks or under a personal namespace, the user may need to edit the scaffold before
  pushing.
- **GitHub-only in v1.** Non-GitHub git providers must wait for a follow-up. R9 explicitly
  refuses non-GitHub sources rather than producing a half-working result.
- **No auto-push.** The bootstrap commit lives locally until the user pushes. If the user
  forgets to push, collaborators won't see the workspace setup. The success message
  (R19) names the push command explicitly to mitigate.
- **Session-id branch suffix.** The branch name carries an 8-hex-char suffix that has no
  meaning to a human reader. This is the cost of B3's collision-safety; the
  retention-forever cost is the same regardless of suffix.

## Downstream Artifacts

To be populated after PRD acceptance:

- Design doc: `docs/designs/DESIGN-init-bootstrap-empty-source.md` (will be revised
  against this PRD)
- Plan doc: `docs/plans/PLAN-init-bootstrap-empty-source.md` (will be rebuilt against
  the revised design)
- Implementation PR: this PR (`docs/init-bootstrap-empty-source` branch) once
  implementation lands
