---
status: Draft
problem: |
  Onboarding a machine-identity workspace vault is a long, cross-context
  choreography that today lives as hand-run shell in runbooks. A team admin
  must create a shared identity, attach Universal Auth, grant read access, and
  lay down folder structure; then every developer sets up a personal overlay,
  mints a client secret, stores it at an exact credential shape, and confirms it
  resolves. Every step is mechanical, the individual-phase credential shape is
  unforgiving, and mistakes surface silently at a later `niwa apply`, far from
  the cause. Depending on vault topology the individual phase may also need a
  login switch, and nothing tells the operator whether it does.
goals: |
  One command, `niwa onboard`, walks a team admin or a developer through setup
  as an interactive wizard. It works out which of the two setups applies and
  branches, makes the vault topology an explicit choice, automates every
  mechanical and exact-shape step it safely can, degrades to guided dashboard
  steps where the provider gives it no automatable surface, and pauses only for
  the logins the chosen topology actually needs. The individual-phase credential
  comes out in the exact contract shape by construction, and the wizard confirms
  the setup resolves before declaring success, so nobody ships a silently broken
  vault.
upstream: docs/briefs/BRIEF-niwa-onboard.md
motivating_context: |
  Two prior efforts (tsukumogami/niwa#194, mint-and-store a credential on an
  existing identity; tsukumogami/niwa#199, a doctor that validates the
  credential contract) productized individual pieces of this flow in isolation.
  Neither owned the whole choreography, the topology choice, or the team-phase
  setup. This PRD specifies the wizard those pieces become internal building
  blocks of, superseding them as standalone commands.
---

# PRD: niwa onboard

## Status

Draft.

Complexity: **Complex**. A technical design follows this PRD. The multi-org
choreography, the interactive-wizard shape with topology branches, the
team-phase automation-vs-guided split, and the secret-hygiene surface all carry
enough decision weight to warrant a DESIGN before implementation. This PRD owns
the requirements; the DESIGN owns the mechanics (exact REST paths, the wizard's
internal state machine, the partial-resume bookkeeping, the config-block
insertion mechanism).

## Problem Statement

niwa resolves a workspace's `vault://` secrets by authenticating with a machine
identity's `client_id` / `client_secret` pair. Getting a workspace to the point
where that resolution works is a long setup sequence spanning two phases, and
the read side gives no help performing it.

The **team phase**, run once per workspace by an admin, creates the shared
machine identity in the vault org, attaches Universal Auth, grants that identity
read access on the target environment, and lays down the folder structure the
workspace expects. The **individual phase**, run once by each developer, sets up
their personal overlay, mints a fresh client secret against the org that hosts
the workspace vault, stores the credential into the vault backing their personal
overlay, and confirms it resolves.

The crossing from the workspace-vault org to the personal-overlay vault hides an
assumption. When the workspace vault and the personal overlay share one account,
the developer stays in a single session throughout. When the workspace vault
lives in a dedicated org and the overlay vault lives in the developer's personal
account, the same crossing requires a login switch in the middle. Both shapes
are common, and today the developer has to work out on their own which one they
are in before they can even follow the runbook.

The individual phase must land on an exact shape or nothing works. The
credential lives at a specific vault path, under a key with a mandatory `p-`
prefix (the vault rejects keys that start with a digit, and roughly 37.5% of
UUIDv4 project IDs do), carrying a TOML body with a required version and the two
credential fields. Get any part of that wrong and there is no error at store
time. The failure surfaces later, as a `niwa apply` that dies partway through on
a credential it cannot parse, far from the typo that caused it.

So the sequence is deterministic enough that a machine should own it, fiddly
enough that humans get it wrong, and unforgiving in a way that hides the mistake
until much later. Today it is transcribed as hand-run shell into onboarding
documents, so the fragility is copied from workspace to workspace rather than
fixed once. The people affected are team admins standing up a new
machine-identity workspace and developers joining one; both currently hold the
whole sequence in their heads and can finish believing they succeeded when they
have produced a vault that will fail silently.

## Goals

- Collapse the whole choreography into one command, `niwa onboard`, that a team
  admin or a developer runs and is guided through as a wizard, with the setup
  it runs inferred from context and confirmable.
- Make the vault topology an explicit, named choice during the individual setup,
  so the number of login pauses is a property the wizard states rather than one
  the operator has to discover.
- Automate every mechanical and exact-shape step the provider gives niwa a safe
  surface for, and where it does not, hand the operator precise dashboard
  instructions and resume, rather than dead-ending on a raw provider error.
- Produce the individual-phase credential in the exact credential-sync contract
  shape by construction, so it cannot be stored malformed.
- Confirm the result resolves before declaring success, so an operator learns
  onboarding worked from the wizard rather than from a failed apply days later.
- Keep niwa out of the vault-administration business: every privileged step runs
  against the operator's own authenticated session, and niwa never holds
  administrative vault credentials of its own.
- Keep the command surface generic, with no org-, workspace-, or
  project-specific identifiers baked in.

## User Stories

**US-1: Team admin stands up a workspace's vault.**
As a team admin bringing a new machine-identity workspace online, I want to run
one command and be walked through creating the shared identity, attaching its
authentication, granting it read access, and laying down the folder structure,
so that my teammates can onboard against a ready vault without my having
assembled the sequence from a runbook.

**US-2: Developer joins a team that already has a shared identity.**
As a developer whose `niwa apply` cannot resolve the team's secrets because I
have no credential yet, I want the wizard to set up my personal overlay, mint a
credential, store it correctly, and confirm it resolves, so that my next apply
works and I never learn the vault path, the key prefix, or the body format.

**US-3: Developer in the split-login topology.**
As a developer whose workspace vault lives in a separate org from my personal
account, I want the wizard to tell me a login switch is coming, walk me to it,
wait, and resume, so that I do not have to figure out on my own whether a switch
was even needed or which org to switch to.

**US-4: Team admin hits a step their plan won't allow.**
As a team admin reaching a step the vault provider gates behind a plan I am not
on, I want the wizard to recognize it, tell me exactly what to create in the
dashboard and with what settings, wait, and then continue automatically, so that
the limit costs a single manual detour rather than the whole automated flow.

**US-5: Operator confirms onboarding actually landed.**
As a developer (or an admin) who wants to know onboarding is real before an
apply depends on it, I want to re-run `niwa onboard` and have it recognize my
setup is complete and go straight to verification, pointing at what is wrong
when the credential does not resolve, so that I get a straight answer up front
instead of discovering a broken setup through a later failing apply.

**US-6: Operator whose topology changed.**
As a developer whose vault topology changed (my personal account and the
workspace vault used to be one org and now are split, or vice versa), I want to
re-run the wizard against the new shape and have it re-mint and re-store the
credential where it now belongs, so that switching shapes is a re-run rather
than a manual teardown.

## Requirements

The PRD uses RFC 2119 normative language: **MUST** is binding, **SHOULD** is a
strong recommendation, **MAY** is permission.

### Functional

**R1 — One command, two setups, wizard-driven.**

niwa MUST provide a single command, `niwa onboard`, that runs an interactive
wizard covering both the team setup (run once by an admin) and the individual
setup (run once by each developer). The wizard MUST work out which setup applies
and branch to it. The command surface MUST NOT split into separate per-role
commands.

**R2 — Setup detection is inferred, always confirmable, and overridable.**

The wizard MUST infer which setup to run from observable workspace and session
state (for example, whether the team identity and folder structure already
exist, whether the operator has a personal overlay, whether a credential already
resolves) rather than requiring the operator to name the setup blind. The
inference MUST be presented to the operator for confirmation before any state is
changed. The command MUST also accept an explicit flag that names the setup
directly and bypasses the inference. Detection MUST NOT silently pick a setup
and proceed without a confirmable prompt or an explicit override.

**R3 — Vault topology is an explicit, named choice during the individual setup.**

During the individual setup the wizard MUST present the vault topology as a named
choice between the **same-login** shape (the workspace vault and the personal
overlay live in one account) and the **split-login** shape (the workspace vault
lives in a dedicated org and the overlay vault lives in the operator's personal
account). The wizard SHOULD infer the likely shape from the personal overlay and
workspace config and MUST let the operator confirm or override it. The chosen
shape MUST determine how many interactive login pauses the wizard inserts: zero
for same-login, exactly one switch for split-login. The exact detection
mechanics are deferred to the DESIGN.

**R4 — Login pauses match the chosen topology, and the wizard resumes.**

The wizard MUST pause for a human login only where the chosen topology genuinely
requires it: the interactive organization pick or SSO round-trip that a session
switch entails. In the same-login shape it MUST NOT insert any login pause after
the operator's initial session is established. In the split-login shape it MUST
insert exactly one pause, between minting against the workspace-vault org and
storing into the personal-overlay vault, walk the operator to that login, wait,
and resume automatically afterward.

**R5 — Team-phase privileged steps run against the operator's own session.**

Every privileged team-phase step MUST run against the operator's own
authenticated vault session (the same delegation niwa already uses for vault
reads). niwa MUST NOT hold administrative vault credentials of its own, mint or
custody an admin token, or reimplement the provider's admin REST API to create
identities, grants, or folders under its own authority.

**R6 — Team-phase automation split: folders automated, identity/auth/grant
guided.**

Within the team setup, the steps MUST be handled as follows in v1:

- **Folder / secret-path creation** MUST be automated by delegating to the
  operator's `infisical` CLI session (`infisical secrets folders create`), which
  is the one team-phase operation the installed CLI exposes natively.
- **Machine-identity creation, Universal Auth attach, and environment read-access
  (ACL) grant** MUST degrade to guided dashboard instructions. For each, the
  wizard MUST print exactly what to create, where, and with what settings; wait
  for the operator; and verify the step landed (for example, that the identity
  now exists and exposes a `client_id`) before continuing.

The wizard MUST NOT rely on driving the provider's identity/org/project
management REST endpoints with the operator's session JWT in v1. (See D3 for the
rationale and the future upgrade path.)

**R7 — Graceful degradation on plan-gated steps.**

When a team-phase step the wizard would otherwise perform is unavailable on the
operator's provider plan, the wizard MUST recognize the gated condition rather
than surface a raw provider error, MUST tell the operator precisely what to do
in the dashboard and with what settings, MUST wait, and MUST resume the rest of
the sequence automatically once the manual step is done.

**R8 — Individual-phase mint, store, and verify are automated.**

The individual setup MUST automate minting, storing, and verifying the
credential, reusing the mechanics the tsukumogami/niwa#194 design settled:

1. Read the existing team identity's `client_id` (a GET against the
   universal-auth identity endpoint). The wizard MUST NOT create the identity in
   this phase; identity existence is a team-phase precondition.
2. Mint a fresh client secret on that identity (a POST against the identity's
   client-secrets endpoint), authenticated by the operator's own session against
   the org that hosts the workspace vault. The minted secret's id MUST be
   captured so a later run can revoke it.
3. Verify the minted pair (R9) before storing it.
4. Store the credential into the personal-overlay vault at the exact
   credential-sync contract shape (R10).

The bearer token for the mint calls MUST come from the operator's provider
session (environment variable or CLI session file), never from a `--token` flag
or any other argv-borne value.

**R9 — Mint-time verification is a real authentication exchange.**

Before storing, the wizard MUST verify the minted credential by performing a real
universal-auth login exchange with it and a read against the target environment
(the same two-hop proof tsukumogami/niwa#194 settled: authenticate the minted
pair, then export from the target environment). A store MUST NOT be attempted
against an unverified pair. This is "provision-depth" verification and is
distinct from the wizard-end check in R10.

**R10 — Individual-phase credential is produced in the exact contract shape by
construction.**

The stored credential MUST be assembled by niwa, not typed by a human, so the
exact-shape contract cannot come out malformed. The wizard MUST write it at:

```
Path: /niwa/provider-auth/<kind>
Key:  p-<project-uuid>
```

with a body that is a TOML document:

```toml
version = "1"
client_id = "<minted client id>"
client_secret = "<minted client secret>"
api_url = "<provider api url>"   # optional; omit for the provider default
```

The `<project-uuid>` segment MUST be the project id from the workspace's vault
provider declaration, used verbatim (no case-folding, no normalization). The
literal `p-` prefix MUST be prepended to that UUID to form the key. This is the
same credential-sync contract `parseProviderAuthBody` consumes at apply time; the
wizard MUST produce exactly that shape and MUST NOT invent a parallel one.

**R11 — Wizard-end verification is a doctor-style shape-and-resolution check.**

Before declaring the setup complete, the wizard MUST run a shape-and-resolution
check that reuses the credential-sync read topology settled by
tsukumogami/niwa#199: one credential-sync provider opened once, the credential
pool enumerated across the three vault-registry sources (workspace overlay, team
config, personal global overlay), the credential-sync provider's own
`(kind, project)` pair self-excluded (it authenticates via CLI session, not the
pool), and the shared in-package contract validator applied to each resolved
body. This ensures the wizard and a later `niwa apply` can never disagree about
whether the credential resolves. This check is "doctor-depth"; it validates
body shape and resolution and does not, by itself, prove live authentication —
that guarantee comes only from R9's mint-time exchange. R9 and R11 MUST be named
and reported distinctly.

**R12 — Wizard-authored config lands in durable sources, never in the `.niwa/`
snapshot.**

Any configuration the wizard authors MUST be written to the durable upstream
source it belongs to, never to the materialized `.niwa/` directory (which is an
atomically replaced snapshot and would silently discard the write on the next
apply). Specifically:

- The personal-overlay vault declaration (`[global.vault.provider]`) and any
  per-workspace personal secrets MUST land in the operator's **personal-overlay
  repo** (`niwa.toml` at its root), which is a repo on the operator's own account.
- The local overlay pointer (`niwa config set global <slug>`) is an
  operator-local write to `~/.config/niwa/config.toml`.
- Any team-config change needed to declare the team vault provider or its
  secret paths MUST land in the **team's workspace source repo**, which requires
  the operator's own review/merge access and MUST NOT be committed on the
  operator's behalf without their action.

For each config write, the wizard MUST make clear which side (upstream repo vs.
operator-local) it lands on. The exact block-insertion mechanism is deferred to
the DESIGN.

**R13 — Self-referential credential guard.**

Because the wizard chooses project UUIDs and org logins on the operator's behalf,
it MUST detect and refuse a self-referential `(kind, project)` — one where the
credential-sync (personal-overlay) provider's own pair would be bootstrapped by
an entry in the credential pool — before writing anything, surfacing the
chicken-and-egg condition rather than producing a vault that cannot bootstrap
itself.

**R14 — Generic command surface.**

No org-, workspace-, or project-specific identifier MAY be baked into the
command's flags, defaults, or messages. Every such constant MUST come from the
workspace config and the operator's personal overlay at runtime. Provider
product names MUST NOT appear in generic surfaces where a neutral term serves.

**R15 — Idempotence and resume.**

Re-running `niwa onboard` against a completed setup MUST go straight to the
wizard-end verification rather than repeat the setup steps — R11 for a completed
individual setup, R21 for a completed team setup. Re-running
against a partially completed setup MUST resume sensibly: steps already done are
detected and skipped, and the wizard picks up where it left off. Re-running after
a topology change MUST re-mint and re-store the credential where the new shape
requires (US-6). The behavior is specified here; the resume bookkeeping mechanics
are deferred to the DESIGN.

**R16 — Distinct terminal outcomes carry distinct exit codes.**

The wizard MUST map its terminal outcomes to distinct exit codes so a caller (a
script, another agent) can tell them apart: success; wizard-end verification
failure (R11 found an unresolved or malformed credential); authentication failure
(absent or wrong-org session, or a mint rejected); operator decline / abort
mid-wizard; and storage-write failure. These MUST be carried by a typed error
routed through the existing `Execute()` error mapping, consistent with how other
niwa commands assign exit codes. The exact integer assignments are finalized in
the DESIGN; the requirement is that the outcome classes are distinguishable.

### Non-functional

**R17 — Secret hygiene (carried verbatim from the tsukumogami/niwa#194 design).**

Every credential-touching path in the wizard MUST obey the following, without
re-derivation:

- A `secret.Redactor` MUST be attached to the context (`secret.WithRedactor`)
  before any mint, verify, or store call, and every secret value (the operator's
  session bearer, the minted client secret) MUST be registered as a
  `secret.Value` the instant it is obtained. Registration and response scrubbing
  are no-ops on a redactor-less context, so this is a precondition, not a
  decoration.
- No secret MAY ever be placed on a subprocess or process argv. REST calls MUST
  carry secrets only in headers; the `infisical secrets set` storage path MUST
  feed the credential body over stdin or a `0600` temp file, never as a
  command-line argument.
- Any credential file the wizard writes MUST be created at mode `0600` from the
  start (via `os.OpenFile`, not write-then-chmod) in the target's own directory,
  then renamed over the target, so no reader can observe a world-readable or
  partial intermediate.
- Mint, verify, and login response bodies MUST be scrubbed by the registered
  redactor before any logging or error wrapping. All errors on these paths MUST
  be produced via `secret.Errorf`.
- No secret value MAY appear in any output surface — stdout, stderr, logs, a
  `--json` payload, or human-readable messages — at any exit path. Only
  non-secret identifiers (identity id, `client_id`, client-secret id, key path,
  status vocabulary) MAY appear.

**R18 — Interactive-terminal precondition.**

The wizard is interactive. When stdin is not a TTY and the inputs it needs were
not supplied non-interactively, the command MUST fail fast with a clear
diagnostic rather than block on a prompt that can never be answered, following
the established `IsStdinTTY` fail-fast pattern in the existing CLI. The
non-interactive inputs are the setup-selection override (R2) and the
topology-selection override (R3); when neither is supplied and no TTY is present,
the wizard cannot proceed and fails fast. (This is distinct from the missing
authenticated-session case, which is an in-scope login pause under R22, not a
fail-fast.)

**R19 — Functional-test coverage with two test doubles.**

The individual-phase steps split across two provider surfaces — REST calls (read
`client_id`, mint, verify) and CLI delegations (`login`, `export`,
`secrets set`, folder ops) — so hermetic coverage requires two test doubles, and
the requirements name both:

- The repo's hermetic `infisical` **CLI stub** on `PATH` (the functional suite's
  fake-binary harness) covers the CLI-delegated operations: login, export,
  `secrets set`, and folder create. It MUST support a store-write failure and a
  plan-gate error for the operations it serves, and it owns the CLI-reachable
  fixtures: the folder structure and the stored credential body the wizard-end
  read resolves through `infisical export` (both seedable as present, absent, or
  malformed), so a test can drive a folder landing-check failure or a wizard-end
  read failure from the CLI side.
- An HTTP-level **Infisical REST test double** — a mock server the wizard's
  mint/verify client is pointed at through its configurable `api_url` — covers
  the REST operations. Its modeled surface MUST include the read-identity,
  mint-client-secret, universal-auth login, and revoke (DELETE client-secret)
  endpoints. It MUST support:
  - **Fault injection** for at least wrong-org authentication failure, mint
    rejection, plan-gate responses, login-exchange failure, and revocation
    failure.
  - **Resource-state seeding**, independent of the fault modes: each
    REST-reachable modeled resource — the identity, its `client_id`, minted
    secret bodies, and the environment read grant — MUST be seedable as present,
    absent, or malformed, so a test can drive a landing-check failure without
    inducing a transport-level fault. (Folder structure and the stored
    credential body are CLI-stub fixtures, per the division above.)
  - **Request recording** of every outbound request, so a test can assert the
    absence of a class of call (for example, that no identity/org/project
    management endpoint was ever called) or the presence of one (for example, a
    revoke request for a specific secret id).

The feature MUST ship a `@critical` Gherkin happy-path scenario for the
individual setup that drives both doubles, so the core flow is gate-checked on
every PR without a real Infisical service or developer login. The TTY-decline
path MUST also be covered by a `@critical` scenario (pinned by AC-32).

### Additional functional requirements

These requirements were added after the Phase 4 jury review to close gaps in
re-run, verification, and precondition behavior. They keep the next free numbers
rather than renumber the requirements above.

**R20 — Minted-secret revocation on supersession and on store failure.**

The minted-secret id captured in R8 has a consumer:

- When a re-run mints a new secret that supersedes a previously recorded one (an
  R15/US-6 re-mint, or a topology change), the wizard MUST attempt best-effort
  revocation of the previously recorded secret using its captured id. Revocation
  failure MUST NOT be fatal: the wizard surfaces a warning naming the un-revoked
  secret id and continues.
- When the mint and mint-time verification (R9) succeed but the subsequent store
  (R8 step 4) fails, the wizard MUST attempt best-effort revocation of the
  just-minted secret before exiting with the storage-write failure outcome (R16),
  so it never leaves an orphaned live secret behind. A revocation failure here is
  likewise a warning and MUST NOT change the storage-write exit outcome.
- When a prior secret's id is not recoverable, the wizard MUST surface the new
  secret id and state that the old secret remains live until its TTL lapses
  (matching the tsukumogami/niwa#194 rotation behavior).

**R21 — Team-setup re-run verification.**

On re-run against a completed team setup, the wizard MUST run a team-phase
verification that mirrors R6's per-step landing checks: the machine identity
exists and exposes a `client_id`, the environment read grant is present, and the
expected folder structure exists. This is the team-setup counterpart to R11
(which verifies the individual-phase credential-sync contract); a team admin has
produced identity/auth/grant/folders and no personal-overlay credential, so R11's
credential-sync read topology does not apply to them. R15's "go straight to the
wizard-end verification" resolves to R21 for the team setup and R11 for the
individual setup. When a check fails, the verification MUST name which team-phase
artifact is missing or incomplete. R21 and R11 MUST be reported distinctly.

**R22 — Session and personal-overlay preconditions.**

- **Authenticated session.** At start the wizard MUST check whether the operator
  has an authenticated `infisical` session. If none, it MUST walk the operator
  through `infisical login` as an in-scope wizard pause (like the topology login
  pauses of R4), wait, and resume — it MUST NOT fail fast on a missing session.
  (R18's fail-fast covers the absence of a usable terminal, not the absence of a
  session.)
- **Personal overlay.** Setting up the personal overlay is a wizard-performed
  step, not an assumed precondition. When the overlay pointer is not registered,
  the wizard MUST register it (`niwa config set global`, an operator-local
  write). When the personal-overlay repo does not exist yet, the wizard MUST
  scaffold the overlay config locally and guide the operator to create and push
  the repo — mirroring the bootstrap flow, which produces a local commit the
  operator pushes — and MUST NOT create a remote repo on the operator's behalf.
  The overlay config MUST land in the personal-overlay repo, never in the
  `.niwa/` snapshot (R12).

## Acceptance Criteria

### Command surface and detection

- [ ] **AC-1**: `niwa onboard` exists as a single command and, when run,
      enters an interactive wizard. There is no separate per-role onboarding
      command.
- [ ] **AC-2**: On an environment where the team identity and folders already
      exist and the operator has no credential yet, the wizard's inferred setup
      is the individual setup, and that inference is shown for confirmation
      before any state changes.
- [ ] **AC-3**: The explicit setup-selection flag forces the named setup
      regardless of inferred state, verified by running it in an environment
      whose inference would otherwise pick the other setup.
- [ ] **AC-4**: The wizard changes no state before the operator confirms the
      detected setup (or supplies the override flag), verified by declining at
      the confirmation prompt and asserting no config, vault, or identity write
      occurred.

### Topology and login pauses

- [ ] **AC-5**: During the individual setup the wizard presents the topology as
      a named same-login / split-login choice and lets the operator confirm or
      override the inferred shape.
- [ ] **AC-6**: In the same-login shape the wizard inserts zero login pauses
      after the initial session is established.
- [ ] **AC-7**: In the split-login shape the wizard inserts exactly one login
      pause, positioned between the mint (against the workspace-vault org) and
      the store (into the personal-overlay vault), and resumes automatically
      after the operator completes it.

### Team phase

- [ ] **AC-8**: Folder / secret-path creation is performed by delegating to the
      operator's `infisical` CLI session, verified against the CLI stub by
      asserting the folder-create delegation fires.
- [ ] **AC-9**: For identity creation, Universal Auth attach, and environment
      read-access grant, the wizard prints guided dashboard instructions whose
      text contains the specific required tokens (the identity name, the auth
      method, and the target environment slug), waits, and then verifies the step
      landed (for identity creation, that the identity now exposes a `client_id`)
      before continuing.
- [ ] **AC-9b**: When a guided team-phase step's landing check fails (for
      example, the operator claims the identity is created but it exposes no
      `client_id`), the wizard does NOT continue to the next step; it re-surfaces
      the instruction or reports the missing artifact. Induced via the REST
      double returning no identity / no `client_id`.
- [ ] **AC-10**: No team-phase step drives the provider's identity/org/project
      management REST endpoints with the operator's session JWT; verified by
      asserting the REST double's request recorder shows zero calls to any
      identity/org/project management endpoint on the team path.
- [ ] **AC-11**: When a team-phase step is plan-gated (induced via a plan-gate
      response from the CLI stub for folder create, or from the REST double for a
      REST-backed step), the wizard emits guided dashboard instructions for that
      specific step rather than a raw provider error, waits, and resumes the
      remaining steps automatically.
- [ ] **AC-12**: No team-phase step uses an administrative token custodied by
      niwa; every privileged call uses the operator's own session.

### Individual phase

- [ ] **AC-13**: The wizard reads the existing identity's `client_id` and mints
      a fresh client secret on it without creating a new identity, verified
      against the REST double (the GET-identity and POST-client-secret requests
      are recorded, and no create-identity request is).
- [ ] **AC-14**: The wizard performs a real universal-auth login exchange plus a
      target-environment read with the minted pair before storing (mint-time
      verification, R9). When the REST double is configured to fail the
      login-exchange, the wizard does not store: no `infisical secrets set`
      delegation fires afterward on the CLI stub.
- [ ] **AC-15**: The stored credential is at path `/niwa/provider-auth/<kind>`,
      key `p-<project-uuid>`, with a TOML body carrying `version = "1"`,
      `client_id`, and `client_secret` (and `api_url` only when non-default).
      Verified by inspecting the stored body shape.
- [ ] **AC-16**: The `<project-uuid>` segment in the key is the workspace's
      configured project id verbatim, with `p-` prepended and no case-folding,
      verified with a mixed-case UUID.
- [ ] **AC-17**: A human never types the vault path, the prefixed key, or the
      body; the wizard assembles all three. Verified by driving the happy path
      with no operator input for those fields.

### Verification, re-run, and generic surface

- [ ] **AC-18**: Before declaring success, the wizard runs the doctor-style
      shape-and-resolution check across the three vault-registry sources with the
      credential-sync provider's own pair self-excluded, and reports it distinctly
      from the mint-time verification (R11 vs R9).
- [ ] **AC-18b**: When the stored credential is malformed or does not resolve,
      the wizard-end check names the failing `(kind, project)` / source and the
      nature of the failure (missing entry, malformed body, missing field, or
      unsupported version) rather than reporting a bare failure. The wizard-end
      read follows R11's topology, which resolves through the credential-sync
      provider's `infisical export` path, so the fixture owner for the stored
      body is the **CLI stub** (seed its export response with a malformed or
      absent body for the pair), not the REST double.
- [ ] **AC-19**: Re-running `niwa onboard` against a fully completed setup goes
      straight to the wizard-end verification and performs no re-setup.
- [ ] **AC-20**: Re-running against a partially completed setup skips the
      already-done steps and resumes at the first incomplete one. (The
      "partially completed" fixture depends on the DESIGN-level resume-state
      representation deferred by R15; the AC is verifiable once that
      representation is fixed.)
- [ ] **AC-21**: Re-running after a topology change re-mints and re-stores the
      credential at the location the new shape requires.
- [ ] **AC-22**: The wizard refuses to write when the credential-sync provider's
      own `(kind, project)` would be bootstrapped from the credential pool
      (self-referential guard), failing before any write.
- [ ] **AC-23**: No org-, workspace-, or project-specific identifier appears in
      the command's flags, defaults, or hard-coded messages; verified by a
      source grep over the command surface.

### Config-authoring targets

- [ ] **AC-24**: The personal-overlay vault declaration is written to the
      operator's personal-overlay repo (`niwa.toml`), and nothing the wizard
      writes lands in the materialized `.niwa/` snapshot directory. Verified by
      asserting the snapshot directory is unchanged and the overlay file carries
      the block.
- [ ] **AC-25**: For each config write, the wizard states whether it landed in an
      upstream repo or in operator-local state; a team-config change is not
      committed without the operator's own action.

### Exit codes, hygiene, and test coverage

- [ ] **AC-26**: Success, wizard-end verification failure, authentication
      failure, operator decline/abort, and storage-write failure each produce a
      distinct exit code. The three failure outcomes are induced via fault
      injection: wrong-org authentication and mint rejection via the REST double,
      store-write failure via the CLI stub, verification failure via a seeded
      malformed/absent body. Abandoning a guided dashboard step folds into the
      operator decline/abort outcome (no separate code).
- [ ] **AC-27**: Rendering every wizard error and output surface against a body
      whose `client_secret` is a sentinel canary never emits the canary — no
      secret reaches stdout, stderr, logs, a `--json` payload, or an error chain.
- [ ] **AC-28**: No secret value is ever placed on a subprocess or process argv;
      verified by asserting the `infisical secrets set` invocation carries the
      body via stdin or a `0600` temp file and REST calls carry secrets only in
      headers.
- [ ] **AC-29**: Any credential file the wizard writes is created via
      `os.OpenFile` at mode `0600` (never a broader mode) in the target's own
      directory and renamed over the target — there is no write-then-chmod path —
      and the final file mode is `0600`. Verified at the mechanism level (the
      open mode, the same-directory temp, the rename, the final mode), not by
      observing a transient intermediate.
- [ ] **AC-30**: When stdin is not a TTY and the needed inputs were not supplied
      non-interactively, the command fails fast with a clear diagnostic instead
      of blocking.
- [ ] **AC-31**: A `@critical` Gherkin scenario drives the individual-setup happy
      path against both the CLI stub and the REST double, and passes with no real
      service or login.
- [ ] **AC-32**: A `@critical` Gherkin scenario covers the TTY-decline path: the
      operator declines at the setup-confirmation prompt and the wizard exits with
      the decline/abort outcome having changed no state.

### Revocation, team-phase verification, and preconditions

- [ ] **AC-33** (R20): On a re-run that supersedes a previously recorded secret,
      the wizard attempts to revoke the prior secret by its captured id. A
      revocation failure (induced via the REST double) is non-fatal: the wizard
      warns naming the un-revoked secret id and completes.
- [ ] **AC-34** (R20): When the mint and mint-time verification succeed but the
      store fails (induced via the CLI stub store-write failure), the wizard
      attempts to revoke the just-minted secret before exiting with the
      storage-write outcome, leaving no orphaned live secret. Verified by
      asserting a revoke request for the just-minted id was recorded.
- [ ] **AC-35** (R21): Re-running against a completed team setup runs the
      team-phase verification (identity exists and exposes a `client_id`, grant
      present, folders exist), reports it distinctly from R11, and names which
      artifact is missing when a check fails. A failing check is induced by
      seeding the absent artifact via the doubles' resource-state seeding: the
      identity, its `client_id`, and the environment grant via the REST double,
      the folder structure via the CLI stub.
- [ ] **AC-35b** (R20 third bullet): On a re-run where no prior secret id is
      recoverable, the wizard surfaces the new secret id and states the old secret
      remains live until its TTL lapses, and does not attempt a revoke — verified
      by asserting no revoke request is recorded on the REST double.
- [ ] **AC-36** (R22): When no authenticated `infisical` session exists at start,
      the wizard walks the operator through `infisical login` as a pause and
      resumes; it does not fail fast for a missing session.
- [ ] **AC-37** (R22): When the personal-overlay pointer is unregistered, the
      wizard registers it via an operator-local `niwa config set global` write.
      When the personal-overlay repo does not exist, the wizard scaffolds the
      overlay config locally and guides the operator to create and push the repo,
      and does not create a remote repo on the operator's behalf.

## Out of Scope

- **niwa holding administrative vault credentials, or reimplementing the
  provider's admin REST API** to create identities, grants, or folders under its
  own authority. This is the hard line from the BRIEF: the wizard drives the
  operator's own session for every privileged step and never becomes a
  vault-administration tool with its own admin-token custody. Crossing it would
  take on org-wide admin blast radius and duplicate a maintained provider
  surface.
- **Driving the provider's identity/org/project management REST endpoints with
  the operator's session JWT in v1.** The capability is unverified against the
  provider docs, and relying on it slides toward the admin-API custody line
  above. v1 uses guided dashboard steps for those operations (D3); a future
  release may revisit session-JWT REST automation once the capability is
  confirmed.
- **Non-Infisical vault backends for the admin and provisioning steps in v1.**
  The credential-resolution layer is already provider-abstracted; this
  onboarding choreography targets the machine-identity flow niwa already
  supports and can generalize to other backends later.
- **Preserving tsukumogami/niwa#194 (`provider-auth provision`) and
  tsukumogami/niwa#199 (`niwa vault check`) as standalone shipped commands.**
  Their mechanics fold into the wizard as internal building blocks; this feature
  supersedes them rather than shipping alongside them.
- **Solving the provider's free-plan identity-count limit.** The provider's free
  tier caps total identities (human plus machine), which can block the
  team-phase identity creation before any automation question arises. This is an
  external constraint the wizard surfaces (as part of the guided identity-creation
  step and its plan-gated degradation), not one this feature removes.
- **Auto-committing team-config changes on the operator's behalf.** Where the
  team setup needs a change to the team's workspace source repo, that repo
  requires the operator's own review/merge access; the wizard states what is
  needed but does not push it for them.

## Decisions and Trade-offs

These entries resolve the details the BRIEF deferred to the PRD (setup
detection, login pause/resume, topology naming/detection) and record the
load-bearing choices the research settled.

**D1 — One command with inferred, confirmable setup detection.**
Decision: a single `niwa onboard` that infers the setup from workspace/session
state, confirms it, and accepts an explicit override flag.
Alternatives: two commands (`onboard-team` / `onboard-dev`); a required
positional argument naming the setup.
Why: the BRIEF frames this as one wizard that "works out which setup applies and
branches." Two commands push the branch decision onto the operator, which is the
exact discovery burden the feature removes. Inference with a mandatory confirm
keeps the operator in control without making them name the setup blind; the
override flag preserves scriptability and a non-interactive escape.

**D2 — Vault topology is an explicit named choice, inferred and confirmed.**
Decision: present same-login vs split-login as a named choice, infer the likely
shape from the personal overlay and workspace config, and let the operator
confirm or override.
Alternatives: silently auto-detect and insert pauses without naming the shape;
always ask with no inference.
Why: the number of login pauses is exactly what the operator cannot predict
today. Naming the shape makes the pause count a stated property rather than a
surprise. Pure silent detection would reintroduce the "why did it just ask me to
log in?" confusion; pure asking wastes the signal already present in config. The
detection mechanism itself is deferred to the DESIGN.

**D3 — Team-phase automation split: folders automated, identity/auth/grant
guided.**
Decision: automate folder/secret-path creation via the operator's `infisical`
CLI session; degrade identity creation, Universal Auth attach, and environment
ACL grant to guided dashboard instructions.
Alternatives: automate all four by having the wizard issue the provider's
management REST calls with the operator's session JWT.
Why: research into the installed `infisical` CLI found it exposes no identity,
org, or project management commands at all — only folder creation plus
login/export/secrets-set are CLI-native. Automating the other three would
require the wizard to call the provider's management REST with the operator's
session token, a capability the provider docs do not confirm is accepted for
those endpoints, and one that slides toward the admin-API custody line the BRIEF
rules out. Guided dashboard steps are unglamorous but honest: the wizard prints
exactly what to create and verifies it landed before continuing, so the operator
never dead-ends. Future upgrade path (not committed): if session-JWT management
REST is empirically confirmed and stays within the custody boundary, those three
steps could later be automated behind the same guided fallback.

**D4 — Individual-phase mint is automated via the settled #194 REST surface.**
Decision: automate mint/store/verify using the endpoints tsukumogami/niwa#194
settled (GET identity for `client_id`, POST client-secrets for a fresh secret,
login-exchange verify), on an identity that already exists.
Alternatives: also degrade the individual-phase mint to a guided dashboard step.
Why: unlike the team-phase management operations, minting a client secret on an
existing identity is the named, bounded provision carve-out #194 already
validated — it does not create an identity, does not need admin custody, and
authenticates with the operator's own session. It is the one credential-creating
operation with a confirmed, safe automatable surface, and automating it is where
the exact-shape-by-construction guarantee (R10) comes from. Identity *creation*
stays guided (D3) precisely because it is the operation #194 deliberately
excluded.

**D5 — Two named verifications at two depths.**
Decision: keep mint-time verification (a real authentication exchange, R9)
distinct from the wizard-end check (doctor-style shape-and-resolution, R11), and
report them under distinct names.
Alternatives: a single verification step reused for both purposes.
Why: the two priors disagree on what "verify" means, and the guarantees genuinely
differ. The mint-time exchange proves the minted pair authenticates and can read
the target; the wizard-end check proves the stored credential's shape resolves
through the same read topology apply uses, so the wizard and apply cannot
disagree — but it deliberately does not re-authenticate. Collapsing them would
either over-claim (a clean shape check is not proof of live auth) or over-cost
(re-authenticating on every re-run). Naming them separately keeps each
guarantee's scope honest.

**D6 — Wizard-end check reuses the #199 read topology by construction.**
Decision: the wizard-end verification reuses the one-provider / three-source /
self-exclusion read topology and the shared in-package contract validator from
tsukumogami/niwa#199 rather than introducing a lighter existence probe.
Alternatives: a bespoke, lighter "does the key exist" check.
Why: #199's design rejected a lighter probe specifically because reusing apply's
exact read path is what makes "the wizard and apply never disagree" true by
construction rather than by test coverage. A parallel check would be a second
source of truth that could drift.

**D7 — Config authoring targets durable upstream sources, never `.niwa/`.**
Decision: write the personal-overlay vault declaration to the personal-overlay
repo, the overlay pointer to operator-local config, and any team-config change to
the team source repo (with the operator's own action); never write persisting
config into the materialized `.niwa/` snapshot.
Alternatives: write directly into the local `.niwa/` snapshot for immediacy.
Why: `.niwa/` is atomically replaced from upstream on every `niwa apply`, so a
write there survives only until the next apply and then vanishes silently —
exactly the class of far-from-the-cause failure this feature exists to kill. The
bootstrap flow already follows this "author upstream, not in the snapshot" rule;
the wizard inherits it.

**D8 — `niwa onboard` is a new top-level command that folds #194/#199 in as
internal building blocks.**
Decision: ship the wizard as its own top-level `onboard` command that reuses the
provision and doctor mechanics as shared internal packages, not by shelling out
to standalone `provider-auth provision` / `vault check` commands.
Alternatives: orchestrate the two prior commands as subprocesses; fold the
wizard under one of their existing namespaces.
Why: the BRIEF supersedes #194 and #199 as standalone commands, so there is no
standalone command to shell out to. Onboarding is a different object from either
provision or doctor — it owns a multi-step human+machine sequence neither prior
command anticipated — so a new namespace fits the "is this the same kind of
object?" test better than extending either. Reusing their logic as internal
packages keeps the "never disagree" guarantee (D6) and avoids duplicating the
secret-hygiene surface.

**D9 — Identity creation is in scope for the team setup, but guided, not
automated.**
Decision: the team setup owns identity creation (a brand-new workspace needs the
identity to exist), performed as a guided dashboard step with verification, not
as an automated call.
Alternatives: leave identity creation entirely out of scope (as #194 did);
automate it.
Why: #194 excluded identity creation because it was scoped to minting on an
existing identity. The wizard is the first artifact that owns end-to-end setup,
so it must cover the case where the identity does not yet exist — but the
installed CLI cannot create identities and the management-REST path is unverified
(D3), so "in scope but guided" is the honest resolution: the wizard is
responsible for getting the identity created, and does so by walking the operator
through it and confirming the result.

**D10 — Re-run is idempotent by resuming from observed state.**
Decision: re-running detects completed and partial state and either verifies (if
complete) or resumes at the first incomplete step; a topology change triggers
re-mint and re-store.
Alternatives: always run the full sequence; refuse to run a second time.
Why: the BRIEF's fourth journey is an operator re-running to confirm the setup
landed, and its topology-change journey is a re-run against the new shape. Both
require re-run to be safe and to do the least work needed. Detecting state and
resuming matches how the operator thinks ("finish what's not done; confirm what
is"). The resume bookkeeping is a DESIGN concern.

**D11 — Minted-secret revocation is best-effort and never fatal.**
Decision: a re-run that supersedes a prior secret best-effort revokes it by its
captured id, and a mint-then-verify success followed by a store failure best-effort
revokes the just-minted secret before exiting with the storage-write outcome
(R20). A revocation failure is a warning, not a fatal error or an exit-code change.
Alternatives: hard-fail when revocation fails; leave every superseded/orphaned
secret to lapse at its TTL; move revocation entirely out of scope.
Why: R8 already captures the minted secret id, so something must consume it or the
capture is dead state. Leaving orphaned live secrets on every re-run or failed
store accumulates credential debt the operator can't see. Hard-failing on a
revocation error would make a successful onboarding hostage to a cleanup step that
is not load-bearing for the credential actually working. Best-effort revocation
plus a warning cleans up the common case without letting the janitorial step block
the outcome; when the prior id is unrecoverable, the wizard says so and the secret
lapses at its TTL, matching #194's rotation behavior.

**D12 — Team-setup re-run verification is its own check, not R11.**
Decision: a completed team setup re-runs a team-phase verification (R21) that
mirrors R6's per-step landing checks; it does not run R11.
Alternatives: reuse R11 (the individual-phase credential-sync check) for both
setups.
Why: R11 validates the individual-phase credential-sync contract across three
vault registries with self-exclusion — artifacts a team admin never produces. A
team admin produces an identity, its auth, a grant, and folders, and no
personal-overlay credential, so R11 has no meaning for them. Mirroring R6's landing
checks is the verification that matches what the team setup actually created, which
is what makes the admin half of US-5 buildable.

**D13 — Session login and overlay setup are in-scope wizard steps, but remote
repo creation stays operator-driven.**
Decision: the wizard checks for an authenticated session at start and, if absent,
walks the operator through `infisical login` as an in-scope pause (not a
fail-fast); it registers the personal-overlay pointer and scaffolds the overlay
config, and when the overlay repo does not exist it guides the operator to create
and push it rather than creating a remote repo itself (R22).
Alternatives: treat an authenticated session and an existing overlay repo as
hard preconditions and fail fast when either is missing; or have the wizard create
the remote overlay repo on the operator's behalf.
Why: the BRIEF's journeys have the wizard "set up my personal overlay" and pause
only for logins the topology needs — both imply the wizard owns the session and
overlay setup rather than assuming them. Failing fast on a missing session would
contradict the "walk them to each login, wait, and resume" outcome. But creating a
remote repo on the operator's account is a heavier, less reversible action with no
documented precedent except the bootstrap flow, which deliberately produces a
local commit the operator pushes. The wizard follows that same boundary: it
authors and stages locally, and the operator owns the push.

## Known Limitations

- **Team-phase identity/auth/grant require manual dashboard steps in v1.** The
  installed provider CLI exposes no management verbs, so these three steps are
  guided rather than automated. The wizard makes them precise and verifies them,
  but they are not one-keypress.
- **Free-plan identity cap is an external wall.** An organization on the
  provider's free tier can exhaust its identity allotment before the team setup
  can create the shared identity. The wizard surfaces this but cannot lift it.
- **Wizard-end verification proves shape and resolution, not live
  re-authentication.** A clean wizard-end check (R11) confirms the stored
  credential resolves through apply's read path; it is not a fresh proof that the
  credential still authenticates. That proof exists at mint time (R9); a rotation
  that invalidates the secret afterward is caught at the next real apply, not by
  a re-run of the shape check.
- **Interactive only.** The wizard needs a TTY (or explicitly supplied inputs);
  it fails fast rather than degrading to a non-interactive mode in v1.

## Open Questions

This section MUST be empty before the PRD transitions to Accepted.

- *(none — the details the BRIEF deferred are resolved under Decisions and
  Trade-offs; remaining mechanics are DESIGN-level, not open requirements
  questions.)*
