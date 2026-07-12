<!-- decision:start id="infisical-interaction-architecture" status="assumed" -->
### Decision: Infisical interaction architecture for `niwa onboard`

**Context**

The `niwa onboard` wizard needs a genuinely new REST client — read an
identity's `client_id`, mint a fresh client secret, verify it via a
universal-auth login exchange, and revoke a client secret — that
authenticates as the operator's own session, never an admin token niwa
custodies (R5, R8, D4). None of this exists in the tree today: no
`internal/provision` package was ever built from the superseded
`provider-auth provision` design, and the one package that talks to
Infisical (`internal/vault/infisical`) implements exactly two things —
CLI subprocess delegation for `infisical export` (behind the read-shaped
`vault.Provider` interface) and one REST call, `auth.go`'s `Authenticate`,
which performs a universal-auth login for an *already-resolved* credential
entry, not identity/mint/revoke management. `vault.Provider` is
deliberately read-shaped (`Name`, `Kind`, `Resolve`, `Close`, plus the
read-shaped `BatchResolver` extension) and the PRD is explicit that it must
not be bent into a management interface — so this decision is about where
the net-new management surface lives *relative to* that package, not
whether it can live inside `Provider` (it categorically cannot).

Three placements were evaluated by a three-way adversarial bakeoff: extend
`internal/vault/infisical` in place; carve a new sibling package dedicated
to management REST; or write the client directly inside the new wizard
orchestration package (`internal/onboard`) with no independent package
identity. The bakeoff also had to settle three coupled mechanical
questions the PRD leaves to the DESIGN: how the wizard detects the
operator's CLI session and org context, how `api_url` is sourced and made
test-pointable, and how the CLI stub and the new HTTP REST double wire into
the functional-test harness per R19.

**Assumptions**

- The exact REST paths for read-identity, mint-client-secret, and
  revoke-client-secret are as recorded in the superseded `provider-auth
  provision` design (`GET /v1/auth/universal-auth/identities/{id}`,
  `POST /v1/auth/universal-auth/identities/{id}/client-secrets`, and a
  `DELETE` on the client-secret's id) — these were described as "almost
  certainly correct by convention" in prior research but not independently
  re-fetched against current Infisical API docs. If wrong, only the
  request-construction detail changes; this decision's package-placement
  choice is insensitive to the exact path strings.
- `infisical login status` (or an equivalent subcommand) is assumed to be
  the primary org-context detection signal, with its exact output shape
  unconfirmed. If it turns out to be unparseable or absent in supported CLI
  versions, org-context detection falls back to classifying the mint/read
  call's own error response (a 403/wrong-org signal), which the wizard needs
  to handle regardless (R16's authentication-failure exit code) — so the
  fallback is not a dead end, only a less proactive UX.
- `api_url` for the wizard's management calls is assumed to be sourced the
  same way R10's stored-credential `api_url` field is: an optional
  workspace-vault-provider-config value, defaulting to the Infisical cloud
  endpoint, never a wizard-specific flag (R14 forbids baked-in constants,
  and the mint call operates against "the org that hosts the workspace
  vault," which is workspace config, not wizard input).
- This decision was made via a three-validator adversarial bakeoff run by a
  single decider agent (not a live human review), in `--auto` mode, per the
  shirabe decision skill's critical-tier process; status is marked
  `assumed` rather than `confirmed` per that skill's threshold, since the
  evidence, while extensive, was contested among the validators until the
  final round.

**Chosen: Extend `internal/vault/infisical` in place**

Add the net-new management REST client — `ReadIdentity`, `MintClientSecret`,
`RevokeClientSecret` — as new, plain package-level functions in
`internal/vault/infisical` (a new file, e.g. `management.go`), alongside the
existing `infisical.go` (CLI export / `vault.Provider`), `subprocess.go`
(the `commander` abstraction), and `auth.go` (the existing universal-auth
login exchange, `Authenticate`). None of the new functions are `Provider`
methods; none are registered with `vault.DefaultRegistry`; none are reached
through `BatchResolver`-style type assertion. `vault.Provider` and its one
existing optional extension are untouched, full stop — the constraint that
this interface must never be bent into a management shape costs nothing
under this choice because the new code never touches it.

**Mint-time verification (R9)** calls the existing `Authenticate`/
`authenticateHTTP` as an ordinary same-package function, passing the
freshly minted `client_id`/`client_secret` pair — zero new import edge, and
no risk of the verification exchange diverging from the one HTTP client
configuration, timeout, and redaction sequencing this codebase already
tests and trusts. This is strictly cheaper than either alternative, both of
which require importing this same function from elsewhere (or duplicating
its ~15 lines and doubling the R17-critical secret-scrubbing surface
forever).

**Session/org-context detection** is added as new helpers in the same
package (e.g. `session.go`), reusing the existing `commander` interface
from `subprocess.go` — the same subprocess-hygiene invariants (`cmd.Env =
nil`, full stdout/stderr capture, `vault.ScrubStderr`) this feature needs
for shelling to `infisical user get token` / reading `INFISICAL_TOKEN` /
parsing `infisical login status`. Because the CLI's `login status` output
shape is unconfirmed, this detection is a proactive UX aid for the
topology-confirmation prompt (R3/D2), not the safety-critical gate — the
authoritative "wrong org" signal is always the classified response of the
actual privileged call (a 403 on `ReadIdentity`, mapped to the
authentication-failure exit code per R16), which the wizard must handle
regardless of whether proactive detection succeeds. Missing-session
detection (R22/AC-36) uses the same commander-based plumbing to check for a
usable session before walking the operator through `infisical login` as an
in-scope pause.

**`api_url` sourcing** extends `auth.go`'s existing `defaultAPIURL` constant
and `entry["api_url"]` pattern with a single package-level precedence
function (e.g. `resolveAPIURL`): an explicit config-declared value (sourced
by the caller from the workspace vault provider config, matching R10's
schema) wins; otherwise an environment-variable test override (e.g.
`NIWA_INFISICAL_API_URL`, mirroring the proven `NIWA_GITHUB_API_URL`
pattern in `internal/github/client.go`) is checked; otherwise the Infisical
cloud default. Both the existing `Authenticate` call and the new management
calls consume this one function, so there is exactly one precedence rule
in the package rather than one per caller.

**Test-double wiring (R19)** is unaffected by this placement choice and
follows two existing precedents directly:
- A new `httptest.Server`-backed HTTP REST double (e.g.
  `infisicalFakeServer` in `test/functional/`), structurally modeled on the
  existing `tarballFakeServer`: per-resource `Set*` seeding methods for the
  identity, its `client_id`, minted-secret bodies, and the environment
  read grant (each independently seedable present/absent/malformed); a
  `SetStatus`-style fault-injection override per fault mode (wrong-org auth
  failure, mint rejection, plan-gate response, login-exchange failure,
  revocation failure); and a `Requests()`/`CountRequests()` request log for
  the AC-10/AC-13/AC-34/AC-35b assertions ("no identity/org/project
  management endpoint was ever called on the team path" / "a revoke
  request for a specific secret id was recorded" / etc.). It is wired into
  the niwa subprocess under test via `s.envOverrides["NIWA_INFISICAL_API_URL"]
  = s.infisicalFake.URL()`, consumed by the existing `buildEnv()` — the
  exact mechanism already proven for `NIWA_GITHUB_API_URL` in
  `test/functional/steps_init_bootstrap_test.go`.
- The existing CLI stub (`writeFakeInfisical`, currently a single-purpose
  shell script recognizing only `export`) is extended, still as a shell
  script on `PATH` per R19's explicit requirement, to also serve `login`,
  `secrets folders create`, and `secrets set`, and to own the seedable
  folder-structure and stored-credential-body fixtures (present, absent, or
  malformed) plus induced store-write and plan-gate failures — matching
  R19's explicit fixture-ownership split between the two doubles.

**Two concrete mitigations close the one legitimate objection raised
against this choice** (see Rationale): a package-doc-comment rewrite in
`infisical.go` explicitly describing the management surface as a second,
distinct purpose from the `Provider`/`Resolve` surface, restating that both
authenticate as the operator's own session and neither is reachable through
`vault.Provider`; and a small static test (walking `internal/onboard`'s
team-phase call sites, or an equivalent lint rule) that fails if any of
`ReadIdentity`, `MintClientSecret`, `RevokeClientSecret` is called from
team-phase code, giving this choice the same call-graph-level audit
backstop a dedicated package would provide "for free," at a fraction of the
structural cost.

**Rationale**

The three-way bakeoff converged here after two full rounds of adversarial
revision. The decisive facts:

1. **R9 reuse is strictly cheapest under this choice** — a same-package
   function call versus a single import edge under both competitors. The
   sibling-package advocate explicitly conceded this in revision ("I
   undersold this... a genuine, not-fully-symmetric trade"), and no
   validator disputed it in the final round.
2. **`vault.Provider` is untouched and unendangered under all three
   alternatives**, including this one — the interface-bending risk the PRD
   flags as non-negotiable costs nothing extra here relative to the
   competitors, because the new functions are plain, receiver-free,
   package-level funcs never referenced from `Factory.Open`, `commander`,
   or `Provider` itself.
3. **The one substantive objection — AC-10's audit optics, i.e. whether a
   dedicated package makes "zero management calls on the team path"
   mechanically checkable from the import graph rather than merely
   enforced by test coverage and review — turns out to favor this choice
   over its closest competitor once examined carefully.** The sibling-
   package advocate's own final position conceded the point is
   "conditional" and explicitly declined to contest an Alternative-1
   outcome ("I would not contest that outcome strongly"). Separately, the
   wizard-internal-client advocate observed that an import-graph check
   against `internal/vault/infisical` proves little on its own, since
   production `apply`'s `Resolve` path already imports that package for
   unrelated reasons — but this cuts against treating "does the caller
   import the package" as a meaningful proxy at all, not specifically
   against this package. What actually closes the gap is the load-bearing
   test R19 already mandates (the REST double's runtime request-recorder
   assertion, which every alternative depends on identically) plus the
   cheap static-lint mitigation adopted above, which gives this choice a
   comparable audit backstop without paying for a package whose sole
   justification the jury itself, mid-bakeoff, downgraded to "narrow."
4. **Keeping the code here preserves a coherent organizing principle: one
   package holds every wire-format niwa speaks to Infisical** — CLI argv
   shapes (`subprocess.go`) and REST JSON shapes (`auth.go`, and now the
   management calls) both. The wizard-internal-client alternative was
   conceded by its own advocate to scatter this same knowledge into
   `internal/onboard`, a package whose core job (wizard UX, prompts, resume
   bookkeeping) has nothing to do with HTTP wire formats — a genuine, not
   hypothetical, mixed-concern cost that this choice avoids entirely. This
   specific point was not directly rebutted by the wizard-internal-client
   advocate in the final round.
5. **D8's own precedent ("fold #194/#199 mechanics in as internal building
   blocks" rather than shipping standalone reusable surfaces), when checked
   against the one superseded mechanic that actually shipped** (`D6`'s
   contract-validator reuse, folded as unexported functions into
   `internal/workspace/credentialsync.go` rather than a fresh package),
   favors folding into an *existing, already-relevant* package over
   inventing either a brand-new sibling package or scattering the code into
   a purpose-mismatched consumer package. Extending `internal/vault/infisical`
   is the version of "fold in place" that matches this precedent most
   closely, since it is already the package whose job is "how niwa talks to
   Infisical."

The accepted trade-off: the package doc comment must be rewritten
carefully (a real, one-time cost, not automatic), and reviewers will need
the doc comment plus the new lint test — not package topology alone — to
confirm the AC-10 boundary holds. This is a documentation/convention
safeguard rather than a compiler-enforced one, which is the honest, named
cost of this choice.

**Alternatives Considered**

- **New sibling package, separate from `internal/vault/infisical`** (e.g.
  `internal/infisical/admin`): would give a package-boundary-level static
  backstop for AC-10's "no management calls on the team path" property, and
  would keep the existing package's doc comment untouched. Rejected because
  its own advocate conceded, after two rounds of adversarial revision, that
  the argument is narrow and conditional, that R9-verification reuse still
  requires importing `internal/vault/infisical` anyway (so the "separation"
  is code-location isolation, not dependency isolation), and that it is a
  package built for exactly one confirmed consumer with no second caller in
  sight — a YAGNI cost the advocate did not dispute, only argued was worth
  paying for the audit property, an argument the advocate itself ultimately
  downgraded to "I would not contest [Alternative 1] strongly."
- **Wizard-internal client, no independent package identity** (all
  management REST code and session detection written directly inside
  `internal/onboard`): would minimize the number of packages touched by
  this PRD and matches a superficial reading of D8's YAGNI stance most
  directly. Rejected because, despite genuinely strong D8/greenfield/
  single-consumer arguments, its own advocate conceded it produces a
  mixed-concern package (wizard orchestration UX mixed with an HTTP client
  and session-detection plumbing that has no natural relationship to
  wizard step-sequencing), still pays an import cost (one edge into
  `internal/vault/infisical` for `Authenticate` reuse — not zero, as under
  the chosen option), and creates a real discoverability gap between the
  existing login-exchange code and the new mint/verify code once they live
  in unrelated-purpose packages. Its strongest argument (D8's "fold as
  internal building blocks" precedent) was ultimately better satisfied by
  folding into the existing Infisical-interaction package than by folding
  into the wizard's own package, once the `credentialsync.go` precedent it
  surfaced was applied to itself.

**Consequences**

- `internal/vault/infisical` gains a second stated purpose in its package
  doc comment (management REST alongside the read-shaped `Provider`
  backend) — anyone auditing "does the vault read path have admin
  capability" must read that comment (and, going forward, the new
  call-site lint test) rather than infer safety from package boundaries
  alone. This is a one-time, named documentation cost, not an ongoing
  structural one.
- `vault.Provider`, `BatchResolver`, `Factory`, and `Registry` require zero
  changes — the read/resolve path used by every `niwa apply` invocation is
  untouched by this feature.
- The wizard's `internal/onboard` package (net-new, created by this PRD)
  imports `internal/vault/infisical` for both its existing CLI-delegation
  surface (login, export, secrets set, folder create — all net-new exported
  functions needed regardless of this decision) and the new management
  functions, keeping a single import edge to reason about for "everything
  the wizard needs from Infisical."
- A new functional-test HTTP double (modeled on `tarballFakeServer`) and an
  extended CLI-stub script are added to `test/functional/`, independent of
  and unaffected by any future revisiting of this specific package-
  placement choice — if the management surface is ever extracted to its
  own package later (e.g., if D3's named-but-unconfirmed future upgrade
  path is taken), the test-double wiring does not need to change, only the
  production import path the tests exercise.
- If a second consumer of the management client emerges later (the only
  scenario any validator identified that would flip this decision),
  extraction into a dedicated package remains a bounded, mechanical refactor
  — move the file, add a constructor if warranted, update the (currently
  single) import site in `internal/onboard` — not a redesign.
<!-- decision:end -->
