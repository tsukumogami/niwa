# Lead: Is the session-name collision real in the current code, and exactly where does the slug travel from `--name` to the agent's display-name flag?

Verified against the worktree at branch `docs/session-name-collision` (HEAD `ef53bb3`, on top of
`d436832`). Every line number below was re-derived from the current source, not inherited.

**Verdict up front: the collision is real.** The value that reaches the launched agent's
`--name` is the sanitized slug and nothing else. The 8 hex digits of entropy that make the
*instance directory* unique are generated in the same function call chain, are appended to a
*different* string, and never touch the display-name flag.

## Findings

### 1. Where `--name` is defined and parsed

`internal/cli/dispatch.go:26` registers the flag on `dispatchCmd`:

```go
dispatchCmd.Flags().StringVarP(&dispatchName, "name", "n", "", "optional display name for the session (sanitized into a slug; also names the niwa instance: <config>+-<id> with no name, <config>+<slug>-<id> with one -- '+' always marks the end of the config name)")
```

The backing variable is `dispatchName string` at `internal/cli/dispatch.go:45` (package-level var
block, `dispatch.go:43-56`). It is a plain cobra string flag with a `-n` shorthand and no
validation, no completion function, and no uniqueness check of any kind at registration.

`dispatchName` is read exactly once in production code, at `internal/cli/dispatch.go:458`. (The
only other references are test files: `internal/cli/dispatch_test.go:93,102,153,678,790`.) That
single read is the entire fan-out point for both the instance name and the session display name.

Note the sibling flag `--label` (`internal/cli/dispatch.go:25`, var at `:44`), which is recorded
only on the durable session mapping (`internal/cli/dispatch.go:769`) and is never forwarded to the
agent. So `--name` and `--label` are genuinely different channels; the design says so at
`docs/designs/current/DESIGN-instance-dispatch.md:175-177`.

### 2. The sanitization rule (claim: `[a-z0-9_]` capped at 40 runes) — CONFIRMED, with detail

`sanitizeInstanceSlug` is at `internal/cli/dispatch.go:910-929`; the doc comment runs
`:891-909`. Body:

```go
func sanitizeInstanceSlug(raw string) string {
	var b strings.Builder
	prevSep := false
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevSep = false
			continue
		}
		if !prevSep {
			b.WriteByte('_')
			prevSep = true
		}
	}
	slug := strings.Trim(b.String(), "_")
	if r := []rune(slug); len(r) > maxDispatchSlugRunes {
		slug = strings.TrimRight(string(r[:maxDispatchSlugRunes]), "_")
	}
	return slug
}
```

Point by point:

- **Character class kept**: only ASCII `a-z` and `0-9` survive as themselves
  (`dispatch.go:913`). Input is lowercased first via `strings.ToLower` (`:912`), so `A-Z` maps into
  `a-z`. Everything else — spaces, punctuation, dashes, non-ASCII letters — is *not* dropped and
  *not* replaced one-for-one: each maximal **run** of disallowed runes collapses to a **single**
  `_` (`:918-921`, guarded by `prevSep`). So the output alphabet is `[a-z0-9_]`, matching the claim.
- **Dashes**: a user-typed dash is disallowed and becomes `_`. `"auth-layer"` -> `"auth_layer"`
  (pinned at `internal/cli/dispatch_test.go:609`). This dash-free property is load-bearing for
  `isDispatchInstanceName` (see 4 below) and is asserted by `assertSlugShape`
  (`internal/cli/dispatch_test.go:649-667`).
- **Trimming**: `strings.Trim(..., "_")` at `:924` removes leading *and* trailing underscores, so
  the result never leads or trails with `_`.
- **Cap**: `maxDispatchSlugRunes = 40` at `internal/cli/dispatch.go:62` (comment `:58-61`). The
  claim of 40 is correct.
- **Rune vs byte**: the cap is applied over `[]rune(slug)` (`:925-926`), so it is rune-counted.
  This is belt-and-braces rather than load-bearing: by the time the cap runs, every remaining rune
  is in `[a-z0-9_]`, i.e. one byte each, so runes == bytes at that point. The rune-awareness that
  *does* matter is in the loop at `:912` (`for _, r := range`), which makes a multi-byte character
  produce one `_`, not one per byte — `"café"` -> `"caf"` (test `dispatch_test.go:612`; the `é`
  becomes a trailing `_` which the trim then eats).
- **Post-cap trim**: only `TrimRight` (`:926`), since a leading `_` cannot exist after `:924`.
- **Empty result**: `""` is returned when nothing usable remains (`"!!!"` -> `""`, test
  `dispatch_test.go:610`), and the caller falls back to slug-less behavior. Note the asymmetry with
  `niwa create`, which *rejects* an unusable `--name` with an error
  (`internal/cli/create.go:114-119`) while `niwa dispatch` silently falls back.

The function is shared by `niwa dispatch` (`dispatch.go:458`), `niwa create`
(`internal/cli/create.go:115`), and `niwa watch` (`internal/cli/watch.go:781`).

### 3. Which function builds the instance directory name, and its exact shape

Two steps, in this order:

1. `dispatchNameSuffix(slug)` at `internal/cli/dispatch.go:879-888` returns
   `slug + "-" + <8hex>` when the slug is non-empty, and `"-" + <8hex>` when it is empty.
2. `computeInstanceName(configName, customName, sep, workspaceRoot)` at
   `internal/cli/create.go:82-106` does the join. With a non-empty `customName` it is a
   one-liner: `return configName + sep + customName, nil` (`create.go:83-85`). Dispatch passes
   `sep = "+"` (`internal/cli/dispatch.go:468`, `const sep = "+"`).

`computeInstanceName` is reached through `realProvisionInstance`
(`internal/cli/instance_from_hook.go:428`, call at `:467`), which dispatch invokes via the
`provisionInstanceFunc` indirection at `internal/cli/dispatch.go:476`.

So the shape is **`<config>+<slug>-<8hex>`** — the claim is correct — and `<config>+-<8hex>` in
the slug-less case. Pinned by `internal/cli/dispatch_test.go:705`
(`^test-ws\+my_thing-[0-9a-f]{8}$`).

Important: `computeInstanceName`'s custom-name branch does **no existence check** — it does not
stat the directory and does not scan for a free slot (that scan exists only on the no-custom-name
branch, `create.go:87-105`). Uniqueness of the dispatch directory rests *entirely* on the random
hex.

### 4. Where the 8 hex digits come from, and whether the suffix is used anywhere else

`internal/cli/dispatch.go:879-888`:

```go
func dispatchNameSuffix(slug string) (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	suffix := "-" + hex.EncodeToString(b[:])
	...
}
```

- **Entropy source**: `crypto/rand` (imported at `internal/cli/dispatch.go:4`), 4 bytes = 32 bits,
  hex-encoded to 8 lowercase digits (`:880-884`). Chosen over a numbered scan or a timestamp for
  lock-free concurrency safety — `docs/designs/current/DESIGN-instance-dispatch.md:150-158`
  (Decision D2).
- **Where the return value goes**: `namePrefix` at `internal/cli/dispatch.go:459` is passed to
  `provisionInstanceFunc` at `:476` and **nowhere else** (`grep -n "namePrefix" internal/cli/dispatch.go`
  returns only `:459` and `:476`).
- **Downstream reuse of the hex**: only as part of the finished instance name. `res.Name` is used
  once, at `internal/cli/dispatch.go:763`, to fill `SessionMapping.InstanceName`. The reaper's
  backstop matches the name structurally with
  `dispatchInstanceNameRe = regexp.MustCompile(`\+[a-z0-9_]*-[0-9a-f]{8}$`)`
  (`internal/cli/dispatch.go:941`, predicate `isDispatchInstanceName` at `:956-958`).
- **The hex never reaches the agent's argv.** It is not in the passthrough, not in the prompt, not
  in any flag. The only way a worker could see it is by reading its own cwd.

### 5. Which flag carries the display name, and the mapping from `--name` to it

The intent is declared as a field on `LaunchFlags`:

- `internal/agentplan/dispatch.go:247-248` — `// DisplayName gives the session a human-readable name.` / `DisplayName string`.

The per-agent table `launchSpecs` is at `internal/agentplan/dispatch.go:347`:

| Agent | Binary | Display-name flag | Cite |
|---|---|---|---|
| `agent.AgentClaude` (`"claude"`) | `claude` | `--name` | `internal/agentplan/dispatch.go:348-349`, `:356` |
| `agent.AgentCodex` (`"codex"`) | `codex` | **none** (field absent from the struct literal, so `""`) | `internal/agentplan/dispatch.go:387-388`, `:414-421` |

Claude's row also declares `LeadingArgs: []string{"--bg"}` (`:350`), so the worker command is
`claude --bg ... --name <slug> <prompt>`. Codex's row deliberately omits `DisplayName`, with the
comment at `internal/agentplan/dispatch.go:419-421` saying a niwa-side intent the agent has no
flag for is dropped rather than guessed at. Those are the only two agents (`internal/agent/agent.go:28-35`).

The mapping happens in `buildDispatchPassthrough`
(`internal/cli/dispatch.go:974-991`; doc comment `:961-973`), in the flag/value table at `:979-984`:

```go
{flags.Model, model},
{flags.PermissionMode, dispatchPermissionMode},
{flags.SubagentType, dispatchAgent},
{flags.DisplayName, slug},          // dispatch.go:985
```

with the emit guard at `:986-988`: `if pair.flag != "" && pair.value != ""`. So for Claude the
argv gains the two discrete elements `"--name"`, `slug`; for Codex the pair is dropped because
`flags.DisplayName == ""`; and for either agent an empty slug forwards nothing.

**The full path, end to end (all in `internal/cli/dispatch.go` unless noted):**

```
--name <raw>                          :26   flag registration
  -> dispatchName                     :45   package var
  -> slug := sanitizeInstanceSlug(..) :458  the ONE read of dispatchName
       |-> namePrefix := dispatchNameSuffix(slug)        :459  slug + "-" + 8hex
       |     -> provisionInstanceFunc(..., namePrefix, "+", ...)  :476
       |         -> computeInstanceName -> configName + "+" + namePrefix   create.go:84
       |             => instance dir  <config>+<slug>-<8hex>
       `-> buildDispatchPassthrough(spec.Flags, slug, resolvedModel)  :564
             -> {flags.DisplayName, slug}                :985   ("--name", slug)
             -> launchRequest{Passthrough: passthrough}  :675
               -> dispatchLaunch -> buildLaunchArgs(..., req.Passthrough)
                                                 dispatch_launcher.go:134
                 -> args = append(args, passthrough...)  dispatch_launcher.go:371
                   => claude --bg ... --name <slug> <prompt>
```

Nothing between `:458` and the exec mutates `slug`. `buildLaunchArgs`
(`internal/cli/dispatch_launcher.go:364-376`) splices the passthrough in verbatim at `:371`; the
only value it rewrites is the workdir grant's `%q` (`:369`, `formatWorkdirGrant` at `:381-392`),
and the only value the launcher edits is the *prompt* (spill handling,
`internal/cli/dispatch_launcher.go:112-116`). Flags are never string-concatenated — each value is
its own argv element by deliberate design (D8; comment at `dispatch_launcher.go:361-363`).

### 6. Is uniqueness added anywhere on that path? — No. Proof.

Negative claims are hard, so here is the closed enumeration:

- `dispatchName` is read exactly once in non-test code (`:458`); `grep -rn "dispatchName\b"` over
  `internal/` returns that line, the flag registration, the var declaration, and five test hits.
- `sanitizeInstanceSlug` (`:910-929`) is a pure function of its input: no clock, no randomness, no
  filesystem, no counter, no package state. Same input, same output, always.
- `slug` reaches `buildDispatchPassthrough` at `:564` as the same variable assigned at `:458` — no
  reassignment in between (the only intervening writes in that region are to
  `dispatchPermissionMode`, `:555`).
- `buildDispatchPassthrough` (`:974-991`) appends `slug` **unchanged**; it neither reads
  `namePrefix` nor takes it as a parameter. The hex-bearing string is not in its scope.
- Later appends to `passthrough` are `--settings` for remote control (`:598`) and, on other paths,
  `--strict-mcp-config` / `--resume` (`internal/cli/watch.go:577-579`, `:838-840`) — none touches
  the `--name` value.
- No collision detection exists anywhere: `WriteSessionMapping`
  (`internal/workspace/session_map.go:126-152`) keys on the session UUID and does a plain
  MkdirAll/WriteFile/Rename — it never enumerates existing mappings, so it cannot notice that a
  live session already carries the same display name.
- `computeInstanceName`'s custom-name branch (`create.go:83-85`) does not check for an existing
  directory either; only the random hex separates two same-named dispatches.

The design document says the quiet part explicitly at
`docs/designs/current/DESIGN-instance-dispatch.md:172-174`: "The random 8-hex is always kept, so
the structural signature the reaper backstop matches ... is preserved and **concurrency stays
collision-safe even when two dispatches share a `--name`**." That sentence is about the *instance
directory*, and it is true of the instance directory. It says nothing about the session display
name, which the same paragraph (`:164-168`) sends to the agent as the bare slug. The collision
safety the design claims and the collision safety the session name needs are two different
properties, and only the first one was designed for.

### 7. Minimal reproduction argument

Take a workspace whose config name is `tsuku`, and run twice, concurrently or back to back:

```
niwa dispatch "review PR 1" --name review
niwa dispatch "review PR 2" --name review
```

Both invocations compute `sanitizeInstanceSlug("review") == "review"` — the input is already in
`[a-z0-9]`, under 40 runes, so it passes through untouched (`dispatch.go:910-929`).

- **Instance directories: different.** Each call draws its own 4 random bytes at
  `dispatch.go:881`, so the names are e.g. `tsuku+review-4e33acfa` and `tsuku+review-9b17c0d2`
  (`create.go:84`). No conflict on disk; both match `isDispatchInstanceName` (`dispatch.go:941`).
- **Session display names: identical.** Both workers are launched as
  `claude --bg --name review <prompt>` (`dispatch.go:985` -> `dispatch_launcher.go:371`), because
  the passthrough carries `slug`, not `namePrefix`. The two live Claude Code sessions therefore
  share the string `review` as their display name.
- **Session mappings: distinct but unhelpful.** Each mapping is keyed on its own session UUID
  (`dispatch.go:761-772`), so niwa can still tell the two apart. But niwa's disambiguator (the
  UUID, or the instance name at `:763`) is not the string the agent-side name resolution uses.

With `--harness codex`, the same two commands produce two distinct instance directories and **no**
display name at all on either worker, because Codex's spec declares no `DisplayName` flag
(`internal/agentplan/dispatch.go:414-421`) and `buildDispatchPassthrough` drops the pair
(`dispatch.go:986`). So the collision is Claude-Code-specific in its current form.

## Implications

1. The fix has an obvious, cheap shape: the uniqueness already exists at
   `internal/cli/dispatch.go:459` in `namePrefix` (`<slug>-<8hex>`). Passing `namePrefix` instead of
   `slug` at `:564` would make the display name unique and keep it human-readable
   (`review-4e33acfa`), at the cost of 9 extra characters in Agent View. That is a one-argument
   change plus test updates — no new entropy, no new state, no new lookup.
2. But that one-line framing hides a real design question about **length and readability**. The
   slug cap is 40 runes (`:62`) chosen for the *directory name* budget; appending `-<8hex>` to the
   display name pushes a maxed-out name to 49 characters in whatever UI lists sessions. If the
   display name needs its own cap, the fix touches `sanitizeInstanceSlug`'s contract, which is
   shared with `niwa create` (`create.go:115`) and `niwa watch` (`watch.go:781`) — so any change to
   the function itself has three callers, while a change confined to `buildDispatchPassthrough`'s
   argument has one.
3. **The repo already contains the precedent.** `niwa watch`'s continuation path passes
   `rec.Handle` — the agent's own per-session job-directory name, which is unique — as the display
   name (`internal/cli/watch.go:576`). So "a unique per-session token in the display-name slot" is
   already a shape this codebase ships; dispatch is the odd one out.
4. **`niwa watch --once` has the same bug, and worse.** `stageReview` builds its slug
   deterministically from the PR coordinates — `sanitizeInstanceSlug(fmt.Sprintf("watch-%s-%s-%d", ...))`
   at `internal/cli/watch.go:781` -> `watch_owner_repo_123` — and forwards that as the display name
   at `:836`. Two review sessions for the same PR (across watch runs, or after a handled-set reset)
   collide by *construction*, not by coincidence. Any fix scoped to `niwa dispatch` alone leaves
   this path collided.
5. Nothing in `niwa` itself resolves a session by display name — the mesh that once did was removed
   wholesale, and the surviving addressing key is the session UUID
   (`internal/workspace/session_map.go:126-152`). So the blast radius is entirely on the agent side:
   niwa's own `list`, `attach`, `stop`, and reap paths are unaffected by the collision and will not
   notice it. That also means niwa has no way to *detect* the collision today; it would have to
   enumerate live mappings and compare a field it does not currently store.

## Surprises

- **The design document actively asserts the safety that does not hold.**
  `DESIGN-instance-dispatch.md:172-174` reads "concurrency stays collision-safe even when two
  dispatches share a `--name`". Read in the section that also describes forwarding the slug to the
  session (`:164-168`), it invites exactly the wrong conclusion. The sentence is true about
  directories and silent about sessions. Whatever fix lands should correct this paragraph, or the
  next reader re-derives the same false confidence.
- **The claimed "capped at 40 runes" is technically rune-counted but the distinction is moot.** By
  the time the cap runs (`:925`), every surviving rune is single-byte. The rune-awareness that
  actually changes behavior is one line earlier, in the range loop at `:912`, where a multi-byte
  character collapses to a single `_` rather than one per byte.
- **`dispatch` and `create` disagree on unusable names.** `niwa create --name "!!!"` is a hard
  error (`create.go:116-118`); `niwa dispatch --name "!!!"` silently falls back to no name at all
  (`dispatch.go:458` plus the empty-value guard at `:986`), so the worker gets no display name and
  the developer is never told. Pinned as intended behavior by
  `TestDispatch_NameSanitizesEmpty_FallsBack` (`dispatch_test.go:786+`).
- **The collision is agent-dependent.** Codex workers get no display name whatsoever
  (`internal/agentplan/dispatch.go:414-421`). Any framing of this as "niwa gives sessions colliding
  names" needs the qualifier "on Claude Code"; on Codex the same flag is inert.
- The `--label` flag (`dispatch.go:25`, recorded at `:769`) is a freeform alias with no uniqueness
  requirement either, but since it lives only in niwa's mapping file and never reaches an agent, it
  is not part of this problem.

## Open Questions

1. **What is Claude Code's actual name-resolution behavior on a duplicate?** The exploration
   premise is that the session name is the address a peer uses, and that a message for the first
   lands on the second. Nothing in the niwa repo can confirm or refute this — the name leaves niwa
   at `exec` and its semantics are entirely the harness's. Needs either the harness's own docs or a
   live two-session experiment. The severity of the whole finding hinges on this.
2. **Does Claude Code dedupe or reject a duplicate `--name` itself?** If it silently suffixes or
   refuses the launch, the niwa-side symptom changes shape (a failed dispatch rather than a
   misrouted message), and so does the right fix.
3. **Should the unique display name be `<slug>-<8hex>` (reuse `namePrefix`), or should it carry a
   shorter human-facing discriminator?** This is a UI-legibility call with no technical constraint
   forcing it, so it wants a human decision.
4. **Should the fix cover `niwa watch`'s deterministic slug (`watch.go:781`, `:836`) in the same
   change?** Its collision is by construction rather than by coincidence, but its blast radius is
   narrower (review sessions only) and its handled-set may already prevent re-staging in practice —
   unverified.
5. **Is a same-name refusal wanted as well as, or instead of, a unique suffix?** niwa could
   enumerate live mappings and warn on a duplicate, but no mapping field currently stores the slug
   (`SessionMapping` at `dispatch.go:761-772` carries `InstanceName` and `Label`, not the slug), so
   this would need a new field.

## Summary

The collision is real and the path is short: `--name` is read exactly once
(`internal/cli/dispatch.go:458`), sanitized to `[a-z0-9_]` capped at 40 runes
(`:910-929`, `:62`), and then forked — the 8 crypto/rand hex digits (`:881-884`) go only into the
instance directory `<config>+<slug>-<8hex>` (`create.go:84`), while the bare slug goes to the
worker as `claude --bg --name <slug>` (`:985` via the Claude row at
`internal/agentplan/dispatch.go:356`), so two `niwa dispatch --name review` calls get two distinct
directories and one shared session name. The uniqueness needed for a fix already exists nine lines
away in `namePrefix`, and `niwa watch` already sets the precedent by passing a unique per-session
handle in the same slot (`internal/cli/watch.go:576`) — while `watch`'s own staging path
(`:781`, `:836`) has the identical bug by construction. The biggest open question is agent-side and
unanswerable from this repo: what Claude Code actually does when two live sessions share a name,
which decides whether this is a misrouting bug or a cosmetic one.
