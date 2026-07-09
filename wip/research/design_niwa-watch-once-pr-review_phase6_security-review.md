# Security Review — DESIGN: niwa watch --once PR-review dispatch (Phase 6)

Verdict: **CONCERNS** (fixable at design stage; no reason to abandon the approach)

The threat model is well-framed and the four prior fixes are correct in intent.
The problem is not what the design contains but what it *delegates and assumes*:
niwa has no sandbox of its own (the design says so at line 48), so the entire
deterministic boundary rests on four unverified properties of the Claude Code
harness sandbox. Two "accepted residuals" understate risk because their
correctness depends on which of those properties actually holds. None of this is
a redesign; it's a set of must-verify items and two doc corrections.

---

## Headline finding: the boundary is delegated, and the design proves the wrong artifact

Line 48 states plainly that "the OS-level sandbox does not exist in the codebase
today." So `sandbox.enabled` / `sandbox.network.allowedDomains` are **Claude
Code harness settings**, not niwa-enforced controls. Every deterministic claim in
the doc ("denial is the sandbox's, not the model's") reduces to four assumptions
about the harness that the design states as fact but never verifies:

1. **Empty-allowlist semantics.** The design assumes `allowedDomains: []` means
   *deny all*. If the harness treats an empty/absent allowlist as *no
   restriction* (allow all), the containment is silently **inverted** — the
   worst possible failure mode, and it would pass every test the design lists
   (the settings stanza is present; only a live egress attempt reveals it).
   Must-verify, and the adversarial test (step 6 / AC9) must actually attempt
   egress and observe denial, not just assert the settings key.

2. **Fail-closed at launch, not just at preflight.** Decision 7's preflight
   *probes* that a sandbox can be created once, before any instance. The
   per-instance re-verify only re-reads the merged settings file. **Neither
   proves the sandbox is actually created and enforcing for the specific
   `claude --bg` launch.** If bwrap/sandbox-exec fails at launch time (kernel
   namespace exhaustion after N instances, seccomp denial, resource limits) and
   the harness responds by logging a warning and running uncontained rather than
   refusing, the fail-closed guarantee evaporates — and because the launch is
   `--bg --detach`, niwa never observes it. This is the single most important
   item to confirm: *does Claude Code refuse to run when `sandbox.enabled:true`
   but sandbox creation fails at runtime?* If it fails open, Decision 7's
   guarantee ("no uncontained session is ever launched") is overstated.

3. **Egress enforcement mechanism (namespace vs proxy).** The "Proxy TLS
   termination" residual implies egress is controlled by an HTTP(S) proxy. If
   that is the *only* mechanism (no network namespace with a default-deny
   route), then a subprocess that ignores proxy env vars and opens a **raw
   socket to an arbitrary IP** escapes entirely — this is not the "narrow
   SNI-evasion seam" the residual describes, it's a full hole. If, instead,
   egress is a network namespace whose only route is the proxy (everything else
   unreachable), then raw sockets fail and the residual is correctly narrow. The
   design's risk rating for this residual is only valid under the namespace
   interpretation. Must-verify which one the harness uses; the residual text is
   written as if the benign case is guaranteed.

4. **Re-verify checks niwa's file, not the harness's effective config.** The
   per-instance re-verify (Decision 7) re-reads *niwa's merged instance
   `.claude/settings.json`*. But the harness performs its **own** further merge
   at launch across managed/enterprise settings, user-level
   `~/.claude/settings.json`, project settings, and the `--settings` flag —
   which Decision 1B notes dispatch already injects for remote control, and
   which is typically highest precedence. Re-verifying niwa's file does not
   prove the *effective enforced* config. Two mitigating facts help: the
   synthetic `HOME` (Decision 3) neutralizes user-level `~/.claude/settings.json`,
   and deep-merge (not replace) means a `--settings` payload that omits
   `sandbox` leaves it intact. But the design should state these dependencies
   explicitly and confirm the `--settings` payload can never carry a
   sandbox-relaxing key, and that no managed/enterprise settings layer can
   override `sandbox.enabled`. As written, the design verifies an artifact one
   merge-step upstream of the one that governs enforcement.

Recommendation: add an explicit "harness sandbox contract" subsection that states
these four properties as *requirements on the harness*, and gate the feature on a
probe/test for each. The design's determinism claim is only as strong as the
weakest of the four, and right now all four are asserted rather than shown.

---

## Q1 — Attack vectors

### Fetch hardening (Decision 2) — mostly complete, two real gaps

The enumerated defenses (fetch by SHA, LFS smudge off, hooks off, submodule
recursion off, `protocol.ext`/`protocol.file` off, isolated gitconfig via
`GIT_CONFIG_NOSYSTEM=1` + relocated `HOME`, no filter-honoring checkout) cover the
standard checkout-time RCE surface well. `.gitattributes`-referenced filter
drivers are neutralized because the driver bodies must live in gitconfig, which
the isolated config removes; a globally-installed LFS filter is removed by the
same relocation (the `GIT_LFS_SKIP_SMUDGE` belt is redundant but harmless). "No
filter-honoring checkout" **is** achievable: fetch by SHA into a bare object
store and never invoke `git checkout`/`switch`/`worktree add`; that is the crux
and the design names it correctly as the sharpest implementation risk.

Two gaps the design does not cover:

- **Exposure primitive can hide files from review (`export-ignore`).** The design
  says expose the tree "by reading blobs by SHA / using a bare-style object
  store" but also says the agent reads "the diff ... from its local clone."
  These are in tension, and the tension matters: if the exposure primitive is
  `git archive`, it honors the `export-ignore` and `export-subst`
  `.gitattributes` — an attacker marks the malicious file `export-ignore` and it
  **vanishes from the reviewed tree while remaining in the merged PR**. This is a
  review-*evasion* vector, not an RCE one, and it survives all the listed
  hardening. Specify plumbing that ignores attributes (`git ls-tree` +
  `git cat-file`), not `git archive`, and add a fixture to the test.

- **Where the head SHA comes from, and the checkout tension.** The search
  (Decision 4) returns issues, not SHAs; niwa needs a `GET
  /repos/{o}/{r}/pulls/{n}` to learn the head SHA. That's platform-vouched, fine,
  and pinning to a SHA removes the poll→fetch TOCTOU on *content* (the reviewed
  bytes are exactly the SHA's). But note: pinning also means niwa may review a
  commit the human later sees superseded — a correctness/UX gap, not a
  containment one; worth a line. Separately, the doc should make explicit that
  "no filter-honoring checkout" is a constraint on the **trusted pre-sandbox
  fetch only**; once the sandbox is up, an in-session `git checkout` is fine
  (any smudge is contained and, under synthetic HOME, has no filter defined
  anyway). The current wording implies the *agent* is handed a bare store, which
  contradicts "reads local files" and over-constrains the in-sandbox side.

- **Minor/accepted:** malicious pack decompression (pack-bomb resource
  exhaustion) during fetch is unaddressed — a local DoS on the trusted side, low
  severity, fine to accept but worth naming.

### Sandbox escape via subprocess — see headline #3

Covered above: whether Bash-spawned subprocesses can egress depends entirely on
namespace-vs-proxy enforcement. If proxy-only, raw sockets escape. The design's
claim that the empty allowlist "blocks the agent's tool egress (Bash and its
subprocesses, alternate binaries, write-then-run) at the OS layer" is only true
under the namespace interpretation.

### The model channel — bounded, but the draft is a real second-order path

The model-API channel goes to Anthropic, not the attacker, so it is not a direct
exfil sink. The realistic path is: injected agent writes attacker-chosen bytes
into the **draft body**, developer approves, `niwa watch post` posts that body to
the PR where the attacker reads it. This is only dangerous if the session has
something sensitive to write — i.e., only if the sandbox/env scrub has a hole. So
it is a *second-order amplifier* of any primary containment failure, plus a
first-order **content-injection channel into the developer** (attacker-authored
prose the developer, or a downstream agent that ingests the draft, reads and may
act on). The design's "Draft text" residual acknowledges the human-checkpoint
half; it should also note the draft can carry prompt-injection aimed at whatever
processes it next, and that the "developer reads it" assumption is load-bearing.

Also relevant: the **one secret that is inside the blast radius** is the model
credential (the "Claude/Anthropic auth the model channel needs," Decision 3). The
design lists it as allowlisted-in but never acknowledges it as the single
in-session secret. If the sandbox holds it is unexfiltrable; if the sandbox has a
hole it leaks. And because the harness always-allows its own API endpoint, that
host is reachable from sandboxed subprocesses too — so "`allowedDomains` is
genuinely empty for the agent's tools" (line 115) is literally inaccurate: there
is exactly one always-reachable host (the API), it just isn't an attacker sink.
Recommend using an ephemeral/scoped model credential if the harness supports it,
and correcting the "genuinely empty" phrasing.

### Persisted records — sound

Draft-path traversal is validated inside the instance root; handle→record
mapping is niwa-generated, not attacker-influenced; records hold only
platform-vouched coordinates. No finding.

### Prompt-injection into the dispatch decision — well-bounded, with a nice property

The metadata-only prompt is a genuine strength, and the **workspace intersection
does more security work than the doc credits**: because `owner/repo` must match a
workspace repo to be dispatched, the attacker cannot smuggle instructions via a
crafted repo/owner name — only the PR number (an integer) is attacker-chosen.
Worth stating explicitly; it closes the one residual identifier-injection worry.
Reconstruct the URL from vetted `owner/repo/number` rather than trusting the
API-returned `html_url` for belt-and-suspenders.

### TOCTOU between re-verify and launch — see headline #2 and #4

Content TOCTOU is closed by SHA pinning. The live TOCTOU is: (a) preflight-probe
vs per-launch sandbox creation (#2), and (b) niwa's re-verified file vs the
harness's effective merged config (#4). Both are real and both are currently
unaddressed by the "re-read the merged document" step.

---

## Q2 — Are mitigations sufficient for identified risks?

For the risks the design *scopes to itself* (env allowlist, injection-proof
prompt, fetch hardening, dedup, post-step event pinning), yes — those are sound:

- Env **allowlist** (Decision 3) is correctly fail-closed and the canary test
  covers env vars plus on-disk sentinels. One inconsistency to note: the
  *filesystem* credential protection is described as "R7 write-scoping
  generalized to reads of credential paths" — a **deny-list of credential
  paths**, which is the same fail-open shape Decision 3B rejects for env. The
  real protection is the synthetic HOME (fail-closed: credentials-under-HOME
  simply aren't there); the path deny-list is belt-and-suspenders and shouldn't
  be leaned on for credentials outside HOME (`/etc`, system agents). Also, the
  fs policy cannot literally be "deny reads outside the clone" (the process must
  exec `/usr/bin/git`, load libs, resolve locale); the doc should reconcile
  "denies reads outside the clone/instance" with the reality that system paths
  must be readable — otherwise it reads as a stronger guarantee than is
  implementable.

- Post-step **event pinning** (Decision 6) is correct and important: fixing
  `event` in trusted code with a non-approving `COMMENT` default, treating the
  draft as opaque body, is the right shape. No finding beyond the draft-content
  note above.

For the risks the design **delegates to the harness**, the mitigations are
*asserted*, not sufficient-as-shown — see the four headline items.

---

## Q3 — Accepted residuals that are actually must-fix, and overstated claims

**Residual that is really a must-verify (not a benign accept):**

- **"Proxy TLS termination / domain-fronting ... narrow SNI-evasion seam."** As
  written this presumes egress is namespace-default-deny with one frontable host.
  Under a proxy-only model with no host allowed, the actual residual is
  raw-socket egress from subprocesses — categorically larger than SNI evasion.
  This cannot be "recorded, not closed" until the enforcement mechanism is
  confirmed. Reclassify as must-verify.

**Overstated claims to correct:**

- Line 115 / 397: "`allowedDomains` is genuinely empty for the agent's tools" —
  false in the presence of the always-allowed API endpoint; say "empty except
  the harness's own always-allowed model endpoint, which is not an attacker
  sink."
- Decision 7 / "no uncontained session is ever launched" — true only if the
  harness fails closed on runtime sandbox-creation failure (headline #2).
  Currently proven only for the *settings stanza*, not runtime enforcement.
- "Denial is the sandbox's, not the model's" — correct *aspiration*, but as
  delegated it depends on headline #1/#3; the doc presents it as established.
- Re-verify "asserts the sandbox stanza ... was not dropped or overridden by the
  merge" — asserts it for niwa's merge, not the harness's downstream merge
  (headline #4).

---

## Q4 — Escalate vs. correctly accepted for a first version

**Escalate / gate v1 on these (all cheap probes or one test each):**

1. Confirm `allowedDomains: []` = deny-all in the harness (else containment
   inverts).
2. Confirm harness fails closed when `sandbox.enabled:true` and sandbox creation
   fails at launch (not just at preflight).
3. Confirm egress enforcement is namespace-default-deny, not proxy-only (governs
   whether raw-socket subprocess egress is possible).
4. Confirm the harness's effective config (managed/enterprise + `--settings`
   flag precedence) cannot relax `sandbox.enabled`; re-verify against the
   effective config or prove equivalence to niwa's file.
5. Make the adversarial test (step 6) attempt a real egress and a real raw-socket
   connection and observe denial — not assert settings presence. Add an
   `export-ignore` review-evasion fixture and a "no working-tree checkout in the
   trusted fetch" assertion.

**Correctly accepted for v1:**

- Windows fail-closed (no staged reviews until later) — right call.
- Model-channel cost/DoS bounded by per-run bound + handled-set — fine to defer
  richer controls.
- Draft-text social engineering with the human as content checkpoint — acceptable,
  provided the "developer reads it" assumption is stated as load-bearing and the
  downstream-agent-ingestion note is added.
- Data-at-rest metadata under `.niwa/` — no secrets, fine.
- Pack-bomb fetch DoS — local, low severity, fine to accept once named.

---

## Bottom line

The design's *own* controls (allowlisted env + synthetic HOME, injection-proof
metadata prompt reinforced by the workspace intersection, event-pinned trusted
post, SHA-pinned inert fetch) are sound and the prior security pass genuinely
hardened them. The gap is that the deterministic boundary is delegated to the
Claude Code harness sandbox and the design asserts four harness properties
(empty=deny, launch-time fail-closed, namespace-not-proxy egress, effective-config
equivalence) as facts. Two "accepted residuals" (SNI seam; "no uncontained
launch") are only benign under the favorable reading of those properties. Convert
the four assertions into gated probes/tests, fix the `git archive`/`export-ignore`
exposure primitive, and correct the "genuinely empty allowlist" overstatement.
None require redesign. Verdict: **CONCERNS**.
