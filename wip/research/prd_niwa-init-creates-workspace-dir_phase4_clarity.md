# Clarity Review

## Verdict: PASS

The PRD is unusually precise for its length: requirements name specific error sentinels, specify which regex applies, identify exact files to update, and set quote-level expectations for messaging. Two developers reading it would build substantively the same thing. A handful of wording issues remain, but none rise to the level of blocking ambiguity.

## Ambiguities Found

1. **R3 ("any other CLI output")**: The phrase "the success message, and any other CLI output" leaves the override surface open-ended. An implementer can't enumerate "any other CLI output" deterministically — `niwa go` listings, `niwa list` (if any), mesh-layer logs, and TUI fragments are all candidates. -> Suggested clarification: Either replace "any other CLI output" with an explicit list of commands/surfaces (status, apply, registry, success message, mesh layer status) or say "every command that surfaces a workspace name reads it from instance state, with no exceptions remaining on the toml's `[workspace] name`."

2. **R4 / AC-8 ("a one-time stderr note")**: "One-time" is undefined here. Once per init? Once per workspace lifetime (suppressed on subsequent runs)? Once per session? Niwa already has a `one-time-notices` mechanism (`docs/guides/one-time-notices.md`), so this term is loaded. -> Suggested clarification: "Emitted on the init invocation that performs the override; not emitted again on subsequent commands." Or, if the intent is to use the one-time-notices machinery, say so explicitly.

3. **R5 ("a single remediation option")**: "Single remediation option" is subjective. Different implementers will pick different remediations (rename, choose new dir, remove the existing one). -> Suggested clarification: Specify the exact remediation text for each path type (file / directory / symlink), the way R6 specifies `ErrWorkspaceExists` and `ErrNiwaDirectoryExists` text.

4. **R5 vs. AC-9/10/11 (path-type taxonomy)**: R5 lists "file, directory, symlink, other." AC-9/10/11 only test file, directory, symlink. "Other" (e.g., FIFO, block device, socket) is unspecified — does it produce `InitConflictError` with qualifier "other"? Different qualifier? -> Suggested clarification: Either drop "other" from R5 or add an AC covering it (e.g., "When `<cwd>/my-ws` is any non-regular, non-directory, non-symlink path, the qualifier is `other`").

5. **R7 / AC-16 ("reserves explanation")**: AC-16 says the error "mentions `..` and reserves explanation." The word "reserves" reads like a typo ("requires"? "includes"?) and the intent is unclear. -> Suggested clarification: State the literal expectation, e.g., "the error message includes the literal `..` and explains it is rejected as a path-traversal sentinel."

6. **R8 / AC-19 (rebind preconditions)**: R8 fires when the registered name matches and `Root` differs. AC-19 adds the precondition "where `/path/B/my-team` does not exist." If `/path/B/my-team` *does* exist, which rule wins — R5 (target-exists rejection) or R8 (rebind warn-and-succeed)? Implementers will guess differently. -> Suggested clarification: State precedence explicitly: "The target-exists pre-flight (R5) runs before the registry-rebind check (R8); when both would fire, R5 wins."

7. **R6 (orphaned `.niwa/` definition)**: R6 says `<cwd>/<name>/.niwa/` "exists without a `workspace.toml`" triggers `ErrNiwaDirectoryExists`. What if `.niwa/` exists with non-workspace.toml contents (e.g., a stray `state.json`, an `overlay/` directory, a partial init)? The PRD treats absence of `workspace.toml` as the discriminator, but the "orphan" definition deserves to be explicit. -> Suggested clarification: "`.niwa/` exists at the path AND `.niwa/workspace.toml` does not exist (regardless of other contents inside `.niwa/`)" — i.e., make the file the sole discriminator.

8. **AC-5 / AC-6 (status and apply "show" / "reference" the name)**: "Shows" and "references" don't say where in the output. Status output has multiple fields; apply has multiple log lines. A reviewer can't objectively pass/fail unless the location is specified. -> Suggested clarification: Either name the field ("the `Workspace:` line in `niwa status` output reads `my-name`") or say "every line of status/apply output that names the workspace uses `my-name`."

9. **R3 / Known Limitations interaction**: R3 says the override is everywhere niwa surfaces a name. Known Limitations item 3 says "a reader inspecting `.niwa/workspace.toml` directly will see the upstream name." That's not a contradiction, but the PRD never explicitly says `niwa workspace show-config` (or any future read-back command) is in or out of scope. If such a command exists or is added, which name does it show? -> Suggested clarification: Add to R3 or to Out of Scope: "Commands that explicitly print the raw `.niwa/workspace.toml` content (e.g., a debug `cat`-equivalent) show the upstream name; all interpretive commands show the override."

10. **R9 ("absolute path of the workspace root")**: For named init, that's `<cwd>/<name>/`. For no-name init, that's `<cwd>/`. Both are clear. But for `niwa init <name> --from <src>` where the rebind warning fires (R8), the "workspace root" is the new `<cwd>/<name>/`, not the old registered `Root`. Implementers may or may not get this right. -> Suggested clarification: "The absolute path printed is always the path on disk where this invocation wrote `.niwa/workspace.toml`."

11. **AC-14 ("citing the allowed character set")**: "Citing" is loose — does the error print the regex literally (`^[a-zA-Z0-9._-]+$`)? A human-readable description ("alphanumerics, dots, underscores, hyphens")? -> Suggested clarification: Pick one form and require it.

12. **R12 / AC-23 / AC-24 (README scope)**: R12 lists three README sections to update. AC-24 lists the same three. But "all `niwa init` examples" in R12 is a wider net than the three sections. Are there other example occurrences in the README? -> Suggested clarification: Either change R12 to "the three sections enumerated below" or add an AC requiring a grep-style audit ("no `mkdir + cd + niwa init` example survives in `README.md`").

## Suggested Improvements

1. **Add a traceability matrix.** R1-R13 are 13 requirements; AC-1 through AC-26 are 26 criteria. The mapping is mostly inferable but not stated. A short table (R# → AC#s) would let a reviewer verify each requirement has at least one binary AC. Quick spot-check: R10 maps to AC-21; R11 maps to AC-22; R12 maps to AC-23 and AC-24; R13 maps to AC-25. R4's stderr override note maps to AC-8. R8's rebind warning maps to AC-20. R9 maps to AC-26. R2 (no-name flow) maps to AC-3 and AC-4. The mapping holds, but making it explicit catches gaps.

2. **Specify exit codes.** Several ACs say "exits non-zero" but don't pin a specific code. Niwa's existing convention (if any) should be referenced; if not, pick `1` for validation errors and `2` for conflict errors (or whatever convention exists) and state it once.

3. **Clarify the success message format.** R9 and AC-26 require the absolute path. The exact format ("Workspace initialized at `/abs/path`" vs. "Created workspace `<name>` (`/abs/path`)") affects functional tests. A literal string template would let test authors write a single assertion.

4. **State precedence among preflight checks.** Multiple checks can fire on the same input (name validation R7, target-exists R5/R6, registry-rebind R8). The PRD is silent on order. Recommended order: validate name (R7) → check target path (R5/R6) → check registry rebind (R8) → write. Make this explicit in Decisions or a new "Preflight order" subsection.

5. **Reword "reserves explanation" in AC-16.** Almost certainly a drafting artifact. Replace with the intended phrasing.

6. **Tighten R3's surface enumeration.** Replace "and any other CLI output" with a closed list, or say "and all commands that read a workspace name."

## Summary

The PRD is well-scoped and unusually concrete: error sentinels are named, regexes are cited by file location, ACs are mostly binary, and the Decisions section pre-empts the obvious "why didn't you just...?" pushback. The ambiguities that remain are mostly wording-level (R3's "any other CLI output," R5's "single remediation option," AC-16's "reserves explanation," R4's "one-time") and a missing precedence statement when R5 and R8 would both fire. Adding a R# → AC# traceability table and tightening the half-dozen loose phrases would push this from "good" to "no implementer questions left."
