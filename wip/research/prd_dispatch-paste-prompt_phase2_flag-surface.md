# Lead: How does an interactive prompt capture compose with `niwa dispatch`'s existing flag surface and argument contract?

## Findings

### 1. The complete flag surface

Every flag on `niwa dispatch` is declared in one place -- the `init()` at
`internal/cli/dispatch.go:20-36`. Nothing is registered in
`dispatch_keepalive.go`, `dispatch_remotecontrol.go`, `dispatch_model.go`, or
`dispatch_plugins.go`; those files hold resolution logic and the `triBoolValue`
flag type, not registrations. A grep for `dispatchCmd.Flags()` across
`internal/` returns only `dispatch.go:21-34`.

| Flag | Short | Type | Default | What it does | Declared |
|---|---|---|---|---|---|
| `--label` | -- | string | `""` | Human-friendly alias recorded on the durable session mapping (`Label` field, `dispatch.go:362`). Never reaches the worker. | `dispatch.go:21` |
| `--name` | `-n` | string | `""` | Display name. Sanitized to a dash-free slug (`sanitizeInstanceSlug`, `dispatch.go:447`), embedded in the instance dir name and forwarded to the worker as `--name <slug>` (`dispatch.go:522-524`). | `dispatch.go:22` |
| `--model` | -- | string | `""` | Worker main-loop model; a capability category or versionless vendor name. Overrides the `[global] dispatch_model` host default (`dispatch.go:257-263`). | `dispatch.go:23` |
| `--permission-mode` | -- | string | `""` | Forwarded verbatim to the worker as `--permission-mode <v>` (`dispatch.go:516-518`). | `dispatch.go:24` |
| `--agent` | -- | string | `""` | Forwarded verbatim to the worker as `--agent <v>` (`dispatch.go:519-521`). Note: this is Claude's *subagent* passthrough, not niwa's agent selector -- called out explicitly at `dispatch.go:165-172`. | `dispatch.go:25` |
| `--detach` | `-d` | bool | `false` | Skips the terminal attach at the end of `runDispatch`. Read at exactly one site, `dispatch.go:391`. | `dispatch.go:26` |
| `--parallel` | -- | int | `0` | Max concurrent repo clones during provisioning; `0` defers to `[global] clone_workers`. Assigned to the package global `provisionCloneWorkers` at `dispatch.go:216`. | `dispatch.go:27-28` |
| `--keep-alive` | -- | tri-state bool (`triBoolValue`, `*bool`) | `nil` (unset) | Arms a keep-alive self-wake. Tri-state so it can override the host `keep_alive_on_dispatch` default in both directions; `NoOptDefVal = "true"` makes the bare form mean true (`dispatch.go:29-34`). Prepends a fixed instruction to the prompt when armed (`dispatch.go:325-332`). | `dispatch.go:33-34` |

Plus one inherited persistent flag: `--no-progress` (`internal/cli/root.go:61`).

Two observations that matter for composition:

- **Only `--keep-alive` touches the prompt string.** It prepends
  `keepAliveArmingInstruction` to `prompt` at `dispatch.go:327`, *after* the
  size validation at `dispatch.go:144-146`. A capture path that produces
  `prompt` earlier in the function composes with this unchanged, but the PRD
  should note that the ceiling is checked against the user's text, not the
  final launched string (the comment at `dispatch.go:311-315` argues the fixed
  few-hundred-byte instruction is well inside the margin).
- **Nothing but `dispatchAttach` reads stdin.** `realDispatchLaunch`
  (`internal/cli/dispatch_launcher.go:31-56`) sets `cmd.Dir` and `cmd.Env` but
  never `cmd.Stdin`, so the background worker gets `/dev/null`. The single
  stdin consumer in the whole dispatch path is `dispatchAttach`
  (`dispatch.go:100-110`), which passes `os.Stdin` straight through to
  `claude attach`.

### 2. Widening `Args: cobra.ExactArgs(1)` (`dispatch.go:131`)

Three shapes are available. niwa already has a settled house style here, and it
picks one of them cleanly.

**Option A -- zero args opens the capture (`cobra.MaximumNArgs(1)`).**
This is the established niwa pattern, not a novel one. `niwa destroy` is the
direct precedent: `Args: cobra.MaximumNArgs(1)` at `destroy.go:56`, with the
zero-arg path branching into an interactive picker at `destroy.go:239-258`,
gated on `IsStdinTTY() && tui.IsAvailable()`, and falling back to a listing plus
a clear error when neither holds. Its `Long` text documents the arity table
explicitly (`destroy.go:40-55`), including a dedicated "Non-TTY behavior"
paragraph. Five more commands use `MaximumNArgs(1)` with an optional
positional: `create.go:74`, `apply.go:91`, `init.go:402`, `status.go:74`,
`reset.go:35`, plus `session_attach_register.go:42,66` and
`session_lifecycle_cmd.go:38,81`. `ExactArgs(1)` survives in only two places
(`config_set.go:35`, `source_inspect.go:59`), both genuinely mandatory.
*Ambiguity created:* none at the parse level. The residual risk is a developer
who habitually types `niwa dispatch` expecting a usage error and now lands in a
capture -- mitigated by the capture rendering a visible prompt and having a
clean abandon path (the brief's fourth journey).

**Option B -- a `-` sentinel positional.** `niwa dispatch -` meaning "read the
prompt from stdin". *Ambiguity created:* it forces the feature to be
stdin-shaped rather than terminal-shaped, which collides with the brief's Out
list ("scripted piping as a design driver"). It also has zero precedent in
niwa: a grep for a `"-"` sentinel across `internal/cli/*.go` returns only
instance-name separator logic (`create.go:83-85,179-180`, `dispatch.go:401-421`)
-- never an stdin marker. And `-` is confusable with the existing `-d`
shorthand for a hurried typist. Reject.

**Option C -- an explicit flag (e.g. `--interactive` / `--edit`).** Keeps
`ExactArgs(1)` impossible to satisfy simultaneously, so it would still need to
relax to `MaximumNArgs(1)` -- the flag buys nothing the arity change does not
already provide, and it costs the developer a thing to know. The brief's In
list is explicit that the feature must work with "no mode to choose, and no
decision the developer has to make before they start" (`BRIEF` In, second
bullet). A flag *is* a mode to choose. Reject as the primary path.

One niwa-specific detail worth carrying into the PRD: several commands
deliberately avoid `cobra.ExactArgs` because "its default error exits 1 with a
generic message" and validate arity in `RunE` instead so they can return a
typed exit code (`session_lifecycle_cmd.go:58-61`,
`session_attach_register.go:38-41`). `dispatch` does not need a special exit
code -- `SilenceErrors`/`SilenceUsage` are both true (`dispatch.go:132-133`) and
`Execute()` prints the error and exits 1 (`root.go:95-96`) -- but the pattern
shows arity validation living in `RunE` is normal here.

### 3. Blast radius of the argument-contract change

Materially smaller than it looks, because widening `ExactArgs(1)` to
`MaximumNArgs(1)` is strictly permissive: every existing one-arg invocation
keeps parsing identically. Nothing that passes a prompt today changes behavior.

**`internal/cli/watch.go` does not go through the cobra command at all.** This
is the key finding for scoping. `watch` calls the *launcher function*
directly -- `dispatchLaunch(ctx, instancePath, prompt, passthrough, nil)` at
`watch.go:579` and `watch.go:826` -- and reuses `dispatchNameSuffix` at
`watch.go:771`. A grep of `watch.go` for `runDispatch`, `dispatchCmd`, and every
dispatch flag global (`dispatchDetach`, `dispatchName`, `dispatchModel`,
`dispatchLabel`, `dispatchParallel`, `dispatchKeepAlive`, `dispatchAgent`,
`dispatchPermissionMode`) returns nothing. `watch --once` is therefore
completely insulated from an `Args` change, from a capture path, and from any
TTY gating -- it never had a prompt argument to begin with. That removes the
single largest correctness worry.

**Go unit tests: 37 call sites, all through one helper.** Every test reaches
the command via `runDispatchCmd(t, prompt)` (`dispatch_test.go:168-177`), which
calls `runDispatch(cmd, []string{prompt})` directly with a synthesized
`cobra.Command` -- it bypasses cobra's `Args` validator entirely, so the arity
change is invisible to all 37. Distribution: `dispatch_test.go` (16),
`dispatch_wiring_keepalive_test.go` (11), `dispatch_wiring_remotecontrol_test.go`
(6), `dispatch_model_test.go` (4). There are zero zero-arg or multi-arg call
sites. What *would* need attention is the helper itself: it constructs a bare
`&cobra.Command{}` with no stdin wired, so a capture path reading from
`cmd.InOrStdin()` or `os.Stdin` needs a seam (the codebase already has the
right shape for this -- `IsStdinTTY` is a package variable specifically so
tests can stub it, `prompt.go:26-28`).

Two tests already pin attach behavior and will need to stay green:
`dispatch_test.go:268-269` asserts attach is called exactly once by default, and
`dispatch_test.go:296-297` asserts `--detach` skips it.

**Functional tests: 9 scenarios, every one passes a positional prompt AND
`--detach`.** `test/functional/features/dispatch.feature:36,55,73,92,115,144`,
`keep-alive.feature:40,64`, and `codex-agent.feature:56`. All are of the form
`When I run "niwa dispatch <prompt> --detach" from the workspace root`. Because
they run the real binary non-interactively, they are the population most
exposed to a TTY-gating regression -- but they all supply a prompt, so under
`MaximumNArgs(1)` they never enter the capture path. They also all pass
`--detach` already, which is a useful datum for question 4: the entire existing
non-interactive test corpus is detached.

**`test/live/dispatch_live_test.go`** documents the manual recipe as
`niwa dispatch <prompt> --detach` (`dispatch_live_test.go:101`) -- prose, not a
parsed contract.

**Scripts and hooks: none.** A grep for `niwa dispatch` across `*.sh`, `*.json`,
`*.yml`, `*.yaml` in the repo returns nothing.

**Agent-facing callers (documented, both positional):**
- `internal/workspace/rootskills/dispatch/SKILL.md:86` -- the `/dispatch` skill's
  canonical invocation, `niwa dispatch "Read <path> ... " --name <slug>`, and
  `SKILL.md:102` for the powerful-model variant.
- `internal/workspace/root_materializer.go:415-418` -- the generated workspace
  CLAUDE.md, which tells every root-level agent the command is
  `niwa dispatch "<task>" --name <slug> [--detach]`.

Both keep working unchanged. Neither needs to learn the capture, and the brief
is explicit that agents "already have a better path" (Out list, scripted-piping
bullet).

**No shell completion to update.** `dispatchCmd` declares no `ValidArgsFunction`
(unlike `session_lifecycle_cmd.go:39,80`), so nothing in `completion.go` knows
about dispatch's positional.

### 4. The `--detach` question

**What `--detach` actually does.** Exactly one thing, at exactly one place. At
`dispatch.go:391-396`, after the mapping is durable and the hints are printed,
`if !dispatchDetach` calls `dispatchAttach(shortID)`, which runs
`claude attach <shortID>` with `os.Stdin`/`os.Stdout`/`os.Stderr` inherited
(`dispatch.go:100-110`). An attach failure is non-fatal and degrades to a
warning (`dispatch.go:392-395`). `--detach` does not change provisioning,
launching, capture, or the mapping. It is purely "does this process hand the
terminal to the new session at the end."

**The case that capture + `--detach` should be rejected.** The brief's In list
commits to "the existing attach behavior, preserved" and frames the present
workaround's forced detachment as part of the problem ("the developer who most
wants to hand off a failure and watch the worker pick it up is exactly the
developer the workaround drops back at a shell prompt", BRIEF Problem
Statement). If the *point* of the feature is that capture and attach finally
coexist, then `--detach` on a capture looks like the developer re-creating the
workaround by hand. Rejecting it is one fewer exit shape to specify and test.

**The case that they compose (stronger).** Three reasons.

First, they are orthogonal in the code. Capture produces `prompt` at the top of
`runDispatch`; `--detach` is consumed 250 lines later at
`dispatch.go:391`. There is no shared state, no ordering constraint, and no
resource contention -- `realDispatchLaunch` never touches stdin
(`dispatch_launcher.go:31-56`), so a capture that reads the terminal leaves
stdin fully available for the subsequent attach. The composition is free; a
rejection would be code deliberately added to forbid something that works.

Second, the fan-out case is real and named in the command's own help text:
`--detach` is described as "the mode for fan-out and scripting"
(`dispatch.go:121-122`). A developer dispatching three workers at a failure, one
after another, wants to paste each one and *not* be attached to the first. That
is a coherent user-facing mode: "capture a prompt interactively, then do not
attach." It is exactly the interactive-input/non-interactive-output quadrant
that `git commit -m` vs `git commit` does not cover but that, say, `docker run
-i -d` does.

Third, rejecting it costs the developer a rule to learn for no protection.
Nothing bad happens if both are set. The error message would have to explain a
restriction whose only justification is stylistic.

**Recommendation: they compose.** `--detach` continues to mean exactly what it
means today -- skip the final attach -- regardless of how the prompt arrived.
The feature adds no new flag interaction and no new rejected combination.

**One caveat the PRD should record.** The brief asserts that in the
`niwa dispatch -d "$(cat)"` workaround "the terminal attaches to the new session
by default, but that path is closed once stdin has been spent feeding the
prompt" (BRIEF Problem Statement). Nothing in niwa's code closes stdin --
`dispatchAttach` hands `os.Stdin` through unmodified (`dispatch.go:106`), and
the launcher never reads it. Whatever makes the workaround's attach unpleasant
is terminal- or shell-state, not a niwa code path. This does not change the
brief's conclusion (the workaround is still bad, and `-d` is still what people
type), but the PRD should not carry "stdin is spent" forward as a mechanism
claim, because it would misdirect the DESIGN into thinking it must recover a
consumed fd. It does not.

### 5. Positional prompt supplied alongside the capture

Under the recommended `MaximumNArgs(1)`, this is not a conflict at all -- the
arity itself decides. One arg means the prompt is the arg and no capture opens.
Zero args means the capture opens. There is no state in which both a positional
prompt and a capture are requested, so there is nothing to error on and nothing
to arbitrate.

This is the strongest argument for Option A over Option C. An explicit
`--interactive` flag would create exactly this problem --
`niwa dispatch --interactive "some prompt"` is genuinely ambiguous and would
need a rejection rule, an error string, and a test. The arity-based contract
never constructs the ambiguous state.

If a later revision does add an explicit opener flag, the least surprising rule
is *reject with a clear error*, not "the argument wins": silently ignoring an
explicitly requested capture is the surprising half of that pair, and niwa
already has precedent for rejecting a valid-looking argument in the wrong
context rather than ignoring it (`destroy.go:80-83`, where an instance name
passed from inside an instance errors with a specific remedy instead of being
dropped).

### 6. Documentation a contract change would obsolete

None of these are *wrong* after a permissive arity change, but each states or
implies the one-argument shape and should be reviewed:

- `README.md:130` -- the command table row, currently
  `niwa dispatch "<task>" [--name <slug>] [--model <model>] [--detach]`. The
  most visible statement of the surface; needs the zero-arg form added.
- `internal/cli/dispatch.go:113` (`Use: "dispatch <prompt>"`) and
  `dispatch.go:115-130` (`Long`). Both assert a mandatory prompt. `destroy.go:40-55`
  is the model for what the rewritten `Long` should look like -- an explicit
  arity table plus a "Non-TTY behavior" paragraph naming the escape hatch.
- `internal/workspace/root_materializer.go:415-418` -- the generated workspace
  CLAUDE.md text handed to every root agent. Changing it changes what agents in
  *new* workspaces are told. Since agents should keep using the positional form,
  the safest move is to leave this alone; flagged so the decision is deliberate
  rather than accidental.
- `internal/workspace/rootskills/dispatch/SKILL.md:83-102` -- the `/dispatch`
  skill. Same reasoning: it should keep passing a positional prompt, but a
  reviewer will ask.
- `docs/guides/session-keep-alive.md:18` -- documents
  `niwa dispatch --keep-alive <prompt>`, i.e. the flag-before-positional order.
  Still valid; worth confirming the capture wording does not contradict it.
- `docs/guides/remote-control-on-dispatch.md` and
  `docs/guides/workspace-config-sources.md:664-678` -- both discuss dispatch
  scoping, neither pins the argument shape. Likely untouched.
- `docs/prds/PRD-instance-dispatch.md` -- the upstream requirements contract.
  **R2** (`PRD:97-98`) says the command "SHALL accept the worker's task prompt as
  input", deliberately not "as a positional argument", so a capture path does
  not contradict it. **R43** (`PRD:278-282`) pins argv-only *delivery to the
  worker*, empty-prompt rejection, and clear failure at the argument-length
  limit -- all of which the capture path preserves, since capture changes only
  how `prompt` is obtained, not how it is handed to `claude --bg`. The
  Decisions section (`PRD:445-449`) is the one place that reads as a closed
  door: "A file/stdin prompt channel is not available from the
  background-launch surface and is out of scope." That sentence is about the
  channel *to the worker*, not about how niwa collects text from the developer,
  but it is close enough that the new PRD should say so explicitly rather than
  leave a reader to reconcile them.

## Recommendation

**Argument contract: relax `dispatch.go:131` to `cobra.MaximumNArgs(1)`.** One
positional argument keeps its exact current meaning. Zero arguments opens the
interactive capture. This is niwa's established idiom for an optional
positional with an interactive fallback -- `niwa destroy` is the direct
precedent (`destroy.go:56`, `destroy.go:239-258`), and seven other commands use
the same arity. Reject the `-` sentinel (no precedent anywhere in niwa, pulls
the feature toward the stdin-piping shape the brief rules out, confusable with
`-d`) and reject an explicit opener flag (it violates the brief's
no-mode-to-choose commitment and manufactures the ambiguity that arity-based
dispatch avoids). The change is strictly permissive: no existing invocation,
test, doc, or agent caller changes behavior.

**Detach: they compose. No new rejected flag combination.** `--detach` retains
its single meaning -- skip the final `dispatchAttach` call at
`dispatch.go:391` -- independent of how the prompt was obtained. Capture and
attach are orthogonal in the code (capture produces `prompt` at the top of
`runDispatch`; the launcher never touches stdin, `dispatch_launcher.go:31-56`),
"paste a prompt, then fan out without attaching" is a coherent mode the
command's own help already names (`dispatch.go:121-122`), and forbidding it
would be code added to prevent something harmless. The PRD gets two exit shapes
to specify, which is the correct outcome: they are the same two exit shapes the
command already has.

**Positional supplied alongside the capture: cannot happen, by construction.**
Arity is the selector, so no state exists in which both are requested. Nothing
to error on. This property is a reason to prefer the arity contract, not an
accident of it. (Should a future explicit opener flag arrive, reject the
combination rather than letting the argument silently win.)

**Blast radius is small and well-bounded.** `watch --once` is unaffected -- it
calls `dispatchLaunch` directly (`watch.go:579`, `watch.go:826`) and touches no
dispatch flag global. All 37 Go unit-test call sites route through
`runDispatchCmd` (`dispatch_test.go:168-177`), which invokes `runDispatch`
directly and never exercises cobra's `Args` validator. All 9 functional
scenarios pass both a positional prompt and `--detach`, so none enter the
capture path. No scripts or hooks invoke the command. The real work is
documentation (`README.md:130`, the `Use` and `Long` strings at
`dispatch.go:113-130`) and giving the test helper a stdin seam.

## Open Questions

- **Should the generated workspace CLAUDE.md and the `/dispatch` skill mention
  the capture at all?** (`root_materializer.go:415-418`,
  `rootskills/dispatch/SKILL.md:83-102`.) Both instruct agents, and agents
  should keep passing a positional prompt. Recommendation is to leave both
  unchanged, but it is a product call about what the workspace tells its agents.
- **Does the size ceiling apply before or after the keep-alive instruction is
  prepended?** Today the check is at `dispatch.go:144-146` and the prepend at
  `dispatch.go:327`, so the ceiling governs the developer's text only. A capture
  that reports the ceiling live (BRIEF's third journey) has to pick a number to
  show, and whether that number accounts for the keep-alive prefix is a human
  call. This is `prd-size-ceiling`'s territory, flagged here because the flag
  interaction is what creates it.
- **Whether the PRD should explicitly supersede the "file/stdin prompt channel
  ... out of scope" sentence in the upstream PRD** (`PRD-instance-dispatch.md:448-449`).
  It is about the worker channel, not the developer's input, but leaving the
  tension unaddressed invites a reviewer objection.

## Summary

Every `niwa dispatch` flag is declared in one `init()` block at
`internal/cli/dispatch.go:20-36` -- `--label`, `--name/-n`, `--model`,
`--permission-mode`, `--agent`, `--detach/-d`, `--parallel`, and the tri-state
`--keep-alive` -- and none of them contend with an interactive capture:
`--keep-alive` is the only one that touches the prompt string, and it does so
after validation. The argument contract should relax from `ExactArgs(1)`
(`dispatch.go:131`) to `MaximumNArgs(1)`, matching `niwa destroy`'s
optional-positional-with-interactive-fallback idiom (`destroy.go:56,239-258`)
and seven other niwa commands; this makes "a positional prompt supplied
alongside the capture" impossible by construction rather than an error case to
specify, and it is strictly permissive -- `watch --once` calls `dispatchLaunch`
directly and is untouched (`watch.go:579,826`), all 37 unit-test call sites
bypass cobra's validator via `runDispatchCmd` (`dispatch_test.go:168-177`), and
all 9 functional scenarios already pass both a prompt and `--detach`. Capture
and `--detach` compose: the flag is read at exactly one site
(`dispatch.go:391`) to skip a final attach that inherits stdin
(`dispatch.go:100-110`), the launcher never reads stdin at all
(`dispatch_launcher.go:31-56`), and "paste a prompt, then fan out without
attaching" is a coherent mode the command's own help text already names -- so
the PRD specifies two exit shapes, which are the two the command already has.
