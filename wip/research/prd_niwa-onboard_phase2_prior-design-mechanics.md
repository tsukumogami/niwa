# Prior design mechanics: synthesis for the `niwa onboard` wizard PRD

Source documents (all four are Planned/In Progress design/PRD artifacts from two superseded
draft PRs whose mechanics now fold into a new `niwa onboard` wizard):

- `mined_DESIGN-provider-auth-provision.md` (Planned)
- `mined_PRD-provider-auth-provision.md` (In Progress)
- `mined_DESIGN-vault-doctor.md` (Planned)
- `mined_PRD-vault-doctor.md` (In Progress)

## 1. Mint + store mechanics (provider-auth-provision)

**REST surface** (all via stdlib `net/http`, never shelled out, never `curl`/`jq`/`base64`):

- `GET /v1/auth/universal-auth/identities/{id}` -> `identityUniversalAuth.clientId` (read the
  existing identity's client_id; provision never creates an identity).
- `POST /v1/auth/universal-auth/identities/{id}/client-secrets` -> response fields
  `clientSecret` and `clientSecretData.id` (mints a fresh secret on that identity, bounded by
  a TTL; the returned id enables later revocation).
- Verify step reuses two existing code paths rather than adding new REST: `Authenticate`
  against `/v1/auth/universal-auth/login` with the minted pair (auth half), then the
  unexported `runInfisicalExport` called with the minted JWT against the target environment
  (read half) -- a successful export is the read proof. No new probe endpoint.

**Storage-target abstraction**: `--store=file|vault`, default `file`.

- `file` (default): writes `~/.config/niwa/provider-auth.toml` at mode `0600`. Chosen as
  default because it has the fewest preconditions -- it always succeeds once minting
  succeeds, which is what makes the agent-driven path reliable.
- `vault` (opt-in): writes the conventional `version = "1"` credential body to the provider's
  vault path via the existing CLI-subprocess pattern (`infisical secrets set`), not REST --
  because a vault write needs the CLI session, unlike the mint calls.
- A single run writes exactly one target, never both.

**CLI/UX decisions worth inheriting**:

- Command shape: `provider-auth provision` (cobra parent `provider-auth`, subcommand
  `provision`), flags `--identity`, `--env`, `--store`, `--json`.
- Exit codes: 0 success; 3 authentication failure (bad/absent session, wrong org, mint
  rejected); 4 target environment not readable by minted credential; 5 storage-write failure.
  Carried by a typed exit-code error mapped in `internal/cli/root.go`'s `Execute()`, mirroring
  the existing `sessionattach.ExitCodeError` / `workspace.InitConflictError` pattern.
- `--json` success payload: `identity_id`, `client_id`, `client_secret_id`, `store_target`,
  `env` -- never the secret.
- Session/bearer token source: read from the provider's environment variable or CLI session
  file, the same way the provider CLI itself reads it. **Never** a `--token` flag (flag values
  land on argv and in shell history/`ps`/procfs). No interactive login is performed by the
  command itself; a session scoped to the wrong org surfaces as the identity read's 403,
  mapped to the authentication-failure exit code.
- Rotation: re-running mints a new secret and overwrites the stored one. Revoke-on-rotate:
  when the prior `client_secret`'s id is recoverable from the overwritten target, the run
  revokes it; when not recoverable, the run surfaces the new id and documents that the old
  secret remains live until its TTL lapses.
- Generic vocabulary is a hard requirement (R10): flags/messages/defaults must never bake in
  one provider's product name or one workspace's org/project identifiers. Consumer-specific
  identifiers belong only in that workspace's private overlay and downstream onboarding skill
  -- this is explicitly the shape the onboarding wizard is expected to take.

## 2. Secret hygiene rules (carry into the wizard PRD as requirements, near-verbatim)

These are stated as non-negotiable in both provision documents and must propagate to any
onboarding step that touches credentials:

- **Redactor-before-first-use.** A `secret.Redactor` MUST be attached to the context
  (`secret.WithRedactor`) *before* any mint/verify/store call, and every secret value
  (session bearer, minted client_secret) MUST be registered via `secret.Value` /
  `secret.RegisterValue` the instant it is received. This is a precondition, not an optional
  decoration: registration and response-body scrubbing are no-ops on a redactor-less context.
- **Never on argv.** No secret may ever be placed on a subprocess or process argv. REST calls
  carry secrets only in headers (`Authorization`); the `infisical secrets set` storage path
  feeds the credential body over **stdin or a `0600` temp file**, never as a command-line
  argument.
- **Atomic 0600 file writes.** Any credential file write (provider-auth.toml, or any similar
  credential file the wizard introduces) MUST create the temp file at mode `0600` from the
  start via `os.OpenFile` (not write-then-chmod), in the *same directory* as the target (so
  the rename is same-filesystem and atomic), then rename over the target. No reader may ever
  observe a world-readable or partial intermediate.
- **Response-body scrubbing.** Mint/verify/login response bodies must be scrubbed by the
  registered redactor before logging or error wrapping. Note the one known gap:
  `scrubResponseBody`'s `clientSecret` string-replace parameter is currently dead code (not
  wired) -- the ctx redactor is the real and only mechanism, not a second layer. Any new code
  must not assume the string-replace fallback catches what the redactor misses.
- **All errors via `secret.Errorf`**, so a secret value can never surface through an error
  message or wrapped error chain.
- **No secret in any output surface**: stdout, stderr, logs, `--json`, or human-readable
  messages carry only non-secret identifiers (identity id, client_id, client_secret_id, store
  target, pair identity, keyedPath, status vocabulary) -- never a fetched or minted secret
  value, at any exit path, on any code path.

## 3. Verify mechanics + credential-sync read topology (vault-doctor)

- **One credential-sync provider, opened once.** Exactly as apply's Step 0.4 does:
  `pickCredentialSyncSpec(global)` then `openCredentialSyncProvider`
  (`internal/workspace/credentialsync.go:31-71`). There is one provider in production, not one
  per pair; every pair's credential body is fetched by calling `Resolve` on that single
  provider instance -- never a bespoke probe, never a pair's own `VaultProviderConfig` (that
  read would be circular: the body is what would authenticate it).
  - When `pickCredentialSyncSpec` returns nil (no anonymous `[global.vault.provider]` in the
    personal overlay -- a legitimate state for single-org users), there is nothing to Resolve
    against: report a single informational `no-credential-sync-configured` finding and exit 0,
    explicitly stating no pairs were verified. Never render an empty all-clear; never report
    this as vault-unreachable.
- **Three VaultRegistry pair sources**, merged and deduplicated, matching exactly what apply's
  `injectProviderTokens` feeds: the workspace-overlay Vault (`apply.go:915`), the team
  workspace-config Vault (`apply.go:1035`), and the personal global-overlay Vault
  (`apply.go:1039`). Enumerating only one registry silently drops pairs.
- **Self-exclusion rule.** Skip the credential-sync provider's own `(kind, project)` pair,
  exactly as `lookupVault`'s self-guard does (`credentialpool.go:428`, via the
  `SelfKind`/`SelfProject` fields, `credentialpool.go:253-268`). apply never validates that
  pair against the vault -- it authenticates through the caller's CLI session (the
  chicken-and-egg case). The doctor reports it as OK with detail "authenticates via CLI
  session," never as `missing-entry`.
- **`parseProviderAuthBody` contract validator.** Stays unexported, lives in-package in
  `internal/workspace/credentialpool.go`. The doctor calls it in-package through a new
  exported entry point, `CheckProviderAuth`, rather than wrapping or duplicating it -- this is
  what makes "doctor and apply never disagree" true by construction, not by test coverage.
- **What the doctor checks** (the checklist of failure modes):
  - Per-pair, via `vault.Ref{Path: CredentialSyncPathPrefix + kind, Key: "p-" + project}` and
    `Resolve`: `ErrKeyNotFound` -> `missing-entry`; `ErrProviderUnreachable` -> abort
    classification, mark the whole run vault-unreachable; success -> run
    `parseProviderAuthBody` and map to `OK` / `malformed-body` / `missing-field` /
    `unsupported-version`. One pair's failure never stops the loop (all pairs are always
    evaluated in one run).
  - File layer via `LoadProviderAuth(configDir)`: `bad-mode` (not 0600), `malformed-file`
    (present but unparseable, or an entry missing a required field -- Detail is a **fixed
    categorical string**, never `LoadProviderAuth`'s raw parse error, since that path is
    currently unsanitized), `absent` (no file -- informational, not a failure, since the file
    layer is optional when the vault layer resolves).
  - `MatchProviderAuth` reports which pairs have a local file entry, for a present valid file.
- **Exit codes 0/1/2**: 0 = every pair and the file layer valid; 1 = vault reached but at
  least one pair or file-layer check failed; 2 = vault unreachable at all (or provider tool
  missing/broken). Carried by a typed error and a branch in `root.go`'s `Execute()`, same
  pattern as provision's exit codes and the existing `ExitCodeError` branches.
- **Strictly read-only, no auth exchange.** The doctor never calls `infisical.Authenticate`
  and performs no machine-identity HTTP exchange of any kind -- it validates body *shape*
  only, never trades credentials for a JWT. This is a materially narrower operation than
  provision's verify step (which does authenticate). A future doctor enhancement that added a
  real auth probe would need its own security review and would need to wire the same
  redactor/RegisterValue protections provision uses -- it does not get them for free today.

## 4. What the two designs settled (do not re-litigate) vs. what they left open (the wizard's PRD must decide)

### Settled -- treat as fixed constraints, not open questions

- niwa's "reads only, does not auto-mint" stance holds everywhere **except** the named,
  bounded provision carve-out: mint-on-**existing**-identity plus write-own-config only.
  Creating a new identity is explicitly out of scope for provision (provider-plan-gated,
  deliberately excluded) -- and the design records that the machine-identity PRD's non-goal
  wording should be annotated to reference this carve-out.
- The provider-auth.toml schema, its `0600` mode, and the atomic temp-file-then-rename write
  pattern are fixed. Any wizard step that writes this file must use `WriteProviderAuth`'s
  approach, not a new one.
- The full secret-hygiene invariant set in section 2 is fixed and non-negotiable; the wizard
  must inherit it verbatim for any new credential-touching step, not re-derive it.
- The doctor's read topology (single credential-sync provider opened once, three-registry
  enumeration, self-exclusion, `parseProviderAuthBody` reuse) is fixed specifically *because*
  it is what makes the "doctor and apply never disagree" guarantee true by construction. The
  wizard must not introduce a parallel/lighter existence check for the same contract.
  Vault-doctor design explicitly rejected a lighter existence probe as an option, for this
  reason.
  - Note: the doctor design commits to **shape validation only** -- it deliberately never
    calls `Authenticate` and treats adding a real auth probe as a distinct, unbuilt future
    change requiring its own security review. The wizard must not assume `niwa vault check`
    already verifies live authentication; if the wizard needs that, it is provision's verify
    step or a genuinely new capability, not something to retrofit onto the doctor silently.
- `niwa vault check` (the doctor) and its exit-code convention (0/1/2) are fixed as the
  vault-namespace's first command, explicitly setting up `niwa vault init` as its named
  second occupant.
- Verify-before-store ordering for provision is structural (store is unreachable on a verify
  error), not merely a suggested sequence.
- Generic-vocabulary requirement (provision R10): the wizard, like provision, must keep any
  provider-/workspace-specific identifiers out of the generic command surface and confined to
  the private overlay / downstream onboarding skill layer.

### Left open -- the wizard's PRD must now decide

- **Identity creation.** Provision explicitly excludes creating a new machine identity
  ("distinct, provider-plan-gated, deliberately excluded"). An onboarding wizard for a
  brand-new user/workspace may need to create the identity in the first place, not just mint
  a secret on one that already exists. Neither doc decides this -- the wizard's PRD must state
  whether identity creation is in scope, and if so, how it reconciles with the "reads only"
  stance's now-doubly-bounded carve-out.
- **Config/vault scaffolding (`niwa vault init`).** The vault-doctor PRD explicitly places
  "config authoring or scaffolding... a `niwa vault init` that writes the
  `[vault.provider]`/`[env.secrets]` contract into the workspace config" **out of scope**,
  calling it "a separate follow-on feature" niwa doesn't have today. This is very likely
  exactly what the onboarding wizard needs to do (or explicitly hand off to a real, still
  unbuilt `niwa vault init`). The wizard's PRD must decide whether it implements this
  scaffolding itself, depends on a to-be-built `niwa vault init`, or continues to punt it.
- **Orchestration ownership.** Both docs explicitly push "workspace-specific onboarding
  orchestration," "interactive provider and GitHub logins," and "the Path A / Path B choice"
  to "a downstream onboarding skill that calls this command" -- i.e., to something like the
  wizard being scoped now. The two prior docs do not specify that orchestration's sequencing,
  error recovery, or UX; the wizard's PRD is the first artifact that must actually own it.
- **Sequencing of provision + doctor.** Neither doc states whether/how an onboarding flow
  should chain `provider-auth provision` and `vault check` together (e.g., provision to
  acquire the credential, then doctor to confirm the full contract including other
  teammates' pairs, before letting the first `apply` run). The wizard's PRD must decide this
  ordering and what a wizard does on partial failure of either step.
- **Command surface for the wizard itself.** Provision lives under `provider-auth provision`;
  doctor lives under a new `vault` parent (`vault check`, with `vault init` reserved as
  follow-on). The wizard is a third surface area (`niwa onboard`, presumably) -- neither doc
  anticipates it, so the wizard's PRD must decide its own namespace and how/whether it invokes
  the other two commands as subprocesses, as shared internal packages
  (`internal/provision`, `internal/workspace.CheckProviderAuth`), or some other mechanism.
- **Precondition on human provider login.** Both provision and the doctor's live fetch assume
  the human has *already* authenticated a provider CLI session before the command runs, and
  both explicitly decline to manage that interactive login themselves. If the wizard is the
  first artifact meant to walk a brand-new user through onboarding end-to-end, it is the first
  one that must actually own sequencing that human login step -- neither prior doc specifies
  how.

## 5. Contradictions or overlaps the PRD must reconcile

- **Scope overlap on "who scaffolds config."** Vault-doctor's PRD calls scaffolding
  (`vault init`) out-of-scope future work; provision's design frames the *first consumer's*
  onboarding sequencing as belonging to "a downstream onboarding skill." Taken together they
  imply a gap no existing artifact fills: something must both scaffold the vault config *and*
  orchestrate the human+command sequence. The wizard's PRD should explicitly state whether it
  is that "something," partially or fully, rather than leave a third unowned gap.
- **Different verification depths, same-sounding word.** Provision's "verify" step is a real
  two-hop credential exchange (mint -> authenticate -> read-probe). The doctor's "check" is
  shape-validation only and explicitly never authenticates. If the wizard's PRD uses a generic
  word like "verify" for a post-onboarding health check, it must disambiguate which of these
  two verification depths it means, since they have different security review requirements and
  different guarantees (the doctor's clean report is *not* proof credentials will authenticate,
  only that the stored shape is well-formed).
- **Namespace tension.** Provision sits under `provider-auth` (an existing parent); the doctor
  deliberately creates a new `vault` parent rather than extending `status` because "the object
  is different." If the wizard needs to invoke both, its PRD should address whether the wizard
  itself becomes a third top-level namespace (`niwa onboard`) or is folded under one of the
  existing two -- and should apply the doctor design's own reasoning test ("is this the same
  kind of object?") rather than default to consistency with either one by habit.
- **Exit-code philosophies are compatible but not unified.** Provision uses 0/3/4/5 (fixed
  per-stage codes); the doctor uses 0/1/2 (fixed per-outcome-class codes). Both route through
  the same `root.go` `Execute()` typed-error branch mechanism, so they're structurally
  compatible, but the wizard's PRD must decide its own exit-code vocabulary rather than assume
  either scheme transfers unchanged, since the wizard's terminal outcomes (a multi-step
  sequence) don't map 1:1 onto either single command's outcome set.
- **Revocation/rotation asymmetry.** Provision supports rotate-and-revoke on re-run; the
  doctor has no analogous concept (it's read-only and stateless per run). If the wizard's flow
  re-runs provision as part of retry/recovery, the PRD should state whether repeated wizard
  runs are expected to accumulate revocations the same way bare `provision` re-runs do, or
  whether the wizard should suppress/alter that behavior in a guided-onboarding context (e.g.,
  a user re-running the wizard after a typo shouldn't necessarily revoke a secret that was
  never actually used).

## Constraints the PRD must state as requirements

1. Any credential-minting step the wizard performs MUST reuse provision's REST surface (GET
   identity, POST client-secrets, login-based verify via `runInfisicalExport`) rather than
   inventing new endpoints, and MUST NOT create a new identity unless the PRD explicitly
   opens that as new scope with its own plan-gating analysis.
2. Any credential-touching code path MUST attach a `secret.Redactor` to context before first
   use, register every secret value (`secret.Value`) the instant it's obtained, use
   `secret.Errorf` for all errors, never place a secret on any argv, and use stdin/temp-file
   for any CLI-subprocess secret writes -- verbatim inheritance of section 2 above, not a
   re-derivation.
3. Any credential file write (provider-auth.toml or equivalent) MUST use the `0600`
   create-in-target-dir-then-rename pattern; no write-then-chmod.
4. If the wizard invokes or wraps `niwa vault check` for a readiness check, it MUST treat that
   check as shape-validation only (no auth guarantee) and MUST NOT represent a clean doctor
   report as proof that authentication will succeed -- that guarantee only comes from
   provision's verify step (or a new, explicitly reviewed auth probe).
5. The wizard PRD MUST explicitly decide and state: (a) whether it performs config/vault
   scaffolding itself or depends on a separate `niwa vault init`; (b) whether/how it
   orchestrates the human's interactive provider and GitHub logins, since both prior artifacts
   explicitly declined to own that; (c) its own command namespace and its relationship (if any)
   to `provider-auth provision` and `vault check`; (d) its own exit-code vocabulary, justified
   independently rather than inherited wholesale from either prior scheme; (e) whether
   identity creation is in scope, given both prior docs deliberately excluded it.
6. Generic-vocabulary requirement (no provider product names, no workspace/org/project
   identifiers baked into flags, defaults, or messages) carries forward unchanged from
   provision's R10 to any new wizard-level command surface.
