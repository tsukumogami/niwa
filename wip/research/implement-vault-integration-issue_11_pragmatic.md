# Pragmatic Review — Issue #11 (vault integration docs)

Commit: `f1dd62bb290fc63b1c8acaa2d40342bb848bd685` on `docs/vault-integration`.
Files reviewed: `docs/guides/vault-integration.md` (406 lines),
`docs/guides/vault-integration-acceptance-coverage.md` (129 lines).

## Summary

Guide is appropriately sized for the surface area. 406 lines covers
schema anatomy + personal overlay + migration + guardrail + CLI + scope
boundaries + security model — each section maps to a distinct PRD AC the
plan asked for. No sprawling "background / philosophy / architecture"
preamble, no duplicated code examples, no filler.

The AC matrix (129 lines) is explicitly a Key AC of Issue 11 ("every PRD
AC line mapped to an implementing test file") — it's the contracted
deliverable, not process ceremony tacked on.

Audience targeting is correct: the guide speaks to a team/dev adopting
vault-backed secrets. It does not lapse into contributor-facing
implementation detail (no package names, no internal API, no refactor
notes). Links to PRD/DESIGN are used for deep threat-model detail rather
than inlined.

Spot-checked command/flag claims (`niwa config set global`,
`--allow-plaintext-secrets`, `--allow-missing-secrets`, `--check-vault`,
`--audit-secrets`, `?required=false`, `vault_scope`, `team_only`) — all
match the code in `internal/cli/`, `internal/vault/`, `internal/guardrail/`.

## Blocking findings

None.

## Non-blocking (advisory) findings

**A1. §Security model is borderline redundant with PRD/DESIGN links.**
Lines 381–401 (20 lines) summarize the threat model, then link out to the
PRD and DESIGN for the same material. The summary repeats content already
covered in §Public-repo guardrail + §Schema anatomy (`secret.Value`
redaction), just re-grouped as a security narrative. The two link lines
at the end would carry the weight alone. Advisory only — the section is
inert and doesn't create maintenance burden; trimming would sharpen the
guide but isn't required.

**A2. §"Why there's no 'replace the whole team provider' path" (lines
244–253).** This is design-rationale content. A user adopting vault
secrets doesn't need to know *why* a feature doesn't exist — they need
to know the per-key shadowing pattern (already covered two sections
above). Candidate for deletion or a one-line "see DESIGN D-9" footnote.
Advisory; the content is short (10 lines) and correctly answers a
predictable "but what if I want to…" question, so keeping it is
defensible.

**A3. AC matrix rows with identical test functions.** A few ACs in the
matrix cite the same test (e.g., `TestResolveGlobalOverridePerWorkspaceBlock`
covers three different ACs; `TestCheckGitHubPublicRemoteSecretsOriginPrivateUpstreamPublic`
covers two). Matrix notes this upfront ("some ACs span more than one
test; the table lists a representative"), which is the right call —
flagging only because a future reviewer might read duplication as
missing coverage. No action needed.

## What is NOT bloat (things I considered and rejected)

- **§Schema anatomy (lines 74–192, ~120 lines).** Covers four distinct
  Key ACs from the plan in one section (anonymous vs named providers,
  vars/secrets split, requirement sub-tables, `vault_scope`,
  `team_only`). Merging or trimming would drop AC coverage.
- **§CLI reference table.** Seven rows, each a distinct user-facing
  surface. No speculative entries.
- **§v1 scope boundaries.** Explicitly required by the plan ("Guide
  calls out GitHub-only guardrail detection and Windows-via-WSL-only
  support"). Four bullets.
- **§Plaintext-to-vault migration (lines 256–315).** Required by plan
  Key AC. The five numbered steps are the minimum to get from
  plaintext to vault with audit verification.

## Content placement (README vs docs/ index)

Nothing in this guide belongs in `README.md`. The README is the project
landing page; this is adoption documentation for a specific feature.
The existing pattern of `docs/guides/<feature>.md` is consistent with
other guides in the repo.

No `docs/` index update needed unless one already exists for other
guides and this was omitted — out of scope for pragmatism review.

## Verdict

Ship as-is. Two advisory nits (A1 security-model duplication, A2
design-rationale aside) are small enough to defer to a follow-up cleanup
pass if ever. Neither creates future contract drag.
