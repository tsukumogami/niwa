<!-- decision:start id="niwa-onboard-detection-and-prompt-ux" status="assumed" -->
### Decision: Mode/topology detection mechanism and prompt UX for `niwa onboard`

**Context**

PRD-niwa-onboard.md already settles, at the requirements level, that both
setup detection (team vs. individual, R2) and topology detection (same-login
vs. split-login, R3) must be inferred where possible and always confirmable
or overridable — never silent, never blind-asked with no attempt at
inference (D1/D2 in the PRD's own Decisions section). What those
requirements defer to design is the concrete detection *mechanism*: which
signals to read, in what order, and — critically — how to avoid paying for
network calls the wizard doesn't already need. They also defer the prompt
plumbing question: the repo has two independent, hand-rolled TTY prompt
helpers (`internal/cli/prompt.go`'s `ReadConfirmation`, a typed-string
exact-match; `internal/cli/init.go`'s `promptBootstrap`, a Y/n loop with
default-yes-on-Enter) and no generalized "ask a sequence of questions"
abstraction, but the wizard needs at least three distinct interaction
shapes: pick-one-of-N, yes/no confirm, and pause-until-Enter (for dashboard
and login-switch waits).

A structural fact shapes both detection mechanisms: neither niwa's config
structs nor Infisical project UUIDs encode org membership anywhere. A
project ID is an opaque string; nothing in `internal/config` associates it
with the org that owns it. So same-login/split-login can never be fully
resolved from static config alone — some live signal is unavoidable for a
*reliable* topology inference. Separately, the team-identity GET call
(reading the shared identity's `client_id`) is already mandatory in two
places regardless of which branch the wizard takes: R8 step 1 (the
individual phase's first automated step) and R21 (team-setup re-run
verification). This makes it possible to design detection so that its one
unavoidable network dependency is a call the flow needs anyway, rather than
detection-only overhead.

**Assumptions**

- The operator's active `infisical` CLI session's org scope is either
  locally introspectable or can be inferred from the *failure shape* (not
  found vs. unauthorized-for-org) of the identity-GET call performed with
  the operator's current session. If the CLI genuinely gives no
  distinguishable signal at all, the fallback is to still use that one call
  but treat any failure as "assume split-login, confirm," which costs no
  extra network round trip — it only weakens the inference's precision, not
  the architecture.
- Explicit override flags for both setup-selection and topology-selection
  exist per Decision 2's command-surface work (assumed given, not re-derived
  here).
- "Infer, then confirm, never silent" is settled at the requirements level
  by PRD D1/D2 and is not re-litigated by this decision; this decision
  competes on mechanism and prompt plumbing only.
- Status is `assumed` rather than `confirmed`: this ran in `--auto` mode with
  no user confirmation, and the topology-inference mechanism rests on an
  unconfirmed CLI-introspection capability (see above) that DESIGN-level
  implementation must verify against the actual `infisical` CLI surface.

**Chosen: Layered local-first detection with one reused live call, small internal prompt kit**

Detection funnel, cheapest signal first:

1. **Free (already-parsed config, zero extra I/O):** if the team config
   declares no `[vault.provider]`/`[vault.providers.*]` at all
   (`VaultRegistry.IsEmpty()`), infer team setup immediately — there is no
   project ID yet to check anything against. No network call needed for
   this branch.
2. **Live, but reused, not detection-only:** if the team vault block IS
   declared, perform the GET-identity call for `client_id` that R8 step 1
   needs anyway (and that R21 needs on any team-setup re-run) using the
   operator's currently active session. Interpret it once for three
   purposes: (a) not-found → team setup incomplete, route to team, verified
   later by R21; (b) found → team phase is complete, so branch on the cheap
   local signal of whether the personal overlay already declares
   `[global.vault.provider]` and a credential already resolves (R15: if yes,
   straight to R11 verification; if no, individual setup); (c) the
   *shape* of a failure on this same call (a generic not-found vs. an
   org-scope/unauthorized failure specifically) doubles as the topology
   signal — see below.
3. **Topology inference piggybacks on step 2's call:** if it succeeds with
   the operator's current session and no org-scope error, infer same-login
   (the active session already reaches the team project). If it fails
   specifically on an org-mismatch/unauthorized basis (not a generic
   transport failure), infer split-login and flag that a login switch will
   be needed before the later store-into-personal-overlay step (R4). When
   the personal overlay doesn't exist yet, default the prior toward
   split-login (the entire reason credential-sync exists is the cross-org
   case) — but this is a prior only, always surfaced as a named,
   overridable prompt: "Detected: split-login — your current session
   doesn't yet reach the team vault's org. Continue? [Y/n]", never silent.

No separate network call exists purely to answer "team or individual" or
"same-login or split-login" — both answers ride on a call the flow performs
regardless of which way it branches.

Prompt UX: introduce one small internal prompt package (naming deferred to
DESIGN, e.g. `internal/cli/wizard` or `internal/prompt`) exposing exactly
three primitives:

- `Confirm(prompt string, defaultYes bool) (bool, error)` — yes/no with a
  stated default, generalizing `promptBootstrap`'s existing Y/n-with-
  default-on-Enter loop shape.
- `Select(prompt string, options []Option) (chosen string, err error)` —
  numbered one-of-N, re-prompting on invalid input; net-new, no existing
  precedent.
- `Pause(prompt string) error` — read-and-discard one line, used only to
  gate on an external action (a dashboard step, a login switch)
  completing; validates nothing.

All three share one internal re-prompt/EOF-handling loop generalized from
`promptBootstrap`'s existing logic (init.go:308-334), and all three are
gated by a single TTY-or-override check performed once at wizard entry
(satisfying R18) rather than scattered per-primitive checks. The two
existing helpers, `ReadConfirmation` and `promptBootstrap`, are left
untouched — not refactored, not deprecated, not made to share code with the
new package. Neither's semantics (exact-string match; single fixed Y/n) is a
strict subset of the new primitives, and touching either risks regressing
`destroy` and `init --bootstrap`, two already-tested, unrelated commands,
for no onboard-specific benefit.

**Rationale**

This mechanism minimizes network cost by construction — detection is nearly
free for a from-scratch team setup (pure config check) and adds zero net
calls for the individual/re-run cases (it reuses R8's and R21's mandatory
identity-GET rather than inventing a parallel one). It keeps both
inferences confirmable and overridable per R2/R3, and topology is always
presented as a named same-login/split-login choice, never buried in a
summary. It also resolves an ordering hazard the heavier alternative
(Alternative 3) runs into: R22 requires the session-login pause to happen
*before* any team-vault-side call, and a design that reuses that same call
for detection is trivially compatible with that ordering, whereas a
composite up-front probe would need several session-dependent checks before
R22's pause has necessarily completed. On prompt UX, it introduces the
smallest new abstraction that the task's own primitive requirement (pick-
one-of-N, yes/no, pause) demands — one shared loop and three call sites,
rather than three-or-more independently hand-rolled copies of the same
EOF/garbage-input handling logic.

**Alternatives Considered**

- **Ask-heavy, minimal inference, copy-adapt `promptBootstrap` per
  question, no new package**: satisfies "build on the existing helpers"
  the most literally and is fully requirements-compliant (asking directly
  still allows a confirm/override framing). Rejected because the task's own
  primitive list guarantees at least three distinct interaction shapes
  (setup pick, topology pick, pause-until-Enter), and copy-pasting
  `promptBootstrap`'s loop per shape means the same re-prompt/EOF-handling
  logic lives in three-plus places — any future fix (e.g., a TTY-detach
  edge case) has to be applied at every call site rather than once.
- **Comprehensive upfront doctor-depth probe, silent-leaning topology,
  external prompt library**: front-loads a composite check (doctor-depth
  read plus live project-to-org lookups for both projects) before branching
  at all, and folds topology into a post-hoc summary rather than a
  dedicated confirm gate. Rejected on two independent requirements
  grounds, not just preference: it fails R3's "operator MUST confirm or
  override" by presenting topology after the fact rather than as a gate,
  and it requires a new external TUI/prompt library, which the task
  explicitly rules out ("no new heavy TUI dependency"). It also creates a
  chicken-and-egg ordering problem against R22 (the composite probe wants
  session-scoped answers before the session-login pause R22 owns has
  necessarily run).

**Consequences**

The wizard gains exactly one new internal package (three prompt primitives,
one shared loop) rather than a scattered set of ad hoc prompt loops, which
should make later additions (if the wizard grows more questions) cheap
without touching `destroy` or `init --bootstrap`. Detection logic threads
through the same identity-GET call used by R8/R21, so a bug in that call's
error-shape handling affects both detection and the individual/team flows
uniformly — a DESIGN-level implementer must be precise about distinguishing
"identity not found" from "unauthorized for this org" in that single call's
error path, since both detection axes now depend on that distinction being
reliable. The unconfirmed piece — whether the `infisical` CLI's session
state gives a cheaper, more direct org-scope signal than interpreting a
failed GET — is a DESIGN-level implementation detail to verify against the
actual CLI surface; if it doesn't, the fallback (treat any failure as
"assume split-login, confirm") preserves the architecture at the cost of a
slightly weaker prior, not a redesign.
<!-- decision:end -->
