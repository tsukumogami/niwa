<!-- decision:start id="setup-script-output-routing" status="assumed" -->
### Decision: Setup-script output routing

**Context**

niwa runs each repo's `scripts/setup/*` executables at Step 6.75 of the apply
pipeline (`internal/workspace/apply.go:1581-1596`), sequentially, one repo after
another. Each script goes through `runCmdWithReporter`
(`internal/workspace/gitutil.go:93-116`), which attaches an `io.Pipe` to both
`cmd.Stdout` and `cmd.Stderr`, scans lines, strips ANSI/OSC escapes, and hands each
line to `r.Status`. `Reporter.Status` returns immediately when `!r.isTTY`
(`reporter.go:63`), so off a TTY every line is discarded before it reaches the
writer. On a TTY the line only sets `spinMsg`; rendering happens on `doTick`'s
100 ms ticker, and `stopSpinner` erases the last frame. Prior research measured
this: off-TTY the buffer is literally empty, and on-TTY exactly one line of a
fifty-line script survives, with *which* line being a goroutine-scheduling
artifact. A failing script's own explanation reaches the operator in neither mode;
all that survives is `warning: setup script scripts/setup/01-x.sh failed for repo:
exit status 3`.

This is not drift from a considered position — it is an implementation that
followed a typo in its own design. `DESIGN-clone-output-ux.md`, the document that
introduced `runCmdWithReporter`, specifies `reporter.Log` for setup scripts at
lines 381-382, 393, 509 and 575-576, and `r.Status` in exactly one place: the Go
doc stub in the Key Interfaces block at 435-438. Verified by grep in this
worktree. The implementation copied that stub verbatim into gitutil.go:87-92,
comment and all, including "so script output is silent in piped/CI contexts". No
rationale for `Status` is recorded anywhere in either design.
`DESIGN-post-clone-scripts.md` Decision 2 independently promises "Stdout/stderr:
printed to niwa's output, prefixed with the repo name", and its Security
Considerations section claims niwa "prints each script name before execution".
Neither has ever been true.

The reading context matters for the shape of the fix. niwa's apply output is
extremely terse and entirely flat: a grep across `internal/` for indented `Log`,
`Defer` or `DeferWarn` format strings returns zero hits, and there are no per-repo
lines at all during an apply — `DESIGN-clone-output-ux.md:179,503` specified
`Log("cloned %s", repo)` per repo, but the parallel-clones change replaced it with
one aggregate spinner (`Status("cloning repos... (%d/%d done)")`, apply.go:1333 and
:1353). A whole successful apply renders as a spinner, then `applied ws (5 repos)`,
then a deferred warning block.

**Assumptions**

- Repo names are unique within a workspace, so the repo name alone identifies a
  repo in a prefix and the group is redundant. Grounded in `ResolveSetupDir`
  (`setup.go:33`), which looks up `ws.Repos[repoName]` by bare name. *If wrong:*
  two repos with the same name in different groups produce ambiguous prefixes, and
  the prefix becomes `[<group>/<repo>/<script>]` — a one-line change, since
  `cr.Group` is already in scope at apply.go:1581.
- Successful-script output being audible is an acceptable cost. Grounded in
  measured volume by class (a real `install-hooks.sh` emits 2 lines, `npm install`
  4, `pip install` 20, a deliberately verbose `go mod download -x` 139; `go build`
  and `go vet` emit 0 on success) and in the fact that `runCmdWithReporter` always
  attaches an `io.Pipe`, so every tool that checks `isatty` is already in quiet mode
  before niwa routes a byte. *If wrong:* the answer is a future `--quiet`/`--verbose`
  pair — niwa has neither today — not a return to discarding output.
- Decision made in `--auto` mode without author confirmation, hence status
  `assumed` rather than `confirmed`.

**Chosen: Stream every line through `Reporter.Log`, prefixed `[<repo>/<script>]`, with a durable per-script announcement**

Three changes, all inside `internal/workspace`:

1. `runCmdWithReporter` routes each scanned, escape-stripped line through
   `r.Log("%s%s", prefix, line)` instead of `r.Status(line)`. Nothing is buffered.
   The prefix is supplied by the caller.
2. `RunSetupScripts` emits `r.Log("running setup script %s/%s", repo, script)`
   immediately before each `exec.Command`, and builds the prefix
   `"[" + repo + "/" + script + "] "` once per script.
3. The docstring at gitutil.go:87-92 and the test at gitutil_test.go:149, both of
   which currently assert the defect is intended, are rewritten.

Rendered, a two-repo apply where koto's script fails looks like:

```
running setup script tsuku/01-git-hooks.sh
[tsuku/01-git-hooks.sh] Installing git hooks... done.
running setup script tsuku/02-install-deps.sh
[tsuku/02-install-deps.sh] added 4 packages in 1s
running setup script koto/01-git-hooks.sh
[koto/01-git-hooks.sh] error: .githooks not found
applied ws (3 repos)
warning: setup script scripts/setup/01-git-hooks.sh failed for koto: exit status 1
```

Identical byte-for-byte off a TTY, minus the spinner that is not there.

**Rationale**

The design already decided this, four times against one, and the one dissenter is
a comment in an interface stub with no argument attached to it. Restoring `Log` is
finishing an implementation, not reversing a decision — which is why this option
carries no burden of overturning anything, and the three alternatives all do.

The objection that would have justified a different answer — that streaming
wrecks the spinner or floods the terminal — was measured and does not hold.
`stopSpinner` nils `r.spinStop` and self-disables (`reporter.go:116-121`), and only
`Status` re-arms it. Nothing in Step 6.75 calls `Status`. So the spinner is torn
down **once for the entire phase**, not once per script and not once per line. A
100-line, two-script apply-loop simulation produced 2 `\r\033[K` sequences total, of
which one is the preceding `Status`'s initial `doTick` render and one is the single
real teardown; all 100 lines arrived in order, unmangled, with no spinner frame
splitting a line. The same measurement on the hybrid produced the same count. The
spinner argument does not distinguish the options at all.

What does distinguish them is that streaming needs no cap. Alternatives 2 and 3
hold output in memory and therefore require a bounded tail, which means the design
must pick an arbitrary number, and a tail can drop the very line that explains the
failure — an error printed early, followed by cleanup chatter or a stack trace.
Streaming writes and forgets. There is nothing to bound and nothing to lose.

Streaming is also the only option under which a hanging script is visible. This is
the Terraform `remote-exec` failure mode (hashicorp/terraform#19557: output stops
arriving and the operator cannot distinguish a hang from a slow success), and it
matters here more than it does for Terraform, because niwa's dominant caller is a
background agent reading a dispatch log after the fact.

Finally, streaming fixes `--no-progress` as a side effect. `internal/cli/apply.go:152`
and `create.go:167` build the Reporter with `!noProgress && term.IsTerminal(...)`, so
the flag forces `isTTY=false`. Today that silences setup output entirely on an
interactive terminal, directly against `DESIGN-clone-output-ux.md:337-339`'s stated
intent that the flag "suppresses the status line without affecting completion lines".
`Log` is durable off-TTY, so the flag stops suppressing content.

**Alternatives Considered**

- **Buffer per script, attach the tail to the returned error** (mirroring
  `runGitWithReporter`'s `fmt.Errorf("%w\n%s", ...)` at gitutil.go:82). The
  runner-up, and the smallest diff of the four — it needs no `apply.go` change at
  all, because apply.go:1592 already formats `sr.Error` with `%v` into a
  `DeferWarn` that `FlushDeferred` routes through `Log`. Rejected because it
  contradicts Decision 2's prose (which says stdout/stderr are *printed*, not
  *printed on failure*), because it requires an arbitrary cap the design would have
  to defend, because a hanging script becomes indistinguishable from a slow one,
  and because the output lands in the deferred block after the summary, detached
  from where it happened — with only the first line of a multi-line deferred
  message carrying the `warning: ` prefix. Its real argument, that successful-script
  output is noise nobody asked for, is a case for a future `--quiet`, not for keeping
  the failure explanation out of the log.

- **Hybrid: keep `Status` for the live TTY feel, additionally buffer and replay on
  failure.** Rejected because the thing it preserves does not exist. The "live TTY
  feel" was measured to be one arbitrary line of fifty, rendered on a 100 ms tick and
  then erased — and prior research showed this is worse than lossy, it is misleading:
  an operator can catch line 4 of 50 and reasonably conclude that is where the script
  failed. Its measured spinner-teardown cost is identical to streaming (2 sequences,
  the single real teardown landing cleanly at the front of the replay block), so it
  buys nothing there either. What it does buy is two mechanisms where one suffices,
  plus the same arbitrary cap as alternative 2.

- **Write output to a log file under the instance and print the path on failure.**
  Rejected on cost: it introduces three conventions niwa does not have. No niwa
  command writes a log or transient file under an instance root today (the only
  `os.MkdirTemp` calls target the OS temp dir). `RunSetupScripts` receives a plain
  repo clone at `<instanceRoot>/<group>/<repo>`, and niwa scaffolds a `.niwa/`
  subdirectory inside a checkout only for delegated worktrees
  (`internal/worktree/worktree.go:106`); `internal/gitexclude/exclude.go:35` excludes
  `*.local*` and `.niwa/`, so a log file anywhere else appears as untracked junk in
  the operator's own `git status` inside their repo, and making it invisible means
  extending exclude scaffolding to plain clones for the first time. And no
  "see &lt;path&gt; for details" output pattern exists anywhere in niwa. All of that
  to point at a file, when the dominant caller is already reading a log.

- **A new durable `Reporter` method.** Folded into the chosen option rather than
  evaluated separately: whatever it did would be `Log` plus a string concatenation,
  since `Log` is already the both-modes-durable channel, pinned escape-free and
  `\r`-free off-TTY by `reporter_test.go:20,34,49`. It would add public API surface
  for no new capability and touch the file the constraints say not to rework. The
  prefix belongs at the call site.

**Consequences**

*What becomes true that the designs already claimed.* Decision 2's "printed to
niwa's output, prefixed with the repo name" becomes true. Its Security
Considerations claim that niwa "prints each script name before execution" becomes
true, in both TTY modes. `DESIGN-clone-output-ux.md`'s four `reporter.Log`
passages become accurate, and its one dissenting stub at 435-438 must be corrected
so the two documents stop disagreeing.

*What gets louder.* A successful apply now prints one announcement line per script
plus whatever the scripts say. On a ten-repo workspace with two scripts each that
is twenty announcement lines on every apply, including no-op re-applies. This is
the accepted cost. It is bounded, every line is greppable by its prefix, and the
alternative — deleting a security claim the design makes — reduces niwa's stated
posture to buy quiet.

*What a hostile or careless script can now do.* Script output now reaches terminal
scrollback, CI logs and dispatch logs, where it previously reached nothing. Escape
injection is already handled: `stripEscapes` runs unconditionally on every line
(gitutil.go:105) before routing. Output forgery is handled by the prefix — a script
echoing `applied ws (5 repos)` renders as `[repo/01-x.sh] applied ws (5 repos)`.
**Secret exposure is not handled by this decision and is explicitly deferred to
Decision 3**, which chose unconditional scrubbing through the per-apply
`secret.Redactor`. This decision creates the exposure path, and it creates the
single choke point where that mitigation attaches: the per-line body of
`runCmdWithReporter`, where the scrub sits between the existing `stripEscapes` call
and this decision's `r.Log`. One place to audit, whatever either decision does.

*What must be deliberately broken.* `gitutil_test.go:149`
(`TestRunCmdWithReporter_AllLinesViaStatus`) asserts that `"fatal: this is fine\n"`
must *not* appear as a permanent line. It goes red by design and must be rewritten,
not worked around — renamed to reflect `Log` routing, keeping its still-valid second
assertion that a `fatal:` line is *not* routed through `Warn` (there is still no
classifier). The docstring at gitutil.go:87-92 documents the bug as a feature and
must be rewritten in the same commit.

*What stays as it is.* `Reporter` is untouched. `Status` remains a no-op off-TTY,
which is correct for spinner progress. The deferred warning at apply.go:1592 keeps
its current job — naming the repo, the script and the exit code — because under
streaming the explanation has already been printed inline, so there is no
duplication. Whether that warning's shape or the command's exit code changes is
Decision 2's call, not this one.

*What a later change would break.* The scanner goroutine calls `Reporter.Log`,
which writes to `r.w` without holding `r.mu`. That is safe today only because the
main goroutine is parked in `cmd.Run()` and `stopSpinner` provably joins the spinner
goroutine before writing — the same undocumented invariant `runGitWithReporter`
already leans on at gitutil.go:68. The Step 6.75 loop is sequential, but the clone
loop directly above it at apply.go:1333 is not. Anything that later parallelizes
setup scripts across repos breaks this. A one-line comment recording the invariant
should land with the change.
<!-- decision:end -->

---

## Sub-question answers

### 1. The exact prefix format

**Flat `[<repo>/<script>] <line>`. One level, no indentation, no per-repo header.**

Reproducing `DESIGN-post-clone-scripts.md` Decision 2's two-level shape
(`Running setup for tsuku...` with indented `  [01-git-hooks.sh] ...` beneath) is
rejected against how niwa's apply output actually reads, verified in this worktree:

- **Nothing in niwa's production output is ever indented.** A grep across
  `internal/` for `Log("  `, `Log("\t`, `Defer("  ` and `DeferWarn("  ` returns zero
  hits. Every `Reporter.Log` format string in `internal/workspace` and
  `internal/plugin` is a flat, lowercase, verb-first line: `applied %s (%d repos)`,
  `created %s (%d repos) → %s`, `healed %d dangling plugin record(s): %s`,
  `destroying instance: %s`, `pruned %d plugin record(s) across %d plugin(s) for %s`.
  A two-level indented block would be the only nested structure niwa emits.
- **There are no per-repo lines at all during an apply.** The design that promised
  `Log("cloned %s", repo)` per repo (`DESIGN-clone-output-ux.md:179,503`) was
  superseded by parallel clones, which collapsed it to one aggregate spinner at
  apply.go:1333/:1353. Introducing a per-repo header for setup scripts specifically
  would give setup scripts a structural prominence that cloning — the more important
  operation — does not have.
- **The sample's `Running setup for niwa... (no setup directory)` line is rejected
  outright.** Repos with no setup dir already `continue` at apply.go:1586. Nothing
  else in niwa announces its own no-ops, and on a ten-repo workspace with two
  setup dirs this would be eight lines of nothing.

Against a header, the flat prefix carries strictly more information per line: in a
flat stream where one repo's lines are immediately followed by another's, a bare
`[01-git-hooks.sh]` is ambiguous the moment two repos ship the same conventional
script name — which the numeric-prefix convention actively encourages. Every line
must be independently attributable, because a dispatch log is read by grepping, not
by scrolling to find the nearest header.

Group is omitted. `cr.Group` is in scope at apply.go:1581 and `%s/%s`-slash-joined
group/repo is precedent (`workspace_context.go:588`), but repo names are unique
within a workspace — `ResolveSetupDir` (`setup.go:33`) keys `ws.Repos` by bare name —
so the group adds width without adding identity. The existing setup-failure
`DeferWarn` at apply.go:1592 already names the repo alone, so this matches.

The repo name needs no new plumbing: `SetupResult.RepoName` is already computed at
`setup.go:47` as `filepath.Base(repoDir)`, which is exactly the repo name given
`repoDir = <instanceRoot>/<group>/<repo>`, and is currently read by nobody. This
change gives that dead field its first reader.

One implementation note: the line must be passed as an argument
(`r.Log("%s%s", prefix, line)`), never concatenated into the format string.
`Reporter.Log` does `fmt.Fprintf(r.w, format+"\n", a...)`, so a script emitting a
literal `%s` would otherwise render as `%!s(MISSING)`.

### 2. Is the per-script announcement a `Log` or a `Status`?

**`Log`.** Unambiguously.

A `Status` announcement would be invisible off-TTY — in dispatch logs, CI, and the
SessionStart hook path (`cli/instance_from_hook.go:366` builds `NewReporter(os.Stderr)`
where stderr is a pipe), which are precisely the environments this whole decision
exists to fix. `DESIGN-post-clone-scripts.md`'s Security Considerations claim that
niwa "surfaces what it's about to run" would remain false in exactly the contexts
where an operator cannot watch. Routing the announcement through the channel whose
defining property is that it discards information would reproduce the defect one
level up.

It is also the only thing that makes two cases visible at all: a script that
succeeds silently (`go build` and `go vet` emit zero lines on success — measured),
and a script that hangs before emitting anything. In both, the announcement is the
sole evidence the script ran.

Wording follows house style — lowercase, verb-ing form, matching
`destroying instance: %s`:

```
running setup script <repo>/<script>
```

Placement: `setup.go`, immediately before `exec.Command` at line 104 — that is,
*after* the executable-bit check at :96-102, so a directory of non-executable files
produces no announcements for scripts that never run.

### 3. What happens to the spinner

**Torn down once for the entire Step 6.75 phase. Interleaving stays correct.**

Measured, not reasoned. `stopSpinner` (`reporter.go:116-132`) returns immediately
when `r.spinStop == nil`, and it nils `spinStop` on its way out. Only `Status`
re-arms it. `Log` calls `stopSpinner` only on TTY. Nothing in Step 6.75 calls
`Status` under this decision. Therefore:

- The **first** `Log` of the phase — the first script's announcement line — performs
  the one real teardown, clearing whatever spinner the preceding phase left running.
- Every subsequent `Log`, across every line of every script of every repo, finds
  `spinStop == nil` and is a plain `Fprintf`.
- The spinner reappears at the next `Status` call, which is a later apply phase.

Measured on a full apply-loop simulation (`Status("cloning repos...")` → script A,
50 lines → script B, 50 lines → `Log("applied ws (2 repos)")`, `isTTY=true`):
2 `\r\033[K` sequences in the whole buffer — one is the initial `doTick` render fired
by `Status` before any script ran, one is the single teardown — with all 100 lines
present, correctly ordered, unmangled, and one spinner glyph in the entire output.
Off-TTY the same simulation produced a clean 101-line append-only buffer with zero
ANSI bytes.

Interleaving is correct by construction, not by luck: there is one scanner goroutine
per script doing sequential `Scan()` calls; the main goroutine is parked in
`cmd.Run()` while it runs; `runCmdWithReporter` joins the goroutine (`<-done`,
gitutil.go:113) before returning, so script A's last line is written before script
B's first; and the Step 6.75 loop over repos (apply.go:1581) is sequential. The one
fragility is the unguarded write noted in Consequences — safe today, broken by any
future parallelization of the loop.

### 4. Cap or truncation on output volume

**No volume cap. None is needed, and adding one would be a defect.**

Streaming retains nothing — each line is written to `r.w` and forgotten. There is no
buffer to bound, so a cap could only mean *discarding output the operator asked to
see*, which is the exact defect being fixed. A tail cap is worse than it sounds:
scripts that fail commonly print the error first and then cleanup chatter or a
stack trace, so a last-N-lines tail preferentially drops the explanation. This is a
concrete advantage over alternatives 2 and 3, both of which *require* a number the
design would then have to justify.

Volume does not warrant one on the evidence. Measured by class in this environment:
a real `install-hooks.sh` from a public repo in this workspace emits 2 lines,
`npm install express` 4, `pip install requests` 20, a deliberately verbose
`go mod download -x` 139, and `go build ./...` / `go vet ./...` emit 0 on success.
`runCmdWithReporter` always attaches an `io.Pipe`, so every script sees
`isatty(1) == false` regardless of niwa's own TTY state, and pip/npm/cargo and
friends are already in quiet mode before niwa routes a byte. The pathological cases
are narrow and self-inflicted (`set -x`, `make` echoing recipes, an explicit `-v`),
and in those the operator has opted into the noise in their own script.

**One number does belong in the design, and it is a correctness bound, not a volume
policy: raise the per-line scanner limit to 1 MB and check `scanner.Err()`.**

`bufio.NewScanner(pr)` at gitutil.go:103 uses the default 64 KB `MaxScanTokenSize`
and `scanner.Err()` is never checked. Measured: a script emitting one 100 KB line
followed by a normal line causes **total silent loss of the script's entire output**
— zero lines reach the reporter, not even the normal line — while
`runCmdWithReporter` returns the misleading `io: read/write on closed pipe` and the
script itself exits 0. The real cause, `bufio.Scanner: token too long`, is visible
only through the `scanner.Err()` that production code never calls. A 60 KB line
works correctly, confirming the boundary.

This is pre-existing and affects `runGitWithReporter` equally, so it is adjacent to
the routing question rather than part of it. But shipping a visibility fix that
still silently loses a whole script's output the moment it prints a base64 blob or a
JSON dump would leave a hole in the thing being fixed. Recommendation:
`scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)` — 16x headroom over the
current ceiling, still a bounded single allocation, and comfortably above anything a
setup script legitimately prints on one line — plus a `scanner.Err()` check after
the loop that surfaces truncation through `Warn` rather than swallowing it.

### 5. Where the change lands

**`internal/workspace/gitutil.go` — `runCmdWithReporter` only.** Its docstring
(lines 87-92) is rewritten; it currently documents the defect as intent. Its body
changes at line 106 from `r.Status(line)` to `r.Log("%s%s", prefix, line)`, gains
the `scanner.Buffer` call and the `scanner.Err()` check, and its signature grows a
`prefix string`. `runGitWithReporter` is not touched by this decision (it may be
touched by the `scanner.Buffer` fix if that is applied to both, which it should be).

**`internal/workspace/setup.go` — `RunSetupScripts` only.** Inside the script loop,
after the executable-bit check at :96-102 and before `exec.Command` at :104: emit
`r.Log("running setup script %s/%s", result.RepoName, name)`, build
`prefix := fmt.Sprintf("[%s/%s] ", result.RepoName, name)`, and pass it through.
`result.RepoName` already holds the right value (`setup.go:47`) and is read by
nobody today. `ResolveSetupDir` is untouched.

**`internal/workspace/apply.go` — no substantive change.** Step 6.75's loop already
has everything it needs; the `DeferWarn` at :1592 keeps its exact current form. The
only edits are arguments at the `RunSetupScripts` call on line 1584.

**Signature.** This decision needs `runCmdWithReporter` to gain a `prefix string`
and `RunSetupScripts` to pass it. It needs nothing else — the repo name is already
in `SetupResult.RepoName` (`setup.go:47`) and the script name is the loop variable.

Decision 3 independently requires a `*secret.Redactor` on both signatures, and its
choice of an **explicit parameter rather than a threaded `ctx`** should be adopted
here too. Its argument is better than the `ctx` shape this decision first reached
for, on two grounds worth recording: `redactor.go:118-131` scopes the
attach-to-context pattern as "a mild anti-pattern" accepted narrowly for the vault
resolution pipeline, which `internal/workspace` is not, and the call site at
apply.go:1584 already holds the concrete `redactor` local from apply.go:1105 — so
threading `ctx` to re-extract a value already in hand adopts the anti-pattern where
its own justification does not reach. Separately, `RunSetupScripts` uses
`exec.Command`, not `exec.CommandContext`, so a `ctx` parameter would advertise
cancellation semantics the function does not honor.

Combined, the two decisions land one signature change per function:

```go
func runCmdWithReporter(r *Reporter, cmd *exec.Cmd, prefix string, red *secret.Redactor) error
func RunSetupScripts(repoDir, setupDir string, r *Reporter, red *secret.Redactor) *SetupResult
```

Nine call sites total, seven of them tests: `gitutil_test.go:157` and `:183`,
`setup_test.go` at :60, :68, :77, :95, :133, :160, :187, and apply.go:1584. The
scrub and this decision's routing meet at exactly one place — the per-line body of
`runCmdWithReporter`, where Decision 3 places `red.Scrub` immediately after the
existing `stripEscapes` call and this decision replaces `r.Status(line)` with
`r.Log("%s%s", prefix, line)` on the next line. That ordering (strip, then scrub,
then prefix, then log) is the one an implementer must preserve.

Adding fields to `SetupResult`/`ScriptResult` costs nothing (the tests use keyed
literals and read named fields), but this decision needs no new fields.

Adding fields to `SetupResult`/`ScriptResult` costs nothing (the tests use keyed
literals and read named fields), but this decision needs no new fields.

**Tests that must change.** `gitutil_test.go:149`
(`TestRunCmdWithReporter_AllLinesViaStatus`) — rewritten and renamed; its
`warning: fatal` assertion survives, its transient-output assertion inverts. The nine
tests in `setup_test.go` need only the mechanical signature edit; none asserts on
reporter output today. A regression test asserting the failing script's own stderr
reaches the operator in **both** TTY modes belongs in `setup_test.go`, and must
assert on permanent output rather than raw buffer bytes — prior research measured
that a naive `strings.Contains(buf.String(), marker)` passes on today's `main` in
TTY mode for any single-line script, which would be a false green.
<!-- Sub-question answers end -->
