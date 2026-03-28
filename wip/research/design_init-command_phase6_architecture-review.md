# Architecture Review: Init Command Design

## Overview

The design at `docs/designs/DESIGN-init-command.md` proposes a `niwa init` command with three modes: detached scaffold, named scaffold, and remote clone. This review evaluates structural fit with the existing codebase.

## Question 1: Is the architecture clear enough to implement?

**Yes, with one gap.** The command flow (section "Solution Architecture") is explicit about ordering and the package placement of each component. The Go types section gives concrete signatures. An implementer can start from this.

**Gap: `--ref` flag mentioned in Decision 1 but absent from `InitOptions`.** The design describes `--ref <tag>` for pinning a clone to a specific tag or commit (Decision 1, step 2), but `InitOptions` only has `Name` and `From`. Either add `Ref string` to `InitOptions` or clarify that `--ref` is deferred to a future phase.

**Gap: `--review` flag described in Decision 1 (step 3) and Security Considerations but not present in InitOptions or the command flow.** Should be explicitly listed as deferred if not in scope.

## Question 2: Are there missing components or interfaces?

### 2a. CloneShallow vs existing Cloner pattern -- Blocking

The design says "Add `CloneShallow(ctx, url, targetDir string, opts CloneOptions) error`" to `internal/workspace/clone.go`. But the existing `Cloner` is a struct with methods (`Clone`, `CloneWithBranch`). The proposed `CloneShallow` is described as a standalone function signature, not a method on `Cloner`.

This is a structural concern. The existing pattern is `Cloner.Clone(ctx, url, dir)` -- the init command should use `Cloner.CloneShallow(ctx, url, dir, opts)` as a method, not introduce a parallel calling convention. The design text says "Extended: internal/workspace/clone.go" which implies method addition, but the Go types section shows `CloneOptions` as a standalone struct without showing it as a `Cloner` method. The implementer should be told explicitly: add a method to `Cloner`, not a package-level function.

**Recommendation:** Amend the design to show `func (c *Cloner) CloneShallow(ctx context.Context, url, targetDir string, opts CloneOptions) error` as the signature.

### 2b. URL construction from org/repo -- Advisory

The design references constructing a clone URL from `<org/repo>` using `GlobalConfig.CloneProtocol()` (HTTPS or SSH). No function or method is proposed for this. The logic is simple (format string), but it should live somewhere reusable -- likely a method on `GlobalConfig` or a helper in `internal/workspace/clone.go`. If it's inlined in `internal/cli/init.go`, it couples URL construction to the CLI layer.

**Recommendation:** Add a `ResolveCloneURL(orgRepo string) string` method to `GlobalConfig` or a helper in the workspace package.

### 2c. Name validation for `--from` argument -- Advisory

The design's Security Considerations mention that `[a-zA-Z0-9._-]+` regex prevents injection in clone URLs. The existing `validName` regex in `internal/config/config.go` matches this exact pattern. The design doesn't reference it. The implementer might create a second regex. The `--from` argument takes `org/repo` format (with a slash), so `validName` as-is won't work directly -- you'd need to split on `/` and validate each segment, or create a `validOrgRepo` pattern.

**Recommendation:** Explicitly call out reusing `config.validName` (currently unexported) or exporting it so init can validate org/repo segments against the same pattern.

### 2d. DiscoverInstance vs DiscoverConfig confusion -- Advisory

The design's Case 2 uses `DiscoverInstance(cwd)` to find `.niwa/instance.json`. But `instance.json` is only created by `niwa apply` (post-clone state). A workspace that was initialized but never applied would have `.niwa/workspace.toml` but no `instance.json`. The design's Case 1 catches workspace.toml. So Case 2 only triggers if you're inside a *different* directory's applied workspace -- which is the correct intent but worth clarifying.

The existing `config.Discover(cwd)` walks up looking for `.niwa/workspace.toml`, while `workspace.DiscoverInstance(cwd)` walks up looking for `.niwa/instance.json`. The design should note that both are checked: Case 1 handles workspace.toml in $PWD, Case 2 handles instance.json in ancestors. But if there's a workspace.toml in a parent (not $PWD), it won't be caught by either case. This is probably fine (init creates a nested workspace), but should be an explicit decision.

## Question 3: Are the implementation phases correctly sequenced?

The design doesn't include explicit implementation phases. The command flow (parse -> preflight -> execute -> postflight) is a correct execution sequence, but there are no phased delivery milestones.

**Recommended phasing:**

1. **Phase 1: Local scaffold** (`niwa init` and `niwa init <name>` without registry). Scaffold template, preflight checks, `internal/workspace/scaffold.go`, `internal/cli/init.go`. No network dependency. Testable in isolation.

2. **Phase 2: Registry integration.** Wire up `LoadGlobalConfig` / `SetRegistryEntry` in the init post-flight. Enable `niwa init <name>` to register and `niwa init <name>` (second time, different dir) to resolve.

3. **Phase 3: Remote clone.** `CloneShallow` on `Cloner`, `--from` flag, URL construction from org/repo + clone protocol.

4. **Phase 4: Edge cases.** `--ref` for tag/commit pinning, `--review` for inspection before commit.

This sequencing has zero cross-phase dependencies (each phase builds on the prior one) and delivers a usable `niwa init` after Phase 1.

## Question 4: Are there simpler alternatives we overlooked?

### 4a. Scaffold as config.Parse round-trip -- not recommended

One alternative: generate the scaffold by constructing a `WorkspaceConfig` struct and marshaling it to TOML. This guarantees the scaffold always matches the schema. However, it loses the commented-out examples (TOML marshaling doesn't produce comments), which are the primary UX goal. The design's choice of a Go string constant is correct.

### 4b. Skip Mode B (named, not registered) -- worth considering

Mode B (`niwa init <name>` where name is not in registry) scaffolds locally and registers. This is functionally identical to `niwa init` + manually editing the name field. The registry-lookup behavior in Mode B (check registry, if found clone, if not scaffold) introduces a control flow branch that could surprise users: `niwa init foo` does different things depending on whether someone previously registered "foo" on this machine.

This isn't blocking -- the design documents the behavior clearly -- but it's worth a UX note in the design: "If `foo` was previously registered from a different directory, `niwa init foo` will clone from that source, which may not be the user's intent."

### 4c. Merge CloneShallow into CloneWithBranch -- recommended

The existing `CloneWithBranch` already constructs git args. Adding `--depth 1` to it (controlled by a parameter or options struct) avoids a third method. Refactor to:

```go
func (c *Cloner) CloneWith(ctx context.Context, url, targetDir string, opts CloneOptions) (bool, error)
```

where `CloneOptions` has `Branch`, `Depth`, and (later) `Ref`. Keep `Clone` and `CloneWithBranch` as convenience wrappers. This is a smaller API surface than adding `CloneShallow` alongside the existing two methods.

## Structural Fit Assessment

### Fits well

- **CLI pattern**: New file `internal/cli/init.go` with cobra subcommand registered via `init()` -- matches `apply.go` exactly.
- **Package boundaries**: scaffold logic in `internal/workspace/`, config operations in `internal/config/`, CLI glue in `internal/cli/`. Dependencies flow downward.
- **Registry reuse**: Uses existing `LoadGlobalConfig`, `SetRegistryEntry`, `SaveGlobalConfig` without modification.
- **State reuse**: Uses existing `DiscoverInstance` for conflict detection.
- **Constants reuse**: `.niwa` directory name already defined as `workspace.StateDir` and `config.ConfigDir`.

### Potential divergence

- **Duplicate constants**: The design says "check $PWD/.niwa/workspace.toml" -- the path components are already defined as `config.ConfigDir` and `config.ConfigFile`. The implementation must use these constants, not hardcode strings.
- **PreflightResult type**: This is a new error-reporting pattern. The existing codebase uses Go errors directly (e.g., `config.Discover` returns `error`). Introducing a `PreflightResult{OK, Error}` struct creates a parallel error reporting mechanism. Consider using standard errors with sentinel values or error types instead, to match how `Discover` and `LoadState` report failures.

## Summary of Findings

| # | Finding | Severity | Recommendation |
|---|---------|----------|----------------|
| 1 | `CloneShallow` should be a `Cloner` method, not standalone | Blocking | Amend design to show as method on Cloner; consider merging with `CloneWithBranch` via options struct |
| 2 | `PreflightResult` introduces parallel error pattern | Blocking | Use standard Go errors with typed errors or sentinel values instead |
| 3 | `--ref` and `--review` flags mentioned but absent from `InitOptions` | Advisory | Either add to `InitOptions` or explicitly mark as deferred |
| 4 | URL construction from org/repo has no proposed home | Advisory | Add helper method to avoid inlining in CLI layer |
| 5 | `validName` regex should be reused, not duplicated for org/repo validation | Advisory | Export or add a validation helper |
| 6 | No explicit implementation phases in design | Advisory | Add phased delivery as recommended above |
| 7 | Mode B (named, not registered) has surprising behavior when name was previously registered elsewhere | Advisory | Add UX note about registry collision |

Blocking count: 2
