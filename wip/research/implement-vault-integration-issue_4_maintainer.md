# Maintainer Review — Vault Integration Issue #4 (resolver + apply.go wiring)

**Target commit:** `1ad548b22a87b8b81f2ee2589db44ba31618bb88` on branch `docs/vault-integration`

**Scope:** Issue 4 of `docs/plans/PLAN-vault-integration.md` — `internal/vault/resolve/`, `internal/workspace/apply.go` wiring, `MergeGlobalOverride` signature change, `internal/workspace/apply_vault_test.go`, `internal/vault/fake/fake.go`, `internal/vault/registry.go` (Unregister), `internal/workspace/override.go`.

## Verdict

**Approve.** The code is unusually clear for a fan-out pivot of this size. GoDoc is thorough, error messages name the right things, the sub-package rationale is documented at the package level, and test naming tracks behavior. The blocking count is 0; one advisory finding below is worth addressing but does not hold the PR.

- **blocking_count: 0**
- **non_blocking_count: 3**

---

## Findings

### A1 (Advisory) — `walkFilesKeys` doc contradicts itself

**File:** `internal/vault/resolve/resolve.go:405-421`

The doc comment is structured as two paragraphs that tell the reader two different things. The first paragraph asserts the function "walk[s] here" and describes promotion behavior ("we promote such keys by keeping the map-level structure and replacing the key with the resolved plaintext"). The second paragraph then says "For v1, the simplest correct behavior: do nothing here." The function body is `return nil`.

The next developer who reads paragraph one will form the mental model that this function resolves `vault://` references inside files-map keys. They'll then be confused when they scroll down and see a two-line no-op. Worse, if they land here while debugging a missing-files-key-resolution symptom, they'll wonder whether a bug has silently broken behavior that the comment claims exists.

The type alone (`map[string]string`, not `map[string]MaybeSecret`) makes paragraph one's "promotion" literally impossible. The comment conflates "what we might do in the future" with "what we do now."

**Fix:** Lead with the no-op behavior, then state why:

```go
// walkFilesKeys is a no-op in v1: config.Files maps have string keys
// and string values, so there is no MaybeSecret slot for the resolver
// to visit. vault:// references inside files-map keys are not
// supported in v1; the parse-time validator (validate_vault_refs.go)
// rejects them.
//
// Kept as a method on walker so the file-walk surface in
// ResolveWorkspace / walkGlobalOverride stays symmetric with env /
// settings. Future work can revisit if we choose to support
// vault-keyed files mappings end-to-end.
```

The two-paragraph hypothetical-first structure is the exact shape of a comment that sends a debugging detour. It's the strongest maintainability issue in the PR.

### A2 (Advisory) — `ResolveWorkspace` GoDoc mixes "caller owns" with "we sometimes own"

**File:** `internal/vault/resolve/resolve.go:178-203`

The doc comment says "The caller owns the bundle: ResolveWorkspace does not call CloseAll" immediately before "When opts.TeamBundle is nil, ResolveWorkspace builds a bundle internally and closes it on return."

These are not contradictory (the ownership switches based on who constructed the bundle), but a fast reader will trip on the juxtaposition. The invariant is actually simpler than the doc makes it sound: **ResolveWorkspace closes exactly the bundles it constructs; caller-supplied bundles are caller-owned.**

**Fix:** Rewrite the ownership paragraph as a single rule:

```go
// Bundle lifetime:
//   - opts.TeamBundle non-nil: caller-owned. ResolveWorkspace does
//     not call CloseAll on it.
//   - opts.TeamBundle nil: ResolveWorkspace builds a bundle from
//     cfg.Vault and closes it before returning. Use TeamBundle when
//     you need to run R12 collision and shadow checks across team +
//     personal bundles before resolution.
```

Non-blocking because the existing wording, while clunky, is factually correct.

### A3 (Advisory) — `effectiveCfg := cfg` line + comment in apply.go are lightly stale

**File:** `internal/workspace/apply.go:229-232`

The comment at lines 229-230 reads "cfg remains the original for per-instance reads; effectiveCfg carries the merge." After the resolver wiring, `cfg` is only read once more below this point (line 306: `cfg.Workspace.Name`), and `effectiveCfg := cfg` at line 231 is always overwritten at line 292. The comment reads like it's describing a meaningful invariant ("the original is preserved for reads"), but the actual motive is narrower: `cfg.Workspace.Name` is the flattening target for `ResolveGlobalOverride`.

A next developer reading this will look for the "per-instance reads" the comment claims — there's really just one (line 306). Not load-bearing; a brief touch-up would help:

```go
// Resolve first (per-file vault bundles), then merge. cfg is read
// one more time below for cfg.Workspace.Name (the flattening target
// for ResolveGlobalOverride); after that, effectiveCfg carries
// everything downstream.
effectiveCfg := cfg
```

---

## Items Checked and Found Clear

### 1. `ResolveWorkspace` GoDoc covers behavior and non-behavior

`resolve.go:178-203` enumerates the five resolution branches (plaintext in vars, plaintext in secrets, vault URI, optional miss, allow-missing) and states that "The caller owns the bundle: ResolveWorkspace does not call CloseAll." See A2 for the minor rewrite suggestion; the substantive content is present and correct.

### 2. Resolver error messages are actionable (R9)

The resolver distinguishes three failure modes cleanly:

- **Unknown provider** (`resolve.go:490-505`): names the TOML location and the referenced provider label, wraps `ErrKeyNotFound`.
- **Missing key** (`resolve.go:513-533`): names the key, names the provider, distinguishes `?required=false` from `--allow-missing-secrets`, wraps `ErrKeyNotFound`. This is the R9 remediation path the PLAN calls for.
- **Provider unreachable** (`resolve.go:538-543`): uses `secret.Errorf` (not `fmt.Errorf`) to scrub any fragments the backend surfaces, wraps `ErrProviderUnreachable`.
- **Parse error on vault URI** (`resolve.go:485-488`): names the TOML location so the user knows which field is malformed.

`providerLabel("")` → `"(anonymous)"` keeps the anonymous-singular case actionable; the collision helper at `CheckProviderNameCollision` does the same.

### 3. `override.go` team_only error messages

Lines 434-444, 462-472, 476-487, 519-529, 534-545, 549-564 — every team_only rejection names the specific key with its full dotted path (`env.secrets.GITHUB_TOKEN`, `claude.settings.permissions`, etc.), references `[vault].team_only` by its concrete TOML key, offers two remediation paths (drop the overlay key, or relax the team's lock), and wraps `vault.ErrTeamOnlyLocked` so `errors.Is` works downstream. The caller learns "team_only rejected key GITHUB_TOKEN" rather than "merge failed."

### 4. `apply_vault_test.go` readability

The three tests (`TestApplyResolvesVaultSecretEndToEnd`, `TestApplyVaultProviderMissingKeyErrors`, `TestApplyVaultAllowMissingSecretsDowngrades`) each lead with a doc comment describing what the test locks in and, importantly, what it does NOT assert. The "what this test locks in" bullet list in `TestApplyResolvesVaultSecretEndToEnd` (lines 43-50) is the kind of thing a reviewer learning the pipeline will actually read.

The materializer-interop comment at lines 130-144 is particularly good: it explains that the test tolerates `***` because the materializer hardening is Issue 6's job, names the exact future change ("Issue 6 switches the materializer to reveal.UnsafeReveal"), and names the exact invariant being asserted now ("the resolver ran: Plain is cleared, Secret is populated"). That's the right level of detail to hand off to the Issue 6 reviewer.

The `withFakeVaultBackend` helper doc at lines 16-24 explains why it registers on `vault.DefaultRegistry` (because `apply.go` consults DefaultRegistry) rather than using a test-local registry. A next developer wondering "why aren't these tests hermetic like the resolver tests?" gets an answer.

### 5. `MergeGlobalOverride` signature change propagation

Signature: `(ws, g, globalConfigDir) (*WorkspaceConfig, error)`. Call sites:
- `apply.go:307` — propagates error with no wrap (correct: the underlying error already has full context including dotted path and `ErrTeamOnlyLocked`).
- `override_test.go:589` (`mustMerge` helper), `:841`, `:873`, `:897` — all handle the second return consistently; the helper short-circuits success-path tests, error-path tests consume the error directly.

The error chain preserves context: `errors.Is(err, vault.ErrTeamOnlyLocked)` works, and the message string carries the specific key name and its dotted path so the user never sees an opaque "merge failed."

### 6. Sub-package rationale

`resolve.go:1-22` — the package doc explains the import cycle (`vault → config` on `VersionToken`, `resolver → config` on `WorkspaceConfig`) and why the sub-package is the idiomatic Go fix. A next developer wondering "why isn't this just a file in `internal/vault/`?" will find their answer immediately.

### 7. Test naming

All resolver and apply-vault tests have names that match their bodies. `TestResolveWorkspaceProviderUnreachable` does test the unreachable path (and sets `AllowMissing: true` specifically to verify AllowMissing does NOT swallow unreachable errors — a subtle contract worth locking in). No test-name-lies.

### 8. Intentional duplication

`isVaultURI` in `resolve.go:579-581` duplicates the prefix check from `internal/config`. Comment at 576-578 names the duplication as intentional ("to keep the resolver in lock-step with the config-layer validator"). This is "divergent twins" with explicit justification — acceptable.

### 9. `deepcopy.go` cross-file symmetry claim

The package doc at `deepcopy.go:10-21` asserts the helpers stay in sync with `override.go`'s `copy*` family. Spot check: `deepCopyClaudeConfig` and `copyClaudeConfigFull` both deep-clone `{Hooks, Settings, Env, Plugins}` and share `{Marketplaces, Content, Enabled}` — identical strategies, differ only in nil-handling because `copyClaudeConfigFull` takes a pointer. Symmetry holds; the comment's warning to future maintainers ("Keep these helpers in sync") is both accurate and load-bearing.

### 10. Bundle lifetime in apply.go

The `BuildBundle` → `defer CloseAll` → `CheckProviderNameCollision` → `ResolveWorkspace` order at `apply.go:257-288` is correct: the team-bundle defer is installed before the personal-bundle build, so a failure during personal-bundle construction still closes the team bundle. The comment at `apply.go:254-256` names the motivation ("providers shut down cleanly even on error paths, R29 no-disk-cache").

---

## Summary

The PR hands a next developer a pipeline they can reason about. Error messages name the key, the provider, and the remediation. The sub-package boundary is documented. Tests lie flat and track their subjects. The one advisory I'd act on before merge (A1, the self-contradicting `walkFilesKeys` comment) is a readability cleanup, not a correctness issue.
