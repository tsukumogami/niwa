# PRD Review: Vault Integration

Reviewer role: product / requirements reviewer
Source: `docs/prds/PRD-vault-integration.md`
Review date: 2026-04-13

## Summary

The PRD is unusually well-developed for a Draft: the user stories are concrete
and traceable, the decision log is substantive, and most requirements are
testable. The main gaps are in: (a) acceptance-criteria coverage for a few
requirements that slipped through (R5 zero-source fallback, R16 re-resolution,
R20 zero-dep constraint); (b) a handful of requirements that embed
subjective or untestable phrasing (R18 "10 minutes", R19 "80% case"); (c)
two open questions (Q-3, Q-4) that are really metrics discussions rather than
unresolved decisions and should be demoted or removed; (d) a persona gap
around CI-only callers that the PRD itself flags in Q-1 but doesn't close;
and (e) the Goals preamble references "12 never-leaks invariants" but only
R21–R32 (12 items) implement them, without a direct Goal→R traceability
line. No MUST-FIX issues block transition to Accepted if the team is
comfortable closing Q-1/Q-3/Q-4, but several SHOULD-FIX items would
tighten the document before hand-off to design.

---

## MUST-FIX

### M-1. Goals section references "12 never-leaks invariants" without traceability

**Concern:** The Goals bullet claims "the feature ships with 12 'never leaks'
invariants enforced at the type system, pipeline, and filesystem-permission
levels" but there is no mapping line in the Goals section to R21–R32. A
reader checking whether the goal is verifiable has to count requirements
to confirm there are twelve.

**Fix:** Add a single traceability sentence to the Goal bullet:
"Enforced by R21–R32 (one invariant per requirement; each has a
corresponding AC in the Security section)." Also cross-check the count:
R21 through R32 is 12 items, so the claim holds, but state it explicitly.

### M-2. R18 "under 10 minutes" has no test protocol

**Concern:** R18 states "End-to-end target: under 10 minutes once the PR is
merged" and explicitly adds "This is a usability budget; the PRD does not
prescribe the UX to achieve it." As written, a third-party reviewer cannot
test R18 — there's no fixture scenario, no hardware baseline, no definition
of "end-to-end" (is that clock time including the PR review? just the post-
merge steps? install time included or excluded?). The bootstrap AC in the
Bootstrap section only tests that docs exist, not that they fit in 10
minutes.

**Fix:** Either (a) define the measurement protocol ("a fresh developer
following the documented sops bootstrap walkthrough, starting from a
machine with niwa already installed, completes the listed steps in under
10 minutes of active work, excluding the PR-review delay"), or (b)
downgrade R18 from a requirement to a design goal in the Non-Functional
Requirements narrative and drop the "MUST"-style bindingness.

### M-3. R19 performance requirement is untestable as written

**Concern:** R19 says "Total resolution time MUST stay under 5 seconds in
the 80% case." "80% case" is undefined — is it a percentile across users,
across workspaces, across invocations? There's no fixture corpus to
measure against. A design reviewer cannot verify this by reading the PRD.

**Fix:** Rephrase as "For a workspace with ≤ 20 vault references, using
backend-level CLI calls with warm provider auth, total niwa-side resolution
time (excluding provider CLI time) MUST stay under 500ms; total wall time
under 5 seconds." Or move the performance target to a design-doc concern
and keep R19 as a non-binding guideline.

### M-4. Q-1 is unresolved but the PRD has already filled in 10 stories (not 8)

**Concern:** Q-1 says "This draft's eight user stories cover the three
archetypes..." but the User Stories section actually runs US-1 through
US-10 (and the doc structure notes US-9 was "inserted later"). The open
question has stale context. More importantly, Q-1 raises a real persona
gap — "CI-only caller" — that is never addressed anywhere in the PRD.
`niwa apply` in CI is called out in D-4 rationale ("breaks non-interactive
callers (`niwa init`, CI)") but there is no user story for the CI
operator and no AC confirming CI paths work.

**Fix:** (a) Update Q-1's count to "ten user stories"; (b) decide whether
a CI-only user story is needed and either add US-11 or explicitly mark
CI-caller as out-of-scope with rationale; (c) "external contributor" is
already covered by US-9, so Q-1 can drop that bullet.

---

## SHOULD-FIX

### S-1. R5 zero-source workspace handling contradicts Q-5

**Concern:** R5 says "Zero-source workspaces MAY set this field for personal
overlay targeting." Q-5 then asks "Should `vault_scope` default to the
workspace name in this case, or require explicit declaration?" These two
statements conflict: R5 makes it optional ("MAY"), but Q-5 treats it as
unresolved. If Q-5 resolves "require explicit declaration," R5 becomes
"MUST," not "MAY." This is a live contradiction that will confuse
implementers.

**Fix:** Resolve Q-5 before finalizing R5, and state the chosen behavior
once. Alternatively, mark R5's zero-source clause as "TBD pending Q-5" and
defer until the question is closed.

### S-2. R16 "re-resolution on every apply" has no acceptance criterion

**Concern:** R16 specifies that `niwa apply` re-resolves every reference
on every invocation. The Rotation AC section tests the observable effect
("next `niwa apply` re-resolves the value"), but there is no AC for the
negative case: a second `niwa apply` with *unchanged* upstream still
performs the re-resolution (i.e., no hidden cache kicks in). Without the
negative test, R16 is only partially covered.

**Fix:** Add to Rotation ACs: "Two consecutive `niwa apply` calls with no
config change and no upstream change both perform a provider `Resolve`
call for each reference (verified via provider-CLI invocation count or a
test-seam fake)."

### S-3. R20 "zero additional external dependencies" has no acceptance criterion

**Concern:** R20 says niwa MUST NOT pull in a vault-specific Go library
and MUST invoke provider CLIs as subprocesses. There is no AC in the
Backends section that tests this — a reviewer cannot verify it without
reading go.mod. The constraint is important enough (affects supply-chain
surface and binary size) that it should be guarded.

**Fix:** Add an AC: "`go mod graph` after implementing the sops and
Infisical backends shows no dependency on `go.mozilla.org/sops`,
`filippo.io/age`, or any Infisical SDK. Backend modules shell out to
the respective CLIs."

### S-4. Q-3 (plaintext-deprecation timeline) is a release-planning question, not a PRD question

**Concern:** Q-3 asks when R14/R32 become the default ("on day one vs.
staged through a release cycle"). The PRD already decides the guardrail
is a hard block (R14: "MUST refuse"; R32: "MUST fail unless... overridden
with `--allow-plaintext-secrets`"). The question is purely about when
that MUST takes effect. This is a release-engineering concern (feature
flag vs. immediate enforcement), not a product-requirements concern.

**Fix:** Move Q-3 out of the PRD and into a release plan / rollout doc.
Or, if it stays, frame it explicitly as "rollout strategy for R14
enforcement" so readers don't think the guardrail itself is unsettled.

### S-5. Q-4 (rollout metrics) is out of scope for this PRD

**Concern:** Q-4 proposes success signals (plaintext count drops to
zero, fewer GitHub secret-scanning alerts, etc.). These are
post-launch adoption metrics that belong in a rollout/launch plan, not
in a requirements doc. None of the metrics listed are things the PRD
could encode as requirements.

**Fix:** Remove Q-4 entirely, or relocate it to the Goals section as a
note that success metrics will be defined separately before launch.

### S-6. US-8 (migration audit) is testable but lacks a corresponding failure-mode AC

**Concern:** R13 (`niwa status --audit-secrets`) exit-code behavior is
tested: non-zero when plaintext present AND vault configured, zero when
all vault-ref or empty. But the in-between case — plaintext values present
and NO vault configured — is not tested. Does the command exit zero or
non-zero? US-8 says "track migration progress," which implies the user
wants to know plaintext count regardless of vault-configured state.

**Fix:** Add an AC clarifying exit behavior when plaintext is present but
no vault is configured (informational zero-exit seems right, matching
"audit tool, not a blocker"), and/or add a `--strict` flag to opt into
non-zero on any plaintext.

### S-7. R3 "reference-accepting locations" list may be incomplete for `[files]`

**Concern:** R3 says references ARE accepted in "`[files]` source keys"
but NOT in "`[env.files]` source paths." This is correct per D-8 and the
Out-of-Scope note, but readers confuse the two similarly-named tables.
The `[files]` table structure from R33 supports three tiers
(`[files.required]` / `[files.recommended]` / `[files.optional]`) — the
PRD doesn't say whether `vault://` URIs are accepted in all three sub-
tables or only in the main `[files]` table.

**Fix:** Clarify R3 to explicitly cover `[files.required]`,
`[files.recommended]`, `[files.optional]` as accepting-locations (or
call out which ones don't).

### S-8. D-1 alternative (c) "one commercial backend with free tier" is borderline strawman

**Concern:** D-1's alternative (c) reads "ship one commercial backend with
free tier (Doppler has the cleanest OAuth UX but is closed-source
SaaS)." The rejection rationale in the next paragraph is that Doppler is
closed-source. But (c) as framed is already "closed-source SaaS" by
definition — it's presented alongside the others only to be dismissed for
the attribute that's baked into its own description. This is a mild
strawman: the alternative was defined in a way that guaranteed rejection.

**Fix:** Either drop (c) as a distinct alternative (it collapses into "ship
Infisical-only" from (b) if you don't care about open-source), or reframe
it as a real trade-off ("accept closed-source SaaS for demonstrably better
OAuth UX") and explain why that trade-off was rejected.

### S-9. Acceptance criterion under Resolution duplicates R11 without adding information

**Concern:** "`vault://provider/key?required=false` resolves to empty
string when missing, with no warning." This is a verbatim restatement of
R11. It's testable, but it doesn't add anything a reader wouldn't know
from reading R11.

**Fix:** Acceptance criteria that are word-for-word restatements of a
requirement are duplication, not verification. Rewrite the AC as a
concrete scenario: "In a workspace with a `vault://nonexistent-key
?required=false` reference, `niwa apply` completes with exit 0 and no
stderr output about the missing key; the corresponding env file contains
the key mapped to an empty string."

### S-10. Out-of-scope item "Windows support" lacks rationale

**Concern:** "Windows support. macOS + Linux only for v1. Windows users
can use WSL." No rationale given. Is this a resource constraint? A
technical constraint (filesystem permissions? `0o600`?)? A user-base
priority call? Without context, a contributor who wants to propose
Windows support doesn't know what barrier to clear.

**Fix:** One sentence of rationale, e.g., "POSIX file-mode semantics
(`0o600`) are load-bearing for R24; equivalent Windows ACL plumbing adds
scope without serving the identified personas in v1."

### S-11. D-11 alternative (c) "implicit default name" rationale is rushed

**Concern:** D-11 rejects "implicit default name for the sole named
provider when URIs omit the name" on the grounds that it's "magic —
same URI behaves differently depending on whether there's one or two
providers." But that same logic could reject the anonymous-vs-named
dichotomy: `vault://key` vs. `vault://name/key` also "behaves
differently depending on" the file's declaration shape. The rationale
doesn't quite distinguish why (c) is magic but the chosen design isn't.

**Fix:** Sharpen the distinction. The real argument is probably that the
chosen design makes the distinction *visible in the URI* (presence/
absence of the slash-separated name), whereas (c) hides it. Say that.

---

## NITS

### N-1. US-9 was "inserted later" — ordering is awkward

US-9 discusses external contributors, which is a natural follow-up to
US-2 (team-member bootstrap). The current placement after US-8 (audit
migration) breaks the narrative arc. Consider renumbering so US-9 sits
closer to the other onboarding stories, or add a one-line transition at
the start of US-9 that acknowledges the audience shift.

### N-2. R15 state labels overlap with existing niwa status vocabulary

R15 introduces `drifted`, `stale`, `ok`. The Materialization AC also uses
`rotated` (under Rotation: "reports `rotated <path>` to stderr"). Four
states total across the PRD. Confirm this is the full vocabulary and
that `rotated` is an event (stderr message), not a status state.

### N-3. Q-2 is marked RESOLVED but still lives in Open Questions

Q-2 says "RESOLVED: both Infisical and sops+age ship in v1.0 as peer
backends (see D-1)" but it's still listed under "Questions to resolve
before the PRD transitions to Accepted." Move resolved questions to a
separate "Resolved during draft" subsection, or delete Q-2 and promote
the sub-question about implementation ordering to its own Q entry.

### N-4. Out-of-scope "v1 niwa `vault import` tool" wording

The item header is "v1 niwa `vault import` tool. Automated plaintext-to-
vault migration is deferred." The "v1" prefix in the header is confusing
because the item is about what's NOT in v1. Drop the "v1" prefix: "niwa
`vault import` tool: automated plaintext-to-vault migration is deferred."

### N-5. D-8 "raw:" prefix — escape syntax conflicts with TOML conventions

D-8 uses `raw:vault://...` to escape. TOML has no `raw:` convention; this
is a niwa-specific string decoder. The PRD doesn't call out that this
decoder is a niwa runtime concern, not a TOML parser concern. Worth one
sentence in D-8 or R17 to make that explicit (otherwise a design reader
might assume it's a TOML feature).

### N-6. Personas "Indie solo developer" is identified as an archetype but has no US

The Problem Statement lists three archetypes (indie solo, team lead,
team member). Team lead gets US-1/US-5/US-7; team member gets US-2/US-4/
US-6; external contributor gets US-9. No story specifically models the
indie solo developer — their needs (one personal vault, per-org PAT
scoping, `niwa apply` just works) are implied by US-3, but US-3 is
framed as a team-member-with-personal-vault story. Consider whether a
minimal indie-solo story is worth adding, or whether the indie case is
correctly subsumed by US-3 + US-6.

### N-7. R33 description strings — length limits?

R33 specifies description-string values must flow into error messages.
No length cap or formatting rules. A team that writes a 2000-character
description would flood stderr. Consider noting a soft cap (e.g., "keep
descriptions to a single line") or leaving the cap to design-doc
discretion.

### N-8. D-3 rationale mentions MergeGlobalOverride without link

D-3 cites "niwa's existing `MergeGlobalOverride` precedence for
`Env.Vars` (v0.5.0)." R7 does the same. A linked reference (even just
"see internal/config/global_override.go" or the relevant issue) would
help a reader who wants to verify the existing pattern.

---

## Persona coverage audit

| Persona | Identified? | User story? |
|---------|-------------|-------------|
| Indie solo developer | Yes, in Problem Statement | No dedicated US (implied by US-3 + US-6) |
| Team lead | Yes | US-1, US-5, US-7 |
| Team member (onboarding) | Yes | US-2 |
| Team member (everyday use) | Yes | US-3 |
| Team member (debug session) | Yes | US-4, US-6 |
| Team member (migration) | Yes | US-8 |
| External contributor | Yes, added later | US-9 |
| "Developer working across multiple orgs" | Implied in US-3 | Covered by US-3 part 2 |
| US-10 | Not in current numbering visible — PRD mentions US-1 through US-10 in the review prompt but file only contains through US-9 | Verify if US-10 exists or if the count was mis-specified |

**Gap:** CI-only caller (caller running `niwa apply` from GitHub Actions
or similar non-interactive environment). D-4's rationale acknowledges
this persona exists ("Interactive prompt breaks non-interactive
callers") but there is no user story or AC covering the CI-caller path.

**Note on US-10:** The review prompt references "US-1 through US-10,
with US-9 inserted later" but the PRD as read contains US-1 through
US-9. Either US-10 exists elsewhere and I missed it, or the prompt
miscounted. Worth confirming.

---

## Requirements testability audit

| Requirement | Testable? | Notes |
|-------------|-----------|-------|
| R1 (pluggable interface) | Partial | "Pluggable" tested by Backend AC "Adding a new backend requires implementing a single Go interface" — OK |
| R2 (anonymous/named declaration) | Yes | Schema ACs cover both shapes |
| R3 (`vault://` URI scheme) | Yes | Schema ACs cover accept/reject locations |
| R4 (per-project scoping) | Yes | Resolution AC with `[[sources]] org = "tsukumogami"` |
| R5 (vault_scope escape) | Partial | See S-1; zero-source case unresolved |
| R6 (resolution chain) | Yes | Resolution ACs cover precedence |
| R7 (personal-wins) | Yes | Resolution AC "personal value wins in the resolved env" |
| R8 (team_only) | Yes | Resolution AC "fails with an error naming the key" |
| R9 (fail-hard with actionable errors) | Yes | Resolution AC covers the fork-PR scenario |
| R10 (`--allow-missing-secrets`) | Yes | Resolution AC |
| R11 (`?required=false`) | Yes | Resolution AC (though duplicative — see S-9) |
| R12 (`GlobalOverride.Vault`) | Yes | Schema AC |
| R13 (`audit-secrets`) | Yes | Audit ACs (though see S-6) |
| R14 (public-repo guardrail) | Yes | Security AC |
| R15 (`SourceFingerprint`) | Yes | Materialization AC |
| R16 (re-resolution every apply) | Partial | See S-2 — no negative-case AC |
| R17 (`raw:` escape) | Yes | Schema AC |
| R18 (bootstrap ≤10min) | **No** | See M-2 — no test protocol |
| R19 (apply ≤5s) | **No** | See M-3 — "80% case" undefined |
| R20 (no external Go deps) | **No** | See S-3 — no AC |
| R21–R32 (security invariants) | Yes | Security ACs cover each |
| R33 (three-level tables) | Yes | Resolution ACs for required/recommended/optional |
| R34 (required > allow-missing-secrets) | Yes | Resolution AC explicit |

Untestable-as-written: R18, R19, R20 (per M-2, M-3, S-3).

---

## Goals-to-requirements traceability

| Goal | Traceable to |
|------|-------------|
| Team configs can be publishable | R14 (public-repo guardrail), R32 (enforce), R13 (audit), R21–R32 (invariants) |
| Per-org personal secret scoping works end-to-end | R4, R5, R6, US-3 |
| New-member bootstrap under 10 minutes | R18 (but R18 itself is untestable — see M-2) |
| Zero new leak classes (12 invariants) | R21–R32 (but traceability not stated — see M-1) |
| Pluggable backend with two peer backends | R1, R20, Backends AC section |

Gaps: the "10 minutes" goal and the "12 invariants" goal both fail to
include a direct traceability line in the Goals section; fixing M-1 and
M-2 closes this.

---

## Open-question quality

| Q | Genuinely unresolved? | Belongs in PRD? |
|---|-----------------------|-----------------|
| Q-1 Personas sign-off | Yes — CI persona is missing | Yes, but update story count (see M-4) |
| Q-2 v1 ordering | Resolved per text itself; sub-question about implementation sequence remains | Move resolved part to "Resolved"; keep sub-question (see N-3) |
| Q-3 Plaintext-deprecation timeline | Not really unresolved — R14/R32 already decide | Move to release plan (see S-4) |
| Q-4 Rollout metrics | Post-launch concern | Remove or move (see S-5) |
| Q-5 Zero-source vault_scope default | Yes, and contradicts R5 | Yes (see S-1) |
| Q-6 Migration UX (vault init command?) | Yes | Yes |
| Q-7 team_only enforcement layer | Yes, affects design | Yes |
| Q-8 Sign-off stakeholders | Yes, process question | Yes, but low stakes |

---

## Out-of-scope audit

All 12 items (counting `CLAUDE.md secret interpolation`, which is
effectively a restatement of R27):

| Item | Exclusion justified? |
|------|----------------------|
| `vault import` tool | Yes — manual migration is tractable |
| Additional vault backends | Yes — pluggable interface defers them cleanly |
| Secret rotation automation | Yes — scope containment |
| Daemon / background watcher | Yes — pull-only is a philosophy, well-stated |
| Windows support | Yes, but rationale missing (see S-10) |
| MFA prompts mid-command | Yes — punts to provider CLIs |
| Per-file materialization permissions | Yes — `0o600` default is the safe choice |
| `[env.files]` vault-backed source paths | Yes — alternative via `[files]` given |
| CLAUDE.md secret interpolation | Yes (this is also R27's forbidden list; slight redundancy) |
| Non-GitHub source control | Yes — public-repo detection scope |

No exclusions appear to belong in scope. S-10 flags the Windows item
for rationale clarity.

---

## Decisions and trade-offs — alternative-vs-strawman check

| Decision | Alternatives genuine? | Notes |
|----------|-----------------------|-------|
| D-1 | Mostly | (c) borderline strawman; see S-8 |
| D-2 | Yes | Three real alternatives each with a cost |
| D-3 | Yes | team-wins vs personal-wins is a live debate in the industry |
| D-4 | Yes | Fall back to empty, prompt, warn all exist in real tools |
| D-5 | Yes | `[claude.vault]` would be defensible given `[claude.content]` pattern |
| D-6 | Yes | Binding tables are a real pattern (e.g., Kubernetes ConfigMaps) |
| D-7 | Yes | Disk cache and keychain cache are real options |
| D-8 | Yes | Backslash escape was genuinely considered |
| D-9 | Yes | Rendezvous names are used by some tools (e.g., external-secrets) |
| D-10 | Yes | Single binary table is the simpler alternative |
| D-11 | Mostly | See S-11 — rationale on (c) could be sharper |

Only D-1 (c) and D-11 (c) are potential strawmen, and both are minor.
The decision log is unusually solid for a PRD.

---

## Summary of findings

- **MUST-FIX**: M-1 (goals→R traceability), M-2 (R18 untestable),
  M-3 (R19 untestable), M-4 (Q-1 stale + CI persona gap).
- **SHOULD-FIX**: 11 items, mostly around AC coverage gaps (S-2, S-3,
  S-6), open-question hygiene (S-4, S-5), and minor rationale polish.
- **NITS**: 8 items, none blocking.

The PRD is substantively in good shape. Closing the four MUST-FIX items
and resolving Q-5 would make it ready to transition to Accepted.
