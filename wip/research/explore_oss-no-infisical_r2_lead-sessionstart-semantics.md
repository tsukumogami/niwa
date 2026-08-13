# Lead: What does Claude Code actually do with a non-zero-exit SessionStart hook, and what should a degraded `niwa dispatch` / ephemeral-session provisioning do when secrets cannot be resolved?

**Evidence class.** Items marked OBSERVED were produced on this host against
Claude Code **2.1.231** by running real `claude -p` sessions with a synthetic
SessionStart hook that varies exit code and stdout shape. The harness is kept at
`wip/hooktest/hook.sh` + `wip/hooktest/settings.json` (a `MODE` env var selects
the variant); each variant was run as
`MODE=<variant> claude -p "<probe>" --settings ./settings.json < /dev/null`, some
additionally with `--output-format stream-json --verbose`. Transcript-level
evidence comes from the resulting JSONL under
`~/.claude/projects/-Users-danielgazineu-...-wip-hooktest/`. Items marked DOCS
are quoted from <https://code.claude.com/docs/en/hooks> (the canonical URL;
`https://docs.claude.com/en/docs/claude-code/hooks` 301-redirects there). Items
marked RECONSTRUCTED come from reading niwa source only.

---

## Findings

### 1. SessionStart is unblockable. There is no exit code and no JSON field that stops a worker.

DOCS, exit-code-2-behaviour table:

| Hook event | Can block? | What happens on exit 2 |
|---|---|---|
| `SessionStart` | No | Shows stderr to user only |

DOCS, the SessionStart section itself:

> This event doesn't support blocking with exit code 2; Claude Code ignores the
> exit code, but `systemMessage` and `additionalContext` are shown to the user.

OBSERVED, the stronger claim — even the universal `continue: false` field does
not stop a SessionStart session. Hook emitted
`{"continue":false,"stopReason":"STOPDELTA provisioning failed"}` and exited 0;
the session ran the prompt to completion and the process exited 0:

```
$ MODE=halt0 claude -p "Write the word BANANA and nothing else." --settings ./settings.json
BANANA
EXIT=0
```

**Consequence for niwa: "fail the session" is not on the menu.** The design
question round 1 left open ("fail the session, boot silently, or boot with a
degraded context?") has only two live options, because the first one is not
implementable. Whatever the hook does, the worker starts and gets a prompt.

### 2. The exit code has no effect on whether `additionalContext` is delivered. Exit 1 with valid JSON works exactly like exit 0.

This is the single most consequential finding and it is not what the
documentation, read literally, leads you to expect. DOCS scope the
JSON-wins-over-exit-code rule to a subset of events:

> * With JSON that passes schema validation, **for events that use the standard
>   decision model**, Claude Code ignores the exit code and the JSON alone
>   decides the outcome

and the Decision control table that defines "events that use the standard
decision model" **does not list SessionStart at all** (its rows are `PreToolUse`,
`PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `PermissionDenied`,
`UserPromptSubmit`, `UserPromptExpansion`, `Stop`, `SubagentStop`). Read
strictly, SessionStart falls outside the carve-out and lands in the "Without JSON
on stdout ... non-blocking error" bucket. Read via the SessionStart section
("Claude Code ignores the exit code"), it works. The docs are genuinely
ambiguous here, so it had to be measured.

OBSERVED. Probe: `Do you see a string starting with MARKERALPHA anywhere in your
context?` Hook stdout in the JSON variants was
`{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"MARKERALPHA the secret codeword is ZEBRAFISH"}}`.

| variant | exit | stdout | stderr | model sees `additionalContext`? | model sees stderr? | transcript attachment(s) |
|---|---|---|---|---|---|---|
| `json0` | 0 | valid JSON | — | **YES** | n/a | `hook_additional_context` |
| `json1` | 1 | valid JSON | 2 lines | **YES** | **NO** | `hook_additional_context` + `hook_non_blocking_error` |
| `json2` | 2 | valid JSON | 1 line | **YES** | not probed | `hook_additional_context` + `hook_non_blocking_error` |
| `bare1` | 1 | empty | 2 lines | **NO** (nothing at all) | **NO** | `hook_non_blocking_error` only |
| `json0err` | 0 | valid JSON | 1 line | YES | not probed | `hook_additional_context` |

The `json1` answer verbatim:

```
YES MARKERALPHA the secret codeword is ZEBRAFISH

I do not see any text containing 'stderr-line' anywhere in my context. The
MARKERALPHA string arrived as "SessionStart hook additional context"; nothing
resembling stderr output came through.
```

The `bare1` answer verbatim (this is niwa's behaviour today):

```
NO — no `MARKERALPHA` string anywhere in my context.
NO — no text containing `stderr-line`.
No hook error notice either, so nothing to quote.
```

Cross-checked at the transcript layer: 7 of the 9 recorded sessions carry a
`hook_additional_context` attachment — every variant that printed JSON,
regardless of exit code — and the only two that do not are the two `bare1` runs.
Two transcripts carry `"exitCode":1` *and* a `hook_additional_context`
attachment, which is the exact combination in question.

**Consequence for niwa: the delivery of the degraded-mode context does not
depend on getting the exit code right.** niwa can emit `additionalContext` and
still exit non-zero if some other consumer needs the failure signal. It should
not, for the reason in finding 3, but the coupling round 1 assumed does not
exist.

### 3. A non-zero exit buys a transcript notice the model cannot read, and costs nothing else.

OBSERVED, the transcript record written on the `bare1` run
(`~/.claude/projects/...-wip-hooktest/ade2da12-....jsonl`, reformatted):

```json
{"type":"attachment","attachment":{
  "type":"hook_non_blocking_error",
  "hookName":"SessionStart:startup",
  "hookEvent":"SessionStart",
  "stderr":"Failed with non-blocking status code: stderr-line-one\nstderr-line-two",
  "stdout":"","exitCode":1,
  "command":"/.../wip/hooktest/hook.sh","durationMs":30}}
```

Two details. The record captures the **full** stderr, not the "first line"
DOCS promises — but it is an `attachment`, a display record, and the `bare1`
probe proves the model does not receive it. And DOCS are explicit that stderr is
never a model channel on this event:

> Stderr from a hook that exits 0 goes to the debug log only, never the
> transcript, and Claude never sees it.

OBSERVED, the machine surface: under `--output-format stream-json --verbose`
each hook produces a `hook_started` then a `hook_response` system event carrying
`stdout`, `stderr`, `exit_code` and `outcome` — and it carries stderr on exit 0
too (`json0err` produced `"stderr":"STDERRGAMMA warning: recommended env key not
supplied\n","exit_code":0,"outcome":"success"`). So an SDK or Agent-View
consumer *can* see niwa's stderr; the model never can, on any exit code.

So the exit code's entire effect on SessionStart is: choose whether a
`hook_non_blocking_error` notice appears in the transcript UI. For niwa that is
a cost, not a benefit — a red "hook error" next to a session that provisioned
fine-but-degraded misreports the outcome, and the person who would read it is by
construction not watching a background worker.

### 4. `systemMessage` could not be shown to reach any surface in this build.

OBSERVED, inconclusive-by-absence. A hook emitting
`{"systemMessage":"SYSMSGBETA degraded provisioning","hookSpecificOutput":{...}}`
produced no distinct stream-json event on either exit 0 or exit 1 — `SYSMSGBETA`
appeared only inside the `hook_response` echo of the hook's own stdout, never as
a standalone `SDKInformationalMessage` or system message. DOCS say
`systemMessage` is a "warning message shown to the user" and that in the Agent
SDK / `--output-format stream-json` "it can arrive as an
`SDKInformationalMessage`". I could not reproduce that on 2.1.231 headlessly.

**What would establish it:** run an interactive TUI session (not `-p`) with the
same hook and observe whether the warning renders in the transcript pane. Until
that is done, treat `systemMessage` as an unverified channel and do not make it
load-bearing.

### 5. niwa's current hook path emits nothing on failure, and nothing readable on success either.

RECONSTRUCTED, confirming and extending round 1.

On failure: `runInstanceHookStart` returns at
`internal/cli/instance_from_hook.go:174` and never reaches the injection write at
`:188-194`. Cobra's `Execute()` prints the error to stderr and exits 1. The
worker therefore gets the `bare1` row of the table in finding 2: **nothing**.
Not a warning, not a hint, not even a notice it can read.

But there is a second, quieter defect that survives even when provisioning
succeeds. `realProvisionInstance` wires the applier's reporter to the process's
stderr:

```go
applier.Reporter = workspace.NewReporter(os.Stderr)   // instance_from_hook.go:366
```

and every warning niwa produces during provisioning goes there — including
`warnRecommended`'s per-key lines (`internal/workspace/required.go:146-157`) and
the R13.1 "personal vault provider unreachable; falling back to..." line at
`internal/workspace/apply.go:1191-1205`. On this path stderr is a black hole:
DOCS say the model never sees it, and finding 3 confirms the transcript shows it
only as part of an error notice. So **niwa already has a "loud but non-fatal"
warning mechanism wired to a channel that, on the hook path, nobody reads.**

The code comment two lines below shows the authors were half-aware of this:

```go
// No result.Warnings loop here, unlike apply/create/reset: this runs from a
// Claude hook whose stdout is a protocol, not a terminal someone is reading.
//                                          instance_from_hook.go:376-377
```

The reasoning is right about stdout and wrong about the conclusion: on
SessionStart, stdout-as-protocol is the *only* channel that reaches anyone, and
stderr is the one that reaches no one.

### 6. What the worker actually lands in is worse than "uninstrumented".

OBSERVED on this host. A niwa workspace root contains no repositories — only
workspace scaffolding and sibling instances:

```
$ ls -a /Users/danielgazineu/dev/niwaw/tsuku/
.  ..  .claude  .niwa  CLAUDE.md  tsuku+tsuku_oss_no_infisical-26c0f110
```

So on a failed SessionStart provision the background worker starts in a
directory whose `CLAUDE.md` describes a multi-repo workspace, with none of those
repos present, with no explanation, and with **other sessions' instances visible
as siblings**. The root `CLAUDE.md` even tells it that "`niwa list` enumerates
the instances." A capable agent handed a coding task in that situation is
plausibly one `cd` away from doing work inside a different session's instance.
That elevates the missing degraded-context emit from a UX gap to a blast-radius
concern, and it is an argument that the degraded string must say *where not to
work*, not merely *what is missing*.

### 7. `niwa dispatch` is a different failure shape with a real reader, and it already has a context channel the hook path lacks.

RECONSTRUCTED. The two paths are mutually exclusive by construction, which round
1 did not state. `niwa dispatch` provisions first
(`internal/cli/dispatch.go:300`) and only then launches the worker with
`cmd.Dir = instanceDir` (`internal/cli/dispatch_launcher.go:89`). Because the
worker's launch cwd is already inside a valid instance, the SessionStart hook's
re-entrancy guard no-ops — asserted directly by
`TestDispatch_SessionStartGuard_NoOpsInsideDispatchInstance`
(`internal/cli/dispatch_test.go:426-459`). The hook path therefore only fires for
a background session that Claude Code itself starts at the workspace root.

| | `niwa dispatch` | SessionStart hook |
|---|---|---|
| When provisioning fails | worker is **never launched** | worker is **already running** |
| Who sees the error | whoever ran `niwa dispatch` — a human terminal, or the coordinator agent's Bash tool result (exit 1 + full stderr) | nobody: stderr to the void, no JSON, `hook_non_blocking_error` notice the model cannot read |
| Error text | `niwa: error: provisioning dispatch instance: <required-keys block>` (dispatch.go:302) | `niwa: error: provisioning instance for session <uuid>: <block>` (instance_from_hook.go:174) |
| Channel to the worker's context | **prompt prefix** (below) | `hookSpecificOutput.additionalContext` |

That last row is the useful discovery. `dispatchLaunch` already takes a
niwa-authored `prefix` distinct from the developer's `body`:

> prefix is niwa-authored text (today, the keep-alive arming instruction) that
> always rides the argv element
> — `internal/cli/dispatch_launcher.go:33-37`

wired at `dispatch.go:420` (`promptPrefix = keepAliveArmingInstruction`) and
passed at `:427`. A degraded-provisioning notice on the dispatch path needs no
new mechanism: it is a second `promptPrefix` contributor, with
`keepAliveArmingInstruction` (`internal/cli/dispatch_keepalive.go:33`) as the
in-repo template for tone and length.

**So dispatch is much less broken than the hook.** Its failure is loud to a
reader who exists and it never strands a worker. Under the chosen
"consumption-placed, strict-when-reachable" remedy it would exit 0 and launch,
and its worker-facing notice rides the prompt prefix. The hook path is where the
new emit has to be invented.

### 8. Partial success *is* expressible in the hook protocol, and the exit code should be 0.

Yes — cleanly. `additionalContext` is a free-form string, the schema imposes no
success/failure semantics, and finding 2 shows delivery is exit-code-independent.
The recommendation, given findings 1–3:

- **exit 0**, so no `hook_non_blocking_error` notice misreports a degraded-but-
  working instance as a broken hook;
- **stdout carries the normal injection plus a degradation block** — one
  document, because a second JSON object on stdout is not a defined protocol;
- **stderr keeps the human/SDK-facing detail** (it still reaches `hook_response`
  and the debug log), so nothing is lost for an operator running with
  `--output-format stream-json`;
- **do not use `continue`/`stopReason`** (finding 1: inert) and **do not rely on
  `systemMessage`** (finding 4: unverified).

Concrete shape. This is a strawman for the artifact, not settled wording. It
appends to `buildSessionStartInjection`'s existing string
(`internal/cli/instance_from_hook.go:300-326`), after the `cd` instruction and
before the instance `CLAUDE.md`:

```
This instance was created with 3 of its declared environment keys unresolved.
They are absent from the instance's .local.env files (not set to empty values):

  ANTHROPIC_API_KEY   (env.secrets, required) - Anthropic API key for Claude
  TAVILY_API_KEY      (env.secrets, recommended) - Tavily search API key
  BRAVE_API_KEY       (env.secrets, recommended) - Brave search API key

Anything in this instance that reads those variables will fail or fall back.
Treat that as expected, not as a bug in the code you are working on: do not
try to repair it, do not write values for them, and do not report it as a
defect. If the task you were given depends on one of them, say so and stop
rather than working around it.

Everything else in the instance is intact; the repos are cloned and the rest
of the environment is materialized.
```

Four properties that the wording is carrying deliberately:

1. **Naming the keys and their descriptions** — the data is already in hand;
   `missingRequired{Scope, Key, Description}` (`internal/workspace/required.go:15-27`)
   is exactly this record, and the required block is already sorted
   (`required.go:88-93`) while `warnRecommended` is not (`:146-157`), so the
   degraded emit should sort both to stay reproducible.
2. **"absent, not empty"** — matches the chosen remedy's contract (unresolved
   keys omitted from `.local.env`, never written as empty values) and tells the
   agent that `os.Getenv` returning `""` is the designed outcome.
3. **The don't-fix instruction.** Without it a competent agent handed
   "ANTHROPIC_API_KEY is missing" will try to fix it, and the failure modes there
   are bad: it may hunt for the value in the environment, write one into a
   config, or file a defect against working code.
4. **No overlay reference, in any form.** The string names keys, scopes, and
   descriptions — all of which the agent can read out of the config it is sitting
   on. It never says why they are unresolved and never names a config source.
   This satisfies the round-1 constraint by construction, and note that it is a
   *strictly narrower* disclosure than what niwa prints today: the shipping
   required-keys error already ends with "supply it via the personal overlay"
   (`required.go:100-101`) — that is niwa's per-user local `niwa.toml`, a
   different concept from a config-repo overlay, so it does not violate the
   constraint, but it is also useless advice to an OSS contributor and should not
   be copied into the agent-facing string.

The degraded text does not need a `--strict` counterpart on this path: strict
mode's whole point is a hard exit, and the hook cannot produce one that anything
observes.

### 9. The 180-second hook timeout is the one degradation mode no emit can cover.

`rootSessionHookTimeoutSeconds = 180` (`internal/workspace/root_materializer.go:60-64`),
documented at `docs/guides/ephemeral-session-instances.md:68`. `additionalContext`
can only be written after provisioning finishes, so a clone or a vault resolution
that overruns the timeout produces the `bare1` row again — worker booted at the
root, nothing said — and no amount of care in the emit changes that. Worth
recording as a known residual, and worth noting that a slow/hanging vault CLI is
a plausible way to hit it. I did not test what Claude Code does to a
timed-out hook (kill signal, recorded outcome); establishing it would take a
hook that `sleep`s past a short configured timeout and an inspection of the
resulting `hook_response` event.

### 10. Two smaller confirmations.

**Multiple SessionStart entries coexist and are independent.** niwa's
materializer deliberately merges its ephemeral entry with any installed one
rather than overwriting (`TestBuildSettingsDoc_SessionStartMergesInstalledAndEphemeral`,
`internal/workspace/materialize_sessionhooks_test.go:47-79`; also the
work-summary interaction test at `materialize_worksummary_test.go:256-281`). So
a niwa hook that exits non-zero does not suppress a sibling hook's context, and
conversely niwa's degraded block will appear alongside other injections, not
instead of them. The block should be self-identifying ("from niwa") for that
reason — as `keepAliveArmingInstruction` already is ("Keep-alive (from niwa):").

**The guide documents the guard's no-op as "exit 0, no output"
(`docs/guides/ephemeral-session-instances.md:89-90`) and documents the success
emit (`:123`), but says nothing about the failure path.** There is no sentence
anywhere in the repo describing what happens when provisioning fails inside the
hook. That is consistent with round 1's read that this path was never designed,
only fallen into.

---

## Implications

1. **"Fail the session" is off the table; the design space is two options, not
   three.** SessionStart cannot block by exit code, and `continue: false` is
   inert there (finding 1, OBSERVED). Any artifact that lists "abort the worker"
   as an alternative is listing something that cannot be built. The real choice
   is "boot silently" (today) versus "boot with a degraded context".

2. **The fix on the hook path is an emit, not an exit-code change.** Because
   `additionalContext` is delivered regardless of exit code, and because stderr
   reaches nobody the model can be, the entire remedy for the launch-coupled path
   is: *always write the JSON*. That is a change to
   `runInstanceHookStart`/`buildSessionStartInjection`, and it is largely
   independent of the `checkRequiredKeys` severity decision — it would improve
   the hook path even if the required check stayed fatal, by turning "worker
   strands silently" into "worker is told the instance could not be created and
   to stop". Those are two separable changes and the emit is the cheaper one.

3. **niwa's existing warning channel is misrouted on this path.** Every
   `Reporter.Warn` on the hook path goes to stderr (`instance_from_hook.go:366`)
   and is invisible to the model. If "loud but non-fatal" is going to mean
   anything for a background worker, the warnings must be *rendered into the
   additionalContext string*, not merely emitted. A design that softens
   `checkRequiredKeys` to a warning and stops there would produce, on this path,
   a silently degraded instance — arguably worse than today's silent strand,
   because at least today nothing pretends to have worked.

4. **`dispatch` and the hook need different remedies at different layers, and
   dispatch's is nearly free.** dispatch already has a niwa-authored prompt
   prefix (`dispatch_launcher.go:33-37`, `dispatch.go:420`); the degraded notice
   is a second contributor to it. The hook needs a new emit. Both should render
   the same underlying record, so the sensible shape is a shared formatter over
   `[]missingRequired`-like data with two renderers.

5. **The degraded string has a second job beyond information: containment.**
   Finding 6 — a stranded worker sits at a root with sibling instances in view
   and a CLAUDE.md telling it how to enumerate them. Whatever niwa emits on a
   *total* provisioning failure must say "you have no instance; do not work in
   this directory or any sibling of it; stop and report," not just name the
   missing keys. That is a distinct message from the partial-success one, and the
   design needs both.

6. **The constraint is easy to satisfy and the current messages already comply,
   accidentally.** A key-and-description inventory discloses nothing about where
   values come from. The one existing phrase that mentions an overlay
   (`required.go:100-101`) means the personal `niwa.toml`, not a config repo — so
   there is no live disclosure bug to fix, only a uselessness problem, and the
   agent-facing string should simply omit remediation advice entirely.

---

## Surprises

- **`continue: false` does not stop a SessionStart session.** It is documented as
  a universal field that makes Claude "stop processing entirely," and it silently
  does nothing here. OBSERVED.
- **Exit code 1 delivers `additionalContext` exactly as exit 0 does** — verified
  three ways (model probe, `hook_additional_context` transcript attachment,
  stream-json). The documentation's carve-out is scoped to "events that use the
  standard decision model," and **SessionStart is absent from the Decision
  control table** that defines that set, so the docs read as though it should
  *not* work. It does.
- **Exit 2 also delivers `additionalContext`**, on an event the docs say cannot
  block with exit 2. So all three exit codes behave identically with respect to
  context injection.
- **The transcript's `hook_non_blocking_error` record carries the full stderr**,
  not the "first line of stderr" the documentation promises — but it is an
  `attachment`, and the model provably cannot read it.
- **stderr is captured in the `hook_response` stream-json event even on exit 0
  with `outcome: "success"`** — so it is not wholly discarded, just unreachable
  by the model.
- **`niwa dispatch` and the SessionStart hook can never both fire for the same
  session** (dispatch launches into the instance, tripping the re-entrancy
  guard). Round 1 treated them as the same path; they are disjoint, and only one
  of them strands a worker.
- **The workspace root a stranded worker lands in contains other sessions'
  instances** and a CLAUDE.md explaining how to list them.
- **`systemMessage` produced no observable surface at all** on 2.1.231 in
  headless mode, on either exit code.

---

## Open Questions

1. Does `systemMessage` render anywhere in an interactive TUI session? If it
   does, it is the natural home for the operator-facing half of a degraded
   report, and the split becomes clean (additionalContext for the agent,
   systemMessage for the human). Needs a TUI run; not establishable with `-p`.
2. What does Claude Code do to a hook that exceeds its configured timeout —
   what `outcome` is recorded, and is any partial stdout honored? Determines
   whether niwa could stream a degradation notice before slow work completes, or
   whether the 180 s ceiling is simply an uncoverable gap (finding 9).
3. Should a *total* provisioning failure and a *partial* (degraded) provisioning
   emit through the same code path with different text, or should the total
   failure additionally do something structural — e.g. still write the session
   mapping so `niwa reap` and `niwa status` know a session exists with no
   instance? Today a failed hook leaves no trace at the workspace root at all.
4. Does the degraded block belong before or after the instance `CLAUDE.md` in
   the injected string? The current builder appends CLAUDE.md last
   (`instance_from_hook.go:312-315`); a long CLAUDE.md could bury a warning
   placed above it, but placing niwa's warning after the workspace's own
   authoritative guidance inverts the stated precedence.
5. Is there an appetite for niwa to depend on the observed exit-code-independence
   of `additionalContext`, given the documentation does not clearly promise it?
   The safe posture is to exit 0 anyway (which is what finding 8 recommends for
   independent reasons), so nothing load-bearing rests on the surprise — but a
   comment in the code should record that the JSON, not the exit code, is what
   carries the message.
6. On the dispatch path, does the degraded notice belong in the prompt prefix
   (rides the argv, always present, costs prompt budget and interacts with the
   spill logic at `dispatch_launcher.go:57-68`) or should dispatch instead rely on
   the instance's own materialized context? The prefix is the precedented channel
   but it is also the one that a very large developer prompt pushes toward a
   spill.

---

## Summary

Claude Code 2.1.231 delivers a SessionStart hook's `hookSpecificOutput.additionalContext`
to the model regardless of exit code — verified on exit 0, 1, and 2 — while
stderr never reaches the model on any exit code and even `continue: false` fails
to stop the session, so niwa's current exit-1-with-no-JSON is the one shape that
tells the worker nothing at all, and "fail the session" is not an implementable
option. The remedy on the hook path is therefore an emit rather than an exit-code
change: always write the JSON, exit 0, and render the missing-key inventory into
the `additionalContext` string itself (niwa's `Reporter.Warn` output is a black
hole on this path even when provisioning succeeds), with a matching notice on the
dispatch path riding the existing niwa-authored prompt prefix — noting that
dispatch never strands a worker at all, since it provisions before launching. The
biggest open question is what a *total* provisioning failure should say and do,
because a stranded worker lands at a workspace root that contains no repos but
does contain other sessions' instances plus a CLAUDE.md explaining how to
enumerate them.
