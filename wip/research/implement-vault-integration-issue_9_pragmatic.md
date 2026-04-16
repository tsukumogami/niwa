# Pragmatic review — Issue #9 plaintext-secrets guardrail

Commit: `a3ee5b6d63c03216290cef1d9c79297328a4e2ac` on `docs/vault-integration`
Files reviewed:
- `internal/guardrail/githubpublic.go` (new)
- `internal/guardrail/githubpublic_test.go` (new)
- `internal/workspace/apply.go` (modified — 1 call site added)

## Verdict

**Approve.** No blocking findings. Two advisory notes below; neither justifies a rewrite.

## Heuristic pass

| Heuristic | Status |
|---|---|
| Single-caller abstraction | One minor hit (see A1) |
| Speculative generality | None — no unused params, no unused config fields |
| Impossible-case handling | None — every error path is reachable |
| Backwards-compat shims | None |
| Scope creep | None — changes match Issue #9 acceptance |
| Gold-plated validation | None — the `cfg == nil` guard in `offendingKeys` is cheap and the guardrail is an external entry point |

## Advisory findings

**A1. `remoteLineFields` is a single-caller helper with a dead return value.**
`githubpublic.go:46` — called only from `enumerateGitHubRemotes` at `:94`, and the caller discards the first return (`_, url := ...`). The helper is well-named and trivially inlineable (`fields := strings.Fields(line); if len(fields) < 2 { continue }; url := fields[1]`). Not worth blocking; the name does add readability. **Advisory.**

**A2. Two tests duplicate coverage the regex table already has.**
- `TestCheckGitHubPublicRemoteSecretsGithubEnterpriseNotFlagged` (:212) runs 6 URL variants end-to-end through a real `git init`. `TestEnumerateGitHubRemotesRegexSamples` (:470) already asserts the same classifier rejects `github.mycorp.com`, gitlab, bitbucket.
- `TestCheckGitHubPublicRemoteSecretsGithubComCaseInsensitive` (:239) overlaps with the case-insensitive positive cases in the same regex table.

Each redundant test spins a real `git init` subprocess, so the cost is non-zero. Tolerable: the two-level coverage (unit classifier + end-to-end dispatch through `git remote -v`) is a legitimate belt-and-braces pattern and these tests will fail loudly if the wiring regresses. **Advisory — do not block.**

## Simplicity check (focus items)

**16 tests — redundancy?** Minor overlap (A2), but the tests split cleanly into surface areas:
- 2 regex/classifier unit tests (one table-driven with 16 URLs, one offending-keys metadata filter)
- 1 enumerate-level unit test (missing git tree)
- 13 end-to-end tests covering: blocking error, one-shot flag path, one-shot re-evaluation, all four config walk sites (env, claude.env, repo override, instance override), vars-not-flagged, resolved-vault pass-through, no-remotes skip, no-public-remotes pass, dedup.

Each test asserts something the others don't, with two exceptions called out in A2.

**Regex adequacy.** `[\w.-]+` on org/repo slugs accepts the full set of legal GitHub handles (underscores, dots, hyphens). `(?i)` on hostname only — deliberate and documented. Anchored both ends. `user@` optional, `.git` optional, trailing `/` optional. Negative cases (`github.com/foo/bar` without scheme, `https://github.com/foo` without repo) are covered in the table. No over-engineering, no under-coverage.

**Git subprocess wrapper minimal.** `enumerateGitHubRemotes` (`:75-108`) is ~30 lines including comments and dedup. The actual subprocess shell is 4 lines: `exec.Command`, `Stderr = io.Discard`, `.Output()`, exit-status check. The `io.Discard` choice is justified in the comment (the guardrail emits its own skip warning, so git's stderr would be redundant noise). No wrapping, no retry, no timeout — correct for a subprocess that either works or doesn't.

## Scope discipline

The apply.go change is a single 3-line insertion at the correct point in the pipeline (pre-resolve, post-bundle-build), with a comment explaining why the guardrail must see the pre-resolve cfg. The comment is load-bearing — the resolver auto-wraps plaintext values, which would defeat the check if run post-resolve. This is exactly the right scope.

No refactors, no drive-by cleanups, no new helper modules, no interface extraction. The `guardrail` package is new but narrow: one exported function, designed so adding a future guardrail is a new call site, not a bit on a shared struct.

## Summary

Ship it. The code does exactly what the issue asks and no more; the one single-caller helper is defensible for readability; the two redundant tests are a small tax but preserve the belt-and-braces split between classifier and dispatch.
