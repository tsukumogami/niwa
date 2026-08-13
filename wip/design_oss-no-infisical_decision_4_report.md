# Decision 4: How the consolidated unresolved-key report is produced and delivered

Serves R6, R7, R8, R9, R14, R20 of `docs/prds/PRD-oss-no-infisical.md`.

## Context

### What exists today, verified

**There is no report.** Today an unresolvable key is an *error*, and the error
is the message. `resolveOne` returns on the first failure
(`internal/vault/resolve/resolve.go:539`, `:551`, `:558`), `walkTable` returns
that error immediately (`internal/vault/resolve/resolve.go:388-397`), and
`ResolveWorkspace` returns it up (`internal/vault/resolve/resolve.go:251-295`).
The walk aborts. Whichever key happened to be visited first is the only key the
user ever hears about. That is precisely the "not just the first key" failure R6
names.

**The walk order is a Go map iteration.** `walkTable` ranges over
`values map[string]config.MaybeSecret` with no sort
(`internal/vault/resolve/resolve.go:389`). Combined with fail-fast, *which* key
is named is nondeterministic across runs against identical input. The same
defect exists in `warnRecommended`, which ranges `t.Recommended` unsorted and
`Fprintf`s one line per miss (`internal/workspace/required.go:146-179`). By
contrast `checkRequiredKeys` sorts its collected misses by `(Scope, Key)` before
rendering (`internal/workspace/required.go:88-93`) — so niwa already contains
both the R7-conforming pattern and its violation, side by side in one file.

**The `Reporter` is a terminal device, not a data channel.** `Reporter`
(`internal/workspace/reporter.go:25-36`) wraps an `io.Writer` plus a TTY flag and
a spinner goroutine. `Log` writes a permanent line; `Warn` is `Log` with a
`"warning: "` prefix (`:137-148`); `Defer`/`DeferWarn` queue strings that
`FlushDeferred` replays through `Log` (`:153-170`). Everything it accepts is a
format string; nothing it accepts is retrievable. There is no way to ask a
Reporter what it was told.

Where the writer points, per surface:

| surface | Reporter target | citation |
|---|---|---|
| `niwa apply` | `os.Stderr`, TTY-aware | `internal/cli/apply.go:152` |
| `niwa create` | `os.Stderr`, TTY-aware | `internal/cli/create.go:167` |
| `niwa init` | `cmd.ErrOrStderr()` | `internal/cli/init.go:176` |
| `niwa dispatch` | `os.Stderr` (via `realProvisionInstance`) | `internal/cli/instance_from_hook.go:366` |
| SessionStart hook | `os.Stderr` (same function) | `internal/cli/instance_from_hook.go:366` |

**On the hook path, `Reporter.Warn` reaches the hook process's stderr and
nothing else.** Claude Code captures a SessionStart hook's stderr for its own
debug surface; it does not inject it into the session's context. The only
channel into the agent is `hookSpecificOutput.additionalContext` on stdout
(`internal/cli/instance_from_hook.go:287-326`). The codebase already knows this
— `realProvisionInstance` carries an explicit comment declining to replay
`result.Warnings` there: *"this runs from a Claude hook whose stdout is a
protocol, not a terminal someone is reading"*
(`internal/cli/instance_from_hook.go:376-378`). So for R14, `Reporter` is not a
delivery mechanism at all. The report must exist as a **value** the hook can
embed in JSON.

**Confirmed: a non-zero hook emits no payload.** `runInstanceHookStart` calls
`provisionInstanceFunc` at `internal/cli/instance_from_hook.go:172` and, on
error, `return`s at `:174` — thirteen lines before
`buildSessionStartInjection` (`:188`) and eighteen before the only
`cmd.OutOrStdout().Write` (`:192`). There is no `defer`, no error branch that
writes, no partial flush. The PRD's stronger justification for R14 is correct
against the code: exit 0 is not a courtesy, it is the only path to the agent.

**`provisionResult` carries only `{Name, Path}`**
(`internal/cli/instance_from_hook.go:91-94`), and it is the sole return channel
from the provisioning function to the hook. Whatever mechanism is chosen, this
struct must gain a report field. That is a fixed cost across every option.

**The existing aggregated unreachable warning is a different feature.**
`internal/workspace/apply.go:1177-1205` emits one `Reporter.Warn` per unique
unreachable provider — but the loop drains
`credentialPool.VaultUnreachableObservations()`
(`internal/workspace/credentialpool.go:497-521`), which is the *machine-identity
credential pool* (provider-auth tokens for cloning), not the env-secret
resolution path. It fires before `BuildBundle` and before
`ResolveAndMergeEffectiveConfig`. It is a good precedent — dedupe on append,
drain once, render at a fixed point in the pipeline — and a bad reuse target:
its observations are about credentials for reaching providers, not about
declared keys. The R6 report needs its own collector.

**`DisclosedNotices` must not be reused.** It is a once-per-workspace
suppression list persisted in instance/root state and consulted with
`sliceContains` before emitting (`internal/workspace/apply.go:463`, `:471`,
`:614`, `:624`, `:1248`; helpers in `internal/workspace/disclosure.go:1-66`). It
exists for *deprecation notices* — things a user needs to be told once. The
unresolved-key report is the opposite: R15 makes re-apply the recovery path, so
the second run is exactly when the user most needs to see whether the gap
closed. A first-run-only report would go silent on every subsequent apply while
the keys stayed missing. Suppression here would also break R19 in the other
direction, since "same output as today" for a fully-resolved workspace is
trivially satisfied by a mechanism with no state at all.

**The double-print is real.** In the instance loop, a failing instance is
printed eagerly to stderr with an `error: applying to %s: %v` prefix
(`internal/cli/apply.go:267`); the same error is then wrapped by
`combineInstanceErrors` into `apply failed for %s: %w`
(`internal/cli/apply.go:429-440`), returned at `:283`, and printed a second time
by `Execute` (`internal/cli/root.go:95`) — which the root command's own comment
describes as *"the single source of truth for error output"*
(`internal/cli/root.go:38-41`). For a one-line error this is mildly redundant.
For a fifteen-line consolidated report it would be a disaster, so this decision
has to address it.

**Deterministic env-file ordering already exists.** `EnvMaterializer.Materialize`
sorts keys before serializing so output is byte-stable
(`internal/workspace/materialize.go:1316-1327`). The R3 records must slot into
that same sorted sequence; the R6 report is a *different* aggregation (across
every repo and every target, grouped by cause) and cannot be produced there.

**Named-term-list precedent for R20.** `envPrefixBlocklist` and
`envSafeAllowlist` (`internal/workspace/envclassify.go:11-41`) are package-level
`[]string` vars consulted by classification code and asserted against by tests.
R20's prohibited-vocabulary list should be the same shape, in the same package
as the renderer.

### The import-direction constraint

`internal/vault/resolve` imports `internal/config` and `internal/vault`;
`internal/workspace` imports `resolve` (`internal/workspace/effective_config.go:9`).
The package doc explains why resolve is a sub-package at all — placing it inside
`internal/vault` would cycle through `config`
(`internal/vault/resolve/resolve.go:7-18`). So:

- The **cause** of a shortfall (provider unreachable / client absent / key not
  found / unsatisfiable) is known only inside `resolve`.
- The **declared level and description** are known only from the
  `EnvVarsTable.Required/Recommended/Optional` maps
  (`internal/config/config.go:217-219`), read today by
  `internal/workspace/required.go`.
- The **promotion miss** (R11) is known in the settings materializer.

No single existing package sees all three. Whatever holds the report must be a
**leaf package** that `resolve`, `workspace`, and `cli` can all import. This
constraint eliminates any design that keeps the report inside `resolve`'s return
values or inside `workspace` alone.

### R9's two causes are not currently distinguishable

`vault.ErrProviderUnreachable` (`internal/vault/errors.go:16`) is returned for
both cases R9 requires to differ. In `runInfisicalExport`, a failure to *start*
the process — the comment says "e.g., CLI not installed" — wraps
`ErrProviderUnreachable` with the raw `os/exec` error
(`internal/vault/infisical/subprocess.go:138-146`), and a non-zero exit that
looks like an auth failure wraps the same sentinel
(`:148-154`). `defaultCommander.Run` returns exit code `-1` and a non-nil `err`
when the binary is missing (`internal/vault/infisical/subprocess.go:50-56`).
The information exists (`errors.Is(err, exec.ErrNotFound)`) but is not carried
as a typed signal. Every option below must add one; I treat it as shared cost
and specify it once in the recommendation.

## Options

### Option A — Report value returned up the call stack

**Mechanism.** A new leaf package (say `internal/keyreport`) defines an
immutable `Report`. `resolve.ResolveWorkspace` and `ResolveGlobalOverride` gain
a second return value, `keyreport.Report`.
`ResolveAndMergeEffectiveConfig` (`internal/workspace/effective_config.go:66-105`)
threads it out as a fifth return value. `runPipeline` merges it with the
required-key findings and the promotion findings and stores it on
`pipelineResult` (`internal/workspace/apply.go:323-350`), alongside the
`warnings`, `shadows`, `authSources` and `setupIncomplete` fields that already
carry non-error diagnostics out of the pipeline as data. `Applier.Create` and
`Applier.Apply` gain a report return value. Each CLI surface renders it.

**Code impact.** Signature changes at every layer:
`ResolveWorkspace` (2 call sites in `effective_config.go`, plus
`apply.go:1074`), `ResolveGlobalOverride`, `ResolveAndMergeEffectiveConfig`
(called from `apply.go:1308` and from the worktree path in
`internal/cli/session_lifecycle_cmd.go`), `Applier.Create`
(`internal/workspace/apply.go:388`; called from `internal/cli/create.go:247`,
`internal/cli/instance_from_hook.go:417`, and the init bootstrap seam),
`Applier.Apply` (`internal/workspace/apply.go:546`; called from
`internal/cli/apply.go:262` and the root materializer). Plus every test that
calls any of them. Roughly six signatures, twenty-odd call sites, and the test
suite.

**Trade-offs.**

*For:* The dependency is explicit and visible in every signature. No hidden
mutable state. A reader following `Apply` sees the report exists. It matches the
`pipelineResult` precedent, which is genuinely how this codebase already moves
non-fatal findings out of the pipeline.

*Against, and decisively:* **the report is lost on exactly the paths that most
need it.** R10 (required key on a reachable provider) and R12 (strict mode) both
keep today's fatal semantics. On a fatal path `runPipeline` returns
`(nil, err)`, `Create` returns `("", err)` after `os.RemoveAll(instanceRoot)`
(`internal/workspace/apply.go:457-460`), and a returned-value report is nil. But
a strict-mode failure should still name *every* affected key, not the first —
R6's "single consolidated report" is about the shape of the message, and strict
mode is precisely when a user is trying to enumerate what is missing. Option A
forces either a second, error-carried copy of the report (a `*ReportError` type
threaded through `errors.As`) or an accepted regression on the strict path.
Both are worse than the alternative.

Secondary: the signature churn is real but bounded; it is not the reason to
reject A. Also, `Apply` currently returns a bare `error`, so `(Report, error)`
makes the common no-report case noisier at twenty call sites.

### Option B — Collector supplied by the caller, drained by the caller

**Mechanism.** The leaf package defines a mutable `*keyreport.Collector` (write
side: `Record(entry)`; dedupe on append, no ordering guarantee) and an immutable
`Report` produced by `Collector.Report()` (read side: sorted, frozen). The
collector is passed *down* as an option field, never returned:

- `resolve.ResolveOptions` gains `Collector *keyreport.Collector`
  (`internal/vault/resolve/resolve.go:35-72`) beside the existing `Stderr` field,
  which is already exactly this pattern — a caller-supplied sink for resolver
  diagnostics.
- `EffectiveConfigOptions` gains the same field
  (`internal/workspace/effective_config.go:27-31`) and forwards it to both
  resolve calls.
- `Applier` gains a `SecretReport *keyreport.Collector` field beside `Reporter`,
  initialized non-nil by `NewApplier` (`internal/workspace/apply.go:169-195`)
  exactly as `Reporter` is at `:174`.
- `checkRequiredKeys` and the promotion check record into the same collector.
- After `Create`/`Apply` returns — **error or not** — the surface reads
  `applier.SecretReport.Report()` and renders.

`provisionResult` gains a `Report keyreport.Report` field, populated in
`realProvisionInstance` after `applier.Create` returns
(`internal/cli/instance_from_hook.go:417-422`), and
`buildSessionStartInjection` gains it as a second parameter.

**Code impact.** Two new option fields, one new `Applier` field, one new
`provisionResult` field, one new parameter on `buildSessionStartInjection`. Zero
existing signature changes. Existing tests compile unchanged; new tests
construct a collector explicitly. The resolver change from fail-fast to
record-and-continue is required here as in every option, and is the largest
single edit: `resolveOne` returns `(MaybeSecret{}, nil)` after recording,
`walkTable` stops propagating (`internal/vault/resolve/resolve.go:388-397`),
and the strict gate moves to a single post-walk `collector.HasFatal()` check.

**Trade-offs.**

*For:* Survives the error path. On an R10/R12 fatal return the collector is
still populated, still owned by the caller, and still renderable — which is what
makes "one consolidated report" true on the strict path too, not just the
tolerant one. Solves the import-direction problem cleanly: `resolve` writes to a
leaf type it can import; `cli` reads it. Gives R14 the report as a value with no
contortion. Sorting on read (`Report()` sorts by `(cause, scope, key)`) makes
R7 hold *without* touching the nondeterministic map walks in
`walkTable` (`resolve.go:389`) or `warnRecommended` (`required.go:147`) — the
ordering defect stops mattering because ordering is imposed at the boundary.

*Against:* Caller-supplied mutable state is less visible than a return value. A
new surface that forgets to drain the collector silently emits nothing. Mitigated
by `NewApplier` initializing it (so it is never nil) and by the fact that there
are exactly five surfaces, all enumerated in R1 and all covered by acceptance
criteria. Also: the collector is not goroutine-safe by default, and
`runPipeline` clones repos through a worker pool
(`internal/workspace/apply.go:1349-1360`). Resolution happens at `:1308`, before
the pool starts, and the promotion check happens in the settings materializer
which runs per-repo — so either the materializer's records go through a mutex or
they are collected into per-repo slices and merged after the pool drains. The
`CredentialPool` has the identical hazard and documents it rather than solving
it (`internal/workspace/credentialpool.go:221-227`); a mutex on `Record` is
three lines and avoids inheriting that debt.

### Option C — Render inline at the point of detection

**Mechanism.** No new type. `resolveOne` writes each finding to
`w.stderr` (which already receives the `AllowMissing` warning at
`internal/vault/resolve/resolve.go:531-535`), `checkRequiredKeys` keeps writing
to `stderrOut` (`internal/workspace/required.go:95-102`), the promotion check
writes at its own site. The apply path already wires all of these to
`a.Reporter.Writer()` (`internal/workspace/apply.go:1313`, `:1325`), which
routes each `Write` through `Reporter.Log`
(`internal/workspace/reporter.go:175-194`). To make it one report rather than N
lines, `Reporter` gains a buffered "report section" — findings accumulate and a
`FlushReport()` renders them as a block, the same shape as `Defer`/
`FlushDeferred` (`internal/workspace/reporter.go:153-170`).

**Code impact.** Smallest of the three by line count. One new pair of methods on
`Reporter`, one `Stderr`-to-`Reporter` plumbing change so the resolver can call
the buffering method rather than `Write` (which loses structure), and no
signature changes.

**Trade-offs.**

*For:* Follows the grain of what is already there. `Defer`/`FlushDeferred` is
literally a buffered-then-rendered block, and `apply.go:528-531` already drains
`result.warnings` into it. Nothing new to learn.

*Against, fatally:* **`Reporter` output is not recoverable as data, so R14 is
unreachable.** The hook needs the report as a string inside a JSON field
(`internal/cli/instance_from_hook.go:287-326`); a Reporter writes to
`os.Stderr` and offers no read-back. Making it readable means adding a
`ReportText() string` accessor — at which point the Reporter *is* the collector,
and it is a collector wearing a terminal device's interface, coupling
`internal/cli`'s JSON assembly to a spinner-aware ANSI writer. The
`permanentOutput` test helper exists precisely because a Reporter's byte stream
interleaves spinner frames with real content and reconstructing intent from it
requires a documented reduction rule
(`internal/workspace/permanent_output_test.go:16-41`). Deriving R14's payload
from that stream would be the worst kind of coupling.

Second problem: **R20 becomes untestable at the right altitude.** A
prohibited-vocabulary test wants to assert against the rendered report as a
value. With C the only artifact is a mixed stderr stream also containing clone
progress, setup verdicts, and shadow diagnostics. The test would have to
string-search the whole stream and could not tell a prohibited term in the
report from the same term in an unrelated line.

Third: **R7 gets harder, not easier.** Inline rendering means order is emission
order, which is the map-iteration order the code has today
(`resolve.go:389`, `required.go:147`). Fixing R7 under C means sorting the
resolver's walk and `warnRecommended`'s walk and the promotion check's walk
independently, and keeping all three consistent forever. Sorting once at a
boundary is one place.

### Option D — Derive the report from the materialized instance

**Mechanism.** Skip in-run collection entirely. R3 already requires generated
dotenv files to carry a machine-recoverable record of each omitted key with its
declared description, and R3's acceptance criterion says niwa's own reader
recovers name and description. So after materialization, read the instance's
generated env files back (`parseEnvFile` already exists at
`internal/workspace/materialize.go:1438`) and synthesize the report. The hook
gets it for free: `buildSessionStartInjection` already reads from the instance
directory (`internal/cli/instance_from_hook.go:300-304`).

**Code impact.** Near zero in the pipeline; one new reader plus a renderer.

**Trade-offs.**

*For:* No plumbing at all. The report and the files cannot disagree, because one
is derived from the other. Re-derivable later by any command — a future
"what does this workspace need" diagnostic (explicitly out of scope in the PRD,
but adjacent) would fall out of the same reader.

*Against:* It cannot satisfy R9 or R8's distinction, and that is not fixable
within R3. The R3 record is a per-key, per-file artifact carrying name and
description. The *cause* — no provider configured, versus provider unreachable,
versus client binary absent — is a property of the run, not of the key, and R3
deliberately does not put it in the file (R18 and the record's
"SHALL NOT carry a value" constraint keep the record minimal). Adding cause to
the record would put run-scoped, host-scoped state into a file that R4 requires
be rewritten on the next apply, and would leak "this host has no client
installed" into a file a contributor might commit.

Worse: it is structurally blind to keys whose *target file was never written*.
`EnvMaterializer.Materialize` returns early when `len(vars) == 0`
(`internal/workspace/materialize.go:1312-1314`), and R11's promotion misses land
in generated Claude settings, not env files. And R3a explicitly accepts that
non-dotenv targets are unreadable by niwa's reader — so a workspace with a
json-format target would produce a *silently short* report, which is R6's
"SHALL NOT name only the first key" failure wearing a different hat.

## Recommendation

**Option B: a caller-supplied collector in a new leaf package, drained and
rendered by each command surface.**

### Shape

New package `internal/keyreport` (leaf: imports only stdlib and
`internal/config` for the level enum, if that; ideally stdlib only).

```
type Cause int  // CauseUnsatisfiable, CauseProviderUnreachable, CauseClientAbsent, CauseRequiredShortfall, CausePromotion
type Level int  // LevelRequired, LevelRecommended, LevelOptional

type Entry struct { Cause Cause; Level Level; Scope, Key, Description, ProviderKind string }

type Collector struct{ ... }        // mutable, mutex-guarded, dedupe on (Scope,Key)
func (c *Collector) Record(e Entry)
func (c *Collector) Report() Report // sorts, freezes

type Report struct{ ... }
func (r Report) Empty() bool
func (r Report) RenderTerminal() string  // operator-facing block
func (r Report) RenderContext() string   // agent-facing prose for additionalContext
```

Wiring, in full:

1. `resolve.ResolveOptions` gains `Collector *keyreport.Collector`, beside the
   existing caller-supplied `Stderr` sink
   (`internal/vault/resolve/resolve.go:69-71`). `resolveOne` records instead of
   returning an error for the three shortfall branches
   (`internal/vault/resolve/resolve.go:526-561`) and returns
   `(config.MaybeSecret{}, nil)`. The `ref.Optional` branch at `:527-529` is
   untouched — that is R2a's deliberate empty and must stay silent and
   unrecorded. `walkTable` no longer aborts.
2. `EffectiveConfigOptions` gains the same field and forwards to both resolve
   calls (`internal/workspace/effective_config.go:27-31`, `:74-98`). The
   worktree path passes `nil` — R21 keeps that path tolerant and unreported.
3. `Applier` gains `SecretReport *keyreport.Collector`, initialized in
   `NewApplier` beside `Reporter` (`internal/workspace/apply.go:174`).
4. `checkRequiredKeys` and `warnRecommended` record into the collector rather
   than formatting (`internal/workspace/required.go:47-179`). Their existing
   `(Scope, Key)` sort at `:88-93` becomes the collector's comparator; the
   unsorted `warnRecommended` loop stops mattering because `Report()` sorts.
5. The promotion check records with `CausePromotion` (R11).
6. **Every surface renders after the call returns, on both the success and the
   error path.**

### Cause of a provider-unreachable failure

Add `vault.ErrClientNotInstalled` in `internal/vault/errors.go` beside
`ErrProviderUnreachable` (`:16`). In `runInfisicalExport`, the start-failure
branch (`internal/vault/infisical/subprocess.go:138-146`) wraps both sentinels
when `errors.Is(err, exec.ErrNotFound)`; every other unreachability keeps
`ErrProviderUnreachable` alone. `resolveOne` maps to `CauseClientAbsent` or
`CauseProviderUnreachable` accordingly. This is the minimum typed signal R9
needs and it costs one sentinel plus one `errors.Is`.

### Rendering per surface

| surface | rendering | placement |
|---|---|---|
| `niwa apply`, `niwa create`, `niwa init` | `RenderTerminal()` via one `Reporter.DeferWarn("%s", ...)` so `FlushDeferred` emits it as a single multi-line block below the summary (`internal/workspace/apply.go:527-531`) | stderr |
| `niwa dispatch` | same, through the same `os.Stderr` Reporter (`internal/cli/instance_from_hook.go:366`) | operator's stderr |
| SessionStart hook | `RenderContext()` appended to the `additionalContext` string in `buildSessionStartInjection` (`internal/cli/instance_from_hook.go:300-326`), exit 0 | stdout JSON |

`niwa create --json` writes its machine payload to stdout
(`internal/cli/create.go:277`) and everything else to stderr — the report goes to
stderr, so R19's "no new output" for a resolving workspace and the JSON contract
both hold.

**The dispatched worker deliberately gets nothing.** `dispatchLaunch` sets
`cmd.Dir = instanceDir` (`internal/cli/dispatch_launcher.go:89`), so the
worker's own SessionStart hook fails the re-entrancy guard at
`internal/cli/instance_from_hook.go:252-256` and never injects. The only
niwa-authored channel into a dispatched worker is the argv prompt prefix, whose
one existing user is the keep-alive instruction
(`internal/cli/dispatch_keepalive.go:33`, applied at
`internal/cli/dispatch.go:416-427`) and which sits under a documented argv size
budget (`internal/cli/dispatch.go:78-96`). No requirement asks for it; the
operator running `niwa dispatch` is present and sees the terminal rendering.
Recorded as an open risk rather than built.

### The double-print

This design fixes it, because it has to. Rendering moves to the surface, so the
surface is where output ordering is decided. Change
`internal/cli/apply.go:267` from `error: applying to %s: %v` to a locator
without the payload (`apply: instance %s failed`), keeping its actual purpose —
telling the operator which instance in a multi-instance loop failed, while the
loop continues — and letting `Execute` (`internal/cli/root.go:95`) remain what
its own comment claims to be, the single source of truth for error text
(`internal/cli/root.go:38-41`). Without this, a fifteen-line report adjacent to
a doubled multi-line error is unreadable.

### R20 enforcement

`internal/keyreport` carries `prohibitedTerms []string` as a package var, in the
shape of `envPrefixBlocklist` (`internal/workspace/envclassify.go:11-29`). The
test renders every cause × level combination and asserts no term appears. The
provider-kind exemption is enforced by comparison against the registered kinds
from `vault.DefaultRegistry` rather than by hardcoding a string, so adding a
backend does not silently widen the exemption.

Proposed initial list: `vault`, `Vault`, `bundle`, `provider spec`, `registry`,
`universal auth`, `machine identity`, `service token`, `projectId`,
`workspaceId`, `export`, `--token`, `INFISICAL_TOKEN`, `1password`, `op://`,
`hashicorp`, `bitwarden`, `doppler`, `sops`, `keychain`. Not on the list, and
deliberately: `key`, `provider`, `client`, `configured` — these are the plain
words R8 and R9 need in order to say anything at all.

### Ordering (R7)

`Report()` sorts by `(Cause, Scope, Key)`. Cause order is fixed by the enum's
declaration order, matching the PRD's own enumeration: unsatisfiable
declaration, provider unreachable, required-key shortfall. Sorting on read means
the map-iteration nondeterminism at `internal/vault/resolve/resolve.go:389` and
`internal/workspace/required.go:147` cannot reach the output.

### Rendered example — no provider configured (R8)

Three declared keys across two levels, no `[vault.provider]` anywhere in the
merged configuration:

```
warning: 3 declared keys could not be resolved. No provider is configured to
supply them. They are absent from the generated environment files.

  required     ANTHROPIC_API_KEY  API key for Claude Code sessions in this workspace
  required     GITHUB_TOKEN       Token used to clone private repositories
  recommended  TAVILY_API_KEY     API key for web search in research skills
```

The `warning: ` prefix is `Reporter.DeferWarn`'s
(`internal/workspace/reporter.go:158`), applied once to the whole block since
the block is a single deferred entry. No repository is named. No remedy sentence
appears: R8 says "state that and no more", and any remedy niwa could write here
would either name a layer it failed to read or invent one. The PRD's own
Decisions section accepts this consequence — the declared descriptions carry the
whole explanatory burden, which is why R6 requires them in the report.

### Rendered example — configured provider unreachable (R9)

Client binary absent:

```
warning: 2 declared keys could not be resolved. The configured infisical
provider could not be reached: its client is not installed on this host.
Install the infisical client and run niwa apply again to fill these in. They
are absent from the generated environment files.

  required  ANTHROPIC_API_KEY  API key for Claude Code sessions in this workspace
  optional  SENTRY_DSN         Error-reporting endpoint for local runs
```

Any other unreachability:

```
warning: 2 declared keys could not be resolved. The configured infisical
provider could not be reached. Check that its client is signed in and that this
host has network access, then run niwa apply again. They are absent from the
generated environment files.

  required  ANTHROPIC_API_KEY  API key for Claude Code sessions in this workspace
  optional  SENTRY_DSN         Error-reporting endpoint for local runs
```

`infisical` appears only as the provider kind, which R20 exempts and R9
mandates. The two remedies differ in their first clause and their instruction,
which is the acceptance criterion.

### Mixed causes

One command can hit more than one cause. R6 requires one report, so the report
is one block with one paragraph per cause group, groups in enum order, keys
sorted within. The count in the lead sentence is the total.

### Hook rendering (R14)

`RenderContext()` drops the `warning:` framing and addresses the agent:

```
Some capabilities are unavailable in this instance. 2 declared keys could not
be resolved, because the configured infisical provider could not be reached:
its client is not installed on this host. These variables are absent from the
instance's environment:

  ANTHROPIC_API_KEY - API key for Claude Code sessions in this workspace
  SENTRY_DSN - Error-reporting endpoint for local runs
```

Appended to the existing `additionalContext` bytes after the CLAUDE.md section
(`internal/cli/instance_from_hook.go:306-315`), with `runInstanceHookStart`
returning nil so the write at `:192` happens and the process exits 0.

## Rejected alternatives

**Option A (returned report).** Rejected on the fatal path: `Create` returns
`("", err)` after `os.RemoveAll` (`internal/workspace/apply.go:457-460`) and
`runPipeline` returns `(nil, err)`, so R10 and R12 shortfalls would carry no
report at all, or would need a parallel error-carried copy. Signature churn
across six functions and ~20 call sites is a secondary cost, not the reason.

**Option C (inline rendering through the Reporter).** Rejected on R14: the
Reporter has no read-back and its byte stream interleaves spinner frames, which
the codebase already documents as requiring a reduction rule to interpret
(`internal/workspace/permanent_output_test.go:16-41`). Making it readable turns
it into a collector with a terminal device's interface. Also makes R20's test
unable to distinguish report text from unrelated stderr, and pushes R7 into
three separate walk-sorting fixes.

**Option D (derive from the generated files).** Rejected on R8/R9: cause is
run-scoped and host-scoped, and putting it in a file that R4 requires be
rewritten and that a contributor might commit is wrong. Structurally short
under R3a's non-dotenv targets and blind to R11 promotion misses.

**Reusing the `CredentialPool` unreachable buffer**
(`internal/workspace/credentialpool.go:497-521`). Rejected: it observes
credentials for reaching providers, not declared keys, and fires at
`internal/workspace/apply.go:1177-1205`, before resolution runs. Good precedent
for the collect-then-drain shape; wrong data.

**Gating the report on `DisclosedNotices`.** Rejected: R15 makes re-apply the
recovery path, so suppressing on the second run would go silent exactly when the
user is checking whether the gap closed.

**Threading the report into the dispatched worker's prompt.** Deferred, not
rejected outright — see Open risks.

## Open risks

- **The dispatched worker learns nothing.** `niwa dispatch` provisions with
  `cmd.Dir = instanceDir` (`internal/cli/dispatch_launcher.go:89`), so the
  re-entrancy guard (`internal/cli/instance_from_hook.go:252-256`) suppresses
  the SessionStart injection. Only the operator sees the report. The PRD's
  background-agent user story is satisfied by the hook path, which is the one
  R14 names; the dispatch worker is a gap the requirements do not close. If it
  proves painful, the prompt prefix is the channel, under the argv budget at
  `internal/cli/dispatch.go:78-96`.
- **Collector concurrency.** The R11 promotion records originate in a per-repo
  materializer that runs inside the clone worker pool
  (`internal/workspace/apply.go:1349-1360`). `Record` needs a mutex, or those
  records need per-repo accumulation and a post-drain merge. `CredentialPool`
  has the same hazard and documents rather than solves it
  (`internal/workspace/credentialpool.go:221-227`); do not inherit that.
- **Record-and-continue changes resolver blast radius.** Turning `resolveOne`'s
  three error branches into records means a genuinely malformed reference — a
  `vault://` URI that fails `ParseRef` (`internal/vault/resolve/resolve.go:497-500`)
  or names an undeclared provider (`:502-517`) — must be classified explicitly
  rather than falling into "unresolved". A parse error is an author bug, not a
  host shortfall, and should stay fatal. The design must draw that line
  precisely or R1 will start swallowing config typos.
- **R20's list will rot.** A new backend adds vendor vocabulary to error strings
  that could reach the report through a wrapped `%v`. The renderer must build
  its text from structured `Entry` fields only, never by interpolating a
  provider error — otherwise the term list is enforced against a template while
  the vendor's own message flows around it.
- **Description quality is unenforced.** The PRD names this in Known
  Limitations. The report's usefulness is bounded by strings written by
  configuration authors, and nothing in this design validates them. A workspace
  whose descriptions read `"the token"` produces a correct, useless report.
- **`--no-progress` and non-TTY rendering.** `Reporter` on a non-TTY writes
  lines directly (`internal/workspace/reporter.go:22-24`), so the block renders
  identically; the byte-identical-across-runs acceptance criterion should be
  asserted with a non-TTY Reporter to avoid the spinner-interleave problem the
  `permanentOutput` helper exists to work around.
