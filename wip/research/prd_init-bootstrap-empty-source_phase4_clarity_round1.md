# Clarity Review

## Verdict: FAIL

The PRD is largely precise but contains several ambiguity hot spots — most notably R3's "one-line inline comment" without exact wording, R19's "prominent stderr block matching the prominence of the existing --rebind warning" without committing to an exact format, the unspecified-success-exit ambiguity in the TTY-decline AC, and unanchored phrases like "fixed, meaningful commit message" and "minimal-ideal scaffold" — that could lead two implementers to ship different artifacts and different test expectations.

## Ambiguities Found

1. **R3 ("inline comment explaining that bootstrap enabled it and how to remove it")** -> The PRD never commits to the exact comment string, only the topic. Two implementers will write two different comments, and neither has an objective pass/fail anchor at review. -> Replace with the literal expected string (e.g., `# Bootstrap enabled mesh; remove this section to disable.`) or attach a "comment must match this regex" rule. Even better: hoist the expected comment text into R14's literal-expected-TOML body referenced by AC1.

2. **R3 / R4 (semantic overlap with R14)** -> R3, R4, and R14 each partially describe the scaffold's contents. A reader has to merge them mentally and may miss that R14's "exactly the following active sections, in this order" overrides R3's looser framing. Two implementers could produce different orderings or extra blocks (e.g., `[claude]` block) and each defend their choice. -> Make R14 the single source of truth for scaffold contents and have R3/R4 cite R14 ("see R14") rather than restating partial requirements.

3. **R5 ("collision-safe across retries")** -> Ambiguous. Does "collision-safe" mean (a) a retry with a new session-id naturally gets a new branch because `<sid>` differs, or (b) the implementation must detect existing-branch and refuse/retry/regenerate? The Known Limitations section's "session-id branch suffix" comment implies (a), but the requirement text is open to both. -> State explicitly: "Because session-create generates a fresh `<sid>` on each invocation, bootstrap retries always produce distinct branch names; no collision-detection logic is required." Or, if (b) is meant, define the detection algorithm.

4. **R14 ("a commented `[claude.content.workspace] source = \"workspace.md\"` hint")** -> The exact comment syntax (single `#` line? `# [claude.content.workspace] source = "workspace.md"`? a block with preamble?) and surrounding whitespace are not specified. AC1 says "matching the literal expected TOML body" but the literal body is not embedded in the PRD. -> Inline the full expected TOML body (~10-15 lines) verbatim in an appendix or in R14 itself; reference it by anchor from AC1.

5. **R14 ("a single schema-doc-link footer")** -> "Schema-doc-link footer" is not defined. Is it `# See: https://...`? Is the URL fixed? Is it one line or multiple? -> Specify the exact line, including the URL, and whether it ends with a newline.

6. **R14 #1 ("`content_dir = \"claude\"`. No `default_branch` line.")** -> The PRD enumerates one negative constraint ("no `default_branch`") but doesn't say whether the block may contain other fields. Could an implementer also add `description = ...` or `version = ...`? -> Rewrite as "shall contain exactly these keys: `name`, `content_dir`. No other keys, including `default_branch`."

7. **R14 #3 ("`[groups.<vis>]` block with `visibility = \"<derived-from-Repo.Private>\"`")** -> "<vis>" is used as both the group key and the visibility value. If `Repo.Private == false`, is the block `[groups.public]` with `visibility = "public"`? Or `[groups.public]` only? AC "Visibility from Repo.Private" implies the key is `public`/`private`, but the mapping function is implicit. -> State explicitly: `Private: true -> [groups.private] visibility = "private"; Private: false -> [groups.public] visibility = "public"`.

8. **R18 ("a fixed, meaningful commit message (\"Initial niwa workspace config\")")** -> The parenthetical resolves what the message is, but "meaningful" is subjective filler that could be edited away in a future revision. -> Drop "meaningful"; state "a fixed commit message: `Initial niwa workspace config`" — exact string, code-fenced.

9. **R19 ("a prominent stderr block matching the prominence of the existing `--rebind` warning at `internal/cli/init.go:351-359`")** -> "Prominence" is undefined (font styling? blank-line padding? a banner? a leading prefix?). The line-range reference will rot the moment anyone edits that file. Two implementers will produce two different visual treatments and both can claim to "match the prominence." -> Either (a) embed the literal expected output as a fenced block in the PRD, or (b) commit to a structural rule ("preceded and followed by a blank line, prefixed with `Bootstrap complete.`") that a test can assert.

10. **R19 "Next steps" section ("inspect via `git show HEAD`, push via `git push -u origin niwa-bootstrap/<sid>`, then `niwa apply` (if the user makes further config edits before push) or skip directly to publish")** -> "Skip directly to publish" introduces a verb ("publish") that has no defined niwa meaning elsewhere in the PRD. Is "publish" `git push`? a future niwa command? -> Either drop the "or skip directly to publish" branch or define what "publish" maps to.

11. **R20 ("shell-wrapper landing-path mechanism")** -> The mechanism is referenced as if it's a known niwa concept, but the PRD doesn't define what file it writes to, what format, or how the shell wrapper reads it. AC8 says "Shell-wrapper landing-path file contains the worktree path" — but the file's location is unspecified. -> Reference the existing niwa landing-path doc/path explicitly, or specify the file location and format in the PRD.

12. **R23 ("a stderr error message that identifies which step failed and what state survives")** -> "Identifies which step failed" is open to interpretation — prefix? exit-code mapping? a specific format? -> Specify required format, e.g., "must contain `step=init|create|session-create` and a `survives:` section listing artifacts to keep."

13. **AC "TTY prompt decline" (`exit 0 or a well-defined "user declined" non-zero`)** -> Two implementers will literally choose differently here. The AC explicitly allows either exit-0 or non-zero, which means the AC is non-binary. -> Pick one: either "user declined -> exit 0" or "user declined -> exit N (non-zero, value M)." A reviewer must be able to assert a specific exit code.

14. **AC "Visibility from Repo.Private" fixture (`Private: false, Visibility: "<toml-metacharacter>"`)** -> The placeholder `<toml-metacharacter>` doesn't say what character or what the test should observe. -> Specify the actual character (e.g., `"\"\n[evil]\nkey = "`) and the exact assertion (e.g., "scaffold contains no `[evil]` block").

15. **N1 ("Bootstrap shall complete in time proportional to one shallow clone plus one session-create on the user's network")** -> "Proportional to" with no constant. Two implementers can both pass with very different absolute latencies. -> Either add a target (e.g., "p50 under 10s on a 100 Mbit connection against a 5 MB repo") or move N1 to "Known Limitations" as observational guidance, not a requirement.

16. **R6 ("The chain shall not introduce new success preconditions")** -> What counts as a "new success precondition"? Is requiring GH_TOKEN for visibility-lookup a new precondition? R17 says visibility-lookup may soft-fail, so technically no, but a reviewer can't verify R6 mechanically. -> Rephrase as a checklist: "the chain shall pass the same flag values it would pass to the standalone commands; environment variable requirements remain unchanged."

17. **R10 ("naming the GH_TOKEN scope guidance")** -> The parenthetical gives the exact text, but the requirement body uses paraphrase wording ("naming the ... scope guidance"). It's unclear whether the parenthetical is the exact required text or an example. -> State: "shall produce stderr text containing the exact substring: `verify GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo scope`."

18. **R11 ("brand-new zero-commit repo")** -> The required substring `If the repo is brand new and has no commits yet, push at least one commit (an empty README is enough) and retry with --bootstrap.` is exact and verifiable, but the other two causes (typo, private no-creds) get no exact wording. AC asks for "the 404 message naming all three causes" — verifiable only if all three strings are specified. -> Specify the exact wording for all three cause statements.

19. **R17 ("emit a stderr `note:` line explaining the fallback")** -> "Explaining" is open-ended. -> Either provide the exact note string or specify required substrings.

20. **R25 ("matching the wording pattern at `internal/cli/init.go:135-137`")** -> Line-range citation will rot, and "wording pattern" is fuzzy. -> Embed the exact error message in the PRD (and keep a code reference for context, not as the spec).

21. **Out of Scope: "Niwa proposes the minimal-ideal scaffold non-interactively"** -> "Minimal-ideal" is subjective marketing-style phrasing. Use "minimal scaffold defined in R14" instead.

22. **Goals #4 ("Adjacent-failure clarity")** -> Lists "no-marker without `--bootstrap`" but R13 shows seven distinct outcomes depending on TTY + flag combination, not one. -> Rephrase goal to reflect that the error path is multi-branched, or move the bullet to scope.

23. **Terminology check ("worktree" vs "session worktree")** -> Used consistently with niwa's existing meaning (`<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`). PASS.

24. **Terminology check ("workspace root" vs "instance root")** -> Used consistently (workspace root = `<cwd>/<name>/`, instance root = the instance dir under the workspace). R19 distinguishes the two explicitly. PASS.

25. **Terminology check ("instance")** -> Used consistently with niwa's existing concept. PASS.

26. **User Story #5 ("the partial state on disk to be discoverable and tearable-down via `niwa destroy`")** -> "Discoverable" has no test anchor — discoverable how? `niwa list`? on-disk inspection? a status command? -> Replace "discoverable" with the concrete observation: "the registry entry for the workspace is queryable via `niwa list` (or named in the failure message)."

27. **Goal #2 ("Self-revealing")** -> "Learns niwa's layout by reading the command's output" is aspirational and hard to test. The downstream requirement (R19) is more concrete; consider re-anchoring goal #2 to R19 directly so the goal has a verifiable counterpart.

28. **AC "Classifier ordering" ("the most-specific arm")** -> "Most-specific" needs a definition or an explicit precedence list. Two implementers can disagree on which arm is "most specific" without a stated rule. -> Append a precedence list: `AmbiguousMarkers > NoMarker > StatusError{401|403} > StatusError{404} > StatusError{other} > wrapped/unwrapped fallback`.

## Suggested Improvements

1. **Inline the full literal expected `workspace.toml` body.** AC1 says it must match a literal body, but the body is described across R3/R4/R14 in pieces. Inline it once, in code-fence, and cite it from each requirement.

2. **Inline the full literal expected R19 stderr block.** This single change resolves ambiguities 9, 10, and the "prominence" handwave. Tests then assert against the literal block.

3. **Replace all line-range citations to source files (`init.go:135-137`, `init.go:351-359`) with the literal text they refer to.** Citations rot the moment the cited file is reformatted; inlining is durable.

4. **Add a "Glossary" or "Definitions" mini-section.** Lock the meaning of: workspace root, instance root, worktree, session, sid, group, source, channel, scaffold, bootstrap branch. Two implementers diverged on what counts as "instance root" in past niwa work; explicit definitions prevent recurrence.

5. **Make Acceptance Criteria fully binary.** Specifically: pick an exit code for TTY-decline; pick a constant for N1's latency target (or move N1 out of requirements); specify the visibility-fixture metacharacter exactly; commit to a classifier precedence list.

6. **Remove subjective hedges.** "Meaningful," "prominent," "minimal-ideal," "appropriate," "self-revealing," "as needed" all appear; each is a clarity-tax. Either remove or replace with a verifiable structural rule.

7. **Reconcile Goal #4 with R13.** Goal #4 enumerates a flat list of error modes; R13 shows a 7-cell matrix. Either expand the goal or accept it as approximate framing and add "(see R10-R13 for full matrix)."

8. **Renumber `Repo.Private` -> `<vis>` mapping explicitly.** R14 #3 leaves the mapping implicit; embed `private: true -> [groups.private]` and `private: false -> [groups.public]` directly.

9. **Clarify "publish" or drop it.** R19's "skip directly to publish" introduces a verb that doesn't appear anywhere else in the PRD and doesn't map to a niwa command.

## Summary

The PRD is well-structured and exhibits strong terminology discipline (workspace/instance/worktree are used consistently with niwa's prior meaning) and has commendably specific exit-code, host-validation, and rollback rules. However, key user-visible artifacts — the scaffolded TOML body, the success stderr block, several error messages, the "prominence" requirement, and the TTY-decline exit code — are described by paraphrase or topical reference rather than by exact strings, and one acceptance criterion explicitly permits two different implementations. Tightening the PRD requires mostly mechanical work: inline the expected scaffold body and stderr block verbatim, replace line-range source citations with literal text, pick exit codes, and remove subjective hedges ("meaningful," "prominent," "minimal-ideal") — after which the PRD should pass clarity review on a second pass.
