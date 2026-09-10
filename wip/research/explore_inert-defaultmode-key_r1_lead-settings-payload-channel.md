# Lead: The inline --settings channel and the single-slot constraint

Investigated in the worktree `docs+inert-defaultmode-key` at niwa `5232825`
(HEAD), against the Claude Code binary actually on PATH, **2.1.267**
(`a9e1808c8204`, linux-x64).

## Findings

### 1. The current payload

**What it is.** One package-level string, built once at init, never
parameterized:

`internal/cli/dispatch_remotecontrol.go:16`

```go
var remoteControlSettingsJSON = fmt.Sprintf("{%q:true}", config.RemoteControlAtStartupKey)
```

which evaluates to the literal `{"remoteControlAtStartup":true}`. There is no
`map[string]any`, no `encoding/json` marshal, no struct, and no builder. The
document is a `fmt.Sprintf` template with exactly one hole, and the hole is
filled by a compile-time constant (`config.RemoteControlAtStartupKey =
"remoteControlAtStartup"`, `internal/config/config.go:449`), not by input. The
doc comment says so explicitly (`dispatch_remotecontrol.go:10-15`):

> It is built from `config.RemoteControlAtStartupKey` -- never from user input
> -- so the injected flag, the materializer, and the read-back share one
> spelling. It is appended to the dispatch argv as a single discrete element.

**How it is passed.** Inline JSON string, not a temp file. Two discrete argv
elements appended to the passthrough slice:

`internal/cli/dispatch.go:598`

```go
passthrough = append(passthrough, spec.Flags.Settings, remoteControlSettingsJSON)
```

`spec.Flags.Settings` is `"--settings"` from the static `launchSpecs` table
(`internal/agentplan/dispatch.go:357`). Nothing writes a settings file for the
flag; niwa has never used the `--settings <file>` form even though the CLI
accepts it (`claude --help`: `--settings <file-or-json>  Path to a settings
JSON file or a JSON string to load additional settings from`).

**When it is passed.** Gated at `dispatch.go:585-602`, step (9c). Three
conditions, all required: the `RemoteControl` capability row is
`StateImplemented` for the dispatched agent AND that agent declares a
`Flags.Settings` spelling (`rcDeliverable`), the host global config loaded, and
`resolveDispatchRemoteControl` returns `inject == true`. The resolver
(`dispatch_remotecontrol.go:37-50`) is a default-fill: it injects only when the
host `[global].remote_control_on_dispatch` is on AND the instance's
materialized `remoteControlAtStartup` is unset AND `ANTHROPIC_API_KEY` is
absent. So on a typical dispatch the flag is often not present at all.

**What it contains today.** Exactly one key. Nothing else has ever been added
to it.

### 2. The single-slot constraint — verified, and now measured

**Is `--settings` genuinely occupied by one producer?** Yes. `dispatch.go:598`
is the only site in the repo that emits `--settings` on any argv. Every other
Claude argv is built through the same funnel:

- `internal/cli/dispatch_launcher.go:364` `buildLaunchArgs` is the single argv
  assembler for launches: `LeadingArgs` + `DetachedArgs` (detached only) +
  `formatWorkdirGrant` + `passthrough` + optional `--` + the prompt.
- `internal/cli/dispatch.go:974` `buildDispatchPassthrough` is the only builder
  of the flag pairs, and it emits exactly four: Model, PermissionMode,
  SubagentType, DisplayName.
- Every other `exec.Command` against `claude` is a non-session verb:
  `claude plugin ...` (`dispatch_plugins.go:171`), `claude stop <id>`
  (`watch.go:480`), `claude --resume <id>` (`sessionattach/supervise.go:44`),
  `claude --version` (`workspace/harness_compat.go:37`), and the reentry attach
  at `dispatch.go:186`. None carries `--settings`.

**The two recorded declines.** Both are real and both are in `docs/designs/current/`.

**Decline 1 — keep-alive, `ef24ee8ab295b46b0840ef18cb54b635f9cb30b4`
(feat(dispatch): opt-in keep-alive for remote-control sessions, #209).**
`docs/designs/current/DESIGN-niwa-session-keep-alive.md:141-146`, verbatim:

> **B-note — no second `--settings`.** RC is injected as a discrete `--settings
> <json>` argv pair and niwa never merges settings JSON. A second `--settings`
> for keep-alive would be passed verbatim alongside RC's, and the CLI's
> repeated-`--settings` behavior is undocumented (likely last-wins), so it could
> clobber `remoteControlAtStartup` and break the very bridge keep-alive
> protects. Since arming is a text nudge (B1/B2), not a setting, keep-alive
> avoids the `--settings` channel entirely and this collision does not arise.

That decline is squarely **about undocumented repeated-flag behavior**, with
"likely last-wins" as an explicit guess, and secondarily about the absence of a
merge step ("niwa never merges settings JSON").

**Decline 2 — watch PR review, `0981df7bc2139e0e126a298bc8e8bb12b7a6ab81`
(feat(watch): add niwa watch --once contained PR-review dispatch, #193).**
`docs/designs/current/DESIGN-niwa-watch-once-pr-review.md:167-173`, verbatim:

> - **Option 1B (rejected): pass the profile via the `--settings <json>`
>   flag** to `claude --bg`. Rejected because dispatch already injects
>   `--settings` for remote control, so a second producer would collide on
>   that single flag; settings that must persist for the instance's life
>   belong in the instance's settings file, which is the documented merge
>   point, not a launch-time flag.

That decline has **two** reasons, and only the first is the slot: "a second
producer would collide on that single flag". The second is a durability
argument — the sandbox stanza must survive for the instance's life, and the
file is "the documented merge point". A per-launch signal (which
`permissions.defaultMode`-as-dispatch-intent is) does not attract that second
objection.

The same design also records an unresolved worry that niwa's own `--settings`
injection might *relax* the sandbox
(`DESIGN-niwa-watch-once-pr-review.md:422-429`, and again at `:858-864`):

> niwa must both re-read the merged instance file to confirm the sandbox stanza
> survived *and* ensure its own `--settings` injection does not relax
> `sandbox.*`. Where niwa cannot observe the harness's final merge, it must not
> silently assume the stanza wins.

**A third, silent occasion.** `docs/designs/current/DESIGN-dispatch-permission-mode.md`
(`d25ad4defdb8204418e59e51afbb991f401732c2`, fix: dispatch-permission-mode,
#277) — the design that created the very read-back this exploration is about —
never considers `--settings` at all. Its three options (A/B/C, lines 152-198)
are all about *where in `runDispatch` the derivation lives*; the channel
(`--permission-mode`) is treated as settled upstream in the frontmatter. So the
`--settings` route for the permission signal has never actually been weighed on
its merits.

**What I measured, since both declines rested on a guess.** Claude Code 2.1.267,
four probes:

| Probe | argv | Result |
|---|---|---|
| Repeated flag, bad-first | `--settings 'BAD_FIRST' --settings '{"env":{"X":"1"}}' doctor` | starts clean — the bad first value is never read |
| Repeated flag, bad-second | `--settings '{"env":{"X":"1"}}' --settings 'BAD_SECOND' doctor` | `Error: Settings file not found: BAD_SECOND` |
| Repeated flag, semantic | `--settings '{"permissions":{"defaultMode":"bypassPermissions"}}' --settings '{"env":{"NIWA_PROBE":"1"}}' -p --permission-prompts none '<touch a file>'` | command **denied** — identical to the no-settings control |
| Control | no `--settings`, same prompt | denied |

**Repeated `--settings` is last-wins, total replacement, silent.** The earlier
occurrence is discarded whole — not merged, not warned about, not even
validated. The design docs' "likely last-wins" is now a measured fact, and the
clobber they feared is exactly what happens. The constraint the exploration was
given is **real and stronger than recorded**: it is not merely undocumented, it
is confirmed-destructive.

### 3. Is a merged builder already half-built?

Yes — three of them, none on the `--settings` path.

**(a) `buildSettingsDoc` — the real multi-contributor builder.**
`internal/workspace/materialize.go:669` builds a `map[string]any` that several
independent concerns write into:

- `permissions.defaultMode` from `cfg.Settings["permissions"]` via
  `permissionsMapping` (`materialize.go:679-690`)
- `permissions.deny` from the worktree-delegation fallback, merged into the
  *same* map, with a comment (`materialize.go:684-687`) that reads like the
  charter for a merged builder:

  > The permissions block may carry two independent keys: defaultMode (from the
  > user's settings) and deny (from the worktree-delegation fallback). They are
  > emitted into the SAME permissions map so a deny fallback never clobbers a
  > configured defaultMode and vice versa.

- `remoteControlAtStartup` (`:712-723`), `keepAliveOnDispatch` (`:725-740`),
  hooks, env, `enabledPlugins`, `extraKnownMarketplaces`,
  `includeGitInstructions`.

Callers then mutate the returned doc further —
`internal/workspace/root_materializer.go:275` adds
`doc["ephemeralSessionMode"] = ephemeral` after the builder returns.

**(b) `agentplan.SettingsPlan` — the producer/serializer seam.**
`internal/agentplan/settings.go:113`. It already owns marshalling, indentation,
trailing newline, file mode (`0o600`), directory name, scope→filename, and
managed-file tagging. Its header comment (`settings.go:9-20`) describes exactly
the consolidation this lead is asking about, for the file channel:

> until now each caller carried its own marshal-then-mkdir-then-write copy of
> the same six lines, which is three places for the indentation, the trailing
> newline, the file mode, or the directory name to drift apart in. ... internal/
> workspace still decides what the document says ... and this file decides where
> it goes, what bytes it becomes, and what permission it is created with.

Note the tag it stamps on every entry (`settings.go:107-111`):

> The entries are tagged `ApprovalPosture`. The document carries several
> capabilities at once -- hooks, session environment, plugin skills -- but each
> of those has its own delivery or its own entries elsewhere, **while the
> permission posture has no vehicle other than this file.**

That last clause is now false. It was already false when `#277` shipped
`--permission-mode`, and it is doubly false given finding 5 below. It is a
comment that has quietly become a wrong claim inside the capability contract.

**(c) `watch.ApplyReviewSettings` — a read-merge-write-verify merger.**
`internal/watch/containment.go:201-266`. It reads the instance's
`.claude/settings.json`, merges in a fully-owned `sandbox` stanza, conditionally
owns `permissions.defaultMode = "default"` while preserving other permissions
keys (`:216-225`), appends up to four PreToolUse hooks deduped by matcher, writes,
re-reads from disk, and re-verifies via `VerifyReviewSettings`. This is the
closest thing in the repo to a merged settings-document builder with more than
one contributor, and it even has the "fully owned vs. merged" distinction a real
builder needs.

**Natural home for a `--settings` builder, and the seams.** The obvious home is
a `dispatchSettingsDoc` (or `agentplan.InlineSettings`) sitting next to
`buildDispatchPassthrough` in `internal/cli`, returning `map[string]any` and
serialized once. The existing seams:

- `dispatch.go:552` — step (9a-derive) already reads `inst` once, before the
  passthrough build, precisely so a settings-derived value can reach the argv.
  That ordering was the whole point of `#277`'s Option A, and it is exactly
  where an inline-settings contributor would hook in.
- `dispatch.go:598` — the single append site; it would become
  `append(pass, spec.Flags.Settings, doc.JSON())`.
- `agentplan.LaunchFlags.Settings` (`internal/agentplan/dispatch.go:250-251`,
  "Settings hands the worker a settings document inline") is already the
  per-agent gate; Codex declares it empty deliberately, so a builder inherits
  the drop-rather-than-guess behavior for free.
- `internal/agentplan/settings.go` already owns "turn a doc into bytes" for the
  file channel; an inline variant is a sibling function, not a new subsystem.

### 4. Argv construction generally

There is **one** owner, with a two-stage split, and it is clean.

```
buildLaunchArgs (dispatch_launcher.go:364)
  = spec.LeadingArgs            ["--bg"]           static, per-agent
  + spec.DetachedArgs           (detached only)    static, per-agent (empty for Claude)
  + formatWorkdirGrant(...)     (empty for Claude; Codex's -c projects={...})
  + passthrough                                    caller-built
  + "--" if spec.PromptSeparator (false for Claude)
  + prompt                                         one argv element, always
```

`passthrough` has exactly three contributors, all in `internal/cli`:

1. `buildDispatchPassthrough` (`dispatch.go:974-992`) — a pure function that
   emits `{Model, PermissionMode, SubagentType, DisplayName}` pairs, skipping any
   whose flag spelling or value is empty. It reads the package-level
   `dispatchPermissionMode` var (`dispatch.go:47`), which is set by the
   `--permission-mode` flag (`dispatch.go:28`) and, since `#277`, by the
   derivation at `dispatch.go:551-557`.
2. Step (9c) at `dispatch.go:598` — the `--settings` pair.
3. `internal/cli/watch.go:576,838` — appends `--strict-mcp-config` when
   sandboxed and `--resume <SessionID>` on a continuation.

`--append-system-prompt` is **not used anywhere**. niwa's niwa-authored prompt
text rides the prompt argv element itself as `launchRequest.Prefix`
(`dispatch_launcher.go:38-41,106`), kept separate from the developer's `Body`
only so the spill can move the body to a file. Plugin flags (`--plugin-dir`,
`--setting-sources`, `--add-dir`) are not used either; plugins reach the worker
through the materialized `settings.json` `enabledPlugins`/
`extraKnownMarketplaces` and a pre-warm pass (`dispatch_plugins.go`).

`buildDispatchPassthrough`'s purity is defended in writing.
`DESIGN-dispatch-permission-mode.md:184-190` rejected Option C for exactly this:

> violates DD6 directly -- `buildDispatchPassthrough`'s own doc comment states
> it "stays a pure argv builder" by design (model resolution already happens in
> the caller for this reason). Giving it a file read and an agent-vocabulary
> branch turns a pure function into one with I/O and policy, which the existing
> code structure deliberately avoids.

So: one owner for argv shape (`buildLaunchArgs`), one owner for the flag pairs
(`buildDispatchPassthrough`), and one out-of-band append for `--settings`. The
`--settings` append is the only thing in the whole argv path that is not routed
through a builder — it is a bare `append` in the middle of `runDispatch`.

### 5. The verdict — and the measurement that changes it

**Moving `permissions.defaultMode` into the `--settings` payload is not merely
feasible; it works today, and the file channel it would replace does not.**

Measured on Claude Code 2.1.267, `--print --permission-prompts none`, asking the
model to run one `touch`:

| # | Channel carrying `defaultMode: "bypassPermissions"` | Bash ran? |
|---|---|---|
| A | project `.claude/settings.json` in cwd | **NO** (denied) |
| B | `--settings '{"permissions":{"defaultMode":"bypassPermissions"}}'` | **YES** |
| C | nothing (control) | NO (denied) |

A is the exploration's premise, confirmed live: the key niwa's materializer
writes is inert. B is the new fact: **the same key in the `--settings` payload
is honored**, and the harness's own help text says why — `--restricted`
"ignores user, project and local settings files (**managed settings and
`--settings` still apply**)". `--settings` sits in the trusted tier alongside
managed settings, not in the project tier that 2.1.258 stopped trusting. A
corroborating startup-time probe: `claude --restricted --settings
'{"permissions":{"defaultMode":"bypassPermissions"}}' -p hi` produces the same
`Error: bypassPermissions not supported in restricted mode` as
`claude --restricted --permission-mode bypassPermissions`, i.e. the settings
document's value reaches the same permission-mode resolution the flag does.

So there are now **two** live channels for the dispatch permission signal
(`--permission-mode`, shipped in `#277`; and `--settings`, unshipped and
unconsidered), and one dead one (the project settings file, which niwa still
writes).

**Does this strengthen or weaken the case for a merged settings-document
builder?** It **strengthens** it, but not for the reason the lead was framed
around, and the strengthening is conditional.

The honest picture:

- **For the permission signal specifically, a merged builder is not needed.**
  `--permission-mode` already ships, is a closed enum, is per-agent-vocabulary
  aware, and costs nothing on the `--settings` slot. Moving the signal into
  `--settings` would trade a free argv slot for a contested one and would gain
  nothing the flag does not already deliver. **Recommendation: leave the
  permission signal on `--permission-mode`.** The smallest change that stops the
  settings document making a claim it cannot keep is about what the materializer
  *writes*, not about which flag delivers it.
- **But the constraint that made a merged builder look necessary is now
  measured to be worse than believed, and that raises the value of building one
  before the next producer arrives.** Repeated `--settings` silently discards
  the earlier document. Today that is survivable because there is exactly one
  producer and it appends last. The moment a second producer appends *after*
  step (9c) — a watch continuation, a future sandbox stanza, anything — remote
  control dies silently, with no error, no warning, and no test that would
  catch it unless someone happens to assert the whole argv. There is no
  guard today: nothing asserts `--settings` appears at most once.
- **Three producers have now bounced off this slot** (keep-alive, watch, and —
  silently — dispatch-permission-mode). Two of the three found another channel;
  the third never looked. That is a pattern, not a coincidence, and it is what a
  builder is for.

**Concrete, minimal recommendation for the exploration:** the merged builder is
worth *one small piece of itself*, not the whole thing. Replace the bare
`append` at `dispatch.go:598` with a single-owner function that returns the
inline settings document (a `map[string]any` marshalled once) and appends the
pair at most once — plus a test that asserts `--settings` occurs exactly zero or
one times in the final argv. That is roughly twenty lines, it makes the
single-slot constraint enforced rather than merely observed, and it turns the
next producer's arrival from a silent breakage into a merge decision someone has
to make on purpose. Anything larger (a fragment registry, an ordered contributor
list, a merge-semantics policy) is speculative until a second producer actually
exists.

**And an independent finding this lead owes the exploration:** the comment at
`internal/agentplan/settings.go:110-111` — "while the permission posture has no
vehicle other than this file" — is factually wrong post-`#277` and post-2.1.258,
and it sits inside the capability contract that decides how postures are
delivered. Whatever the exploration lands on, that sentence needs correcting;
it is the clearest single artifact of the settings document still claiming
authority it lost.

## Implications

1. **The sibling exploration's constraint holds and is harder than recorded.**
   Do not plan on a second `--settings`. Last-wins is now measured, not guessed.
2. **The `--settings` channel is a *working* carrier for `permissions.defaultMode`.**
   That is new information; neither prior decline knew it, and the design that
   chose `--permission-mode` never evaluated it. If any future work needs a
   permission-shaped signal that `--permission-mode`'s closed enum cannot
   express (a `deny` list, an `allow` list, `additionalDirectories`), the
   settings payload is the only channel that carries it — and it will be
   contended.
3. **The smallest honest change is on the write side.** The materializer should
   stop writing a `permissions.defaultMode` that Claude Code will ignore, or
   should write it somewhere that reads unambiguously as niwa's own signal (the
   `keepAliveOnDispatch` precedent — a niwa-defined key in the same file,
   documented as niwa-only — is the shipped pattern for exactly this and is one
   line of materializer change plus one struct field).
4. **`buildSettingsDoc` is the file-channel builder and it is already merged.**
   Any "merged builder" conversation should be careful to say *which* document:
   the file has a builder; the inline payload has a `fmt.Sprintf`.

## Surprises

1. **Last-wins is total and silent.** `--settings 'GARBAGE' --settings '{}'`
   starts cleanly. The garbage is never even parsed. A clobbering second
   producer would leave no trace anywhere.
2. **`--settings` honors a permissive `permissions.defaultMode` when the project
   settings file does not.** The channel niwa has occupied since
   `b36491503e6dd14cbf038b502d86ede6cafcd934` for one boolean turns out to be
   the *only* document channel that still carries the posture — and niwa put a
   `--permission-mode` flag next to it instead, without noticing.
3. **Claude Code 2.1.267 has a first-class `--remote-control [name]` flag.**
   Its help says "Start an interactive session with Remote Control enabled
   (optionally named)", so it may not compose with `--bg`. If it does, niwa's
   sole occupant of the `--settings` slot could vacate it entirely, and the
   single-slot constraint dissolves. Worth ten minutes of measurement before
   anyone designs around scarcity.
4. **The dispatch-permission-mode design never considered `--settings`.** Its
   entire options section is about *where in the function* the derivation goes.
   The channel choice was inherited, not made.
5. **`internal/agentplan/settings.go:110-111` asserts the permission posture has
   no vehicle other than the settings file.** That was already untrue when it
   was written into the capability contract.
6. **`watch` writes `permissions.defaultMode = "default"` into the instance file**
   (`containment.go:222`) as the whole mechanism of its operator-approval
   posture, and re-verifies it survived (`VerifyReviewSettings`). If project-scope
   `defaultMode` is inert for *permissive* values only, that still works; if
   2.1.258's change is broader, the ask posture is quietly broken too. Not
   tested here — flagged.

## Open Questions

1. **Does `--remote-control` compose with `--bg`?** If yes, the entire
   single-slot problem is optional. One command answers it.
2. **Is project-scope `defaultMode` inert for *all* values or only permissive
   ones?** Probe A only covers `bypassPermissions`. `watch`'s ask posture
   depends on `"default"` being honored from the file; if it is not, that is a
   separate live bug in `internal/watch/containment.go`.
3. **Does a `--settings` document *merge* with the project file, or replace the
   layer?** The help says "load additional settings from", implying merge, and
   probe B did not test whether the project file's other keys survived alongside
   it. A builder that assumes merge and gets replacement would be a new class of
   bug.
4. **Precedence of `--settings` vs `--permission-mode` when both carry a mode.**
   Untested. If someone ever emits both, one silently wins.
5. **Should the `--settings` slot get a guard now?** A test asserting
   `--settings` appears at most once in the assembled argv costs almost nothing
   and converts the constraint from tribal knowledge into a failing build. My
   recommendation is yes, regardless of what else the exploration decides.

## Summary

niwa's `--settings` payload is one hardcoded inline JSON string,
`{"remoteControlAtStartup":true}` built by `fmt.Sprintf` at
`internal/cli/dispatch_remotecontrol.go:16` and appended as two discrete argv
elements at `internal/cli/dispatch.go:598` — the only `--settings` producer in
the repo, and the only part of the argv path not routed through a builder. The
single-slot constraint is real and worse than the two recorded declines
(`ef24ee8` keep-alive's "the CLI's repeated-`--settings` behavior is
undocumented (likely last-wins)", `0981df7` watch's "a second producer would
collide on that single flag"): I measured Claude Code 2.1.267 and repeated
`--settings` is last-wins, total replacement, silent, discarding the earlier
document without even parsing it. The verdict is that moving
`permissions.defaultMode` into the payload is fully feasible — measurably so,
since `--settings` honors a permissive `defaultMode` where the project settings
file no longer does — but it is not worth spending the contested slot when
`--permission-mode` already ships that signal for free; the case for a merged
builder is strengthened only to the extent of one small guard at `dispatch.go:598`
(single-owner append plus a test that `--settings` occurs at most once), and the
real minimal fix belongs on the materializer's write side.
