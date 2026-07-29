---
schema: design/v1
status: Current
problem: |
  Standing up a machine-identity vault for a niwa workspace is a long
  cross-context choreography that today lives as hand-run shell in runbooks: a
  team admin creates a shared identity, attaches Universal Auth, grants read
  access, and lays down folders; then every developer mints a client secret,
  stores it at an unforgiving exact shape, and hopes it resolves. The design
  problem is to orchestrate that choreography as one interactive command across
  two delegation surfaces (the operator's `infisical` CLI session and a net-new
  management REST client), with the individual-phase credential correct by
  construction, every privileged step run against the operator's own session,
  and the whole flow testable hermetically with no real Infisical service.
decision: |
  Ship `niwa onboard` as a single new cobra command backed by a wizard engine in
  `internal/onboard` that recomputes all resume, skip, and landing decisions from
  observable world state on every run, persisting exactly one non-secret record
  (a minted secret id, for revocation) at `~/.config/niwa/`. Detection is
  layered local-first, reusing the one identity-GET the flow already needs.
  Team-phase folders are automated via `infisical secrets folders create`;
  identity, Universal Auth, and grant degrade to guided dashboard steps with
  landing checks. Individual-phase mint/verify/revoke run through new
  package-level functions in `internal/vault/infisical/management.go`; the
  credential is assembled and stored over stdin at the exact credential-sync
  contract shape; config is authored per-site with a surgical TOML table helper.
  Two hermetic test doubles (an httptest REST server and the extended CLI stub)
  drive the `@critical` scenarios. Terminal outcomes map to a typed
  `onboard.ExitCodeError` carrying codes 2-6.
rationale: |
  World-state re-derivation is chosen over a persisted step machine because the
  PRD already mandates a live landing check at every point that matters, so a
  step cursor would either duplicate those checks or be trusted instead of them,
  recreating the silent-failure-far-from-the-cause mode the feature exists to
  kill. Extending `internal/vault/infisical` in place keeps every Infisical
  wire format in one package and makes mint-time verification a same-package
  call rather than a new import edge. Guided dashboard steps for
  identity/auth/grant are the honest resolution of a CLI that exposes no
  management verbs and a session-JWT REST path the provider docs don't confirm.
  The per-site config posture and the two-double test split follow the PRD's own
  R12/R19 divisions rather than inventing new ones.
upstream: docs/prds/PRD-niwa-onboard.md
---

# DESIGN: niwa onboard

## Status

Current

This design satisfies PRD-niwa-onboard (In Progress) and supersedes the
standalone command surfaces of tsukumogami/niwa#194 (`provider-auth provision`)
and tsukumogami/niwa#199 (`vault check`), folding their mechanics in as internal
building blocks per the PRD's D8.

## Context and Problem Statement

`niwa` resolves a workspace's `vault://` secrets by authenticating a machine
identity's `client_id` / `client_secret` pair. Getting a workspace to the point
where that resolution works is the long setup sequence the PRD describes: a
team phase run once by an admin (create the shared identity, attach Universal
Auth, grant environment read access, lay down folders) and an individual phase
run once per developer (mint a fresh secret against the org hosting the
workspace vault, store it into the personal-overlay vault at the exact
credential-sync shape, confirm it resolves). Today none of it is owned by code;
it's transcribed as shell into onboarding documents, so the fragility is copied
from workspace to workspace rather than fixed once.

The technical problem this design owns is orchestration, not any single REST
call. Four properties make the orchestration hard, and they shape every decision
below:

- **A hard custody boundary runs through the middle of it.** Every privileged
  step must run against the operator's own authenticated vault session (R5).
  `niwa` never mints or holds an admin token and never reimplements the
  provider's admin REST under its own authority. That splits the work across two
  delegation surfaces that behave nothing alike: the operator's `infisical` CLI
  session (login, export, `secrets set`, folder create) and a net-new REST
  client that speaks with the operator's own bearer token (read identity, mint,
  verify-login, revoke). Some team-phase operations have no automatable surface
  on either, so they degrade to guided dashboard steps.

- **The choreography spans contexts the operator can't predict.** Depending on
  vault topology, the crossing from the workspace-vault org to the personal
  overlay is either a single session (same-login) or a mid-flow login switch
  (split-login). Nothing tells the operator which shape they're in. The wizard
  has to infer it, name it, and insert exactly the login pauses it implies —
  zero or one (R3/R4).

- **The individual-phase credential is unforgiving and must be right by
  construction.** It lands at `/niwa/provider-auth/<kind>`, key `p-<project-uuid>`,
  carrying a TOML body with a mandatory `version` and the two credential fields
  (R10). Any part wrong and there's no error at store time; the failure surfaces
  later as a `niwa apply` that dies on a credential it can't parse. The wizard
  assembles the shape so a human never types it, and reuses apply's exact read
  topology to confirm resolution before declaring success (R11).

- **All of it must be hermetically testable.** The individual phase touches two
  provider surfaces, so hermetic coverage needs two test doubles (R19): the
  repo's on-PATH `infisical` CLI stub for CLI-delegated operations and an
  HTTP-level REST double for mint/verify/revoke. Both must seed resource state,
  inject faults, and record requests so the `@critical` scenarios gate the core
  flow on every PR with no real service or developer login.

No wizard, state machine, or multi-step interactive command exists anywhere in
`internal/cli` or `internal/workspace` today — this is genuinely new ground
built on existing cobra, exit-code, prompt, and vault-delegation idioms.

## Decision Drivers

Drawn from the PRD's requirements and from the shape of the existing codebase.

**From the requirements:**

- **Hard custody boundary (R5, Out of Scope).** No admin-token custody, no
  reimplementation of the provider's management API under niwa's authority.
  Every privileged call rides the operator's own session.
- **Secret hygiene, inherited verbatim (R17).** Redactor attached before first
  use; every secret registered the instant it's obtained; nothing on argv; REST
  secrets in headers only; the CLI store path fed over stdin or a `0600` temp
  file; all errors via `secret.Errorf`; no secret in any output surface at any
  exit path.
- **Hermetic two-double testability (R19).** The individual-phase happy path and
  the TTY-decline path ship as `@critical` Gherkin scenarios driving both
  doubles with no real service.
- **Wizard and apply must never disagree (R11/D6).** The wizard-end check reuses
  apply's exact credential-sync read topology so agreement is true by
  construction, not by test coverage.
- **Topology is a first-class, named, confirmable choice (R3).** The number of
  login pauses is a stated property, not a surprise.
- **Graceful degradation on plan-gated and un-automatable steps (R6/R7).** A
  gated step becomes precise guided instructions plus a landing check, never a
  raw provider error.
- **Generic command surface (R14).** No org-, workspace-, or project-specific
  identifier in flags, defaults, or messages; every such constant comes from
  config at runtime.

**From the codebase:**

- **Established cobra / exit-code / prompt idioms.** Commands are package-level
  `var xCmd` with flags bound in `init()` and `rootCmd.AddCommand`; exit codes
  flow through typed errors matched by `errors.As` in `root.go`'s `Execute()`;
  interactive prompts are hand-rolled `bufio` loops gated by the `IsStdinTTY`
  func-variable, with a non-TTY fail-fast (init.go's `handleNoMarkerR13`).
- **The read-shaped `vault.Provider` abstraction must not be bent.** `Provider`
  is `Name`/`Kind`/`Resolve`/`Close` plus the read-shaped `BatchResolver`; the
  net-new management surface lives beside it, never inside it.
- **BurntSushi/toml has no format-preserving edit path.** A struct round-trip is
  a full-file rewrite that drops comments and unknown keys, so config authoring
  needs a surgical table-level insert, not a re-marshal.
- **No new heavy dependencies.** No TUI/prompt library (survey/bubbletea/promptui
  are all absent); the wizard builds on stdlib `bufio` and `golang.org/x/term`,
  already in `go.mod`.

## Considered Options

The five decision questions below were each evaluated with structured
trade-off analysis. Each renders its context, key assumptions, the chosen
approach with rationale, and the alternatives with their rejection reasons.

### Decision 1: Wizard control-flow and resume architecture

`niwa onboard` covers two setups and must re-run sensibly (R15): go straight to
verification against a completed setup, resume at the first incomplete step
against a partial one, and re-mint/re-store after a topology change. AC-20's
partial-resume fixture depends on whatever resume-state representation this
settles. Several codebase facts are load-bearing. There's no wizard or
state-machine precedent to build on. `state.json` (`.niwa/instance.json`) lives
*inside* the atomically-replaced `.niwa/` snapshot and survives `niwa apply`
only via the closed-set `preserveInstanceState` carry-over. A separate precedent
exists for genuinely machine-local files: the XDG `~/.config/niwa/` directory
(`GlobalConfigPath`, `GlobalConfigDir`, `OverlayDir`). And R20 requires capturing
a minted client-secret's id across runs so a later run can best-effort revoke a
superseded or orphaned one — an id that cannot be reconstructed from the REST
surface R19 models (no list-client-secrets endpoint or correlator field is
specified anywhere).

**Key assumptions:**

- The unresolved REST shape for the Universal-Auth-attach and environment-grant
  landing checks (whether they ride the GET-identity response or need a separate
  read) changes only the number and shape of the probe's calls, not the
  probe-not-cursor architecture. If the real API exposes no read surface for one
  of these, the wizard falls back to trusting the operator's claim for that
  sub-step — a narrowing of R6's guarantee, not a change to this decision. Verify
  early in implementation.
- Every team-phase landing check (identity, UA attach, grant, folders) is a
  world-state probe, not an internally remembered cursor.

#### Chosen: Hybrid — stateless step resume, one persisted non-secret record scoped to R20

Every resume, skip, and landing decision is answered by a world-state probe,
recomputed from scratch on every invocation, reusing the exact read paths the
rest of the system already uses so the wizard can never disagree with what
`niwa apply` or the doctor-style checks would see:

- Setup detection (R2) and topology detection (R3) are inferred from observable
  workspace and session state, not from a remembered prior choice.
- Team-phase step landing (R6, R7, R9's plan-gate degradation, R21's re-run
  verification) is a live check per step: does the identity now expose a
  `client_id`? does the grant show up? does the folder path exist? A
  guided-dashboard wait is not a distinct persisted state — the wizard blocks
  synchronously in-process (print, wait, re-probe, continue), and if the operator
  quits and returns, the next run re-probes from scratch and finds the same
  frontier step, with no "I was waiting here" marker to go stale.
- Individual-phase sequencing (R8, R9, R10) and both wizard-end checks (R11
  individual, R21 team) reuse the credential-sync read topology and the same
  landing probes, so AC-19 (complete setup goes straight to verification) and
  AC-20 (partial setup resumes at the first incomplete step) fall out for free as
  compositions of the same probes.

The single exception: one small, non-secret, durable record — the previously
minted client-secret's id plus a "not recoverable" flag (R20's third bullet) —
is persisted because it provably cannot be reconstructed from observable state.
It lives at `~/.config/niwa/` (not on `InstanceState`), keyed by `(kind, project)`
to mirror the credential pool's own keying, shaped as a flat non-extensible
struct (`{secret_id, recoverable}` or equivalent), never a map keyed by step
name and never a generic metadata bag, with exactly one consumer: R20's
best-effort revocation. It participates in zero resume/skip decisions. It's
written via `os.OpenFile` at mode `0600` in its own directory then renamed over
the target, matching the hygiene discipline the wizard's credential paths follow,
even though the file never holds secret bytes.

The rationale ties to the decision's own near-invariant: the wizard's
remaining-work computation must never disagree with what apply would see, and
reused observable state beats private bookkeeping that can go stale. That rules
out a general persisted step machine — the PRD's own criteria (AC-9/AC-9b, R19's
seedable resource states, R11/R21's mandatory pre-completion checks) already
require a live check at every point that matters, so a cursor either duplicates
them (forfeiting its efficiency argument) or gets trusted instead of them
(recreating the silent-failure mode). R20 is the one genuinely different
question — "what happened once," not "what's left" — and no probe can answer it,
so it earns the one persisted datum, placed where its real scope (operator ×
vault-org, not per-instance-sandbox) lives.

#### Alternatives Considered

**Persisted step-state machine.** A dedicated file recording an explicit step
cursor (`team-identity-created`, `individual-minted`, ...), read at start to
resume at the first not-done step, with world reads used only to perform a step,
never to decide whether to skip it. Its case is real: it gives AC-20 the most
direct fixture (seed the file, assert the resume point), avoids re-probing
confirmed steps on every resume, and gives the "waiting on guided step N"
condition a first-class representation. Rejected because it collides with the
PRD's own acceptance criteria — AC-9/AC-9b and R19 already mandate a live,
independently-seedable check on every guided step regardless of what a cursor
claims, and R11/R21 mandate one before declaring any run complete. A cursor
layered on top either duplicates those checks or gets trusted instead of them,
recreating the exact "mistake surfaces silently at a later `niwa apply`, far from
the cause" failure the problem statement names. It also introduces real new
machinery against an explicit YAGNI pressure (a step taxonomy, a schema, a
migration story, topology-aware step invalidation for the re-run-after-topology
case) with no precedent to build on, and carries staleness and mid-write
corruption risks pure statelessness can't have by construction.

**Fully stateless re-derivation.** No bookkeeping file at all; every run
re-verifies every step by probing observable state, with landing check and
resume check being the same probe. This makes "never disagree with apply" true by
construction for 100% of decisions and fits the guided-wait/quit-and-resume shape
without inventing any "waiting" state. Rejected as the *complete* answer for
exactly one reason: R20's minted-secret revocation genuinely cannot be re-derived
from the modeled REST surface, so a literal zero-persistence reading can only
satisfy R20's letter via its degraded TTL-lapse fallback — never actually
revoking. Since R20 and D11 clearly intend best-effort revocation as the primary
behavior, a design that makes that behavior permanently unreachable is declining
to build an accepted requirement under cover of purity. The chosen hybrid keeps
every strength of this option and closes its one gap with the smallest addition.

**Hybrid homed on `InstanceState`.** Identical mechanism, but the R20 record
lives as a new field on `.niwa/instance.json`, following the existing v4
`AuthSources` precedent and reusing `preserveInstanceState`'s carry-over for
free. Rejected in favor of `~/.config/niwa/` placement because the minted secret
and the credential it seeds are scoped to the operator and the vault org, not to
any one instance sandbox. An operator who deletes and recreates an instance, or
runs `niwa onboard` from a second instance of the same workspace — both ordinary
per US-6 — would find the id invisible to the "other" instance purely because of
where it's filed, misclassifying a wrong-lookup-key bug as R20's legitimate "not
recoverable" case. `~/.config/niwa/` also keeps the record clear of
`preserveInstanceState`'s carry-over list (machinery built for a snapshot-swap
problem this record never has) and sits exactly where `niwa config set global`
already writes operator-local state.

### Decision 2: Command surface, flags, and exit-code vocabulary

`niwa onboard` must cover two setups through one wizard (R1), with confirmable/
overridable setup detection (R2) and topology (R3), a non-TTY fail-fast (R18),
and five distinct terminal outcomes a script can branch on (R16). The two
superseded designs each chose their own exit-code scheme (0/3/4/5 and 0/1/2), but
neither ships standalone, so their vocabularies are historical record, not a
constraint to reconcile. The repo's convention (`root.go`'s `Execute()`) is a
per-command-family `errors.As` dispatch — `*sessionattach.ExitCodeError` and
`*workspace.InitConflictError` each carry their own small integers scoped to
their commands; there is no global exit-code registry, and code 1 is the one
value with cross-command meaning (generic unclassified error).

**Key assumptions:**

- A flag-conflict usage error (e.g. `--team` and `--individual` together) needs
  no dedicated code, by analogy to `--overlay`/`--no-overlay` (plain exit 1)
  rather than `--bootstrap`/`--no-bootstrap` (a PRD-mandated code). If wrong, a
  later code is a mechanical addition.
- The five R16 outcomes plus the R18 precondition are the complete set needing
  dedicated codes for v1; a mid-wizard Ctrl-C folds into decline/abort per AC-26.
- `errors.As` dispatch means two typed-error families can reuse the same integer
  without ambiguity, so a self-consistent onboard vocabulary suffices — no need
  to hunt for globally-unclaimed integers.

#### Chosen: Single command, boolean override-flag pairs, fresh sequential exit codes

One cobra command, `internal/cli/onboard.go`, registered exactly like every
other (`var onboardCmd`, flags in `init()`, `rootCmd.AddCommand(onboardCmd)`). No
subcommands. Flags:

- `--team` / `--individual` — mutually exclusive booleans overriding R2's
  inferred setup, mirroring `init.go`'s `--bootstrap`/`--no-bootstrap` shape
  exactly (a plain `if teamFlag && individualFlag { return err }`, not cobra's
  `MarkFlagsMutuallyExclusive`).
- `--same-login` / `--split-login` — mutually exclusive booleans overriding R3's
  inferred topology, meaningful only within the individual path; combining either
  with `--team` is the same plain-error usage conflict.
- `--json` — single `BoolVar` following `list.go`'s
  `json.NewEncoder(cmd.OutOrStdout())`. The terminal envelope is
  `{"status": <string tied 1:1 to the exit code>, "setup": "team"|"individual",
  "exit_code": <int>, "detail": "<non-secret message>"}` plus setup-specific
  non-secret identifiers (identity id, `client_id`, client-secret id, store
  target, kind/project) — never a secret value, at any exit path.
- `--accept-api-url` — single `BoolVar`; the explicit non-interactive acknowledgment
  of a non-default `api_url` (Decision 3 step 0, Decision 4). In a scripted/non-TTY
  run there is no operator to confirm the resolved endpoint the operator's bearer
  will be sent to, so a non-default `api_url` fails fast (exit 2) unless this flag is
  passed; it never covers a non-`https` value, which is rejected unconditionally in
  every mode. In an interactive run the flag is optional (it pre-acknowledges and
  skips the entry gate's prompt).
- `--no-progress` is inherited from root; no onboard-specific progress flag.

Non-TTY behavior (R18): when stdin is not a TTY and neither the setup override
nor (when relevant) the topology override supplies the needed input, the command
fails fast with a fixed diagnostic before any state change — the same shape as
`init.go`'s non-TTY path. The same fail-fast (exit 2) covers a non-default `api_url`
in a non-TTY run without `--accept-api-url`: it must never be silently accepted with
no operator to confirm it. Note both are distinct from a missing authenticated
session, which is an in-scope pause (R22/D3-session), not a fail-fast.

Exit codes are a new `onboard.ExitCodeError{Code int, Msg string}` (same two-field
shape as `sessionattach.ExitCodeError`), with a third `errors.As` arm in
`Execute()`:

| Code | Outcome | Requirement |
|---|---|---|
| 0 | success | — |
| 1 | *(reserved — generic/unclassified error, unchanged repo-wide; not assigned to any onboard outcome)* | — |
| 2 | non-interactive precondition fail-fast | R18 / AC-30 |
| 3 | operator decline / abort mid-wizard | R2 / AC-4, AC-32 |
| 4 | authentication failure | R9 / AC-14 |
| 5 | storage-write failure | R8 step 4 / AC-34 |
| 6 | wizard-end verification failure | R11 / AC-18b |

Ordering follows the wizard's own pipeline (precondition → confirm/decline →
mint+authenticate → store → verify), a more useful mnemonic than the PRD's prose
order. R1 forecloses subcommands as a matter of requirements; a single command is
also the only shape that satisfies R2's mechanism (the wizard, not the operator's
command choice, presents the inferred setup for confirmation). Reserving 1 as the
untyped fallback avoids blurring the one value with existing cross-command
meaning.

#### Alternatives Considered

**Two subcommands (`onboard team` / `onboard individual`).** Rejected because R1
explicitly forbids a per-role split, and independently because it pushes R2's
inference back onto the operator's command choice rather than the wizard's own
confirmable branch, undermining the discovery-burden removal that is US-1/US-2's
whole point. It would also duplicate the non-TTY fail-fast and `--json` envelope
across two definitions.

**Single enum flag (`--setup=team|individual`, `--topology=...`).** Rejected in
favor of boolean pairs because the nearer, directly-cited precedent
(`--bootstrap`/`--no-bootstrap`) is a boolean pair and both onboard choices are
inherently binary, so the enum's generality buys nothing and reads less
consistently alongside `init`.

**Inheriting a prior's exit-code scheme wholesale (0/3/4/5 or 0/1/2).** Rejected
because neither prior ships standalone, the wizard's outcome set doesn't map 1:1
onto either, and literal reuse of either scheme's numbers for different meanings
(provision's "4" is target-env-unreadable; onboard's would be authentication
failure) would actively confuse anyone who worked on the superseded designs, with
no offsetting benefit.

**A dedicated exit code for the flag mutual-exclusion conflict.** Rejected for a
plain error (generic exit 1), matching `--overlay`/`--no-overlay`; a dedicated
code is only warranted when a requirement demands one, and none does here.

### Decision 3: Detection mechanism and prompt UX

The PRD already settles at the requirements level that both setup detection (R2)
and topology detection (R3) are inferred where possible and always confirmable or
overridable. What's deferred to design is the *mechanism* — which signals, in
what order, and how to avoid network calls the flow doesn't already need — and
the prompt plumbing. A structural fact shapes it: neither niwa's config structs
nor Infisical project UUIDs encode org membership anywhere, so same-login vs
split-login can't be fully resolved from static config; some live signal is
unavoidable. Separately, the team-identity GET (reading the shared identity's
`client_id`) is already mandatory in two places regardless of branch — R8 step 1
(the individual phase's first automated step) and R21 (team re-run verification).
That makes it possible to hang detection off a call the flow needs anyway.

**Key assumptions:**

- The operator's active `infisical` session org scope is either locally
  introspectable (`infisical login status`) or inferable from the *failure shape*
  (not-found vs unauthorized-for-org) of the identity-GET performed with the
  current session. If the CLI gives no distinguishable signal, the fallback still
  uses that one call but treats any failure as "assume split-login, confirm,"
  costing no extra round trip — weaker precision, same architecture. Verify
  against the real CLI/REST error surface in implementation.
- The setup and topology override flags from Decision 2 exist.
- "Infer, then confirm, never silent" is settled by the PRD and not re-litigated
  here.

#### Chosen: Layered local-first detection with one reused live call, small internal prompt kit

Detection funnel, cheapest signal first:

0. **`api_url` trust gate (before any bearer-carrying call, including detection):**
   the very first thing the wizard does after `resolveAPIURL` — ahead of the
   detection call in step 2, which is itself the first call that carries the
   operator's live session bearer — is validate the resolved `api_url`. A non-`https`
   value is rejected unconditionally before any request is built. A non-default
   `https` value is surfaced for explicit acknowledgment: in an interactive run,
   through the prompt kit as its own gate at wizard entry (not folded into the later
   setup/topology confirm, which depends on detection results that don't exist yet);
   in a non-TTY/scripted run, only via the explicit `--accept-api-url` override
   (Decision 2), otherwise a fail-fast (exit 2). This gate is decoupled from and
   strictly precedes the setup/topology confirm, because the call that confirm reads
   its inputs from is the same bearer-carrying call the gate protects.
1. **Free (already-parsed config, zero extra I/O):** if the team config declares
   no `[vault.provider]`/`[vault.providers.*]` at all (`VaultRegistry.IsEmpty()`),
   infer team setup immediately — there's no project id yet to check anything
   against. No network call for this branch.
2. **Live but reused, not detection-only:** if the team vault block *is* declared,
   perform the GET-identity call R8 step 1 needs anyway (and R21 needs on any team
   re-run) with the operator's active session — this is the first bearer-carrying
   call, and it runs only after step 0's gate has passed — and interpret it once for
   three
   purposes: (a) not-found → team setup incomplete, route to team; (b) found →
   team phase complete, so branch on the cheap local signal of whether the
   personal overlay already declares `[global.vault.provider]` and a credential
   already resolves (R15: if yes, straight to R11 verification; if no, individual
   setup); (c) the *shape* of a failure on this same call (generic not-found vs
   org-scope/unauthorized) doubles as the topology signal.
3. **Topology piggybacks on step 2's call:** success with the current session and
   no org-scope error → infer same-login. An org-mismatch/unauthorized failure
   (not a generic transport failure) → infer split-login and flag the coming login
   switch (R4). When the personal overlay doesn't exist yet, default the prior
   toward split-login (the cross-org case is the whole reason credential-sync
   exists) — but only as a prior, always surfaced as a named, overridable prompt
   ("Detected: split-login — your current session doesn't yet reach the team
   vault's org. Continue? [Y/n]"), never silent.

No separate network call exists purely to answer "team or individual" or
"same-login or split-login"; both ride the call the flow performs regardless.

Prompt UX introduces one small internal prompt kit (living in `internal/onboard`)
exposing exactly three primitives:

- `Confirm(prompt string, defaultYes bool) (bool, error)` — yes/no with a stated
  default, generalizing `promptBootstrap`'s Y/n-with-default-on-Enter loop.
- `Select(prompt string, options []Option) (chosen string, err error)` — numbered
  one-of-N, re-prompting on invalid input; net-new.
- `Pause(prompt string) error` — read-and-discard one line, used only to gate on
  an external action (a dashboard step, a login switch); validates nothing.

All three share one internal re-prompt/EOF loop generalized from
`promptBootstrap`, and all three are gated by a single TTY-or-override check
performed once at wizard entry (satisfying R18), not per-primitive. Every
config-sourced or response-sourced string the kit displays (guided-instruction
tokens, the `api_url` line, identity name, environment slug) passes through one
shared display-sanitizer first: it strips or escapes control and non-printable
bytes (so a hostile team-config value can't emit ANSI cursor moves or a carriage
return to redraw the confirm line) and renders any host in an ASCII/punycode-
normalized form (so a homoglyph like a Cyrillic `о` in a lookalike host is
visible). This is what makes the `api_url` gate's display-based defense real —
without it, a last-look prompt can be spoofed. The two
existing helpers (`ReadConfirmation`, `promptBootstrap`) are left untouched —
neither's semantics is a strict subset of the new primitives, and touching either
risks regressing `destroy` and `init --bootstrap` for no onboard benefit.

This minimizes network cost by construction (detection is nearly free for a
from-scratch team setup and adds zero net calls otherwise), keeps both inferences
confirmable, and resolves an ordering hazard the heavy alternative hits: R22
requires the session-login pause *before* any team-vault-side call, and reusing
that same call for detection is trivially compatible with that ordering.

#### Alternatives Considered

**Ask-heavy, minimal inference, copy-adapt `promptBootstrap` per question, no new
package.** Satisfies "build on the existing helpers" most literally and is
requirements-compliant (asking directly still allows a confirm/override framing).
Rejected because the task's own primitive list guarantees at least three distinct
interaction shapes (setup pick, topology pick, pause-until-Enter), so
copy-pasting the loop per shape puts the same re-prompt/EOF logic in three-plus
places — any future fix (a TTY-detach edge case) has to be applied at every call
site.

**Exhaustive upfront doctor-depth probe, silent-leaning topology, external
prompt library.** Front-loads a composite check (doctor-depth read plus live
project-to-org lookups for both projects) before branching, and folds topology
into a post-hoc summary rather than a dedicated confirm gate. Rejected on two
independent requirements grounds: it fails R3's "operator MUST confirm or
override" by presenting topology after the fact, and it needs a new external TUI
library the drivers explicitly rule out. It also creates a chicken-and-egg
ordering problem against R22 (the composite probe wants session-scoped answers
before R22's session-login pause has necessarily run).

### Decision 4: Infisical interaction architecture

The wizard needs a genuinely new REST client — read an identity's `client_id`,
mint a fresh client secret, verify it via a universal-auth login exchange, revoke
a client secret — authenticating as the operator's own session, never an admin
token niwa custodies (R5/R8/D4). None of this exists today: no `internal/provision`
package was ever built from the superseded provision design, and the one package
that talks to Infisical (`internal/vault/infisical`) implements exactly two
things — CLI subprocess delegation for `infisical export` behind the read-shaped
`vault.Provider`, and one REST call (`auth.go`'s `Authenticate`, a universal-auth
login for an *already-resolved* credential). `vault.Provider` is deliberately
read-shaped and the PRD forbids bending it into a management interface, so this
decision is about where the net-new management surface lives *relative to* that
package, not whether it can live inside `Provider` (it can't).

**Key assumptions:**

- The REST paths for read-identity, mint-client-secret, and revoke are as
  recorded in the superseded provision design
  (`GET /v1/auth/universal-auth/identities/{id}`,
  `POST /v1/auth/universal-auth/identities/{id}/client-secrets`, and a `DELETE` on
  the client-secret's id), described as "almost certainly correct by convention"
  in prior research but not re-fetched against current docs. If wrong, only
  request construction changes; the package-placement choice is insensitive to the
  exact path strings. Re-fetch and confirm early in implementation.
- `infisical login status` (or equivalent) is the primary org-context detection
  signal, its exact output shape unconfirmed. If unparseable or absent, org
  detection falls back to classifying the mint/read call's own error (a 403 /
  wrong-org signal), which the wizard handles regardless (R16) — a less proactive
  UX, not a dead end.
- `api_url` is sourced the same way R10's stored-credential `api_url` is: an
  optional workspace-vault-provider-config value defaulting to the Infisical cloud
  endpoint, never a wizard-specific flag (R14 forbids baked-in constants).

#### Chosen: Extend `internal/vault/infisical` in place

Add the net-new management REST client — `ReadIdentity`, `MintClientSecret`,
`RevokeClientSecret` — as plain package-level functions in a new
`internal/vault/infisical/management.go`, alongside `infisical.go` (CLI export /
`vault.Provider`), `subprocess.go` (the `commander` abstraction), and `auth.go`
(the existing `Authenticate` login exchange). None are `Provider` methods; none
are registered with `vault.DefaultRegistry`; none are reached through
`BatchResolver`. `vault.Provider` and its one optional extension are untouched —
the constraint that this interface must never become a management shape costs
nothing here because the new code never touches it.

- **Mint-time verification (R9)** is a two-hop proof — authenticate the minted
  pair, then read the target environment with the resulting token — and both hops
  stay in this package. The auth hop calls `authenticateHTTP` directly (the pair
  form, `authenticateHTTP(ctx, apiURL, clientID, clientSecret)`, not the map-taking
  `Authenticate`; if the map form is used instead, the `entry` map is assembled
  from the minted pair) as an ordinary same-package function — zero new import edge,
  no risk of the verification exchange diverging from the one HTTP client config,
  timeout, and redaction sequencing already tested and trusted. The read hop is a
  new header-carrying REST secrets-read function in `management.go` (e.g.
  `ReadEnvironmentSecrets`) that carries the short-lived access token from the auth
  hop in the `Authorization` header and reads the target environment. It does
  **not** reuse `infisical export --token`, which would put the token on argv and
  violate R17/AC-28 — the read hop must be REST-with-header for exactly the same
  no-argv reason the mint calls are. Homing both hops here is strictly cheaper than
  either alternative, which would import these functions (or duplicate the
  R17-critical secret-scrubbing surface forever).
- **Session/org-context detection** is added as helpers in the same package (e.g.
  `session.go`), reusing the existing `commander` interface from `subprocess.go`
  and its subprocess-hygiene invariants (`cmd.Env = nil`, full stdout/stderr
  capture, `vault.ScrubStderr`) to shell to `infisical user get token` / read
  `INFISICAL_TOKEN` / parse `infisical login status`. Because the `login status`
  shape is unconfirmed, this detection is a proactive UX aid for the topology
  prompt, not the safety-critical gate — the authoritative "wrong org" signal is
  always the classified response of the actual privileged call (a 403 on
  `ReadIdentity`, mapped to the authentication-failure exit code). Missing-session
  detection (R22) uses the same plumbing before walking the operator through
  `infisical login`.
- **`api_url` sourcing and trust guard.** `resolveAPIURL` extends `auth.go`'s
  existing `defaultAPIURL` constant and `entry["api_url"]` pattern with one
  package-level precedence function: an explicit config-declared value wins;
  otherwise an environment test override (`NIWA_INFISICAL_API_URL`, mirroring the
  proven `NIWA_GITHUB_API_URL` pattern in `internal/github/client.go`); otherwise
  the cloud default. Both `Authenticate` and the new management calls consume this
  one function, so there's exactly one precedence rule in the package. Because the
  config-declared value comes from the shared workspace-config repo and the
  management calls carry the operator's live session bearer to whatever host it
  names, a non-default `api_url` is treated as security-sensitive input, not a
  neutral endpoint field. The guard is its **own gate at wizard entry**, run right
  after `resolveAPIURL` and **before any bearer-carrying call — including the
  detection GET-identity of Decision 3, which is itself the first call that carries
  the bearer** (Decision 3 step 0). It is decoupled from the setup/topology confirm:
  that confirm reads its inputs from the detection call, so it necessarily runs
  after the bearer is already in flight and cannot be the point that protects it.
  The gate has two rules. First, a non-`https` `api_url` is an **unconditional hard
  reject** in every mode, before any request is built — never "warn and proceed,"
  since a warning in a scripted run is silent acceptance. Second, a non-default
  `https` `api_url` requires explicit acknowledgment: in an interactive run the
  wizard displays it (through the display-sanitized prompt kit, R1) and requires a
  confirm; in a non-TTY/scripted run, where no operator is watching, it fails fast
  (exit 2) unless `--accept-api-url` is supplied. It is never silently accepted. R14
  still permits a workspace to declare a non-default `api_url` for a self-hosted
  instance — the guard makes that declaration visible, scheme-checked, and
  explicitly acknowledged, it does not forbid it.
- **Two mitigations** close the one legitimate objection (AC-10 audit optics): a
  package-doc-comment rewrite in `infisical.go` describing the management surface
  as a second, distinct purpose from `Provider`/`Resolve` (restating that both
  authenticate as the operator's own session and neither is reachable through
  `vault.Provider`), and a small static test (walking `internal/onboard`'s
  team-phase call sites, or an equivalent lint rule) that fails if either
  mutating management call (`MintClientSecret`/`RevokeClientSecret`) is called
  from team-phase code — giving this choice the call-graph audit backstop a
  dedicated package would provide "for free," at a fraction of the structural
  cost. `ReadIdentity` is deliberately outside the ban: the team path's own
  landing checks (AC-9's "identity now exposes a `client_id`" and the R21
  sweep) require that read-only probe, so the lint bans mutation, not reads.

The rationale: R9 reuse is strictly cheapest here (a same-package call versus an
import edge under both competitors). `vault.Provider` is untouched and unendangered
under all three placements, so the interface-bending risk costs nothing extra
here. Keeping the code here preserves a coherent organizing principle — one
package holds every wire format niwa speaks to Infisical, CLI argv shapes
(`subprocess.go`) and REST JSON shapes (`auth.go`, now the management calls) both
— which the wizard-internal alternative would scatter into a package whose job
(wizard UX) has nothing to do with HTTP wire formats. And D8's own "fold in place"
precedent, checked against the one superseded mechanic that actually shipped
(#199's contract validator folded as unexported functions into
`internal/workspace/credentialsync.go`), favors folding into an existing,
already-relevant package. The accepted cost is honest: reviewers need the doc
comment plus the lint test — not package topology alone — to confirm the AC-10
boundary holds.

#### Alternatives Considered

**New sibling package (e.g. `internal/infisical/admin`).** Would give a
package-boundary static backstop for AC-10's "no management calls on the team
path" and keep the existing doc comment untouched. Rejected because R9-verification
reuse still requires importing `internal/vault/infisical` anyway (so the
"separation" is code-location isolation, not dependency isolation), and it's a
package built for exactly one confirmed consumer with no second caller in sight —
a YAGNI cost paid for an audit property that the load-bearing R19 request-recorder
assertion plus the cheap static-lint mitigation already deliver without a new
package.

**Wizard-internal client, no independent package identity.** All management REST
code and session detection written directly inside `internal/onboard`. Minimizes
packages touched and matches a superficial reading of D8's YAGNI stance most
directly. Rejected because it produces a mixed-concern package (wizard
orchestration UX mixed with an HTTP client and session-detection plumbing that has
no natural relationship to step-sequencing), still pays an import cost (one edge
into `internal/vault/infisical` for `Authenticate` reuse — not zero, as under the
chosen option), and creates a real discoverability gap between the existing
login-exchange code and the new mint/verify code once they live in unrelated-purpose
packages. Its strongest argument (D8's "fold as internal building blocks") is
better satisfied by folding into the existing Infisical-interaction package than
into the wizard's own, once the `credentialsync.go` precedent is applied to
itself.

### Decision 5: Config authoring mechanics

`niwa onboard` writes configuration in three places that behave nothing alike:
the operator's personal-overlay repo (a real git clone, entirely the operator's
account), the operator-local config file (not a git repo at all), and the team's
workspace source repo (a shared, review-gated object the operator has merge access
to). R12 and R22 fix *which* logical block lands in which; R13/AC-22 fix that the
wizard refuses to write when the credential-sync provider's own `(kind, project)`
would be bootstrapped from the pool. Undecided is the *mechanics*: how to edit an
existing file without destroying what the operator put there, and whether the
wizard takes git actions or only instructs. The constraint with teeth: `.niwa/` is
an atomically-replaced snapshot, so any wizard write there is silently discarded
by the next `niwa apply`. Two codebase facts narrow the space. BurntSushi/toml has
no format-preserving edit path — a struct round-trip is a full-file rewrite.
And the one precedent for wizard-authored config reaching a durable source
(`niwa init --bootstrap`'s `RunBootstrap`) commits a scaffold to a branch and
deliberately never pushes it (`TestRunBootstrap_R24_NoPush`); the operator's own
action is what gets it upstream.

**Key assumptions:**

- The personal-overlay repo, once registered, is available as a real local git
  clone with a `.git` directory (per `OverlayDir`,
  `$XDG_CONFIG_HOME/niwa/overlays/<org>-<repo>/`), not a bare fetched copy. If it's
  a bare copy with no history, the "commit locally, let the operator push"
  mechanic needs a session/worktree wrapper comparable to bootstrap's
  `CreateSessionFunc`.
- The `[global.vault.provider]` table is fully wizard-owned once R12/R22 assign
  it: the wizard replaces the *whole table* it finds (header through the next
  top-level table or EOF), not key-by-key. A hand-edited unrecognized key inside
  that specific table is not preserved across wizard runs; comments and other
  tables elsewhere are untouched.
- "Print a snippet and instruct" for the team-config repo means no local git write
  at all, per the Out of Scope wording. If the design later wants an uncommitted
  working-tree edit in the operator's checked-out team-config repo, that's a
  smaller variant of the same "instruct, don't act" posture and doesn't change the
  mechanism.

#### Chosen: Per-site mechanism — surgical table-level TOML insertion, split three ways by git posture

Three distinct behaviors for three distinct write sites, unified by one shared
editing primitive (table-header-aware insertion with a pre-write landing check):

1. **Personal-overlay repo** (`niwa.toml` at the overlay root — the
   `[global.vault.provider]` declaration and any `[workspaces.<name>.env.secrets]`
   per-workspace personal secrets, R12 first bullet). The wizard reads the file if
   it exists (or starts empty if the repo/file is being scaffolded per R22). A
   landing check runs first: does a `[global.vault.provider]` table already exist
   with the exact values about to be written? If yes, no-op — this is what makes
   re-runs idempotent without ever producing a second table (a duplicate top-level
   key is a hard TOML parse error, not just noise). If absent, the wizard appends
   it preceded by a blank line, leaving every byte before it untouched. If present
   with different values (a re-run after a topology or project change), the wizard
   replaces only the span from that table's header line to the next top-level
   header (or EOF) — a whole-table replace, not a merge. Everything else (comments,
   other tables, unrelated `[workspaces.*]` blocks) is copied through verbatim. The
   wizard commits locally with no custom author identity (mirroring bootstrap's
   `TestRunBootstrap_R18_NoAuthorArgNoAuthorEnv`) and does **not** push; it reports
   the commit and tells the operator to `git push`. This mirrors `RunBootstrap`
   exactly, including the "repo doesn't exist yet" case. A low-governance repo (the
   operator's own account, no review gate) gets one consistent habit ("onboard
   always leaves me a commit to push") across both scaffold and edit-existing
   cases.
2. **Local overlay pointer** (`~/.config/niwa/config.toml`'s `[global_config]`
   block, set by `niwa config set global <slug>` — R12 second bullet). Not a git
   repo; the wizard writes it directly — no commit/push posture, because there's no
   upstream to sync to. If an existing `niwa config set` code path already does
   this write, the wizard reuses it rather than inventing a second writer.
3. **Team's workspace source repo** (`[vault.provider]`, `[env.secrets]` refs,
   optional `[vault].team_only` / `[workspace].vault_scope` — R12 third bullet).
   The wizard makes **no git write of any kind**. It computes the exact TOML
   snippet (same table-aware generation as case 1, so the content is still produced
   by niwa, not hand-typed), prints it naming the destination file, and stops. The
   operator carries it into their own edit/PR/review flow. This is the only posture
   consistent with "requires the operator's own review/merge access" — a
   review-gated repo is not one the wizard should create commits against, even
   unpushed ones, because an unpushed local commit in a shared clone is easy to
   mistake for a landed change.

All three share one editing primitive, but only sites 1 and 3 use its *write*
path; site 3 uses only its *render* path (produce the snippet, skip the file I/O),
and site 2 doesn't use it at all (a flat pointer field, not a table the wizard
owns). The mechanism ties to its drivers: only surgical table-scoped insertion
preserves operator content given BurntSushi's lack of a round-trip encode; both
write sites target repos, never the snapshot; the per-site posture follows the
PRD's own different treatment of the two repos; and the landing check is
inseparable from the insertion because a duplicate top-level table is a parse
error, not just noise.

#### Alternatives Considered

**Struct re-marshal.** Parse the overlay file into the existing
`WorkspaceOverlay`/`GlobalOverride` structs, mutate the `Vault` field, re-encode
the whole struct back to TOML. Rejected because BurntSushi's `Encode` (the only
TOML writer in the tree) produces a fresh serialization with no memory of the
original's comments, key ordering, or blank-line structure — a "targeted mutation"
that is, in practice, a full-file overwrite of every comment and human-added block.
It also silently drops (or errors on) any top-level key the struct doesn't model,
which real overlay files are free to carry (the schema is intentionally permissive).
This is exactly the "clobbers comments/formatting/unknown keys" cost, not a
hypothetical one — and it would require *adding* a marshal-back path that doesn't
exist today, carrying the same net-new-code cost as the surgical helper while
delivering a strictly worse fidelity guarantee.

**Guided templates for every site, including the personal overlay.** Never write
any file directly; always print the block and require the operator to paste it,
uniformly. Rejected for the overlay and local-pointer sites specifically: the
overlay carries no review gate and no governance reason to force manual authoring,
and R8's "automate every mechanical step" plus US-2's "so I never learn the vault
path, the key prefix, or the body format" extend the same logic from the credential
body to this config block — forcing hand-typed TOML reintroduces the identical
exact-shape transcription risk the feature exists to eliminate. It would also still
need a landing check to detect whether the manual paste succeeded, so it doesn't
even avoid the idempotence-check cost. This alternative is *right* for the
team-config site (case 3 adopts it there), but wrong as a uniform rule.

**One uniform commit/push posture across all repo writes.** Either "always commit
and push for the operator" or "never touch git anywhere." Rejected as a blanket
rule because the PRD text already draws the line between the two repos differently:
R22 describes the overlay case ending in "a local commit the operator pushes,"
while Out of Scope forbids even that much for team-config ("does not push it for
them"). Uniformly applying the overlay's commit-locally behavior to team-config
puts the wizard in the business of committing to a repo it has no review standing
in; uniformly applying team-config's print-only behavior to the overlay abandons
R22's already-settled scaffold behavior and needlessly demotes the one write site
where "commit, don't push" is both safe and established.

## Decision Outcome

**Chosen: 1-hybrid + 2-single-command + 3-layered-detection + 4-extend-in-place +
5-per-site-config.**

The five choices compose into one wizard with a single spine: `niwa onboard` is
one cobra command (`internal/cli/onboard.go`) that opens a single TTY gate at
entry, then runs a per-step loop where every step is `(landing check →
execute-or-guide → re-check)`. There's no persisted step cursor. Every "what's
left" question is answered by re-probing observable world state, so a re-run, a
resume-after-interruption, and a fresh run are the same code path finding
different frontiers. The one thing the wizard remembers between runs is a single
non-secret record at `~/.config/niwa/` — the last minted client-secret id, keyed
`(kind, project)` — consumed only by R20 revocation and touching zero resume
decisions.

The wizard starts by establishing preconditions (R22): if there's no authenticated
`infisical` session it walks the operator through `infisical login` as an in-scope
pause (not a fail-fast); if the personal-overlay pointer is unregistered it
registers it (`niwa config set global`, operator-local), and if the overlay repo
doesn't exist it scaffolds the config locally and guides the operator to create
and push it. Before any call that carries the operator's bearer, it runs the
entry-time api_url gate (Decision 3 step 0): `resolveAPIURL`, then hard-reject a
non-`https` value and require an interactive confirm or `--accept-api-url` (else
exit 2) for a non-default `https` one — this precedes detection precisely because
the detection call is itself the first bearer-carrying call. Then it detects the
setup layered local-first: if the team config declares no vault provider at all,
it's the team setup (free, no network); if a provider is declared, it fires the one
identity-GET the flow needs anyway and reads the result three ways — not-found
routes to team, found-plus-resolving-credential
routes straight to R11 verification, found-plus-no-credential routes to the
individual setup. The inferred setup is always shown for confirmation (via the
prompt kit's `Confirm`/`Select`) before any state changes, and `--team` /
`--individual` override it.

**Team setup** runs each step as a landing-check-guarded unit. Folder/secret-path
creation is automated by delegating to `infisical secrets folders create`.
Identity creation, Universal Auth attach, and environment read grant degrade to
guided dashboard steps: the wizard prints exactly what to create, where, and with
what settings (the identity name, the auth method, the target environment slug —
all sourced from config, never baked in), waits via `Pause`, then verifies the
step landed (for identity, that it now exposes a `client_id`) before continuing. A
failed landing check does not advance (AC-9b); a plan-gated step emits guided
instructions rather than a raw provider error, waits, and resumes (R7). No
team-phase step drives an identity/org/project management REST endpoint with the
operator's session JWT (AC-10, enforced by the request recorder and the static
call-site lint test).

**Individual setup** runs the automated mint pipeline through the new
`management.go` functions. It reads the identity's `client_id` (`ReadIdentity`,
never creating an identity), mints a fresh secret (`MintClientSecret`, capturing
the returned secret id for R20), and — before storing — runs R9's two-hop proof
against the minted pair: a real universal-auth login exchange (same-package
`authenticateHTTP`) followed by a target-environment read that carries the
resulting token in a header (`ReadEnvironmentSecrets`, never `infisical export
--token`, which would put the token on argv). A store is never attempted against an
unverified pair. On success it assembles the credential body — every interpolated
field (`client_id`/`client_secret` from the mint response, `project`/`api_url` from
config) TOML-encoded before embedding, so a hostile character can't break the body
or inject a key — and stores it via `infisical secrets set` fed over stdin (never
argv) at `/niwa/provider-auth/<kind>`, key `p-<project-uuid>` (the project uuid
verbatim, no
case-folding, `p-` prepended), with `version = "1"`, `client_id`, `client_secret`,
and `api_url` only when non-default. In the split-login topology, exactly one login
pause sits between the mint (against the workspace-vault org) and the store (into
the personal-overlay vault); in same-login, zero pauses. The self-referential guard
(R13) refuses to write when the credential-sync provider's own `(kind, project)`
would be bootstrapped from the pool, before any write.

**Both setups end in verification, and the two setups verify different things.**
The individual setup runs the doctor-depth wizard-end check (R11): one
credential-sync provider opened once, the credential pool enumerated across the
three vault-registry sources (workspace overlay, team config, personal global
overlay), the sync provider's own `(kind, project)` self-excluded, and the shared
in-package `parseProviderAuthBody` validator applied to each resolved body — the
exact read topology `niwa apply` uses, so the wizard and apply can't disagree. The
team setup runs the landing-check sweep instead (R21): identity exists and exposes
a `client_id`, grant present, folders exist. R9, R11, and R21 are named and
reported distinctly. Config the wizard authors lands per-site: the overlay
declaration is a surgical table insert committed-not-pushed in the overlay repo,
the pointer is an operator-local write, and the team-config change is a rendered
snippet the operator carries into their own review flow.

Terminal outcomes route through a typed `onboard.ExitCodeError` picked up by a new
`errors.As` arm in `Execute()`: 0 success, 2 non-interactive fail-fast, 3
decline/abort, 4 authentication failure, 5 storage-write failure, 6 wizard-end
verification failure (1 stays the repo-wide untyped fallback). R20 revocation is
best-effort and never changes an exit code: a re-run that supersedes a recorded
secret revokes the prior one by its captured id; a mint-then-verify success
followed by a store failure revokes the just-minted secret before exiting code 5;
an unrecoverable prior id yields a warning and TTL-lapse.

**Rationale for the combination.** The choices reinforce each other around one
idea: the wizard should own the mechanical and exact-shape work while keeping every
privileged action on the operator's own session, and should never hold private
state that could disagree with the world. Decision 1's world-state re-derivation is
what lets Decision 3's detection be "reuse the call you already make" rather than "a
probe plus a remembered choice," and it's what makes Decision 5's per-site landing
checks the same idempotence mechanism the whole wizard already runs on. Decision
4's extend-in-place keeps mint-time verification a same-package call, which keeps
the R17 secret-hygiene surface single-sourced rather than duplicated — the same
reason Decision 5 feeds the store over stdin and Decision 1 writes its one record
at `0600`. The accepted trade-offs are named: guided dashboard steps for
identity/auth/grant (honest, given the CLI has no management verbs and the
session-JWT REST path is unconfirmed), a documentation-and-lint AC-10 backstop
rather than a compiler-enforced one, and a whole-table config replace that doesn't
preserve unknown keys inside the one wizard-owned table.

## Solution Architecture

### Component map

| Package / file | Role | New or existing |
|---|---|---|
| `internal/cli/onboard.go` | Cobra command: `var onboardCmd`, flags in `init()`, `rootCmd.AddCommand`, builds the wizard and maps its typed error | New |
| `internal/cli/root.go` | Add one `errors.As` arm for `*onboard.ExitCodeError` | Edit (one arm) |
| `internal/onboard/` | Wizard engine: setup/topology detection, the step loop, team and individual runners, verification, exit-code construction | New package |
| `internal/onboard/` prompt kit | `Confirm` / `Select` / `Pause` over one shared re-prompt/EOF loop; single TTY gate; shared display-sanitizer (control-byte strip, punycode-normalize) for all echoed config/response strings | New |
| `internal/onboard/` api_url gate | Entry-time gate after `resolveAPIURL`, before any bearer-carrying call: non-`https` hard reject, non-default confirm-or-`--accept-api-url`-or-exit-2 | New |
| `internal/onboard/` R20 record | `~/.config/niwa/` read/write helper: `{secret_id, recoverable}` keyed `(kind, project)`, `0600` open-in-dir-then-rename | New |
| `internal/onboard/` config authoring | Surgical table-level TOML insert/render helper with per-field TOML encoding of interpolated values; overlay write via `0600` temp-then-rename; per-site drivers (overlay commit-no-push, pointer direct write, team-config render-only) | New |
| `internal/vault/infisical/management.go` | `ReadIdentity`, `MintClientSecret`, `RevokeClientSecret`, `ReadEnvironmentSecrets` (the R9 read hop) — plain package-level REST funcs, operator's own bearer or the minted pair's token, all header-carried | New |
| `internal/vault/infisical/session.go` | Session/org-context detection via `commander`; missing-session check | New |
| `internal/vault/infisical/auth.go` | Existing `Authenticate` reused for R9; extended with `resolveAPIURL` precedence | Edit |
| `internal/vault/infisical/infisical.go` | Package doc rewrite: state the second (management) purpose | Edit (doc) |
| `internal/workspace/credentialsync.go`, `credentialpool.go` | Reused for R11: `pickCredentialSyncSpec`, `openCredentialSyncProvider`, `parseProviderAuthBody`, three-registry enumeration, self-exclusion | Reuse |
| `test/functional/` `infisicalFakeServer` | httptest REST double: seeding, fault injection, request recording; wired via `NIWA_INFISICAL_API_URL` | New |
| `test/functional/` `writeFakeInfisical` | Extend the CLI stub: `login`, `secrets folders create`, `secrets set`; folder + stored-body fixtures; store-write and plan-gate faults | Edit |
| `test/functional/features/onboard.feature` | `@critical` individual happy-path and TTY-decline scenarios | New |

The wizard engine imports `internal/vault/infisical` for both its CLI-delegation
surface (login, export, secrets set, folder create — net-new exported functions)
and the new management functions, keeping a single import edge for "everything the
wizard needs from Infisical." `vault.Provider`, `BatchResolver`, `Factory`, and
`Registry` require zero changes — the read/resolve path every `niwa apply` uses is
untouched.

### Key function surfaces

- `onboard.ExitCodeError{Code int, Msg string}` — same shape as
  `sessionattach.ExitCodeError`, constructed at each terminal outcome and unwrapped
  in `Execute()`.
- `infisical.ReadIdentity(ctx, apiURL, bearer secret.Value, identityID string)
  (clientID string, err error)` — GET; classifies 403/wrong-org distinctly from
  not-found for the topology signal and the authentication-failure outcome.
- `infisical.MintClientSecret(ctx, apiURL, bearer secret.Value, identityID string)
  (clientID, clientSecret secret.Value, secretID string, err error)` — POST;
  registers the minted secret on the redactor the instant it's parsed; returns the
  non-secret `secretID` for R20 capture.
- `infisical.RevokeClientSecret(ctx, apiURL, bearer secret.Value, identityID,
  secretID string) error` — DELETE; best-effort, its failure a warning.
- `infisical.ReadEnvironmentSecrets(ctx, apiURL, accessToken secret.Value,
  projectID, env, path string) error` — the R9 read hop; carries the minted pair's
  short-lived access token (from `authenticateHTTP`) in the `Authorization` header,
  reads the target environment via REST (never `infisical export --token`, which
  would put the token on argv), and returns success/failure as the read proof. Its
  response body is scrubbed by the registered redactor before any error wrapping.
- `infisical.resolveAPIURL(configVal string) string` — config → env
  (`NIWA_INFISICAL_API_URL`) → cloud default; single precedence rule consumed by
  `Authenticate` and all management calls. Its result feeds the entry-time api_url
  gate (Decision 3 step 0), which runs before any bearer-carrying call: a non-`https`
  value is hard-rejected unconditionally, and a non-default `https` value requires an
  interactive confirm or the `--accept-api-url` override (else exit 2 in a non-TTY
  run), because this value directs the operator's live session bearer.
- Store-subprocess output scrubbing: the `infisical secrets set` delegation's
  stdout/stderr passes through the same `vault.ScrubStderr` treatment
  `runInfisicalExport` already applies to the export path, so the store path has the
  same scrub-before-error discipline even though the CLI has no documented reason to
  echo a stored value.
- Prompt kit: `Confirm(prompt string, defaultYes bool) (bool, error)`,
  `Select(prompt string, opts []Option) (string, error)`, `Pause(prompt string)
  error`, over one shared loop, gated once at entry, with one shared
  display-sanitizer applied to every config/response-sourced string it prints
  (strip/escape control and non-printable bytes; ASCII/punycode-normalize any host).
- Config authoring: an insert primitive that finds a named top-level table by
  header line and replaces its span (header to next top-level header or EOF), or
  appends it after a blank line if absent; a render-only variant producing the
  snippet text for the team-config site. The overlay `niwa.toml` write uses the same
  `0600`-temp-in-dir-then-rename discipline as the R20 record (no in-place
  truncate+rewrite), and assumes a single writer — the operator's own run — so the
  landing-check→write window is not a concurrent-corruption surface. Every
  config-sourced or REST-returned value interpolated into a TOML body (the credential
  body and the overlay block) or a URL path (`secret_id` in the revoke DELETE,
  identity id in the GET) is TOML-encoded / percent-escaped / character-validated
  before embedding, so a value carrying `"`, a newline, or `]` can't inject
  structure — see the normative rule under Security Considerations.

### Data flow — team setup

1. Preconditions (R22): ensure authenticated session (pause + `infisical login` if
   absent); ensure overlay pointer registered and overlay repo scaffolded/guided.
2. Detect: team config declares no vault provider → team setup (free). Confirm via
   prompt.
3. Per step, landing-check-guarded: folder create (automated,
   `infisical secrets folders create`); identity create, UA attach, env grant
   (guided dashboard + `Pause` + re-probe). A failed check re-surfaces the
   instruction; a plan-gated step emits guided instructions and resumes.
4. Verify (R21): identity exposes `client_id`, grant present, folders exist; name
   the missing artifact on failure.
5. Any team-config change is rendered as a snippet naming its destination file; the
   operator carries it into their review flow. No management REST endpoint is ever
   called on this path (AC-10).

### Data flow — individual setup

Same-login and split-login differ only by the single login pause between mint and
store. The self-referential guard runs before any write.

```mermaid
sequenceDiagram
    participant Op as Operator (TTY)
    participant W as Wizard (internal/onboard)
    participant M as management.go (REST, operator bearer)
    participant A as auth.go authenticateHTTP + management.go ReadEnvironmentSecrets (REST)
    participant CLI as infisical CLI (stub on PATH)
    participant V as credential-sync read topology

    W->>W: TTY gate + preconditions (session, overlay) [R22]
    W->>W: resolveAPIURL then api_url gate [D3 step 0]: non-https -> hard reject; non-default -> confirm (TTY) or --accept-api-url or exit 2
    W->>M: ReadIdentity(identityID) [R8.1] (first bearer-carrying call, only after the gate)
    M-->>W: client_id  (403/wrong-org -> exit 4)
    W->>Op: Confirm setup + topology (named, overridable) [R2/R3]
    W->>M: MintClientSecret(identityID) [R8.2]
    M-->>W: client_id, client_secret, secret_id  (capture secret_id -> R20 record)
    W->>A: authenticateHTTP(minted pair) then ReadEnvironmentSecrets(token in header) [R9 two-hop]
    A-->>W: ok  (login-exchange or read-hop fail -> exit 4, no store)
    Note over W,Op: split-login only: Pause for one login switch [R4]
    W->>W: self-referential guard [R13] (violation -> refuse, no write)
    W->>CLI: infisical secrets set (body over stdin, 0600) [R8.4/R10]
    CLI-->>W: ok  (store-write fail -> revoke just-minted [R20] -> exit 5)
    W->>V: wizard-end check: 1 provider, 3 registries, self-exclude, parseProviderAuthBody [R11]
    V-->>W: resolves  (malformed/absent -> exit 6, name failing pair/source)
    W->>Op: success (exit 0); config authored per-site [R12]
```

Re-run behavior composes from the same probes: a completed setup goes straight to
the wizard-end check; a partial setup resumes at the first failing probe; a
topology change re-mints and re-stores where the new shape requires, best-effort
revoking the prior secret via the `~/.config/niwa/` record.

### Test-double architecture

Two doubles cover the two provider surfaces, split exactly as R19 divides fixture
ownership.

- **`infisicalFakeServer` (httptest, REST).** Structurally modeled on the existing
  `tarballFakeServer`. Its modeled endpoints are read-identity, mint-client-secret,
  universal-auth login, revoke (DELETE client-secret), and the environment
  secrets-read the R9 read hop (`ReadEnvironmentSecrets`) targets — so the two-hop
  mint-time verification on the `@critical` happy path runs entirely against the
  double. Per-resource `Set*` seeding for the identity, its `client_id`,
  minted-secret bodies, the target-environment secrets the read hop resolves, and
  the environment read grant, each independently present/absent/malformed. A
  `SetStatus`-style fault-injection override per fault mode: wrong-org auth failure,
  mint rejection, plan-gate response, login-exchange failure, read-hop failure, and
  revocation failure. A `Requests()` /
  `CountRequests()` log for AC-10 ("no identity/org/project management endpoint on
  the team path"), AC-13 (GET-identity and POST-client-secret recorded, no
  create-identity), AC-34 (a revoke for the just-minted id), and AC-35b (no revoke
  when no prior id). Wired into the niwa subprocess under test via
  `s.envOverrides["NIWA_INFISICAL_API_URL"] = s.infisicalFake.URL()`, consumed by
  the existing `buildEnv()` — the exact mechanism already proven for
  `NIWA_GITHUB_API_URL`.
- **Extended `writeFakeInfisical` CLI stub.** Still a shell script on `PATH` per
  R19. Extended from recognizing only `export` to also serve `login`,
  `secrets folders create`, and `secrets set`. It owns the CLI-reachable fixtures:
  the folder structure and the stored credential body the wizard-end read resolves
  through `infisical export` (both seedable present/absent/malformed), plus induced
  store-write and plan-gate failures. AC-18b's malformed-stored-body case seeds the
  stub's export response, not the REST double, because the wizard-end read follows
  R11's topology through the credential-sync provider's `infisical export` path.

The `@critical` individual-setup happy path drives both doubles with everything
seeded present and passes with no real service or login. The `@critical`
TTY-decline scenario drives the setup-confirmation decline and asserts exit 3 with
no state change. AC-20's partial-resume and AC-19's complete-setup fixtures are
combinations of the same seeding — no bespoke fixture format and no interaction
with the one persisted R20 file, which AC-33 seeds directly and AC-35b exercises by
its deliberate absence.

## Implementation Approach

Sequential, buildable and testable incrementally, feeding a single-PR plan. Each
phase names its deliverables and test surface.

**Phase 0 — Early verification of the carried assumptions (do first).** Before
building on them, confirm the three high-priority assumptions the cross-validation
flagged: (a) the Universal-Auth-attach and environment-grant landing-check REST
shapes — whether they ride the GET-identity response or need a separate read;
(b) the management REST paths (read-identity, mint, revoke, and the R9 read-hop
environment secrets-read that `ReadEnvironmentSecrets` targets), re-fetched against
current Infisical API docs rather than inherited from the superseded provision
design — the read-hop's exact path and shape is net-new to this design and must be
pinned to a header-carrying (no-argv) REST endpoint; (c) whether `infisical login
status` output is parseable for org context, or the wizard must fall back to
classifying the management call's own error. Record findings in the plan. Each has
a defined fallback (trust-the-operator-claim for a missing landing-check read;
request-construction-only change for a wrong path; assume-split-login-and-confirm
for an unparseable session status), so none blocks the build, but each changes call
count or parsing detail and should be settled before the code that depends on it
lands. Deliverable: a short verification note and any path corrections. Test
surface: none (research).

**Phase 1 — Foundation: management client + test doubles.** Add
`internal/vault/infisical/management.go` (`ReadIdentity`, `MintClientSecret`,
`RevokeClientSecret`), `session.go` (session/org detection), and the `resolveAPIURL`
extension in `auth.go` plus the `api_url` validation function it feeds (a
non-`https` value is an unconditional hard reject; a non-default value is flagged for
the entry gate that Phase 2 wires ahead of detection); rewrite the package doc comment
and add the static call-site lint test. Build `infisicalFakeServer` and extend
`writeFakeInfisical`. Deliverables: the REST client, session detection, the api_url
validation function, both doubles. Test surface: unit tests for the management funcs
against the REST double (seeding, fault injection, request recording), including the
R17 secret-hygiene assertions (redactor registration, headers-only secrets,
`secret.Errorf`); the api_url validation (non-`https` hard-rejected in every mode,
non-default flagged); the AC-10 lint test asserting it is a direct-call-site check
(the runtime recorder is the load-bearing team-path check). The store-subprocess
`ScrubStderr` treatment is asserted in Phase 5 alongside the store path it guards.

**Phase 2 — Prompt kit + detection + api_url entry gate.** Add the
`Confirm`/`Select`/`Pause` prompt kit over one shared loop with the single entry TTY
gate and the shared display-sanitizer (strip/escape control bytes, ASCII/punycode-
normalize hosts), the entry-time api_url gate wired to run right after `resolveAPIURL`
and before the detection call, and the layered local-first detection (config signal,
then the reused identity-GET, then topology from the call's success/failure shape).
Deliverables: prompt kit, display-sanitizer, api_url entry gate, detection funnel.
Test surface: unit tests for each primitive (re-prompt, EOF, default-on-Enter, TTY
gate); the display-sanitizer (a value carrying ANSI/CR/control bytes is neutralized, a
homoglyph/punycode host renders visibly); the gate ordering (the non-`https` reject
and the non-default acknowledgment both fire before any bearer-carrying call — asserted
by the REST double's request recorder showing zero requests when the gate rejects); and
detection routing across the config-empty, identity-not-found, identity-found-resolving,
and identity-found-no-credential cases plus the split-login failure-shape inference.

**Phase 3 — Command surface + exit codes.** Add `internal/cli/onboard.go` (flags
including `--accept-api-url`, `init()`, `AddCommand`), the `onboard.ExitCodeError`
type, the `Execute()` arm, and the non-TTY fail-fast. Deliverables: the command shell
wired to the wizard engine skeleton. Test surface: exit-code assertions per the table;
the non-TTY fail-fast (AC-30); the non-TTY api_url contract (a non-default `api_url`
in a non-TTY run fails fast with exit 2 unless `--accept-api-url` is supplied, and is
never silently accepted; a non-`https` value rejects regardless of the flag); flag
mutual-exclusion (plain exit 1); the `--json` envelope shape.

**Phase 4 — Team setup.** The team runner: folder-create delegation, guided
identity/UA/grant steps with landing checks, plan-gate degradation, and the R21
verification sweep. Deliverables: the team-phase step loop. Test surface: AC-8
(folder delegation fires), AC-9/AC-9b (guided text tokens, landing check blocks on
failure), AC-10 (zero management calls recorded on the team path), AC-11 (plan-gate
guided path), AC-35 (team re-run verification names the missing artifact).

**Phase 5 — Individual setup + store + R20 record.** The individual runner: mint
pipeline (read → mint → R9 verify → store), the credential-body assembly and stdin
store, the split-login single pause, the self-referential guard, and the
`~/.config/niwa/` R20 record helper with best-effort revocation on supersession and
on store failure. Deliverables: the individual-phase pipeline and the R20 record.
Test surface: AC-13 (read + mint, no create-identity), AC-14 (verify-before-store,
no store on login-exchange fail), AC-15/AC-16/AC-17 (exact stored shape, verbatim
mixed-case uuid, no human-typed fields), plus a hostile-character store-shape fixture
(a `client_id` / `api_url` carrying `"`, newline, or `]` is TOML-encoded before
embedding, so the stored body stays well-formed and injects no extra key/table),
AC-22 (self-referential refusal before write), AC-33/AC-34/AC-35b (revoke on
supersession, revoke-on-store-failure, no-revoke-when-unrecoverable) plus a
`secret_id` hostile-character fixture for the revoke DELETE path, and the R17
stdin/`0600`/no-argv assertions (AC-28/AC-29) and the `ScrubStderr` treatment of the
`secrets set` subprocess's stdout/stderr.

**Phase 6 — Config authoring.** The surgical table-insert primitive (with
per-field TOML encoding of interpolated values) and the three per-site drivers
(overlay commit-no-push using the `0600`-temp-then-rename discipline shared with the
R20 record, pointer direct write, team-config render-only), plus the "which side did
it land on" reporting. Deliverables: config authoring. Test surface: AC-24 (overlay
carries the block, `.niwa/` snapshot unchanged), AC-25 (each write states upstream vs
operator-local; team-config not committed), a hostile-character config-authoring
fixture (a `api_url` carrying `"`, newline, or `]` is TOML-encoded so it can't inject
structure into the committed overlay), the atomic-write assertion (temp-in-dir +
rename, no in-place truncate), and idempotence/whole-table-replace unit tests over
hand-authored fixtures with comments and unknown tables preserved.

**Phase 7 — Wizard-end verification + preconditions.** Wire R11 (reuse
`pickCredentialSyncSpec` / `openCredentialSyncProvider` / three-registry
enumeration / self-exclusion / `parseProviderAuthBody`), the R22 session and
overlay preconditions, and the distinct reporting of R9/R11/R21. Deliverables: the
wizard-end check and preconditions. Test surface: AC-18/AC-18b (doctor-depth check
distinct from R9, names failing pair/source; malformed body seeded via the CLI stub
export), AC-36 (missing-session pause), AC-37 (pointer registration + overlay
scaffold-and-guide, no remote repo created).

**Phase 8 — Functional `@critical` scenarios.** The `onboard.feature` file and step
definitions: the individual-setup happy path driving both doubles and the
TTY-decline path, both `@critical`, plus the re-run scenarios (AC-19/AC-20/AC-21)
composed from seeding. Deliverables: functional coverage. Test surface: the full
`@critical` gate running offline (AC-31/AC-32), AC-23 (source grep for baked-in
identifiers over the command surface).

## Security Considerations

The feature's whole reason to exist is credential handling, so the security
surface is central, not incidental. The core custody model is sound: every
privileged call rides the operator's own session, the minted-secret handling and
the stdin-fed store path meet R17's hygiene bar with a specific test plan, and the
team-phase no-management-REST boundary is enforced by both a runtime request
recorder and a static call-site lint test. One real gap surfaced in review — an
unvalidated `api_url` routing the operator's session bearer — and this design closes
it with the guard folded into Decision 4. The dimensions below are covered in
order of how much they matter here.

### Custody boundary (R5)

The hard line is that niwa never holds an admin token and never drives the
provider's management API under its own authority; every privileged step runs
against the operator's own authenticated session. The design preserves this in
mechanics, not just prose. In the team phase the one automated action is
`infisical secrets folders create`, a CLI delegation on the operator's own session
— never a management REST call. That boundary is enforced two ways: a runtime
request recorder asserts zero calls to any identity/org/project management endpoint
on the team path (AC-10), and a static call-site lint test fails if a mutating
management call (`MintClientSecret`/`RevokeClientSecret`) appears at a team-phase
call site — `ReadIdentity` stays allowed there as the read-only landing-check probe
AC-9 and the R21 sweep themselves require. The two checks are not equal, and the
design says so plainly: the static lint
is a **direct-call-site** check only — it does not catch indirect dispatch through
function values or interface methods, so no one should over-trust it. The **runtime
request recorder is the load-bearing check**: it asserts zero management-endpoint
calls on the team path regardless of how the call would have been reached, catching
actual calls that a call-site lint by construction cannot. The design is honest that
this is a documentation-and-lint-plus-runtime boundary rather than a
compiler-enforced one — Go has no visibility scoping finer than package — and the
recorder (load-bearing) plus lint (belt-and-suspenders) plus package-doc comment is
a proportionate mitigation.
In the individual phase the three management calls are real REST, but each
authenticates with the operator's own bearer and stays inside the one carve-out
tsukumogami/niwa#194 validated (mint on an existing identity, never create),
matching R5's letter and spirit.

### Credential flow and data exposure (R17)

R17's hygiene rules are carried verbatim and are the same discipline already proven
in `auth.go`'s `Authenticate` / `scrubResponseBody`, reused rather than duplicated
(Decision 4) — which is what makes them trustworthy: one tested scrubbing sequence,
not a second reimplementation that could drift. A `secret.Redactor` is attached to
the context before any mint/verify/store call, and every secret (the operator's
session bearer, the minted client secret) is registered as a `secret.Value` the
instant it's obtained. Secrets never touch argv or process env: REST calls carry
them only in `Authorization` headers, the `infisical secrets set` store path feeds
the body over stdin (never a command-line argument), and CLI subprocesses run with
`cmd.Env = nil` (inherit, never extend). Any credential file is created at mode
`0600` via `os.OpenFile` in the target's own directory then renamed over the target,
so no reader observes a world-readable or partial intermediate. Mint, verify, and
login response bodies are scrubbed by the registered redactor before any logging or
error wrapping, all errors on these paths go through `secret.Errorf`, and — added in
this review — the `infisical secrets set` subprocess's own stdout/stderr gets the
same `vault.ScrubStderr` treatment the export path already has, so the store path
carries belt-and-suspenders scrubbing even though the CLI has no documented reason
to echo a stored value. No secret value appears in any output surface (stdout,
stderr, logs, `--json`, error chains) at any exit path; a canary-based test
(AC-27) asserts it. The one git-committed artifact, the personal-overlay TOML block,
carries provider *configuration* only (kind, project, `api_url` when non-default) —
never `client_id` or `client_secret`, which live only in the vault. And the R20
record at `~/.config/niwa/` holds only the non-secret `secret_id` plus a
recoverable flag: the id is an opaque handle whose only use is a DELETE call that
itself requires the operator's bearer to succeed, so knowing it grants no
capability.

### Supply-chain trust and the `api_url` guard

Two external trust anchors exist. The `infisical` CLI on PATH is trusted exactly as
every existing `niwa apply` vault resolution already trusts it; this design adds no
new assumption there, and a compromised binary earlier on PATH is a pre-existing
whole-feature risk, not one onboarding introduces. The new CLI delegations (`login`,
`secrets folders create`, `secrets set`) resolve the `infisical` binary the same way
the existing `export` path does — through the same `commander` / `exec` lookup with
no new PATH-resolution semantics and no different working directory — so they add no
new binary-resolution surface. The second anchor is the one review found: `api_url`
is sourced from the shared workspace-config repo, and the management calls send the
operator's live session bearer — the highest-privilege credential this feature
touches, broader than the narrowly-scoped minted secret — to whatever host it names.
Without a guard, a malicious or mistaken `api_url` that slips past team-config review
silently exfiltrates that bearer to an attacker-controlled host the next time any
developer runs the wizard. It's not a zero-click remote attack (it needs an
already-compromised or carelessly-reviewed team-config repo, behind the same review
gate that protects every other team-config value), but the payoff is high and the
mechanism is silent.

The guard that closes it is an **entry-time gate**, and its ordering is
load-bearing. The detection GET-identity call (Decision 3) is itself the first call
that carries the bearer, so a guard folded into the later setup/topology confirm
would fire *after* the bearer was already in flight — it would never protect the
call it guards. The gate therefore runs on its own, right after `resolveAPIURL` and
before any bearer-carrying call, decoupled from the detection-dependent confirm
(Decision 3 step 0, Decision 4). It has two rules with no soft edges. A non-`https`
`api_url` is an **unconditional hard reject** in every mode, before any request is
built — never "warn and proceed," because a warning in a scripted run is silent
acceptance. A non-default `https` `api_url` requires explicit acknowledgment: an
interactive confirm on a TTY, or the `--accept-api-url` override in a non-TTY run,
otherwise a fail-fast (exit 2). It is never silently accepted, and the scripted path
— a CI runner or bootstrap script, where no human watches the confirm line — is
where this matters most, so it gets the strictest treatment rather than the loosest.
The display the interactive confirm relies on is itself hardened (terminal-output
safety below), so the last-look can't be spoofed. This reuses the Decision 3 prompt
kit — no new subsystem — and keeps R14's allowance for a self-hosted endpoint while
making that endpoint visible, scheme-checked, and explicitly acknowledged. No
third-party library supply-chain concern applies: the design rules out new
dependencies and builds on already-vetted stdlib plus `golang.org/x/term`.

### Injection surfaces: encode-or-validate before embedding (normative)

Argv is closed (fixed flag names plus config values, never response bodies), but two
non-argv sinks interpolate external strings into structured output, and both are
governed by a normative rule: **every REST-returned or config-sourced value embedded
into a TOML body or a URL path MUST be TOML-encoded / percent-escaped /
character-validated before embedding.** The two sinks are the stored credential body
(the `client_id`/`client_secret` from the mint response and `project`/`api_url` from
config, assembled into a TOML document and fed to `infisical secrets set`) and the
committed overlay `niwa.toml` block (`kind`, `project`, and the attacker-influenceable
`api_url` written by the surgical table insert). Because the design deliberately
hand-builds TOML by string surgery rather than trusting BurntSushi's marshaler
(Decision 5), an un-encoded field carrying `"`, a newline, or `]` could break the
body or inject an extra key or table — landing a malformed credential that fails
silently at a later `niwa apply` (the exact failure this feature exists to kill) or
injecting structure into the operator's personal repo. The same rule covers
`secret_id` and identity id before they go into the `RevokeClientSecret` DELETE path
and the `ReadIdentity` GET path. Hostile-character fixtures (a `client_id` / `api_url`
carrying `"`, newline, `]`) are added to the AC-15/16/17 store-shape test surface and
the Decision 5 config-authoring test surface, so "correct by construction" is
enforced, not merely asserted.

### Terminal-output safety

The `api_url` guard's last-look defense is display-based, so the display must be
trustworthy. Every config-sourced or response-sourced string the wizard echoes —
guided-instruction tokens (identity name, environment slug), the `api_url` line, and
any confirm text — passes through the prompt kit's shared display-sanitizer first: it
strips or escapes control and non-printable bytes, so a hostile team-config value
can't emit ANSI cursor moves or a carriage return that redraws the confirm line to
show a benign URL while the bearer goes elsewhere; and it renders any host in an
ASCII/punycode-normalized form, so a homoglyph host (a Cyrillic `о` in a lookalike
domain, or raw punycode) is visible rather than passing as legitimate. This is a
different axis from R17 secret-scrubbing: AC-27 asserts no *secret* reaches output,
while this asserts non-secret config values are control-char-safe and homoglyph-legible.
Without it the mitigation the `api_url` gate leans on would be weaker than claimed.

### Lower-severity and inherited dimensions

**External artifact handling** (REST responses and CLI subprocess output): the new
`management.go` functions follow the existing checked-unmarshal,
explicit-missing-field, no-silent-zero-value model, and R19's fault-injection matrix
exercises the classification paths, so a malformed body yields a classified error and
a non-zero exit — not code execution or a silent wrong-branch success. Argv is built
from fixed flag names plus config-sourced values (project id, environment slug,
path), never from response bodies, so there's no argv-injection surface. The one
place external content reaches structured output — TOML bodies and URL paths — is
governed by the encode-or-validate rule above; treat that as the home for this
dimension rather than a bare "low." **Permission scope** is bounded and correct: the personal-overlay
git write commits locally and never pushes (mirroring the audited `RunBootstrap` /
`TestRunBootstrap_R24_NoPush`), the team-config site is render-only with zero git
writes, the R20 record is `0600` and non-secret, and folder creation is narrow,
additive, and idempotent with landing checks — not a vector for unintended grants.

### Residual risk

A compromised or carelessly-reviewed team-config repo remains a trust anchor: it
can still declare a hostile `api_url`, and the same repo already carries every other
team-config value the workspace trusts. The entry-time gate (hard-reject non-`https`,
explicit acknowledgment for a non-default endpoint in every mode including scripted),
the display-sanitizer that keeps the acknowledgment legible, and the encode-or-validate
rule together convert a silent exfiltration into an event that is either refused
outright or surfaced for a named, last-look acknowledgment — but an operator who
acknowledges a non-default endpoint without scrutiny can still be misled. That
residual is the honest boundary of a config-driven, self-hosting-capable design: the
guard makes a hostile endpoint visible and acknowledged rather than impossible. It is
documented, not escalated, because it is a property of allowing self-hosted endpoints
at all — distinct from the mitigation gaps this review closed (gate ordering, the
scripted-path contract, and injection encoding), which were escalated and fixed in
the design text above rather than accepted.

## Consequences

### Positive

- **One command replaces a hand-run, cross-context runbook.** A team admin or a
  developer runs `niwa onboard` and is guided through the whole choreography; the
  fragility is fixed once in code rather than copied per workspace.
- **The individual-phase credential is correct by construction (R10).** The wizard
  assembles the path, the `p-`-prefixed key, and the TOML body; a human never types
  any of the three, so the unforgiving exact shape can't come out malformed.
- **Wizard and apply can't disagree (R11/D6).** The wizard-end check reuses apply's
  exact credential-sync read topology and the shared `parseProviderAuthBody`, so
  "the wizard said it's fine but apply fails" is impossible by construction, not by
  test coverage.
- **The custody boundary holds (R5).** Every privileged step runs on the operator's
  own session; niwa holds no admin token and drives no management REST under its own
  authority. The team path makes zero management-endpoint calls, checked at runtime
  by the request recorder and statically by the call-site lint test.
- **World-state re-derivation makes re-run and resume free.** No step taxonomy, no
  schema, no migration story, no cursor to go stale — resume is just running the
  same landing checks at the top of a run. AC-19/AC-20 fall out as seeding
  combinations.
- **Single Infisical-interaction package.** Every wire format niwa speaks to
  Infisical lives in one place, and mint-time verification is a same-package call
  that reuses the one tested HTTP config and redaction sequence rather than
  duplicating the R17 surface.
- **Config authoring preserves operator content.** The surgical table insert leaves
  comments, unknown tables, and unrelated blocks untouched, and lands in durable
  upstream sources, never the `.niwa/` snapshot.

### Negative

- **Team-phase identity/auth/grant are guided, not automated (v1).** The installed
  CLI exposes no management verbs and the session-JWT REST path isn't confirmed, so
  three steps are precise dashboard instructions plus landing checks rather than
  one keypress.
- **The AC-10 boundary is documentation-and-lint enforced, not compiler enforced.**
  Reviewers need the package doc comment plus the static call-site test to confirm
  the team path makes no management calls; package topology alone doesn't prove it.
- **Whole-table config replace drops unknown keys inside the wizard-owned table.** A
  hand-edited unrecognized key inside `[global.vault.provider]` is lost on the next
  wizard-driven update to that table.
- **Wizard-end verification proves shape and resolution, not live
  re-authentication.** A clean R11 check confirms the stored credential resolves
  through apply's read path; it is not a fresh proof the credential still
  authenticates. That proof exists only at mint time (R9).
- **Interactive only.** The wizard needs a TTY or explicitly supplied inputs; it
  fails fast rather than degrading to a non-interactive mode in v1.
- **The free-plan identity cap is an external wall.** An org on the provider's free
  plan can exhaust its identity allotment before the team setup can create the
  shared identity; the wizard surfaces this but can't lift it.

### Mitigations and early-verification items

- **Three carried assumptions are verified first (Phase 0), each with a defined
  fallback.** (a) The Universal-Auth-attach and environment-grant landing-check REST
  shapes are unverified — confirm whether they ride the GET-identity response or
  need a separate read; fallback is trusting the operator's claim for that one
  sub-step (a narrowing of R6, not an architecture change). (b) The management REST
  paths are inherited from the superseded provision design and not re-fetched —
  re-verify against current docs; a wrong path changes only request construction.
  (c) `infisical login status` parseability is unverified — fallback is classifying
  the management call's own error, which the wizard handles regardless (R16), for a
  less proactive but functional topology prompt.
- **The AC-10 documentation risk is mitigated by the runtime request recorder** (the
  load-bearing check every alternative depends on) plus the static call-site lint
  test, giving the audit property without a dedicated package.
- **The `api_url` bearer-exfiltration gap (security review) is closed by an
  entry-time gate, and the Phase 6 security review's three must-fixes are folded in.**
  The gate runs after `resolveAPIURL` and before any bearer-carrying call — including
  the detection GET-identity, which is itself the first such call, so a guard folded
  into the later setup/topology confirm would fire too late. A non-`https` `api_url`
  is an unconditional hard reject in every mode; a non-default `https` one requires an
  interactive confirm or the `--accept-api-url` override (else exit 2 in a scripted
  run), never silently accepted. The interactive display is hardened by the prompt
  kit's control-byte/homoglyph sanitizer so the last-look can't be spoofed. Every
  REST-returned or config-sourced value embedded in a TOML body or URL path is
  TOML-encoded / validated first, closing the credential-body and overlay-injection
  surface. The store subprocess's stdout/stderr gets the same `ScrubStderr` treatment
  as the export path, and the overlay write uses the R20 record's temp-then-rename
  discipline. Residual risk is documented honestly: a compromised team-config repo
  remains a trust anchor, and the guard makes a hostile endpoint visible and
  acknowledged rather than impossible.
- **The whole-table-replace limitation is accepted and documented** rather than
  solved, because the table's schema is small and fully wizard-assigned by R12/R22,
  so there's no expected operator-authored content inside it to lose.
- **The R20 record's scope is pinned by an explicit normative constraint:** it has
  exactly one consumer (best-effort revocation), participates in zero resume/skip
  decisions, and any future proposal to give it a second consumer or a second field
  reopens Decision 1 rather than extending it. This sentence, not the section title,
  is what keeps the design from drifting back toward a persisted step machine.
