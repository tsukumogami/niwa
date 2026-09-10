# Lead: What do niwa's dispatch layout and spelling tests actually forbid, and which implementation shapes stay legal for a change to the dispatch naming path?

All line numbers below were read from the tree at
`public/niwa/.claude/worktrees/session-name-collision`, branch `docs/session-name-collision`.
The whole guardrail family is green on this tree (verified by running the named tests).

## Findings

### 1. `internal/cli/dispatch_layout_test.go` — the dispatch-path scan

An AST scan (not grep), so comments are never violations and formatting cannot hide a
violation. Six tests.

**Scope.** `dispatchPathFiles` (`internal/cli/dispatch_layout_test.go:40-50`) is the strict
list: `dispatch.go`, `dispatch_reentry.go`, `dispatch_capture.go`, `dispatch_keepalive.go`,
`dispatch_launcher.go`, `dispatch_model.go`, `dispatch_remotecontrol.go`, `dispatch_spill.go`,
`session_records.go`. **`dispatch.go` is on this list**, which is the file the candidate fix
touches. A listed file missing from disk is a hard `t.Fatalf` (`internal/cli/dispatch_layout_test.go:156`,
and again in `TestDispatchPathFilesAreAllPresent`, `internal/cli/dispatch_layout_test.go:579-593`).

**Rule A — no agent discriminator constant** (`TestDispatchPathNamesNoAgentConstant`,
`internal/cli/dispatch_layout_test.go:309-318`). Deny list
`dispatchAgentConstants = {"AgentClaude", "AgentCodex"}` (`internal/cli/dispatch_layout_test.go:78`).
The detector (`agentConstantViolations`, `internal/cli/dispatch_layout_test.go:256-283`) resolves
the *file's own* import binding for `github.com/tsukumogami/niwa/internal/agent`
(`internal/cli/dispatch_layout_test.go:182`, `resolveAgentPackageBinding`
`internal/cli/dispatch_layout_test.go:206-237`), so `import ag ".../agent"; ag.AgentCodex` and a
dot import both fail (`TestDispatchScanFollowsAnImportAlias`,
`internal/cli/dispatch_layout_test.go:434-481`). A *different* package legitimately named `agent`
does not trigger it (`internal/cli/dispatch_layout_test.go:467-480`). Failure message: `"the
dispatch path names an agent constant at N site(s); which agent is launched is data resolved once
and threaded, not a branch:"` (`internal/cli/dispatch_layout_test.go:315`).

**Rule B — no agent-naming string literal** (`TestDispatchPathNamesNoAgentLiteral`,
`internal/cli/dispatch_layout_test.go:325-334`). Deny list `dispatchAgentLiterals`
(`internal/cli/dispatch_layout_test.go:97-111`), matched on **whole unquoted literal values**, in
two groups:

- agent identity: `"claude"`, `"codex"`, `".claude"`, `".codex"`, `"CLAUDE.md"`, `"AGENTS.md"`
- session-record schema: `"CODEX_HOME"`, `"jobs"`, `"state.json"`, `"rollout-*.jsonl"`,
  `"sessionId"` (`internal/cli/dispatch_layout_test.go:106-110`)

Substring containment is explicitly legal — `"claude-code-is-not-the-binary-name"` is pinned as
a non-violation (`internal/cli/dispatch_layout_test.go:357-359`). `"sessions"` is deliberately
**not** on the list (`internal/cli/dispatch_layout_test.go:113-122`), because niwa has its own
`.niwa/sessions`. Failure message: `"the dispatch path names an agent's own binary or state
directory at N site(s); both belong to the agent's launch declaration:"`
(`internal/cli/dispatch_layout_test.go:331`).

**Rule C — package-wide completeness guard** (`TestNoUnreviewedAgentNamingInThisPackage`,
`internal/cli/dispatch_layout_test.go:534-573`). Ranges over *every* non-test `.go` file in
`internal/cli`. Dispatch-path files are skipped (they are held to the stricter A+B); everything
else must either be clean or appear in `excusedAgentNamingFiles`
(`internal/cli/dispatch_layout_test.go:497-513`) with a prose reason naming the declaration row
that makes it one agent's: `dispatch_plugins.go`, `job_state.go`, `instance_from_hook.go`,
`repo_resolve.go`, `watch.go`. A file cannot be both scanned and excused
(`internal/cli/dispatch_layout_test.go:589-591`).

**Rule D — only `dispatch_reentry.go` may read the re-entry fields**
(`TestOnlyTheReentryFileReadsTheReentryFields`, `internal/cli/dispatch_layout_test.go:647-682`).
`reentryFields = {"ResumeArgs", "HintVerbs"}` (`internal/cli/dispatch_layout_test.go:604`); the
detector matches on the *selector name alone*, with no type resolution
(`reentryFieldViolations`, `internal/cli/dispatch_layout_test.go:615-632`). So
`anything.ResumeArgs` in any non-test file other than `dispatch_reentry.go` fails. Note this is a
**field-name** deny list, not a "struct fields are off limits" rule — `Flags`,
`Flags.DisplayName`, `Binary`, `Records` are all freely readable elsewhere.

**Controls.** `TestDispatchScanDetectsWhatItForbids`
(`internal/cli/dispatch_layout_test.go:345-384`), `TestDispatchScanDetectsARecordSchemaTable`
(`internal/cli/dispatch_layout_test.go:406-426`), `TestDispatchScanFollowsAnImportAlias`
(`internal/cli/dispatch_layout_test.go:434-481`), `TestReentryFieldRuleDetectsWhatItForbids`
(`internal/cli/dispatch_layout_test.go:688-717`). Each asserts an exact violation count against
synthetic source and asserts nothing was flagged in a comment.

**What is NOT forbidden.** Flag spellings. `"--name"`, `"--permission-mode"`, `"--model"`,
`"--agent"`, `"--settings"`, `"--label"` appear in no deny list. Neither does `"name"`.

### 2. `internal/agentplan/layout_scan_test.go` — the sibling scan

Same technique, two different targets (`internal/agentplan/layout_scan_test.go:93-95`:
`workspaceDir = "../workspace"`, `leafDir = "."`).

- `TestWorkspaceNamesNoAgentConstant` (`internal/agentplan/layout_scan_test.go:218-233`):
  `internal/workspace` may not name `AgentClaude`/`AgentCodex` (`agentConstants`,
  `internal/agentplan/layout_scan_test.go:65`). Weaker than the CLI version — it compares the
  package identifier against the literal `"agent"` (`internal/agentplan/layout_scan_test.go:223`)
  and so does **not** follow an import alias.
- `TestWorkspaceNamesNoAgentContextFilename` (`internal/agentplan/layout_scan_test.go:239-244`):
  `internal/workspace` may not name `CLAUDE.md`, `CLAUDE.local.md`, `AGENTS.md`,
  `AGENTS.override.md` (`agentContextFilenames`, `internal/agentplan/layout_scan_test.go:55-60`).
  No exemption list exists any more (`internal/agentplan/layout_scan_test.go:26-49` records that
  it was deleted rather than emptied).
- `TestLeafNeverWrites` (`internal/agentplan/layout_scan_test.go:296-315`): `internal/agentplan`
  may not reference `exec.*` at all, nor
  `os.{Chmod,Create,Link,MkdirAll,Mkdir,O_APPEND,O_CREATE,O_RDWR,O_TRUNC,O_WRONLY,Remove,RemoveAll,Rename,Symlink,Truncate,WriteFile}`
  (`forbiddenOSNames`, `internal/agentplan/layout_scan_test.go:73-90`). Read-only access stays
  legal (`internal/agentplan/layout_scan_test.go:70-72`). **This is what keeps `agentplan`
  data-only: a naming fix could not do any I/O there even if it wanted to.**
- Control: `TestContextFilenameScanReportsALiteral` (`internal/agentplan/layout_scan_test.go:260-290`).

### 3. `internal/agentplan/dispatch_spelling_test.go` — the one hand-typed table

`TestLaunchSpecSpellingsAreLiteral` (`internal/agentplan/dispatch_spelling_test.go:38-78`) is the
deliberate change-detector. It writes out, by hand, each agent's `Binary`, `ResumeArgs` and
`HintVerbs`: Claude = `claude` / `["attach"]` / `["attach","logs","stop"]`; Codex = `codex` /
`["resume"]` / `["resume"]` (`internal/agentplan/dispatch_spelling_test.go:39-52`). It also fails
if an agent gains a `launchSpecs` row with no case here
(`internal/agentplan/dispatch_spelling_test.go:74-78`).

Critically: **it pins `Binary`, `ResumeArgs`, `HintVerbs` — and nothing else.** It does not pin
`Flags.DisplayName`, `Flags.Model`, `LeadingArgs`, `Records`, or any other spec field. So the
`--name` spelling in `launchSpecs` (`internal/agentplan/dispatch.go:356`) is unpinned by this
test. A change that does not alter the flag spelling table doesn't touch this file at all.

### 4. `internal/agentplan/contract_test.go` — the declaration table's structural suite

Five tests, all written against the table rather than against a named agent:

- `TestDeclarationTableCoversEveryPairExactlyOnce` (`internal/agentplan/contract_test.go:22-56`):
  every (capability, agent) pair has exactly one row;
  `len(declarations) == len(All()) * len(agent.All())`. **Adding a capability constant to the
  catalog without adding 2 rows fails immediately.**
- `TestDeclarationsAreWellFormed` (`internal/agentplan/contract_test.go:61-87`): implemented rows
  carry zero `Kind`/`Reason`; unavailable rows carry both, with `Kind` inside
  `{AgentCannotReceive, NoSuchConcept, NotBuilt}`, and no `Requires`.
- `TestRequiresIsClosed` (`internal/agentplan/contract_test.go:93-109`),
  `TestRequiresGraphIsAcyclic` (`internal/agentplan/contract_test.go:180-227`).
- `TestHookDeliveredRowsRestOnTheHooksRow` (`internal/agentplan/contract_test.go:133-175`):
  derives the hook-delivered set from `Requires` and binds those rows' reason kind and wording to
  the `Hooks` row.

### 5. `internal/agentplan/gate_test.go` — the per-scope enabled gate

`TestGatedProducerDeclaresNothing` (`internal/agentplan/gate_test.go:14-59`),
`TestGateDoesNotChangeTheDeclarationTable` (`internal/agentplan/gate_test.go:66-83`),
`TestOpenGateMatchesUngated` (`internal/agentplan/gate_test.go:88-104`). This is about
`[claude] enabled` / `[codex] enabled` zeroing plan entries. **It has no bearing on the dispatch
naming path** — gates cover plan-borne writes, and `DispatchLaunch` is `RouteLaunch`
(`internal/agentplan/capability.go:194`), consulted rather than written.

### 6. `internal/cli/dispatch_contract_test.go` — behavioural contract

Ten tests. The relevant properties:

- Nothing here asserts the exact `--name` value. The one test that compares a whole passthrough
  slice computes the expectation by calling the builder itself
  (`internal/cli/dispatch_wiring_remotecontrol_test.go:127`).
- `TestDispatchUsesTheResolvedAgentsSpec` (`internal/cli/dispatch_contract_test.go:99-172`)
  substitutes an invented spec (with `Flags.Model: "--pick"`) and asserts the dispatch follows
  *it*, and that stdout contains no `"claude "`
  (`internal/cli/dispatch_contract_test.go:166`). A change that appends a suffix to the
  display-name value would not be observed here — the invented spec declares no `DisplayName`
  (`internal/cli/dispatch_contract_test.go:106-116`).
- `TestDispatchSharedHalfRunsForEveryAgent` (`internal/cli/dispatch_contract_test.go:185-274`),
  `TestDispatchPreflightsTheDeclaredBinary` (`internal/cli/dispatch_contract_test.go:380-407`),
  `TestDispatchHintsComeFromTheDeclaration` (`internal/cli/dispatch_contract_test.go:437-462`),
  `TestDispatchResumeUsesTheDeclaredVerb` (`internal/cli/dispatch_contract_test.go:468-501`) —
  all read expectations off the spec.
- `TestDispatchWarnsWhenTheWorkerStartsUnoriented`
  (`internal/cli/dispatch_contract_test.go:293-336`) is the model for a
  **predicate-over-the-table** gate: `agentplan.ConfigDocRepoScoped(ag)`
  (`internal/cli/dispatch_contract_test.go:315`), with the comment at
  `internal/cli/dispatch_contract_test.go:308-314` stating explicitly that "a name check here is
  the hardcoded branch the contract exists to prevent."

### 7. Precise restatement of the constraint

The stated framing ("dispatch-path files must not name an agent, either as a constant or as a
string literal; gate on FLAG SPELLINGS or CAPABILITY LOOKUPS instead") is **correct, and
narrower than it sounds.** Precisely:

"Naming an agent" means, in a non-test file listed in `dispatchPathFiles`:

1. a selector or bare identifier resolving to `AgentClaude` or `AgentCodex` from
   `internal/agent`, under any import alias or a dot import; **or**
2. a string literal whose whole value is one of the eleven entries in `dispatchAgentLiterals`
   (`internal/cli/dispatch_layout_test.go:97-111`).

Everything else is legal. In particular a *flag spelling* comparison is legal and already in the
tree — `internal/cli/dispatch.go:551-554` reads:

```go
if dispatchPermissionMode == "" &&
    spec.Flags.PermissionMode == "--permission-mode" &&
    inst != nil && inst.Permissions != nil &&
    inst.Permissions.DefaultMode == "bypassPermissions" {
```

This lives in `dispatch.go`, a scanned file, and passes both scans because `"--permission-mode"`
is not on any deny list. It landed in commit `d25ad4d fix: dispatch-permission-mode (#277)`,
which is the concrete precedent for "gate on the flag spelling." Its own comment
(`internal/cli/dispatch.go:542-549`) argues the gate in terms of the flag's value vocabulary
rather than the agent's name.

### 8. The sanctioned mechanisms, in order of preference

**(a) Read the spec field and let empty mean "no such intent."** This is the default and is what
`buildDispatchPassthrough` already does (`internal/cli/dispatch.go:974-992`): a pair is emitted
only when `pair.flag != "" && pair.value != ""`. `LaunchFlags` documents the contract at
`internal/agentplan/dispatch.go:230-234` — "An empty value means the agent has no such flag and
the intent is dropped rather than guessed at." Codex declares no `DisplayName`
(`internal/agentplan/dispatch.go:414-425`), so nothing is forwarded to it, with no branch
anywhere.

**(b) Compare the flag spelling.** Precedent above (`internal/cli/dispatch.go:552`).

**(c) Export a predicate from `agentplan` over its own tables.** `ConfigDocRepoScoped`
(`internal/agentplan/rootskills.go:216-242`) is the worked example; its doc comment
(`internal/agentplan/rootskills.go:218-226`) is the canonical statement of when to reach for
this: "there is no declaration row that means it ... so the honest gate for that sentence is the
layout table itself, read through a predicate, rather than a row invented to carry it or the
agent's name spelled out at the call site." Consumed at `internal/cli/dispatch.go:420`, pinned at
`internal/agentplan/rootskills_test.go:85-101`.

**(d) Add a capability row.** `agentplan.Lookup(cap, ag)` returning a `Declaration`
(`internal/agentplan/declaration.go:387`, struct at `internal/agentplan/declaration.go:55-62`).
This is the heaviest option: it requires a `catalog` entry
(`internal/agentplan/capability.go:172-197`), two new `declarations` rows or the exhaustiveness
test fails, and a `Route`. **The comment at `internal/agentplan/capability.go:26-34` is explicit
that not everything gets a row** — a capability is "one thing a workspace preparation can deliver
to an agent session."

### 9. Where the naming fix legally belongs

The forwarded value is built here:

```go
// internal/cli/dispatch.go:974
func buildDispatchPassthrough(flags agentplan.LaunchFlags, slug, model string) []string {
	var pass []string
	for _, pair := range []struct{ flag, value string }{
		{flags.Model, model},
		{flags.PermissionMode, dispatchPermissionMode},
		{flags.SubagentType, dispatchAgent},
		{flags.DisplayName, slug},          // <- internal/cli/dispatch.go:985
	} {
		if pair.flag != "" && pair.value != "" {
			pass = append(pass, pair.flag, pair.value)
		}
	}
	return pass
}
```

and called from `runDispatch` at `internal/cli/dispatch.go:564`:
`passthrough := buildDispatchPassthrough(spec.Flags, slug, resolvedModel)`.

The 8-hex uniqueness suffix is **already computed in the same function, roughly a hundred lines
earlier**:

```go
// internal/cli/dispatch.go:458-459
slug := sanitizeInstanceSlug(dispatchName)
namePrefix, err := dispatchNameSuffix(slug)
```

`dispatchNameSuffix` (`internal/cli/dispatch.go:879-889`) returns `slug + "-" + 8hex` when the
slug is non-empty, and bare `"-" + 8hex` when it is not. `runDispatch` starts at
`internal/cli/dispatch.go:269` and runs past line 860, so `slug`, `namePrefix` and line 564 are
all in one scope. No plumbing is needed.

**The change belongs at the call site, `internal/cli/dispatch.go:564`, inside `runDispatch`** —
passing a suffixed value instead of the bare `slug`. That is legal: it names no agent, adds no
literal from any deny list, reads no re-entry field.

**It needs no capability and no predicate. It is agent-agnostic.** The `flags.DisplayName != ""`
test in the builder already does all the per-agent gating: Claude declares `--name`
(`internal/agentplan/dispatch.go:356`) and gets the suffixed value; Codex declares nothing
(`internal/agentplan/dispatch.go:414-425`) and gets nothing forwarded. Suffixing the value
regardless of which flag spelling carries it is the correct shape and matches how `model` and
`dispatchPermissionMode` are already handled.

**Do NOT put the suffix inside `buildDispatchPassthrough` itself.** Two reasons, both concrete:

1. The function has **three** callers, and two of them are `niwa watch`:
   `internal/cli/watch.go:576` (passes `rec.Handle` as the slug, for a resumed review session)
   and `internal/cli/watch.go:836` (passes a PR-derived slug from
   `internal/cli/watch.go:781`). Suffixing inside the builder silently renames watch's review
   sessions too.
2. The `pair.value != ""` guard at `internal/cli/dispatch.go:987` is what implements
   "no `--name` supplied means no `--name` forwarded." If a suffix is appended before that
   check, the value is never empty and `--name` starts being forwarded for every dispatch —
   which breaks two existing tests (see below) and changes documented behaviour.

**Hazard to design around:** with no `--name`, `namePrefix` is `"-<8hex>"`, a leading-dash
value. Passing that as an argv element after `--name` risks the agent's own flag parser reading
it as a flag. The conservative shape keeps today's fall-back — forward nothing when the slug is
empty — and forwards `slug + "-" + 8hex` only when the slug is non-empty. (Whether the agent's
`--name` accepts a dash inside the value is unverified here; see Open Questions.)

### 10. Tests that would need updating

- **`internal/cli/dispatch_test.go:737-739`** — `passthroughHasNameSlug(gotPass, "my_thing")`
  asserts the forwarded pair is exactly `--name my_thing`. This is the single test that pins the
  exact value, and it **will fail** on any suffix. Helper at
  `internal/cli/dispatch_test.go:825-832`.
- **`internal/cli/dispatch_test.go:745-781`** (`TestDispatch_NoName_NoSlugNoNameFlag`) and
  **`internal/cli/dispatch_test.go:786-821`** (`TestDispatch_NameSanitizesEmpty_FallsBack`) —
  both assert *no* `--name` element appears in the passthrough. These stay green under the
  conservative shape and go red if the suffix is applied unconditionally. They are the guard that
  catches mistake (2) above.
- **`internal/cli/dispatch_wiring_remotecontrol_test.go:127`** — compares against
  `buildDispatchPassthrough(claudeLaunchSpec().Flags, "", "")`. Computed, and the slug is empty
  there, so unaffected.
- No golden files are involved. `internal/workspace/testdata` is the only testdata directory and
  is unrelated.
- **No functional Gherkin scenario exercises `--name`.** `test/functional/features/dispatch.feature`
  never passes it (all its `name = "myws"` hits are workspace-config TOML, not the dispatch
  flag), and `test/functional/dispatch_steps_test.go` has no `--name` step. So nothing in
  `test/functional/` needs updating.
- `internal/cli/dispatch_launcher_test.go:34` uses `DisplayName: "--label"` in a synthetic spec
  but never passes a non-empty display-name value through it, so it is unaffected.
- Prose that would drift: the flag help text at `internal/cli/dispatch.go:26` ("optional display
  name for the session (sanitized into a slug ...)"), the builder doc comment at
  `internal/cli/dispatch.go:966-969` ("forwarded to the worker as `--name <slug>` so the launched
  claude session carries the same display name embedded in the instance directory" — note it
  already says "claude", but that is a comment and comments are never scan violations), and the
  generated workspace orientation text at `internal/workspace/root_materializer.go:434`.

## Implications

The guardrails do not obstruct the candidate fix at all. They forbid two very specific things in
nine specific files — agent discriminator constants, and eleven exact string values — and a
uniqueness suffix on a flag value is neither. The fix is one expression change at
`internal/cli/dispatch.go:564` inside `runDispatch`, using `namePrefix` (or a value derived from
the same hex) that is already in scope from `internal/cli/dispatch.go:459`.

No capability row is warranted. The `DispatchLaunch` row already exists
(`internal/agentplan/capability.go:194`), the per-agent difference is already expressed as
`LaunchFlags.DisplayName` being empty for Codex, and adding a row would trip
`TestDeclarationTableCoversEveryPairExactlyOnce` into demanding two new declarations for a fact
the flag table already carries. Reaching for a predicate like `ConfigDocRepoScoped` is likewise
unnecessary: that pattern exists for facts *no* existing table carries, and this fact
("does this agent have a display-name flag?") is exactly what `LaunchFlags` is.

The one real design constraint is not architectural but blast-radius: `buildDispatchPassthrough`
is shared with `niwa watch`, so the suffix has to be applied by the dispatch caller, not inside
the builder. And the empty-value guard at `internal/cli/dispatch.go:987` is load-bearing for the
documented "no `--name` means no `--name`" fall-back, so the suffix must be applied only to a
non-empty slug.

The test cost is one line: `internal/cli/dispatch_test.go:737`.

## Surprises

1. **`dispatch.go` is a scanned file, but the scan is far narrower than "no agent-specific
   behaviour."** The tree already contains an explicit per-agent behavioural branch in that
   file — `spec.Flags.PermissionMode == "--permission-mode"` at `internal/cli/dispatch.go:552`,
   which is Claude-only in practice — and it passes because it names a flag spelling rather than
   an agent. So "gate on flag spellings" is not a workaround; it is the sanctioned idiom with a
   commit behind it (`d25ad4d`, PR #277).

2. **`buildDispatchPassthrough` has three callers, two of them in `niwa watch`**
   (`internal/cli/watch.go:576`, `internal/cli/watch.go:836`). The stated framing treats the
   display-name forward as a dispatch concern; it is also a watch concern.
   `internal/cli/watch.go:576` passes `rec.Handle` as the display name, which means review
   sessions are already named by their handle rather than by a slug. This is not mentioned
   anywhere in the exploration context and is the main thing that makes "put the suffix in the
   builder" the wrong shape.

3. **`dispatch_spelling_test.go` does not pin `Flags.DisplayName`.** It pins only `Binary`,
   `ResumeArgs` and `HintVerbs` (`internal/agentplan/dispatch_spelling_test.go:39-52`). The
   `--name` spelling at `internal/agentplan/dispatch.go:356` is unpinned by any hand-written
   test. A typo there would be caught only by the functional suite.

4. **`gate_test.go` is unrelated to the dispatch naming path.** It covers the per-scope
   `enabled` gate over plan-borne writes; `DispatchLaunch` is `RouteLaunch`, consulted rather
   than written. It was in the lead's list but contributes no constraint here.

5. **The re-entry field rule (`ResumeArgs`, `HintVerbs`) matches on selector name with no type
   checking** (`internal/cli/dispatch_layout_test.go:615-632`). Any local variable or unrelated
   struct that happens to have a field called `HintVerbs` would fail the rule in any non-test
   file in `internal/cli` except `dispatch_reentry.go`. Worth knowing, but the naming fix touches
   neither field.

6. **`"sessions"` is deliberately absent from the literal deny list** while `"jobs"` is present
   (`internal/cli/dispatch_layout_test.go:113-122`), because niwa keeps its own sessions in
   `.niwa/sessions`. The rationale given — "a scan that has to be lied to in order to pass is
   worse than a scan with one fewer string in it" — is the clearest statement of how these
   exclusions are meant to be reasoned about.

## Open Questions

1. **Does the agent's `--name` accept a value containing a dash?** `sanitizeInstanceSlug`
   (`internal/cli/dispatch.go:910-929`) forces slugs dash-free, but that invariant exists for
   `isDispatchInstanceName`'s regex over *directory* names
   (`internal/cli/dispatch.go:941`, rationale at `internal/cli/dispatch.go:899-905`), not for the
   flag value. Whether `<slug>-<8hex>` is acceptable as a display name, and whether the dash or
   the hex confuses the peer-address lookup, needs measuring against the real binary.

2. **Is the display name really the peer address, and is it matched exactly or by prefix?** The
   exploration context asserts this. Nothing in the niwa tree references peer addressing (the
   agent-facing mesh was removed in niwa PR #151/#154). If matching is prefix- or
   substring-based, a suffix may not disambiguate; if exact, it does. This is the load-bearing
   premise of the whole fix and is unverified in this repo.

3. **Should `niwa watch`'s two call sites get the same treatment?**
   `internal/cli/watch.go:576` and `internal/cli/watch.go:836` produce display names by the same
   mechanism and can collide the same way (two watch passes on the same PR produce the same
   slug). Fixing only dispatch leaves that open. Deciding this is what determines whether the
   suffix belongs at one call site or three.

4. **What happens to the no-slug dispatch?** Today it forwards no display name at all, so every
   unnamed dispatch is anonymous rather than colliding. If the peer-address problem also applies
   to unnamed sessions, forwarding a bare hex might be wanted — but that changes documented
   behaviour and breaks two existing tests (`internal/cli/dispatch_test.go:745`,
   `internal/cli/dispatch_test.go:786`). Needs a product call.

5. **Does the 40-rune slug cap interact?** `maxDispatchSlugRunes = 40`
   (`internal/cli/dispatch.go:62`). A suffixed display name becomes up to 49 characters. Whether
   the agent truncates or rejects a long `--name` is unmeasured.

## Summary

The dispatch-path scan forbids exactly two things in nine named files — the `AgentClaude`/`AgentCodex`
constants under any import binding, and eleven whole-value string literals (agent names, their
dot-directories, their context filenames, and the five session-record schema fields) — and flag
spellings such as `"--name"` and `"--permission-mode"` are explicitly outside it, with
`internal/cli/dispatch.go:552` already shipping a flag-spelling gate in a scanned file. So
appending the 8-hex suffix to the display-name value is legal with no capability row and no
predicate: it belongs at the call site `internal/cli/dispatch.go:564` inside `runDispatch`, where
`namePrefix` from `internal/cli/dispatch.go:459` is already in scope, and it must NOT go inside
`buildDispatchPassthrough`, whose other two callers are `niwa watch` and whose empty-value guard
implements the documented "no `--name` means no `--name`" fall-back. The biggest open question is
whether the display name really is the peer address and whether it is matched exactly — nothing
in the niwa tree corroborates that premise, and a prefix or substring match would defeat a suffix
entirely.
