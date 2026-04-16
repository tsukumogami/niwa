# Issue 10 maintainer review — CLI flags, status subcommands, bootstrap pointer

Commit: `26655fab4938a752d2867390851f5b74f650a022`
Branch: `docs/vault-integration`
Working dir: `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa`

Scope reviewed:

- `internal/cli/apply.go` + `apply_test.go`
- `internal/cli/init.go` + `init_test.go`
- `internal/cli/status.go`
- `internal/cli/status_audit.go` + `status_audit_test.go`
- `internal/cli/status_check_vault.go` + `status_check_vault_test.go`
- `internal/workspace/apply_vault_test.go`

## Blocking

### 1. Divergent "vault declared" check between audit and its siblings

`internal/cli/status_audit.go:75` gates the plaintext-failure exit on
`cfg.Vault != nil`:

```go
if hasPlaintext && cfg.Vault != nil {
    return fmt.Errorf("plaintext values present in *.secrets tables while a vault is configured")
}
```

The two sibling code paths that answer the same question ("does this workspace
have a vault configured?") use `IsEmpty()`:

- `internal/cli/status_check_vault.go:54` — `if cfg.Vault == nil || cfg.Vault.IsEmpty()`
- `internal/cli/init.go:211` — `if cfg == nil || cfg.Vault == nil || cfg.Vault.IsEmpty()`

`VaultRegistry.IsEmpty()` (in `internal/config/vault.go:135`) returns true when
`Provider == nil && len(Providers) == 0 && len(TeamOnly) == 0`. A workspace that
parses a `[vault]` header with only `team_only = [...]` or with no declared
providers yields `cfg.Vault != nil` but `IsEmpty() == true`.

The next developer's misread: they will assume the three paths agree on what
"has a vault" means, because the helper exists and the other two paths use it.
The practical effect is that `niwa status --audit-secrets` will fail a workspace
that `emitVaultBootstrapPointer` (no note printed) and `niwa status --check-vault`
("no vault providers declared; nothing to check") both treat as having no vault.
The user will see "plaintext values present ... while a vault is configured"
when nothing of the sort is true.

Fix: replace `cfg.Vault != nil` with `!cfg.Vault.IsEmpty()` at
`status_audit.go:75`. The PRD AC (§R13, §acceptance-criteria line 1033) says
"plaintext values AND a vault is configured"; `IsEmpty()` is the
project-standard predicate for "vault configured."

**Blocking** — wrong error message for a workspace that declares an empty or
team-only-only `[vault]` block.

## Non-blocking

### 2. `--check-vault` rotation output has no call-to-action

`printVaultRotations` at `status_check_vault.go:210` emits:

```
vault-rotated /path/to/file
  source team/API_KEY: old-tok... -> new-tok...
    provenance: ...
```

The user sees *what* rotated but no pointer to `niwa apply`. Compare with
`emitVaultBootstrapPointer` which closes with "Then run `niwa apply`.", giving
the next step explicitly.

Issue 10's review checklist asks the rotated-file output to be "actionable".
Experienced users will guess the next command, but a maintainer or first-time
user staring at "vault-rotated X" with no verb is one extra click away from
action. Consider appending a single footer line such as
`run \`niwa apply\` to materialize these rotations` when `len(rotations) > 0`.

Non-blocking — the information is sufficient; the guidance is missing.

### 3. `--check-vault` returns nil even when rotations are detected

`runCheckVault` returns nil whether rotations exist or not. Per-source
re-resolution errors land in `rotationSource.Err` and are printed, not
returned. PRD AC line 1042 says "reports which files would change", which is
consistent with a zero exit. But a user scripting
`if ! niwa status --check-vault; then alert; fi` gets no signal. If that
shell-integration use case is on the table, a `--fail-on-rotation` (or
non-zero-on-detection) decision is worth recording before someone else files a
bug.

Non-blocking — PRD is silent on exit code for the detection case; current
behavior is a defensible choice, but the silence is worth a brief comment on
the function pointing the next reader at the PRD line.

### 4. Pre-existing `driftLabel` dead branch visible in `status.go`

`status.go:159-162`:

```go
driftLabel := "drifted"
if status.DriftCount == 0 {
    driftLabel = "drifted"
}
```

Both branches assign the same string. This predates Issue 10 (already flagged
in `wip/research/implement-vault-integration-issue_8_maintainer.md` and
`issue_7_maintainer.md`). Not a regression introduced here, and `status.go` is
only lightly edited by this commit, but it is visible to reviewers looking at
the file. Out of scope for this PR; worth a follow-up ticket to either make
the zero-case label "clean" or drop the branch.

## Summary of positive findings

- **Flag help text (R `--allow-missing-secrets`, `--allow-plaintext-secrets`)**:
  both flags say "one-shot" in the help string (`apply.go:20-24`). The semantics
  reach the `Applier` struct via direct field assignment at
  `apply.go:108-110`; `TestApplyCmd_AllowFlagsThreadToApplier` pins the
  plumbing so a future refactor that drops the wiring will fail tests. Clear.

- **Audit classification labels** are centralized as constants
  (`classVaultRef`, `classPlaintext`, `classEmpty`, `classResolved` at
  `status_audit.go:18-23`) and referenced by name everywhere, including tests.
  No magic strings compared in multiple places. Clear.

- **Audit table alignment** computes `keyWidth`, `classWidth`, `tableWidth`
  once from the row set before rendering (`status_audit.go:214-229`). Columns
  stay aligned even when one key name is much longer than the rest. Clear.

- **Bootstrap pointer — Infisical specifically**: `bootstrapCommandFor("infisical")`
  returns `` `infisical login` `` (`init.go:259-266`), matching PRD §1061.
  `TestEmitVaultBootstrapPointer_Infisical` locks this in. The
  `if mode == modeClone` guard at `init.go:148` ensures scaffolded workspaces
  (commented examples only) never see the note. Clear.

- **Test names** all describe the scenario under assertion:
  `TestApplyVaultRequiredFalseDowngradesSilently`,
  `TestRunAuditSecrets_ExitsNonZeroWhenPlaintextAndVaultConfigured`,
  `TestDetectVaultRotations_RotatedValueReportsChange`,
  `TestDefaultStatusStaysOffline`. No misleading names. Clear.

- **`TestDefaultStatusStaysOffline`** is a good invariant-locking test: the
  "default status is offline" contract would otherwise be invisible to the
  next developer touching `showDetailView`. Keep.

- **`refFromSourceID`** handles the anonymous-provider shape `"/KEY"` correctly
  (empty `ProviderName`), with `TestRefFromSourceID` pinning the table of
  shapes. A malformed `"no-slash"` source ID produces `ok=false` rather than a
  panic; `TestDetectVaultRotations_MalformedSourceIDReportsError` asserts the
  error surfaces through the report rather than aborting the command. Clear.

- **`collectAuditEntries`** walks the six `*.secrets` locations listed in its
  doc comment, sorts repo names, and deliberately skips `*.vars`; the
  `TestCollectAuditEntries_WalksAllSecretsTables` fixture explicitly asserts
  `PUBLIC_VAR@env.vars` is absent. Clear.

- **`TestApplyEmitsShadowStderr`** goes the extra mile to redirect the real
  `os.Stderr` through a pipe because `runPipeline` writes to `os.Stderr`
  directly. That is the right choice — anything less would let an accidental
  refactor into a swallowed-writer silently pass.
