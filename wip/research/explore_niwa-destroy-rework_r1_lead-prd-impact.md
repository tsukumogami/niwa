# Lead: PRD impact inventory

## Findings

### Per-PRD review

12 PRDs in `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-8/public/niwa/docs/prds/`. Status legend: D=Done, A=Accepted, X=Delivered, P=Proposed.

| PRD | Status | Scope (one-line) | Mentions destroy? | Affected? |
|---|---|---|---|---|
| PRD-shell-integration.md | A | Shell wrapper, `niwa()` function, NIWA_RESPONSE_FILE protocol, completions, lifecycle (install/uninstall/status) | Yes — only as an "out of scope" note about resettsuku (line 307); doesn't list destroy in cd-eligible commands | YES (high) |
| PRD-niwa-init-creates-workspace-dir.md | D | Single-command init creates `<cwd>/<name>/`; name override; pre-flight conflict shape | No direct destroy mentions; PRD's R10a/AC-21a establish the named-init landing protocol via `NIWA_RESPONSE_FILE` that destroy must mirror in reverse | YES (medium — symmetry only) |
| PRD-mesh-session-lifecycle.md | D | Per-session worktrees, `niwa_destroy_session`, `niwa session destroy` CLI | Yes — "destroy" appears 30+ times but EXCLUSIVELY for session destroy, not workspace/instance destroy | NO (different surface) |
| PRD-cross-session-communication.md | X | Mesh layer, daemon, channel installer, task lifecycle | Yes — R2 (ManagedFiles + niwa destroy), R38 (daemon shutdown grace), AC-P11 (destroy + grace), config defaults table, "Known limitations" entry | YES (high) |
| PRD-workspace-config-sources.md | D | Subpath-aware snapshot config sourcing; brain-repo subpath | Mentions `destroy --force` only as a passing reference to the "existing destructive-operation pattern" (line 1001), and `destroy/re-init ritual` rejection (line 198) | YES (light — line 1001 will become inaccurate) |
| PRD-workspace-visibility-overlay.md | X(extended) | Overlay repo for additional repos/groups/secrets | None | NO |
| PRD-config-distribution.md | A | Hooks/settings/.local.env distribution | "lifecycle" header only (line 299); no destroy refs | NO |
| PRD-env-example-integration.md | X | `.env.example` discovery | None | NO |
| PRD-global-config.md | A | Global config repo overlay | None | NO |
| PRD-machine-identity-vault-sync.md | D | Machine identity distribution via personal vault | None | NO |
| PRD-plugin-installation.md | P | Project-scoped plugin install | None | NO |
| PRD-vault-integration.md | X | Pluggable vault, layered secrets | None | NO |

### Affected requirement IDs (with current text and gap)

#### PRD-shell-integration.md (Accepted)

This PRD is the single biggest source of doc work, because today it scopes the "cd-eligible command" set to `create` and `go` exclusively; destroy is not in the protocol contract.

- **R1 (Shell function wrapper)** — line 108-111. Current text: "When shell integration is active, a `niwa()` shell function intercepts cd-eligible subcommands (`create`, `go`), captures the binary's stdout (a directory path), and calls `cd` to that path."
  - **Gap**: destroy needs the inverse — the wrapper must `cd` the user OUT of a deleted directory when destroy removes the cwd. This is a different protocol shape than `create`/`go` (they receive a non-empty path; destroy may also write a path, but the path is a *safe ancestor*, not a target the user explicitly named).
  - **Update**: amend R1 to add `destroy` to the intercepted-subcommand list with a sub-clause clarifying destroy's payload contract: when the shell wrapper's cwd is inside the deleted instance, write a landing path (e.g., the workspace root or `$HOME` if the workspace itself was wiped); when the cwd is outside the deletion target, write nothing.

- **R2 (Stdout protocol)** — line 113-115. Current text refers to "cd-eligible commands print a single absolute directory path to stdout on success." Already obsolete (DESIGN-shell-navigation-protocol.md replaced this with NIWA_RESPONSE_FILE).
  - **Gap**: this is a stale requirement vs. the actual protocol. Destroy rework is a good moment to reconcile R2 with the response-file protocol the design doc actually describes. Not strictly destroy-specific, but adjacent.
  - **Update**: orthogonal cleanup; flag for design doc to address, but not blocking destroy.

- **R6 (shell-init status)** — line 130-132. Reports whether the wrapper is loaded; the new destroy adds another command to the protocol surface. Status output is unaffected mechanically but the protocol envelope it advertises changes.
  - **Update**: probably none; R6 doesn't enumerate intercepted commands.

- **R11 (Runtime hint)** — line 187-189. Current text: "When a cd-eligible command runs and `_NIWA_SHELL_INIT` is unset, niwa prints a hint…". Hint fires on cd-eligible commands.
  - **Gap**: destroy without the wrapper means the user stays in a directory that no longer exists. The hint should fire here too.
  - **Update**: ensure destroy is added to the cd-eligible set R11 enumerates implicitly.

- **R14 (Optionality)** — line 201-202. "All niwa commands must work without the shell function wrapper." Destroy must satisfy this: without the wrapper, destroy still removes the directory; the user gets the standard "no such file or directory" UX from their shell at next command.
  - **Gap**: nothing to amend; current text covers it. Worth a confirming AC in the rework PR.

- **Out of Scope, "apply navigation"** — line 305-307. Current text: "`niwa apply` is non-destructive (updates in place), so the user's cwd stays valid. No cd behavior needed, **unlike resettsuku which destroyed and recreated the workspace.**"
  - **Gap**: this passage explicitly contrasts apply with destroy/recreate; the new destroy has cd behavior (in the inverse direction). The "no cd behavior needed" exemption needs to call out destroy as the explicit exception with cd behavior.
  - **Update**: append a sentence to this paragraph clarifying that destroy is in the new cd-eligible set.

- **Acceptance Criteria sections** — current ACs cover only `create` and `go`. Adding destroy requires net-new ACs for: (a) destroy from inside instance navigates to workspace root or appropriate ancestor, (b) destroy with `--force` from workspace root navigates to workspace parent, (c) destroy without wrapper leaves user with the stale-cwd condition silently and prints a recovery hint per R11.

- **D3 (apply has no navigation behavior)** — line 338-340. Current text contrasts apply's no-cd posture with resettsuku. Same edit as the Out of Scope paragraph above: needs to acknowledge destroy as the deliberate cd-on-removal counterpart.

#### PRD-cross-session-communication.md (Delivered)

This PRD is the canonical owner of the daemon-shutdown-on-destroy contract. Rework must preserve all of it.

- **R2** — line 171-172. "Every written path shall be tracked in `InstanceState.ManagedFiles` so that drift detection and `niwa destroy` work uniformly." (PASSES — load-bearing requirement; rework MUST preserve.)

- **R38** — line 533-540. **Most critical destroy spec in any PRD.** Current text: `niwa destroy` shall send SIGTERM, wait up to the configured destroy-grace window, send SIGKILL if the daemon has not exited, then remove the instance directory. The daemon is stateless across restarts; all durable state is on disk in `.niwa/tasks/` and `.niwa/roles/`.
  - **Gap**: R38 specifies behavior PER-INSTANCE-DESTROY only. The reworked destroy adds two new modes: (a) picker over multiple instances (each must run R38's daemon-shutdown sequence), (b) `--force` workspace-wipe (must run R38 sequence for EVERY instance in the workspace, presumably in some order — concurrent? sequential?). R38 needs a clarifying clause for multi-instance destroys.
  - **Update**: amend R38 to add: "When destroy targets multiple instances (workspace-wide `--force`), the daemon-shutdown sequence shall be applied to each instance's daemon independently. No instance directory shall be removed until that instance's daemon has exited."

- **AC-P11** — line 693-697. "Running `niwa destroy` removes the instance directory…with NIWA_DESTROY_GRACE_SECONDS=1 and a daemon that ignores SIGTERM, destroy completes within ~2 seconds (grace + cleanup)."
  - **Gap**: AC is single-instance only.
  - **Update**: add an AC-P11b (or extend AC-P11) covering: (a) destroy with no instance arg from workspace root with multiple instances triggers picker (assert picker behavior is mutually exclusive with R38); (b) destroy `--force` from workspace root with N instances completes within ~2*N + cleanup seconds.

- **Configuration Defaults table** — line 647. Row: `niwa destroy grace window | 5 seconds | NIWA_DESTROY_GRACE_SECONDS`. (No change.)

- **Known Limitations**, line 1049-1051. "`niwa destroy` is required for clean removal: Deleting an instance directory with `rm -rf` leaves the daemon running until fsnotify detects the missing watched directory."
  - **Gap**: still true. No update needed.

#### PRD-niwa-init-creates-workspace-dir.md (Done)

This PRD's value to destroy is the established protocol pattern, not direct content.

- **R10a** — line 247-263. The `NIWA_RESPONSE_FILE` integration for named init that destroy will mirror.
  - **Gap**: not destroy's content but destroy's mirror. The destroy PRD/spec should cite R10a as precedent ("symmetric with R10a in PRD-niwa-init-creates-workspace-dir") for consistent landing-path semantics.
  - **Update**: no change to this PRD; cross-reference from the new destroy spec.

- **AC-21a, AC-21b, AC-21c** — line 413-427. Establish the wrapper-detected, success-only landing-path contract that destroy will mirror.
  - **Update**: no change here; these become the test pattern for destroy's "land in a safe ancestor" ACs.

#### PRD-workspace-config-sources.md (Done)

- Line 1001 — "niwa's existing destructive-operation pattern (`destroy --force`, `reset --force`) is non-interactive and already familiar."
  - **Gap**: if the destroy rework adds a typed-confirmation guardrail for non-pushed work (the lead mentions this), this passage becomes inaccurate — destroy will *no longer* be uniformly non-interactive.
  - **Update**: amend to qualify the claim ("is non-interactive **except for typed-confirmation when destroy detects unpushed work**") OR drop the claim entirely and replace with a softer reference. Light touch; one sentence.

- Line 198 — "No `--force` flag, no destroy/re-init ritual." (User story for snapshot model; references the *absence* of destroy in the snapshot-rebuild path. No change needed.)

### Cross-cutting gaps

1. **No master "lifecycle" PRD.** The four lifecycle commands (init, create, apply, destroy) are not captured as a coherent set anywhere. The closest artifact is `DESIGN-instance-lifecycle.md`, which is a design doc, not a PRD. Init has its own PRD; create is partially covered in PRD-shell-integration.md (R9); apply is referenced everywhere but not owned by a PRD; destroy has no PRD. The destroy rework is a good moment to create one (see Update strategy).

2. **The shell-wrapper's "cd-eligible command list" is implicit and scattered.** PRD-shell-integration R1 says "create, go". DESIGN-mesh-session-lifecycle adds `session create` to that list (Decision 4). DESIGN-niwa-init-creates-workspace-dir adds `init <name>` to the list. There is no single source of truth for which commands are wrapper-intercepted. Adding destroy is the fourth such extension; this is the right moment to either (a) treat the cd-eligible list as a contract managed in one place, or (b) accept the per-feature accumulation pattern. The rework should at minimum update PRD-shell-integration R1 to add `destroy` so the canonical statement is current.

3. **No PRD owns the "wrapper-driven cd-out-of-deleted-dir" semantic.** This is a new wrapper behavior — all existing cd-eligible commands write a *target* the user named; destroy writes a *fallback safe ancestor* the wrapper picks. The semantic is novel enough to warrant explicit treatment in either PRD-shell-integration (R1 amendment) or a new destroy PRD.

4. **Picker UX has no precedent in any PRD.** No existing PRD covers an interactive picker. The mesh-session-lifecycle PRD explicitly defers fuzzy session-ref matching (line 740-747), preferring exact IDs. Destroy's picker is a new interaction class — single-instance pre-resolved (no picker), multi-instance from root (picker required). Either:
   - The new destroy PRD covers picker UX in scope; or
   - A separate "interactive picker" PRD is created and referenced.
   - The cleaner choice for now is the former; pickers are not yet generalized.

5. **Typed-confirmation guardrail has no precedent.** Existing `--force` flags across niwa (init `--rebind`, destroy `--force`, reset `--force`) are all single-flag opt-ins. A typed-confirmation pattern (user types the workspace/instance name to confirm) is a new safety layer. Either documented in the destroy PRD or in PRD-shell-integration as an extension to "destructive operation patterns".

### Design doc impact

| Design doc | Impact | Notes |
|---|---|---|
| DESIGN-instance-lifecycle.md | HIGH — Decision 4 ("Reset and destroy") and "command flow overview" both encode the current "instance arg from cwd, --force flag" model. The reworked destroy changes this surface. | Must amend or supersede Decision 4 with: (a) cwd-context-driven mode selection (inside instance → that instance, at root → picker or `--force` workspace), (b) typed-confirmation for unpushed-work guardrail, (c) wrapper landing-path emission. |
| DESIGN-shell-navigation-protocol.md | MEDIUM — defines NIWA_RESPONSE_FILE protocol and lists `create` and `go` as cd-eligible commands. Line 32 hardcodes this. | Must amend the cd-eligible command list to include `destroy` (and any other commands that have accreted since: `init <name>`, `session create`). The protocol mechanism itself doesn't change — only the consumer set. |
| DESIGN-contextual-completion.md | MEDIUM — Decision 3 ("Destructive-command completion behavior", line 210-237) chose "complete normally" with no friction. The picker UX is friction by design. Need to reconcile: completion fires when cobra parses args; the picker fires when no arg is given AND wrapper is sourced. They don't conflict but the design doc should call out the relationship. | Amend Decision 3 to note: "When destroy is invoked WITHOUT an instance argument, the picker UI handles selection; tab completion still works for users who prefer typing." |
| DESIGN-cross-session-communication.md | LOW — destroy daemon shutdown is documented (line 568, 802, 1054, 1247) and remains accurate for single-instance destroy. | Must add a one-paragraph clarification in the "destroy" subsection covering the multi-instance (workspace `--force`) path. The PGID-kill-then-daemon-grace ordering must be preserved per-instance. |
| DESIGN-mesh-session-lifecycle.md | LOW — uses "destroy" only for session destroy (different concept). | No change. |

## Implications

### Per-PRD update strategy

| PRD | Strategy | Effort |
|---|---|---|
| PRD-shell-integration.md | **Inline amendment** to R1, R11 wording; **append** to R14 ACs; **edit** the Out-of-Scope/D3 resettsuku paragraphs to acknowledge destroy as a deliberate cd-eligible exception. **Add cross-link** to new destroy PRD. | Medium — ~5 small edits. |
| PRD-cross-session-communication.md | **Inline amendment** to R38 adding the multi-instance clause; **add** AC-P11b for picker and `--force` workspace cases. The PRD is "Delivered" but pre-1.0, so amendment is acceptable; alternatively, a versioned addendum like the overlay PRD's "extended" status. | Small — 1 R amendment, 1 new AC. |
| PRD-niwa-init-creates-workspace-dir.md | **No edits**. Becomes referenced precedent only. New destroy PRD cites R10a/AC-21 pattern for landing-path symmetry. | Zero. |
| PRD-workspace-config-sources.md | **Inline amendment** to line 1001 to soften the "non-interactive" claim if typed-confirmation lands. Otherwise no change. | Trivial — 1 sentence. |
| PRD-mesh-session-lifecycle.md | **No edits**. Session destroy and instance destroy are separate surfaces; the rework doesn't touch session destroy. | Zero. |

### Should we write a new PRD?

**Yes.** Recommend a new `PRD-niwa-destroy.md` (Proposed) for these reasons:

1. **Scope is substantial.** The rework introduces three new behaviors at once: contextual mode selection (in-instance vs. at-root), picker UX (interactive selection from root), and wrapper landing-path emission (cd-out-of-deleted-dir). Each on its own would warrant 1-2 requirements; together they need a coherent spec.

2. **Cross-cutting requirements**. The rework spans surface areas owned by three different PRDs (shell-integration, cross-session-communication, instance-lifecycle-the-design-doc). A new PRD becomes the load-bearing reference that all three can cross-link to, instead of fragmenting the spec across three amendment patches.

3. **Typed-confirmation guardrail is a novel safety pattern.** Even if it's small, it deserves a requirement of its own and ACs covering: typed name match, mismatch behavior, `--force` bypass, interaction with `NIWA_RESPONSE_FILE` (does typed-confirm happen before or after the wrapper response is written?).

4. **The picker is an entirely new interaction model.** Worth its own R items so the design doc has clear constraints to design against (sort order? show last-applied time? include unpushed-work warning inline? ESC behavior?).

5. **PRD-shell-integration is "Accepted" but pre-1.0.** Repeatedly amending it as new commands accrete is fine for small additions; but the destroy rework adds enough that a dedicated PRD reads more cleanly and keeps the shell-integration PRD focused on the protocol/lifecycle, not per-command spec.

The new PRD should:
- Open with a problem statement covering the current destroy's blunt-instrument behavior (workspace destroy not supported; cd-out-of-deleted-dir not handled; picker absent; unpushed-work guardrail absent).
- Cite PRD-shell-integration R1 as the protocol it extends.
- Cite PRD-cross-session-communication R38 as the daemon-shutdown invariant it must preserve.
- Cite PRD-niwa-init-creates-workspace-dir R10a as the landing-path precedent it mirrors.
- Cite PRD-mesh-session-lifecycle R17 (`niwa session destroy`) as the cousin command (independent surface, not affected).
- List ACs that map onto each of the three behaviors above.

### Documentation order

1. **First**: amend PRD-shell-integration R1 + R11 to enumerate destroy in the cd-eligible set. (Independent, can land first.)
2. **Then**: write new PRD-niwa-destroy.md. (Pulls from #1's revised contract.)
3. **Then**: amend PRD-cross-session-communication R38 + AC-P11. (Pulls from PRD-niwa-destroy multi-instance scope.)
4. **Concurrent with #2**: design doc work (DESIGN-niwa-destroy.md or amendment to DESIGN-instance-lifecycle.md Decision 4 + DESIGN-shell-navigation-protocol.md cd-eligible list).
5. **Last**: light touch to PRD-workspace-config-sources line 1001.

## Surprises

1. **The "cd-eligible command list" has no canonical home.** It's distributed across PRD-shell-integration R1, two design docs (mesh-session-lifecycle, niwa-init-creates-workspace-dir), and the actual code in `internal/cli/shell_init.go`. The rework is the third time a feature has had to update this list (after init's named landing in PR #94-ish, after mesh session create). Worth raising whether the list deserves a single owner.

2. **PRD-mesh-session-lifecycle.md uses "destroy" 30+ times but means something completely different.** "Session destroy" (worktree removal) and "instance destroy" (entire instance directory removal) share a verb but no logic. A reader skimming for destroy-related work will get a lot of false positives there. No update needed; just a note for the design doc reader.

3. **PRD-cross-session-communication R2's ManagedFiles invariant is load-bearing for destroy.** Every file the channel installer writes is registered for `niwa destroy` cleanup. The reworked destroy must preserve this — no reason it wouldn't, but worth explicitly testing in ACs that the workspace `--force` path triggers ManagedFiles cleanup for every instance's installed files.

4. **DESIGN-instance-lifecycle.md is Status: Current** and has explicit text saying "Reset of local-only workspaces isn't supported (config would be lost). Mitigation: clear error message with alternative (`destroy + init`)." If destroy now wipes the workspace under `--force`, the mitigation language stays accurate but the *workspace* (containing the local-only config) is itself a target of destroy. A subtle interaction worth checking — destroying a local-only workspace is a config-loss event with no recovery path.

5. **PRD-cross-session-communication AC-P11 is the only AC anywhere that exercises destroy with grace timing.** Picker and workspace-`--force` ACs will be net-new; there is no template to mirror.

## Open Questions

1. **Should PRD-shell-integration's R2 ("Stdout protocol") be retired now?** It's already obsolete (replaced by NIWA_RESPONSE_FILE in the design doc). Doing it as part of destroy rework keeps the PR focused on shell-integration consistency; doing it later keeps the destroy PR scoped. Defer to PR planner.

2. **Does the new PRD need to spec the picker's TUI library / rendering choice?** PRDs are "what to build", not "how". The picker should describe input/output contract (sort, fields shown, keys, exit codes) and leave library/rendering to the design doc. Worth confirming with the brainstorming round.

3. **Typed-confirmation: where does it sit in the response-file protocol?** Specifically: does the typed prompt happen BEFORE the wrapper writes the landing path (so the user can ESC out without cd happening), or AFTER (cd to a safe ancestor first, then prompt, but if user cancels, cd back)? The latter is awful; the former is correct. The new destroy PRD should pin this down explicitly so the design doc doesn't have to.

4. **Workspace-`--force` order**: when destroying N instances under `--force`, do we destroy them sequentially (clean output, slow) or concurrently (fast, output races)? PRD-cross-session-communication R38 doesn't constrain order. The destroy PRD should specify, even if just "sequential, deterministic order".

5. **What does "the workspace is empty" mean for the no-arg root destroy case?** The lead phrasing is "destroy can delete the workspace directory entirely (when empty + no arg, or when --force + no arg)". "Empty" needs a definition — empty of instances? empty of all files including workspace.toml? PRD must pin down.

## Summary

Three PRDs need touching: PRD-shell-integration.md (largest amendment, R1/R11/R14/Out-of-Scope/D3), PRD-cross-session-communication.md (R38 + AC-P11 multi-instance clause), and PRD-workspace-config-sources.md (one-sentence touch on line 1001). A new PRD-niwa-destroy.md is warranted because the rework introduces three substantial new behaviors (contextual mode selection, picker UX, wrapper cd-out-of-deleted-dir) that span at least three existing PRD surfaces and have no canonical home today. The biggest documentation risk is that the implicit "cd-eligible command list" is scattered across one PRD, two design docs, and the code — destroy is the third feature in a row to extend it without consolidation, and the rework PR should at minimum update PRD-shell-integration R1 to keep that contract honest.
