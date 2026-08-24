---
topic: codex-instance-root-skills
last_updated: 2026-08-23
phase_pointer: spawn_and_await
execution_mode: auto
home_pr: 271
settled_branch: docs/codex-root-skills
plan_doc: docs/plans/PLAN-codex-instance-root-skills.md
koto_session: execute-codex-instance-root-skills
exit:
exit_artifacts: []
child_snapshots: []
---

# /execute state: codex-instance-root-skills

## Phase 0 — complete

Slug `codex-instance-root-skills` re-validated against `^[a-z0-9-]+$`. The PLAN's
`execution_mode` re-validated as `single-pr`, so no coordinated-mode preflight was
run — that declaration carries no `mode:single-pr` record because every tool the
single-pr path needs is already `always`. No stale `parent_orchestration:` sentinel
was present at session start.

The cross-skill child template asserted clean
(`skills/execute/scripts/assert-child-template.sh`, exit 0).

## Phase 1 — drive

**Home PR adopted, not created.** This run entered on the `/scope` branch
`docs/codex-root-skills`, which carries the whole scoping chain. Rather than
opening a second PR on an `impl/` branch and orphaning those commits, the branch
was pushed and draft PR tsukumogami/niwa#271 opened on it, then adopted through the
`orchestrator_setup` override path. The settled branch is recorded in koto context
and read back verified, so `spawn_and_await` routes every child to it.

**Worktree discipline: impact `none`.** `git rev-list --count fc50683..origin/main`
returns 0 — main has not advanced since this branch's base, so there is nothing to
rebase onto and no upstream change that could touch the PLAN's foundation.
Classification written to `wip/work-on_codex-instance-root-skills_impact.json`.

**Children materialized.** Seven, one per PLAN issue, with the PLAN's dependency
edges: issues 1, 2 and 3 depend on nothing; 4 needs 1 and 3; 5 needs 3; 6 needs 2,
4 and 5; 7 needs 6.

They run **sequentially**, not in parallel. Every commit lands on one shared branch
in one worktree, so concurrent children would race on the same tree — which is why
the PLAN's own Implementation Sequence calls parallelization theoretical only.

### Issue progress

- [x] 1 — refactor(plugin): unhook internal/plugin from internal/workspace
- [x] 2 — feat(plugin): add the Claude plugin manifest to the embedded tree
- [x] 3 — feat(agentplan): add root skills and niwa-plugin leaf vocabulary
- [x] 4 — feat(workspace): register the root skills and niwa-plugin deliveries
- [x] 5 — refactor(cli): gate the dispatch warning on the payload-scope predicate
- [x] 6 — feat(agentplan): flip rows 18 and 19 for codex and bind both capabilities
- [x] 7 — test(functional): add root skills placement and discovery scenarios

## Drift observed, for the coordinator handoff

**shirabe's plan skill and its validator disagree about single-pr Dependency
Graphs.** The `/plan` skill's single-pr spec requires the `## Dependency Graph`
section populated with internal IDs; validator check FC14 raises a notice saying
a `single-pr` plan must not populate it and should either switch to `multi-pr`
or drop the diagram body. Both cannot be satisfied.

The child followed the skill spec and kept the mermaid diagram, accepting the
notice. This run removed the diagram and folded its full edge set into the
Implementation Sequence prose, because `single-pr` is not negotiable here (the
PRD requires both rows to flip in one change, and `/execute` accepts only
`single-pr` or `coordinated`), the validator then reports 0 errors and 0
notices, no required-section check fires on the removal, and one edge that had
existed only in the diagram is now stated in text.

Neither choice is wrong; the inconsistency is upstream in shirabe and is worth
an issue there rather than a local workaround repeated by every plan author.

## Issue 1 — verified

Commit 19089fc. All five acceptance criteria re-checked by this orchestrator
rather than taken on the child's word:

- `go list -f '{{join .Deps "\n"}}' ./internal/plugin | grep -c 'internal/workspace'`
  prints 0.
- `plugin.Install` takes the developer home as data and returns its `Action`;
  the always-nil state parameter is gone.
- `plugin.MaterializeTo` is exported at `internal/plugin/installer.go:191`.
- `internal/cli/plugin_adapter.go` is deleted and no non-test file references
  `InstallNiwaPlugin`.
- `go build ./...`, `go vet ./...` and the full `go test ./...` are green.

Editor diagnostics reported compile errors mid-run; they were from an
intermediate state and do not reproduce against the committed tree.

## Issue 2 — verified, and it caught a trap the chain had missed

Commit e7b8617. The BRIEF, PRD, design and plan all recorded that adding
`.claude-plugin/plugin.json` was mechanically safe because the installer copies
the whole tree and no test pins the file set. All of that is true and none of it
was sufficient: `//go:embed files/niwa` is a plain directory pattern, and Go's
embed silently drops every path element beginning with a dot.

Measured rather than reasoned. With the file added and the directive untouched,
walking `pluginFS` listed only `manifest.json` and the skill — the new manifest
was on disk and absent from the binary. Adding the `all:` prefix put it in.

Nothing downstream would have reported the loss, because the manifest the
installer actually reads is `manifest.json`, which embeds either way; the only
symptom would have been a session resolving the bare skill name.

Verified end to end afterwards: materializing the tree through the exported
`MaterializeTo` and symlinking it at a session's own `.codex/skills/niwa`
resolves **`niwa:niwa-migrate-config`** against the real binary, under an
isolated empty `CODEX_HOME`, with no credential and no model turn. That is the
name issue 7's live scenario asserts.

`TestEmbedded_CarriesThePluginManifest` reads the file back through `pluginFS`,
not off disk, so the regression it guards is the one that actually bites.

## Issues 3 and 5 — done in this session

The delegated agent for issue 3 hit a model session limit and returned nothing;
it left the tree clean, so there was no partial work to reconcile. Both issues
were implemented here instead.

**Issue 3** (commit 4c01f95) added `RootSkillsPlan`, `RootSkillsReconcileSpec`,
`NiwaPluginPlan`, `NiwaPluginTreeName`, `ConfigDocRepoScoped` and the four
delivery constants. All inert: a producer gated on an unimplemented capability
yields an empty plan, which the tests assert directly. One test skips by design
until row 18 flips, and says so rather than passing vacuously.

The root plan deliberately sets no `ExcludeAs`. That field feeds git-exclude
coverage for a path inside a working tree, and the helper consuming it searches
*upward* for an enclosing repository — so aiming it at an instance root that
happens to sit inside somebody's checkout would write into that repository's
exclude file. The omission is commented at the call site.

**Issue 5** (commit 411c55f) re-gated the dispatch warning. This had to land
before the flip, not after: the warning fired on row 18 being unimplemented, so
delivering row 18 would have silenced it while MCP servers, the session
environment and the posture were still missing. Verified both branches actually
run — the contract test exercises `claude` and `codex` subtests, and Codex warns
while Claude does not.

## CI failures fixed

Two checks were red on the first push, both from the docs chain rather than the
code.

`validate-docs` failed FC20: a reference to
the design's `current/` location named no file. It came from this run's own
scope state file, where Phase 1 had recorded that path as **absent** — the check
reads a written path as a live reference, so a record of an absence looked like
a pointer to a moved document. The glob results are now described in prose.

`pr-body` failed PB2: the body had no `---` separator between the squash commit
body and the reviewer context. Rewritten to the two-part convention and checked
offline with `shirabe validate --pr-body` before posting.

## Issues 4, 6 and 7 — done

**Issue 4** (`f58edc8`, `5fccace`) landed the executor side. Two conflicts the
outline had not anticipated came back from it, and both were real.

It proposed a `pendingDeliveries` staging map so the four new deliveries could be
registered before their bindings without tripping the binding check's reverse
direction. Declined, and the map reverted in `ce1aeed`. It was self-policing in
one direction but switched the "registered but nothing bound to it" check off for
exactly the four names this work adds — a hole in the one test that makes these
rows' claims falsifiable, inside the change whose purpose is submitting them to
it. The registrations moved to issue 6 alongside the bindings instead, which is
the declaration table's own rule and needs no new mechanism.

Its second finding was accepted: delivering niwa's plugin only on rank-2 config
detection would have row 19 declare a capability an ordinary apply never
delivers. It is a per-apply pass beside directory trust now. The consequence —
`plugin.Install` running every apply for Claude — is handled by routing its
notice through the one-time disclosure record, keyed on the outcome so a
workspace that opts out and back in still hears the new result.

**Issue 6** (`da8bab9`) is the flip, in one commit. Verified beyond the report:

- Both rows read implemented for Codex; all four bindings present; no
  declaration row carries `ReasonNotBuilt` any more (only the enum's own
  definition mentions it).
- The guide's gap list shrank **by regeneration**: both bullets and the whole
  "What niwa hasn't built yet" heading are gone, because the generator omits an
  empty group, and the drift test passes with no `-update` and no manual edit.
- The binding rule is load-bearing in **both** drift directions, proven on a
  scratch copy rather than argued. Reverting row 18 while its delivery stays
  bound fails with "is bound to delivery \"root-skills\" but is not declared
  implemented; something delivers a capability nobody declared". Deleting the
  registration while the declaration stands fails with "the contract binds
  (root-project-skills, codex) to delivery \"root-skills\", which nothing in
  internal/workspace registers".
- The PRD took a 26-line appended amendment with zero deletions, so the matrix
  body is untouched.

**Issue 7** (`a4e689f`) is the acceptance bar. Three offline `@critical`
scenarios cover placement, idempotence, reconcile-on-removal and both collision
cases; one `@codex-discovery` scenario carries discovery against the real binary.

The live scenario's control is the part that matters and it is built correctly:
it renders from one directory **below** the instance root, so the real populated
tree it just resolved sits immediately overhead, and asserts those skills are
absent. An empty-directory control could not tell "loaded from where the session
stands" from "the walk went somewhere else and found them anyway". It also
asserts each skill was read from a file under the instance root, which rules out
a same-named skill the machine happened to have elsewhere.
