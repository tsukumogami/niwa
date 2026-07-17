# Decision 1 (standard): Dispatch-state representation, SHA-awareness, trigger-semantics, migration

## Question
How to record the last-dispatched head SHA, the per-source trigger-semantics
declaration, and the session-continuation reference; and how to migrate the flat
handled-set without a re-fire storm.

## Evidence
- Handled-set is flat text lines `owner/repo#number`; `LoadHandledSet` already
  skips malformed lines (state.go:47-49) — a natural dual-format seam. No versioning.
- `AppendHandled` is append-only + idempotent (permanent suppression). The SHA
  comparison would land in `Select` (select.go).
- StagedRecord is per-PR JSON with no session/instance ref; `instancePath`,
  `provRes.Name` are in scope at SaveStagedRecord time (watch.go:206,256).
- Both stores grow monotonically; only the instance layer is reaped (name+TTL
  backstop). Record/handled layers have no cleanup — the core structural gap.

## Decision
Split the state across the two stores by lifetime:

1. **Last-dispatched SHA -> the handled-set (permanent, per PR).** Evolve the line
   format to carry the SHA (e.g. `owner/repo#number@<sha>`), extend `HandledKey`/
   `isHandledKey` to parse both shapes, and make `LoadHandledSet` dual-format:
   a legacy SHA-less line parses as "handled at unknown SHA". The SHA must live in
   the permanent store (not the GC'd record) so a dismissed PR still suppresses on
   an unchanged head (R7) and re-qualifies on a new head (R2). Migration is
   lazy-on-read/append (the registry.go R23 mirror-field precedent), no rewrite pass.
   Legacy unknown-SHA entries adopt the current head on first observation without
   staging (R17, no storm).
2. **Session-continuation reference -> the StagedRecord (per live dispatch, GC'd).**
   Add `InstancePath` (in scope, zero new machinery) as the liveness anchor, and —
   for resume (Decision 2) — the captured `SessionID`/`ConversationID`/`ShortID`.
   Records are pruned when their instance is no longer live (new staged-record GC,
   Decision 4), mirroring the reaper, so the record store stops growing unbounded.
3. **Trigger-semantics declaration.** Introduce a typed `TriggerSemantics`
   (`level` | `edge`) in the watch state package. The PR source declares `level`;
   the coalesce / one-live-session logic branches on it rather than hard-coding.
   Physically reserve a spot in the handled-state format (a header/version line the
   dual-format parser tolerates) so a future edge source records its own semantics.
   For ED2 there is one source (`level`); the contract has room for `edge` with no
   edge consumer yet (PRD Known Limitation).

## Alternatives
- All state in one new JSON file (drop the flat handled-set): rejected — larger blast
  radius, discards the skip-malformed migration seam, no benefit over dual-format.
- SHA in the StagedRecord only: rejected — records are GC'd, so a dismissed PR would
  lose its last-dispatched SHA and re-fire on an unchanged head (breaks R7).
