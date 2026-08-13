---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-oss-no-infisical.md
milestone: "Degraded secret resolution"
issue_count: 5
---

## Status

Active

Single-pr mode, so the Draft to Active transition fired when authoring finished.
No GitHub issues or milestone are created; the outlines below are the
decomposition `/work-on` consumes.

## Scope Summary

Make instance materialization survive an absent or unauthenticated vault
backend: mark unresolved keys rather than erroring, omit them from generated
environment files with a recoverable record, report them once per command, and
return strictness as an opt-in workspace setting.

## Decomposition Strategy

**Horizontal.** The design's components have stable interfaces and one is a
prerequisite for everything else: nothing can report, omit, or conditionally fail
until a value can carry the reason it is unresolved. A walking skeleton was
considered and rejected — the end-to-end path here is not the integration risk,
because every component already exists and already runs on every apply. The risk
is in a data-model change rippling through four merge functions and 134 test
literals, which a thin vertical slice would expose no earlier than a horizontal
first issue does.

Two pairs must land together, and the outlines below are shaped so that each
issue is independently green rather than each issue being independently useful.
That distinction is why this plan is single-pr: see Implementation Sequence.

## Issue Outlines

### Issue 1: feat(vault): carry an unresolved reason on resolved values

**Goal**: Give a resolved value a nilable mark recording why it has no value, and
move the fatality decision from the resolver to the post-merge check.

**Acceptance Criteria**:
- [ ] `config.MaybeSecret` carries a nilable `Unresolved` field holding cause,
      declared level, declared description, and provider kind where one exists
- [ ] `String()` and `MarshalText()` return the zero rendering on a marked value,
      so the redaction promise is unchanged
- [ ] Both deep-copy paths carry the mark
- [ ] A sentinel distinguishes "client binary not installed" from other
      unreachability, and the two wrap sites set it separately
- [ ] A reference naming a provider the merged configuration does not declare is
      reclassified out of key-not-found, so it is not treated as a
      reachable-provider miss
- [ ] The workspace-overlay layer is not resolved twice
- [ ] The resolver marks instead of erroring for exactly four causes: key not
      found on a reachable provider, provider unreachable, client not installed,
      and undeclared provider. Malformed references, unsupported versions,
      undecodable bodies, and missing required fields stay hard errors
- [ ] The post-merge required-key check reads the mark and fails only for a
      required key whose cause is a reachable provider not holding it
- [ ] `AllowMissingSecrets` plumbing is deleted, including both CLI assignments
      and every test reference, so the tree compiles
- [ ] The per-reference opt-out path is untouched; both silent-downgrade tests
      pass unmodified

**Dependencies**: None

**Type**: code
**Files**: `internal/config/maybesecret.go`, `internal/vault/resolve/resolve.go`, `internal/vault/resolve/deepcopy.go`, `internal/vault/errors.go`, `internal/vault/infisical/subprocess.go`, `internal/workspace/required.go`, `internal/workspace/effective_config.go`, `internal/workspace/apply.go`, `internal/cli/apply.go`, `internal/cli/create.go`

### Issue 2: feat(keyreport): collect and render unresolved keys

**Goal**: Accumulate marks during a run and render one sorted report per command
surface, on both the success and failure paths.

**Acceptance Criteria**:
- [ ] A new `internal/keyreport` package holds the collector and renderers,
      importing only `config` from this codebase
- [ ] Accumulation is mutex-guarded, because per-repo materializers record
      concurrently
- [ ] `Report()` sorts by cause, then scope, then key, so ordering is independent
      of both map iteration and goroutine scheduling
- [ ] The report is assembled from two sources: marks carried on values, and
      keys declared in a requirement sub-table with no value at all, derived
      post-merge. A report built only from marks is empty for the
      no-provider case and fails this criterion
- [ ] The post-merge walk covers the optional sub-table as well as required and
      recommended, so the declared-level column is complete
- [ ] The collector threads through resolver options, effective-config options,
      and the applier as a caller-supplied field
- [ ] Every command surface renders after the run returns, including the paths
      that return an error
- [ ] The no-provider rendering names no repository and offers no remedy
- [ ] The unreachable rendering names the provider kind and gives a different
      remedy for an absent client binary than for other unreachability
- [ ] Renderers strip C0 and C1 control characters and the unicode line and
      paragraph separators from descriptions
- [ ] A prohibited-vocabulary test fails when a banned term reaches a report; the
      provider-kind token is exempt
- [ ] The duplicated apply error is reduced to one rendering

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/keyreport/keyreport.go`, `internal/keyreport/render.go`, `internal/vault/resolve/resolve.go`, `internal/workspace/effective_config.go`, `internal/workspace/apply.go`, `internal/cli/apply.go`, `internal/cli/create.go`, `internal/cli/root.go`

### Issue 3: feat(workspace): omit unresolved keys and record them in generated files

**Goal**: Stop writing unresolved keys as empty values, write a recoverable
record in their place, and stop promotion failing on the keys that omission
newly makes absent.

**Acceptance Criteria**:
- [ ] A marked key is omitted from generated environment files, never written
      with an empty value
- [ ] A dotenv record carries key, declared level, cause, and JSON-encoded
      description on one physical line, interleaved at the key's sorted position
- [ ] The key name is validated against the pattern the `.env.example` writer
      already uses; a key that fails is encoded or skipped, never interpolated
- [ ] A record is emitted in the target's own format for json and shell targets;
      the json file stays valid JSON and the shell file stays sourceable
- [ ] No record changes the parsed value of an adjacent assignment, verified with
      records placed immediately before and after an assignment and at the first
      and last lines of the file
- [ ] Both empty-value-map early returns admit "records exist", so a repo whose
      keys are all unresolved still writes a file
- [ ] A records-aware reader recovers key name and description from dotenv
      records; the existing reader signature is preserved by a wrapper
- [ ] Recovered records are treated as untrusted input and revalidated on read
- [ ] The promote path is three-way: promote, omit-and-report when the key is in
      the unresolved set, or keep today's hard error when it is neither
- [ ] The instance path derives its unresolved set from the marks already in the
      effective config, with no new field threaded
- [ ] The worktree path derives its unresolved set from records via the
      records-aware reader
- [ ] A later apply that resolves the key removes the record with no residue

**Dependencies**: Blocked by <<ISSUE:2>>

**Type**: code
**Files**: `internal/envformat/envformat.go`, `internal/workspace/materialize.go`, `internal/workspace/worktree_content.go`

### Issue 4: feat(config): add strict mode as a workspace setting and flag

**Goal**: Return fail-on-shortfall as an opt-in that reaches the unattended
paths, and that a visibility overlay cannot set.

**Acceptance Criteria**:
- [ ] `strict_secrets` is a tri-state field on the `[workspace]` table
- [ ] A `--strict-secrets` flag is registered on `create`, `apply`, and `init`
      through one shared registrar
- [ ] Precedence is: the flag when explicitly present on the command line,
      otherwise the setting, otherwise tolerant. `--strict-secrets=false`
      de-escalates a workspace that sets it
- [ ] Strictness is consulted beside the post-merge required-key check, turning
      every collected mark fatal under strict mode
- [ ] The promote branch gains a strict arm, since promotion runs later and
      per-repo
- [ ] A strict failure leaves no instance behind, matching today's semantics
- [ ] The unattended provisioning path reads the setting, so `dispatch` and the
      SessionStart hook honour it with no flag
- [ ] `reset`, `watch --once`, and reap honour it, and a test records this as
      deliberate
- [ ] A decode-only tombstone on the overlay type produces a warning when an
      author sets strict mode there, and the value does not take effect
- [ ] Strictness is never threaded into worktree re-materialization
- [ ] The stale comment describing a worktree secret-resolution tolerance that no
      longer exists is deleted
- [ ] On partial provisioning the SessionStart hook emits its structured output
      carrying the report and exits 0

**Dependencies**: Blocked by <<ISSUE:2>>, <<ISSUE:3>>

**Type**: code
**Files**: `internal/config/config.go`, `internal/config/overlay.go`, `internal/workspace/override.go`, `internal/workspace/apply.go`, `internal/workspace/materialize.go`, `internal/workspace/effective_config.go`, `internal/cli/apply.go`, `internal/cli/create.go`, `internal/cli/init.go`, `internal/cli/instance_from_hook.go`

### Issue 5: refactor(cli): retire --allow-missing-secrets and correct its descriptions

**Goal**: Turn the flag into a documented no-op and remove every description that
claims behaviour it no longer has.

**Acceptance Criteria**:
- [ ] `--allow-missing-secrets` remains accepted on `create` and `apply`, marked
      deprecated, and visible in help output with the deprecation notice
- [ ] It changes no outcome: exit code and generated files are identical with and
      without it, for both an absent-key shortfall and an unreachable-provider
      shortfall
- [ ] Passing it with `--strict-secrets` is rejected as a contradiction
- [ ] No description of the flag in the codebase claims removed behaviour,
      verified by a test that fails against the tree before this change. The
      scan excludes published PRDs and designs, which legitimately quote the old
      behaviour
- [ ] The provider-unreachable sentinel's doc comment no longer claims the flag
      consults it

**Dependencies**: Blocked by <<ISSUE:1>>

**Type**: code
**Files**: `internal/cli/apply.go`, `internal/cli/create.go`, `internal/vault/errors.go`, `docs/guides/vault-integration.md`

## Dependency Graph

## Implementation Sequence

**Critical path:** Issue 1 → Issue 2 → Issue 3 → Issue 4. Issue 5 branches off
Issue 1 and can land any time after it.

**Parallelization:** only Issue 5 is genuinely parallel. Issues 2, 3, and 4 are a
chain because each consumes what the previous produces — the collector needs the
marks, the record needs the collector's cause vocabulary, and strict mode needs
both the marks to turn fatal and the promote branch to attach its strict arm to.

**Why one PR.** No issue here delivers observable value alone. Issue 1 changes a
data model and reports nothing. Issue 2 reports keys that generated files still
write as empty values, which is a message contradicting the file beside it. Issue
3 is the first point where a contributor's workspace behaves differently, and it
depends on both. The requirements' headline outcome — a workspace materializes on
a host with no vault backend — is true only once Issues 1 through 3 are all in,
and Issue 4 is what keeps the existing guarantee for anyone relying on it. Split
across PRs, the intermediate states are each a coherent build with an incoherent
product: a tree that reports problems it does not act on, or acts without
reporting.

**Green-at-each-step, not useful-at-each-step.** The outlines are shaped so every
issue leaves the suite passing, which is what makes them reviewable in sequence
inside one PR. Issue 1 carries the deletions of the plumbing it obsoletes because
leaving them breaks the build; Issue 3 carries the promote fix because introducing
omission is exactly what starts firing the promote error.

**Between Issue 1 and Issue 3** the tree is deliberately in a non-conforming but
green state: marked values render as empty strings, so unresolved keys are still
written as blanks. That is today's behaviour minus the flag, and it is why Issue
3 is not optional before the PR is reviewable.

**Test blast radius.** Around fourteen existing tests change and roughly seven
are new. The ones worth knowing before starting, because they encode behaviour
this work deliberately inverts:

- The resolver tests asserting the default-is-an-error path, the warning text
  naming the flag, and that the tolerant flag does not rescue an unreachable
  provider. All three change in Issue 1.
- The resolver test asserting an unknown provider surfaces as key-not-found. Its
  expected sentinel changes in Issue 1, which is why that issue is not inert.
- The apply test asserting the tolerant flag downgrades. Its flag assignment goes
  in Issue 1; its file expectations change in Issue 3.
- The worktree test pinning that a promoted key missing from the clone is an
  error. It is the canary for Issue 3's promote fix — it fails the moment
  omission lands and passes again once the three-way branch is in.
- Two silent-downgrade tests, one in the resolver and one in apply, assert the
  per-reference opt-out stays quiet. Both must pass **unmodified** throughout.
  If either needs editing, the opt-out contract has been broken and the change
  is wrong.
- The test asserting the tolerant flag does not downgrade a required miss
  survives untouched, because strict-when-reachable keeps that guarantee.

Every test file referencing the deleted plumbing fields must be updated inside
Issue 1 or the package will not compile.
