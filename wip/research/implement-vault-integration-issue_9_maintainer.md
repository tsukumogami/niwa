# Maintainer Review — Issue #9 (public-repo plaintext-secrets guardrail)

Commit: `a3ee5b6d63c03216290cef1d9c79297328a4e2ac` on `docs/vault-integration`.

Files reviewed:
- NEW: `internal/guardrail/githubpublic.go`
- NEW: `internal/guardrail/githubpublic_test.go`
- MODIFIED: `internal/workspace/apply.go`

## Verdict

**Non-blocking.** The change is ready to land from a maintainability standpoint.
No misread risks were found that would cause the next developer to form a wrong
mental model. A handful of advisory nits below would make a clear implementation
slightly clearer, but none of them rise to blocking.

## Checklist walk-through

### 1. Error message tells the user exactly what to do

Yes. The error (`CheckGitHubPublicRemoteSecrets`, lines 233–248) is structured:
it names the remote, enumerates the offending keys, then gives two next steps
in order — move values to `vault://<key>` (primary path) and
`--allow-plaintext-secrets` (escape hatch). It also points at
`niwa status --audit-secrets` for the enumeration case. The flag is correctly
described as one-shot in the error text.

Assertions in
`TestCheckGitHubPublicRemoteSecretsOriginPrivateUpstreamPublic` pin all four
pieces of that contract (remote named, key named, no secret value leaked,
vault migration + escape hatch mentioned). A future developer who accidentally
rewrites the error text into something vaguer will break the test.

### 2. `--allow-plaintext-secrets` warning notes the one-shot semantics

Yes (lines 221–229): "`--allow-plaintext-secrets` is one-shot — next
`niwa apply` will re-check." `TestCheckGitHubPublicRemoteSecretsAllowsPlaintextOneShot`
asserts on the literal substring "one-shot" and
`TestCheckGitHubPublicRemoteSecretsOneShotReevaluates` verifies the behavior
by running the guardrail twice.

### 3. No-git-tree warning is clear about what the user lost

Yes, but see advisory below. The stderr message is
`"warning: no git remotes detected; public-repo guardrail skipped"`. The
second clause ("guardrail skipped") is the load-bearing piece that keeps this
from looking like silent approval, and the test pins that exact substring.

Advisory: the helper `enumerateGitHubRemotes` returns `haveGit=false` for
both "not a git tree" AND "git tree with zero remotes" (lines 83–90). The
variable name `haveGit` therefore misrepresents one of the two cases — a
directory with `.git/` but no remotes is a git tree. The current call-site
message ("no git remotes detected") papers over this because that phrasing is
accurate for both cases. But if someone later adds a second caller, they may
read `haveGit=false` as "not a git tree" and write the wrong branch. Consider
renaming to `haveRemotes` or splitting into `(haveGit, remotes)` returns. Not
blocking — the single current caller reads correctly.

### 4. Regex comments explain case-insensitive + literal `\.`

Yes for case-insensitive (lines 19–25 and 30–33 both spell out the `(?i)`
rationale and the host-only scope). The `\.` is not called out explicitly, but
its intent is obvious from reading the URL it constructs (`github\.com`) and
from the explicit list of "would match without literal dots" cases enumerated
in the doc block ("no GHE, no case variants of other domains"). This is fine —
not every regex metacharacter needs a standalone comment when the surrounding
prose already explains "we match github.com only". Non-blocking.

### 5. GoDoc on the public API explains the R14/R30 contract

Yes, and this is the strongest part of the file. The `CheckGitHubPublicRemoteSecrets`
doc comment (lines 171–199) spells out:
- the R14/R30 requirement being enforced;
- both flag branches (allow on, allow off) and what each emits;
- the one-shot contract ("The flag is one-shot by contract — no state is
  written, so the next apply re-runs the check");
- the no-git-tree fallback and explicitly that it is NOT silent;
- the v1 scope boundary (github.com only, GHE deliberately excluded);
- the R22 no-secret-bytes guarantee.

The package doc (lines 1–6) additionally explains the "each guardrail is its
own call site" pattern, which helps the next developer know where to add a
second guardrail.

The call-site comment in `apply.go` (lines 351–360) is also excellent: it
documents *why* the pre-resolve `cfg` (not `resolvedCfg`) is passed, which
is the kind of subtlety that would otherwise become a landmine when someone
later refactors the resolve/merge ordering.

### 6. Test names describe user-observable behavior

Mostly yes. Good ones:
- `TestCheckGitHubPublicRemoteSecretsAllowsPlaintextOneShot`
- `TestCheckGitHubPublicRemoteSecretsOneShotReevaluates`
- `TestCheckGitHubPublicRemoteSecretsNoGitTree`
- `TestCheckGitHubPublicRemoteSecretsGithubEnterpriseNotFlagged`
- `TestCheckGitHubPublicRemoteSecretsOnlyWalksSecretsTables`
- `TestCheckGitHubPublicRemoteSecretsDeduplicatesRemotes`
- `TestOffendingKeysIgnoresDescriptionSubtables`

`TestCheckGitHubPublicRemoteSecretsOriginPrivateUpstreamPublic` is a slight
exception: the name describes the fixture (origin=private, upstream=public),
not the behavior under test. The behavior being asserted is "only the public
remote is flagged and only its keys appear in the error." A name like
`TestCheckGitHubPublicRemoteSecretsFlagsOnlyPublicRemotesInMixedSet` would be
more behavior-centric. Minor, advisory.

## Additional maintainability observations

### a. Test helpers in the test file

`initGitRepo`, `addRemote`, `runGit`, `newCfgWithPlaintextSecret`, and
`newCfgWithResolvedSecret` are well-commented about *why* they exist (env
scrubbing rationale, IsSecret() promotion semantics). A new test author
reading the file will know which helper to reach for.

### b. `offendingKeys` documents the "never walk description sub-tables" rule

Lines 125–126 in `githubpublic.go` plus the dedicated
`TestOffendingKeysIgnoresDescriptionSubtables` pin the `required/recommended/
optional` vs. `values` distinction. Without this, a future contributor
extending `EnvVarsTable` could easily flag description strings as offenders.
The comment + test combination catches that class of regression.

### c. Minor: `isGitHubPublicRemote` does a nil-URL fast path

Lines 58–62 return `false` for `url == ""`. The regex would also return false,
so the fast path is a micro-optimization. It's not misleading, so leave it —
but it's a non-load-bearing branch.

### d. Apply wiring is explicit and documented

The `AllowPlaintextSecrets` field on `Applier` (lines 35–43 in `apply.go`)
spells out the flag's semantics in a doc comment, including the one-shot
contract. The field addition and the call-site insertion in `runPipeline`
(lines 351–363) are both narrowly-scoped — no surprising side effects on
other pipeline steps.

## Summary

| Severity | Count | Notes |
|----------|-------|-------|
| Blocking | 0 | |
| Advisory | 3 | `haveGit` naming; one test-name nit; `\.` literal is implicit (fine) |

The code is easy to pick up cold. Doc comments explain contracts, not just
behavior; tests pin message substrings and one-shot semantics; the call-site
comment in `apply.go` captures the subtle "pre-resolve cfg on purpose" detail
that would otherwise be a future-refactor footgun.
