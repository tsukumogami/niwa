# Architect review — Issue 10 (CLI flags + status subcommands)

Commit: `26655fab4938a752d2867390851f5b74f650a022`
Branch: `docs/vault-integration`
Working dir: `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa`

## Summary

The change fits the niwa architecture. New CLI surface (`--audit-secrets`,
`--check-vault`, `--allow-missing-secrets`, `--allow-plaintext-secrets`)
lands entirely in `internal/cli`, flows into existing `workspace.Applier`
fields and existing `vault`/`resolve` packages, and does not introduce
any parallel machinery. Dependency direction is respected
(`cli -> workspace -> config/vault`, never reversed). The offline-by-default
invariant from Issue 7 is preserved by construction: `showDetailView`
calls only `LoadState` and `ComputeStatus`, neither of which builds a
bundle; the opt-in `runCheckVault` is the only new entry point that
constructs a `*vault.Bundle` on the status command.

No blocking findings.

## Critical-check walkthrough

### 1. Default `niwa status` stays offline (Issue 7 invariant)

**Status: preserved.**

`runStatus` (`internal/cli/status.go:59`) branches on `statusAuditSecrets` /
`statusCheckVault` first; the default path calls `showDetailView` or
`showSummaryView`, both of which read state and call
`workspace.ComputeStatus`. `ComputeStatus`
(`internal/workspace/status.go:67`) is documented offline-only and does
not construct a `*vault.Bundle`.

`TestDefaultStatusStaysOffline`
(`internal/cli/status_check_vault_test.go:211`) proves this: a
`countingFactory` is registered on `vault.DefaultRegistry`, a state
file containing a `SourceKindVault` entry is saved, `showDetailView`
runs, and the assertion is `factory.last == nil` — i.e., `Open` was
never invoked, so `Resolve` obviously wasn't either. This is a stronger
check than counting calls, because it verifies the bundle-build stage
never ran.

One caveat worth noting (non-blocking): the test exercises
`showDetailView` directly, not `runStatus`. Since `runStatus`'s default
branch ends at `showDetailView`/`showSummaryView` with no intervening
provider work, the gap is small, but a follow-up `runStatus`-level
check with no flags set would close the loop against future
refactors that add a pre-dispatch hook.

### 2. `--check-vault` does NOT invoke materializers or write state

**Status: holds.**

`runCheckVault` (`internal/cli/status_check_vault.go:32`) does four
things and nothing else:
1. Loads config via `config.Discover` + `config.Load`.
2. Loads state via `workspace.LoadState`.
3. Builds a team bundle via `resolve.BuildBundle(ctx, nil, cfg.Vault, ...)`.
4. Iterates `state.ManagedFiles`, invokes `provider.Resolve` per unique
   vault `SourceID`, compares returned `VersionToken.Token` against the
   recorded token.

It does NOT:
- Call any `Materializer` (no import of the `workspace.Materializer`
  surface; no calls into the apply pipeline).
- Call `workspace.SaveState` (only `LoadState` is invoked; no write path).
- Touch repos (no `github.Client`, no `Cloner`).
- Run guardrails (`guardrail.CheckGitHubPublicRemoteSecrets` is not
  reached).

The bundle is closed on exit (`defer bundle.CloseAll()` at line 69)
including the error path, per R29 no-disk-cache. The per-source cache
in `detectVaultRotations` (lines 108-141) correctly deduplicates
provider RPCs when the same vault source backs multiple managed files.

### 3. `--audit-secrets` does NOT invoke providers

**Status: holds.**

`runAuditSecrets` (`internal/cli/status_audit.go:41`) only calls
`config.Load` + `collectAuditEntries` + `loadShadowsForAudit`. The
classification decision in `classifyMaybeSecret` works off the parsed
`MaybeSecret` value and a `strings.HasPrefix(ms.Plain, "vault://")`
check — no resolution. The `SHADOWED` column comes from
`state.Shadows`, which is populated by the last apply (Issue 8) — the
code does not re-run `DetectShadows`, so it works without a global
config directory being reachable.

Minor note (non-blocking): the exit-nonzero condition at line 75 is
`if hasPlaintext && cfg.Vault != nil` rather than
`cfg.Vault != nil && !cfg.Vault.IsEmpty()`. A user who declares `[vault]`
with no providers would have `cfg.Vault` non-nil but effectively empty.
The practical impact is narrow (they've opted into the vault workflow
syntactically even if no provider is wired yet), and the stricter
behavior is arguably correct. Worth considering for a follow-up but
not a structural problem.

### 4. Flag plumbing to `Applier.AllowMissingSecrets` / `AllowPlaintextSecrets`

**Status: correct.**

`applyCmd.Flags()` registrations (`internal/cli/apply.go:20-24`) bind
to `applyAllowMissingSecrets` and `applyAllowPlaintextSecrets` package
vars. `runApply` copies them onto the Applier at lines 109-110:

```go
applier.AllowMissingSecrets = applyAllowMissingSecrets
applier.AllowPlaintextSecrets = applyAllowPlaintextSecrets
```

The Applier then threads these into:
- `guardrail.CheckGitHubPublicRemoteSecrets(..., a.AllowPlaintextSecrets, ...)`
  (`internal/workspace/apply.go:361`)
- `resolve.ResolveOptions{AllowMissing: a.AllowMissingSecrets}`
  (`internal/workspace/apply.go:382, 396`)

Test coverage:
- `TestApplyCmd_HasAllowMissingSecretsFlag` + `HasAllowPlaintextSecretsFlag`
  pin the flag definitions.
- `TestApplyCmd_AllowFlagsThreadToApplier` proves `ParseFlags` populates
  the package vars.
- `TestApplyVaultAllowMissingSecretsDowngrades`
  (`internal/workspace/apply_vault_test.go:432`) proves the Applier
  field reaches the resolver.

### 5. `?required=false` handling

**Status: covered.**

`TestApplyVaultRequiredFalseDowngradesSilently`
(`internal/workspace/apply_vault_test.go:496`) runs a full
`applier.Create` with `AllowMissingSecrets = false` and a
`vault://ANTHROPIC_KEY?required=false` URI against a backend that does
not know the key. The assertions:
- `Create` succeeds.
- No `warning: vault:` or `--allow-missing-secrets` string appears on
  stderr (captured via `os.Pipe` swap on `os.Stderr`).

This confirms the Ref `Optional` bit flows end-to-end through parse ->
resolve -> merge without being stripped.

### 6. Bootstrap pointer fires on `modeClone` only

**Status: correct and justified.**

`runInit` (`internal/cli/init.go:148`) gates
`emitVaultBootstrapPointer` with `if mode == modeClone`. The inline
comment correctly explains why: `modeScaffold` produces a template
whose `[vault.*]` sections are commented examples, so
`vaultKindsDeclared` on that parsed config returns empty anyway.
Gating on mode is belt-and-braces but makes the intent explicit, and
`TestEmitVaultBootstrapPointer_NoVaultNoOp` pins the helper's no-op
path.

One subtle point: for `modeNamed` (named-but-unregistered), the
scaffolded template is also example-commented, so the mode gate also
protects the case where a user runs `niwa init my-new-project`
(no `--from`, no registry entry) and sees no spurious pointer. Good.

## Architectural notes

### Dependency direction

`internal/cli` imports `internal/config`, `internal/workspace`,
`internal/vault`, `internal/vault/resolve`, `internal/secret`.
None of those packages imports `internal/cli`. No circular or
upward-pointing edges introduced by this change.

### Helper duplication (acknowledged in code)

`sortedSecretKeys` in `status_audit.go:196` duplicates a similar
helper in `internal/workspace`. The inline comment notes the
duplication is deliberate because `cli -> workspace` is the only
allowed direction and the workspace helper isn't exported. This is
the right call for a 9-line helper; exporting `workspace.SortedSecretKeys`
purely for a cli-package caller would be over-coupling. Not a
finding.

### New types are local

`auditEntry`, `vaultRotation`, `rotationSource`, `initMode` are all
package-local to `cli`. They do not leak into `workspace` or `vault`
and do not compete with existing types in those packages.

### Bundle lifetime

`runCheckVault` creates a team-only bundle (no personal overlay passed
to `BuildBundle`). Given that `--check-vault` is about detecting
team-side rotations against the recorded state, ignoring the personal
overlay is the right scope. A future extension could add a
`--check-vault-overlay` or similar, but that's new capability, not a
structural gap.

## Non-blocking suggestions

1. **`cfg.Vault.IsEmpty()` in audit exit check** (`status_audit.go:75`):
   consider changing `cfg.Vault != nil` to
   `cfg.Vault != nil && !cfg.Vault.IsEmpty()` so an empty `[vault]`
   block with no providers doesn't gate the exit code. Low-impact.
2. **Offline invariant test at `runStatus` level**: the existing test
   exercises `showDetailView` directly; an analogous test that calls
   `runStatus` with both flags unset would catch any future refactor
   that introduces pre-dispatch bundle work. Cheap to add.

## Blocking findings

None.
