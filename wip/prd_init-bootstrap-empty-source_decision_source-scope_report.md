<!-- decision:start id="source-scope" status="assumed" -->
### Decision: Source scope during bootstrap pipeline

**Context**

When `niwa init --from <empty-remote> --bootstrap` runs the
create-pipeline as part of the turnkey flow, the pipeline walks each
`[[sources]]` entry's org and clones every repo it discovers. The
scaffolded workspace.toml proposed by `DESIGN-init-bootstrap-empty-source.md`
Decision 4 declares a bare `[[sources]] org = "<from-slug>"` with no
`repos = [...]` allow-list, so today's pipeline would discover and
clone the entire source org — which is fine for a brand-new
single-repo org but contradicts the "intro to niwa" framing the
moment the user is adopting niwa inside an established org with
multiple repos. The fresh-org and many-org cases have to share one
default. The decision is which posture bootstrap takes when the
user's preferred experience is "land the named repo, ready to drive,
nothing else."

The `[[sources]]` block already carries two relevant fields
(`internal/config/config.go:170-174`): `Repos []string` (allow-list,
short-circuits the GitHub API call when set) and `MaxRepos int`
(default 10, hard-fails discovery above the threshold). Both are
documented in `DESIGN-workspace-config.md` and used in
`PRD-workspace-visibility-overlay.md` as the canonical way to scope
a source to a known repo set. The mechanics are pipeline-native.

**Assumptions**

- The PRD extends bootstrap into a turnkey flow that immediately runs
  the create-pipeline against the scaffolded workspace.toml. If the
  flow stays "user pushes, user runs apply later," this decision is
  moot — the user reviews the workspace.toml before any auto-discovery
  fires.
- The `--from` slug identifies the bootstrap repo unambiguously and
  the user wants exactly that repo on the first run. The task
  framing makes this explicit.
- Established orgs adopting niwa onboard the rest of their repos via
  a follow-up workspace.toml edit (remove the `repos = [...]` line),
  not via a magic "expand the source" command. This matches the
  documented broaden-later workflow in
  `DESIGN-workspace-config.md`.
- The pipeline contract — that workspace state is a function of the
  parsed workspace.toml — is preserved.

**Chosen: S2 — Restrict via `[[sources]] repos = [<bootstrap-repo>]` in the scaffold**

The bootstrap scaffold emits the source block with an explicit
single-repo allow-list:

```toml
[[sources]]
org = "<org-from-slug>"
# repos = [...] scopes auto-discovery to a known set. Remove this
# line to onboard every repo in the org on the next `niwa apply`.
repos = ["<bootstrap-repo>"]
```

The pipeline's `discoverRepos` (`internal/workspace/apply.go:1726-`)
short-circuits on `len(source.Repos) > 0`, builds a one-element list
from the named repo, and never calls `ListRepos`. The bootstrap repo
lands in the group folder for its visibility classification, the
session worktree primitive has its `<instanceRoot>/<group>/<repo>/.git`
target, and the pipeline produces exactly the state the
workspace.toml describes — no drift, no surprise, no parallel code
path.

The implementation hook is a one-line addition to
`workspace.ScaffoldFromSource`'s emitted template plus the comment
explaining how to broaden. `ScaffoldOptions` already carries the
slug components needed to populate the allow-list.

**Rationale**

S2 is the only option that delivers a predictable one-repo
bootstrap across every org size with zero new pipeline code and a
workspace.toml that truthfully represents what the pipeline did:

- **Predictable.** Fresh org with one repo: one clone. Established
  org with five repos: one clone. Established org with five hundred
  repos: one clone. Behavior is invariant to org size, which
  matches the "intro to niwa" framing as a property of the
  bootstrap command rather than an accident of the source org's
  shape.
- **Reuses existing schema.** `repos = [...]` is not new surface.
  `DESIGN-workspace-config.md`, `DESIGN-explicit-repos.md`, and
  `PRD-workspace-visibility-overlay.md` all document and use it.
  Bootstrap emitting it is reuse, not novelty.
- **Preserves the pipeline contract.** Workspace state remains a
  function of workspace.toml. The user inspects the bootstrap
  branch, sees the allow-list, understands what was cloned and
  why, and edits to broaden when ready. No "next apply will
  suddenly clone twenty more repos" trap.
- **Avoids the `max_repos` hard-fail at bootstrap time.** A bare
  source against a 10+-repo org hard-fails today's pipeline with
  the threshold error; bootstrap inherits that failure under S1.
  The allow-list bypasses the threshold check entirely (the API
  call doesn't run), keeping bootstrap successful for orgs of any
  size.
- **Documentable transition path.** The "remove this line" comment
  is one sentence; the user's workflow to onboard the rest of the
  org is one edit plus `niwa apply`. This matches the existing
  broaden-later pattern niwa docs already cover.

The cost is one scaffold line and one comment, paid against
Decision 4's "minimal-ideal" goal. The line is a documented schema
feature with clear purpose, not a workaround.

**Alternatives Considered**

- **S1 — Trust workspace.toml, clone everything in the source org.**
  Rejected because behavior depends on org size: perfect for a
  fresh single-repo org, surprise extra clones for orgs in the 2-9
  range, hard-fail above the default `max_repos = 10` threshold.
  The "intro to niwa" framing only holds for one of three cases. A
  bootstrap that hard-fails on the user's established org leaves
  them with a half-committed bootstrap branch and a config error
  to diagnose — the worst UX of any option.

- **S3 — Skip `[[sources]]` discovery during bootstrap; clone the
  bootstrap repo directly via a one-off mechanism.** Rejected
  because it introduces a parallel clone path that diverges from
  the pipeline's contract. The bootstrap state on disk wouldn't
  match what `niwa apply` produces against the scaffolded
  workspace.toml — the next user-driven apply would clone every
  other repo in the org, shifting the surprise from bootstrap time
  to first apply rather than removing it. Implementation also
  duplicates state-write, ManagedFile, hook-injection, and content
  side-effects that today live inside `runPipeline`.

- **S4 — Inline prompt at a clone-count threshold.** Rejected
  because the prompt-design surface (threshold value, default
  answer, non-interactive bypass flag, CI behavior) is larger than
  the problem warrants, and niwa's only prompt precedent
  (`destroy`) is reserved for filesystem-destructive operations.
  Behavior would be non-deterministic from the user's perspective
  — the same `niwa init --bootstrap` command yields different
  workspace shapes as the org grows past the threshold. S2's
  invariance is preferable.

**Consequences**

- The bootstrap scaffold gains one active line (`repos = ["..."]`)
  plus a one-line comment explaining how to broaden. Decision 4's
  scaffold shape grows from 3 active lines to 4 plus a comment.
  `workspace.ScaffoldFromSource` and its `ScaffoldOptions` need no
  schema changes; the repo name is already implicit in the parsed
  source.
- The user's day-one workspace contains exactly one repo, in the
  group folder matching its visibility, ready for the session
  worktree primitive. The bootstrap PR (the user's first niwa PR)
  is reviewable.
- Onboarding the rest of an established org becomes a documented
  one-line edit (`repos = [...]` removal) plus `niwa apply`. The
  scaffold comment serves as the in-place pointer; the contributor
  guides at `docs/guides/workspace-config-sources.md` cover the
  fuller story.
- Pipeline code is unchanged. `discoverRepos`'s existing
  short-circuit at `apply.go:1729` handles the new scaffold output
  natively.
- A future enhancement — e.g., bootstrap-time prompt to broaden if
  the source org is small and the user wants the full org — remains
  possible. The S2 default is the conservative posture; a flag like
  `--bootstrap-scope=org` could opt into S1's behavior without
  changing the default.
<!-- decision:end -->
