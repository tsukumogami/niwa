# Lead: What else in niwa depends on the dispatched session's name being exactly the user-supplied slug?

Scope: worktree `.claude/worktrees/session-name-collision`, branch `docs/session-name-collision`.
Every line number below was read from source in this worktree, not carried over from a prior pass.

## Findings

### 0. The whole fan-out from `--name` is two lines

`dispatchName` is read in exactly one place in the non-test tree
(`internal/cli/dispatch.go:458`), and the derived slug is consumed in exactly two:

```
internal/cli/dispatch.go:458   slug := sanitizeInstanceSlug(dispatchName)
internal/cli/dispatch.go:459   namePrefix, err := dispatchNameSuffix(slug)     // -> INSTANCE DIRECTORY NAME
internal/cli/dispatch.go:564   passthrough := buildDispatchPassthrough(spec.Flags, slug, resolvedModel)  // -> SESSION DISPLAY NAME
```

`buildDispatchPassthrough` (`internal/cli/dispatch.go:974-995`) emits the pair
`{flags.DisplayName, slug}` at `:985`, and only when both halves are non-empty. The flag
declaration is `internal/cli/dispatch.go:26`. A repo-wide grep for `dispatchName` outside
`_test.go` returns nothing else.

So the question "what else depends on the name" reduces to: what reads back either (a) the
instance directory name, or (b) the string niwa handed the agent as `--name`. Nothing in
niwa reads (b) back at all — see §7.

### 1. Instance directory name — the load-bearing consumer, and NOT the display name

`dispatchNameSuffix` (`internal/cli/dispatch.go:879-889`) returns `slug + "-" + <8 hex>`
(or bare `-<8hex>` with no slug); `provisionInstanceFunc` joins it to the config name with
`"+"` (`internal/cli/dispatch.go:478`, separator const at `:469`). The resulting name is
matched structurally by

```
internal/cli/dispatch.go:941   var dispatchInstanceNameRe = regexp.MustCompile(`\+[a-z0-9_]*-[0-9a-f]{8}$`)
internal/cli/dispatch.go:958   func isDispatchInstanceName(name string) bool
```

The invariants this predicate rests on are spelled out at `internal/cli/dispatch.go:943-957`
and in `sanitizeInstanceSlug`'s doc comment (`:891-909`): slugs are dash-free, and
`create --name` appends no `-<8hex>` tail.

**Verdict: a display-name-only suffix changes nothing here**, provided the suffix is added
after `dispatchNameSuffix` has already consumed the slug. If the suffix were instead folded
into `sanitizeInstanceSlug`, three things break at once: the directory name would carry a
second random token, `niwa create --name` would start producing random instance names
(`internal/cli/create.go:115` calls the same function), and `niwa watch`'s stable per-PR
instance naming (`internal/cli/watch.go:781`) would lose its determinism.

### 2. Session capture — the claim is correct; the key is the instance directory

`captureSessionID` (`internal/cli/dispatch_capture.go:36-62`) does:

```
dispatch_capture.go:44   targetDir := normalizePath(instanceDir)
dispatch_capture.go:48   rec, ambiguous, err := matchRecordByCwd(root, records, targetDir)
dispatch_capture.go:56   return rec.ID, rec.Handle, nil
```

and `matchRecordByCwd` (`internal/cli/session_records.go:352-375`) matches on
`normalizePath(candidate.Cwd) != targetDir` — the recorded working directory, nothing else.
Its doc comment at `:341-350` states the reason explicitly: "the caller hands over a freshly
created, unique directory".

The handle it returns is filled in by `withHandle` (`internal/cli/session_records.go:216-223`):

```go
case agentplan.HandleRecordDir: rec.Handle = filepath.Base(dir)
case agentplan.HandleSessionID: rec.Handle = rec.ID
```

For Claude the declaration is `Handle: HandleRecordDir` (`internal/agentplan/dispatch.go:365`),
i.e. the jobs-directory basename — and that basename is a leading slice of the session UUID,
not the display name (`internal/cli/job_state.go:188-204` scans for "a job dir whose name is a
prefix of the session id"). For Codex it is `HandleSessionID` (`internal/agentplan/dispatch.go:~446`).

`internal/cli/dispatch_capture_test.go` builds three fixture stores (`:52`, `:80`, `:128`) all
keyed on `cwd`; the only handle assertion (`:268-289`) checks the handle is the record's own
directory name, not a slice of the id. No test in that file mentions a display name.

**Verdict: unaffected.** Capture would work identically if the display name were a random
string, or absent.

### 3. Keep-alive — nothing keys on the name

`internal/cli/dispatch_keepalive.go` is 120 lines and contains no reference to the slug or
the name. The two decision functions take only flag/config/instance-settings inputs:

```
dispatch_keepalive.go:94    func resolveDispatchKeepAlive(flag *bool, global config.GlobalSettings, inst *instanceSettings) bool
dispatch_keepalive.go:115   func remoteControlEnabled(rcInjected bool, inst *instanceSettings) bool
```

The arming channel is a fixed prompt prefix (`keepAliveArmingInstruction`, `:33`) prepended to
the task prompt, and the fact of arming is recorded on the mapping as the boolean `KeepAlive`
(`internal/cli/dispatch.go:769`), read back by `niwa list` against the session's job entry
(`internal/cli/list.go:125`). **Verdict: unaffected.**

### 4. Remote control — nothing keys on the name

`internal/cli/dispatch_remotecontrol.go` (64 lines) resolves from
`(global config.GlobalSettings, inst *instanceSettings, env []string)` only
(`:37-50`); the injected document is a constant built from a config key
(`:16`). **Verdict: unaffected.**

### 5. Reap, the session→instance mapping, and `instance from-hook`

- **Mapping store key**: the session UUID. `sessionMappingPath` validates and joins
  `<workspaceRoot>/.niwa/sessions/<session_id>.json`
  (`internal/workspace/session_map.go:114-119`, validator at `:20-27`). The struct
  (`:49-95`) carries `InstanceName`, `InstancePath`, `Handle`, `Label`, `Origin`,
  `KeepAlive` — and **no display-name field**. `InstanceName` is set from `res.Name`, the
  instance *directory* name (`internal/cli/dispatch.go:763`).
- **Primary reap sweep**: joins mappings to instances by `InstancePath`
  (`internal/cli/reap.go:348-349`), decides liveness from `mapping.SessionID`
  (`internal/cli/reap.go:393`), and uses `mapping.InstanceName` only as a label in the
  spared/notice report (`internal/cli/reap.go:394-397`).
- **Backstop sweep**: eligibility is `isDispatchInstanceName(filepath.Base(rec.Path))`
  (`internal/cli/reap.go:595`) — the directory name again — plus unmapped-ness
  (`:586-587`), age, and two liveness guards (`internal/cli/reap.go:736-739`). The full
  contract is documented at `internal/cli/reap.go:524-570`.
- **Liveness readers**: `sessionLive` keys on the session id (`internal/cli/job_state.go:104-123`),
  `instanceHasLiveJob` keys on the job's `cwd` resolving inside the instance path
  (`internal/cli/job_state.go:142-165`). Neither reads a name.
- **`niwa instance from-hook`** (ephemeral session instances): the correlation key is the
  hook payload's `session_id`, validated as a UUID (`internal/cli/instance_from_hook.go:160`),
  and the instance is named `<config>-<first 12 hex of the UUID>`
  (`:176-183`, const at `:80-85`). The `+` separator is explicitly reserved for dispatch
  slugs (`:179`). No slug, no display name, anywhere on this path.

**Verdict: all unaffected.**

### 6. `niwa list` and the other instance-facing commands

`runList` prints `r.Name` — the instance directory name from
`workspace.EnumerateInstanceRecords` (`internal/cli/list.go:59`, `:82-87`) — plus a resume
line built from the mapping's `Handle` via the agent's own declaration
(`internal/cli/list.go:147-172`, `reentryCommand` at `internal/cli/dispatch_reentry.go:134-151`).
`niwa destroy <name>`, `niwa apply --instance <name>` and `niwa status` all match on
`state.InstanceName`, again the directory name (`internal/workspace/destroy.go:52`,
`internal/workspace/scope.go:163`, `internal/workspace/status.go:74`,
`internal/workspace/scan.go:116-123`). `completeSessionIDs` completes worktree session ids
from `.niwa/sessions` (`internal/cli/completion.go:97-116`).

**Verdict: unaffected.** None of these surfaces has ever shown the session display name;
`PRD-dispatch-paste-prompt.md:141` says so in as many words ("niwa's own listing shows
instance names and has never shown prompt text").

### 7. There is no reverse lookup on the display name anywhere in niwa

`agentplan.SessionRecords` (`internal/agentplan/dispatch.go:158-216`) declares `CwdPath` and
`IDPath` and nothing resembling a name path, so `decodeSessionRecord`
(`internal/cli/session_records.go:259-283`) never extracts one. `jobState`
(`internal/cli/job_state.go:37-64`) decodes `sessionId`, `template`, `cwd`, `state`, `tempo`,
`inFlight`, `block`, `needs` — no name. The pre-pivot agent mesh that could have addressed a
peer by name was removed wholesale (per the workspace notes; nothing matching it survives in
`internal/`).

The display name is **write-only from niwa's point of view**: it goes out on argv and is never
read back.

### 8. `niwa watch` — the consumer nobody named, and the only place a name IS a key

`stageReview` derives its own slug from the PR identity and uses it **three** ways:

```
internal/cli/watch.go:781   slug := sanitizeInstanceSlug(fmt.Sprintf("watch-%s-%s-%d", pr.Owner, pr.Repo, pr.Number))
internal/cli/watch.go:782   namePrefix, err := dispatchNameSuffix(slug)                                  // instance dir name
internal/cli/watch.go:836   passthrough := buildDispatchPassthrough(claudeLaunchSpec().Flags, slug, "")  // display name
internal/cli/watch.go:869   Handle:        slug,                                                        // staged-record key
```

`StagedRecord.Handle` is a **filesystem key**: `SaveStagedRecord` writes
`<workspaceRoot>/<stagedRecordsRelDir>/<Handle>.json` after an `isSafeHandle` charset check
(`internal/watch/state.go:331-347`); `LoadStagedRecord` (`:350-364`), `DeleteStagedRecord`
(`:366-379`) and `ListStagedHandles` (`:382-403`) all address records by it, and the hidden
`niwa watch-check-freshness --handle` command loads by it too
(`internal/cli/watch_check.go:65`). `continueReview` re-forwards the stored handle as the
display name on the resume launch (`internal/cli/watch.go:576`).

Note this is niwa's *own* handle namespace, distinct from the Claude handle: the record's
`ShortID` field is the jobs-dir basename `claude stop` accepts (`internal/watch/state.go:319-327`,
used at `internal/cli/watch.go:563` and `:604`).

**Verdict: affected only by *where* the suffix is applied.**
- Suffix applied at the dispatch call site (between `:458` and `:564`, or as a new argument
  to `buildDispatchPassthrough` that watch passes empty): watch is untouched.
- Suffix applied inside `buildDispatchPassthrough`: watch's display name silently gains a
  random tail while its record key stays the bare slug. Nothing breaks functionally (nothing
  maps a display name back to a handle), but the deliberate "the name in Agent View is the
  handle you type" property at `internal/watch/state.go:290-292` is lost, and
  `continueReview`'s resume would show a *different* random tail from the original stage.
- Suffix applied inside `sanitizeInstanceSlug`: watch's record key becomes non-deterministic
  and per-stage; `ListStagedHandles`-driven GC still works (it enumerates), but the
  human-typeable `--handle` and the "same PR, same handle" property are gone. Do not do this.

### 9. Codex ignores the display name entirely

The Codex launch spec declares no `DisplayName` (`internal/agentplan/dispatch.go:414-421`,
with the comment "No subagent types, no session display name, and no inline settings
document"), and `buildDispatchPassthrough` drops a pair whose flag is empty
(`internal/cli/dispatch.go:983-989`). So `--name` is forwarded on Claude dispatches only.
A uniqueness suffix would be invisible on `--harness codex` — the fix is asymmetric across
harnesses by construction.

### 10. Tests

**Functional (`test/functional/`) — nothing asserts on the session display name.**
`dispatch.feature` never passes `--name` (checked in full, 406 lines; the flag does not
appear). Nor does `keep-alive.feature`, `codex-agent.feature`, or `agent-selection.feature`.
The only name assertions are against the *instance directory* regex, mirrored from the CLI:

```
test/functional/dispatch_steps_test.go:21    var dispatchInstanceNameRe = regexp.MustCompile(`\+[a-z0-9_]*-[0-9a-f]{8}$`)
test/functional/dispatch_steps_test.go:163   if e.IsDir() && dispatchInstanceNameRe.MatchString(e.Name())
test/functional/keepalive_steps_test.go:140  if dispatchInstanceNameRe.MatchString(r.Name)
```

**Unit tests — three would need attention, and two of them are the real design constraint.**

- `internal/cli/dispatch_test.go:669-739` `TestDispatch_Name_SlugInInstanceAndSession` asserts
  the forwarded argv contains the exact pair `--name my_thing` via `passthroughHasNameSlug`
  (`:824-831`). **This fails outright** under a display-name suffix and must be updated to
  assert a prefix-plus-suffix shape.
- `internal/cli/dispatch_test.go:742-781` `TestDispatch_NoName_NoSlugNoNameFlag` and
  `:783-820` `TestDispatch_NameSanitizesEmpty_FallsBack` both assert that **no `--name` is
  forwarded at all** when the slug is empty. These pin a genuine behavioral decision: a
  suffix applied unconditionally would start naming previously-unnamed sessions
  (`--name 4e33acfa`), which these tests forbid today.
- `internal/cli/dispatch_wiring_remotecontrol_test.go:127` compares the launched argv
  byte-for-byte against a **second call** to `buildDispatchPassthrough(...,"","")`. If the
  random suffix were generated *inside* that function the two calls would differ and this
  test would fail nondeterministically — a strong argument for generating the suffix once in
  `runDispatch` and passing it in.

### 11. Docs

Nothing promises the *session display name* equals the raw `--name`; several places promise
it equals the *sanitized slug*, which a suffix would make false as written:

- `docs/designs/current/DESIGN-instance-dispatch.md:161-177` — "it is forwarded to the
  session as `claude --bg --name <slug>` so the Claude session carries a human display name in
  Agent View", and "concurrency stays collision-safe even when two dispatches share a
  `--name`" (which is true of the *instance*, and is precisely the sentence that hides the
  session-name collision). Line 175-177 also draws the `--name` vs `--label` distinction.
- `internal/workspace/rootskills/dispatch/SKILL.md:112-113` — "`--name` gives the session a
  readable name in Agent View (sanitized into a slug; it also names the instance, e.g.
  `<config>+<slug>-<id>`)". This ships embedded in the binary and is materialized into every
  workspace root, so it is a user-visible doc that would need the same edit.
- `internal/workspace/root_materializer.go:434` — the generated orientation text spells the
  command as `niwa dispatch "<task>" --name <slug> [--detach]`.
- `README.md:155` — lists the flag; makes no equality promise.
- `docs/prds/PRD-dispatch-paste-prompt.md:141` — "`--name` remains the way to label a dispatch
  on niwa's surfaces"; `docs/briefs/BRIEF-dispatch-paste-prompt.md:186` — "Telling the three
  apart from the outside is what `--name` is for."
- `docs/prds/PRD-instance-dispatch.md` R-numbers about uniqueness (`:142-144`, `:244`, `:380`)
  are all about the **instance** directory/name/mapping key, never the session name.

Separately, `internal/workspace/snapshotwriter.go:20-26` documents that the `/dispatch` skill
writes its brief to `.niwa/dispatch-briefs/<slug>.md`. That `<slug>` is chosen independently
by the skill (`SKILL.md:86-97`) and passed to the worker as an absolute path inside the
prompt; it is not mechanically tied to `--name`, so a suffix does not orphan any brief.

### 12. Anything that reconstructs the slug from the instance directory name?

No. A repo-wide grep for splitting/cutting on `"+"` outside `internal/github/tar.go` returns
nothing. `isDispatchInstanceName` is a match-only predicate; no code extracts the slug back
out of a directory name, and no code compares a directory name to a display name.

### Consumer table

| Consumer | File:line | Correlation key it actually uses | Broken by a display-name-only suffix? |
|---|---|---|---|
| Instance directory name | `dispatch.go:459`, `:478` | `slug` + fresh 8-hex, joined with `+` | No (suffix must be applied after `:459`) |
| Reaper backstop eligibility | `reap.go:595`; regex `dispatch.go:941` | instance dir basename regex `\+[a-z0-9_]*-[0-9a-f]{8}$` | No |
| Session capture | `dispatch_capture.go:44-48`; `session_records.go:352-375` | recorded `cwd` == instance dir | No |
| Captured handle | `session_records.go:216-223`; `job_state.go:188-204` | record-dir basename (a UUID prefix) or the UUID | No |
| Session mapping store | `session_map.go:114-119` | session UUID (filename) | No |
| Primary reap sweep | `reap.go:348-349`, `:393` | `InstancePath`, then `SessionID` | No |
| `instance from-hook` (ephemeral) | `instance_from_hook.go:160`, `:176-183` | hook `session_id` UUID; dir `<config>-<12hex>` | No |
| Keep-alive | `dispatch_keepalive.go:94`, `:115` | `--keep-alive` flag / instance settings / host config | No |
| Remote control | `dispatch_remotecontrol.go:37-50` | host config / instance settings / env | No |
| `niwa list` | `list.go:82-87`, `:147-172` | instance dir name; mapping `Handle` | No |
| `destroy` / `apply --instance` / `status` | `destroy.go:52`, `scope.go:163`, `status.go:74` | `InstanceName` (dir name) | No |
| Functional features + steps | `dispatch_steps_test.go:21`, `:163`; `keepalive_steps_test.go:140` | instance dir name regex | No |
| **`niwa watch` staged-record key** | `watch.go:869`; `state.go:331-347`, `:350-364` | the watch slug, used as `<handle>.json` | **Only if the suffix goes into `buildDispatchPassthrough` or `sanitizeInstanceSlug`** |
| **`niwa watch` continuation display name** | `watch.go:576` | `rec.Handle` (the stored slug) | Same conditional |
| **`TestDispatch_Name_SlugInInstanceAndSession`** | `dispatch_test.go:735-738`, `:824-831` | exact argv pair `--name my_thing` | **Yes — fails; must be updated** |
| **No-name / empty-slug tests** | `dispatch_test.go:775-780`, `:816-819` | absence of `--name` in argv | **Yes if the suffix is unconditional** |
| **RC baseline argv equality test** | `dispatch_wiring_remotecontrol_test.go:127` | byte-equality with a second `buildDispatchPassthrough` call | **Yes if the suffix is generated inside that function** |
| Codex dispatches | `agentplan/dispatch.go:414-421`; `dispatch.go:983-989` | no `DisplayName` declared; pair dropped | No (suffix is a no-op there) |
| `/dispatch` brief file | `snapshotwriter.go:20-26`; `SKILL.md:86-97` | a slug the skill picks, passed as an absolute path in the prompt | No |
| Docs (design, skill, orientation) | `DESIGN-instance-dispatch.md:161-177`; `SKILL.md:112-113`; `root_materializer.go:434` | prose | Text becomes inaccurate; needs editing |

## Implications

The blast radius is far smaller than the lead feared. Every durable correlation in niwa keys
on one of three things — the session UUID, the instance *directory* path/name, or the agent's
own record handle — and none of the three touches the display name. The display name is
write-only: it leaves on argv at `dispatch.go:564` and is never read back by any niwa code
path, because no `SessionRecords` declaration and no `jobState` field even exposes it.

That makes the change tractable, but it puts almost all the design weight on **where** the
suffix is applied. Three call sites share the slug machinery, and only one of them is
`niwa dispatch`:

1. **Not in `sanitizeInstanceSlug`** (`dispatch.go:910`) — shared with `niwa create --name`
   (`create.go:115`) and `niwa watch` (`watch.go:781`); a suffix there randomizes instance
   directory names for both.
2. **Not in `buildDispatchPassthrough`** (`dispatch.go:974`) — shared with `niwa watch`
   (`watch.go:836`, `:576`), whose slug is simultaneously its staged-record filename, and
   whose baseline is compared byte-for-byte in a test (`dispatch_wiring_remotecontrol_test.go:127`).
3. **In `runDispatch`**, between line 459 (after the instance name is fixed) and line 564 —
   the only site where the change reaches exactly the dispatched session's display name and
   nothing else.

The second design question the existing tests already answer for us: `dispatch_test.go:742-820`
pins "no slug means no `--name` at all." A suffix cannot be applied unconditionally without
changing that behavior — so either the suffix rides only a non-empty slug (leaving no-name
dispatches as anonymous, and hence *still* colliding with each other under whatever default
name Claude assigns), or that decision is deliberately reversed and those two tests rewritten.

Reusing the instance's own 8 hex is the obvious way to make the session name and the instance
directory legible as a pair, but `dispatchNameSuffix` (`dispatch.go:879-889`) currently
returns only the concatenated string and discards the hex; it would need to return the token
separately (or the caller would have to re-parse its own output, which is worse).

Finally, a suffix is invisible on Codex (§9). If the point is "two dispatches with the same
`--name` must be separately addressable", that guarantee will hold for Claude only until the
Codex spec grows a display-name flag.

## Surprises

- **`niwa watch` is a second, undocumented consumer of the slug machinery** and the lead did
  not mention it. It is the only place in the repo where a display name and a niwa-owned
  lookup key are deliberately the *same string* (`watch.go:836` and `:869`), and that key is
  a filename (`internal/watch/state.go:331-347`).
- **The same collision the exploration is about already exists in `watch`, by construction.**
  Its slug is `watch-<owner>-<repo>-<number>` (`watch.go:781`), which is deterministic — so
  re-staging the same PR after a dismissal reuses the identical display name *and* the
  identical record filename. Whatever is decided for `dispatch` should probably be decided for
  `watch` too, but with the record-key half held fixed.
- **The design doc contains a sentence that reads as reassurance and is actually the bug**:
  "concurrency stays collision-safe even when two dispatches share a `--name`"
  (`DESIGN-instance-dispatch.md:172-173`). It is true of the instance directory and silently
  false of the session name.
- **Codex dispatches never receive a display name at all** (`agentplan/dispatch.go:414-421`),
  so the collision — and any fix — is Claude-only.
- The instance-name slug is capped at 40 runes (`dispatch.go:63`, applied at `:925-927`), a
  cap chosen for filesystem-name headroom. The display name inherits that cap today purely
  because it is the same string; nothing separately bounds it.

## Open Questions

1. **Should the suffix reuse the instance's own 8 hex, or be independent?** Reuse makes
   `list`/Agent View correlation trivial but requires changing `dispatchNameSuffix`'s
   signature (`dispatch.go:879`) to surface the token.
2. **What happens with no `--name`?** Today: no `--name` forwarded, session unnamed, and two
   anonymous dispatches are equally unaddressable. Naming them `-<8hex>` would fix that and
   break `dispatch_test.go:742-781` and `:783-820` — a deliberate call, not an accident.
3. **Does `niwa watch` get the same treatment?** Its collision is real, but its slug doubles as
   a human-typeable `--handle` (`watch_check.go:65`); suffixing it costs that property.
4. **Is the Claude jobs-directory basename ever derived from `--name`?** Everything in this
   repo says it is a UUID prefix (`job_state.go:188-204`, and the declaration comment at
   `agentplan/dispatch.go:360-364`), but that is an observed layout, not a contract. If it
   ever became name-derived, the captured `Handle` would carry the suffix — self-consistently,
   since capture reads the actual directory name — but the "handle you type" would change
   shape. Not verifiable from niwa's source.
5. **Is the session name genuinely the peer address in Claude Code?** The premise comes from
   outside this repo; nothing in niwa addresses a session by name (the `niwa_*` mesh was
   removed), so this cannot be confirmed here.

## Summary

Every durable correlation in niwa keys on the session UUID, the instance directory path or
name, or the agent's own record handle — capture matches on recorded `cwd`
(`dispatch_capture.go:44-48`), the mapping store is filed under the UUID
(`session_map.go:114-119`), and the reaper's backstop matches the instance directory regex
(`reap.go:595`) — so the dispatched session's display name is write-only and appending a
uniqueness suffix to it breaks no runtime consumer. The whole design question is *where* the
suffix goes: it must be applied in `runDispatch` between lines 459 and 564, because
`sanitizeInstanceSlug` and `buildDispatchPassthrough` are both shared with `niwa watch`
(`watch.go:781`, `:836`, `:869`), where the same slug doubles as a staged-record *filename*,
and because `dispatch_wiring_remotecontrol_test.go:127` compares argv byte-for-byte against a
second call to the builder. The biggest open question is the no-name case: two existing tests
(`dispatch_test.go:742-820`) pin "empty slug means no `--name` forwarded at all", so anonymous
dispatches stay mutually unaddressable unless that decision is deliberately reversed.
