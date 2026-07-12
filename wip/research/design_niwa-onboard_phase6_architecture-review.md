**Verdict:** PASS

# Architecture review — DESIGN-niwa-onboard

Reviewed the Solution Architecture, Implementation Approach, and the five decision
records against PRD-niwa-onboard (R1–R22) and against the actual repo code under the
worktree. The architecture is coherent, well-motivated, and implementable. Every
load-bearing claim about existing code checks out. There is one genuine
under-specification (R9's read-hop) that must be nailed down during planning; it is
bounded and fits the design's own "carried assumption / Phase-0 verify" pattern, so
it is a must-fix note rather than a redesign, hence PASS not FAIL.

---

## 1. Is the architecture clear enough to implement?

Mostly yes. A competent Go developer could build the command surface (Decision 2),
the prompt kit (Decision 3), the management REST client (Decision 4), the config
authoring primitive (Decision 5), the world-state re-derivation control flow
(Decision 1), the R20 record, and both test doubles directly from the text. The
component map, key-function-surface list, exit-code table, and the two Mermaid data
flows are concrete enough to code against without guessing. The decisions carry
their rejected alternatives with reasons, so an implementer won't relitigate.

The one place an implementer would have to guess is R9's verification read-hop — see
Finding A below.

## 2. Missing components / interfaces — coverage of R1–R22

Note: the PRD defines R1–R22 (not R23; the "R23" in `root.go` belongs to the init
PRD, a different feature). Every onboard requirement has an architectural home:

- R1 command / R2 setup detect / R3 topology → `onboard.go` + Decision 3 funnel. OK.
- R4 login pauses → `Pause` primitive + split-login single-pause in the data flow. OK.
- R5 custody / R14 generic surface → Decision 4 + AC-10 recorder+lint + AC-23 grep. OK.
- R6 team split / R7 plan-gate / R21 team re-run verify → team runner (Phase 4). OK.
- R8 mint pipeline / R10 exact shape / R13 self-ref guard → individual runner
  (Phase 5), `management.go`, credential assembly. R13 has real precedent
  (`credentialpool_r13_test.go`, `credentialpool_r12_test.go` exist). OK.
- R9 mint-time verify → **partially homed** (Finding A).
- R11 wizard-end check → reuse of `pickCredentialSyncSpec` /
  `openCredentialSyncProvider` / `parseProviderAuthBody` (all confirmed present). OK.
- R12 config lands durable → Decision 5 per-site. OK.
- R15 idempotence/resume → Decision 1 (probe-not-cursor). OK.
- R16 exit codes → Decision 2 table (0/2/3/4/5/6, 1 reserved), new
  `onboard.ExitCodeError` + third `errors.As` arm. OK.
- R17 hygiene → Security Considerations, reuses `auth.go` scrub discipline. OK
  (but interacts with Finding A).
- R18 non-TTY fail-fast → Decision 2, mirrors `init.go`. OK.
- R19 two doubles → Test-double architecture section. OK (but see Finding A: the
  modeled REST surface omits a secrets-read endpoint the R9 read-hop may need).
- R20 revocation → R20 record + `RevokeClientSecret`. OK.
- R22 preconditions → data-flow step 1 + Decision 5 overlay scaffold. OK.

The five data-flow paths the task asked about are all covered: team, individual
same-login (zero pause), individual split-login (one pause between mint and store),
re-run/verify-only (probe → straight to R11 or R21), and plan-gated degradation
(guided instructions + landing check + resume). No missing path.

## 3. Phase sequencing

Sound and incrementally buildable/testable. Phase 0 (verify carried assumptions) →
1 (management client + both doubles) → 2 (prompt kit + detection) → 3 (command shell
+ exit codes) → 4 (team runner) → 5 (individual + R20) → 6 (config authoring) → 7
(wizard-end + preconditions) → 8 (functional @critical). Dependencies respected: the
@critical individual happy path in Phase 8 depends on Phases 5/6/7, all earlier.
Phase 3 wires the command to a "wizard engine skeleton" before the runners land in
4–5; that means Phase 3's exit-code tests exercise stubs, which is normal. No
inversion.

## 4. Simpler alternatives / accidental complexity

Little to trim. The design already argues down the persisted step-machine (correctly
— it would duplicate or displace the live checks the PRD mandates) and keeps exactly
one persisted datum (the R20 secret id) with a pinned single consumer. The prompt kit
is three primitives over one loop — minimal. The two-double split is forced by R19.
The `api_url` trust guard is the one thing that looks like gold-plating, but it closes
a real bearer-exfiltration gap the security review found and reuses the existing
confirm gate (no new subsystem), so it's justified. No accidental complexity worth
removing. Decision 3's topology-from-failure-shape inference is clever-but-fragile,
which the design itself already flags as a Phase-0 verify item with a defined
fallback — acknowledged, not hidden.

## 5. Do code claims match reality?

Spot-checked extensively; all accurate:

- `internal/vault/infisical/` = `auth.go` + `infisical.go` + `subprocess.go` (no
  `management.go`/`session.go` yet — correctly "New"). ✓
- `auth.go`: `Authenticate`, `authenticateHTTP`, `scrubResponseBody`,
  `defaultAPIURL = "https://app.infisical.com/api"`, `entry["api_url"]` pattern. ✓
- `vault.ScrubStderr` at `internal/vault/scrub.go:35`; export path already scrubs. ✓
- `sessionattach.ExitCodeError{Code, Msg}` at `detach.go:33`; `Execute()` uses
  `ece.Msg`/`ece.Code`. Exactly two typed arms today (`sessionattach.ExitCodeError`,
  `workspace.InitConflictError`) + exit-1 fallthrough — the "third arm" claim is
  correct. ✓
- `IsStdinTTY` func-var (`prompt.go:26`), `promptBootstrap` (`init.go:308`),
  `ReadConfirmation` (`prompt.go:42`), `handleNoMarkerR13` (`init.go:267`). ✓
- `pickCredentialSyncSpec`, `openCredentialSyncProvider`, `parseProviderAuthBody`,
  `VaultRegistry.IsEmpty()` (`config/vault.go:136`). ✓
- `GlobalConfigPath`/`GlobalConfigDir`/`OverlayDir`, `preserveInstanceState`
  (`snapshotwriter.go:443`), `InstanceState.AuthSources`. ✓
- `NIWA_GITHUB_API_URL` (`github/client.go:43`); functional suite sets it via
  `s.envOverrides["NIWA_GITHUB_API_URL"] = s.githubFake.URL()`, consumed by
  `buildEnv()` (`steps_test.go:77`, ranges `envOverrides` at line 112) — the exact
  mechanism the design says `NIWA_INFISICAL_API_URL` will mirror. ✓
- `vault.Provider` = Name/Kind/Resolve/Close + `BatchResolver` (`provider.go`). ✓
- `RunBootstrap`, `TestRunBootstrap_R24_NoPush`,
  `TestRunBootstrap_R18_NoAuthorArgNoAuthorEnv`, `CreateSessionFunc`. ✓
- `tarballFakeServer` (model for the new REST double), `writeFakeInfisical`
  (`steps_test.go:125`, currently export-only, to be extended). ✓

Minor imprecision (not blocking): the design says R9 reuse "calls the existing
`Authenticate`", but `Authenticate(ctx, entry map[string]any)` takes a map; the pair
form is `authenticateHTTP(ctx, apiURL, clientID, clientSecret)`. The design names
both, so an implementer has a compliant handle either way.

---

## Findings

### Finding A (must fix during planning) — R9's read-hop has no compliant, enumerated home

R9 mandates a **two-hop** mint-time proof: authenticate the minted pair *and* read
the target environment. The design homes the auth hop (reuse
`Authenticate`/`authenticateHTTP`) but never names a function for the **read hop**,
and the gap collides with a hard NFR:

- The one existing "read a target env with a specific credential" mechanism is
  `infisical export --token <access-token>` (`subprocess.go:131-135`). That places
  the token on **argv**, which R17 and AC-28 forbid outright ("No secret MAY ever be
  placed on a subprocess or process argv").
- A REST secrets-read carrying the token in a header *would* satisfy R17, but no such
  function is in the management surface (`ReadIdentity`/`MintClientSecret`/
  `RevokeClientSecret`), and the design's own REST-double modeled surface (following
  R19: read-identity, mint, universal-auth login, revoke) has **no secrets-read
  endpoint** either. So neither the production surface nor the test double models it.

R9/AC-14 sits on the `@critical` individual happy path, so this is not a corner. As
written, an implementer reaching Phase 5's "read → mint → R9 verify → store" step must
make an on-the-spot architecture decision that touches a hard NFR and the R19 double's
modeled surface. **Resolution (bounded):** name the read-hop as a header-carrying REST
secrets-read function in `management.go`, add the corresponding endpoint to
`infisicalFakeServer`'s modeled surface, and add it to the Phase-0 verification bucket
(it is the same shape as the three assumptions already parked there). This does not
alter any Decision's chosen approach; it fills an omission.

### Finding B (minor) — R9 auth-hop signature

As above: point the R9 reuse at `authenticateHTTP` (pair form), not `Authenticate`
(map form), or note that an `entry` map is assembled from the minted pair. Trivial,
but worth pinning so Phase 5 doesn't guess.

### Finding C (nit) — "R1–R23" framing

The onboard PRD is R1–R22. The "R23" reference in `root.go` belongs to the init PRD.
No coverage gap; just avoid implying an R23 requirement exists for this feature.

---

## Bottom line

PASS. The architecture is clear, the phases are correctly ordered, the code
foundations the design leans on all exist as described, and there is no structural
flaw or accidental complexity to remove. The single real gap (Finding A: R9's read-hop
function surface + its R19 double endpoint, under the R17 no-argv constraint) is
bounded and should be resolved in planning by adding a header-carrying secrets-read to
`management.go` and to the REST double, and folding it into the Phase-0 verification
list alongside the three assumptions already parked there.
