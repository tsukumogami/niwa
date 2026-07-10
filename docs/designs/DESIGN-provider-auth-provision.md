---
status: Planned
upstream: docs/prds/PRD-provider-auth-provision.md
problem: |
  niwa reads a machine-identity credential to resolve a workspace's vault secrets but
  offers no way to obtain one. Onboarding a machine-identity workspace means hand-running
  fragile, non-portable REST and base64 shell to mint and store the credential, with no
  secret-hygiene guarantee. The read path is built; the acquisition path is not.
decision: |
  Add a `niwa provider-auth provision` command as a bounded carve-out to niwa's
  reads-only credential stance: it mints a client_secret on an already-existing machine
  identity, verifies it before storing, and writes it to one caller-selected target
  (the provider-auth.toml file by default, or a vault entry). Identity minting lives
  behind a provider-agnostic Provisioner capability implemented by the existing Infisical
  backend; orchestration lives in a new internal/provision package; the command layer
  stays provider-neutral.
rationale: |
  Minting on an existing identity is not provider-plan-gated (creating one is), so the
  carve-out is both useful and bounded -- niwa still never creates identities and never
  does general vault writes. Reusing the existing net/http auth client, secret redactor,
  and provider-auth schema keeps the net-new surface to the identity-management REST
  calls and a TOML writer. The file default has the fewest preconditions, so the
  agent-driven path always succeeds once creds are minted.
---

## Status

Planned

Technical design for the `niwa provider-auth provision` command, downstream of the
Accepted `docs/prds/PRD-provider-auth-provision.md`. Resolves the three framing questions
the PRD deferred (stance reconciliation, default storage target, REST surface placement)
plus command/package structure and secret-hygiene mechanics.

## Context and Problem Statement

niwa resolves `vault://` secrets for a machine-identity workspace by authenticating with
a `client_id` / `client_secret` pair. Today that pair is read from `provider-auth.toml`
(via `LoadProviderAuth` in `internal/workspace/providerauth.go`, which enforces mode
`0o600` and parses a `[[providers]]` array of `kind` + a backend config map) or from a
vault-sourced entry merged in by the credential pool. The credential is *consumed* by
`internal/vault/infisical/auth.go`, whose `Authenticate` POSTs `clientId` / `clientSecret`
to `/v1/auth/universal-auth/login` over stdlib `net/http`, registering the secret with a
`secret.Redactor` and scrubbing it from response bodies.

What niwa has no code for is *obtaining* that credential. A developer or agent onboarding
such a workspace does it by hand: read an identity's `client_id`, mint a fresh
`client_secret` through the provider's REST API, confirm the credential authenticates and
can read the target environment, then write it into the exact schema niwa reads. That
sequence is currently transcribed into onboarding docs as `curl` / `jq` / `base64` shell
-- non-portable (the org-claim base64 decode breaks under BSD `base64` on macOS), unsafe
(the secret can land in shell history), and duplicated per workspace. The technical
problem is to move that mint / verify / store sequence into niwa as one portable,
secret-safe, non-interactive command without broadening niwa's credential-write surface
beyond what onboarding actually needs.

## Decision Drivers

- **Bounded write surface.** niwa's machine-identity PRD records a "reads only, does not
  auto-mint" stance. Any write path must be narrow and defensible, not an open door to
  "niwa manages identities."
- **Secret hygiene is non-negotiable (PRD R12).** The minted secret must never reach
  stdout, stderr, logs, or errors. niwa already has the redactor to enforce this; the
  design must route the new secret through it.
- **Portability (PRD R11).** No dependence on `curl` / `jq` / `base64`; identical
  behavior on Linux and macOS. Favors reusing the existing `net/http` client over
  shelling out for the mint path.
- **Generic command surface (PRD R10).** Provider-specific REST vocabulary must stay
  behind the vault abstraction; the command's flags and messages stay neutral.
- **Fewest preconditions for the automatable path.** The agent-driven path (PRD R5)
  should succeed with only what the mint step already needs, not an additional writable
  session.
- **Reuse over rebuild.** The auth client, redactor, provider-auth schema, and vault
  registry exist. Net-new should be limited to the identity-management REST calls, a
  TOML writer, and thin orchestration.

## Considered Options

### Decision 1 -- Reconcile with the "reads only, does not auto-mint" stance

- **Option A (chosen): bounded carve-out.** Provision mints a `client_secret` on an
  identity that already exists and writes only niwa's own credential config
  (`provider-auth.toml` or the conventional vault path). niwa still never creates
  identities and never performs general vault writes. The stance holds for everything
  except this named, narrow operation.
- **Option B: reverse the stance.** Declare niwa a credential-lifecycle manager and drop
  the reads-only framing. Rejected: it invites scope creep (create/delete identities,
  arbitrary vault writes) the feature does not need and the plan gate would block anyway
  (creating identities is provider-plan-gated).
- **Option C: keep all writes out of niwa; leave the flow in the onboarding skill.**
  Rejected: this is the status quo the PRD exists to remove -- the fragile shell stays,
  just relocated into a skill, and the portability/hygiene guarantees never become code.

The carve-out wins because minting on an existing identity is *not* plan-gated while
creating one *is*: the boundary that keeps the feature useful is the same boundary that
keeps it bounded. This design records that `PRD-machine-identity-vault-sync.md`'s non-goal
wording should be annotated to reference this carve-out -- a doc-consistency task for
`/plan`, not a silent contradiction.

### Decision 2 -- Default storage target

- **Option A (chosen): default to the `provider-auth.toml` file; `--store=vault` opt-in.**
  The file write needs only the freshly minted credential and the niwa config dir. It
  always succeeds once minting succeeds, which suits the agent-driven default path.
- **Option B: default to the vault entry.** More secure (no secret-bearing file on disk)
  but requires a *separate* writable, personal-scoped vault session (the two-login flow).
  The generic command cannot assume that session exists, so a vault default would fail
  for the common onboarding case. Kept as the explicit opt-in for users who want the
  no-local-file posture.
- **Option C: require an explicit `--store` every time.** No surprise, but adds friction
  to the common path for no safety gain over a documented default plus a `0600` file.

Selection rule: `--store=file|vault`, default `file`. The file target writes
`~/.config/niwa/provider-auth.toml` at mode `0600`; the vault target writes the
conventional `version = "1"` credential body to the provider's vault path.

### Decision 3 -- Where the identity-management REST lives

- **Option A (chosen): a provider-agnostic `Provisioner` capability on the vault
  abstraction, implemented by the Infisical backend.** Add the identity-read and
  client-secret-mint calls to `internal/vault/infisical` (a new `provision.go`) alongside
  the existing `auth.go`, exposed through an optional interface the vault registry can
  type-assert. The command and orchestration talk to the interface, never to Infisical
  URLs.
- **Option B: put the REST calls in the command layer.** Rejected: leaks provider
  vocabulary into `internal/cli`, violating R10, and duplicates the HTTP/redaction plumbing
  `auth.go` already has.
- **Option C: shell out to the provider CLI for minting too.** Rejected on portability:
  the mint/verify path would inherit the CLI's quirks, and niwa already talks to
  universal-auth over `net/http`, so REST is the consistent, portable choice. (The vault
  *store* path is the one place shelling out to `infisical secrets set` stays acceptable,
  because vault reads already shell out and writing a vault secret needs the CLI session.)

### Decision 4 -- Orchestration location and command shape

- **Option A (chosen): a new `internal/provision` package orchestrates mint -> verify ->
  store; a thin `provider-auth provision` cobra command in `internal/cli` calls it.**
  Keeps the sequencing, exit-code mapping, and `--json` shaping in one cohesive place
  that depends on the vault `Provisioner` (mint/verify) and a new provider-auth writer
  (store).
- **Option B: fold orchestration into `internal/workspace`.** Rejected: `workspace` owns
  apply/converge over a materialized workspace; provisioning a credential is a standalone
  operation with no workspace state, so co-locating blurs the package's responsibility.
- **Option C: orchestrate inline in the cobra command.** Rejected: puts sequencing and
  secret handling in the CLI layer, which is harder to test and easier to leak from.

Because minting is *identity-level*, not project-level, `internal/provision` cannot obtain
the provisioner through `vault.Registry.Build` (which needs a full `ProviderSpec` whose
`project` is required by `infisical.Factory.Open`). Instead `internal/provision` imports
`internal/vault/infisical` directly to construct the provisioner, while `internal/cli`
depends only on `internal/provision` -- so the command layer stays provider-neutral (R10)
without inventing a project-scoped `Provider` just to type-assert a capability.

### Decision 5 -- Session-token source and the verify read-probe

- **Option A (chosen): source the provider bearer from the environment/CLI session,
  never a flag; verify by reusing the export path with the minted JWT.** The raw provider
  session token (the bearer the identity-read and mint calls need) is read from the
  provider's env var / CLI session file the same way the provider CLI itself reads it --
  not accepted as a `--token` flag, because a flag value lands on argv and in shell
  history. The target-environment read-probe reuses the existing (unexported)
  `runInfisicalExport` with the minted credential's JWT (`--token`), reachable from a
  sibling `provision.go` in package `infisical`; a successful export of the target env is
  the read proof.
- **Option B: accept `--token` / `--session-token` as a flag.** Rejected on hygiene: the
  bearer is a secret and a flag value is world-visible via `ps` / procfs, contradicting
  niwa's own "no secret on argv" invariant (`internal/vault/infisical/subprocess.go`,
  `auth.go`).
- **Option C: re-authenticate a fresh JWT and call a raw secrets-list REST endpoint for
  the probe.** Rejected: duplicates logic `runInfisicalExport` already has and widens the
  REST surface niwa maintains.

## Decision Outcome

`niwa provider-auth provision` is a new cobra parent (`provider-auth`) and subcommand
(`provision`) in `internal/cli`. It calls `internal/provision`, which runs a strict
mint -> verify -> store sequence:

1. **Mint** through a provider-agnostic `Provisioner` implemented by
   `internal/vault/infisical`: read the identity's `client_id`
   (`GET /v1/auth/universal-auth/identities/{id}` -> `identityUniversalAuth.clientId`) and
   mint a fresh `client_secret`
   (`POST /v1/auth/universal-auth/identities/{id}/client-secrets` ->
   `clientSecret`, `clientSecretData.id`), bounding the mint with a TTL.
2. **Verify** by reusing the existing auth path: authenticate the minted pair against
   `/v1/auth/universal-auth/login` and confirm it can read the named target environment.
   On failure, stop -- nothing is stored.
3. **Store** to exactly one caller-selected target: the `provider-auth.toml` writer
   (default, mode `0600`, net-new) or the vault entry (`--store=vault`, via the existing
   CLI subprocess pattern).

The minted secret is registered with a `secret.Redactor` the moment it is received and
carried only in memory through verify and store; all errors use `secret.Errorf` so the
value can never surface. Wrong-organization sessions fail the identity read or mint with
the provider's 403, surfaced as the authentication-failure exit code. Re-running mints a
new secret and overwrites the stored credential (rotation). This bounded carve-out is the
whole of niwa's new write surface: mint-on-existing-identity plus write-own-config.

## Solution Architecture

### Components

| Component | Location | New? | Responsibility |
|-----------|----------|------|----------------|
| `provider-auth` parent + `provision` subcommand | `internal/cli/provider_auth.go` | New | Flag parsing (`--identity`, `--env`, `--store`, `--json`), exit-code mapping, redacted output |
| Provision orchestrator | `internal/provision/provision.go` | New | Creates the redactor and attaches it to ctx first; mint -> verify -> store sequencing; typed exit-code errors |
| `Provisioner` capability | `internal/vault` (interface) + `internal/vault/infisical/provision.go` (impl) | New | Read identity `client_id`; mint `client_secret` with TTL; return non-secret metadata + a `secret.Value` secret |
| Credential verifier | `internal/vault/infisical/auth.go` `Authenticate` (auth half) + `runInfisicalExport` with the minted JWT (read half) | Reuse | Confirm minted creds authenticate and can export the target env |
| provider-auth.toml writer | `internal/workspace/providerauth.go` (add `WriteProviderAuth`) | New | Emit `[[providers]]` entry; temp file created `0o600` in the target dir, then rename |
| Vault store path | `internal/vault/infisical` (net-new `secrets set` subprocess) | New | `--store=vault`: write the `version = "1"` body via stdin/0600 temp file, never argv |
| Secret redaction | reuse `internal/secret` (`Redactor`, `Value`, `Errorf`) | Reuse | Register session token and minted secret; scrub all output/errors |

### Interfaces

```go
// internal/vault: optional capability a backend may implement.
type Provisioner interface {
    // MintClientSecret mints a credential on an existing identity.
    // The returned secret is pre-registered with the call's redactor;
    // callers must never log res.ClientSecret.
    MintClientSecret(ctx context.Context, req MintRequest) (MintResult, error)
}

type MintRequest struct {
    IdentityID   string        // existing identity; provision never creates one
    TTL          time.Duration // bounds blast radius of the minted secret
    APIURL       string        // defaults to the backend default
    SessionToken secret.Value  // caller's provider bearer, from env/CLI session, never a flag
}

type MintResult struct {
    ClientID       string       // identifier, not a secret
    ClientSecret   secret.Value // secret; carried as secret.Value, never a bare string
    ClientSecretID string       // for later revoke; not a secret
}
```

The command layer depends only on `internal/provision`; `internal/provision` imports
`internal/vault/infisical` to build the provisioner (minting is identity-level, so it does
not go through the project-scoped `Factory.Open` / `Registry.Build`). Infisical vocabulary
never reaches `internal/cli` (R10). `internal/provision` creates a `secret.Redactor` and
attaches it to the context with `secret.WithRedactor` **before** any mint/verify/store
call, and registers both the `SessionToken` and the minted `ClientSecret` the instant each
is in hand -- the ctx redactor is a precondition, not an optional decoration, because
`auth.go`'s registration and response scrubbing are no-ops when no redactor is on the ctx.
(Note for implementers: `scrubResponseBody`'s `clientSecret` parameter is currently dead
code -- the belt-and-suspenders string replace is not wired -- so the ctx redactor is the
real mechanism, not a fallback. The minted secret is always well above the redactor's
6-byte minimum fragment length.)

### Data flow

```
provision cmd (flags) -> internal/provision.Run
  -> vault.Provisioner.MintClientSecret        (GET identity, POST client-secrets)
  -> infisical.Authenticate + target-env probe (POST universal-auth/login; verify read)
  -> store: WriteProviderAuth (0600 file)  OR  infisical secrets set (vault)
  -> Result{identity, store target, client_secret_id}  (no secret) -> --json / exit code
```

The caller's provider **session token** (the bearer used for the identity read and mint)
is read from the provider's environment variable or CLI session file -- the same source
the provider CLI itself uses -- and carried as a `secret.Value`, registered with the
redactor. It is never accepted as a `--token` flag (a flag value lands on argv and in
shell history) and never placed on any subprocess argv; it rides the mint calls only as an
`Authorization` header. The command does not perform interactive login; a session scoped to
the wrong org makes the identity read return 403, which maps to the authentication-failure
exit code.

### Exit codes and `--json`

| Code | Meaning |
|------|---------|
| 0 | success: minted, verified, stored |
| 3 | authentication failure (bad/absent session, wrong org, mint rejected) |
| 4 | target environment not readable by the minted credential |
| 5 | storage-write failure |

`--json` success payload: `{ "identity_id", "client_id", "client_secret_id",
"store_target", "env" }` -- never the secret. Each non-zero exit prints a redacted
message naming the failed stage and a remediation. The codes are carried by a new typed
exit-code error that `internal/cli/root.go`'s `Execute()` maps to a process exit, mirroring
the existing `sessionattach.ExitCodeError` / `workspace.InitConflictError` branches (the
current `Execute()` only handles those two, so the new branch is net-new).

## Implementation Approach

1. **Vault `Provisioner` interface + Infisical impl.** Add the interface to
   `internal/vault`; implement `MintClientSecret` in a new
   `internal/vault/infisical/provision.go` reusing `auth.go`'s HTTP + redactor plumbing
   (identity GET, client-secrets POST, TTL). Read the session bearer from the provider env
   var / CLI session file as a `secret.Value`; send it only as an `Authorization` header.
   Unit-test against a stub HTTP server, including a 401/403 body that echoes the token to
   prove it is scrubbed.
2. **Verify probe.** In the same package, verify by calling `Authenticate` with the minted
   pair (auth half) and `runInfisicalExport` with the resulting JWT against the target env
   (read half). Both are reachable in-package; no new REST surface.
3. **provider-auth.toml writer.** Add `WriteProviderAuth` to
   `internal/workspace/providerauth.go`: create the temp file with `os.OpenFile(..., 0o600)`
   in the *same directory* as the target (same-fs rename is atomic; no write-then-chmod
   window), then rename; merge or replace the `(kind, project)` entry. Round-trip test with
   `LoadProviderAuth`.
4. **Orchestrator.** `internal/provision` attaches a `secret.Redactor` to the ctx and
   registers the session token before minting; runs mint -> verify -> store with typed
   errors mapping to exit codes; verify-before-store enforced structurally (store is
   unreachable on verify error). Table-test the sequence and each failure branch.
5. **Command + exit codes.** `provider-auth provision` cobra command: flags, `--json`,
   redacted human output; register via `AddCommand`. Add the typed exit-code error and a
   branch in `internal/cli/root.go` `Execute()`.
6. **Vault store path.** `--store=vault` writes the `version = "1"` body via the provider
   CLI's secret-set operation, feeding the body over **stdin or a `0600` temp file, never
   argv** (the credential body is a secret; niwa's argv-hygiene invariant forbids it on the
   command line). This is a net-new subprocess (only read/`export` exists today).
7. **Revoke-on-rotate.** On a re-run that replaces a stored credential, revoke the prior
   `client_secret` when its id is known (from the overwritten target), so repeated runs do
   not accumulate live secrets on the identity. When the prior id cannot be recovered, the
   run surfaces the new `client_secret_id` and documents that the previous secret remains
   live until its TTL lapses.
8. **Doc-consistency annotation.** Annotate `PRD-machine-identity-vault-sync.md`'s
   non-goal wording to reference the provision carve-out (tracked as a plan issue).

## Security Considerations

- **Secret in memory only, redacted at the boundary.** Both the session bearer and the
  minted `client_secret` are registered with a `secret.Redactor` the instant each is in
  hand, and carried as `secret.Value` (not bare strings). Registration depends on the
  redactor being attached to the context, so `internal/provision` calls
  `secret.WithRedactor` *before* the first mint call -- a precondition, since `auth.go`'s
  registration and response scrubbing are no-ops on a redactor-less ctx. Values are held in
  memory through verify and store and never written to any log, stdout, stderr, or error;
  all errors use `secret.Errorf`, and `--json` and human output carry only non-secret
  identifiers. (The `scrubResponseBody` string-replace fallback is currently unwired dead
  code, so the ctx redactor is the sole mechanism, not a second layer.)
- **Verify-before-store.** Storage is structurally unreachable unless verification
  succeeds, so a bad credential is never persisted. This also prevents a half-provisioned
  state where a workspace points at an unusable credential.
- **TTL-bounded mint, revoke-on-rotate.** The minted secret carries a TTL to bound the
  blast radius if a run is interrupted before the credential is used. Because a re-run mints
  a *new* server-side secret, rotation revokes the prior `client_secret` when its id is
  recoverable from the overwritten target, so repeated runs do not accumulate live secrets;
  when it cannot be recovered, the run surfaces the new id and the prior secret expires with
  its TTL. The returned `client_secret_id` also enables manual revocation.
- **File at `0600`, written atomically.** The `provider-auth.toml` writer creates the temp
  file with mode `0600` from the start (`os.OpenFile`, not write-then-chmod) in the target's
  own directory, then renames (same-fs rename is atomic), so a reader never observes a
  world-readable or partial intermediate. This matches the guardrail `LoadProviderAuth`
  already enforces on read.
- **No secret on argv, including the vault-store path.** The session bearer and minted
  secret never reach a subprocess command line: the mint calls use HTTP headers, and the
  `--store=vault` path feeds the credential body over stdin or a `0600` temp file. This
  keeps niwa's existing "no secret on argv" invariant intact for the net-new write path.
- **No new identity creation; vault-write narrowness is convention-enforced.** The
  `Provisioner` interface exposes only `MintClientSecret` -- no identity create/delete, no
  arbitrary write -- so the mint half is bounded by the *capability*. The `--store=vault`
  half rides the provider CLI's secret-set, which can technically address any path; its
  narrowness (writing only niwa's own credential path) is enforced by niwa's argv/path
  construction, i.e. by convention, not by the capability. This distinction is called out so
  a future change cannot silently widen it.
- **Wrong-org isolation.** A session token scoped to the wrong organization cannot read or
  mint on the target identity; the provider returns 403 and the command exits with the
  authentication-failure code rather than silently acting on the wrong org.
- **Response-body scrubbing.** Mint and verify responses are scrubbed with the registered
  redactor before any logging or error wrapping, so a provider error echoing the secret
  cannot leak it.

## Consequences

### Positive

- Onboarding a machine-identity workspace becomes one portable, non-interactive command;
  the fragile `curl` / `jq` / `base64` shell disappears from onboarding docs.
- Secret-hygiene and portability guarantees live in niwa, enforced by the redactor and the
  `net/http` client, not re-implemented per onboarding reader.
- The net-new surface is small: identity-management REST, a TOML writer, and thin
  orchestration -- everything else reuses existing, tested plumbing.
- The command is generic; the first consumer's identifiers stay in its private overlay and
  downstream onboarding skill.

### Negative / trade-offs

- niwa gains a credential-write path where it had none. Mitigation: the bounded carve-out
  is explicit, named, and structurally limited (no identity creation, no general vault
  write); the machine-identity PRD is annotated so the stance and the exception are read
  together.
- The default `file` target leaves a secret-bearing credential on disk. Mitigation: mode
  `0600`, atomic write, and the documented `--store=vault` opt-in for a no-local-file
  posture.
- niwa takes on the provider's identity-management REST contract (endpoint shapes,
  TTL/revoke semantics). Mitigation: the calls sit behind the `Provisioner` interface in
  the Infisical backend, isolated from the command and orchestration and covered by
  stub-server tests.

### Mitigations summary

Verify-before-store, redactor-at-the-boundary, `0600` atomic writes, TTL-bounded mints,
and the interface boundary between generic orchestration and provider REST together keep
the carve-out narrow, portable, and leak-resistant.
