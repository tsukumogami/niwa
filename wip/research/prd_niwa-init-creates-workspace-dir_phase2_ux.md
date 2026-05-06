# Phase 2 Research: UX Perspective

## Lead 1: User-facing error message and remediation guidance

### Findings

**Existing error vocabulary in `internal/workspace/preflight.go`.** Three sentinels and one structured wrapper:

- `ErrWorkspaceExists` ("workspace already exists") — `.niwa/workspace.toml` already lives at the target.
- `ErrNiwaDirectoryExists` (".niwa directory exists without workspace config") — orphaned `.niwa/`, no toml.
- `ErrInsideInstance` ("directory is inside an existing instance") — walking up finds an `instance.json`.
- `InitConflictError{Err, Detail, Suggestion}` wraps a sentinel with a context line and a one-sentence remediation.

The three concrete `InitConflictError` instances (preflight.go:60–84) use this voice:

| Case | Detail | Suggestion |
|---|---|---|
| `ErrWorkspaceExists` | `found .niwa/workspace.toml` | `Use niwa apply to update the existing workspace` |
| `ErrNiwaDirectoryExists` | `found .niwa directory without workspace.toml` | `Remove the <abs path>/.niwa directory and retry` |
| `ErrInsideInstance` | `this directory is inside the instance at <path>` | `Change to a directory outside the existing instance before running init` |

**What the user actually sees today.** `runInit` in `internal/cli/init.go:123-130` prints **only** `Detail` and `Suggestion`, joined by `\n  ` (two-space indent for the suggestion). The sentinel `Err` value is *not* surfaced in the user-visible string — only the `Detail` and `Suggestion` strings reach stderr. The wrapped `InitConflictError.Error()` method (which uses `"%s: %s. %s"` joining all three) is **not** what users see; only `errors.As`-extracted `Detail/Suggestion` is rendered. So in practice today the rendered form is:

```
Error: <detail>
  <suggestion>
```

**Voice across the rest of the CLI.** `internal/cli/` favors lowercase imperative-ish error messages, frequently with a trailing `\nhint: …` line (`go.go:128, 132, 219`) for actionable remediation. Examples:

- `repo %q not found in current instance: %w\nhint: use "niwa status" to list available repos`
- `not inside a workspace\nhint: use "niwa go <workspace>" to navigate to a registered workspace: %s`
- `instance directory already exists: %s` (`create.go:132`) — bare path, no hint.
- `workspace %q not found in registry. Registered workspaces: %s` (`apply.go`/`create.go`) — comma-list of recovery candidates.

So the established niwa idiom for an "already exists" / "discoverability" fix is: **first line states the problem with the offending path; second line is a `hint:` (or two-space-indented suggestion) pointing at the fix.** The init pre-flight uses the indented-suggestion form via `InitConflictError`; everywhere else uses `\nhint: …`. Both are precedented; the `InitConflictError` form is the right one for this new sentinel because it's reading the existing preflight family.

**On differentiating file vs. directory vs. symlink.** Currently `CheckInitConflicts` uses `os.Stat` (which follows symlinks) and never differentiates among path types — for the existing three cases there's no need. The new `<cwd>/<name>` existence check is different: any pre-existing path type is a hard reject (per scope). I see two sensible positions:

1. **Single message regardless of type** — "path already exists at <abs>". Simple, terse, matches `create.go:132` (`instance directory already exists: %s`).
2. **Type-aware message** — distinguish file/dir/symlink via `os.Lstat`, e.g. "<abs> already exists (file)" / "(directory)" / "(symlink)". Slightly more helpful: a user who sees "(symlink)" knows immediately they may have a stale workspace alias; "(directory)" tells them to either pick a new name or remove the dir; "(file)" is almost certainly a typo.

Recommendation: position **(2)** with `os.Lstat` (so symlinks aren't masked), but kept to a one-word qualifier. The cost is one extra branch; the value is real because the new flow is the **first** preflight check that runs against an arbitrary user-supplied path — the existing three cases all run against a directory the user is already standing in, so type ambiguity didn't matter before.

**On the "use `niwa apply` to update an existing workspace" hint.** Today this hint fires whenever `ErrWorkspaceExists` triggers. Under the new flow, `<cwd>/<name>` may exist for **four** distinct reasons:

| Sub-case | Suggestion that's actually useful |
|---|---|
| Plain file at `<cwd>/<name>` | "Pick a different name or remove the file" |
| Unrelated directory at `<cwd>/<name>` (no `.niwa/`) | "Pick a different name, remove the directory, or run `niwa init` from inside it without a positional name" |
| Niwa workspace at `<cwd>/<name>` (has `.niwa/workspace.toml`) | "Use `niwa apply` to update the existing workspace" — same as today's `ErrWorkspaceExists` |
| Orphaned `.niwa/` at `<cwd>/<name>` | "Remove `<abs>/.niwa` and retry" — same as today's `ErrNiwaDirectoryExists` |

A clean approach: introduce **`ErrTargetDirExists`** as the new outer sentinel for "the named target path is occupied." When the existing path *is itself a niwa workspace* (i.e., `<cwd>/<name>/.niwa/workspace.toml` exists), the preflight could downgrade to today's `ErrWorkspaceExists` so the existing "use niwa apply" hint still triggers. This lets us preserve the existing learning moment for the most-common upgrade path while keeping the new sentinel focused on "this name is taken."

The exploration's `wip/explore_niwa-init-creates-workspace-dir_findings.md:99-101` proposes the simpler shape: a single new sentinel `ErrTargetDirExists` that fires for any pre-existing path type, with the existing `CheckInitConflicts(targetDir)` continuing to run after it (which would never trigger because the target doesn't exist). The PRD should pick between:

- **Simple**: one sentinel, one message, one suggestion ("pick a different name or remove the path").
- **Smart**: differentiate sub-cases (file/dir/symlink/existing-workspace/orphaned-niwa) with tailored suggestions.

Smart is more helpful for a flow whose whole purpose is to be a UX papercut fix; the cost is ~15 lines of branching in preflight.go.

### Implications for Requirements

The PRD must specify:

1. **Sentinel error name and message.** A new sentinel (`ErrTargetDirExists` per exploration) is added to `internal/workspace/preflight.go`. Sentinel string: lowercase, ≤6 words (e.g., `"target path already exists"`).

2. **Error rendering format.** The error must follow the existing init preflight rendering: `Detail` line followed by an indented `Suggestion` line (matching `init.go:127`, where today's `\n  ` join already works). New errors should reuse `InitConflictError` rather than introduce a new shape.

3. **Detail line content.** Must include the absolute path of the conflicting target (consistent with `instance directory already exists: %s` in `create.go:132` and the existing `ErrNiwaDirectoryExists` detail).

4. **Path type qualifier (recommended).** PRD should require one-word qualifiers (file / directory / symlink) so the user can distinguish a typo from an intentional collision. Implementation uses `os.Lstat` so symlinks aren't followed.

5. **Sub-case routing for existing niwa workspaces.** When `<cwd>/<name>` *is* a niwa workspace (has `.niwa/workspace.toml`), the preflight should surface today's `ErrWorkspaceExists` message (with the "Use niwa apply to update the existing workspace" suggestion), not the generic `ErrTargetDirExists`. Same for orphaned `.niwa/` → `ErrNiwaDirectoryExists`. Reasoning: those existing suggestions are the right remediation; reusing them preserves a learning moment that's already wired up.

6. **Suggestion text for the new generic case.** PRD should specify the wording. Proposed: `"Pick a different name or remove <abs path> and retry."` This mirrors the `ErrNiwaDirectoryExists` voice ("Remove the <path> and retry") and is consistent with `internal/cli/go.go` patterns.

7. **No second-pass `CheckInitConflicts`.** Once the `<cwd>/<name>` target check passes (path didn't exist), there's nothing for `CheckInitConflicts(targetDir)` to find — the directory is freshly created by niwa. The PRD should clarify this so the implementer doesn't paint themselves into a corner trying to keep the second call alive against a directory that's about to be created.

### Open Questions

1. **Type-aware vs. single message?** Is the "(file)" / "(directory)" / "(symlink)" qualifier worth the small branching cost? My recommendation is yes; user input needed to confirm.

2. **Should the generic-case suggestion mention multiple recovery options, or pick one?** Compare `ErrInsideInstance`'s single-option suggestion ("Change to a directory outside…") to the multi-option phrasing I proposed above. niwa convention seems to favor single-option suggestions; PRD should commit to one.

3. **When the existing path is an unrelated directory (not a niwa workspace, no `.niwa/`), should the suggestion mention `niwa init` (no name) as an option?** I.e., should the error coach the user toward "If you meant to init in that directory, `cd` into it and run `niwa init` without a name"? This bridges to the discoverability lead — see Lead 3.

## Lead 3: Discoverability and migration

### Findings

**Current `Long:` help text on `initCmd` (`internal/cli/init.go:57-72`).** The first line says "Initialize a new niwa workspace **in the current directory**." That's the line that has to change. The three modes documented are:

```
niwa init                 -> scaffold .niwa/workspace.toml in cwd
niwa init <name>          -> scaffold or clone-from-registry in cwd
niwa init <name> --from   -> clone --from into cwd
```

All three modes share the implicit assumption "in cwd." Under the new flow, only mode 1 (no positional, no `--from`) and mode 3-without-name (`niwa init --from <src>` with no positional) keep that semantic. Modes that take a positional `<name>` switch to "in `<cwd>/<name>/`."

**README `niwa init` mentions (`README.md`).** Found at lines 40, 113-116, 138, 157, 173, 176. Notable hits:

- **Quick start step 2** (line 39-41): `mkdir my-workspace && cd my-workspace; niwa init my-project`. The new flow lets us drop the `mkdir &&` and the `cd`, replacing this with `niwa init my-project` from any directory. This is the headline-level example for first-time users — it must update.
- **Shared workspace configs** (line 138): `niwa init my-team --from my-org/workspace-config; niwa apply`. Currently implies you've already `cd`'d. Under the new flow this just works as-is, but the surrounding prose should be updated to make the new behavior explicit (since users currently have to mkdir+cd).
- **Re-using a registered name** (line 157): `cd ~/other-dir; niwa init my-team`. This is mode `modeClone` (named workspace, registry has source URL). Under the new flow, this *also* changes — `niwa init my-team` from `~/other-dir` will create `~/other-dir/my-team/` instead of initializing in `~/other-dir`. README needs a note here.
- **Commands table** (lines 113-116): currently `niwa init [name]` → "Create a new workspace with a scaffolded config." Should explicitly mention "creates `<cwd>/<name>/` if a name is given."
- **`docs/guides/workspace-config-sources.md:45,56,69`** also has `niwa init my-workspace --from org/...` examples. These already "naturally" assume the new flow (no preceding `cd`), so they'll *become correct* once the behavior change ships — minor wording update, not a structural rewrite.

**Error message as a learning moment.** Existing niwa convention strongly supports this. Examples:

- `"workspace %q root directory not found: %s\nThe registry entry may be stale. Re-register with: niwa init"` (`go.go:115`) — error tells the user the exact recovery command.
- `"workspace %q has no instances. Create one with: niwa create %s"` (`go.go:154`) — same pattern.
- `"-r requires being inside a workspace instance\nhint: use \"niwa go -w <workspace> -r %s\" to target a specific workspace"` (`go.go:128`) — same pattern.

The "directory exists" error is the **primary touchpoint** for users who learned `mkdir + cd + niwa init`: they'll mkdir the dir, cd in, run `niwa init foo`, hit the "target path already exists" error because `<cwd>/foo` (which they tried to mkdir-then-cd-into) is now their cwd's child via mkdir. Wait — actually re-read the failure mode: a user following the old flow does `mkdir foo; cd foo; niwa init foo`. From the new code's perspective, `cwd` is `foo` and the target is `foo/foo/`. `foo/foo/` doesn't exist → init succeeds, but creates a nested `foo/foo/` directory the user didn't expect.

This is a worse failure mode than I initially expected. The user will get a successful "Workspace 'foo' initialized." message and walk away thinking everything worked, only to discover later that their workspace lives at `foo/foo/.niwa/`. **The error path doesn't catch this case at all.** Two potential mitigations:

1. **Heuristic warning when `filepath.Base(cwd) == name`.** If the user runs `niwa init foo` from a directory itself named `foo`, emit a stderr note: `"note: niwa init now creates the directory for you. Looks like you're already inside '<cwd>'; consider running from the parent directory."` This catches the most common old-flow muscle-memory case without being intrusive.

2. **Heuristic warning when `<cwd>` is empty or near-empty.** If the user just `mkdir`'d and `cd`'d into a fresh empty dir, niwa creating yet another subdirectory inside it is almost certainly not what they wanted. But this is fuzzier and harder to land cleanly.

Mitigation (1) is cheap (one `filepath.Base` comparison), specific (only fires for the documented old-flow pattern), and non-blocking (informational, init still succeeds). Worth specifying in the PRD.

**On help text rewording.** The `Long:` block needs to communicate three things: (a) `niwa init` (no name) still inits in cwd, (b) `niwa init <name>` now creates `<cwd>/<name>/`, (c) the positional name overrides any cloned `[workspace] name`. Proposed rewrite preserved-mode-by-mode:

```
Initialize a new niwa workspace.

Modes:

  niwa init
    Scaffold a minimal .niwa/workspace.toml in the current directory
    with commented examples. The workspace name defaults to "workspace".
    No registry entry is created.

  niwa init <name>
    Create <name>/ in the current directory and initialize the workspace
    inside it. If <name> is registered in the global registry with a
    source URL, clone from that source (same as --from). Otherwise
    scaffold locally and register the workspace as local-only.

  niwa init <name> --from <org/repo>
    Create <name>/ in the current directory, shallow-clone the config
    repo as <name>/.niwa/, and register the name-to-source mapping in
    the global registry. The given <name> overrides any [workspace] name
    declared in the cloned config.
```

**On precedents for breaking-change communication.** I checked the niwa git log for "BREAKING" / "breaking" / "deprecat" markers. Only one match: `81aae0b feat(config): rename [content] to [claude.content]`. That commit handled the rename by **accepting both forms**, with a parser warning on the deprecated path until v1.0. That's a config-schema rename, not a CLI-behavior change, so it's not directly applicable; the equivalent for CLI behavior would be `--here`/`--in-place` flag for old-flow users, which the PRD scope explicitly puts out of scope ("clean breaking change", line 41-42 of the scope doc). There is **no CHANGELOG, RELEASES.md, or MIGRATION.md** in the repo (`find` for those files returned empty); the project communicates changes via GitHub PR descriptions and commit messages only.

Pre-1.0 niwa has not historically used deprecation messages or migration notes for CLI-shape changes. The error message + help text + README are the available channels, and they're sufficient for the audience size. No precedent to break.

### Implications for Requirements

The PRD must specify:

1. **`Long:` help text rewrite.** PRD should require the help text to (a) drop "in the current directory" from the lead sentence, (b) explicitly state "Create `<name>/` in the current directory" for both modes that take a positional name, (c) mention the name-overrides-config semantic for `--from`. Proposed wording above can be used as the starting point.

2. **README updates.** PRD must require updates to:
   - Quick start step 2 (lines 39-41): drop `mkdir && cd`.
   - Commands table row for `niwa init [name]` (lines 113-116): mention directory creation.
   - "Shared workspace configs" prose (lines 132-141): drop the implicit cd assumption.
   - "Once registered, the name can be reused" example (lines 152-159): explicitly note the new directory will be created.
   - `docs/guides/workspace-config-sources.md` lines 45, 56, 69: confirm examples align with new flow (likely just a tone tweak).

3. **Error message learning moment (recommended).** When `<cwd>/<name>` exists *and* `filepath.Base(cwd) == name`, append a hint to the error: `"note: niwa init now creates the directory; running from <parent of cwd> would target a fresh <name>/ instead."` This catches the old-flow muscle memory.

4. **Old-flow muscle-memory heuristic (separate from #3).** When `niwa init <name>` runs from a directory whose basename is `<name>` and `<cwd>/<name>` does **not** exist (so init proceeds and creates a nested `<cwd>/<name>/`), niwa should emit a stderr note before proceeding (or as part of the success message): `"note: niwa init now creates a subdirectory; this initialized <name>/ inside <cwd>. To init in the current directory, run niwa init without a name."` This catches the case where the old-flow user's `mkdir foo; cd foo; niwa init foo` would silently succeed with a doubly-nested path. **PRD should explicitly decide whether to include this heuristic** — it's the difference between "silent footgun for old-flow users" and "self-correcting flow."

5. **No `--here` / `--in-place` flag.** Already in scope (line 41-42); PRD should keep this exclusion explicit because some reviewers will ask.

6. **No CHANGELOG / migration doc requirement.** Not the niwa convention; not needed. PRD should not require these.

### Open Questions

1. **Should the muscle-memory heuristic (Implication #4) be in scope?** It catches the silent-double-nesting failure mode but adds one informational message that may surprise users who *intentionally* ran from a same-named directory. My recommendation: yes, include it, because the silent failure is genuinely bad and the "false positive" rate is near-zero (no one names a directory the same as the workspace they're about to put inside it on purpose).

2. **Should the help text mention the name-override semantic explicitly, or only document it in the README?** Verbose `Long:` help text is fine in cobra, but if the PRD wants this to be terse, the override semantic could live only in README + error messages.

3. **For the `--from` mode specifically: when the cloned config's `[workspace] name` differs from the positional, should niwa emit a one-time notice on the first `niwa apply` (or in the init success message) explaining that the positional name "wins"?** E.g., `"note: workspace name 'foo' (from your command) overrides 'bar' (from cloned config)."` This is a small surface but it's the kind of thing that would show up in a confused-user issue six months from now.

## Summary

The new `ErrTargetDirExists` sentinel should reuse the existing `InitConflictError{Detail, Suggestion}` shape (`internal/workspace/preflight.go`) with a path-type-aware detail line and a remediation suggestion; when the existing path is itself a niwa workspace or orphaned `.niwa/`, the preflight should fall through to the existing `ErrWorkspaceExists`/`ErrNiwaDirectoryExists` messages so today's "Use niwa apply" learning moment survives. The discoverability story rests on three updates — `init.go` `Long:` help text, README quick-start (drop `mkdir && cd`), and a muscle-memory heuristic in either the error message or success output to catch the `mkdir foo; cd foo; niwa init foo` pattern that would otherwise silently create a nested `foo/foo/` directory. Open questions for human input: path-type qualifier in the error, whether to include the muscle-memory heuristic, and how loudly to surface the name-override semantic.
