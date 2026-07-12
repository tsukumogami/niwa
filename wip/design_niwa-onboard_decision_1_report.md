<!-- decision:start id="wizard-control-flow-resume-architecture" status="assumed" -->
### Decision: Wizard control-flow and resume architecture

**Context**

`niwa onboard` is a new interactive wizard covering two setups: a team phase
(admin: create a shared machine identity, attach Universal Auth, grant read
access, create folder structure — mostly guided dashboard steps with landing
checks) and an individual phase (developer: read the identity's `client_id`,
mint a secret, verify via a real auth exchange, store into the personal
overlay vault at an exact contract shape, then run a doctor-style wizard-end
check). PRD-niwa-onboard.md R15 requires re-running the wizard to resume
sensibly: go straight to verification against a completed setup, resume at
the first incomplete step against a partial one, and re-mint/re-store after a
topology change. AC-20's partial-resume fixture explicitly depends on
whatever resume-state representation this decision settles.

Research into the codebase established several load-bearing facts before the
bakeoff began. First, there is no wizard or state-machine precedent anywhere
in `internal/cli` or `internal/workspace` — this is genuinely new ground.
Second, `state.json` (`.niwa/instance.json`) is not, as initially guessed,
structurally outside the atomically-replaced `.niwa/` snapshot; it lives
inside it and survives `niwa apply` only via an explicit, closed-set
carry-over helper (`preserveInstanceState`). Third, a separate, already-used
precedent exists for genuinely machine-local, non-git-managed files: the XDG
`~/.config/niwa/` directory, independently reimplemented by
`GlobalConfigPath`, `GlobalConfigDir`, and `OverlayDir`. Fourth, and most
decisive: PRD R20 requires capturing a minted client-secret's id across runs
so a later run can best-effort revoke a superseded or orphaned one, and
research confirmed this id cannot be reconstructed from the REST surface R19
models — no list-client-secrets endpoint or correlator/description field is
specified anywhere in the PRD, and D11's own rationale already assumes local
capture as a given. Fifth, every team-phase guided step's "did it land" check
(identity, Universal Auth attach, environment grant, folders) is specified or
clearly intended as a world-state probe, not an internally remembered step
cursor — though the exact REST shape for the UA-attach and grant checks isn't
pinned down at the requirements level (an assumption, see below).

**Assumptions**

- The unresolved REST shape for the Universal-Auth-attach and
  environment-grant landing checks (whether they ride on the same
  GET-identity response or need a separate read call) does not change the
  "probe, not cursor" architecture — only the number/shape of the REST calls
  the probe issues. If Infisical's real API turns out to expose no read
  surface for one of these at all, the wizard would have to fall back to
  trusting the operator's unverified claim for that one sub-step, which is a
  narrowing of R6's "verify the step landed" guarantee, not a change to the
  resume architecture itself. This should be confirmed against the real API
  early in the DESIGN.
- This decision was made in `--auto` mode without a human confirming the
  final call; per the decision skill's status convention this alone is
  sufficient to mark the decision `assumed` rather than `confirmed`, even
  though the evidence (three independently-argued validators converging on
  the identical mechanism from three different starting positions, backed by
  concrete codebase research) is unusually strong for this status.
- The one open disagreement among all three validators — whether the DESIGN
  doc should title the surviving mechanism "hybrid" or "stateless resume with
  one narrow, PRD-forced exception" — is treated here as a documentation
  framing choice, not an architectural one; both framings describe the
  identical mechanism, and this report picks "hybrid" for descriptive
  clarity while carrying forward the explicit anti-scope-creep language every
  validator agreed is the thing that actually matters.

**Chosen: Hybrid — stateless step resume, with one persisted non-secret record scoped to R20**

Every resume, skip, and landing decision the wizard makes is answered by a
world-state probe, recomputed from scratch on every invocation, reusing the
exact read paths other parts of the system already use so the wizard can
never disagree with what `niwa apply` or the doctor-style checks would see:

- Setup detection (R2) and topology detection (R3) are inferred from
  observable workspace/session state, not from a remembered prior choice.
- Team-phase step landing (R6, R7, R9's plan-gate degradation, R21's re-run
  verification) is a live check per step — does the identity now expose a
  `client_id`? does the grant show up? does the folder path exist? — and a
  guided-dashboard wait is not a distinct persisted state: the wizard either
  blocks synchronously in-process (print instructions, wait, re-probe,
  continue) or, if the operator quits and returns later, the next invocation
  re-probes from scratch and finds the same frontier step, with no "I was
  waiting here" marker to go stale or need clearing.
- Individual-phase step sequencing (R8, R9, R10) and both wizard-end checks
  (R11 for the individual phase, R21 for the team phase) reuse the
  credential-sync read topology (D6) and the same landing-check probes,
  giving AC-19's "re-run against a complete setup goes straight to
  verification" and AC-20's "re-run against a partial setup resumes at the
  first incomplete step" for free, as compositions of the same probes, with
  no separate resume-decision function to keep in sync.

The single exception: one small, non-secret, durable record — the previously
minted client-secret's id, plus a flag for "not recoverable" (R20's third
bullet) — is persisted specifically because it cannot be reconstructed from
observable state, unlike everything else the wizard tracks. It lives at
`~/.config/niwa/` (not on `InstanceState`/`.niwa/instance.json`), keyed by
`(kind, project)` to mirror the credential pool's own keying, shaped as a
flat, non-extensible struct — `{secret_id, recoverable}` or equivalent, never
a map keyed by step name and never a generic metadata bag — with exactly one
consumer: R20's best-effort revocation on supersession or store failure. It
participates in zero resume/skip decisions. Written via `os.OpenFile` at mode
`0600` in its own directory then renamed over the target, matching the
hygiene discipline (R17/AC-29) the rest of the wizard's credential-touching
paths already follow, even though this file itself never holds secret bytes.

**Rationale**

The parent decision's own constraint is close to a design invariant: the
wizard's remaining-work computation must never disagree with what `niwa
apply` would see, and reuse of observable world state is favored over
private bookkeeping that can go stale. That constraint, on its own, rules out
a general-purpose persisted step-state machine — the PRD's own acceptance
criteria (AC-9/AC-9b, R19's seedable resource states, R11/R21's mandatory
pre-completion checks) already require a live world-state check at every
point that matters, so a step cursor either duplicates those checks
(forfeiting the efficiency argument for having one) or gets trusted instead
of them, recreating the exact "silently broken far from the cause" failure
this feature exists to eliminate. All three validators reached this
conclusion independently, including the validator advocating for the
persisted-cursor alternative.

But R20 is not a "what's left" question — it's a "what happened once"
provenance question, and the research is unambiguous that no probe can
answer it: R19 enumerates the REST double's full modeled surface (read-
identity, mint, login, revoke) with no list-secrets endpoint or correlator
field, and D11's own stated rationale ("R8 already captures the minted
secret id, so something must consume it or the capture is dead state")
already assumes local capture as a given, not an open question. A strictly
pure-stateless reading can only satisfy R20's letter by permanently taking
its third-bullet degraded fallback — never revoking, always falling to
TTL-lapse — which defeats the requirement's evident intent, since R20 and
D11 (both already-Accepted) clearly frame best-effort revocation as the
primary behavior. Choosing pure statelessness at the DESIGN layer would be
silently discharging an already-Accepted requirement's proactive half under
the banner of architectural purity, not a genuinely simpler design.

The hybrid resolves this by matching mechanism to question type: probes for
everything observable, one narrowly-scoped persisted record for the one
datum that provably isn't. Placement at `~/.config/niwa/` (over
`InstanceState`) follows the record's real-world scope — operator ×
vault-org, not per-instance-sandbox — and avoids a concrete correctness bug:
homing it on `InstanceState` would make the record invisible across
ordinary instance churn (deletion/recreation, a second instance of the same
workspace, both explicitly ordinary per US-6), causing R20's "not
recoverable" fallback to misfire on a "wrong lookup key" case that isn't
genuinely unrecoverable at all. `~/.config/niwa/` also avoids borrowing
`preserveInstanceState`'s atomic-swap carry-over machinery to solve a
problem (surviving `.niwa/` snapshot replacement) the record never actually
has.

**Alternatives Considered**

- **Persisted step-state machine**: a dedicated file recording an explicit
  step cursor (e.g. "team-identity-created", "individual-minted", ...),
  read at start to resume at the first not-done step; world-state reads used
  only to perform a step, never to decide whether to skip it. Its case is
  real — it gives AC-20 the most direct possible fixture (seed the file,
  assert the resume point), avoids re-probing already-confirmed steps on
  every resume in a flow explicitly expected to have multiple interruptions,
  and gives the "waiting on guided step N" condition a first-class
  representation. Rejected because it collides head-on with the PRD's own
  acceptance criteria: AC-9/AC-9b and R19 already mandate a live,
  independently-seedable world-state check on every guided step regardless
  of what a cursor claims, and R11/R21 mandate one before declaring any run
  complete. A cursor layered on top of those mandatory checks either
  duplicates them (forfeiting the efficiency argument that's its strongest
  case) or gets trusted instead of them — recreating the exact "mistake
  surfaces silently at a later `niwa apply`, far from the cause" failure mode
  the PRD's own problem statement names as the thing this feature exists to
  kill. It also introduces real new machinery against an explicit YAGNI
  constraint (a step taxonomy, a schema, a migration story, topology-aware
  step invalidation for R15/US-6's re-run-after-topology-change case) with no
  existing precedent to build on, and carries structural risks pure
  statelessness cannot have by construction: staleness if a second
  admin/machine touches the same team setup, corruption from a process killed
  mid-write during an indefinite guided wait, and manual tampering that's
  invisible until a much later failure. Even its own advocate, after seeing
  the other two positions, concluded the general mechanism doesn't survive
  and that only its narrow core (persisting R20's one non-observable datum)
  is defensible — which converges on the chosen hybrid rather than remaining
  a distinct alternative.

- **Fully stateless re-derivation**: the wizard carries no bookkeeping file
  of its own at all; every run, every step is re-verified by probing
  observable state directly, with landing check and resume check being the
  same probe. This is the architecture that makes "never disagree with
  apply" true by construction for literally 100% of decisions, fits the
  guided-dashboard indefinite-wait/quit-and-resume shape without inventing
  any "waiting" state (probe-then-print is the same code path every time,
  and self-corrects for free if the world changed during the wait), and adds
  zero new machinery beyond what R6/R7/R8/R9/R10/R11/R21 already require the
  wizard to do. Rejected as the *complete* answer for exactly one reason,
  conceded by its own advocate without hedging: R20's minted-secret
  revocation genuinely cannot be re-derived from the modeled REST surface, so
  a literal zero-persistence reading can only satisfy R20's letter via its
  degraded, TTL-lapse fallback — never actually revoking a superseded or
  orphaned secret. Since R20 and D11 are already-Accepted PRD requirements
  that clearly intend best-effort revocation as the primary behavior (not
  the exception case), a design that makes that primary behavior permanently
  unreachable is declining to build an accepted requirement under cover of
  architectural purity, not a neutral, simpler reading of it. The chosen
  hybrid keeps every one of this alternative's genuine strengths (the "never
  disagree" guarantee for all resume/skip decisions, the zero-new-machinery
  guided-wait handling, the direct fit with R19's resource-state-seeding test
  doubles) and closes its one gap with the smallest addition research showed
  was necessary.

- **Hybrid, homed on `InstanceState` (variant 3a)**: identical mechanism to
  the chosen design, but the one persisted R20 record lives as a new field
  on `.niwa/instance.json`, following the precedent of the existing v4
  `AuthSources` field (a map of non-secret categorical records, added via an
  additive schema-version bump). This was a genuine contender — it reuses an
  existing, already-proven schema-evolution pattern with zero new carry-over
  code, since `preserveInstanceState` already carries the whole file through
  `niwa apply`'s atomic swap. Rejected in favor of the `~/.config/niwa/`
  placement (3b) because the minted secret and the credential it seeds are
  scoped to the operator and the vault org, not to any one instance sandbox:
  an operator who deletes and recreates an instance, or runs `niwa onboard`
  from a second instance of the same workspace — both ordinary operations,
  not corner cases, per US-6's topology-change re-run and general instance
  lifecycle churn — would have a minted-secret id that's invisible to the
  "other" instance purely because of where it's filed, not because it's
  genuinely unrecoverable. That's a structurally worse failure than a
  placement problem: it makes the mechanism misclassify a wrong-lookup-key
  bug as R20's own legitimate "not recoverable" case, which is exactly the
  kind of misleading signal this feature exists to eliminate elsewhere.
  `~/.config/niwa/` also keeps the one exception entirely clear of
  `preserveInstanceState`'s closed-set carry-over list and `InstanceState`'s
  schema-version lineage — machinery built to solve `.niwa/`'s atomic-swap
  problem, which the R20 record never has in the first place — and it sits
  exactly where R12 already puts `niwa config set global`'s operator-local
  write, extending an existing operator-local-vs-durable-upstream split
  rather than introducing a second, competing per-instance-vs-per-operator
  one.

**Consequences**

- The wizard needs no step taxonomy, no phase enum, no resume-decision
  function distinct from the landing-check functions R6/R9/R11/R21 already
  require — "resume" is simply running those same checks at the top of a run
  instead of only at the point a step would otherwise execute.
- AC-20's partial-resume fixture is answered entirely by seeding the CLI
  stub's and REST double's resource state (present/absent/malformed, per
  R19) in the right combination — no bespoke fixture format, no interaction
  with the one persisted file at all. AC-19 (complete setup, straight to
  verification) is the same fixture mechanism with everything seeded
  present.
- One new small file is introduced: `~/.config/niwa/<something>.json` (exact
  name is a DESIGN-level detail), keyed by `(kind, project)`, holding
  `{secret_id, recoverable}` (or equivalent) and nothing else — no secret
  bytes, no step state, no phase marker. AC-33 (revoke-on-supersession) is
  fixtured by pre-seeding this file with a prior id and asserting a `DELETE
  client-secret` call fires; AC-34 (revoke-on-store-failure) needs no
  pre-seeded file at all, since the just-minted id is available from the
  in-memory mint response; AC-35b (id not recoverable) is fixtured by the
  file's deliberate absence, exercising R20's own documented fallback as a
  first-class, tested path rather than a theoretical one.
- The DESIGN doc must carry an explicit normative constraint alongside
  whichever label it uses for this architecture: the one persisted record
  has exactly one consumer (best-effort revocation), participates in zero
  resume/skip decisions, and any future proposal to give it a second
  consumer or a second field must be treated as reopening this decision, not
  as a natural extension of it. All three validators, arguing from three
  different starting alternatives, agreed this sentence — not the section
  title — is what actually prevents the design from drifting back toward the
  rejected step-state-machine's structural risks.
- The exact REST shape for the Universal-Auth-attach and environment-grant
  landing checks is not yet pinned down at the requirements level and should
  be confirmed against Infisical's real API early in the DESIGN phase; this
  affects call count and response parsing for those two probes, not the
  architecture chosen here.
<!-- decision:end -->
