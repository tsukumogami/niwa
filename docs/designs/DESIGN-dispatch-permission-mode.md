---
schema: design/v1
status: Proposed
problem: |
  `buildDispatchPassthrough` only forwards `--permission-mode` when the
  operator supplies it explicitly; nothing derives it from the workspace's
  materialized permission posture. The fix must read the instance's already-
  materialized `.claude/settings.json` before that call (which currently
  happens later, at dispatch.go:554, after the passthrough is already
  built), and must not fire for Codex, whose permission-equivalent flag
  takes an unrelated value vocabulary.
decision: |
  Read the instance's materialized settings once, immediately before
  `buildDispatchPassthrough` is called, and derive `dispatchPermissionMode`
  from it when unset and the dispatched agent's flag spelling is Claude's
  `--permission-mode`. The existing later read (step 9c) is deleted and its
  consumers reuse the earlier one.
rationale: |
  This is the smallest diff that satisfies every PRD requirement: one struct
  field, one call-site move, one conditional. A second independent read was
  rejected for duplicating file I/O against the codebase's own stated
  single-read intent. Pushing the read and the agent-vocabulary branch into
  `buildDispatchPassthrough` itself was rejected for breaking that
  function's existing pure-argv-builder contract.
upstream: docs/prds/PRD-dispatch-permission-mode.md
decision_provenance: inline-resolved
---

# DESIGN: dispatch permission mode

## Status

Proposed

## Context and Problem Statement

`niwa dispatch`'s `dispatchPermissionMode` package-level variable is set only
from the operator's own `--permission-mode` CLI flag
(`internal/cli/dispatch.go:28,47`). `buildDispatchPassthrough`
(`internal/cli/dispatch.go:944`) forwards it into the launched worker's argv
only when it is non-empty — nothing else ever populates it. When it's empty,
the worker starts with whatever permission mode its own materialized
`.claude/settings.json` and the launching user's global Claude Code settings
resolve to, which since Claude Code 2.1.258 no longer includes the project
`.claude/settings.json`'s `permissions.defaultMode` value.

Two structural facts about the current code shape this design has to work
within:

1. **The build-then-read ordering problem.** `buildDispatchPassthrough` is
   called at `dispatch.go:534`. The only existing read of the instance's
   materialized settings, `readInstanceSettings(instancePath)`, happens
   later, at `dispatch.go:554`, inside the remote-control default-fill block,
   and its result (`inst`) is reused by the keep-alive resolver at step 9d.
   A value read at :554 is not available to the passthrough build at :534.
2. **The agent-flag-vocabulary problem.** `agentplan.LaunchFlags` gives each
   dispatch-capable agent its own permission-equivalent flag spelling.
   Claude's is `--permission-mode` (vocabulary: `bypassPermissions`,
   `acceptEdits`, etc). Codex's is `--sandbox`, a disjoint vocabulary; niwa
   deliberately forwards nothing on it today because Codex workers already
   get full trust through `WorkdirGrantArgs`
   (`projects={...trust_level="trusted"}`). A derivation keyed only on "does
   this agent have a permission flag" would wrongly touch Codex.

The PRD already settled three architectural questions (see PRD-
dispatch-permission-mode's Decisions and Trade-offs): read materialized
settings rather than `workspace.toml`, move the settings read ahead of the
`buildDispatchPassthrough` call rather than duplicating it, and scope the
derivation to Claude's flag spelling only. What remains open is the concrete
shape: where the derivation function lives, how it's invoked relative to the
existing 9b/9c/9d step ordering, and how the agent-scoping check
(requirement R4) is expressed in code.

## Decision Drivers

- **DD1 (R5).** The derived value must come from the instance's
  already-materialized `.claude/settings.json`, never from re-parsing
  `workspace.toml` in the CLI layer.
- **DD2 (R6).** The read this derivation depends on must be available before
  `buildDispatchPassthrough` runs at `dispatch.go:534`. Consolidating with
  the existing `:554` read is allowed but must not change the
  remote-control (9c) or keep-alive (9d) behaviors that already consume it.
- **DD3 (R4).** The derivation must fire only for the agent whose
  `LaunchFlags.PermissionMode` is Claude's spelling (`--permission-mode`),
  never for Codex's `--sandbox`.
- **DD4 (R2, R3).** An explicit `--permission-mode` on the invocation always
  wins; a workspace with no declared posture, or `"ask"`, forwards nothing —
  both unchanged from today's code path.
- **DD5 (R7).** A settings-read failure (absent, unreadable, malformed file)
  must degrade to "nothing derived," never fail the dispatch.
- **DD6 (maintainability).** The fix should read as a small, local addition
  to the existing step-9 sequence in `runDispatch`, not a new abstraction
  layer — `buildDispatchPassthrough` is a pure argv builder today and should
  stay one.

## Considered Options

### Decision: where does the derivation live, and how is it invoked?

**Option A — Derive inline in `runDispatch`, before step 9b, reusing a single
`readInstanceSettings` call.** Move the existing `readInstanceSettings`
call from step 9c up to immediately before `buildDispatchPassthrough`
(new step, ahead of current 9b). Add a `Permissions.DefaultMode` field to
the `instanceSettings` struct in `dispatch_plugins.go`. Before calling
`buildDispatchPassthrough`, if `dispatchPermissionMode == ""` and the
dispatched agent's `flags.PermissionMode == "--permission-mode"` and
`inst != nil && inst.Permissions != nil && inst.Permissions.DefaultMode ==
"bypassPermissions"`, set `dispatchPermissionMode = "bypassPermissions"`.
The single `inst` value then also feeds the existing 9c/9d consumers
unchanged (they already receive it as a parameter/closure-captured
variable today; only the call site moves earlier).

*Trade-off:* touches the call-site ordering of an already-dense function
(`runDispatch`), so the diff has to be read carefully against 9c/9d to
confirm nothing between the old and new call sites depended on the old
position. Low risk in practice — nothing between :534 and :554 reads
`inst` or depends on `passthrough`'s content, only its existence.

**Option B — Add a second, independent `readInstanceSettings` call
immediately before `buildDispatchPassthrough`, leaving the existing :554
call untouched.** Avoids touching the 9c/9d block at all.

*Trade-off:* reads `.claude/settings.json` twice per dispatch. Violates
DD6 in spirit — niwa's own comment at the existing call site ("The instance
settings are read once too") states the one-read intent this option
abandons. Also duplicates the nil/error handling for the same file in two
places, which is exactly the kind of drift DD1/DD5 are trying to avoid
(two read sites can silently diverge on error handling over time).

**Option C — Push the derivation into `buildDispatchPassthrough` itself,
having it accept `*instanceSettings` and do the read internally.**

*Trade-off:* violates DD6 directly — `buildDispatchPassthrough`'s own doc
comment states it "stays a pure argv builder" by design (model resolution
already happens in the caller for this reason). Giving it a file read and
an agent-vocabulary branch turns a pure function into one with I/O and
policy, which the existing code structure deliberately avoids.

## Decision Outcome

**Chosen: Option A.** It satisfies DD1, DD2, DD3, DD4, DD5, and DD6 with the
smallest diff: one struct field added, one call site moved, one
conditional added before the existing `buildDispatchPassthrough` call,
zero new read sites, and zero change to `buildDispatchPassthrough`'s own
signature or purity. Option B was rejected for reading the same file
twice against the codebase's own stated single-read intent (DD6). Option C
was rejected for breaking `buildDispatchPassthrough`'s pure-function
contract, which the codebase deliberately preserves today (DD6).

`decision_provenance: inline-resolved` — this design's sentinel-gated
dispatch context routed the decision back through the parent chain rather
than to a standalone `/decision` invocation; three real options were
weighed to equal depth above rather than a single default being asserted.

## Solution Architecture

**`internal/cli/dispatch_plugins.go`** — widen `instanceSettings`:

```go
type instanceSettings struct {
    EnabledPlugins         map[string]bool
    ExtraKnownMarketplaces map[string]marketplaceEntry
    RemoteControlAtStartup *bool
    KeepAliveOnDispatch    *bool
    // Permissions mirrors the materialized settings.json permissions key.
    // Non-nil only when the file declared one; a nil Permissions or a
    // DefaultMode other than "bypassPermissions" both mean "nothing to
    // derive" to every reader of this field.
    Permissions *struct {
        DefaultMode string `json:"defaultMode"`
    } `json:"permissions"`
}
```

`readInstanceSettings` itself is unchanged — `json.Unmarshal` already
populates any field the struct declares; widening the struct is the whole
change on this side. Its existing error contract (return `(nil, err)` on a
missing/unreadable/malformed file) is what R7 rides on: callers already
treat a `readInstanceSettings` error as "nothing to act on," and this
derivation is one more such caller.

**`internal/cli/dispatch.go`** — in `runDispatch`, immediately before the
current step 9b (`buildDispatchPassthrough` call, today at line 534):

```go
// (9a-derive) Read the instance's materialized settings once, ahead of the
// passthrough build so a derived --permission-mode can ride the same argv
// this call produces. The 9c/9d consumers below reuse this same `inst`
// value instead of reading the file again.
inst, _ := readInstanceSettings(instancePath)
if dispatchPermissionMode == "" &&
    spec.Flags.PermissionMode == "--permission-mode" &&
    inst != nil && inst.Permissions != nil &&
    inst.Permissions.DefaultMode == "bypassPermissions" {
    dispatchPermissionMode = "bypassPermissions"
}
passthrough := buildDispatchPassthrough(spec.Flags, slug, resolvedModel)
```

The existing `inst, _ := readInstanceSettings(instancePath)` line currently
at step 9c is deleted; the block there uses the `inst` bound above instead.
`buildDispatchPassthrough` itself is unchanged — it already forwards
`dispatchPermissionMode` when non-empty (`internal/cli/dispatch.go:953`);
this design only changes what value that variable holds by the time the
call happens.

The `spec.Flags.PermissionMode == "--permission-mode"` comparison is DD3's
gate. It reads directly off the already-resolved `LaunchSpec` for the
dispatched agent (`spec`, already in scope at this point in `runDispatch`)
rather than introducing a new per-agent capability flag — Claude's spelling
is the one value this comparison needs to recognize, since Codex's `spec`
has `PermissionMode: "--sandbox"` and every other declared agent (today,
just these two) has `""` in that field where not implemented.

## Implementation Approach

1. Add the `Permissions` field to `instanceSettings` in
   `dispatch_plugins.go`.
2. Move the `readInstanceSettings` call from step 9c to immediately before
   the `buildDispatchPassthrough` call (new step 9a-derive), and delete the
   now-redundant call at the old site.
3. Add the derivation conditional (DD3's agent-scoping check, R1's
   bypass-value check, R7's nil-safety) immediately after the moved read
   and before `buildDispatchPassthrough` is called.
4. Add/extend tests: a fixture with `permissions.defaultMode:
   "bypassPermissions"` and no explicit flag asserts the derived flag
   appears (AC1); an explicit `--permission-mode` asserts it wins (AC2); no
   `permissions` key, and `"ask"`, assert nothing forwarded (AC3); a Codex
   `LaunchSpec` fixture asserts nothing forwarded regardless of the
   materialized settings (AC4); a `workspace.toml`-vs-materialized-settings
   disagreement fixture asserts the materialized value wins (AC6); an
   absent/malformed settings file asserts no flag and no dispatch failure
   (AC7); the existing `dispatch_remotecontrol_roundtrip_test.go` and
   `dispatch_keepalive_roundtrip_test.go` suites are re-run unmodified to
   confirm the call-site move didn't regress them (AC8).
5. Run `go test ./internal/cli/... -run TestDispatch -v` and the full
   `go test ./...` suite per the PRD's Validation.

## Security Considerations

The derivation reads a file niwa itself already wrote
(`<instance>/.claude/settings.json`, via `RootSettingsMaterializer`) into an
argv element for a process niwa itself launches with `cmd.Env =
os.Environ()` in the same operator's own security context — no new trust
boundary is crossed. The value forwarded is a closed enum member
(`"bypassPermissions"`), never interpolated as a free-form string, and it
only ever *widens* a worker's permission posture to match what the
workspace's own configuration already declared, on the same instance the
operator already created. `spec.Flags.PermissionMode` is read from the
statically-declared `launchSpecs` table (`internal/agentplan/dispatch.go`),
not from any user- or network-supplied input, so DD3's gate is not an
injectable comparison. No new file is read that wasn't already read by this
same function a few lines later; no new write path is introduced.

## Consequences

**Positive:** Bypass-configured workspaces get working unattended dispatch
again, with zero new operator-facing surface. The instance-settings read
consolidates from two call sites to one, which is a net simplification of
`runDispatch`, not just a feature add.

**Negative:** `runDispatch`'s step-9 sequence gains one more
inter-dependency (9c/9d now depend on a value bound earlier than before),
which a future reader has to trace back one more line than today. Mitigated
by the comment on the moved read explaining why it moved and what depends
on it.

**Mitigations:** AC8 pins the existing remote-control/keep-alive behavior
against regression from the call-site move, so a future refactor that
breaks the dependency fails a test rather than silently drifting.
