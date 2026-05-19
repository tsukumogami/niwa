# Phase 6 Second-Opinion Security Review — init bootstrap from empty source

Scope: re-check the `DESIGN-init-bootstrap-empty-source.md` against
the Phase 5 review (`wip/research/design_init-bootstrap-empty-source_phase5_security.md`).
This is NOT a fresh dimension-by-dimension review; it is a check that
nothing was missed and that the clarifications applied are airtight.

## Inputs verified

- The three Phase 5 clarifications have landed in the design:
  - Host-check-before-git-invocations stated in Solution Architecture
    (Key Interfaces, `RunBootstrap` docstring) and reaffirmed in
    Phase 4 deliverables.
  - Cleanup defer contract: `workspaceCreated = false` AFTER
    `RunBootstrap` returns success — codified in both the
    `RunBootstrap` docstring and the Data Flow trailing paragraph.
  - Plain `git commit -m "..."` (no `--author`, no `GIT_AUTHOR_*` /
    `GIT_COMMITTER_*` override) is documented in `RunBootstrap`'s
    "Git identity" paragraph and the Data Flow.
- These match the Phase 5 ask. No drift between Phase 5 outcome and
  Phase 6 inputs.

## Re-checking the threat model

### Q1. Attack vectors not considered in Phase 5

Walked the four trust boundaries:

1. **Attacker controls the `--from` URL** (the user pastes it from
   the internet). Phase 5 covered remote-helper exploitation. I
   additionally walked URL-shape spoofing through the slug parser:
   - `parseInitSource` routes URL-shaped inputs to
     `workspace.ParseSourceURL` → `parseOverlaySlug`
     (`internal/workspace/overlaysync.go:65`).
   - `parseOverlaySlug` accepts `file://`, `https://`, `http://`,
     `git://`, `git@`, and bare absolute paths. It extracts the
     host from the URL's authority component via
     `strings.SplitN(withoutScheme, "/", 3)[0]`.
   - The host check then runs on `src.IsGitHub()`, which returns
     true iff `Host == ""` (slug shorthand defaults) or
     `Host == "github.com"` (verbatim string match, case-sensitive).
   - Spoofing variants tested mentally (`github.com.evil.com`,
     `github.com:1234@evil.com`, `GITHUB.COM`, `gıthub.com`
     punycode dotless-i, `https://X.github.com/owner/repo`) all
     parse to a non-empty `Host` that does not byte-equal
     `"github.com"`, so `IsGitHub()` rejects them.
   - Conclusion: the host check as specified (operating on the
     parsed `Source`, not on the raw `cloneURL`) closes URL-shape
     spoofing in v1.

2. **Attacker controls the GitHub API response**. Phase 5 noted
   that the only data crossing from the GitHub API into a durable
   artifact is the `private` bool, normalized to a closed-set
   string (`"public"` / `"private"`). I re-verified the existing
   normalization site at `internal/github/client.go:92-100` and
   the design's Decision 4 statement
   "reusing the `private` bool → `Visibility` normalization that
   `ListRepos` already does". The contract is that the JSON
   `visibility` string is OVERWRITTEN if empty, but a malicious
   API host could send `"visibility": "</toml-poison>"` and the
   normalizer would leave it intact. This is a low-severity
   residual — the only consumer that touches `Visibility` for the
   scaffold is `ScaffoldFromSource`, which the design specifies
   reuses the `private` bool path. The implementation must
   actually derive `Visibility` from `Repo.Private` rather than
   `Repo.Visibility` to be safe. Flag for Phase 4 reviewers.

3. **Attacker controls the user's git identity**. Phase 5 covered
   the `--author` / `GIT_AUTHOR_*` invariant. I re-verified that
   the design's `RunBootstrap` docstring forbids both. No
   additional vector here.

4. **Attacker on the network path** (HTTPS MITM). Phase 5 did not
   discuss this. It is genuinely out of scope: niwa trusts the
   system's TLS root store + git's HTTPS implementation. The
   bootstrap path inherits the same trust model as `git clone` and
   today's `MaterializeFromSource`. Worth a sentence in the design
   noting "no transport-layer hardening beyond what git does
   natively" only if the doc claims a broader guarantee — it
   doesn't.

### Q2. Are mitigations sufficient — "GitHub host only" enforceable?

The host check operates on the parsed `Source.Host` string. Walking
all the `--from` input shapes through `parseInitSource`:

- `org/repo` → `source.Parse` → `Source{Host: ""}` → IsGitHub true,
  cloneURL synthesized via `ResolveCloneURL` to `https://github.com/...`.
- `host/owner/repo` (slug shorthand with host) → `Parse` parses
  host into `Source.Host`. IsGitHub true only for `Host == "github.com"`.
- `https://...` / `http://` / `git://` → `parseOverlaySlug` extracts
  the URL host into `Source.Host`. IsGitHub true only when the
  literal byte string is `"github.com"`. **Note**: case-sensitive
  comparison means `Https://GITHUB.COM/X/Y` parses to `Host = "GITHUB.COM"`
  and is rejected. This is correct (a non-GitHub host could legally
  redirect `GITHUB.COM` if DNS is poisoned, but rejection at this
  layer means git never sees the URL).
- `git@host:org/repo` SSH → `parseOverlaySlug` SSH branch. IsGitHub
  true only for `host == "github.com"`.
- `file://` / bare absolute path → IsGitHub false (Host set to a
  `file://` prefix). Rejected.

I also walked the `ResolveCloneURL` pass-through (`clone.go:90-112`):
for URL-shaped input it returns the URL verbatim. So the cloneURL
that reaches `git fetch` is exactly the user's `--from` string for
any URL-shaped input. The host check is the ONLY thing standing
between a user-typed URL and `git fetch`. Phase 5 documented this.
Sufficient given the parsed-Source semantics; no implementation
gap visible from the design.

One subtle interaction: the host check runs on `src.Host`, but the
URL passed to `git fetch` is whatever `ResolveCloneURL` returns —
which for URL-shaped input is the raw `--from` string verbatim.
If the parser and `ResolveCloneURL` disagree on what counts as
"github.com", the check could be bypassed. They do NOT disagree
today (both use literal byte-string equality with `"github.com"`),
but this coupling is implicit. **Recommend the design state that
`RunBootstrap` checks `src.IsGitHub()` AND that the implementation
uses `src.CloneURL(...)` rather than re-running `ResolveCloneURL`
on the raw input**, so the URL handed to git derives from the
already-validated Source. Today's init flow uses `ResolveCloneURL`
on the raw `source` at `init.go:254`, which means the cloneURL
passed to `MaterializeFromSource` (and onward to `RunBootstrap`)
is the verbatim user input for URL shapes — fine, since the host
check on `src` already rejected anything non-GitHub, but the
implementer needs to know this invariant.

### Q3. N/A justifications re-checked

Re-validating each "not applicable" or "not introduced" call:

- **Command injection in git invocations**: airtight. All git
  calls go through `exec.CommandContext("git", args...)` with
  arguments as separate elements. The only user-derived string
  is the cloneURL; the branch name (`niwa-bootstrap`) and commit
  message (`"Initial niwa workspace config"`) are
  niwa-controlled constants. No shell anywhere in the path. I
  cross-referenced `internal/workspace/clone.go:63` as the
  established pattern the design points to. Confirmed.

- **Scaffold content influenced by remote data**: airtight IF
  the implementation derives `Visibility` from `Repo.Private`
  (bool → enum-string) and not from `Repo.Visibility` (raw API
  string). The design says it does (Decision 4 commentary), but
  this is a load-bearing invariant for the security claim. Flag
  for Phase 4 implementation review.

- **Git hooks from the cloned remote**: airtight. `git fetch`
  into a `git init`-ed directory does not transfer hooks. The
  local `.git/hooks/` directory is populated from the local git
  template directory, not the remote. The first `niwa apply`
  against the scaffolded `workspace.toml` runs nothing — no
  hooks, no plugins, no marketplace fetches, no vault — because
  Decision 4 explicitly emits no `[claude.hooks]` /
  `[claude.plugins]` / `[claude.marketplaces]` / `[vault.*]`
  entries. The user must subsequently uncomment and configure
  them before any executable side effect occurs.

- **Confused-deputy via piped stdin**: airtight. The TTY gate
  uses `IsStdinTTY()`; piped stdin reaches the non-TTY refusal
  branch, not the prompt.

- **Token handling**: NOT fully airtight. Phase 5 flagged the
  `NIWA_GITHUB_API_URL` override as existing-not-introduced. The
  bootstrap path adds one more caller (`GetRepo`) that inherits
  this behavior. I confirmed via `internal/github/client.go:41-50`
  that the env var is read unconditionally at API client
  construction time and applies to every method call including
  the new `GetRepo`. See Q5 below for whether this is a new vector
  or an existing one.

### Q4. Residual risks worth escalating

Phase 5 flagged the `NIWA_GITHUB_API_URL` override as residual. I
looked for similar-shape residuals:

- **`GIT_*` env vars**: `git init`, `git fetch`, `git checkout`,
  `git add`, `git commit` all honor `GIT_DIR`, `GIT_WORK_TREE`,
  `GIT_CONFIG_*`, `GIT_SSH_COMMAND`, and dozens of others. A user
  with hostile env (e.g., `GIT_SSH_COMMAND="rm -rf ~"`) would have
  their bootstrap subverted. This is the same trust model as
  every other niwa command that invokes git (today: `clone.go`,
  `sync.go`, the materialize swap). NOT introduced by this design;
  the same residual class as `NIWA_GITHUB_API_URL`. Worth one line
  in Security Considerations acknowledging "the bootstrap path
  inherits the standard git env-var trust model; a hostile
  `GIT_*` environment subverts bootstrap the same way it subverts
  every other git command."

- **`HOME` / `XDG_CONFIG_HOME` override**: `git` reads
  `~/.gitconfig` for `user.name` / `user.email` (the bootstrap
  commit identity). A user who has shadowed `HOME` could
  unwittingly commit under an attacker-supplied identity. Same
  class as above; not introduced.

- **`PATH` poisoning**: if `PATH` resolves to a malicious `git`
  binary, the entire flow is compromised. Same class as every
  other shell tool. Not introduced.

None of these need design changes. A single sentence in Security
Considerations grouping them as "inherited from the host git
toolchain" would close the loop.

### Q5. Token exposure — slug misroute scenario

The user's question: if `--from github.com/X/Y` is passed and the
slug parser somehow misroutes to a different API host (via
`NIWA_GITHUB_API_URL` or otherwise), could the token leak?

Walking it carefully:

- `--from github.com/X/Y` (slug shorthand with explicit host
  segment) → `source.Parse` → parts=["github.com","X","Y"] →
  `containsDot("github.com")` true → `Host="github.com"`,
  `Owner="X"`, `Repo="Y"`. `IsGitHub()` returns true.
- `RunBootstrap` host check passes.
- `GetRepo(ctx, "X", "Y")` is called via the `APIClient`. The
  client's `BaseURL` is determined at construction time
  (`NewAPIClient` → reads `NIWA_GITHUB_API_URL` → default
  `https://api.github.com`).
- The API call URL becomes `<BaseURL>/repos/X/Y`. The bearer
  token (from `resolveGitHubToken()`) is attached to the request.
- **If `NIWA_GITHUB_API_URL=https://evil.com` is set, the token
  is sent to evil.com.**

This IS a real path to token exposure, but it requires the user
(or their shell init) to have set `NIWA_GITHUB_API_URL` to a
hostile value. The slug parser does NOT misroute — the misroute
is purely in the API client's base URL. So the question's premise
"the slug parser somehow misroutes" is false. The slug parser
correctly classifies `github.com/X/Y` as a GitHub source; the
token leak comes from the existing `NIWA_GITHUB_API_URL` knob.

This is exactly the residual Phase 5 flagged. The bootstrap path
does add one more `NIWA_GITHUB_API_URL`-honoring caller, but it
does not create a new vector — `ListRepos`, `FetchTarball`,
`HeadCommit`, and the new `GetRepo` all read from the same
`APIClient.BaseURL`. **Verdict: not a new risk; the Phase 5
"existing residual" framing is accurate.**

The hardening Phase 5 suggested ("warn at niwa startup when
`NIWA_GITHUB_API_URL` is set") is still the right follow-up, and
it scales linearly with the number of token-sending callers —
adding `GetRepo` makes the warning marginally more valuable, not
qualitatively different.

## Items worth surfacing before approval

In priority order, what should change before approval:

1. **Implementation invariant: `Visibility` derives from
   `Repo.Private`, not `Repo.Visibility`** (Q3, scaffold-content
   dimension). The design states this in Decision 4 commentary
   but does not state it as a load-bearing security invariant. A
   one-line addition to `ScaffoldFromSource`'s docstring
   ("Visibility must be derived from `Repo.Private` (bool) and
   normalized to `\"public\"` or `\"private\"`; do not pass the raw
   API `visibility` field, which is a remote-controlled string")
   would protect this from drifting during implementation.

2. **Document the cloneURL = src.CloneURL invariant** (Q2). State
   in the `RunBootstrap` docstring or Solution Architecture that
   the URL handed to `git fetch` MUST be derived from the
   validated `Source` (either via `src.CloneURL(protocol)` or by
   continuing to call `ResolveCloneURL` on the raw input — both
   work today since they agree on what counts as github.com).
   This prevents a future refactor from accidentally widening the
   gap between "what was validated" and "what git sees."

3. **Note the inherited-git-environment trust model** (Q4). One
   sentence in Security Considerations residual section,
   grouping `NIWA_GITHUB_API_URL`, `GIT_SSH_COMMAND`,
   `GIT_CONFIG_*`, `HOME`, `PATH`, etc. as "inherited from the
   host environment; a hostile env shell subverts bootstrap the
   same way it subverts every other niwa or git command in this
   process." This is mostly housekeeping — it makes explicit
   that v1 does not add hardening for the existing residual
   class.

None of these block approval. They are clarifications that
protect already-correct choices during implementation. If the
design owner prefers to bake (1) into Phase 4's deliverables list
("ScaffoldFromSource derives Visibility from Repo.Private; tests
assert this") instead of the docstring, that works equally well.

## Verdict

The design is structurally sound. Phase 5's three clarifications
landed cleanly. The remaining items are documentation-grade
tightenings around invariants the design already implicitly
relies on. No architectural changes needed. No N/A justification
is unjustified — the only one with implementation-level fragility
is the visibility-derivation invariant, which is one line of
docstring away from being load-bearing.

Recommended outcome: **proceed to approval with the three
documentation tightenings above noted as Phase 4 implementation
checklist items**.
