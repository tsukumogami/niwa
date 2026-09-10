---
schema: design/v1
status: Planned
problem: |
  `runDispatch` sanitizes `--name` into a slug, spends the dispatch's 32 random bits on the
  instance directory name, and passes the bare slug to `buildDispatchPassthrough` as the
  worker's display name. Two dispatches with the same `--name` therefore forward the same
  Claude Code session name, which is the peer address. niwa never prints the name, never
  records it, and has no seam to substitute its random source in tests.
decision: |
  Reuse the instance directory's random token as the session name's distinguishing part.
  `runDispatch` draws the token once through a replaceable reader, builds the instance
  prefix and the session name (`<slug>-<token>`) from it, and hands the finished name to
  the unchanged `buildDispatchPassthrough`, so `niwa watch` keeps its behavior. The name is
  forwarded, recorded on the session mapping (`session_name`, omitempty) and printed as
  `  session name: <name>` only when the slug is non-empty and the launched agent declares
  a display-name flag.
  `niwa list` joins the mapping to show the recorded name in text and JSON.
rationale: |
  Reusing the token needs no second random read, meets the PRD's 32-bit floor exactly, and
  makes a worker's name and its instance directory legible as a pair (`review-4e33acfa`
  lives in `<config>+review-4e33acfa`). Recording the name, rather than deriving it, is the
  only way `niwa list` can avoid showing pre-change and Codex sessions a name they don't
  answer to, because their mappings look identical to new ones. Composing the name in the
  dispatch caller, not in the shared builder, keeps `niwa watch` and the builder's
  existing tests untouched, and gating on the declared flag spelling keeps the dispatch
  path free of agent names.
upstream: docs/prds/PRD-session-name-collision.md
user_visible_surface: true
---

# DESIGN: Unique session names for dispatched workers

## Status

Planned

## Context and Problem Statement

`niwa dispatch` launches a background Claude Code worker per task. Its naming path runs
through `runDispatch` in `internal/cli/dispatch.go`:

1. `slug := sanitizeInstanceSlug(dispatchName)` reduces `--name` to `[a-z0-9_]`, capped at
   40 runes, with no leading or trailing `_`.
2. `namePrefix, err := dispatchNameSuffix(slug)` reads 4 bytes from `crypto/rand`, hex
   encodes them, and returns `<slug>-<8hex>` (or `-<8hex>` with no slug). That becomes the
   instance directory `<config>+<slug>-<8hex>`.
3. `passthrough := buildDispatchPassthrough(spec.Flags, slug, resolvedModel)` forwards the
   bare slug after the agent's display-name flag (`--name` for Claude; Codex declares none,
   so nothing is forwarded).
4. `workspace.WriteSessionMapping` records the session keyed on its UUID. The mapping has
   no field for the forwarded name.
5. The success block prints `Dispatched session <uuid>`, `  instance: <path>`, and the
   agent's management hints. The name never appears.

So two dispatches with `--name review` get distinct instance directories and one shared
session name. In Claude Code that name is how peers address a session, across every
background, interactive, local and remote session on the account, and a send to a name two
live sessions share has no documented destination. The accepted PRD
(`docs/prds/PRD-session-name-collision.md`) settles what must change. This design answers
how, against the code above.

The technical gaps are four. The random token is spent on one string when two need it.
`dispatchNameSuffix` calls `crypto/rand.Read` directly, so no test can fix its output. The
session mapping, which is the only per-dispatch record `niwa list` joins against, has no
place to hold a name. And `buildDispatchPassthrough` is shared with `niwa watch`
(`watch.go:576` on resume, `watch.go:836` on staging), where the value in the display-name
slot doubles as a staged-record filename and a handle the developer types, so the builder
itself can't be the place the name changes.

## Decision Drivers

Each driver carries the PRD requirement it comes from.

- **D1. A unique forwarded name (R1, R2, R3).** Two named dispatches forward different
  names in R2's `<slug>-<hex>` format, from at least 32 random bits, with no lookup or
  lock of other sessions.
- **D2. A replaceable random source (R3).** Tests must be able to fix the random bytes, and
  a read failure must stop the dispatch before any instance is provisioned or worker
  launched.
- **D3. Report and record only what was forwarded (R6, R8, R9, R12).** The report line
  and the `niwa list` line and field appear only for dispatches that actually forwarded a
  name. niwa never derives a name from an instance directory name.
- **D4. Existing output and neighbors unchanged (R7, R10, R11, R13).** The headline, the
  instance line, and the hint lines keep their text and order. `niwa watch`, `niwa create
  --name`, instance directory naming, and reaper eligibility are unchanged. Mappings
  written before the change read back and list exactly as today.
- **D5. Agent-neutral dispatch path (R16).** `internal/cli/dispatch_layout_test.go` forbids
  agent constants and agent-name literals in the dispatch-path files. Any per-agent
  difference keys on the agent's declared flags.
- **D6. No added dispatch-time lookups (R15).** The name depends only on the slug and the
  random source.
- **D7. Small, reviewable change.** The fix should touch few files, add no package, and keep
  the existing tests meaningful rather than rewriting them.

## Considered Options

### Decision 1: Where the distinguishing part comes from

The name needs 32 random bits. The dispatch already draws 32 for the instance name.

#### Chosen: Reuse the instance directory's token

Draw the 4 bytes once, through a package-level reader, and use the same 8-hex token in both
the instance name and the session name. The worker `review-4e33acfa` then lives in
`<config>+review-4e33acfa`, so a developer can match an Agent View row to a `niwa list`
entry by eye. Uniqueness is exactly what instance names already have. A stub that returns
`0xab` for every read produces `review-abababab` in both places.

#### Alternatives Considered

- **Draw a second, independent token for the name.** Also meets the 32-bit floor, and keeps
  the two identifiers statistically independent. Rejected because independence buys
  nothing here (neither identifier is a secret, and both need only be unique), while it
  costs the visual pairing between a worker and its instance and adds a second read that
  can fail. It also leaves D1's "no lookup" property no stronger than reuse does.
- **A shorter token, such as the harness's 6-hex row references.** Reads better in Agent
  View. Rejected by the PRD's 32-bit floor (R3): the harness's references disambiguate rows
  in one listing and carry no uniqueness guarantee.
- **Derive the token from the session UUID.** Attractive because the UUID is unique by
  construction, which would turn R1's probabilistic uniqueness into certainty. Rejected on
  ordering: the harness assigns the UUID when the worker starts, and `dispatchCapture`
  learns it only after launch, while the name has to be on the launch command line (R4,
  D1). Renaming the session after launch would need a harness verb niwa doesn't have.

### Decision 2: Where the forwarded name is recorded for `niwa list`

`niwa list` must show the name for new dispatches and must never show a name a session
doesn't hold (R8, R9).

#### Chosen: A `session_name` field on the session mapping

Add `SessionName string` with `json:"session_name,omitempty"` to
`workspace.SessionMapping`, set only when the dispatch forwarded a name. Add the same
field, also `omitempty`, to `workspace.InstanceRecord`, and fill it in
`annotateFromSessionMappings`, the join `niwa list` already runs to fill `KeepAlive`.
Legacy mappings have no key, decode to `""`, and list exactly as today; their JSON records
keep today's shape. This is the pattern the mapping already uses for `Handle`, `Origin`
and `KeepAlive`.

#### Alternatives Considered

- **Derive the name from the instance name, gated on a marker.** Strip `<config>+` from
  `<config>+review-4e33acfa` for mappings that look like named Claude dispatches. Rejected:
  a mapping written before this change has the same `Origin: "dispatch"`, the same agent,
  and the same instance-name shape as one written after it, so no marker on disk separates
  a session answering to `review` from one answering to `review-4e33acfa`. Adding a marker
  is recording the name with extra steps. The PRD's R9 rules derivation out.
- **A separate per-instance file holding the name.** Keeps the mapping schema untouched.
  Rejected because it adds a second store `niwa list`, `niwa reap` and the snapshot writer
  would all have to learn about, while the mapping is already the per-dispatch record that
  survives config swaps and that `niwa list` already joins.

### Decision 3: Where the name is composed

The PRD's R11 already rules out the shared helpers, so this is recorded for traceability
rather than as an open choice.

#### Chosen: In `runDispatch`, passed through the unchanged builder

`runDispatch` composes the name and passes it to `buildDispatchPassthrough` where it passes
`slug` today. The builder keeps its signature and its "emit only when both flag and value
are non-empty" guard. `niwa watch` keeps calling the builder with its own slug or handle and
sees no change, and `dispatch_wiring_remotecontrol_test.go`, which compares argv against a
second builder call with an empty slug, stays valid.

#### Alternatives Considered

- **Append the suffix inside `buildDispatchPassthrough`.** One edit site. Rejected because
  the builder serves `niwa watch`, whose display-name value is also its staged-record
  filename and the handle it prints, so watch sessions would silently change names (R11).
- **Forward `dispatchNameSuffix(slug)` directly when the slug is non-empty.** The simplest
  change: for a named dispatch that string already is `<slug>-<token>`. Rejected in favor of
  the split because it leaves the forwarded name defined as "the instance prefix, when it
  happens to have no leading `-`", which is exactly the coupling the security review warns
  about (the no-slug form is `-<token>`). A separate `dispatchSessionName` returning `""` for
  an empty slug makes the no-leading-dash property hold by construction and makes R15's
  inputs-only rule checkable by reading one function.
- **Append the suffix inside `sanitizeInstanceSlug`.** Rejected because the sanitizer also
  names `niwa create --name` instances and watch slugs, so both would change (R11), and the
  instance name would gain a second suffix.

## Decision Outcome

The three choices fit together as one small change in the dispatch caller. `runDispatch`
reads the token once through `dispatchRandReader`, builds the instance prefix exactly as
today from the slug and token, and builds the session name as `<slug>-<token>` when the
slug is non-empty. Whether a name is forwarded is decided by the agent's declared flag
spelling: when `spec.Flags.DisplayName` is non-empty and the slug is non-empty, the name is
forwarded, recorded on the mapping, and printed. Otherwise nothing changes from today.
`niwa list` reads the recorded value and shows it. Instance names, the builder, the
sanitizer and `niwa watch` are untouched.

Every PRD requirement lands in a named place: uniqueness and format (R1 to R4) in the token
and composition functions, lifetime (R5) because nothing but the dispatch ever forwards the
name, reporting (R6, R7) in the success block, recovery (R8, R9) in the mapping field and the
list join, the unchanged surfaces (R10 to R13) because the new behavior is gated and additive,
documentation (R14) in the files named under Solution Architecture, and R15 and R16 because the composition
function is pure and the gate is a flag spelling.

## Solution Architecture

### Components

**`internal/cli/dispatch.go`**

- `var dispatchRandReader io.Reader = rand.Reader`: the replaceable random source (D2).
  Tests swap it, as they already swap `dispatchLaunch`, `dispatchCapture` and
  `provisionInstanceFunc`.
- `func newDispatchToken() (string, error)`: reads 4 bytes from `dispatchRandReader` with
  `io.ReadFull` and returns 8 lowercase hex digits. A read error returns an error, and
  `runDispatch` fails with the existing "generating instance name" message before
  provisioning.
- `func dispatchInstancePrefix(slug, token string) string`: returns `slug + "-" + token`, or
  `"-" + token` with no slug, the same strings `dispatchNameSuffix` returns today.
- `dispatchNameSuffix(slug)` stays, as a thin wrapper that calls `newDispatchToken` and then
  `dispatchInstancePrefix`. It has a second production caller, `stageReview` in
  `internal/cli/watch.go`, which names watch review instances with it; keeping the wrapper
  is what keeps `niwa watch` compiling and unchanged (R11). `runDispatch` stops calling it,
  because it needs the token itself.
- `const dispatchSessionNamePattern = "^[a-z0-9](?:[a-z0-9_]{0,38}[a-z0-9])?-[0-9a-f]{8}$"` and a compiled
  regexp beside it: the one definition of the forwarded-name shape, used by the dispatch
  tests and by `niwa list`'s read-side check, so the token width and the 40-character slug
  cap are written once and a tampered mapping can't carry an oversized name.
- `func dispatchSessionName(slug, token string) string`: returns `slug + "-" + token`, or `""`
  with no slug. Pure: its only inputs are the two strings (R15).
- `runDispatch` changes in four places. At the naming step it calls `newDispatchToken`,
  then `dispatchInstancePrefix`. Before building the passthrough it computes

  ```go
  forwardedName := ""
  if spec.Flags.DisplayName != "" {
      forwardedName = dispatchSessionName(slug, token)
  }
  passthrough := buildDispatchPassthrough(spec.Flags, forwardedName, resolvedModel)
  ```

  The condition names a flag spelling, not an agent, so the layout tests pass (D5). At the
  mapping write it sets `SessionName: forwardedName`. In the success block, after the
  `  instance:` line, it prints `  session name: <forwardedName>` when `forwardedName` is
  non-empty.
- The `--name` flag help and the `buildDispatchPassthrough` doc comment are updated (R14).

**`internal/workspace/session_map.go`**

- `SessionMapping` gains `SessionName string` with `json:"session_name,omitempty"` and a
  comment saying it holds the display name the dispatch forwarded, empty when none was.

**`internal/workspace/state.go`**

- `InstanceRecord` gains `SessionName string` with `json:"session_name,omitempty"`, declared
  after `KeepAlive` so existing JSON keys keep their order, and filled at the CLI layer like
  `KeepAlive`.

**`internal/cli/list.go`**

- `annotateFromSessionMappings` also collects `m.SessionName` by `m.InstancePath` and sets
  it on the matching record, but only when the value matches
  `dispatchSessionNamePattern`; anything else is treated as absent. The field is
  display-only and never becomes argv, a path, or a lookup key.
- The text output prints `  session name: <name>` on the line after the instance name and
  before any `  resume:` line, when the record carries one.
- The long help and the `--json` flag description mention the line and the `session_name`
  field (R14).

**Documentation**

The user-facing documentation for this change is the `--name` flag help, the `niwa list`
long help, and the `/dispatch` root skill. No guide under `docs/guides/` documents dispatch
naming or the success block today, so none needs a new section.

- `docs/designs/current/DESIGN-instance-dispatch.md`: three passages go stale today. The
  D1 flag description (around lines 88-89, "a slug that names both the instance and the
  Claude session"), the D2 forwarding sentence (around line 167, `claude --bg --name
  <slug>`), and the collision-safety and `--name`/`--label` sentences (around lines
  173-176). The collision-safety sentence says session names carry the suffix; the others
  describe `<slug>-<token>`.
- `internal/workspace/rootskills/dispatch/SKILL.md`: the `--name` description names the
  suffix; the report-back step relays the `session name:` line alongside the session id.
  `niwa apply` rewrites installed root skills, so existing workspaces pick it up. The check
  goes in `internal/workspace/root_materializer_test.go`.

### Key Interfaces

- Forwarded value: one argv element after the agent's display-name flag, matching
  `dispatchSessionNamePattern` (the token is exactly 8 hex; the PRD's `{8,}` allows longer).
- Dispatch stdout, named Claude dispatch:

  ```
  Dispatched session 3f2a...-...
    instance: /path/to/ws/tsuku+review-4e33acfa
    session name: review-4e33acfa
    claude attach 9c1e0b7f
    claude logs 9c1e0b7f
    claude stop 9c1e0b7f
  ```

  The hint handle (`9c1e0b7f` here) comes from the harness's own session records via
  `dispatchCapture`, not from the token; only the instance name and the session name share
  the token.

- Mapping JSON: a new optional key `"session_name": "review-4e33acfa"`.
- `niwa list` text: `tsuku+review-4e33acfa`, then `  session name: review-4e33acfa`, then
  `  resume: ...`. `niwa list --json`: a new optional key `session_name` on the record.

### Data Flow

`--name` → `sanitizeInstanceSlug` → slug. `dispatchRandReader` → `newDispatchToken` →
token. (slug, token) → `dispatchInstancePrefix` → instance name, unchanged. (slug, token) →
`dispatchSessionName` → forwarded name when the agent declares a display-name flag → launch
argv, mapping `session_name`, and the success block's `session name:` line. Later,
`niwa list` → `ListSessionMappings` → `InstanceRecord.SessionName` → the text line and the
JSON key.

## Implementation Approach

One pull request, in four steps ordered so each has a baseline to test against.

### Step 1: Pin today's behavior

This is the largest piece of test work in the change, because `niwa watch` has no seam to
observe what it launches. `stageReview` and `continueReview` take a concrete
`*github.APIClient` and call `watch.FetchPRHead` directly, and no watch test stubs
`dispatchLaunch`. Step 1 first adds the minimal seams: a package-level function variable
for the PR-head fetch, used by both paths, and use of the existing `dispatchLaunch`
indirection to record argv. Then it adds characterization tests before changing code: `niwa watch` staging and resume record the
display-name value they forward and their `staged review ... (handle ...)` and
`continued review ... (handle ...)` lines; a legacy mapping fixture (today's schema, no
`session_name`, instance `<config>+review-<8hex>`) with a golden of `niwa list` text and
`--json`, `niwa status`, `niwa reap`, and the hints, captured from the current code. These
must pass unchanged at the end.

### Step 2: Token, name, forwarding, mapping, report line

Add `dispatchRandReader`, `newDispatchToken`, `dispatchInstancePrefix` and
`dispatchSessionName`; rewire `runDispatch`; add `SessionMapping.SessionName`; print the
report line. Update `TestDispatch_Name_SlugInInstanceAndSession` to assert the suffixed
name. Add tests for the stubbed-byte name, the forwarded-value pattern
`^[a-z0-9](?:[a-z0-9_]{0,38}[a-z0-9])?-[0-9a-f]{8}$` for one-character and 40-rune slugs, the 64-goroutine
race check on the generator,
the random-read failure (no provisioning, no launch), the report line in all five success
exits and its absence on failure, the headline and `instance:` line, the absence of any
`claude ` substring in stdout for a stubbed agent whose binary isn't `claude`, a pin that
the attach step and the `niwa list` resume command forward no display-name flag, and the
unnamed and Codex-spec cases, plus a direct unit test that `dispatchSessionName("", token)`
returns `""`. The race test calls `newDispatchToken` and
`dispatchSessionName` together from 64 goroutines.

### Step 3: `niwa list`

Add `InstanceRecord.SessionName`, extend the join and the text output, and update the long
help. Add tests for the text line order, the JSON key, its absence for unnamed, Codex and
legacy mappings, a mapping whose `session_name` fails the pattern being shown as absent, a new test that the `niwa list` long help contains `session name:` and `session_name`, and
update `TestList_JSONShapeIsUnchanged`'s comment to allow the optional `session_name` key.

### Step 4: Documentation

Update `DESIGN-instance-dispatch.md`, the `/dispatch` root skill and its materializer test,
and confirm the `--name` help contains `suffix`.

Step 1 comes first because steps 2 and 3 touch code the watch and legacy tests must prove
unchanged. Steps 2 and 3 are separable but share the mapping field, so 3 follows 2.

## Security Considerations

**Input and argv.** The forwarded name is built only from the sanitized slug, which is
closed to `[a-z0-9_]` and never starts or ends with `_`, plus `-` and 8 lowercase hex
digits. It reaches the worker as one discrete argv element after the agent's display-name
flag, through the unchanged `buildDispatchPassthrough` and `exec`, never through a shell and
never concatenated into a command line, so the instance-dispatch design's flag-injection
rule still holds. The name never starts with `-`. That depends on `dispatchSessionName`
returning `""` for an empty slug; it must not reuse `dispatchInstancePrefix`, whose no-slug
form is `-<token>`. Tests pin that every forwarded value matches
`^[a-z0-9](?:[a-z0-9_]{0,38}[a-z0-9])?-[0-9a-f]{8}$` for one-character and 40-rune slugs, and that an unnamed
dispatch forwards no display-name pair.

**Random source.** The token still comes from `crypto/rand`, now read with `io.ReadFull`
through a package-level reader that only in-package tests replace. A read failure stops the
dispatch before any instance is provisioned or worker launched.

**Not a capability.** The name is a routing label among one account's sessions, not a secret
or an authorization. Anyone who can send to it can already address every session on the
account. The suffix changes which session a name resolves to, not who may send to it, so the
token's unpredictability isn't relied on for security.

**Storage and display.** `session_name` lives in the existing mapping file (directory
`0700`, file `0600`), next to more sensitive fields it already holds. It is display-only:
niwa never uses it as an argv element, a path component, or a lookup key, and resume
commands keep using the handle. Dispatched workers run as the same user and could rewrite a
mapping, so `niwa list` accepts a recorded `session_name` only when it matches the
forwarded-name pattern and otherwise treats it as absent. A tampered `session_name`
therefore can't push terminal escape sequences into the text output. When several mappings
point at the same instance path, the one with the latest `Created` time supplies the name.

**Exposure.** The name adds a random token to a slug already shown in Agent View and in the
instance directory name. The shared token lets someone who sees the session name infer the
instance directory's basename, which isn't secret and isn't used as a credential. No network
call, subprocess, dependency or new file is added, and the dispatch path still names no
agent.

**Residual risk.** Uniqueness is probabilistic at 32 bits, and sessions niwa didn't launch,
or launched before this change, can still carry colliding names. Two further risks sit inside
the existing same-user trust boundary and are accepted rather than mitigated. Any session on
the account, including a prompt-injected worker, can deliberately take the name a niwa
worker holds and so receive messages addressed to it. The token doesn't prevent that and
isn't meant to. And a same-user process that rewrites a mapping can make `niwa list` show a
well-formed but wrong name, because the recorded value isn't verified against the harness.
Neither needs escalation: both need an attacker already running as the developer.

## Consequences

### Positive

- Two dispatches with the same `--name` get separately addressable workers, and the name
  and instance directory share a token, so matching one to the other takes no lookup.
- The developer sees the name at dispatch and in `niwa list`, closing the gap where niwa
  forwarded a name it never showed.
- `niwa watch`, `niwa create --name`, instance naming, and legacy sessions are untouched,
  and characterization tests prove it.
- The dispatch gains a test seam for its random source that it lacked.

### Negative

- Forwarded names grow by 9 characters, up to 49 for a slug at the 40-rune cap, and Agent
  View may truncate long rows.
- Sessions dispatched before the change still answer to bare names and can collide with
  each other.
- The mapping and `niwa list --json` gain a field, which consumers that reject unknown keys
  would notice.
- Reusing the token ties the session name to the instance name: a future change to instance
  naming has to consider the session name too.
- `niwa list`'s read-side check hard-codes the token's width (8 hex digits): widening the
  token later would make older recorded names fail the check unless the pattern accepts
  both widths.

### Mitigations

- The PRD's Known Limitations name the Agent View width question and recommend a manual
  check with a maximum-length slug before release; the role stays at the front of the name,
  so a truncated row still shows it.
- Pre-change sessions are left alone deliberately; they age out as they're deleted.
- Both new keys are `omitempty`, so legacy, unnamed and Codex records keep their exact
  shape. New named Claude records do gain `session_name`; the PRD accepts that addition to
  `niwa list --json`, and `TestList_JSONShapeIsUnchanged`'s comment is updated to say so.
- `dispatchInstancePrefix` and `dispatchSessionName` sit next to each other with a comment
  saying they share the token, and the instance-name regex test pins the instance half.
- The pattern lives in one constant, `dispatchSessionNamePattern` (which also bounds the
  slug to 40 characters), so a width change is one
  edit that covers the dispatch tests and the list check together.
