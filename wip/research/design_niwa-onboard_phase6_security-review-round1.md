**Verdict:** FAIL

# Phase 6 Security Review: niwa-onboard design

Reviewed: `docs/designs/DESIGN-niwa-onboard.md` (Security Considerations plus the
mechanics it depends on in Decisions 3, 4, 5 and the data-flow diagrams) against
the Phase 5 security research (`wip/research/design_niwa-onboard_phase5_security.md`).

The core custody model is sound and the Phase 5 review is a good piece of work:
the operator's-own-session invariant holds, the minted-secret hygiene (R17) is
strong and reused rather than reimplemented, and the team-phase no-management-REST
boundary is enforced by a runtime recorder plus a static lint. My disagreement is
narrow but load-bearing: the Phase 5 finding (unvalidated `api_url` exfiltrates the
operator's session bearer) was accepted as *closed* by the confirm-gate mitigation,
but the mitigation as written does not actually hold in two modes, and it is
internally contradicted by the detection flow. That is a must-fix-before-planning
gap, so this review returns FAIL. The fixes are small and local; the architecture
is fine.

---

## Must-fix gaps (reason for FAIL)

### F1. The `api_url` confirm gate fires *after* the first bearer-carrying call, not before it — the two decisions contradict each other

This is the headline. Decision 4 states the guard runs "before the first
bearer-carrying call (`ReadIdentity`)... at the existing setup/topology confirm
gate." But Decision 3 makes `ReadIdentity` the *detection* call: "perform the
GET-identity call... **with the operator's active session**, and interpret... the
shape of a failure on this same call... doubles as the topology signal." The
data-flow sequence diagram (lines 933-936) confirms the real order:

```
W->>W: TTY gate + preconditions
W->>M: ReadIdentity(identityID) [R8.1]      <-- bearer sent to config-sourced api_url
M-->>W: client_id (403/wrong-org -> exit 4)
W->>Op: Confirm setup + topology            <-- the gate the guard is bolted onto
```

`ReadIdentity(ctx, apiURL, bearer, identityID)` carries the operator's live session
bearer to the config-sourced `api_url`. Topology detection *needs that call's
result* to form the confirm prompt, so the call must precede the confirm — which
means the bearer reaches a possibly-hostile `api_url` **before** the guard the
design relies on to protect it. Decision 4's "confirm before the first
bearer-carrying call" is therefore impossible as written: the first bearer-carrying
call is the detection call, and it happens first.

Consequence: as specified, a hostile `api_url` that slipped past team-config review
exfiltrates the bearer during detection, exactly the attack Phase 5 flagged and
believed closed. The confirm gate never gets a chance to run first.

Fix: hoist the `api_url` validation ahead of *detection*, as its own gate at wizard
entry (right after `resolveAPIURL`, before any call that carries the bearer),
decoupled from the setup/topology confirm that depends on detection results. The
non-`https` reject in particular must run before the first HTTPS/HTTP request is
built. This is a small reordering, but the design text has to say it, because a
faithful plan of the current text ships the hole.

### F2. The guard has no defined behavior in the scriptable non-TTY / `--json` path — the one mode where the attack is most valuable

The design's own "Interactive only" consequence says the wizard runs when a TTY is
present **"or explicitly supplied inputs"** are given. With
`--individual --split-login`, inputs are supplied, so the non-TTY path does *not*
fail fast (R18 fail-fast only triggers when the needed override is *absent*) — it
proceeds non-interactively. But the `api_url` mitigation is a `Confirm` prompt on
the TTY-or-override gate, and there is no `api_url` override flag. So in the
scripted path the mandatory confirm has nothing to prompt against and its behavior
is undefined: either it is silently skipped (reopening F1's exfiltration for every
CI/automation run) or it blocks a run that was meant to be non-interactive.

This matters more than the interactive case, not less: a scripted `niwa onboard` in
a CI runner or bootstrap script is where a bearer is handed over with no human
watching the confirm line at all.

Fix: specify the non-TTY contract for a **non-default** `api_url`:
- non-`https` `api_url` is an **unconditional hard reject** in every mode (the
  design currently says "rejected (**or warned on**)" — "warn" in non-TTY equals
  "silently proceed," which is not a mitigation);
- a non-default (even if `https`) `api_url` over non-TTY must fail fast (exit 2 /
  R18) unless an explicit acknowledgment override is supplied — it must never be
  silently accepted because there is no operator to confirm it.

### F3. REST-returned and config-sourced string fields are interpolated into TOML/URLs with no stated encode-or-validate step — an injection surface Phase 5 rated "no injection surface / low"

Phase 5's External-Artifact dimension concluded "no argv-injection surface... never
from response bodies" and rated it low. That is correct for *argv* but overlooks two
non-argv sinks:

1. **The stored credential body.** The wizard assembles a TOML body
   (`version`, `client_id`, `client_secret`) embedding the `client_id`/`client_secret`
   returned by `MintClientSecret` and the `project`/`api_url` from config, then feeds
   it to `infisical secrets set` over stdin. The design deliberately distrusts
   BurntSushi's marshaler and hand-builds TOML by string surgery for config authoring
   (Decision 5), which raises the odds the credential body is *also* built by string
   formatting. A field containing `"`, newline, or `]` would break the body or inject
   additional keys/tables — landing a malformed credential that fails silently at a
   later `niwa apply`, the exact failure mode the feature exists to kill. The design
   claims the credential is "correct by construction," but that property only holds if
   the interpolated values are TOML-encoded or validated (`^[A-Za-z0-9._-]+$`-style)
   before embedding.
2. **The committed overlay `niwa.toml`.** The surgical table insert writes
   `kind`, `project`, and `api_url` (when non-default) into a file that is then
   git-committed. `api_url` is the one attacker-influenceable field (team config). A
   value like `https://ok.example"]` + newline + `[evil]` string-inserted into the
   overlay injects structure into the operator's personal repo. This is first-order:
   it needs only a confirmed-but-hostile `api_url`, and it compounds with F4 (a
   confirm line whose rendering can be spoofed).

Fix: state normatively that every REST-returned or config-sourced value interpolated
into a TOML body or a URL path is TOML-encoded / percent-escaped / character-validated
before embedding, and add a hostile-character test (a `client_id` / `api_url` carrying
`"`, newline, `]`) to the AC-15/16/17 store-shape and the Decision-5 config-authoring
test surface. Same rule for `secret_id` before it is placed in the `RevokeClientSecret`
DELETE path.

---

## Strong recommendations (should land, not FAIL blockers on their own)

### R1. Sanitize control/ANSI bytes and address homographs in everything echoed to the terminal — this directly protects the F1/F2 mitigation

Phase 5 has no dimension for terminal-output safety, and it is the soft underbelly
of the whole `api_url` mitigation. The design's defense is "**display** the resolved
`api_url` and let the operator catch it." But:
- config-sourced values (identity name, environment slug, and the `api_url` line)
  are echoed raw. ANSI/control sequences (cursor-up + overwrite, CR) let a hostile
  team-config value redraw the confirm line so the operator sees a benign URL while
  the bearer goes elsewhere;
- a homoglyph host (Cyrillic `о` in `infisicаl.io`, or punycode) reads as legitimate,
  defeating the last-look defense that F1/F2 depend on.

Fix: strip/escape non-printable and control bytes from all config- and
response-sourced strings before printing (guided instructions *and* the confirm
line); display the `api_url` host in an ASCII/punycode-normalized form so lookalikes
are visible. Without this, the mitigation Phase 5 leans on is weaker than claimed.

### R2. Make the overlay `niwa.toml` write atomic and narrow the landing-check -> write TOCTOU

The R20 record and credential temp files are explicitly `0600` open-in-dir-then-rename,
but the Decision-5 overlay config write is described only as "append" / "replace the
span." An in-place truncate+rewrite corrupts the operator's overlay on a mid-write
crash, and the read-then-write window (landing check reads the file, computes a span,
writes) can, under a concurrent writer, produce the duplicate-top-level-table state
the design elsewhere treats as a *safety* backstop rather than an outcome it can
cause. Severity is low (single-operator, low-concurrency), but the fix is free:
write the overlay via the same temp+rename discipline already used for the R20 record,
and state the single-writer assumption explicitly.

### R3. Pin down what "reachable from team-phase code" means for the AC-10 lint

The static call-site lint "fails if `ReadIdentity`/`MintClientSecret`/`RevokeClientSecret`
is reachable from team-phase code." A true Go reachability analysis is nontrivial and
a shallow grep misses indirect dispatch (function values, interface methods). The
runtime request recorder is the load-bearing check and catches actual calls
regardless of path, so the pair is sound — but the design should say the lint is a
direct-call-site check that does not catch indirection, so no one over-trusts it.

---

## Answers to the four questions

**1. Attack vectors not considered.** The prompt's list maps to findings as follows:
- `infisical` from PATH — correctly dispositioned as inherited/pre-existing; a
  compromised binary earlier on PATH is a whole-feature risk, not one onboard adds.
  Low, no change. (Worth one line confirming the new CLI delegations resolve the
  binary the same way the existing `export` path does — no new PATH lookup with a
  different CWD.)
- REST responses -> shell-adjacent behavior — **F3**: not shell/argv (that is
  genuinely closed), but TOML-body and URL-path injection, which Phase 5's "low / no
  injection surface" understates.
- Guided-dashboard / config echoed to terminal (ANSI / homograph) — **R1**: an
  entire unconsidered dimension that undercuts the F1/F2 mitigation.
- R20 record integrity — low: poisoning requires local write as the operator's uid;
  the residual concern is `secret_id` -> DELETE-path interpolation, folded into F3.
- TOCTOU landing-check -> write — **R2**: low severity, dominated by single-writer
  context; fix is atomic write.
- Confirm-gate bypass in `--json` / non-TTY — **F2**: real and load-bearing.

**2. Are mitigations sufficient?**
- `api_url` exfiltration guard: sufficient in interactive TTY mode *only if* re-ordered
  (F1); insufficient/undefined in non-TTY-with-overrides and `--json` (F2); and its
  display-based defense is undermined without terminal sanitization (R1). Net: not yet
  sufficient as written.
- Custody-boundary recorder + lint: sufficient. Recorder is load-bearing; lint is
  belt-and-suspenders (see R3).
- Scrubbing coverage: sufficient *for secrets* (redactor + `ScrubStderr` extension to
  the store subprocess + AC-27 canary). But scrubbing-for-secrets is a different axis
  from sanitization-for-terminal-safety (R1), which is not covered at all; AC-27
  asserts no *secret* reaches output, not that non-secret config values are
  control-char-safe.

**3. Low / "not applicable" dispositions that are actually higher.** External Artifact
Handling was rated **low** ("no injection surface"); escalate to **medium** pending an
explicit encode-or-validate rule (F3). Terminal output (ANSI/homograph) was given no
dimension at all; it belongs at **medium** because it directly weakens the api_url
mitigation (R1).

**4. Residual risk to escalate vs document.** The design's stated residual risk (a
compromised team-config repo remains a trust anchor) is correctly *documented* — that
is an honest boundary of a self-hosting-capable, config-driven design. What must be
**escalated and fixed before planning** is not that boundary but the fact that the
mitigation meant to convert silent exfiltration into a caught-at-runtime event does not
hold in the scripted path (F2) and is mis-ordered relative to the call it guards (F1).
The design presents a medium-high finding as closed; it is closed only for a correctly
re-ordered, interactive run. Close F1/F2/F3 in the design text, then this is a PASS.
