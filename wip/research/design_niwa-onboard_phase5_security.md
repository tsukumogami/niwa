# Security Review: niwa-onboard

## Dimension Analysis

### External Artifact Handling

**Applies:** Yes.

The wizard parses two classes of external input into decisions: REST responses
from the new Infisical management client (`ReadIdentity`, `MintClientSecret`,
`RevokeClientSecret`, plus the reused `Authenticate` login exchange) and the
output of `infisical` CLI subprocesses (`login`, `secrets folders create`,
`secrets set`, `export`, `user get token` / `login status`).

Risks and mitigations:

- **Malformed or adversarial REST responses driving wrong branch decisions.**
  The topology inference (Decision 3) classifies a 403/org-mismatch response
  differently from a generic not-found, and mint/verify responses are parsed
  into typed fields. The existing `infisical.go`/`auth.go` precedent (checked
  JSON unmarshal, explicit missing-field errors, no silent zero-value fallback)
  is the model the new `management.go` functions follow per the design's
  Decision 4, and R19's fault-injection matrix (wrong-org, mint rejection,
  plan-gate, login-exchange failure, revocation failure) exercises the
  classification paths in tests. Severity: **low**. A malformed body causes a
  classified error and a non-zero exit code, not code execution or a silent
  wrong-branch success — worst case is a confusing prompt, not a security
  breach.
- **CLI subprocess output.** `subprocess.go`'s existing hygiene (`cmd.Env =
  nil`, full-buffer stdout/stderr capture, no streaming to niwa's own stdio)
  already governs every CLI delegation the wizard adds. Argv is built from
  fixed flag names plus config-sourced values (project id, environment slug,
  path) — never from response bodies — so there is no argv-injection surface
  from external content. Severity: **low**, inherited posture, not a new risk
  this design introduces.
- **No code execution or file-write path is driven by unvalidated external
  content.** The wizard writes exactly two kinds of files (the personal-overlay
  TOML block and the R20 record), and both are constructed by niwa from
  config/response fields it validates, never from raw external bytes written
  through.

No changes required beyond what R19's test-double fault-injection already
plans to cover in Phase 1.

### Permission Scope

**Applies:** Yes.

The wizard's write surface: the operator's personal-overlay git clone (commit,
no push), `~/.config/niwa/` (the R20 record and the overlay pointer), and the
vault itself (via `infisical secrets folders create` / `secrets set`, both
riding the operator's own CLI session). It also reads and briefly holds the
operator's live Infisical session bearer (Decision 4's `session.go`) to build
`Authorization` headers for the management REST calls.

- **Git writes are appropriately scoped.** Site 1 (personal overlay) commits
  locally and never pushes, mirroring the audited `RunBootstrap` /
  `TestRunBootstrap_R24_NoPush` precedent — an unpushed local commit in the
  operator's own low-governance repo is a safe, reversible action. Site 3
  (team-config repo) is render-only with zero git writes, which is the correct
  posture for a review-gated shared repo the wizard has no standing to commit
  against (Decision 5, correctly rejecting a uniform commit-everywhere rule).
  Severity if this boundary were reversed: **high** (an unpushed commit landing
  silently in a shared clone could be mistaken for a merged change) — but the
  design gets this right.
- **The R20 record file is scoped correctly.** `0600`, own directory,
  open-then-rename, non-secret content (`{secret_id, recoverable}`). No
  concern.
- **The operator's own live session bearer is the highest-privilege secret
  this feature handles** — broader in scope than the narrowly-purposed
  minted client secret, since a session bearer is whatever the operator's
  `infisical login` session can do. R8/R17 correctly forbid custodying it
  (env var or CLI session file only, never `--token`), and it's used
  transiently to build request headers, never written to disk or argv. This
  is the correct model, but it raises the stakes on *where* that bearer is
  sent — see Supply Chain / Data Exposure below, where the one real gap in
  this design lives.
- **No privilege escalation via the team path.** Folder creation is the one
  automated team-phase action, and it is a narrow, additive, idempotent
  operation with a landing check before/after — not a vector for producing
  unintended grants.

### Supply Chain or Dependency Trust

**Applies:** Yes — this is where the review's one substantive finding is.

- **The `infisical` CLI on PATH is trusted**, exactly as it already is for
  every existing `niwa apply` vault resolution. This design adds no new trust
  assumption here; a compromised `infisical` binary earlier on PATH is a
  pre-existing risk to the whole vault-integration feature, not something
  `niwa onboard` introduces or worsens.
- **`api_url` is sourced from workspace-vault-provider config, with no
  validation before it receives the operator's bearer.** Decision 4 states
  `resolveAPIURL` follows precedence: an explicit config-declared value wins,
  then `NIWA_INFISICAL_API_URL` (test-only), then the cloud default. The
  config-declared value comes from the **team's workspace source repo** — a
  shared, review-gated repo, but one where R14 explicitly requires the
  workspace to be able to declare a non-default `api_url` (for self-hosted
  Infisical instances). Nothing in the design validates the resolved value
  (scheme, host allowlist, or even a simple "does this match the well-known
  default" check) before `ReadIdentity`/`MintClientSecret`/`RevokeClientSecret`
  send the operator's own live session bearer to it as an `Authorization`
  header.

  The attack: a malicious or compromised entry in the team-config repo (a bad
  PR that slipped review, or a compromised admin overlay) points `api_url` at
  an attacker-controlled host. The next developer who runs `niwa onboard`
  silently sends their own real Infisical session bearer — the
  highest-privilege credential this feature ever touches — to that host. This
  is a credential-exfiltration path via what is nominally just an endpoint
  configuration field, and it is qualitatively different from the credential
  fields the design already scrutinizes closely (`client_id`/`client_secret`),
  because `api_url` is not treated as security-sensitive input anywhere in the
  PRD or design text.

  **Severity: medium-high.** It requires an already-compromised or
  carelessly-reviewed team-config repo to trigger (the same review gate that
  protects every other team-config value), so it is not a zero-click remote
  attack — but the payoff (the operator's own live session bearer, not a
  narrowly-scoped machine credential) is high, and the mechanism is silent:
  nothing in the current design surfaces the resolved `api_url` to the
  operator before it's used, so a reviewer who misses a subtle `api_url`
  change during team-config PR review gives the operator no second chance to
  notice at wizard-run time either.

  This is a design gap I've flagged as **Option 1** below, not merely a
  documentation note, because closing it is a small, mechanical addition (a
  confirm/display step in an already-planned prompt) rather than a new
  subsystem.

- **No third-party library supply chain concern.** The design explicitly rules
  out new dependencies (no TUI/prompt library); the wizard builds on stdlib
  `bufio`/`net/http`/`os/exec` and `golang.org/x/term`, already vetted and in
  `go.mod`.

### Data Exposure

**Applies:** Yes.

- **Client secrets and the session bearer.** R17 (carried verbatim) and its
  AC-27/AC-28/AC-29 test plan are strong and specific: redactor registration
  the instant a secret is obtained, headers-only for REST, stdin/`0600`-temp
  for the CLI store path, `secret.Errorf` on every credential-touching error
  path, and a canary-based test asserting no secret reaches any output
  surface (stdout, stderr, logs, `--json`, error chains). This is the same
  discipline already proven in `auth.go`'s `Authenticate`/`scrubResponseBody`,
  reused rather than duplicated per Decision 4 — which is also why it's
  trustworthy: it's one tested scrubbing sequence, not a second
  reimplementation that could drift.
- **One narrow gap in the redaction surface as currently described:** R17's
  text calls out scrubbing "mint, verify, and login response bodies" but the
  design's Solution Architecture doesn't explicitly extend the same
  scrub-before-error treatment to the **`infisical secrets set` (store) CLI
  subprocess's own stdout/stderr**, the way `runInfisicalExport` already
  applies `vault.ScrubStderr` to the export path. If the real `infisical` CLI
  ever echoes part of what it just stored (e.g., in a verbose success message
  or a validation error naming the value), that output flows through the same
  `commander` capture but isn't named in the design as a scrub point.
  Severity: **low** (the CLI has no documented reason to echo secret values on
  `secrets set`, and stdin-fed secrets are the standard hygiene control), but
  it costs nothing to make explicit and test, given the pattern already exists
  one function away.
- **The R20 record and `--json`/identifier output are correctly non-secret.**
  `secret_id` is an opaque identifier consumed only for a DELETE call that
  itself requires the operator's bearer to succeed — knowing the id alone
  grants no capability. `client_id`, identity id, key path, and status
  vocabulary are explicitly the only fields allowed in `--json` and guided
  instructions, matching the existing `Origin` metadata model in
  `internal/secret` (non-secret-by-design fields separated from plaintext
  bytes).
- **Terminal/guided-dashboard output** only ever echoes config-sourced,
  non-secret identifiers (identity name, auth method, environment slug) per
  R14 — no risk.

### Custody Boundary and Credential Flow (domain-specific)

**Applies:** Yes.

- **Is the custody boundary (R5) actually preserved by the design's
  mechanics, not just its prose?** Mostly yes, with the one caveat above.
  - Team phase: the only automated call is `infisical secrets folders
    create`, which is a CLI delegation riding the operator's own session —
    never a management REST call. This is enforced two ways: a runtime
    request recorder (AC-10, asserting zero calls to any identity/org/project
    management endpoint on the team path) and a static call-site lint test
    (failing if `ReadIdentity`/`MintClientSecret`/`RevokeClientSecret` is
    reachable from team-phase code). The design is honest that this is a
    documentation-and-lint boundary, not a compiler-enforced one — an
    accepted, reasonable trade-off given Go's lack of visibility scoping
    finer than package, and the mitigation (recorder + lint + doc comment) is
    proportionate.
  - Individual phase: `ReadIdentity`/`MintClientSecret`/`RevokeClientSecret`
    are real management-REST calls, but every one authenticates with the
    operator's own bearer (never a niwa-held admin token), and the scope is
    bounded to the one carve-out #194 already validated (mint on an existing
    identity; never create). This matches R5's letter and spirit.
- **The credential store path (stdin to `infisical secrets set`) never puts
  the secret on argv or in an intermediate file**, matching R17/AC-28
  directly, and is consistent with the CLI-delegation hygiene already
  established for `export`.
- **The one place a secret could leak into argv, env, logs, or a commit:**
  none, by the design's own mechanics — argv is fixed-flag-plus-config-value
  only, `cmd.Env = nil` (inherit, never extend), and the only git-committed
  artifact (the personal-overlay TOML block) carries provider *configuration*
  (kind, project, api_url when non-default) — never `client_id` or
  `client_secret`, which live only in the vault via `infisical secrets set`.
  This separation (config in git, credential in vault) is correctly
  maintained throughout.
- **The genuinely new risk this feature introduces to the custody model** is
  not a leak of the minted client secret (that surface is well-guarded and
  well-tested) — it's the operator's own broader session bearer being routed,
  based on config the wizard trusts without validation, to a REST endpoint
  the operator never explicitly confirmed. That's the Supply Chain finding
  above, restated in custody terms: the custody boundary says "every
  privileged step runs against the operator's own session" as a *safety*
  property, but that safety property depends on the request actually going to
  the real Infisical service — an assumption this design doesn't currently
  check or surface.

## Recommended Outcome

**OPTION 1 - Design changes needed:** Add a small, mechanical safeguard around
`resolveAPIURL` (Decision 4) before it's wired to carry the operator's bearer:

1. When the resolved `api_url` is non-default (i.e., a workspace-config value
   overrides the Infisical cloud default), the wizard MUST display the
   resolved value to the operator as part of an existing confirm gate — the
   natural point is the same setup/topology confirmation prompt Decision 3
   already introduces (`Confirm`/`Select`), so this needs no new prompt
   primitive, only an additional line of text and a mandatory pause before the
   first call that carries the bearer (`ReadIdentity`). This gives the
   operator a last-look chance to catch a malicious or mistaken `api_url`
   even if it slipped past team-config review, without adding new interaction
   shapes to the prompt kit.
2. Optionally (defense in depth, not a hard requirement): a minimal scheme
   check — reject or warn on a non-`https` `api_url` — costs one `strings`
   check and closes the cheapest variant of the attack (a plain-`http`
   redirect target) outright.
3. Extend the R17-style scrub discipline explicitly to the `infisical secrets
   set` subprocess's stdout/stderr (mirroring `runInfisicalExport`'s
   `vault.ScrubStderr` application), so the store path has the same
   belt-and-suspenders treatment as the export path already has, even though
   no secret is expected to appear there today.

These are additions to Decision 4's `resolveAPIURL` text and to the R17
carry-forward list in the design doc, not a new subsystem or architecture
change — they fit inside the wizard's existing prompt-kit and scrubbing
machinery. Everything else reviewed (custody boundary, credential-store
hygiene, R20 record scope, data-exposure test plan) is sound as designed and
needs no change.

## Summary

The design's core custody model is sound: every privileged call rides the
operator's own session, the minted-secret handling and stdin-fed store path
meet R17's secret-hygiene bar with a strong test plan (AC-27/28/29), and the
team-phase boundary (no management REST calls) is enforced by both a runtime
recorder and a static lint test. The one real gap is that `resolveAPIURL`'s
config-declared override is used to route the operator's live session bearer
without any validation or operator-visible confirmation, creating a quiet
credential-exfiltration path if a malicious `api_url` ever reaches team
config. The fix is small — surface the resolved non-default `api_url` at the
existing confirmation prompt, optionally enforce `https`, and extend the
already-proven `ScrubStderr` pattern to the store subprocess — and should be
folded into Decision 4 and the R17 carry-forward before this design leaves
Proposed status.
