# Lead: Niwa output site map

## Findings

### internal/workspace/apply.go

All calls use `fmt.Fprintf(os.Stderr, ...)` — hardwired, not injectable.

| Line | Message | Kind | Source |
|------|---------|------|--------|
| 170 | `warning: %s` (pipeline warnings from Create path) | warning | niwa |
| 225 | `warning: could not check drift for %s: %v` | warning | niwa |
| 229 | `warning: managed file %s has been modified outside niwa` | warning | niwa |
| 245 | `warning: %s` (pipeline warnings from Apply path) | warning | niwa |
| 254 | `rotated %s` via `emitRotatedFiles(existingState, result, os.Stderr)` — note: `emitRotatedFiles` takes an `io.Writer` parameter but the call site passes `os.Stderr` directly | status | niwa |
| 362–365 | `warning: workspace overlay has new commits since last apply (was %s, now %s)` | warning | niwa |
| 611–613 | `shadowed provider %q [personal-overlay shadows team: ...]` | warning/info | niwa |
| 647–649 | `shadowed %s %q [personal-overlay shadows team: ...]` | warning/info | niwa |
| 688 | `checkRequiredKeys(effectiveCfg, os.Stderr)` — passes os.Stderr as writer, so required-key warnings go there | warning | niwa |
| 709 | `cloned %s into %s` | progress/status | niwa |
| 715 | `pulled %s (%d commits)` | progress/status | niwa |
| 717 | `skipped %s (up to date)` | progress/status | niwa |
| 719 | `warning: could not fetch %s: %s` | warning | niwa |
| 721 | `skipped %s (%s)` (dirty/branch/diverged) | progress/status | niwa |
| 724 | `warning: sync failed for %s: %v` | warning | niwa |
| 727 | `skipped %s (already exists)` (--no-pull path) | progress/status | niwa |
| 811 | `skipped content for %s (claude = false)` | progress/status | niwa |
| 915 | `warning: setup script %s/%s failed for %s: %v` | warning | niwa |
| 968 | `warning: could not remove managed file %s: %v` | warning | niwa |

**Note on suppressibility:** The overlay repo is actively filtered *out* of `allRepos` before the clone loop (line 477–485), so no output referencing the overlay name escapes through these messages.

### internal/workspace/clone.go

Git subprocess output — not injectable, not suppressible.

| Line | What | Kind | Source |
|------|------|------|--------|
| 61–62 | `cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr` on `git clone` | subprocess raw output | git |
| 70–71 | `cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr` on `git checkout <sha>` | subprocess raw output | git |

These write git's progress lines ("Cloning into '...'", "remote: Enumerating objects...", etc.) directly to os.Stderr. There is no wrapping; the subprocess writes bytes to the fd.

### internal/workspace/sync.go

Git subprocess output — not injectable, not suppressible.

| Line | What | Kind | Source |
|------|------|------|--------|
| 68–69 | `cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr` on `git fetch origin` | subprocess raw output | git |
| 88–89 | `cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr` on `git pull --ff-only origin <branch>` | subprocess raw output | git |

These fire for every repo that gets synced during apply. The fetch progress line ("From github.com:org/repo", "   abc123..def456  main -> origin/main") appears inline between niwa's own `pulled`/`skipped` messages.

### internal/workspace/overlaysync.go

Intentionally suppressed — no output at all. The `exec.Command(...).Run()` calls have no Stdout/Stderr assignment. Privacy requirement: the overlay repo name must not appear in standard apply output (R22).

### internal/workspace/configsync.go

Git subprocess output — not injectable, not suppressible.

| Line | What | Kind | Source |
|------|------|------|--------|
| 42–43 | `cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr` on `git pull --ff-only origin` (config repo sync) | subprocess raw output | git |

This fires before the apply pipeline starts (called from `cli/apply.go` and from step 2a inside `runPipeline` for the global config dir).

### internal/workspace/setup.go

Git subprocess output — not injectable, not suppressible.

| Line | What | Kind | Source |
|------|------|------|--------|
| 105–106 | `cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr` on each repo setup script | subprocess raw output | setup scripts |

Setup scripts run after clone/sync for each repo. Their stdout and stderr both land on niwa's stderr.

### internal/workspace/materialize.go (SettingsMaterializer)

| Line | Message | Kind | Source |
|------|---------|------|--------|
| 509 | `warning: %s` (gitignore check warnings) | warning | niwa |

The `EnvMaterializer` has an injectable `Stderr io.Writer` field (line 534–536) with a `stderr()` helper that falls back to `os.Stderr`. The `FilesMaterializer` has the same pattern (line 815–836). Both are injectable for tests but the production code path in `NewApplier` (apply.go:74–79) constructs them with zero-value structs, so both fall back to `os.Stderr` in production.

The `EnvMaterializer.Stderr` is used in `runEnvExamplePrePass` (not shown here but presumably emits `.env.example` parse warnings). The `FilesMaterializer.Stderr` is used only in `noteLocalInfix` — a "note:" diagnostic when a destination path is rewritten to include the `.local` infix.

### internal/cli/apply.go

| Line | Message | Kind | Source |
|------|---------|------|--------|
| 100 | `warning: %s` (config-load warnings) | warning | niwa |
| 138 | `error: applying to %s: %v` | error | niwa |
| 144 | `warning: %v` (registry update failure) | warning | niwa |

### internal/cli/create.go

| Line | Message | Kind | Source |
|------|---------|------|--------|
| 105 | `warning: %s` (config-load warnings) | warning | niwa |
| 152 | `instance created at: %s` (fallback when --repo lookup fails) | status | niwa, via `cmd.ErrOrStderr()` |
| 162 | `Created instance: %s` | status | niwa, via `cmd.ErrOrStderr()` |

Note: `create.go:152` and `create.go:162` use `cmd.ErrOrStderr()` (cobra's method, which falls back to `os.Stderr`), while most other sites use `os.Stderr` directly.

### internal/guardrail/githubpublic.go

| Line | Message | Kind | Source |
|------|---------|------|--------|
| 242 | warning message about plaintext secrets in a public repo | warning | niwa |

Called from `apply.go:634` as `guardrail.CheckGitHubPublicRemoteSecrets(configDir, cfg, a.AllowPlaintextSecrets, os.Stderr)` — the stderr writer is passed in, making this function injectable at the call site. However `apply.go` always passes `os.Stderr`.

### internal/cli/init.go (relevant subset)

| Line | Message | Kind |
|------|---------|------|
| 187 | `warning: could not update registry: %v` | warning |
| 199 | `warning: could not write instance state: %v` | warning |
| 307 | `warning: could not read overlay HEAD: %v` | warning |
| 329 | `warning: could not read overlay HEAD: %v` | warning |

Also uses `cmd.ErrOrStderr()` inside `emitVaultBootstrapPointer` for the "note: this workspace declares a vault" messages (lines 226–231).

---

## Output Site Count Summary

**niwa-authored `fmt.Fprintf` calls to stderr:**
- `internal/workspace/apply.go`: 17 sites
- `internal/workspace/materialize.go`: 1 site (SettingsMaterializer gitignore warning)
- `internal/cli/apply.go`: 3 sites
- `internal/cli/create.go`: 3 sites
- `internal/cli/init.go`: 4 sites
- `internal/guardrail/githubpublic.go`: 1 site

**Git subprocess pipes to os.Stderr (`cmd.Stdout = os.Stderr; cmd.Stderr = os.Stderr`):**
- `clone.go`: 2 sites (clone + sha checkout)
- `sync.go`: 2 sites (fetch + pull)
- `configsync.go`: 1 site (config repo pull)
- `setup.go`: 1 site (setup scripts)

**Total: ~29 niwa-authored output sites + 6 subprocess pipe sites = ~35 total.**

---

## Implications

**Scatter level: high.** The 17 sites in `apply.go` alone span four distinct call contexts (Create warnings, Apply warnings, Apply drift checks, runPipeline clone/sync loop, runPipeline shadow diagnostics, runPipeline setup-script warnings, cleanRemovedFiles). Every site is a raw `fmt.Fprintf(os.Stderr, ...)` with no shared dispatch.

**What a thin `Reporter` interface could wrap:** All niwa-authored `fmt.Fprintf(os.Stderr, ...)` calls follow a small vocabulary of message types: `cloned`, `pulled`, `skipped`, `warning:`, `error:`, `rotated`, `shadowed`, `note:`. A `Reporter` interface with methods like `Progress(verb, target, detail string)`, `Warning(msg string)`, `Error(msg string)`, and `Note(msg string)` could cover all of them. The callers in `apply.go` are clustered enough that threading a `Reporter` through `runPipeline` (already takes `*pipelineOpts` and returns `*pipelineResult`) would require adding one field to the `Applier` struct rather than changing every individual call site's signature.

**Three infrastructure changes are needed for a `Reporter` approach:**
1. Add a `Reporter` field to `Applier` (or `runPipeline`); set it from the CLI layer.
2. Replace every `fmt.Fprintf(os.Stderr, ...)` in `apply.go` with reporter method calls. No signature changes to existing callers are required because `runPipeline` is already an internal function.
3. Wire the injectable `EnvMaterializer.Stderr` and `FilesMaterializer.Stderr` fields (already present, already tested) through to the same reporter's writer.

The CLI layer (`cli/apply.go`, `cli/create.go`) has 6 sites that also use `os.Stderr` directly. These are independent of the `Applier` and would need their own threading, but they emit only warnings/errors — not the high-frequency progress lines — so they are lower priority.

**The `emitRotatedFiles` function is a partial model:** It already takes an `io.Writer` parameter (line 1027), which is the right pattern. The `apply.go` call site (line 254) passes `os.Stderr` directly, so the abstraction is only half-done.

---

## Surprises

1. **`emitRotatedFiles` already has the right signature** (`io.Writer` parameter) but the call site defeats it by passing `os.Stderr` literally. This is an inconsistency: one function was written with injectability in mind but it was never wired up.

2. **The two materializers with injectable `Stderr` fields are never wired in production.** `NewApplier` constructs `&EnvMaterializer{}` and `&FilesMaterializer{}` with zero-value structs, so their `Stderr` field is nil and both fall back to `os.Stderr`. The injectable field exists purely for tests.

3. **`configsync.go` is a sixth subprocess pipe site not mentioned in the prior survey.** It fires before the apply pipeline for both the workspace config repo (CLI layer) and the global config repo (step 2a in `runPipeline`). This means a `git pull` output for the config repo appears *before* any per-repo clone/sync output, with no framing — it looks identical to workspace-repo pulls.

4. **`setup.go` pipes setup script stdout and stderr to os.Stderr.** This means a repo's `niwa-setup/` scripts can emit arbitrary multi-line output that is mixed into the clone/sync stream with no labeling.

5. **The `checkRequiredKeys` function receives `os.Stderr` as a parameter** (apply.go:688), suggesting it was designed for injectable output — but again, the call site always passes `os.Stderr`.

6. **`guardrail.CheckGitHubPublicRemoteSecrets` receives `os.Stderr` as a parameter** (apply.go:634), same pattern — injectable by design, never actually injected in production.

7. **Subprocess git output for clone is interleaved with niwa's own "cloned X into Y" message.** Git prints progress to stderr during clone (remote counting/receiving/resolving), then niwa prints "cloned X into Y" afterward. From the user's perspective this produces a burst of git noise followed by niwa's summary line — the opposite of what inline status would require (summary line first, then optional details).

---

## Open Questions

1. **What does `runEnvExamplePrePass` emit via `EnvMaterializer.Stderr`?** That method wasn't read in this pass. It likely emits `.env.example` parse warnings — worth cataloging as additional sites.

2. **Does `SyncConfigDir` called from `cli/apply.go` (before `applier.Apply`) produce duplicate output vs. the call from `runPipeline` step 2a (global config dir)?** The two calls are for different directories (workspace config vs. global config), but the subprocess output looks the same. Needs verification.

3. **Can git's subprocess output be captured and re-emitted through a `Reporter`?** This requires either piping to an `io.Pipe` and parsing git's line format, or suppressing git output (as `overlaysync.go` already does) and relying on niwa's own status messages. Suppressing is simpler but loses git error details on failure. For inline status indicators, suppressing git's fetch/pull progress and keeping only the error output on failure would be the right model.

4. **Is there an existing integration test that captures stderr as a whole?** If the functional tests compare full stderr output, any restructuring toward a `Reporter` will require updating golden outputs.

5. **How does `cmd.ErrOrStderr()` (used in `create.go` and `init.go`) interact with cobra test harnesses?** The inconsistency between `os.Stderr` and `cmd.ErrOrStderr()` at different sites means a `Reporter` injected via `Applier` wouldn't automatically capture the CLI-layer messages — those would need separate threading.

---

## Summary

There are approximately 35 output sites total: 29 niwa-authored `fmt.Fprintf` calls (17 concentrated in `internal/workspace/apply.go`) and 6 git subprocess pipes hardwired to `os.Stderr` across `clone.go`, `sync.go`, `configsync.go`, and `setup.go`. The key architectural challenge is the git subprocess output: because subprocess stdout/stderr are assigned directly to `os.Stderr`, any inline status display would require either suppressing git's progress stream entirely (as `overlaysync.go` does) or capturing it through a pipe, since there is no way to intercept bytes a subprocess writes to a file descriptor. The biggest open question is whether the functional test suite compares raw stderr output, which would determine how invasive a `Reporter` refactor needs to be before it can ship.
