# Decision 3: required_skills placement

## Question

Where should the `required_skills` precondition input live for the new
delegate-time capability gate (issue #113): inside the opaque task `body` (a
body convention, peeked at by the handler), or as a top-level parameter on
`delegateArgs` alongside `to`, `mode`, `expires_at`, `session_id`, and
`read_only`?

The same convention must hold for `niwa_redelegate` (#114): whichever side
owns `required_skills`, the redelegate primitive must merge or preserve the
field in the same place.

## Options

### A. Inside body (`body.required_skills`)

**Mechanics.** The MCP wire schema (`internal/mcp/server.go:264-279`) and
`delegateArgs` (`internal/mcp/handlers_task.go:46-55`) stay unchanged.
`handleDelegate` adds a step between the existing `UNKNOWN_ROLE` check
(`handlers_task.go:130-133`) and `createTaskEnvelope`
(`handlers_task.go:141`):

1. `var peek struct { RequiredSkills []string \`json:"required_skills"\` }`
2. `_ = json.Unmarshal(args.Body, &peek)` (already known to be a non-null
   object — `body` is required, validated above).
3. Intersect `peek.RequiredSkills` with the target session's manifest.
4. On miss, return `errResultCode("MISSING_SKILLS", …)` with `{missing,
   available}` in the error body before any task ID is allocated.

The body remains an opaque `json.RawMessage` from the wire's perspective; the
peek is a one-off read by the handler with no schema commitment beyond
"`required_skills` is a string array, if present".

**Pros.**

- Matches the established convention: `body` is opaque to niwa, and the body
  carries every other workspace-specific field today (instructions, tool
  hints, etc.). The `niwa-mesh` skill docs already describe body shape as a
  workspace concern.
- No wire-schema churn. No change to `delegateArgs`, no new
  `Required`-array entry, no new top-level descriptor in `inputSchema`.
- Forward-compatible by construction. Future fields like
  `required_marketplaces` or `min_token_budget` slot into the same body
  convention with one additional unmarshal field per gate — the schema does
  not balloon as preconditions multiply.
- Naturally propagates through `niwa_redelegate`. The redelegate handler
  reads the source envelope body via `ReadState(taskDirPath(...))` and either
  reuses or shallow-merges it (lead 5, redelegate strawman). `required_skills`
  travels with the body for free; no special propagation logic is required.
- Manifest-coupled fields stay close to the rest of the workpiece. A worker
  reading the envelope body sees the same `required_skills` the gate
  evaluated; there is no risk of body and top-level args drifting in
  workspace tooling.

**Cons.**

- Audit-log fidelity loss. `extractArgKeys` (`audit.go:114-133`) only
  captures top-level keys of the wire arguments, so `required_skills`
  appearing in `body` does not surface as a distinct `arg_keys` entry — it
  is hidden inside the existing `body` key. Operators querying the audit
  log cannot grep for "calls that asserted a skill requirement" without a
  separate signal.
- Validation is a body peek, not a schema commitment. A typo
  (`requried_skills`) silently bypasses the gate. (Mitigatable via a
  workspace-side linter or by refusing unknown body keys, but neither is
  free.)
- The `MISSING_SKILLS` error response is the only structured signal that the
  field was honored; absence of `required_skills` in body is
  indistinguishable from "no requirement asserted" in the log.

**Risk.** Low. The only structural change is one unmarshal in a single
handler, gated on a non-empty body that is already validated at line 115.
The audit blind-spot is real but bounded — `MISSING_SKILLS` failures are
still logged via `error_code` (audit.go:142-153), so failed gates are
observable; only successful skill assertions are invisible.

### B. Top-level parameter on `delegateArgs`

**Mechanics.** Add `required_skills` to:

- `delegateArgs` struct (`handlers_task.go:46-55`):
  `RequiredSkills []string \`json:"required_skills,omitempty"\``.
- The wire schema in `server.go:264-279` (`Properties` map, with a
  `schemaProp{Type:"array", Description:…}` entry; not added to
  `Required`).
- The same fields on the future `niwa_redelegate` registration so the
  redelegate handler can override or preserve the source's value.
- Envelope persistence: either store `required_skills` on the envelope
  (`TaskEnvelope`, `types.go:206-224`) so redelegate can recover it without
  re-asserting, or expect callers to re-pass it on every redelegate.

The handler then uses `args.RequiredSkills` directly. Same gate position
between `UNKNOWN_ROLE` and `createTaskEnvelope`, same `MISSING_SKILLS`
contract, no body unmarshal.

**Pros.**

- Full audit-log fidelity. `required_skills` appears as a discrete entry in
  `arg_keys`, making "did this call assert a skill requirement?" a grep on
  the audit log. Useful for fleet-level "are coordinators using the gate?"
  observability.
- Schema-validated at the MCP boundary. Type errors surface as structured
  rejections rather than silent peek failures.
- The field is unambiguously niwa's responsibility, not a workspace
  convention. Niwa owns the gate, niwa owns the input.

**Cons.**

- Splits the established convention. Today every workspace-specific knob
  rides in `body`; promoting `required_skills` to a top-level argument
  signals that *some* preconditions are first-class while others are not,
  with no obvious rule for which is which.
- Schema cost scales with preconditions. `required_marketplaces`,
  `min_token_budget`, `required_models`, etc. each become new top-level
  arguments and new schema entries, both on `niwa_delegate` and on
  `niwa_redelegate`. The wire schema balloons; every new gate is a
  cross-handler change.
- Redelegate propagation requires explicit plumbing. The redelegate
  handler must either persist `required_skills` on the envelope (envelope
  schema additive change) or require the caller to re-pass it on every
  redelegate (footgun: a redelegate that forgets the field silently
  weakens the gate). A body convention sidesteps both.
- `delegateArgs` becomes a grab-bag of routing + workspace concerns. The
  current shape (`to`, `mode`, `expires_at`, `session_id`, `read_only`) is
  uniformly about *how to dispatch*. `required_skills` is about *what the
  workpiece needs* — the same category as instructions and tool hints,
  which live in body.

**Risk.** Medium. Each future precondition is another schema migration on a
public MCP tool. Forgetting to repeat the field on `niwa_redelegate`'s
schema is a real footgun; coupling envelope persistence to redelegate
fidelity adds an envelope-schema change.

## Chosen

**A. Inside body (`body.required_skills`).**

## Rationale

Three things tip the decision toward A:

1. **Convention preservation.** `body` is opaque to niwa today and that is a
   load-bearing property: every workspace-specific knob (instructions, tool
   hints, mode-of-work) lives inside it, and niwa's MCP surface stays small
   and stable. `required_skills` is a workspace-specific assertion about
   what the workpiece needs — same category as those existing knobs. Lead
   5's open question 5 frames this directly, and the "splits the body
   convention" cost in option B is real and recurring: each future gate
   becomes a schema migration.

2. **Forward-compatibility.** The decision must accommodate
   `required_marketplaces`, `min_token_budget`, etc. without
   re-architecting. With A, each new gate is one more body field and one
   more handler-side peek — the wire schema does not grow. With B, each
   new gate requires schema additions on both `niwa_delegate` and
   `niwa_redelegate`, plus an envelope-persistence question. A scales
   linearly in handler code; B scales in public schema surface.

3. **Redelegate composes for free.** With A, `niwa_redelegate` reads the
   source envelope body via `ReadState(taskDirPath(...))` (lead 5,
   §"Redelegate cross-state reads"), shallow-merges any
   `body_overrides`, and re-runs the same gate. `required_skills` rides
   along inside the body — no special-case propagation, no envelope-schema
   change, no risk of a redelegator silently dropping the gate by omitting
   a top-level field.

The audit-log blind-spot is the one real cost. It is acceptable because
(a) failed gates are still logged via `error_code: MISSING_SKILLS`, so the
audit trail captures every gate violation; (b) successful assertions are a
"did the caller assert *and* satisfy?" question that operators can answer
by joining audit logs with envelope reads; and (c) if grep-on-arg_keys
becomes a hard requirement later, the audit schema can be extended (e.g., a
`body_keys` capture of the top-level keys inside `body`) without changing
the wire schema or the body convention.

## Audit-log impact

`extractArgKeys` (`audit.go:114-133`) reads only top-level wire keys, so a
delegate call asserting `required_skills` produces audit entries with
`arg_keys: ["body","mode","read_only","session_id","to"]` — identical to a
call that did not assert any requirement. The audit signal that a gate was
applied comes from:

- `error_code: MISSING_SKILLS` for failed gates (always logged).
- For successful gates, no top-level audit signal; observers must read the
  envelope body to confirm. This is consistent with how every other body
  field is treated today (instructions, tool hints, etc.).

If post-hoc "which calls asserted a skill requirement?" becomes a recurring
operator question, a follow-up extension to `AuditEntry` could capture
`body_top_keys []string` (sorted list of `body`'s top-level keys, never
values) — preserves the no-values invariant and surfaces the gate
assertion without committing to a wire-level schema. Out of scope for this
decision.

## Composition with niwa_redelegate

The redelegate strawman (lead 5) reads the source via
`ReadState(taskDirPath(s.taskStoreRoot(), sourceTaskID))`, takes the source
envelope body verbatim or shallow-merges `body_overrides` into it, then
runs the same precondition pipeline as `handleDelegate` against the
(possibly-overridden) target role and session. With A, `required_skills`
is part of `body`, so:

- Default redelegate (no body override) preserves `required_skills` as-is
  and re-runs the gate against the new target's manifest. A gate-bypass
  via redelegate is structurally impossible.
- Body-override redelegate that omits `required_skills` clears the
  requirement (caller's intent). Body-override that mutates
  `required_skills` re-asserts a new requirement. Both behaviors are
  expressible without redelegate-specific code.
- The same `MISSING_SKILLS` error contract applies on redelegate
  pre-allocation — no new error code, no per-tool branching.

If we instead chose B, `niwa_redelegate` would need explicit propagation
logic: either persist `required_skills` on the envelope (envelope-schema
change), or require callers to re-pass it on every redelegate (silent
weakening footgun). A avoids both.

## Confidence

**High.** The convention argument is clear, the forward-compatibility
argument compounds with each future gate, and redelegate composition is
strictly easier with A. The only meaningful counter (audit-log fidelity) is
addressable later via an audit-schema extension that does not require
revisiting this decision. Lead 5's recommendation aligns with this
conclusion.

## Assumptions

1. **Manifest source-of-truth (lead 2 dependency).** The gate consults the
   target session's plugin/skill manifest, wherever lead 2 lands it. The
   placement of `required_skills` (body vs top-level) is independent of
   the manifest's location; this decision does not block on lead 2.
2. **Audit-schema stability.** We accept that successful gate assertions
   are not directly visible in `arg_keys`. If that becomes blocking, a
   non-breaking `body_top_keys` extension to `AuditEntry` is the planned
   escape hatch.
3. **Body convention applies to all future preconditions.** Subsequent
   gates (`required_marketplaces`, `min_token_budget`, …) follow the same
   body-key pattern. If a future gate needs argument-level audit
   visibility, that gate revisits this decision; the default is body.
4. **`niwa_redelegate` reads the envelope server-side.** Lead 5's
   strawman has redelegate read `<taskStoreRoot>/.niwa/tasks/<id>/
   envelope.json` directly; this is the precondition that makes A's
   "redelegate composes for free" claim true. If redelegate is ever
   redesigned to require the caller to re-pass the body, this decision
   should be revisited.
5. **No top-level `required_skills` typo-detection requirement.** A
   workspace-side linter or convention check is acceptable for catching
   `requried_skills` and similar typos. If we later need wire-level
   rejection of unknown body keys, that is a separate decision (and
   probably a body-schema registry feature, not a per-gate promotion).
