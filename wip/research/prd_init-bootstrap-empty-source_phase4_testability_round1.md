# Testability Review

## Verdict: FAIL

Most acceptance criteria are well-scoped and assertable, but a cluster of GitHub-fixture-dependent ACs (auth/404/visibility/ambiguous-markers), under-specified message wording, and missing rollback-trigger fixtures block a writer from producing a complete test plan from the AC alone.

## Untestable Criteria

1. **"401/403 auth error" AC**: The criterion says "against a private repo without GH_TOKEN" but the niwa functional harness uses `localGitServer` (file://) and the GitHub-specific path uses `tarballFakeServer` (httptest). Neither helper is named here, and "private repo without GH_TOKEN" implies real GitHub access. -> Specify the fixture: "via `tarballFakeServer` returning 401 (and a separate row for 403) for `GET /repos/{owner}/{repo}/tarball/HEAD`" and assert exact stderr lines.

2. **"404 missing repo" AC**: "naming all three causes" is not testable as a single assertion — what string anchors are required for typo, private-no-creds, zero-commit? -> List the literal substrings the test must grep for (e.g., "If the repo is brand new and has no commits yet, push at least one commit ... retry with `--bootstrap`" plus one anchor per other cause).

3. **"404 zero-commit case" AC**: Says "against a brand-new zero-commit repo" but the test fixture for that is undefined — `localGitServer` can serve empty bare repos via `Repo(name)`, while the GitHub tarball path returns 404 for no-HEAD repos. Per R11 the message dispatch comes from GitHub 404; how does the test stage that with `tarballFakeServer`? -> Specify: "stage a `tarballFakeServer` 404 for repo `owner/empty-no-readme` and assert stderr contains the exact `push at least one commit` substring."

4. **"Ambiguous markers" AC**: References "the existing `*config.AmbiguousMarkersError` remediation" — testable only if the existing text is fixed and asserted by string. The AC does not name the substring to assert on. -> Capture a golden snippet (or name a constant) the test should match.

5. **"TTY prompt happy path" / "TTY prompt decline" ACs**: Functional tests run niwa as a subprocess with piped stdin (non-TTY). Driving a TTY requires `pty`-based wrapping not currently in the harness, per the guide. -> Either downgrade these to unit tests against the prompt-decision function with an injected `isTTY` predicate, or explicitly require a new pty helper.

6. **"TTY prompt decline" exit code**: "exit 0 or a well-defined 'user declined' non-zero" is ambiguous — pick one. -> Pin exit code to a single value (e.g., 0, or a specific code like `2`).

7. **"Visibility from Repo.Private" AC**: "an adversarial GitHub-API fixture returning `Private: true, Visibility: 'public'`" — the niwa harness does not document a `tarballFakeServer` knob for `GET /repos/{owner}/{repo}` metadata response. -> Specify the fixture endpoint and how to inject the bool/string mismatch; clarify whether the test runs against `tarballFakeServer` or a new fake.

8. **"Visibility-lookup soft-fail" AC**: "a fixture returning a network error" — `tarballFakeServer` is httptest, so network errors are harder to stage than status codes. -> Specify how (e.g., close the server before the call, or return 500; clarify that both qualify).

9. **"Worktree label in success message" AC**: Says "absolute path returned by `git worktree list --porcelain`" — the test would have to shell out to git in the same sandbox after niwa exits, which is fine, but the AC does not say whether to also assert prefix/format. Combined with R19 (multiline stderr block "matching the prominence of --rebind"), "prominence" is subjective. -> Specify the exact `Worktree: <path>` line format and the surrounding box/separator, not "prominence."

10. **"Allow-list scoping" AC**: "against a source org with multiple repos" — for a GitHub-only flow staged via `tarballFakeServer`, what does "multi-repo org" mean to the fake? The bootstrap path only clones the bootstrap repo per R4, but the AC speaks of an org. -> Reframe as: "assert that no clone command was issued for any repo other than the bootstrap repo, regardless of org content."

11. **"Commit identity preserved" AC**: "asserted at the argv level: the commit invocation contains no `--author` and no `GIT_AUTHOR_*`" — this is a unit-test-only assertion that needs an injectable exec recorder. The AC implies this exists but the PRD does not commit to that seam being present. -> Cross-reference the design seam (or add an explicit requirement that such a seam exists for testing).

12. **"Classifier ordering" AC**: References "the most-specific arm" without enumerating which arm is most specific. -> Spell out the ordering: `*config.AmbiguousMarkersError` > `*config.NoMarkerError` > `*github.StatusError{404}` > `*github.StatusError{401|403}` > generic, or whatever the chosen order is.

13. **"Mutual exclusion" AC**: "refuses with the 'mutually exclusive' wording pattern" — names a pattern but not the literal substring. -> Capture the exact wording (e.g., "flags --bootstrap and --no-bootstrap are mutually exclusive") to assert on.

14. **"Stepwise rollback at create step" / "session-create step" ACs**: "a forced failure during create (e.g., source clone fails)" — how does the test force the failure? The bootstrap orchestrator does not expose a fault-injection seam. -> Specify the trigger (e.g., point `[[sources]] repos` at a non-existent repo, or use an injectable invoker stub) and state which surface the test runs against.

15. **R19 success-block ordering**: The AC list checks for individual lines but does not assert their order or grouping. R19 says the block contains specific lines but doesn't pin the order, so two implementations could both "pass" while looking different to the user. -> Pin the line order and any "Next steps:" header text.

## Missing Test Coverage

1. **R2 unchanged-cwd-behavior preserved**: R2 says "today's behavior of `niwa init --from <slug>` (no positional, no `--bootstrap`) materializing in cwd is unchanged" — no AC asserts the non-bootstrap path was not regressed.

2. **R3 inline comment content**: R3 mandates "a one-line inline comment explaining that bootstrap enabled it and how to remove it" — no AC asserts the comment exists or matches expected text.

3. **R5 deterministic-per-session-id**: R5 says the branch name is deterministic per `<sid>` — no AC verifies that two invocations of the rollback-then-retry path with the same `<sid>` produce the same branch name (or that they explicitly cannot collide due to fresh `<sid>`s).

4. **R5 stored in session state**: R5 says the branch name is stored in session state — no AC asserts the state file contains the branch name field.

5. **R14 scaffold section ordering**: R14 mandates a strict order for `[workspace]`, `[[sources]]`, `[groups.<vis>]`, `[channels.mesh]`. The happy-path AC says "matching the literal expected TOML body" but does not include or reference that literal. -> Attach the golden expected body to the AC.

6. **R14 commented `[claude.content.workspace]` hint and schema-doc footer**: No AC asserts these exist in the scaffold.

7. **R15 `.niwa/claude/.gitkeep`**: No AC asserts the `.gitkeep` file is present.

8. **R17 stderr note for visibility soft-fail**: Covered by an AC, but the AC does not pin the exact `note:` line text.

9. **R20 shell-wrapper landing path**: Mentioned in the happy-path AC as "Shell-wrapper landing-path file contains the worktree path" but the file's location/name and the contract for the wrapper are not in the AC.

10. **R22 git invocation safety**: No AC asserts that bootstrap uses `exec.CommandContext` with split-args (no shell). The host-check ordering AC verifies no git ran in the host-refusal case, but does not assert the shape of git invocations on the happy path.

11. **R23 exit-code coverage on every failure step**: Several rollback ACs say "exit non-zero" without specifying the code. If exit codes carry meaning (per existing niwa conventions), they should be pinned.

12. **R24 no-push assertion**: No AC verifies that bootstrap does not invoke `git push`. The success block names the push command, but the test should assert absence.

13. **R25 mutual exclusion error wording**: Covered, but see Untestable Criteria #13.

14. **N1 latency budget**: "Bootstrap shall complete in time proportional to one shallow clone plus one session-create" — no AC pins a latency assertion or budget. (Acceptable to drop, but flag it explicitly.)

15. **N2 typed `*github.StatusError`**: No AC asserts the classifier is wired to `errors.As(*github.StatusError)` rather than string-matching.

16. **N5 no-secret-written**: No AC asserts the token never lands on disk in scaffolded files or state.

17. **Idempotency rerun — workspace-exists vs registry-name-in-use vs cwd-conflict**: R8 has three branches but the "Idempotency / re-run conflict" AC tests only one ("complete prior bootstrap on disk"). The registry-name-in-use-only and `<cwd>/<name>` is-a-file branches have no AC.

18. **Bootstrap-against-non-empty-but-niwa-configured remote**: Out of scope per the Out-of-Scope list, but no AC asserts that `--bootstrap` against a remote that *already* has `.niwa/workspace.toml` either falls back to the clone path or fails clearly.

## Summary

The PRD has 24 acceptance criteria covering the major happy paths and most documented failure modes, but a recurring pattern undermines testability: the AC say "against a private repo" or "against a brand-new zero-commit repo" without naming the test fixture (`tarballFakeServer` vs `localGitServer`), and they reference existing error messages by type without quoting the substring to assert on. Several requirements (R3 comment text, R14 section ordering and footer, R15 `.gitkeep`, R20 wrapper file contract, R22 no-shell invocation, R24 no-auto-push) have no corresponding AC at all, and the TTY-prompt ACs require a pty-driven harness the niwa functional suite does not currently document. The PRD is close — pinning literal expected stderr/stdout substrings, naming the fixture helper per AC, and adding ACs for R3/R14-footer/R15/R20/R22/R24 would flip the verdict to PASS.
