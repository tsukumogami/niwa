# Clarity Review

## Verdict: PASS

The PRD is unusually precise for its scope; the ambiguities found are localized wording issues, not structural under-specification, and an implementer could derive Gherkin scenarios directly from the user stories and acceptance criteria.

## Ambiguities Found

For each, give: (a) location/section, (b) the ambiguous text quoted exactly, (c) why it's ambiguous, (d) suggested clarification.

1. **R10 (Snapshot materialization)**: "No `.git/` directory MUST exist in the materialized snapshot."
   - **Why ambiguous**: The MUST applies to the directory but says nothing about other VCS metadata or hidden files an implementer might generate as side-effects of the fetch path (e.g., `.gitattributes`, `pax_global_header`, GitHub's tarball top-level wrapper directory). R10 also says "containing only the files from the resolved source subpath plus a single provenance marker" — does "only the files" exclude empty directories that exist in the source? Does it exclude OS-injected files like `.DS_Store`? Implementer A might strip aggressively; implementer B might pass through everything.
   - **Suggested fix**: Add a sentence: "The materialized tree MUST contain exactly (a) every regular file present at the resolved subpath in the source commit, with directory structure preserved, and (b) one provenance marker. No additional files (including but not limited to `.git/`, `pax_global_header`, tarball wrapper directories, or VCS metadata files outside `.git/`) MUST persist."

2. **R12 (Snapshot atomicity)**: "Snapshot refresh MUST be atomic: niwa writes the new materialization to a sibling location and renames it into place on success."
   - **Why ambiguous**: "Atomic" plus "rename" implies POSIX `rename(2)` semantics, but `rename(2)` on a non-empty target directory is not portable (it's POSIX-required-to-fail; Linux requires the target to be empty or to use `renameat2(RENAME_EXCHANGE)`). An implementer could read this as "rename over the old dir" (which fails), "rmdir-then-rename" (which is not atomic), or "rename old → backup, rename new → real, rmdir backup" (atomic-ish but the AC about "previous snapshot is removed only after the rename completes" doesn't pin it down either).
   - **Suggested fix**: State the sequence explicitly: "niwa MUST materialize the new snapshot at a sibling path (e.g., `<workspace>/.niwa.new/`), then atomically swap it into place using a rename sequence such that at no point is `<workspace>/.niwa/` absent or partially populated. The previous snapshot MUST be removed only after the new snapshot is observable at the canonical path." Acknowledge that on platforms without atomic directory swap, niwa MUST use a documented best-effort sequence and name it.

3. **R14 (GitHub tarball fetch)**: "niwa MUST use the GitHub REST tarball endpoint with selective `tar` extraction filtered to the requested subpath."
   - **Why ambiguous**: "selective `tar` extraction" doesn't specify whether niwa shells out to the system `tar(1)` (which version? GNU vs BSD differ in flag handling), uses Go's `archive/tar` to stream-extract, or uses some library wrapper. The choice has correctness implications (system `tar` may not be present on all supported platforms; Go's tar handles symlinks differently). The PRD scope ("self-contained, no system dependencies" per the org CLAUDE.md) suggests Go's stdlib is intended, but R14 doesn't say.
   - **Suggested fix**: Either commit to the implementation ("niwa MUST stream-extract the tarball using Go's `archive/tar` package without invoking a system `tar` binary") or commit to the contract ("niwa MUST extract the tarball using a mechanism that requires no system binaries beyond what niwa already depends on").

4. **R26 (Plaintext-secrets guardrail)**: "The GitHub-public pattern match MUST run against the marker tuple."
   - **Why ambiguous**: "GitHub-public pattern match" is a term of art that isn't defined anywhere in the PRD. An implementer who hasn't read the existing guardrail code can't tell what fields of the marker tuple participate in the match (host? owner+repo? does private vs public visibility get inferred from the API or from the slug?), nor what "match" means (regex? equality? prefix?).
   - **Suggested fix**: Either inline the contract ("MUST consider the source GitHub-public when `marker.host == 'github.com'` AND a public-visibility check via the configured GitHub API succeeds for `marker.owner/marker.repo`") or cite the existing function and file ("the contract from `internal/.../guardrail/githubpublic.go:CheckGitHubPublicRemoteSecrets` is preserved; only the input source changes from `git remote -v` to the marker tuple").

5. **R30 (Performance)**: "A first-time GitHub fetch SHOULD complete in under 5 seconds for a typical config-sized subpath (≤1 MB compressed) on a normal broadband connection. The 40-byte SHA-endpoint drift check on subsequent applies SHOULD complete in under 500 ms."
   - **Why ambiguous**: This is the most prominent SHOULD in the PRD. It's neither testable (no defined "normal broadband", no measurement methodology) nor binding (SHOULD vs MUST). An implementer cannot fail a test against this; a reviewer cannot block a PR with this. It reads as aspirational but is shaped like a requirement.
   - **Suggested fix**: Either drop it (move to "Known Limitations" as expected behavior) or pin it ("On a synthetic test against `github.com` with simulated 100 Mbit/s downstream and 30 ms RTT, a first-time fetch of a 1 MB-compressed subpath MUST complete in under N seconds and the SHA drift check MUST complete in under M ms; CI enforces these via `test/perf/`."). The current form satisfies neither posture.

6. **R33 (Cleanup)**: "The fallback path's temporary clone directory MUST be cleaned up on success and on most failure paths (process kill is the exception; document the resulting cleanup ritual)."
   - **Why ambiguous**: "most failure paths" is the textbook hedge — an implementer can claim conformance no matter how many failure paths leak the temp dir. Which paths are required to clean up vs allowed to leak? Is "panic" in the exception set? "Out-of-disk while copying"?
   - **Suggested fix**: Enumerate: "The fallback path MUST clean up the temporary clone directory on (a) successful copy, (b) any error returned from the fetch or copy step, (c) any context cancellation. niwa MAY leak the temp directory only when the process receives an uncatchable signal (SIGKILL) or is otherwise terminated without giving Go's deferred cleanup a chance to run. The cleanup ritual for stranded temp dirs (path convention, manual recovery command) MUST be documented in the operator guide."

7. **R27 (Deprecation timing)**: "The `--allow-dirty` flag MUST be silently accepted for one release with a stderr deprecation notice... It MUST be hard-removed in a subsequent release."
   - **Why ambiguous**: "one release" and "a subsequent release" have no anchor. If v1 ships the deprecation notice, does v1.1 hard-remove? v2.0? "A subsequent release" technically allows v37. The Decisions section says "hard-removed in v1.1" but the requirement doesn't bind that.
   - **Suggested fix**: Pin to the version called out in the decisions section: "MUST be silently accepted with a deprecation notice in the v1 release. MUST be removed in v1.1." The AC verifying this requirement should reference the same versions.

8. **AC for R8 (`niwa.toml` content_dir)**: "a `niwa.toml` without `[workspace] content_dir` fails apply with a targeted diagnostic."
   - **Why ambiguous**: "Targeted diagnostic" is subjective. R8's prose says "fail with a diagnostic" without specifying its content. R6, R7, and R23 each call out specific information the diagnostic must include (filenames, both URLs, etc.); the R8 diagnostic doesn't get the same treatment. An implementer could ship "error: invalid config" and claim conformance.
   - **Suggested fix**: Match the specificity of R6/R7: "MUST fail with a diagnostic naming the resolved source slug, the resolved subpath (root), the missing setting (`[workspace] content_dir`), and the explicit-opt-in escape hatch (`content_dir = '.'`)."

9. **R20 / R29 (Lazy migration trigger)**: R20 says mirror fields "MUST be lazily upgraded by populating the mirror fields on the next registry write." R29 says "The first `niwa apply` after upgrade triggers the lazy migrations (R20, R21) automatically."
   - **Why ambiguous**: R20 ties migration to "the next registry write" but the registry isn't necessarily written on every `niwa apply` — it's written on `niwa init`, `niwa config set`, and a few other commands. R29 asserts apply triggers it. These two are subtly inconsistent: a user who only ever runs `niwa apply` may never see the migration if `apply` doesn't write the registry.
   - **Suggested fix**: Pick one anchor and apply consistently: either "MUST be upgraded on the first invocation of any command that loads the registry, regardless of whether that command otherwise writes" (eager), or "MUST be upgraded on the next command that mutates the registry; read-only commands MUST NOT mutate" (lazy, and document that pure-apply users won't see the mirror until they run init/set/etc.). Then update R29 to match.

## Suggested Improvements

1. **R5 marker precedence wording**: Currently "rank 1, rank 2, rank 3" — using the word "rank" with numbers reads slightly oddly ("higher rank" could mean either direction). Consider "first match, second match, third match" or "precedence 1 (highest)".

2. **R11 marker contents "at minimum"**: The phrase "MUST record at minimum" leaves unbounded room for additional fields. Since R34 commits to no specialized tooling, the marker is part of the public contract — consider locking the field set explicitly and reserving an `extensions` block for future additions.

3. **AC "verifies Rxx" tags**: These are excellent and should be retained. Consider also adding reverse links from each requirement to the AC line(s) that verify it, to make coverage gaps visible.

4. **Story 2 `--force` UX**: The story says "instructing the developer to inspect `<workspace>/.niwa/` for pending edits before re-running with `--force`." Consider whether the diagnostic should also surface the new resolved subpath (since `tsukumogami/vision` is bare-slug, the user might be surprised that discovery picked `.niwa/`). A "discovered subpath: `.niwa/`" line would make the new state visible at the moment of decision.

5. **Story 4 final sentence**: "Issue #72 is invisible." This is rhetorical/marketing-shaped phrasing in a user story that otherwise reads as factual present-tense narration. Consider deleting or rewriting as "The failure mode that wedged this workflow before this redesign no longer exists."

6. **Out-of-Scope wording on read-only enforcement**: "hard-enforcement is an opt-in for a follow-up" — "opt-in" implies a flag exists. If the follow-up doesn't exist yet, this should read "hard-enforcement is a candidate for a follow-up release" to avoid suggesting the option is already designed.

7. **Decision: slug delimiter rationale**: The reasoning leans heavily on shell-metachar safety for `?`. Worth noting that `:` and `@` are also fine in `git config`-style URL parsing, which is a relevant constraint for the registry's stored form. Strengthens the rationale.

## Summary

Found 9 ambiguities, mostly minor — the PRD is a strong draft and none of the ambiguities are structural enough to fail it. The two most material are R12's "atomic rename" (which conflates several incompatible POSIX semantics that an implementer needs to disambiguate) and R30's SHOULD-shaped performance budget (which reads like a requirement but isn't testable). Recommended action: PASS the PRD into design, but address the R12 atomicity contract and either pin or move R30 before the design phase consumes them as constraints.
