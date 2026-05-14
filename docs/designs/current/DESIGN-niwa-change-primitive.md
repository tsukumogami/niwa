---
status: Current
problem: |
  The F5 PRD locks every shape downstream features encode against — the
  ChangeState v=1 schema, the audit log v=2 bump, the four-event
  taxonomy, the MCP tool I/O, the URL contract, the auth contract, the
  GC behavior. What it does NOT pick is how to lay those decisions onto
  the existing niwa source tree: which subpackage owns the listener,
  which process owns the listener's lifecycle, where the dual-target
  event emitter lives, how the audit log v=2 reader stays backward-
  compatible with the v=1 records already on disk on the reference
  fleet, and how the @critical Gherkin scenarios pull a worktree + base
  ref + head ref + a real diff into the test sandbox without network
  access. Getting the wiring wrong leaks lifecycle responsibilities
  across packages (signal handlers in `internal/web/`, GC tickers in
  the MCP server) and forecloses the clean separation F10's verdict
  endpoint will need.
decision: |
  Two processes own the F5 substrate, communicating only through the
  filesystem. (1) The per-session `niwa mcp-serve` process registers
  the three new MCP tools in `internal/mcp/handlers_change.go` and
  writes to `.niwa/changes/<id>/` directly — it does NOT host the HTTP
  listener. (2) A new instance-singleton process `niwa surface serve`
  (wired in `internal/cli/surface.go`) owns the HTTP listener, the GC
  ticker, the surface lock, and the SIGTERM/SIGINT handlers. Both
  processes emit events through a single `appendEventLog` helper
  exported from `internal/mcp/changelog.go` that fans out one entry to
  the per-change `transitions.log` (flock + fsync) and one to
  `.niwa/mcp-audit.log` v=2 (mutex + atomic O_APPEND). The web package
  is split into `internal/web/` (listener, lifecycle, routing),
  `internal/web/render/` (stdlib `html/template` + embedded CSS via
  `//go:embed`), and `internal/web/gc/` (the sweep). The atomic
  `.niwa/changes/<id>/` reservation reuses the existing
  `O_CREATE|O_EXCL` placeholder + 5-retry birthday loop pattern,
  hoisted into a shared `internal/mcp/atomicid.go` helper so
  `session_lifecycle.go` and the new change creator share one
  implementation. The audit log v=2 reader treats absence of `kind` as
  `kind="tool_call"` (and the missing payload as `nil`); the v=1
  `AuditEntry` struct grows three optional fields with `omitempty` and
  one CHANGE log file. The functional test fixture reuses
  `localGitServer` from `test/functional/localrepo_test.go`, extended
  with a `SourceRepoWithDiff(name, baseContent, headContent)` helper
  that produces a bare repo whose default branch is `main` and whose
  worktree commit range produces a non-empty unified diff for the
  Gherkin walking-skeleton scenario.
rationale: |
  Splitting the listener out of `niwa mcp-serve` matches the rest of
  niwa's substrate: per-session processes own per-session state;
  per-instance state belongs in per-instance processes
  (`niwa mesh watch`, `niwa surface serve`). Co-locating the listener
  inside `mcp-serve` would either spawn N listeners (one per session,
  each trying to bind `127.0.0.1:0` and racing for
  `.niwa/surface.lock`) or elect a leader at startup — both rejected
  because the operator's mental model is one URL per niwa instance,
  and the per-instance singleton is the existing convention. The PRD's
  R6 phrasing "MCP server spawns the listener in a goroutine at
  startup" is honored at the package layer (the `internal/mcp`
  package's audit substrate is what the listener consumes, not the
  `niwa mcp-serve` process specifically). The `appendEventLog` helper
  lives in `internal/mcp/changelog.go` rather than `internal/web/`
  because both processes (mcp-serve and surface serve) emit events,
  and putting the emitter under `internal/mcp` keeps audit-log access
  in the package that already owns the audit-log mutex. The atomic-ID
  hoist into `internal/mcp/atomicid.go` is the smallest abstraction
  that lets the change creator and the session creator share one
  reservation loop; the alternative (copying the 19-line function)
  was rejected because the PRD locks both ID shapes verbatim and the
  reservation protocol is a hard correctness property neither caller
  should re-derive. The `internal/web/render/` split is the only web
  subpackage that earns its keep — the listener and the GC sweep
  live in `internal/web/` directly because each is a single file's
  worth of code at F5, and over-decomposing greenfield packages
  obscures the call graph for first-time readers. `html/template` is
  chosen over hand-rolled string formatting because diff bodies
  contain `<script>` tags from real changes and the template engine's
  automatic contextual escaping eliminates an entire class of XSS
  bugs the hand-rolled path would have to reinvent at every render
  site.
---

# DESIGN: niwa Change-as-Reviewable Primitive and Basic Web Render (F5)

## Status

Current

## Amendments

### A1: Reshape from per-instance to machine-scope `niwa surface serve`

The original design split F5 into a per-session `niwa mcp-serve` and a
per-instance `niwa surface serve`. Operator feedback during
implementation flagged that with 3–10 active niwa instances per
machine the per-instance shape produces 3–10 separate HTTP listeners
on 3–10 separate ephemeral ports — a fleet-scale ergonomic gap that
forecloses agent-to-agent visibility across instances.

The amendment: `niwa surface serve` is now **one process per machine**.
At boot it reads the user's global config (`~/.config/niwa/config.toml`
honoring `XDG_CONFIG_HOME`), enumerates every workspace in the
`[registry]` section, and discovers every niwa instance (the workspace
root itself + any first-level sub-directory containing a `.niwa/`)
under each workspace. The listener federates across all of them; the
GC sweep iterates each in turn.

The shifts:

| Concern | Original | Amended |
|---------|----------|---------|
| Process scope | Per niwa instance | One per machine |
| Workspace discovery | None — each instance owned its listener | Reads `~/.config/niwa/config.toml` `[registry]` |
| URL contract | `http://127.0.0.1:<port>/changes/<id>#comment-<id>` | `http://127.0.0.1:<port>/workspaces/<workspace>/<instance>/changes/<id>#comment-<id>` |
| Lock file | `<instance>/.niwa/surface.lock` | `~/.config/niwa/surface.lock` |
| Token file | `<instance>/.niwa/surface.token` | `~/.config/niwa/surface.token` |
| Port file | `<instance>/.niwa/surface.port` | `~/.config/niwa/surface.port` |
| GC scope | One instance | Federated across every discovered instance, each with its own audit sink so cleanup events still co-locate with the change data |
| Audit log placement | Per-instance | Unchanged — each instance still owns its own `<instance>/.niwa/mcp-audit.log`. Only the listener and lifecycle moved up; the data plane stays where it was. |

The `originating_sessions: [<sid>]` plural field is also amended to
the singular `originating_session: <sid>`. Each session-branch is
owned by a single agent; the plural shape was YAGNI under that
ownership model.

The Telegram bridge — Decision D1 in the PRD — remains fully
deferred. F5 no longer claims any Telegram delivery; the
`change_ready` event lands in the per-instance `mcp-audit.log` and a
separate spec (yet to be filed) decides how it reaches a notification
channel.

All other shapes locked by the PRD (the schemas, the four-event
taxonomy, the MCP tool I/O shapes, the auth contract, the base-ref
precedence, the GC abandonment thresholds, the diff-snapshot
strategy) are unchanged.

## Context and Problem Statement

niwa's collab surface roadmap (F5–F15 in the niwa collab-surface
milestone ladder; F5 is this change-as-reviewable primitive, F6 is
comments, F10 is the verdict gate, F18 is the notification bridge,
and so on) encodes every downstream feature against a `change` object
that does not exist yet, and a web surface category niwa has never
shipped. The F5 milestone locks verbatim contracts that downstream
features compose against: the ChangeState v=1 schema, the audit log
v=2 bump, four event payloads, three MCP tool I/O shapes, the
`/changes/<id>#comment-<id>` URL contract, the per-instance Bearer
auth contract, the base-ref discovery precedence, and the
abandonment GC behavior. With the WHAT locked, this design picks the
HOW: which Go packages own which responsibility, how the two
long-running niwa processes (per-session `niwa mcp-serve` and
per-instance `niwa surface serve`) coordinate through the filesystem
substrate without leaking lifecycle into each other, how the
`appendEventLog` helper fans out without re-implementing two atomicity
models, and how the @critical Gherkin scenario gets a real worktree
with a non-empty diff into the test sandbox without network access.

The substrate is greenfield for HTTP: no listener exists today, no
Bearer auth exists today, no CORS handling exists anywhere, and
`niwa surface` is not a CLI noun yet. The substrate is *not* greenfield
for atomic ID reservation (`newSessionLifecycleID` already implements
the protocol the PRD mandates for `.niwa/changes/<id>/`) or for audit
emission (`fileAuditSink` already owns the mutex + atomic-append v=1
path the v=2 emitter must compose with). Choosing where the new code
hooks the existing patterns is the dominant design question, not
inventing the patterns.

## Decision Drivers

- **PRD shapes are non-negotiable.** ChangeState v=1, audit log v=2,
  event payloads, MCP I/O, URL contract, auth contract, GC behavior:
  all locked. This design decides only HOW to land them.
- **Per-instance vs. per-session ownership.** `mcp-serve` runs per
  session worker (one process per Claude Code session). The HTTP
  listener must be exactly one per niwa instance. The two cannot share
  the same process boundary.
- **Atomic substrate already exists.** The session lifecycle
  reservation protocol (`O_CREATE|O_EXCL` + 5-retry birthday loop) and
  the audit-log v=1 atomicity model (mutex + `O_APPEND ≤ PIPE_BUF`)
  are battle-tested. Re-implementing either is a regression risk; the
  design hoists / extends them instead.
- **Sub-package boundaries earn their keep.** niwa has 12
  `internal/` packages today. Adding a half-dozen subpackages under
  `internal/web/` for F5's 600-ish lines of new code would obscure the
  call graph more than it clarifies separation.
- **`html/template` automatic contextual escaping is load-bearing.**
  Diff bodies contain `git diff` output that may include `<script>`
  tags from a change being reviewed. NFR4 mandates escaping; the
  template engine moves that from a per-call discipline to a
  package-level invariant.
- **Public repo.** Documents must not reference private vision-repo
  issue URLs in committed prose. Internal workflow command names are
  used to author the design but do not appear in the design itself.
- **Implementable by /plan without further design questions.** Any
  remaining ambiguity is named explicitly as a deferral with a target
  PRD or design.

## Considered Options

The PRD locks the user-visible contracts (schemas, URLs, events,
auth). The implementation-level questions below are the design
decisions this doc owns. Each lists the chosen option and at least one
genuinely-considered alternative so future readers see the trade-off
was not automatic.

### D1: Listener lifecycle owner

**Question:** Which process owns the HTTP listener's lifetime —
`niwa mcp-serve`, a new `niwa surface serve`, or the existing
`niwa mesh watch` daemon?

**Chosen:** A new instance-singleton CLI command `niwa surface serve`.
The operator runs it in a terminal or under a service manager; it
owns the listener goroutine, the GC ticker goroutine, the
`.niwa/surface.lock` PID file, the SIGTERM/SIGINT handler, and the
`http.Server.Shutdown(ctx)` cancellation.

**Why:** `niwa mcp-serve` is per-session; spawning a listener inside
each session worker would either bind N listeners or require leader
election at startup. The operator's mental model is one URL per niwa
instance; the per-instance singleton is the existing convention
(`niwa mesh watch` is per-instance, daemons are per-instance,
`.niwa/` is per-instance). `niwa mesh watch` was considered as the
host but rejected: the watch daemon's job is task claiming and worker
spawning, and bundling a public HTTP listener with internal task
queue mechanics would couple two unrelated failure modes (a crashed
listener restarts the daemon; a wedged task spawn would freeze the
surface). Separate processes, separate lifecycles, separate restart
semantics.

### D2: `internal/web/` subpackage layout

**Question:** Subdivide `internal/web/` into `server`, `handlers`,
`render`, `gc`, all in one file, or some middle ground?

**Chosen:**
```
internal/web/
├── server.go         # listener boot, routing, lifecycle, auth middleware
├── handlers.go       # the three GET endpoints + their helpers
├── render/
│   ├── render.go     # html/template loader + RenderChange / RenderIndex
│   ├── styles.css    # embedded via //go:embed
│   └── templates.go  # //go:embed templates/*.tmpl, parse once at init
├── gc/
│   └── sweep.go      # GC loop, on-boot sweep, ticker, abandoned-detection
└── auth.go           # Bearer-token middleware (no-op for F5 reads; locked for F10)
```

**Why:** `internal/web/render/` earns separation because templates
and the embedded CSS are a self-contained surface F11/F12/F13 will
swap or extend independently of the listener. `internal/web/gc/`
earns separation because the GC sweep has its own goroutine
lifecycle, its own ticker, and its own boot-time scan distinct from
serving HTTP. The listener boot and the three handlers live in
`internal/web/server.go` and `internal/web/handlers.go` directly
because each is single-file-sized at F5 and over-decomposing would
obscure the call graph for first-time readers. Auth middleware lives
in `internal/web/auth.go` even though F5 has no mutation endpoints,
because F10 will compose verdict-cast mutations on the same
middleware and the contract is locked here.

**Alternative considered:** Flat `internal/web/web.go` single-file
package. Rejected because the GC ticker and the render templates
each have multiple files (or embedded assets) that pollute a flat
package's symbol space.

### D3: `niwa surface serve` CLI wiring

**Question:** Where in `internal/cli/` does the new command live, and
how does the command tree compose?

**Chosen:** New file `internal/cli/surface.go` registering a parent
`surfaceCmd` (Use: `surface`) with one subcommand `serveCmd`
(Use: `serve`). Pattern mirrors `internal/cli/session.go` /
`internal/cli/mesh.go`. Flags:
- `--port N` (int, default 0 → ephemeral). Bound via
  `cobra.Command.Flags().IntVar`.
- `--rotate-token` (bool, default false).

The parent `surface` noun is reserved for future subcommands
(`niwa surface status`, `niwa surface stop` if F11/F13 needs them);
F5 ships only `serve`.

**Why:** Mirrors the existing session/mesh CLI pattern so operators
learning niwa today see one consistent shape. The two-level command
tree leaves room for F10+ subcommands without renaming. Boot proceeds
through the steps the PRD R10 enumerates (acquire lock → ensure
token → bind port → write `surface.port` → log → run GC once →
start serving + GC ticker → wait for SIGTERM).

### D4: HTTP server lifecycle and shutdown propagation

**Question:** How does cancellation flow from the CLI process's root
context through `http.Server.Shutdown(ctx)`, and where is the
goroutine spawned?

**Chosen:** In `runSurfaceServe` (`internal/cli/surface.go`):

```go
ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
defer cancel()

srv, err := web.New(ctx, web.Config{...})           // returns *http.Server + GC stop func
if err != nil { return err }

errCh := make(chan error, 1)
go func() { errCh <- srv.ListenAndServe() }()

select {
case <-ctx.Done():
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    return srv.Shutdown(shutdownCtx)
case err := <-errCh:
    return err
}
```

The `web.New` constructor takes the parent context, spawns the GC
goroutine internally (it cancels on `ctx.Done()`), and returns the
`*http.Server`. **No signal handlers live in `internal/web/`** — the
CLI process owns signal handling; `internal/web/` only owns the
`ctx`-driven cancellation chain.

**Why:** `signal.NotifyContext` is the stdlib idiom for translating
OS signals into context cancellation; co-locating it with the CLI
command (where the process lifecycle is) keeps `internal/web/` a
pure library. The 5-second `Shutdown` timeout matches `mcp-serve`'s
existing patterns and is long enough for in-flight `GET /changes/<id>`
renders to complete (NFR2 caps each at <200ms) without orphaning
hung browser connections indefinitely.

### D5: `appendEventLog` dual-target emitter

**Question:** Where does the helper that emits one event to both
`.niwa/changes/<id>/transitions.log` AND `.niwa/mcp-audit.log` v=2
live, what does it take as arguments, and what does it lock?

**Chosen:** New file `internal/mcp/changelog.go` exports:

```go
// ChangeEvent is the F5 event taxonomy (R5).
type ChangeEvent struct {
    Kind     string         // "change_ready" | "review_surface_opened" | "change_engaged" | "change_cleaned"
    ChangeID string         // empty for review_surface_opened
    Payload  map[string]any // event-specific (R5)
}

// AppendChangeEvent fans one event to the per-change transitions log
// (when ChangeID is set) AND the instance-wide mcp-audit.log v=2.
// The per-change append takes the per-change flock; the audit append
// reuses the existing fileAuditSink mutex. Each target is best-effort
// — a failure in one does not skip the other.
func AppendChangeEvent(instanceRoot string, audit *fileAuditSink, e ChangeEvent) error
```

The helper acquires the per-change `.lock` for `transitions.log`
appends only when `e.ChangeID != ""` (the `review_surface_opened` event
has no change context). The audit append funnels through
`fileAuditSink.Emit` extended for v=2 (D7 below).

**Why:** The package is `internal/mcp` because both processes
(`mcp-serve` and `surface serve`) import it, and the audit sink's
mutex lives there. Locating the emitter under `internal/web/` would
force a `web → mcp` dependency cycle when the MCP handlers in
`handlers_change.go` need to emit `change_ready`. The function
signature explicitly takes `audit *fileAuditSink` so callers control
the sink (production = file-backed; unit tests = nil-path no-op
sink). Errors are surfaced but the caller (handler / GC sweep)
ignores them — emit failure must never break a real MCP call or a
real HTTP render, consistent with the existing dispatch-loop
discipline.

**Alternative considered:** Put the emitter on the `Server` struct
in `internal/mcp/server.go` as a method. Rejected because the GC
sweep in `internal/web/gc/` is not a `Server`-bound caller, and a
package-level function avoids passing the whole server around.

### D6: Audit log v=2 reader backward compatibility

**Question:** Records emitted before F5 ships use v=1 (no `kind`, no
`payload`). The v=2 reader on the reference fleet must keep parsing
them.

**Chosen:** The `AuditEntry` struct grows three optional fields with
`omitempty`:

```go
type AuditEntry struct {
    V         int            `json:"v"`
    At        string         `json:"at"`
    Kind      string         `json:"kind,omitempty"`      // NEW: "tool_call" | "event"
    Role      string         `json:"role,omitempty"`
    TaskID    string         `json:"task_id,omitempty"`
    Tool      string         `json:"tool,omitempty"`      // existing; was required in v=1
    ArgKeys   []string       `json:"arg_keys,omitempty"`  // existing; was required in v=1
    Event     string         `json:"event,omitempty"`     // NEW
    Payload   map[string]any `json:"payload,omitempty"`   // NEW
    OK        bool           `json:"ok"`
    ErrorCode string         `json:"error_code,omitempty"`
}
```

`Tool` and `ArgKeys` move from required to optional in JSON terms;
they remain populated for `kind="tool_call"` entries. The reader path
in `audit_reader.go` infers the kind:

```go
func (e *AuditEntry) effectiveKind() string {
    if e.Kind != "" {
        return e.Kind
    }
    if e.Tool != "" {
        return "tool_call"   // v=1 record
    }
    return ""                // malformed / corrupt; skip per existing policy
}
```

The v field on disk increments to 2 for entries the new emitter
writes; v=1 records on disk continue to parse cleanly because every
new field is `omitempty` and absent-on-read maps to zero values the
reader tolerates.

**Why:** Adding optional fields and inferring `kind` from `Tool`
presence is the smallest reader-side change that lets v=1 and v=2
records coexist in one log file. Bumping `v` to 2 on new writes makes
the schema version observable. Splitting into two `AuditEntry` types
(`AuditToolCall`, `AuditEvent`) was rejected because every reader
(filter functions, tests) currently consumes one slice; the
discriminator lives more cleanly in `Kind`.

### D7: Audit log v=2 emit path and payload size enforcement

**Question:** PRD R4 requires that over-2KB entries be downgraded
(payload replaced with `{}`, `error_code="payload_too_large"`,
mutation still succeeds). Where does the size check happen?

**Chosen:** Inside `fileAuditSink.Emit`, immediately after
`json.Marshal` and before the file open. The marshalled byte slice is
inspected; if `len(data) > 2048`, the entry's `Payload` is replaced
with an empty map, `ErrorCode` is set to `"payload_too_large"`, the
entry is re-marshalled, and that smaller buffer is written. The
caller's `AuditEntry` is not mutated (the in-memory record retains
the full payload for the MCP response).

**Why:** Centralizing the budget check in the sink means every
emitter — MCP handlers, the change emitter, future bridge consumers —
gets the same enforcement without per-call discipline. The 2KB budget
is well below the PIPE_BUF (~4096 bytes) Linux atomic-append limit,
leaving headroom for envelope fields and future schema growth. The
in-memory record stays intact so the MCP response is not poisoned by
the audit-log truncation — only readers of `mcp-audit.log` observe the
downgrade.

### D8: Atomic `.niwa/changes/<id>/` directory reservation

**Question:** PRD R1 says the change directory creation uses the same
`O_CREATE|O_EXCL` placeholder + 5-retry birthday loop as
`newSessionLifecycleID`. Reuse, generalize, or copy?

**Chosen:** Hoist the protocol into a new file
`internal/mcp/atomicid.go` exporting:

```go
// ReserveID atomically reserves a unique identifier in dir by
// attempting O_CREATE|O_EXCL on a placeholder file. The ID is
// produced by gen(); on EEXIST the loop retries up to 5 times.
// placeholderName(id) returns the file path within dir to lock.
func ReserveID(
    dir string,
    gen func() (string, error),
    placeholderName func(id string) string,
) (string, error)
```

`newSessionLifecycleID` becomes a one-line caller passing a 4-byte
hex generator and the `<id>.json` placeholder; the new
`reserveChangeID` is a parallel one-line caller passing a UUIDv4
generator and the `<id>/.lock` placeholder (the lock file inside a
freshly-created directory). The directory itself is created with
`MkdirAll(dir, 0o700)` *before* the placeholder open; on EEXIST the
caller removes the empty directory and retries.

**Why:** The protocol is a correctness property (TOCTOU-safe ID
reservation under concurrent writers) that two callers should share
one implementation of. Generalizing rather than copying avoids
divergence if one site gains a bug fix or a hardening check. The
generator + placeholder-name parameters are the minimum surface
needed to cover both the session case (8-hex hex, `<id>.json`) and
the change case (UUIDv4, `<id>/.lock`). Copying the function was
rejected because PRD R1 calls out the precedent explicitly — drift
between the two would force the PRD's reviewer-side ergonomics
("matches session_lifecycle.go pattern") to be re-validated each
release.

### D9: GC goroutine lifecycle composition

**Question:** The GC sweep runs on `niwa surface serve` boot and
then every `gc_interval_hours` (default 6) thereafter. How does it
compose with the HTTP server's lifecycle?

**Chosen:** The GC loop is spawned by `web.New(ctx, cfg)` inside
`internal/web/gc/sweep.go`:

```go
// Run starts the GC loop. It runs an on-boot sweep synchronously
// before returning, then spawns the ticker goroutine. Cancellation
// flows from ctx; the goroutine exits cleanly when ctx is done.
func Run(ctx context.Context, instanceRoot string, audit *mcp.fileAuditSink, cfg Config) error
```

The on-boot sweep runs synchronously inside `Run` before the goroutine
spawns, so a misconfigured `gc_interval_hours` (PRD R9 says the
process must exit 1 at boot with `"invalid gc_interval_hours: must be
between 1 and 168"`) surfaces before the listener accepts requests.
The ticker goroutine uses `time.NewTicker(cfg.IntervalHours)` and
shuts down on `ctx.Done()` via the standard select-on-tick pattern.

**Why:** The sweep is in-process with the listener (one process per
instance), so cancellation through the same context produces clean
shutdown. The on-boot sweep being synchronous catches config errors
before the operator sees the URL in stderr — fail-fast at boot is the
existing CLI pattern (`niwa apply`, `niwa create`). A separate
daemon for the GC sweep was rejected because the sweep needs the same
audit sink the listener uses, and a third process would re-derive
`.niwa/surface.lock` ownership.

### D10: HTML rendering: stdlib `html/template` with embedded assets

**Question:** Hand-rolled string formatting, `text/template`, or
`html/template`?

**Chosen:** `html/template` with templates embedded via `//go:embed`
in `internal/web/render/templates.go`. Two templates: `change.tmpl`
(per-change view) and `index.tmpl` (list view). Templates are parsed
once at package init via `template.Must(template.ParseFS(...))` and
reused across requests. CSS is a sibling `styles.css` embedded as a
`string` and injected into both templates via a `{{.CSS}}` field on
the data struct.

**Why:** `html/template` performs contextual escaping automatically
(HTML attribute, URL, JavaScript contexts each get appropriate
escaping rules). NFR4 mandates escaping diff content before
`<pre>`-wrapping; the template engine moves that from a per-call
discipline to a package-level invariant. Hand-rolled string
concatenation was rejected because every render site would need to
remember to call `html.EscapeString`, and one missed call ships an
XSS. `text/template` was rejected because it has no contextual
escaping — using it for HTML output would replicate the same
discipline-required failure mode. Parse-once-at-init is the standard
Go template pattern; per-request parsing would cost ~1ms per render
that NFR2's <200ms budget can absorb but does not need to spend.

### D11: Inline CSS via `//go:embed`

**Question:** Inline CSS as a Go `const`, embed via `//go:embed`, or
hand-format inside the template?

**Chosen:** `//go:embed styles.css` in
`internal/web/render/templates.go`. The CSS file lives at
`internal/web/render/styles.css` (committed to the repo, editable as
a `.css` file with editor syntax support); the embed directive pulls
it into the binary at compile time. The template injects the content
via a `<style>{{.CSS}}</style>` block at the top of the page.

**Why:** Compile-time embedding ships exactly one binary with the CSS
inside; no asset endpoint is needed (PRD R6 prohibits one). Editing
a `.css` file in an editor is materially better than maintaining a
multi-line Go raw string. Inlining as `const cssText = \`...\`` was
rejected because IDE CSS tooling (Prettier, language servers) does
not see Go raw strings as CSS; `//go:embed` keeps the file a real
`.css` file. Hand-formatting CSS inside the template was rejected
because the same CSS is used for both `change.tmpl` and `index.tmpl`,
and duplicating it across two templates is a maintenance trap.

### D12: `.niwa/surface.lock` PID liveness check

**Question:** PRD R10 says stale-lock detection checks if the PID in
`surface.lock` is alive. Which syscall path?

**Chosen:** `os.FindProcess(pid)` followed by
`proc.Signal(syscall.Signal(0))`. On Unix, `os.FindProcess` always
succeeds (it just wraps the PID in a `*os.Process`); the real check
is `Signal(0)`, which returns nil if the process exists and the
caller has permission to signal it, `os.ErrProcessDone` (Go 1.21+) or
`syscall.ESRCH` if the process is gone, and `syscall.EPERM` if the
process exists but is owned by another UID.

```go
proc, _ := os.FindProcess(pid)
if err := proc.Signal(syscall.Signal(0)); err != nil {
    if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
        return false  // dead — stale lock, safe to reap
    }
    if errors.Is(err, syscall.EPERM) {
        return true   // alive but different UID — treat as alive, do not reap
    }
    return true       // unknown error — fail closed (assume alive)
}
return true
```

The `EPERM` case is rare for niwa (single-user reference fleet) but
the fail-closed treatment prevents a UID mismatch from racing the
reap path.

**Why:** `Signal(0)` is the canonical Unix liveness probe and works
without spawning subprocesses or reading `/proc`. The reference
fleet (Linux) supports `os.ErrProcessDone` directly via the runtime;
the `syscall.ESRCH` fallback covers Go's pre-1.21 callers (none exist
on the reference fleet but the code is portable). Reading
`/proc/<pid>/status` was rejected because it ties the listener to
procfs availability and adds a code path the existing
`mcp.IsPIDAlive` helper does not need.

**Note on existing helper:** `internal/mcp/liveness.go` already
exports `IsPIDAlive(pid, startTime int64)` which combines a PID +
start-time check used by the daemon. F5's `surface.lock` only stores
the PID (no start time); rather than expand the lock format, the
surface uses a lighter `IsProcessAlive(pid int)` helper added to
`internal/mcp/liveness.go` that omits the start-time gate. The daemon
keeps its existing helper unchanged.

### D13: Diff content escaping pipeline location

**Question:** PRD NFR4 mandates `html.EscapeString` on diff content
before `<pre>`-wrapping. Where in the render pipeline?

**Chosen:** Implicitly, by `html/template`. The template reads the
diff file content into a `string` field on the data struct
(`Diff string`) and references it as `{{.Diff}}` inside
`<pre>{{.Diff}}</pre>`. `html/template`'s automatic contextual
escaping HTML-escapes string fields in HTML body context; no explicit
`html.EscapeString` call is needed. The render code path is:

```
ReadFile(diff.patch) → string → template.Execute(w, data)
                                  ↓
                                  automatic escaping inside {{.Diff}}
```

**Why:** Per D10, `html/template`'s automatic escaping is the entire
reason the template engine was chosen. Calling `html.EscapeString`
manually and then passing the result through the template would either
double-escape (if the field is a `string`) or require marking the
field as `template.HTML` (which bypasses escaping — the dangerous
path). Trusting the template engine for one consistent escape pass is
both safer and simpler. The trailer line "diff truncated at 4 MiB"
(R7) is added to the file contents and therefore escaped along with
the diff body; no special path is needed.

### D14: Test fixtures for Gherkin scenarios

**Question:** The `@critical` `review-surface.feature` scenarios need
a worktree, a base ref, a head ref, and a non-empty diff. What
fixtures support this offline?

**Chosen:** Extend `test/functional/localrepo_test.go`'s
`localGitServer` with a new helper:

```go
// SourceRepoWithDiff creates a bare repo named <name>.git with two
// commits on the default branch (main): the first commits
// baseContent as content.txt, the second amends headContent into the
// same file. The returned file:// URL clones to a worktree whose
// `git diff origin/main..HEAD` produces a non-empty unified diff. The
// helper is used by F5's review-surface.feature scenarios.
func (s *localGitServer) SourceRepoWithDiff(name, baseContent, headContent string) (string, error)
```

Unit tests for `internal/web/` use `httptest.NewServer` (R6) and
hand-construct the `.niwa/changes/<id>/state.json` + `diff.patch`
files; no `localGitServer` involvement. Unit tests for
`internal/mcp/handlers_change.go` use `t.TempDir()` plus
`localGitServer.SourceRepoWithDiff` to invoke the MCP tool against a
real worktree.

**Why:** The `localGitServer` helper is already the offline-test
substrate niwa uses for every functional test that needs a git repo;
extending it with one focused method covers the F5 walking-skeleton
without adding a parallel fixture path. Hand-constructing
`state.json` + `diff.patch` for unit tests avoids the cost of
spawning git for tests that only exercise HTTP rendering — the
diff content is template input, not a code path under test in
`internal/web/`. The MCP handler tests need the real `git diff`
invocation because PRD R7 makes the snapshot the contract; mocking
git there would let the diff format drift.

## Decision Outcome

The F5 implementation lands as:

1. **Two processes**: per-session `niwa mcp-serve` writes to
   `.niwa/changes/<id>/` and emits `change_ready` into
   `mcp-audit.log` v=2; per-instance `niwa surface serve` reads the
   same directory, serves HTTP on the kernel-assigned ephemeral port,
   runs the GC sweep, and emits `review_surface_opened`,
   `change_engaged`, and `change_cleaned`.
2. **One emitter** in `internal/mcp/changelog.go` fans every event to
   both the per-change `transitions.log` and the instance-wide audit
   log; the audit sink in `audit.go` extends from v=1 to v=2 with a
   backward-compatible reader.
3. **One reservation protocol** in `internal/mcp/atomicid.go` is
   shared by `newSessionLifecycleID` and the new change creator.
4. **`html/template`** with embedded CSS via `//go:embed` provides
   the render path; automatic contextual escaping satisfies NFR4
   without per-call discipline.
5. **`signal.NotifyContext`** in `internal/cli/surface.go` translates
   SIGTERM/SIGINT into context cancellation that flows through the
   HTTP listener, the GC ticker, and the surface-lock cleanup.
6. **`localGitServer.SourceRepoWithDiff`** in
   `test/functional/localrepo_test.go` is the one new test fixture;
   everything else reuses existing offline-test substrate.

The PRD's 32-row decision table is implemented verbatim; no
user-visible contract is renegotiated by this design.

## Solution Architecture

### Process topology

```
                   ┌─────────────────────────────┐
                   │   niwa surface serve        │   per instance
                   │   (singleton CLI process)   │   (operator-run)
                   │                             │
                   │  internal/cli/surface.go    │
                   │    └─ signal.NotifyContext  │
                   │  internal/web/server.go     │
                   │    └─ http.Server on        │
                   │       127.0.0.1:0           │
                   │  internal/web/gc/sweep.go   │
                   │    └─ GC ticker goroutine   │
                   │                             │
                   │  ─── reads ───────────►     │
                   │  .niwa/changes/<id>/        │
                   │  .niwa/surface.token (gen)  │
                   │  .niwa/surface.port (write) │
                   │  .niwa/surface.lock (hold)  │
                   │  ─── emits ──────────►      │
                   │  .niwa/mcp-audit.log v=2    │
                   │  .niwa/changes/<id>/        │
                   │    transitions.log          │
                   └─────────────────────────────┘
                              ▲
                              │ filesystem
                              │ (no IPC)
                              ▼
   ┌─────────────────────────────────────┐
   │   niwa mcp-serve (one per session)  │   per Claude Code session
   │                                     │
   │   internal/mcp/server.go            │
   │     ├─ existing 15 tools            │
   │     └─ 3 NEW tools                  │
   │   internal/mcp/handlers_change.go   │
   │     ├─ handleCreateChange  (R3)     │
   │     ├─ handleListChanges   (R3)     │
   │     └─ handleQueryChange   (R3)     │
   │   internal/mcp/changelog.go         │
   │     └─ AppendChangeEvent            │
   │   internal/mcp/atomicid.go          │
   │     └─ ReserveID (shared with       │
   │        session_lifecycle.go)        │
   │                                     │
   │   ─── writes ────────────►          │
   │   .niwa/changes/<id>/state.json     │
   │   .niwa/changes/<id>/diff.patch     │
   │   .niwa/changes/<id>/transitions.log│
   │   .niwa/mcp-audit.log v=2           │
   │   ─── reads ─────────────►          │
   │   .niwa/surface.port (URL compose)  │
   └─────────────────────────────────────┘
```

The two processes share state only through files under `.niwa/`. No
process-to-process IPC, no shared memory, no service discovery
beyond the per-instance directory convention.

### Package and file layout

New files (greenfield):

```
internal/
├── cli/
│   └── surface.go              # niwa surface serve command + flag parsing
├── mcp/
│   ├── atomicid.go             # shared O_CREATE|O_EXCL reservation loop
│   ├── changelog.go            # AppendChangeEvent (dual-target emitter)
│   ├── handlers_change.go      # 3 MCP tool handlers
│   └── changestore.go          # ChangeState v=1 read/write under per-change flock
└── web/
    ├── server.go               # listener boot, routing, lifecycle
    ├── handlers.go             # GET /, GET /changes/, GET /changes/<id>
    ├── auth.go                 # Bearer middleware (F5: no-op for reads)
    ├── render/
    │   ├── render.go           # RenderChange / RenderIndex
    │   ├── templates.go        # //go:embed templates/*.tmpl
    │   ├── templates/
    │   │   ├── change.tmpl
    │   │   └── index.tmpl
    │   └── styles.css          # //go:embed asset
    └── gc/
        └── sweep.go            # GC sweep + ticker

test/functional/
├── features/
│   └── review-surface.feature  # @critical Gherkin scenarios
└── localrepo_test.go           # extended with SourceRepoWithDiff
```

Modified files:

```
internal/cli/mcp_serve.go       # no change — handlers register via server.go
internal/mcp/audit.go           # AuditEntry gains 3 omitempty fields (D6)
internal/mcp/audit_reader.go    # effectiveKind() inference helper
internal/mcp/server.go          # tools/list adds 3 toolDefs; switch in callTool gains 3 cases
internal/mcp/session_lifecycle.go  # newSessionLifecycleID becomes a one-line caller of ReserveID
```

### Data shapes

The on-disk shapes are locked by the PRD verbatim. The Go types
match field names 1:1 with JSON tags:

```go
// internal/mcp/changestore.go
type ChangeState struct {
    V                  int      `json:"v"`
    ID                 string   `json:"id"`
    State              string   `json:"state"`
    OriginatingSession string   `json:"originating_session"`
    OriginatingTasks   []string `json:"originating_tasks"`
    CreatedAt           string         `json:"created_at"`
    UpdatedAt           string         `json:"updated_at"`
    BaseRef             string         `json:"base_ref"`
    HeadRef             string         `json:"head_ref"`
    Branch              string         `json:"branch"`
    WorktreePath        string         `json:"worktree_path"`
    DiffPath            string         `json:"diff_path"`
    Verdict             *Verdict       `json:"verdict"` // always nil at F5
    Metadata            map[string]any `json:"metadata"`
}

type Verdict struct{} // reserved; F10 populates fields

const (
    ChangeStatePending     = "pending"
    ChangeStateInReview    = "in-review"
    ChangeStateVerdictCast = "verdict-cast"
    ChangeStateCleaned     = "cleaned"
)
```

`Verdict` is a typed nil at F5 (always serialized as `null`); F10
adds fields. Using a pointer-to-struct instead of `any` lets the
compiler check the verdict shape when F10 lands.

### Key code paths

**`niwa_create_change` (per-session mcp-serve)**

```
handleCreateChange(args)
├── validate session_id (regexp)
├── load .niwa/sessions/<session_id>.json
├── resolve worktree path; assert git repo
├── compute (session_id, head_ref) idempotency key
├── scan .niwa/changes/ for non-cleaned match → return existing { state: "not_modified" }
├── resolve base_ref (R8 precedence, or hint)
├── git -C <worktree> diff <base>..<head> → buf (with 4 MiB truncate trailer)
├── ReserveID(.niwa/changes/, uuidv4, "<id>/.lock")
│     ↑ shared with newSessionLifecycleID
├── MkdirAll(.niwa/changes/<id>/, 0o700)
├── Write state.json atomically (tmp + rename)
├── Write diff.patch (0o600)
├── AppendChangeEvent(instanceRoot, audit, ChangeEvent{
│       Kind: "change_ready",
│       ChangeID: <id>,
│       Payload: { change_id, url, originating_session, base_ref, head_ref }
│   })
└── return { change_id, state: "pending", url, base_ref, head_ref }
```

**`GET /changes/<id>` (instance surface serve)**

```
mux.HandleFunc("GET /changes/{id}", handler)
└── handler(w, r)
    ├── id := r.PathValue("id"); validate UUIDv4
    ├── state, err := changestore.Read(instanceRoot, id)
    │     ↑ shared flock + JSON parse
    ├── if state.State == "pending":
    │       changestore.UpdateState(instanceRoot, id, mut: pending → in-review)
    │       (re-reads under lock; idempotent on double-arrival race)
    ├── diff, err := os.ReadFile(.niwa/changes/<id>/diff.patch)
    ├── AppendChangeEvent(instanceRoot, audit, ChangeEvent{
    │       Kind: "change_engaged",
    │       ChangeID: id,
    │       Payload: { change_id, surface_url }
    │   })
    └── render.RenderChange(w, RenderData{State: state, Diff: diff})
          ↑ html/template; auto-escapes Diff
```

**GC sweep (instance surface serve)**

```
gc.Run(ctx, instanceRoot, audit, cfg)
├── sweepOnce(now=time.Now()) // synchronous
│     ├── for each <id> in .niwa/changes/:
│     │       state, err := changestore.Read(...)
│     │       if state.State != "pending": continue
│     │       if now.Sub(state.UpdatedAt) < cfg.AbandonDays: continue
│     │       changestore.UpdateState(..., mut: pending → cleaned)
│     │       os.Remove(<id>/diff.patch)
│     │       AppendChangeEvent(audit, {Kind: "change_cleaned", ...})
│     └──
├── go func() {
│       t := time.NewTicker(cfg.IntervalHours)
│       defer t.Stop()
│       for {
│           select {
│           case <-ctx.Done(): return
│           case <-t.C: sweepOnce(time.Now())
│           }
│       }
│   }()
└── return nil
```

### State transitions

The change state machine encoded in `changestore.UpdateState`:

```
                  HTTP GET /changes/<id>
                          ▼
       ┌───────────┐    ┌─────────────┐    F10 verdict cast
       │  pending  │──▶ │  in-review  │ ─────────────────────▶ verdict-cast
       └───────────┘    └─────────────┘                              │
             │                                                       │
             │ GC sweep                                              │
             │ (≥ gc_abandon_days)                                   │
             ▼                                                       │
       ┌───────────┐                                                 │
       │  cleaned  │  ◀───── (F5 never sweeps in-review/verdict-cast) ┘
       └───────────┘
```

The `pending → in-review` write happens under the per-change flock
inside `UpdateState`; concurrent `GET /changes/<id>` requests
serialize (one writes, the rest observe the new state without
re-writing). `change_engaged` fires per HTTP hit regardless (R5);
the state write is the only deduped operation.

### Cross-cutting interfaces

**The `appendEventLog` contract (D5).** Every code path that needs
to emit an F5 event calls `mcp.AppendChangeEvent`. The function
hides the dual-target fan-out: a per-change `transitions.log` line
under the per-change flock, plus an audit-log v=2 line under the
sink's mutex. Errors are returned (callers ignore them).

**The `ReserveID` contract (D8).** Every code path that needs a
fresh, collision-checked ID + reserved-on-disk slot calls
`mcp.ReserveID`. The function takes the directory, the generator,
and the placeholder-name-producing function; it loops up to 5 times
on EEXIST and returns the reserved ID.

**The audit sink contract (D7).** `fileAuditSink.Emit` enforces the
2KB payload budget by inspecting the marshalled byte slice and
downgrading over-budget entries before write. Callers see no API
change.

## Implementation Approach

The work decomposes into atomic, sequencable steps each of which can
ship as its own PR / commit (the PLAN phase will tag each as an
issue). Phasing is logical, not strict — issues within a group can
parallelize.

### Phase 1 — Substrate (no behavior change)

- **`internal/mcp/atomicid.go`** — extract `ReserveID` from
  `session_lifecycle.go`. Update `newSessionLifecycleID` to call it.
  Add unit tests for the shared helper (race, collision, retry).
- **`internal/mcp/audit.go`** + **`audit_reader.go`** — extend
  `AuditEntry` with `Kind`, `Event`, `Payload` fields and bump
  on-write `V` to 2. Add `effectiveKind()` to the reader. Validate
  v=1 records on disk still parse with unit tests using fixture
  files.
- **2KB payload-budget enforcement** inside `fileAuditSink.Emit`
  (D7). Unit-tested with a large payload that triggers the downgrade.

### Phase 2 — Change storage primitives

- **`internal/mcp/changestore.go`** — `ChangeState` struct + JSON
  tags, `Read(instanceRoot, id)`, `UpdateState(instanceRoot, id,
  mutator)`. Mirrors the `taskstore.go` mutator pattern: shared flock
  on `.lock`, schema validate on read, atomic tmp+rename on write,
  fsync of `state.json` and the parent dir.
- **`internal/mcp/changelog.go`** — `AppendChangeEvent` dual-target
  emitter. Unit test: emit one event, assert both `transitions.log`
  line and `mcp-audit.log` v=2 line appear.

### Phase 3 — MCP tools

- **`internal/mcp/handlers_change.go`** — `handleCreateChange`,
  `handleListChanges`, `handleQueryChange`. Each handler is a
  validator → resolver → atomic-write → event-emit pipeline.
- **`internal/mcp/server.go`** — three `toolDef` entries in
  `toolsList`, three cases in the `callTool` switch.
- Unit tests for the three handlers using `t.TempDir()` plus
  `localGitServer.SourceRepoWithDiff` for the diff capture path.
- Race test for concurrent `niwa_create_change` calls against the
  same session (mirrors `newSessionLifecycleID`'s race test).

### Phase 4 — HTTP surface

- **`internal/web/server.go`** — `New(ctx, cfg)` constructor,
  `*http.Server` setup with `net/http`'s 1.22+ method+path routes,
  CORS-stripping middleware (i.e. no `Access-Control-Allow-*`
  emitted), graceful-shutdown plumbing.
- **`internal/web/handlers.go`** — `/`, `/changes/`,
  `/changes/<id>`. Each handler reads state, mutates if needed
  (`pending → in-review` on the per-change endpoint), emits the
  matching event, renders the template.
- **`internal/web/auth.go`** — Bearer middleware. F5 wires it but
  applies it to zero routes; F10 will compose by tagging mutation
  routes.
- **`internal/web/render/`** — templates, embedded CSS, render
  functions.
- **httptest-based unit tests** for the three GET endpoints.

### Phase 5 — Surface lifecycle CLI

- **`internal/cli/surface.go`** — `surface` parent cobra command,
  `serve` subcommand. Flags `--port`, `--rotate-token`. Boot sequence
  per PRD R10: lock → token → bind → port-advertise → log → sync
  GC sweep → start serve + GC ticker → wait on `ctx.Done()`.
- **`internal/web/gc/sweep.go`** — `Run(ctx, instanceRoot, audit,
  cfg)` constructor. Synchronous on-boot sweep; ticker goroutine.
- **`internal/mcp/liveness.go`** — add `IsProcessAlive(pid int)`
  helper (D12).

### Phase 6 — End-to-end testing

- **`test/functional/localrepo_test.go`** — `SourceRepoWithDiff`
  helper.
- **`test/functional/features/review-surface.feature`** —
  `@critical` Gherkin scenarios:
  - Agent creates a change; the operator opens the browser; sees diff.
  - Agent creates a change; HTTP listener not running; URL composes
    with `<port>` placeholder; once listener starts, URL resolves.
  - GC moves abandoned change to `cleaned`; web index reflects new
    state.
- **`test/functional/steps_surface_test.go`** (new file) — step
  definitions: `the operator runs niwa surface serve`,
  `the agent calls niwa_create_change for session <sid>`,
  `the surface URL renders the diff`, etc.

### Phase 7 — Documentation

- `docs/guides/surface.md` — operator guide: how to run
  `niwa surface serve`, what the token is, how to rotate it, how the
  GC interval is configured.
- README link from the niwa repo root.

## Security Considerations

Per the workspace's mandatory security-review discipline, this design
was reviewed against the standard threat surfaces. The PRD's NFR4
section is the authoritative security contract; the design adds
implementation-level mechanisms that satisfy it.

### Threats and mitigations

| Threat | Mitigation | Where |
|--------|------------|-------|
| Cross-origin browser attack against the loopback listener | Listener binds 127.0.0.1 only (kernel-enforced); CORS strips `Access-Control-Allow-*` headers so browsers reject cross-origin XHR | `web.server`, listener config |
| XSS via diff content (`<script>` tags in a reviewed change) | `html/template` automatic contextual escaping of every string field rendered in HTML body context (D10, D13) | `web.render`, all `{{.Diff}}` references |
| Symlink redirection on `.niwa/changes/<id>/state.json` writes | `O_NOFOLLOW` on every open in `changestore.go`, mirroring `taskstore.go`'s discipline | `changestore.go` |
| TOCTOU race on `.niwa/changes/<id>/` creation | `O_CREATE\|O_EXCL` placeholder protocol via `ReserveID` (D8) | `atomicid.go` |
| Audit-log truncation by oversized event payload | 2KB enforcement in `fileAuditSink.Emit` with explicit downgrade (D7) | `audit.go` |
| Stale `surface.lock` lets two listeners bind concurrently | PID liveness check via `Signal(0)` with `EPERM` fail-closed (D12); the second listener also fails on the `net.Listen` bind with `EADDRINUSE` as a backstop | `surface.go`, `liveness.go` |
| Token leak via logs / stderr | First-boot stderr message includes only the path to `.niwa/surface.token` and never the token contents (PRD R10 step 4) | `surface.go` |
| Token leak via filesystem readable by non-owner | File modes `0o600` enforced at every write; directory `0o700` | `surface.go`, `changestore.go` |
| Prompt-injection in `metadata` field reaching audit log | `metadata` is opaque to F5 — never read into payload, never logged. PRD R1 commits this | `handlers_change.go` |
| Bearer-auth token in URLs | Auth contract is header-only (`Authorization: Bearer ...`); query params and cookies explicitly rejected (PRD R6) | `auth.go` |
| Diff content path traversal (a change `id` like `../foo`) | UUIDv4 regex validation on every read in `changestore.Read` (mirrors `taskstore.ReadState` validation discipline); `filepath.Join` of validated `id` only | `changestore.go` |

### Defense-in-depth boundary

The F5 security boundary is per-host. Any process on the same UID
that can `curl 127.0.0.1:<port>` can also `cat
.niwa/changes/<id>/state.json`; the token gates mutations (F10+
endpoints), not reads. Cross-host access is structurally impossible
because the listener never binds a non-loopback interface.

### What this design does NOT defend against

- **Compromised same-UID process.** F5 has no mitigation for an
  attacker who has already executed code as the niwa user. The
  whole substrate is read/writable to that attacker; no protocol
  hardening here changes that.
- **Token disclosure via filesystem ACL bug.** If `.niwa/surface.token`
  is created with the wrong mode due to a future regression, the
  Bearer contract collapses. The design mitigates by setting `0o600`
  at every write site and unit-testing the mode; runtime detection
  of mode drift is out of scope.
- **HTML injection through field names** (e.g. a malicious branch
  name like `<img src=x onerror=...>`). `html/template` escapes all
  string fields; the only path that bypasses is a `template.HTML`
  cast that the design does not use.

### Outcome

The PRD's security contract (NFR4) is fully satisfied by the
mechanisms above. No new threat surfaces are introduced that the PRD
did not already address. The Phase 5 security review of the
shirabe:design skill concludes: **standard** (no new threats requiring
escalated review).

## Consequences

### Positive

- **Clean per-instance vs. per-session separation.** The two
  processes share only filesystem state; restarts of one do not
  perturb the other. F10's verdict-cast endpoint will compose
  cleanly on this boundary.
- **Audit log v=2 is additive.** Existing v=1 records continue to
  parse; new event records use the same atomicity model. No data
  migration is required on the reference fleet.
- **One reservation protocol, one emitter.** `ReserveID` and
  `AppendChangeEvent` are the only shared primitives the two
  processes depend on; their correctness is testable in isolation,
  and every caller composes against the same surface.
- **`html/template` makes XSS a package-level invariant.** NFR4
  compliance does not require per-call discipline; future contributors
  cannot accidentally ship an unescaped render path.
- **Test substrate already exists.** `localGitServer` covers the
  offline Gherkin requirements; one new helper (`SourceRepoWithDiff`)
  is all the fixture work.

### Negative

- **Two processes to run.** Operators must remember to start
  `niwa surface serve` after `niwa create` / `niwa apply`. The CLI
  guide must call this out clearly. A future enhancement (e.g.
  `niwa apply --start-surface`) can fold the boot, but F5 keeps the
  scope minimal.
- **No dynamic config reload.** `gc_interval_hours` and
  `gc_abandon_days` are read at boot; changing them requires
  restarting `niwa surface serve`. Acceptable for F5 (these change
  rarely); future work can add `niwa surface reload` if needed.
- **Audit-log v=2 readers in the wild.** Any tool that currently
  parses `mcp-audit.log` directly (none on the reference fleet but
  the file format is conceptually public) needs to handle absent
  `Tool` / `ArgKeys` fields for `kind=event` records. The change is
  additive but readers that assume `Tool != ""` will break on
  event entries. Mitigation: the schema bump is documented in this
  design, and `audit_reader.go`'s `effectiveKind()` is the
  reference implementation.

### A5: Sweep also reaps stale in-review changes, plus a 4th MCP tool for explicit cancellation

The original design had GC reap only `pending` changes past the
abandonment threshold, on the rationale that an in-review change had
a reviewer attached and F10's verdict-cast was the imminent exit
transition. A post-implementation reachability audit surfaced that
between F5 ship and F10 ship `in-review` has *no* exit transition at
all — any HTTP GET on a change advances `pending → in-review` under
the per-change flock, and a stale Telegram bookmark, search-bot
crawl, or accidental click parks the change in `in-review`
permanently.

Two amendments to close the gap:

1. **Sweep eligibility broadens.** `sweepOnce` now considers both
   `pending` and `in-review` changes past `AbandonDays`. `verdict-cast`
   stays out of scope (human attestation must persist through F10's
   continuation, whatever it is). Cleaned changes are still skipped
   structurally, plus a new idempotency pass reclaims any
   `diff.patch` leaked by a daemon that crashed between
   `sweepChange`'s state mutation and its file removal — the on-boot
   sweep is now re-entrant.

2. **`niwa_cancel_change` MCP tool ships.** Workers retract a change
   they created in error, and the niwa runtime auto-cancels changes
   on two cascade hooks: task abandonment
   (`handleFinishTask(outcome="abandoned")` reaps every change in
   `OriginatingTasks`) and session destruction
   (`handleDestroySession` reaps every change in
   `OriginatingSession`). Authorization on the verb requires the
   calling session_id (from `NIWA_SESSION_ID` env) to equal
   `OriginatingSession` OR the calling task_id (from `NIWA_TASK_ID`
   env) to appear in `OriginatingTasks`. Coordinators without either
   env stamped are rejected — they should route cancellation through
   the auto-cascade, not call the verb directly.

The `niwa-mesh` skill's `allowed-tools` block grows to 18 entries
(the original 14 + the three F5 verbs from the original ship + the
new `niwa_cancel_change`). Workers operating under the skill can now
reach every F5 verb directly — fixing a shipped-but-unreachable gap
where the skill omitted the F5 tools entirely.

### Considered and deferred — URL composition reads global config per call

`composeChangeURL` in `internal/mcp/handlers_change.go` resolves the
workspace+instance identity for the calling instance root by reading
`~/.config/niwa/config.toml` and running the registry traversal on
every `niwa_create_change` / `niwa_list_changes` URL emit. Same for
the surface port read from `~/.config/niwa/surface.port`.

For F5 the call frequency is low (a handful of changes per session
per day at the reference-fleet load), so per-call disk reads are not
a bottleneck. The (workspace, instance) tuple is also deterministic
from the instance root and the registry — caching it at MCP server
startup is sound.

We considered caching at `Server.New(role, instanceRoot)` time and
threading the cached identity through to URL composition. Rejected
because:

1. It adds a coupling between two layers (the MCP server now needs
   to know about the global config at startup, not just at call
   time), and the configuration could change while the server is
   running (operator runs `niwa init` between calls).
2. The MCP server's startup path is already substantial; adding
   "load global config + resolve identity" to it makes the
   per-instance startup heavier for a perf benefit that doesn't
   exist yet.

If profiling shows the per-call read becomes hot (unlikely below
~100 changes/sec, three to four orders of magnitude above F5's
observed load), the cache can be added behind the same function-
indirection variables (`surfaceConfigDirFn`, `loadGlobalConfigFn`,
`resolveWorkspaceInstanceFn`) the tests already use, with cache
invalidation on a registry-file mtime check. Noted here so future-
us doesn't relitigate the decision.

### Mitigations

- The README / surface guide explicitly documents the two-process
  topology.
- The Phase 5 `surface.go` boot sequence prints a clear stderr
  banner (per R10) so operators see when the listener is ready.
- A follow-up issue (filed at PLAN time) will track adding an
  `audit_reader` migration helper if any downstream consumer
  surfaces.

## Open Questions and Deferrals

Anything not pinned by the PRD or this design is named here with its
deferral target so the PLAN phase has zero remaining design
ambiguity.

- **Telegram bridge wiring.** Deferred to the niwa↔coding-tools
  notification bridge spec (PRD D1). F5 emits `change_ready` correctly
  into the audit substrate; the bridge picks the transport.
- **Multi-instance event aggregation.** F5 emits per-instance; bridge
  spec owns cross-instance fan-out (PRD Open Item 2).
- **Comments primitive, verdict cast, line anchoring, threading,
  mentions, CLI/TUI surfaces, polished web UX, koto linkage, hosted
  tier.** All deferred to F6–F16 per PRD's deferral table.
- **`niwa surface stop` / `niwa surface status`.** Reserved as
  subcommands of the `surface` noun; F5 ships only `serve`. Future
  PRDs may add them as operational ergonomics emerge.
- **`audit_reader` migration helper** for downstream consumers if
  any surface. Filed at PLAN time as a follow-up issue (no consumer
  identified at design time).

## References

- Adjacent prior art:
  - `internal/mcp/session_lifecycle.go:131-158` —
    `newSessionLifecycleID` (atomic ID protocol generalized in D8)
  - `internal/mcp/audit.go` — v=1 emit path (extended in D6, D7)
  - `internal/mcp/taskstore.go` — flock + fsync + atomic
    tmp+rename pattern (mirrored by `changestore.go`)
  - `internal/cli/session.go` — cobra command-tree pattern (mirrored
    by `internal/cli/surface.go`)
  - `test/functional/localrepo_test.go` — offline git fixture
    (extended by `SourceRepoWithDiff`)
