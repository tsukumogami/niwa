# Completeness Review

## Verdict: FAIL

The PRD is well-structured and covers the seven settled decisions, but it leaves several implementer-facing behaviors under-specified (interaction with sibling init flags `--overlay`/`--no-overlay`/`--no-install-plugins`/`--rebind`/`--skip-global`, exit codes, stdout vs. stderr split, the literal scaffold body, the success-message format, `GH_TOKEN` unset handling) and has acceptance criteria that don't fully map back to requirements.

## Issues Found

1. **Interaction with sibling init flags not specified.** `niwa init` today accepts `--overlay`, `--no-overlay`, `--no-install-plugins`, `--rebind`, and `--skip-global`. The PRD never says whether `--bootstrap` is compatible with each, what overlay behavior does during bootstrap (does the bootstrap repo participate in overlay discovery? does `--no-overlay` carry into the chained create?), or what `--no-install-plugins` does to the chained pipeline run. Add a Flag-Interaction section enumerating: `--bootstrap` + `--overlay <slug>` (accept, pass-through to create), `--bootstrap` + `--no-overlay` (accept, suppresses convention-discovery), `--bootstrap` + `--rebind` (refuse, since R8 already refuses bootstrap on registry-collision), `--bootstrap` + `--skip-global` (accept, pass through), `--bootstrap` + `--no-install-plugins` (accept, pass through; rank-2 logic doesn't apply since bootstrap scaffold is always rank-1).

2. **`GH_TOKEN` unset behavior not explicit.** R10 names `GH_TOKEN` scope guidance for 401/403, but the PRD doesn't say what happens when `GH_TOKEN` is empty/unset. For public repos, anonymous access should succeed; for private repos, a 404 (not 401) is what GitHub returns to anonymous callers. R11's 404 message names the "private repo without credentials" cause, but R10 does not say "401/403 implies a token was sent but lacks scopes; 404 + GH_TOKEN unset implies anonymous against a private repo." Fix: add a clause to R10 or a new R10a clarifying token-presence states and the error path that applies in each.

3. **Exit-code convention is under-specified.** R23 says "0 on success, non-zero on any step failure" but doesn't distinguish step failures from each other. AC for the TTY-decline case ("exit 0 or a well-defined 'user declined' non-zero") is hedged. Define explicit exit codes: 0 success, 1 generic failure, 2 flag-validation error (matches existing pattern), and decide deterministically whether user-decline at the TTY prompt is 0 or non-zero. The current "or" in the AC will produce inconsistent behavior across implementations.

4. **stdout vs. stderr split is not specified.** R19 mandates a "prominent stderr block" on success but never says what (if anything) goes to stdout. Other init paths print `"Initializing from: <url>"` to stdout (cmd.OutOrStdout) and the success summary to stderr. Specify: progress lines per step (init done, create done, session done) to stdout vs. stderr; final block to stderr per R19; the user's `cd` target — does the shell-wrapper landing-path file go through a special channel, or is it stdout? An implementer can't pipe-and-grep the success output without this.

5. **Telemetry/logging is absent.** The PRD has no Observability section. Existing niwa commands emit notices, warnings, and `note:` lines through the `workspace.Reporter` abstraction. Bootstrap-specific events (rank-2 plugin install on the bootstrap repo, vault bootstrap pointer post-clone when `[vault.*]` is present in the scaffold — which it isn't but the init.go path checks anyway, visibility-lookup soft-fail) need to be enumerated so the implementer knows which reporter channels to invoke. Add a Notices & Observability section.

6. **The literal scaffold TOML body is "matching the literal expected TOML body" in AC but never appears in the PRD.** R14 enumerates four blocks in an order, but R3's inline comment, R4's `repos` line, and R14's footer schema-doc-link are all under-specified at the byte level. AC says "matching the literal expected TOML body" but the body is nowhere written down. Add a fenced TOML block to R14 showing the exact expected scaffold (with `<placeholder>` markers for derived values) so implementer and reviewer have a shared artifact to test against.

7. **R5 branch-name back-compat fallback is mentioned only in Decisions section.** Decision 5 says "back-compat with an empty-field fallback to `session/<sid>`." R5 itself doesn't mention this. Promote the fallback into R5 as a requirement so it's not lost when someone reads requirements without the trade-offs section.

8. **R7 rollback contract has implementation gaps.** "tear down the instance directory (matches `niwa destroy --instance` behavior) and any daemons it spawned" — but the chained command's create-step may spawn mesh daemons (per C1 channels-on). Specify: does the rollback wait for daemon shutdown? What if shutdown fails (timeout, stuck process)? Today's destroy has rollback semantics for these cases; cite them or restate.

9. **No requirement covers what `niwa apply` state the bootstrap leaves behind.** R1 says "init → create → session-create" is the chain, but the existing onboarding flow includes `niwa apply` (per Problem Statement step 6). The PRD doesn't say whether bootstrap implicitly runs apply (the description "channel infrastructure installed" implies create handles it) or whether the user is expected to run apply later. R19 mentions "then `niwa apply` (if the user makes further config edits before push)" but this is buried in a Next Steps list. Make it a requirement: "Bootstrap does not run `niwa apply`. Channel infrastructure for `[channels.mesh]` is installed by `niwa create`'s pipeline."

10. **AC has no negative coverage for R6 (success criteria match standalone).** R6 says "if `niwa session create <repo> <purpose>` succeeds standalone, the same sequence succeeds inside `--bootstrap`." No AC asserts this equivalence. Add an AC: "Functional test runs `niwa session create my-project bootstrap` against a workspace produced by bootstrap, and the operation succeeds without re-initializing state."

11. **AC has no coverage for R17 visibility-soft-fail logging.** R17 says "emit a stderr `note:` line explaining the fallback." The AC "Visibility-lookup soft-fail" mentions a `note:` line but doesn't assert the wording. Specify the literal note text (or a regexp) so the implementer doesn't guess.

12. **AC has no coverage for R18 commit message wording.** R18 mandates the literal "Initial niwa workspace config" commit message. AC "Commit identity preserved" checks author/committer but not message. Add: "Bootstrap branch tip commit's subject line is exactly `Initial niwa workspace config`."

13. **AC has no coverage for R20 shell-wrapper landing-path.** R20 says the shell wrapper directs the user inside the session worktree. AC "Happy path with positional name" mentions "Shell-wrapper landing-path file contains the worktree path" but the no-positional-name AC doesn't repeat it. Spot-check: R20 implies the absolute path goes through the wrapper file (likely `~/.niwa/cd-target` or similar) — name the mechanism so the AC can target it.

14. **AC has no coverage for R21 host-validation-before-git ordering at the integration level.** "Classifier ordering" AC and "Host-check ordering" AC both exist, but they're unit-level. Add an integration AC: "Running `niwa init bar --from gitlab.com/owner/repo --bootstrap` produces zero `.git` directories anywhere under cwd."

15. **AC has no coverage for R22 argv-injection invariant.** R22 mandates `exec.CommandContext` with separate args and no shell. No AC asserts this. Add: "A unit test injecting a slug like `owner/foo;rm -rf /tmp/test` confirms the slug never reaches a shell — it either fails slug-shape validation or appears as a single argv element."

16. **R13 ambiguity for TTY + `--bootstrap` + `--no-bootstrap`.** R13 enumerates flag combinations but `--bootstrap` + `--no-bootstrap` is handled by R25's mutual-exclusion check, which runs before R13's flow. R13 should explicitly cross-reference R25 to make the precedence clear.

17. **R8 "non-niwa file/dir" case is under-specified.** R8 says niwa refuses for "workspace already exists, registry name already in use, or `<cwd>/<name>/` is a non-niwa file/dir." The third sub-case (existing non-niwa directory) needs different remediation text than the first two — there's nothing to destroy. Specify the literal Detail+Suggestion for each sub-case.

18. **AC for happy path doesn't assert `.niwa/claude/.gitkeep` from R15.** R15 mandates `.niwa/claude/.gitkeep`. Happy path AC doesn't list it among on-disk artifacts. Add it.

19. **AC for "Channels enabled in scaffold" doesn't assert R3's inline comment.** R3 says the `[channels.mesh]` block has "a one-line inline comment explaining that bootstrap enabled it and how to remove it." AC only asserts `Channels.IsEnabled() == true`. Add: "the comment string matches the literal expected text."

20. **No AC for R6 negative case.** R6 says "The chain shall not introduce new success preconditions." No AC verifies the negation (i.e., that no new precondition was introduced). Add a regression-style AC: "If `niwa session create` works against a hand-authored workspace with the same scaffold body, it must also work after bootstrap; specifically, no precondition unique to the bootstrap path is exercised."

## Suggested Improvements

1. **Add a Flag-Interaction matrix.** Render a table with rows = sibling flags (`--overlay`, `--no-overlay`, `--rebind`, `--skip-global`, `--no-install-plugins`, `--no-bootstrap`) and columns = (compatible, refused, behavior change). This is the single highest-leverage clarification for an implementer.

2. **Inline the expected scaffold TOML.** Put the literal expected file inside R14 as a fenced code block with `<placeholder>` markers. The AC's "matches the literal expected TOML body" only works if the body is in the PRD.

3. **Define an Observability/Reporter section.** Enumerate the notices, warnings, and `note:` lines bootstrap emits, by step. Pattern after how PRD-niwa-init-creates-workspace-dir captures rank-2 plugin notices.

4. **Add explicit exit-code table.** 0 success, 1 step failure, 2 flag validation. Decide TTY-decline = 0.

5. **Add a "Bootstrap does not run apply" requirement.** This is implied by the chain shape but worth pinning down explicitly so a future implementer doesn't add an apply step "for symmetry."

6. **Cross-reference R13 from R25.** A reader hitting R13 first needs to know R25's mutual-exclusion check runs upstream.

## Summary

The PRD captures the seven settled decisions cleanly and the requirements list is dense, but several implementer-facing concerns are missing: flag interactions with sibling init flags, exit codes, stdout/stderr split, telemetry, `GH_TOKEN`-unset behavior, the literal scaffold TOML body, and the back-compat branch-name fallback. AC has good coverage of the happy-path and adjacent-failure modes, but skips several requirements (R6 equivalence, R15 .gitkeep, R18 commit message, R22 argv-injection, R3 inline comment wording). Recommend a Phase 5 revision pass to inline the scaffold body, add a Flag-Interaction matrix, and add the missing ACs before declaring the PRD ready for design.
