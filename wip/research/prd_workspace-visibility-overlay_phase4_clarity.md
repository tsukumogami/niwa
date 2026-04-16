# Clarity Review: PRD-workspace-visibility-overlay

## Verdict: FAIL
This PRD contains 15 significant ambiguities that would allow two developers to build different features. Critical gaps exist in state detection heuristics, merge semantics, output validation, and undefined operational terms.

## Ambiguities Found

1. **R8 — "valid git repo"** (line 72)
   - Text: "If the directory exists and is a valid git repo, it was previously cloned."
   - Ambiguity: "valid" is not defined. Does it require `.git/` directory presence only, or also successful git rev-parse, or HEAD validity, or recent commit history?
   - Impact: Two developers will implement different validation logic. One might accept a corrupted repo; another might reject it.
   - Suggested clarification: Specify exact validation: "contains a `.git` directory and `git rev-parse HEAD` succeeds" or similar concrete check.

2. **R5 — "equivalent to the global config sync step"** (line 66)
   - Text: "niwa syncs the companion (equivalent to the global config sync step)."
   - Ambiguity: Readers must understand global config sync behavior, which is not explained in this PRD. Different interpretations possible (shallow clone? fetch + reset? pull?).
   - Impact: Sync behavior could differ from global config unintentionally.
   - Suggested clarification: Explicitly state the sync operation (e.g., "git fetch origin; git reset --hard origin/HEAD").

3. **R6 & R7 — "any reason"** (lines 68, 70)
   - Text: "If the companion... clone fails for any reason... niwa silently skips" vs "if the sync fails for any reason, `niwa apply` aborts."
   - Ambiguity: "any reason" is overly broad. Network timeouts, DNS failures, rate limits, and permission errors all behave differently operationally. No guidance on which are caught vs retried vs fatal.
   - Impact: Developers must guess at error handling strategy. Prod will expose inconsistencies.
   - Suggested clarification: Enumerate specific error categories (network, auth, not-found) and behavior for each, or state that all exceptions are caught as one class.

4. **Goals & Stories — "fully functional workspace"** (lines 6, 27, 43)
   - Text: "Users... get a fully functional workspace with only the public repos"
   - Ambiguity: "fully functional" is subjective. Does it mean can init/apply? Can build? Can commit? Has all necessary hooks and env?
   - Impact: Acceptance criteria AC123-124 cannot be objectively verified without defining this term.
   - Suggested clarification: Define in Goals: "A fully functional workspace is one where `niwa apply` completes without error and all public repos are cloned and configured for their declared use."

5. **Goals & Stories — "no indication that private configuration exists"** (lines 6, 27, 43)
   - Text: "with no indication that private configuration exists"
   - Ambiguity: Broad term. Does it include stderr, debug logs, STDERR env var, error codes, exit codes, intermediate state files? AC146 narrows to "no reference" but the goal is vaguer.
   - Impact: Subjective interpretation. One dev might think this means stdout only; another includes all logs.
   - Suggested clarification: State concretely in Goals: "Users without access receive no error messages, warnings, debug output, or exit codes that reference the companion or private configuration."

6. **R17 — Duplicate source org error condition and timing** (line 94)
   - Text: "If both the public config and the companion declare a `[[sources]]` entry for the same GitHub org, `niwa apply` aborts with an error... Auto-discovery ... is prohibited in companion files for orgs that also appear in the public config."
   - Ambiguity: How is the check performed? Does auto-discovery phase run first, then validation? Or is the prohibition enforced at parse time? What if companion auto-discovery matches an org not yet auto-discovered in public config (ordering issue)?
   - Impact: Error handling path is unclear. When is the error thrown—during companion validation, during merge, during repo discovery?
   - Suggested clarification: "During companion parsing, if any org appears in the public config's source list, reject any `[[sources]]` entry for that org in the companion file with the error: ...".

7. **R23 — "standard apply output" vs "verbose/debug output"** (line 110)
   - Text: "niwa must not include the companion's registration URL... in standard apply output. These may appear in verbose/debug output only."
   - Ambiguity: What constitutes "standard"? Does niwa have flags like `--verbose`, `--debug`, `-v`, `--quiet`, `--json`? No definition of output modes.
   - Impact: Developers won't know which flags unlock companion reference info. Acceptance criteria AC146 can't be objectively tested without understanding output mode semantics.
   - Suggested clarification: "Define 'standard' as the output of `niwa apply` without flags. Companion references are permitted only when `--debug` or `--verbose` flags are passed."

8. **AC124 — "merged into the workspace"** (line 124)
   - Text: "Apply produces a workspace with repos from both configs."
   - Ambiguity: Terminology conflict. R13-16 define merge semantics with precedence rules, but "merged" could mean shallow concatenation, deep merge with conflict resolution, or something else. Doesn't clarify what "from both configs" means—by name? By entry count?
   - Impact: Ambiguous acceptance test. How do you verify "repos from both configs"? Check repo count? Check specific repo names?
   - Suggested clarification: "The workspace contains all repos from the public config plus all repos from the companion, with public config entries taking precedence for duplicates. Verify by: (1) count repos from public config, (2) count repos from companion, (3) confirm total count equals (1) + (2) with no duplicate repo names."

9. **AC140 — CLAUDE.md import order with conditional** (line 140)
   - Text: "workspace context appears before `@CLAUDE.private.md`, which appears before `@CLAUDE.global.md` (if global config is also registered)."
   - Ambiguity: What if global config is not registered? Is the import order still enforced? What if private companion is registered but global is not? Acceptance criteria are conditional but acceptance checklist doesn't mark which tests apply under what conditions.
   - Impact: Test interpretation depends on deployment context. CI/CD without global config will pass, but the PRD doesn't specify whether this is correct behavior.
   - Suggested clarification: "When a global config is registered, the import order is: @CLAUDE.workspace.md, @CLAUDE.private.md, @CLAUDE.global.md. When global config is not registered, the order is: @CLAUDE.workspace.md, @CLAUDE.private.md. Verify with: `cat workspace/CLAUDE.md | grep -o '@CLAUDE.*'`."

10. **R6 vs R7 state distinction — "previously cloned" heuristic** (lines 68, 70, 72)
    - Text: "R8: niwa derives 'previously cloned' state from the presence of a git repository in the companion's local clone directory."
    - Ambiguity: Heuristic is fragile. What if user manually deletes `$XDG_CONFIG_HOME/niwa/private/` but companion state exists elsewhere (git config, cache)? What if `.git/` is corrupted? What if directory exists but is not a git repo (created by user error)?
    - Impact: R6 (first-time silent skip) and R7 (subsequent fatal error) will produce inconsistent behavior for edge cases.
    - Suggested clarification: "The 'previously cloned' state is determined by: (1) `$XDG_CONFIG_HOME/niwa/private/` directory exists, AND (2) `git -C $XDG_CONFIG_HOME/niwa/private/ rev-parse HEAD` exits with code 0. If both conditions are true, treat as previously cloned. Otherwise, treat as first-time."

11. **AC146 — "no reference" to companion details** (line 146)
    - Text: "Standard `niwa apply` output (non-verbose) contains no reference to the private companion's repo name, URL, or registration status."
    - Ambiguity: What counts as a "reference"? Is mentioning repo name `vision` a reference if vision exists in both configs? Is an error code like `ERR_COMPANION_SYNC_FAILED` a reference? Does "no reference" include temp files, cache, exit codes, process names?
    - Impact: Test is subjective. One reviewer might accept "Sync failed: network error" as non-revealing; another rejects it as potentially alluding to companion existence.
    - Suggested clarification: "The output of `niwa apply --verbose` (non-debug mode) must not contain: (a) the companion's GitHub repo name or URL, (b) the companion's local path, (c) the term 'companion', (d) the term 'private' as a descriptor of configuration. Verify with: `niwa apply 2>&1 | grep -iE '(companion|acmecorp/dot-niwa-private|\.config/niwa/private)'` must return empty."

12. **R13 — "appended... drives repo discovery" ordering** (line 86)
    - Text: "Sources from the companion are appended to the public config's sources after parsing. The combined sources list drives repo discovery."
    - Ambiguity: Does append order matter for repo discovery? If both public and companion sources reference the same repo (by different name), which is used? What if repo names collide across orgs?
    - Impact: Repo discovery order affects CI/CD reliability. Ordering ambiguity could surface in production as intermittent conflicts.
    - Suggested clarification: "Companion sources are appended in order. Repo discovery iterates the combined source list in order. If the same repo is discovered multiple times, the first discovered entry is used; subsequent entries are logged at debug level only."

13. **R14-16 — "takes precedence" semantics** (lines 88, 90, 92)
    - Text: "If a group name exists in both... public config's definition takes precedence."
    - Ambiguity: "takes precedence" is vague. Does the entire group definition replace the companion's, or does precedence apply per-field? Example: if public group `tools` defines repos [A, B] and companion defines [C, D], is the result [A, B], [A, B, C, D], or undefined?
    - Impact: Developers will implement different merge strategies. Tests in AC131-132 only check exact scenarios; edge cases will fail differently.
    - Suggested clarification: "When the same group, repo, or content entry exists in both configs, the public config's entry completely replaces the companion's. No field-level or list-level merging occurs. The companion's entry is ignored in its entirety."

14. **R20 — XDG_CONFIG_HOME default behavior** (line 104)
    - Text: "The local clone path is derived at runtime from `$XDG_CONFIG_HOME/niwa/private/`..."
    - Ambiguity: What if `XDG_CONFIG_HOME` is unset? Use `~/.config` by default per XDG spec? Or error? Different systems have different conventions.
    - Impact: Cross-platform behavior undefined. Windows users or systems without XDG may fail unpredictably.
    - Suggested clarification: "If `XDG_CONFIG_HOME` is set, use `$XDG_CONFIG_HOME/niwa/private/`. Otherwise, use `~/.config/niwa/private/`. This follows the XDG Base Directory specification fallback convention."

15. **"Silently ignored" terminology conflict** (lines 88, 90, 131, 132)
    - Text: "public config's definition takes precedence" (R14) vs "public config's `tools` group definition is used; the companion's is silently ignored" (AC131)
    - Ambiguity: Are these equivalent? Does "silently ignored" mean no log output? No debug output? Just no error message? Or is it the same as "takes precedence"?
    - Impact: Acceptance criteria AC131-132 use "silently ignored" but requirements R14-16 use "takes precedence." Consistency is unclear. Verifiers won't know whether to check logs.
    - Suggested clarification: Use consistent terminology. Define: "When an entry exists in both configs, use the public config's definition and produce no warning, error, or log output at any level for the ignored companion entry."

## Suggested Improvements

1. **Define "fully functional workspace"**: Add to Goals section: "A fully functional workspace is one where `niwa apply` completes successfully, all configured repos are cloned to their configured paths, hooks execute without error, and environment setup matches the workspace config intent."

2. **Consolidate output mode definitions**: Add a new subsection under Requirements titled "Output and Logging" that defines `standard`, `--verbose`, `--debug` modes explicitly. State what companion information is visible in each mode.

3. **Formalize state detection heuristics**: Replace informal descriptions of "previously cloned" and "valid git repo" with concrete shell command equivalents that developers can implement and test consistently.

4. **Enumerate error categories in R6 and R7**: Replace "any reason" with explicit categories: (1) network error (DNS, timeout, connection refused), (2) authentication error (403, 401), (3) not-found error (404), (4) other (parse, disk I/O). Specify behavior for each.

5. **Add a reference validation criteria**: Create an acceptance criterion that lists specific strings/patterns that must NOT appear in standard output, formatted as regex patterns for automated testing.

6. **Clarify merge semantics with examples**: Add a subsection with worked examples showing public vs companion collisions for groups, repos, and content entries. Include field-level nesting examples (e.g., if group has sub-fields, how are collisions resolved?).

7. **Specify companion parse-time vs apply-time validation**: Clarify which validations happen when companion file is first parsed vs when sources are resolved vs when repos are synced. Add timeline diagram.

8. **Define interaction with instance-level flags**: Add a truth table showing all combinations of: (instance skip_private flag) x (--skip-private arg) x (companion registered) → behavior.

## Summary

This PRD establishes valuable goals and comprehensive requirements but suffers from insufficient specificity in operational details and acceptance criteria. The absence of defined terms (fully functional, any reason, valid git repo, silently ignored, standard output), ambiguous merge semantics, and subjective acceptance criteria create a 15-point gap that would lead to inconsistent implementations. A developer review would likely surface these ambiguities in task estimation or code review, causing rework. Pre-implementation clarification of state heuristics, error handling categories, output modes, and merge behavior would prevent downstream integration issues.

