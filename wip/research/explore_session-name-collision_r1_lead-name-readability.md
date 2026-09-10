# Lead: Where does the dispatched session's name surface to a human, and what is the readability cost of appending a uniqueness suffix?

Round 1. All claims re-verified against source in this worktree
(`.claude/worktrees/session-name-collision`). Note: the design docs live under
`docs/designs/current/`, not `docs/designs/` as the lead stated.

## Findings

### The headline: niwa never displays the session name. Not once.

The sanitized slug travels **one way only** — niwa mints it, embeds it in the
instance directory name, and forwards it to the agent as `--name <slug>`. It is
never read back, never stored as a session-name field, and never printed as a
session name by any niwa command.

- `internal/cli/dispatch.go:458` — `slug := sanitizeInstanceSlug(dispatchName)`
  is the only place `dispatchName` is consumed (`grep -rn "dispatchName\b"`
  returns exactly the flag registration at `:26`, the var at `:45`, and this
  line).
- From there the slug has exactly two consumers:
  1. `dispatchNameSuffix(slug)` (`internal/cli/dispatch.go:879`) → the instance
     directory name.
  2. `buildDispatchPassthrough(..., slug, ...)` (`internal/cli/dispatch.go:974`,
     called at `:985` with `{flags.DisplayName, slug}`) → `claude --bg --name
     <slug>`.
- The session mapping record (`internal/workspace/session_map.go:49-95`) has
  **no name field**. It has `SessionID`, `InstanceName`, `InstancePath`,
  `TranscriptPath`, `Created`, `Ephemeral`, `Agent`, `Handle`, `Label`,
  `Origin`, `KeepAlive`. `Label` is `--label`, a separate freeform alias that is
  written (`internal/cli/dispatch.go:769`) and **never read by any printing
  path** — `grep -rn "dispatchLabel"` finds only the flag, the var, and the
  write.
- niwa reads nothing named from the agent's job record either:
  `SessionRecords` for Claude (`internal/agentplan/dispatch.go:365-378`) reads
  `cwd` and `sessionId` only.

So the readability objection has to be argued entirely on the agent harness's
surfaces (Agent View / `claude agents` / session pickers), because niwa's own
surfaces show the *instance name*, not the session name.

### Surface-by-surface enumeration

**1. `niwa dispatch` success output — does NOT print the name.**

`internal/cli/dispatch.go:806-810`:

```go
fmt.Fprintf(out, "Dispatched session %s\n", sessionID)
fmt.Fprintf(out, "  instance: %s\n", instancePath)
for _, line := range reentryHints(spec, handle, instancePath) { ... }
```

The headline is the full session **UUID**. The hint lines are
`claude attach <handle>` / `claude logs <handle>` / `claude stop <handle>`
(`internal/agentplan/dispatch.go:380-384`: `ResumeArgs: ["attach"]`,
`HintVerbs: ["attach","logs","stop"]`), where the handle is the job **record
directory name**, not the display name (`internal/agentplan/dispatch.go:373`,
`Handle: HandleRecordDir`). The developer sees the slug only as a substring of
the printed `instance:` path.

This is by design, per PRD R3 (`docs/prds/PRD-instance-dispatch.md:100-102`):
"On success the command SHALL report, to stdout, the launched session's
**identifier** and how to manage it (attach/inspect/stop)". Nothing requires
reporting the name.

**Directly answers the lead's last sub-question: if a suffix were added today,
the user would NOT learn what the real session address is from dispatch's
output.** They would have to infer it from the `instance:` path (which carries
`<config>+<slug>-<8hex>`, so the suffix is in fact visible there) or go look in
Agent View. That is a fixable gap — printing the name is one `Fprintf`.

**2. `niwa list` — prints instance names, never session names.**

`internal/cli/list.go:82-95` prints `r.Name` (the instance directory name, which
already contains `<slug>-<8hex>`), optionally `(keep-alive)`, then a `resume:`
line built from the handle. One name per line, **no truncation, no column
budget**. Corroborated by the PRD for the paste-prompt work
(`docs/prds/PRD-dispatch-paste-prompt.md:139-141`): "niwa's own listing shows
instance names and has never shown prompt text, so `--name` remains the way to
label a dispatch on niwa's surfaces."

**3. `niwa status [instance]`** — `internal/cli/status.go:207`,
`fmt.Fprintf(out, "Instance: %s\n", status.Name)`. Full instance name, own line,
no truncation.

**4. `niwa reap` sparing report** — `internal/cli/reap.go:208-227`. Instance
names are comma-joined onto **one line**, capped at 3 names
(`maxSparedNamesListed = 3`, `internal/cli/reap.go:175`) with an "and N more"
suffix. This is the single densest niwa surface for names, and the cap exists
precisely to bound line length. Still no per-name truncation. Again: instance
names, not session names.

**5. `niwa watch`** — the one place niwa *does* print a session display name.
`internal/cli/watch.go:886-889`:

```go
fmt.Fprintf(cmd.OutOrStdout(),
    "niwa watch: staged review for %s/%s#%d (handle %s)%s\n",
    pr.Owner, pr.Repo, pr.Number, slug, reviewWritePosture(...))
```

where `slug = sanitizeInstanceSlug("watch-<owner>-<repo>-<number>")`
(`internal/cli/watch.go:781`) and the same slug is forwarded as the session
display name via `buildDispatchPassthrough(..., slug, "")`
(`internal/cli/watch.go:836`) **and** stored as the record's `Handle`
(`:869`). On continuation, `internal/cli/watch.go:576` re-forwards
`rec.Handle` as `--name` so the resumed session keeps the same display name.
Note that watch's session name carries **no** hex suffix — only its instance
directory does (`dispatchNameSuffix(slug)` at `:782`). Watch names are
deterministic-per-PR rather than random, so they collide across re-stagings of
the same PR by construction.

**6. Docs and examples.** `README.md:155` documents the flag; the embedded
`/dispatch` skill (`internal/workspace/rootskills/dispatch/SKILL.md`) is the
most explicit statement of the name's purpose:

> "**`--name`** gives the session a readable name in Agent View (sanitized into
> a slug; it also names the instance, e.g. `<config>+<slug>-<id>`)."

and its step 4 tells the coordinator to report "the brief path, the dispatched
session id and how to reach it (`claude attach <id>` …)" — **the id, not the
name**. The generated workspace-root CLAUDE.md
(`internal/workspace/root_materializer.go:434`) shows
`niwa dispatch "<task>" --name <slug> [--detach]` and says the worker "appears
in Agent View" — no display-width claim.

**7. Agent View itself.** The repo has one empirical study of Agent View,
`docs/spikes/SPIKE-ephemeral-session-instances.md`, which probed hook firing,
cwd inheritance and job-entry lifecycle. **It records nothing about how a
session's display name is rendered, at what width, or whether it is truncated.**
The `/dispatch` skill notes one adjacent fact: "its user-visible `systemMessage`
channel does NOT reach the Agent View dashboard row" — implying the row is
narrow enough that what appears there matters, but nobody in this repo has
measured it.

### Display width budgets

There is **no truncation of a name anywhere in niwa**. `grep -rn
"truncat\|ellips"` across `internal/` finds only: a display-only SHA truncation
in `niwa status` (`internal/cli/status.go:330-332`, 64-hex → 12 + ellipsis), a
rune-boundary cut for spilled prompt excerpts
(`internal/cli/dispatch_spill.go:197`), and fixed column widths in the
secrets-audit table (`internal/cli/status_audit_auth.go:117-123`) whose comment
explicitly says the renderer "absorbs the overflow **without truncating**".

The arithmetic, verified:

- `maxDispatchSlugRunes = 40` (`internal/cli/dispatch.go:62`), enforced at
  `:925-926`. Comment: "40 runes is generous for a human-readable label while
  leaving room below filesystem name limits for the config prefix and the
  `-<8hex>` signature suffix."
- Suffix is `"-" + hex.EncodeToString(4 bytes)` = 9 chars
  (`internal/cli/dispatch.go:879-888`).
- So a suffixed session name is at most **49 chars**, and the instance directory
  name is `<config>` + `+` + 49 — for config `tsuku` that is **55 chars, today,
  already, unconditionally**.

That last number is the crux: **every niwa surface that shows a name already
shows a 40-rune slug plus a dash plus 8 hex**, because that is the instance
name. Adding the same 9 chars to the session name adds nothing to any niwa
surface. It adds 9 chars to exactly one place: the Agent View row, which niwa
does not own and has not measured.

### Is the name ever typed by a human?

**The session display name: no niwa subcommand accepts it.** Enumerating every
`Use:` line in `internal/cli/` (`grep -rn "Use:" internal/cli/*.go`), the
identifier-taking commands are:

| Command | Argument | Completion |
|---|---|---|
| `niwa destroy [instance]` | instance **name** | `completeInstanceNames` (`internal/cli/destroy.go:21`) |
| `niwa status [instance]` | instance **name** | `completeInstanceNames` (`internal/cli/status.go:30`) |
| `niwa reset [instance]` | instance **name** | `completeInstanceNames` (`internal/cli/reset.go:18`) |
| `niwa apply --instance` | instance **name** | `completeInstanceNames` (`internal/cli/apply.go:38`) |
| `niwa worktree apply/destroy/attach/detach <session-id>` | worktree session id (8 hex) | `completeSessionIDs` (`internal/cli/completion.go:83-110`) |
| `niwa go [target] [session-id]` | repo/workspace + worktree session id | `completeGoTarget` |

`completeInstanceNames` (`internal/cli/completion.go:31-63`) enumerates instance
directories and offers the full name — **so the 49-char slug-plus-hex tail is
already tab-completable and already typed today**, and the shell does the
typing. There is no completion for, and no command taking, a *session* display
name. So a suffix would not force any new copy-paste on a niwa command line.

The one place a human types a display name is the agent harness's own peer
addressing (`SendMessage`/`claude attach`-adjacent flows), which is outside this
repo. Note that `claude attach` takes the **handle** (record dir name), not the
display name (`internal/agentplan/dispatch.go:373`), so even the agent's own
management verbs as niwa uses them are name-free.

Second collision surface worth flagging, also slug-keyed and also
human-readable: the `/dispatch` skill writes its brief to
`<workspace-root>/.niwa/dispatch-briefs/<slug>.md`
(`internal/workspace/rootskills/dispatch/SKILL.md`, step 2; the path is
exercised in `test/functional/steps_workspace_config_sources_test.go:81-95`).
Two dispatches with the same topic slug **silently overwrite each other's
brief** — the same class of bug, one layer up, and it is not addressed by
suffixing the session name.

### Precedents: readable-name-plus-suffix, and suffix lengths

**a) The instance directory name itself — `<config>+<slug>-<8hex>`.** The
justification is in `docs/designs/current/DESIGN-instance-dispatch.md:152-176`
(D2). The chosen option is stated as serving both goals at once:

> "A random token is collision-safe under concurrency without any lock **and
> reads clearly in `niwa list` as a dispatch-created instance**."

and on the slug specifically:

> "The random 8-hex is **always kept**, so the structural signature the reaper
> backstop matches … is preserved and **concurrency stays collision-safe even
> when two dispatches share a `--name`**. The slug is additive: it never
> replaces the random token."

That sentence is a direct, already-accepted precedent for exactly the change
under consideration: the design already anticipated two dispatches sharing a
`--name` and answered it by keeping the random token. It just never extended the
answer from the directory to the session name.

**b) The 12-hex ephemeral-instance prefix.** `DESIGN-ephemeral-session-
instances.md:220-237` names the trade explicitly and is the repo's only place
where a *shorter* hex is weighed:

> "using at least the first 12 hex characters of the UUID (niwa applies no
> charset/length validation to the `--name` suffix and a UUID is filesystem-safe;
> **12+ chars keeps collisions negligible while the job dir's own 8-char prefix
> shows shorter is used elsewhere**)."

Implemented as `sessionNamePrefixLen = 12` (`internal/cli/instance_from_hook.go:81-85`).
The same decision also states the durable-identity-versus-cosmetic-label posture
that this exploration is arguing about:

> "A human-friendly alias (e.g. derived from the first `UserPromptSubmit` once
> the topic is known) MAY be recorded as an optional `label` field on the mapping
> later, but the on-disk instance directory is never renamed … so durable
> identity stays the `session_id` and any slug is **cosmetic metadata**."

**c) 8-hex worktree session ids, with the collision math written down.**
`internal/worktree/session_lifecycle.go:145-162`:

> "generates a random **8-character lowercase hex** session ID and atomically
> reserves its state file with `O_CREATE|O_EXCL`. Retries up to 5 times on
> collision (**birthday probability ~1e-9 at 20 sessions**)."

These ids are the *entire* human-facing identifier: they are the argument to
`niwa worktree apply/destroy/attach/detach` and `niwa go`, and they are the
branch name (`session/<sid>`, `internal/worktree/worktree.go:32-36, 155, 202`).
niwa has already shipped a surface where a human types and reads a bare 8-hex
token with no readable part at all — and gave it tab completion instead of a
readable name.

**d) `niwa watch`'s machine-generated readable slug.** `watch_<owner>_<repo>_<num>`
plus `-<8hex>` on the directory (`internal/cli/watch.go:781-782`). This is a
shipped example of a readable slug that is 30-40 chars before any suffix and
that niwa prints verbatim to stdout.

**e) The one place niwa REJECTED decorating a name — and why.**
`DESIGN-agent-capability-contract.md:782-785`, rejecting a `niwa-` prefix on
generated MCP server names:

> "Namespace-prefixing generated server names instead of detecting collisions: a
> `niwa-` prefix **changes every name users address**, doesn't make collisions
> impossible, and hides the conflict rather than surfacing it."

This is the strongest in-repo articulation of the readability objection — but
read the reasons: the prefix was rejected because it (i) touched *every* name,
(ii) did **not** actually make collisions impossible, and (iii) hid rather than
surfaced the conflict. A random 8-hex suffix on a dispatched session name fails
none of those three tests: it touches only dispatch-created sessions, it does
make collisions negligible (~1e-9 at 20, per (c)), and it is a uniqueness token
rather than a conflict-hiding rename. The precedent cuts *for* the suffix once
you read past the headline.

**f) Completion-design posture on collisions.** `DESIGN-contextual-completion.md:158-183`
chose to emit both colliding entries rather than silently pick one, with the
rationale: "Discoverability wins over keystroke economy given the small
candidate set (tens, not thousands)." And in Out of Scope
(`:495-497`): "**`create --name <suffix>` completion.** The suffix becomes part
of a new directory name; **suggesting existing values invites collisions**."
niwa's stated posture is consistently: surface collisions loudly, prefer
uniqueness to keystrokes.

### Does the suffix apply to every harness?

No. Only Claude declares a `DisplayName` flag
(`internal/agentplan/dispatch.go:356`, `DisplayName: "--name"`). Codex declares
none, and the table says so explicitly (`internal/agentplan/dispatch.go:419-424`):

> "No subagent types, **no session display name**, and no inline settings
> document. Each is a niwa-side intent this agent has no flag for, and an intent
> with no flag is dropped rather than guessed at."

`buildDispatchPassthrough` drops any pair whose flag spelling is empty
(`internal/cli/dispatch.go:983-987`). So a suffix would change the observable
behavior of Claude dispatches only; a Codex dispatch forwards no name today and
would forward none after.

## Implications

**The readability objection is much weaker than it looks, and it is
misdirected.** Every niwa surface that shows a name today shows the *instance*
name — `<config>+<slug>-<8hex>`, already up to 55 characters, already carrying
the exact 9-character suffix under debate, already printed untruncated by
`niwa list`, `niwa status`, `niwa watch` and the reap report, and already typed
(with completion) into `niwa destroy`, `status`, `reset` and `apply --instance`.
niwa has been paying the full cost of `review-3f9a2b1c` on its own surfaces
since dispatch shipped, and nothing in the repo records a complaint about it.
The delta the objection is really about is 9 characters on one Agent View row
that this repo has never measured and does not own.

**The prior justification is reusable almost verbatim.**
`DESIGN-instance-dispatch.md:170-174` already reasoned about two dispatches
sharing a `--name` and concluded that keeping the random token is what preserves
collision safety. Extending the same sentence from the directory to the session
name is an incremental application of a shipped decision, not a new one. The
supporting collision math is also already written down
(`internal/worktree/session_lifecycle.go:148-149`, ~1e-9 at 20 sessions), so an
8-hex suffix needs no fresh analysis.

**The one real cost is discoverability of the address, and it is one line of
code.** Today `niwa dispatch` prints the session UUID and the instance path, so
a user who wanted to address a peer by name would have to dig the slug out of
the path or open Agent View. If the name becomes an address that carries a
suffix, dispatch should print it — the fix is an `Fprintf` next to
`internal/cli/dispatch.go:806-807`, and PRD R3 does not forbid it. Any proposal
that adds a suffix without adding that line makes the address genuinely harder
to find, which is a legitimate objection to *that* proposal, not to suffixing.

**Shorter hex is available if the objection survives.** The repo has an explicit
precedent for weighing suffix length — 12 hex chosen with a note that "the job
dir's own 8-char prefix shows shorter is used elsewhere"
(`DESIGN-ephemeral-session-instances.md:222-224`) — and a shipped 8-hex
identifier with birthday math attached. 4 hex (65k values, ~0.3% collision at
20 sessions) is the plausible cheaper option; 6 hex (16M) is essentially free.
Nothing in the repo argues *for* 8 specifically over 6 or 4 on readability
grounds; 8 was chosen for the instance name because it also has to be a
structural signature (`dispatchInstanceNameRe`, `internal/cli/dispatch.go:941`),
which the session name does not.

**Watch is the case to check before changing shared code.** `niwa watch` calls
`buildDispatchPassthrough` directly with its own deterministic slug
(`internal/cli/watch.go:836`) and deliberately re-forwards the same name on
continuation (`:576`) so a resumed review keeps a stable identity. A suffix
added inside `buildDispatchPassthrough` would break that stability; a suffix
added in `runDispatch` before the call would not. That is a design constraint,
not a blocker.

## Surprises

- **niwa prints the display name exactly once in the whole codebase, and it is
  in `niwa watch`, not `niwa dispatch`** (`internal/cli/watch.go:886-889`,
  "(handle %s)"). The command whose flag creates the name never shows it back.
- **`--label` is write-only.** `dispatchLabel` is recorded on the mapping
  (`internal/cli/dispatch.go:769`) and read by nothing. There is a documented,
  already-shipped, entirely unused channel for a freeform human alias — which
  weakens any argument that the display name must carry the human-friendly
  burden alone.
- **The `/dispatch` skill has the same collision bug one layer up.** Brief files
  land at `.niwa/dispatch-briefs/<slug>.md`; two same-slug dispatches overwrite
  the brief silently. Suffixing the session name does not fix it.
- **`niwa watch` session names collide by construction**, not by accident: the
  slug is derived from the PR coordinates, so re-staging the same PR reuses the
  name. `DESIGN-niwa-watch-pr-hardening.md:448-464` documents an adjacent
  ambiguity ("the stopped prior entry and the resumed one share the instance
  cwd") without connecting it to name reuse.
- **Codex dispatches have no session name at all**, so the whole problem is
  Claude-specific and a fix must not assume every harness has a name to make
  unique (`internal/agentplan/dispatch.go:419-424`).
- **Nothing in niwa truncates a name.** The only length guard on any name path
  is `maxDispatchSlugRunes = 40`, and its stated reason is filesystem limits,
  not display width (`internal/cli/dispatch.go:58-62`).

## Open Questions

1. **How wide is the Agent View row, and does it truncate?** Unmeasured
   anywhere in this repo — `SPIKE-ephemeral-session-instances.md` probed hooks
   and job lifecycle, never rendering. If the row clips at, say, 24 chars, a
   9-char tail on a 40-rune slug is invisible-and-useless rather than merely
   ugly, which would argue for a *shorter* suffix and possibly for shortening
   `maxDispatchSlugRunes` on the session side. Needs a live probe or a human who
   has one open.
2. **Is the display name genuinely the peer address in Claude Code, or is the
   handle/short-id?** niwa's own management verbs all take the handle
   (`internal/agentplan/dispatch.go:373`), never the name. Nothing in this repo
   documents name-based peer addressing; that claim comes from outside and
   should be verified before the whole fix is justified on it.
3. **4, 6, or 8 hex?** The repo supports either 8 (worktree ids, with math) or a
   deliberate shortening (the 12-vs-8 note). Choosing needs an estimate of
   concurrent same-name dispatches — 20 is the number the existing birthday
   calculation uses.
4. **Should `niwa dispatch` print the session name regardless of the collision
   fix?** It is arguably a standalone gap today (the user cannot see the
   sanitized slug except embedded in a path), and it is a prerequisite for any
   suffix proposal not to make things worse.
5. **Does `--label` want to become the readable surface** — e.g. name gets the
   suffix, label stays clean and gets printed — or should the unused field be
   removed rather than repurposed?
6. **What about the brief-file collision** in the embedded `/dispatch` skill?
   In scope for this exploration or a separate issue?

## Summary

niwa never displays a dispatched session's display name — the slug is written
one way into `claude --bg --name` and into the instance directory, and the only
place any niwa command prints a session name at all is `niwa watch`'s "staged
review … (handle %s)" line; `niwa dispatch` prints the session UUID and the
instance path, `niwa list`/`status`/`reap` print instance names, and nothing
anywhere truncates a name. That means the readability cost of a suffix falls
entirely on Agent View, while every niwa surface has already displayed
`<config>+<slug>-<8hex>` (up to 55 chars, tab-completed into `niwa destroy` and
friends) since dispatch shipped — and `DESIGN-instance-dispatch.md:170-174`
already justified keeping the random token precisely because "two dispatches
share a `--name`", so the reasoning is reusable rather than new. The biggest
open question is whether the Agent View row truncates and at what width, since
nobody in this repo has ever measured it, closely followed by whether the
display name really is the peer address at all given that every niwa-issued
management verb uses the handle instead.
