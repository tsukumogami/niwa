# Lead: Can the agent contract be a declarative plan?

## A. What the Materializers Actually Do

I read every writer the dual-agent surface touches on `main`
(`content.go`, `materialize.go`, `root_materializer.go`,
`worktree_content.go`, `workspace_context.go`, `apply.go`,
`internal/gitexclude/exclude.go`) and the prior attempt's Codex writers on
`origin/docs/dual-agent-workspace`. The headline result is that niwa has
**already separated "decide what to say" from "write it"** almost
everywhere, and it did so deliberately — several of the pure halves carry
doc comments saying so.

### Class 1: pure content computation (no I/O at all)

Every "what goes in the file" decision in the prep path is already a pure
function of config:

| Function | file:line | Produces |
|---|---|---|
| `buildSettingsDoc` | `materialize.go:654-940` | the whole `settings.json` / `settings.local.json` document as `map[string]any`; **performs no write** |
| `generateRootClaudeContent` | `root_materializer.go:386-429` | workspace-root context markdown from `cfg` |
| `generateWorkspaceContext` | `workspace_context.go:563` | instance `workspace-context.md` from `cfg` + classified repos |
| `expandVars` | `content.go:349-355` | template expansion, `strings.NewReplacer` only |
| `rootHoistableConfig` | `root_materializer.go:310-350` | the root-hoistable plugin/marketplace partition + report strings |
| `resolveWorkSummaryHooks` / `resolvePrBodyHook` | `materialize.go:517-602`, `:644-649` | which built-in hook registrations to inject |
| `permissionsMapping`, `hookEventMapping` | `materialize.go:297-309` | value/key translation tables |
| `supportsWorktreeHooks` | `harness_compat.go:49-57` | the version comparison, explicitly "the pure, unit-testable comparator that `SupportsWorktreeHooks` wraps around the exec call" |
| `worktreeFallbackDisclosure` | called `apply.go:1569`, described at `:1567` as "the pure … helper" | warn/explain booleans |
| `renderNiwaBlock`, `unionPatterns` | `gitexclude/exclude.go:149-188`, `:88-105` | the exclude-file bytes; doc states it is "pure and idempotent" |
| `stripWorktreeContextSection`, `renderWorktreeLayerBody` | `worktree_content.go:909`, `:813` | the worktree framing section body |

`renderContentFile` (`content.go:255-268`) is the interesting one: it reads a
source out of the **niwa config directory**, containment-checks it, expands
vars, and returns a string. Its doc comment (`content.go:248-254`) says it
"performs no write — callers that need to persist the result write the
returned string themselves," and that it exists precisely so the write-path
and render-to-string paths "cannot drift." That is a plan-shaped split
already shipped on `main`.

On the Codex branch the same shape recurs and is stronger:
`renderInstanceContextLayer`, `renderGroupContextLayer`,
`renderRepoContextLayer` (`codex_context.go`) are pure renders returning
strings, and `ComposeCodexContext` (`codex_compose.go`) is documented as "It
performs no write; the callers that materialize group files, per-repository
overrides, and worktree files own that."

**Count: ~13 distinct pure content computations on `main`, plus 5 more on the
Codex branch. All trivially expressible as data.**

### Class 2: filesystem write with niwa bookkeeping

This class is far thinner than the brief anticipated, because the
bookkeeping is not carried by the writers at all.

Every write site in scope has the identical three-line shape —
`os.MkdirAll(dir, 0o755)`, `os.WriteFile(target, data, mode)`, append
`target` to a `[]string`:

- `content.go:177`, `:192`, `:284`
- `workspace_context.go:132`, `:154`, `:174`, `:186`, `:219`, `:296`, `:360`, `:401`
- `root_materializer.go:213`, `:286`, `:376`
- `worktree_content.go:327`, `:772`
- `materialize.go:263` (+ `os.Chmod` 0o755 at `:267`), `:1238`, `:1593`, `:1856`

**21 write sites, three permission values** (`0o644`, `secretFileMode` =
`0o600` at `materialize.go:28`, and `0o755` for hook scripts).

The bookkeeping vocabulary a plan would have to reproduce is genuinely
small, because niwa's managed-file machinery is a **post-hoc pass over a
flat path list**, not something the writers participate in:

- `Materializer.Materialize` returns `([]string, error)` — just paths
  (`materialize.go:206-209`).
- Step 7 (`apply.go:1701-1718`) walks `writtenFiles []string`, hashes each,
  and builds `ManagedFile{Path, ContentHash, SourceFingerprint, Sources,
  Generated}` (`state.go:184-190`).
- `cleanRemovedFiles` (`apply.go:1846-1859`) deletes any prior-state path
  absent from the current produced set. That is the entire reconciliation.
- Provenance rides a **side channel**: `ctx.recordSources(path, []SourceEntry)`
  (`materialize.go:185-195`) into a shared `map[string][]SourceEntry`.
- git-exclude is **one call per repo** at the pipeline level
  (`apply.go:1631`), taking `repoDir` plus any custom secret-output names as
  extra patterns; `gitexclude.EnsureRepoExclude` is idempotent and no-ops
  outside a git repo (`exclude.go:54-83`).

So the executor needs to know, per entry: path, bytes, mode, whether the
path is tracked as a managed file, any extra exclude pattern it implies, and
its `[]SourceEntry`. Nothing else.

### Class 3: reads the filesystem/environment first and branches

Twelve operations, and they are not all equally hard:

**Preconditions on a declared path** (executor can run these universally, no
per-capability code):
1. `checkContainment` + `resolveExistingPrefix` (`content.go:294-343`) — symlink-escape check before every content write.
2. `safeTargetPath` (`materialize.go:1623-1666`) — Lstat walk over ancestors for config-declared env-output targets.
3. `matchedByBasePattern` + `gitexclude.IsGitRepo` fail-closed ordering for custom secret-output names (`worktree_content.go:236-243`, `exclude.go:111-114`).

**Input resolution from niwa-owned directories** (not target-tree probes):

4. `autoDiscoverRepoSource` (`content.go:221-235`) — `os.Stat` on `{content_dir}/repos/{repo}.md`.
5. `InstallOverlayClaudeContent` / `InstallGlobalClaudeContent` — `os.ReadFile` of the source, ENOENT means "no-op, not an error" (`workspace_context.go:210-216`, `:390-401`).

**Idempotent read-modify-write on niwa's own output, each with a closed rule:**

6. `appendToWorkspaceRulesFile` (`workspace_context.go:137-155`) — append an `@import` line unless already present.
7. `installWorktreeContextLayer` (`worktree_content.go:754-767`) — read `CLAUDE.local.md`, strip the delimited section via `stripWorktreeContextSection`, re-append. A *replace-delimited-section* rule.
8. `removeImportFromCLAUDE` (`workspace_context.go:159-175`) — migration-only removal.
9. `installWorktreeRulesImport` (`worktree_content.go:706`, `:712`) — `os.Stat` overlay/global paths, conditionally append. A *conditional entry*.

**Genuinely branching on foreign content in the target tree** — three, all on the Codex branch:

10. `DetectCodexConflicts` (`codex_conflict.go`) — `os.Lstat`, `git ls-files` (`gitTracksPath`), and a bounded first-line marker probe (`readCodexOwnerProbe`). **Already structured as a separate pre-pass returning a `CodexRepoVerdict` data value**, which the writers consult as a gate and the cleanup consults as a delete-exemption.
11. `readRegularFileNoFollow` (`codex_compose.go`) — `O_RDONLY|O_NOFOLLOW|O_NONBLOCK` open, fstat, read, of the repository's own committed `AGENTS.md`.
12. `codexLinkTargets` / `codexPayloadPathOwnership` (`codex_link.go`) — Readlink + EvalSymlinks staleness compare.

**Probe of the host environment:** `SupportsWorktreeHooks` (`harness_compat.go:36-43`) execs `claude --version`. It is already hoisted to a **once-per-apply pre-pass** whose result is frozen into the `WorktreeDelegation` struct (`apply.go:1544-1563`, type at `materialize.go:340-349`) and threaded to every repo. Another existing decision-as-data pass.

### Class 4: side effects outside the instance

Four, and none of them belongs in a plan:

1. `plugin.Install` → `~/.claude/plugins/marketplaces/niwa/` (`internal/plugin/installer.go:74`), already behind the `Applier.InstallNiwaPlugin` function-field seam (`apply.go:102-108`).
2. `healDanglingPluginRecords` (`apply.go:1734`) and `reconcileMarketplaceRegistry` (`apply.go:1741`) — mutate Claude Code's global registry, both fail-safe.
3. Codex branch: `EnsureCodexTrust` (`codex_trust.go:197`) writes the developer's own `~/.codex/config.toml` under a lock (`os.UserHomeDir` at `:79`, `:92`; `writeCodexConfig` at `:572`; `lockCodexTrust` at `:619`).
4. Subprocess calls: `git rev-parse --git-common-dir` (`exclude.go:121`), `git ls-files` (`codex_conflict.go`), `claude --version` (`harness_compat.go:37`).

### Tally

| Class | Count | Plan-expressible? |
|---|---|---|
| 1 — pure content | ~13 on main, +5 on the Codex branch | Yes, trivially |
| 2 — write + bookkeeping | 21 write sites, 3 modes | Yes; the bookkeeping vocabulary is 3 fields |
| 3 — read-then-branch | 12, of which 3 are preconditions, 2 input resolution, 4 closed-rule RMW, 3 foreign-content, 1 host probe | Yes for 9; the remaining 3 need named policies |
| 4 — outside the instance | 4 | No — and they already sit outside the materializer set |

## B. The Plan Type

Sketch, in a new leaf package (name it `internal/agentplan`):

```go
// Op is the closed set of primitive operations an agent's plan may declare.
// Adding a member is a design change, not an implementation detail.
type Op uint8

const (
    OpWriteFile      Op = iota // write Content at Path, mode Mode
    OpAppendLine               // append Content to Path unless already present
    OpReplaceSection           // replace the region delimited by Marker
    OpDeliverTree              // symlink Source at Path; copy on failure
)

// Precondition is the closed set of conditions gating an entry.
type Precondition uint8

const (
    Always          Precondition = iota
    IfSourceExists               // OpAppendLine's stat-then-append
    IfNotForeign                 // consult the ownership verdict
)

// Entry is one declared write.
type Entry struct {
    Op           Op
    Path         string       // absolute target
    Content      []byte       // OpWriteFile / OpAppendLine / OpReplaceSection
    Source       string       // OpDeliverTree, and IfSourceExists' probe path
    Mode         os.FileMode  // 0o600 | 0o644 | 0o755
    Marker       string       // OpReplaceSection delimiter
    Pre          Precondition
    Managed      bool         // participates in ManagedFiles + cleanRemovedFiles
    ExcludeAs    string       // extra gitexclude pattern implied, "" for none
    Sources      []SourceEntry // provenance, for SourceFingerprint
}

// Plan is one agent's whole declared output for one level.
type Plan struct {
    Entries  []Entry
    Exempt   []string // paths cleanup must not delete though not produced
    Warnings []string // conflicts, refusals, hoist omissions
}
```

**Does this reproduce the managed-file vocabulary, and is it big?** No, and
no. Three fields — `Managed`, `ExcludeAs`, `Sources` — cover everything
`apply.go` Step 7 + `cleanRemovedFiles` + `EnsureRepoExclude` consume today.
`Exempt` is the one genuinely new concept, and it exists only because the
Codex conflict rule needs "do not delete a path I refused to write"
(`codex_conflict.go`'s `Conflicted`, documented at length as the cleanup's
question). Nothing in `CheckDrift` (`state.go:660`), the state schema
(`state.go:184-218`), or `ComputeSourceFingerprint` (`state.go:230`) changes.

**One type has to move.** `SourceEntry` and `ComputeSourceFingerprint` live
in `internal/workspace/state.go:198-249`. They are four strings and a
sha256+sort; a leaf package cannot import `internal/workspace`, so either
they move down or the executor converts at the boundary. Moving them is the
cleaner call — they are already documented as pure metadata that "never
carry secret material" (`state.go:192-197`).

**One type is the real obstacle, and it is avoidable.** `EffectiveConfig` is
declared at `internal/workspace/override.go:17-37` — a workspace type. A
leaf plan producer cannot take it. But its fields are all `config.*` types
plus two `map[string]string` and a `[]string`, so it is movable; and the
cheaper answer, more in keeping with the house idiom of call-site-sized
inputs, is for the leaf producer to take a narrow input struct that
`internal/workspace` fills. `EffectiveConfig` appears 31 times across 10
non-test files, so moving it is measurable but not free — I recommend not
moving it.

## C. Where the Hard Cases Break It

The prior attempt's three hard cases split cleanly into "already
plan-shaped" and "needs one closed policy."

**Conflict detection — already plan-shaped, no change needed.**
`DetectCodexConflicts` (`codex_conflict.go`) is a detection pass that
returns `CodexRepoVerdict` and writes nothing, and its own doc says the
verdict "is the single ownership authority for the repository. The writers
consult it before writing and the managed-file cleanup consults it before
deleting, so a path cannot be foreign to one and niwa's to the other." That
is exactly a plan-producing pre-pass. The plan producer runs the detection,
emits no `Entry` for a conflicted path, appends a line to `Warnings`, and
adds the path to `Exempt`. The one wrinkle is that
`InstallRepoCodexLink` re-asks the ownership question at write time
(`codex_link.go`, deliberately, "closes the window where a foreign entry
appears between the detection pass and this write"). Under a plan that
becomes `Pre: IfNotForeign` — the executor re-checks, which is the same
belt-and-braces with the check moved into generic code.

**`O_NOFOLLOW` inlining — plan-shaped, with the read on the producer side.**
`readRegularFileNoFollow` is a *read of an input*, not a write. The composer
already does it and returns `CodexComposition{Content, Refusal}` — content
plus a report — with no write. So the plan producer performs the read, the
resulting bytes land in `Entry.Content`, and the refusal lands in
`Warnings`. This is a stdlib syscall (`os.OpenFile` with a build-tagged
`nofollowOpenFlags`), so a leaf package can do it. The TOCTOU window between
compose and write already exists today and is not widened.

**Symlink-or-copy tree delivery — this is the genuine break.**
`InstallRepoCodexLink` (`codex_link.go`) inspects, removes a stale link,
retargets, or recursively copies with a hop bound
(`copyCodexPayload`, `codexCopyMaxLinkHops = 4`) following symlinks. That is
not `{path, content, mode}` under any reading. It is also not arbitrary
code: it is one named, self-contained delivery discipline whose only
parameters are a source directory and a target path. It becomes
`OpDeliverTree{Source: <instance>/.codex, Path: <repo>/.codex}` and its ~150
lines move into the executor as the implementation of that one op. Note the
code itself already declares the result "deliberately not a managed file …
neither of which the managed-file pipeline can hash, so this writer
reconciles it against the payload itself on every apply" — so it sets
`Managed: false` and the plan model does not have to explain it.

**Verdict on C:** none of the three needs arbitrary functions at write time.
Two are already data-producing passes; the third needs one closed op kind.
The hybrid the brief hypothesized — "a small closed set of policies rather
than arbitrary functions" — is not a fallback here, it is what the code
already wants to be.

Two additional things worth folding into the closed set, because they exist
on `main` and would otherwise force ad-hoc code: `OpReplaceSection` (the
worktree context layer's stable-heading replace,
`worktree_content.go:754-767`) and `OpAppendLine` (the `@import` accumulation
in `.claude/rules/workspace-imports.md`,
`workspace_context.go:137-155`). With those, the op set is four, and the 21
`os.WriteFile` sites on `main` reduce to three ops plus a mode.

## D. Verdict and Recommendation

**Option 3, weighted hard toward option 1.** Concretely:

- A new leaf package (`internal/agentplan`, sibling to `internal/agent`,
  importing `internal/agent` and `internal/config` and nothing above them)
  holds three things: the closed capability enumeration and per-agent
  support matrix (pure data), the plan types above, and the per-agent plan
  producers — which are pure functions from narrow config inputs to a
  `Plan`, performing reads only of niwa-owned inputs and (for the inline
  case) a guarded read of a repository's own committed context file.
- `internal/workspace` gains one generic executor, `applyPlan(*Plan)
  ([]string, []string, error)` returning written paths and extra exclude
  patterns, implementing exactly four ops and three preconditions. It
  contains no agent name and no context filename.
- Everything class 4 stays exactly where it is: the plugin installer
  function-field seam, the registry heals, the trust write. They are not
  capabilities delivered *into* the workspace and the contract should not
  pretend otherwise.

This is not option 1 in the purest sense — `OpDeliverTree`'s implementation
is imperative code living in `internal/workspace` — but property 4 gets a
real package boundary anyway, because the *choice* of what to deliver, where,
and under what name is entirely in the leaf, and the executor is provably
agent-blind.

### What fails a test if someone regresses it

Three tests, all cheap, all stdlib, all runnable under the existing
`go test -race ./...` with no new dependency (r1 confirmed `go/ast`,
`go/parser`, `go/token` are stdlib and CI runs only `gofmt -l`, `go vet`,
`go test -race`):

**1. The layout test (property 4).** An AST scan asserting that no non-test
file in `internal/workspace` names `agent.AgentClaude`/`agent.AgentCodex` or
contains a string literal in `{"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md",
"AGENTS.override.md"}`. Status today: the constants half **passes** (the
only three occurrences are in comments — `root_materializer.go:95`,
`apply.go:46`, `worktree_content.go:440`). The literals half **fails today**,
at `content.go:156`, `:186`, `:208`, `worktree_content.go:743`,
`workspace_context.go:196`, `:229`, `:411`, and the dead const
`root_materializer.go:51` (`rootClaudeFile`, referenced only from
`root_materializer_test.go:135`). So the test is meaningful on the day it
lands rather than vacuously green.

**2. The plan-shape test (property 2, and the one that only the plan model
makes possible).** `Plan(agent, inputs)` returns a value. A pure table test —
no tmpdir, no filesystem, no built binary — can assert:
- for `AgentCodex`, no entry's `Path` ends in `CLAUDE.md` or `CLAUDE.local.md`;
- for `AgentClaude`, no entry's `Path` ends in `AGENTS.override.md`;
- every entry's `Op` is a member of the closed set;
- for every (agent, capability) pair, the support matrix returns
  Implemented, or Conditional/Unavailable **with a non-empty reason**, and an
  unlisted pair is a hard failure — the `vault.Registry.Build` fail-closed
  posture (`registry.go:88-114`), not `agent.known`'s honor system.

This is the test that catches PR #248's exact regression. That PR iterated a
hardcoded `materializedAgents` slice at every call site while
`Applier.Agent` was read by nothing; under a plan, `Plan(AgentCodex)`
returning any `CLAUDE.*` entry is a one-line assertion failure. Today the
equivalent check requires provisioning an instance in a tmpdir and walking
the tree.

**3. The wiring test.** Assert the executor's only entry point takes a
`*Plan` and that `internal/workspace` constructs no plan except through
`agentplan.Plan(ag, …)`. This is the structural answer to "the agent
parameter is threaded and read by nothing": if bytes can only reach disk via
a plan, and plans only come from a function taking `ag`, then `ag` is
load-bearing by construction. It is checkable with the same AST scan as test 1.

Option 2 (interface implemented inside `internal/workspace`, per-agent
files) can support test 1 but not tests 2 or 3 — its only observable is the
filesystem, so every assertion costs a tmpdir and an apply. It also invents
a `foo_claude.go`/`foo_codex.go` convention that r1 established has **no
precedent anywhere in this repo**; per-agent variation here is always either
a method with branches on `agent.Agent` or a map keyed by it.

## Strongest Objection, Answered

**The objection:** *You are introducing an intermediate representation for a
two-agent problem. That is the same over-abstraction that killed PR #248 —
an `Agent` seam threaded everywhere and load-bearing nowhere. Worse, the
plan type will grow a field every time a capability doesn't fit, and in a
year `applyPlan` is an interpreter for a config language nobody designed.*

**The answer, in three parts.**

First, this is not a new abstraction; it is the generalization of one niwa
has already built four separate times. `WorktreeDelegation`
(`materialize.go:340-349`) is a decision computed once and threaded as data
to writers. `CodexRepoVerdict` (`codex_conflict.go`) is a detection pass's
result consulted by both writers and cleanup. `InstalledHooks`
(`materialize.go:199-202`) is one materializer's output consumed as another's
input. `repoContextLayer` (`codex_context.go`) is a rendered layer passed to
two writers. Each was invented ad hoc for one capability. The plan is the
fifth instance, named once.

Second, the growth risk is real and the mitigation is the closed `Op` enum,
not good intentions. The evidence that four ops suffice is empirical, not
aspirational: 21 of the ~24 write sites on `main` are `MkdirAll` +
`WriteFile` with one of three modes, and each of the three that isn't
(`appendToWorkspaceRulesFile`, `installWorktreeContextLayer`,
`InstallRepoCodexLink`) is already a single named helper with a documented
rule. If a fifth op is ever needed, that is a design conversation — which is
the correct cost for adding a new way for niwa to touch a user's repository.

Third, PR #248 failed for a reason a package boundary does fix. Its `Agent`
was dead because the writers reached config directly and hardcoded the agent
constants beside it; the parameter was decorative. Under this shape there is
no path from config to bytes that bypasses `Plan(ag, …)`, and test 3 asserts
it. The seam is not "threaded and hopefully used" — it is the only route.

A second objection deserves naming: *the leaf package still performs reads
(the `O_NOFOLLOW` inline, `os.Stat` on overlay files), so it is not really
pure and the boundary is fuzzy.* True, and the boundary should be stated as
**reads inputs, declares outputs** rather than "pure." That is a defensible,
testable line: an AST check that the leaf package never calls
`os.WriteFile`, `os.MkdirAll`, `os.Symlink`, `os.Remove`, or `exec.Command`
is mechanical and exact.

## Adoption Cost

Scoped to what dual-agent capability actually touches; `internal/workspace`
is not rewritten.

**Changes:**
- New leaf package: plan types, op set, capability matrix, per-agent
  producers. New code, ~400 lines, no existing code to break.
- Move `SourceEntry` + `ComputeSourceFingerprint` out of
  `state.go:198-249` into the leaf (or a shared leaf). Mechanical; the JSON
  tags and the state schema do not change.
- New executor in `internal/workspace`, ~200 lines including
  `OpDeliverTree`'s tree copy (which on the Codex branch already exists as
  ~150 lines in `codex_link.go`).
- Convert the eight context writers to plan producers: `content.go`'s three
  installers, `workspace_context.go`'s `InstallWorkspaceContext` /
  `InstallOverlayClaudeContent` / `InstallGlobalClaudeContent`,
  `root_materializer.go:373` `writeRootClaudeMD`,
  `worktree_content.go:736` `installWorktreeContextLayer`. ~500 lines
  touched, mostly deletions of repeated `MkdirAll`+`WriteFile` pairs.
- `buildSettingsDoc` becomes a plan producer. It already returns a document
  and writes nothing; its three call sites (`root_materializer.go:246`,
  `workspace_context.go:~355`, `materialize.go:1206`) each carry their own
  copy of marshal → MkdirAll → WriteFile. Converting **deletes** two of the
  three copies. This is a net simplification, not a cost.
- `runPipeline` Step 7 (`apply.go:1701-1718`) reads `Managed` and `Sources`
  off plan entries instead of a bare `[]string` plus a side map. Roughly 20
  lines.
- `cleanRemovedFiles` (`apply.go:1846`) gains an `Exempt` consultation.
  Four lines.

**Explicitly not changed:** `runPipeline`'s step ordering, the `Materializer`
interface, `HooksMaterializer` / `EnvMaterializer` / `FilesMaterializer`
(config-driven, not agent-driven — leave them out of PR 1), `CheckDrift`,
the state schema, `MergeOverrides` / `MergeInstanceOverrides` and the rest of
`override.go`, `EffectiveConfig`'s location, the plugin-installer seam, the
registry heals.

**Honest risk:** the eight context writers carry accumulated boundary rules
(overlay append, subdir content, the `@import` migration removals) that a
mechanical conversion can drop. Two of them —
`InstallRepoContentTo` and `installWorktreeContextLayer` — are already
half-broken on `main` (they take `ag agent.Agent`, gate on
`WritesRepoLevelContext()`, then hardcode `"CLAUDE.local.md"`, leaving
`LocalContextFileName()` with zero callers module-wide). Converting them is
where the "no behavior change" claim is hardest to defend, and it is where
whatever mechanism r1 identified as missing for proving no-behavior-change
needs to be pointed.

## Open Questions

- Where do the `SourceEntry` type and `ComputeSourceFingerprint` land — the
  new leaf, or a separate tiny provenance leaf? The former is simpler; the
  latter matches how `envformat` and `keyreport` were carved out.
- Does the plan cover the *instance-root settings* writer
  (`InstallWorkspaceRootSettings`, `workspace_context.go:242`) in PR 1, or
  only the context writers? It takes no `agent.Agent` today and its hook-copy
  loop discards errors (`:291-296`) — a silent skip that a plan executor
  would surface, which is a behavior change even if a welcome one.
- `Exempt` exists solely for the Codex conflict rule. Should PR 1 introduce
  it (unused, therefore dead) or should PR 2 add it? Introducing an unused
  field is the same dead-seam smell the mandate is reacting to.
- `codexPathTracked` shells out to `git ls-files`. A leaf package running a
  subprocess is a new thing for the leaf tier — acceptable, given
  `internal/gitexclude` (a stdlib-only leaf) already execs `git rev-parse`,
  but it should be an explicit decision, not an accident.

## Summary

Yes, and the codebase is already most of the way there: every "what to say"
decision in the prep path is a pure function today (`buildSettingsDoc`
performs no write, `renderContentFile` is documented as render-only,
`ComposeCodexContext` and `DetectCodexConflicts` on the prior branch return
data rather than writing), while all 21 write sites in scope reduce to
MkdirAll+WriteFile with three permission values, and the managed-file
bookkeeping the plan must reproduce is only three fields because
reconciliation is a post-hoc pass over a flat path list. The one genuine
break is the Codex payload's symlink-or-recursive-copy delivery, which is
not expressible as content+path+mode but is one named discipline, so the
answer is a closed four-op set (write, append-line, replace-section,
deliver-tree) rather than arbitrary functions at write time. Recommend a
leaf `internal/agentplan` holding the capability matrix and per-agent plan
producers with a generic executor in `internal/workspace` — it is the only
option that makes property 2 and property 4 assertable in a pure table test
with no tmpdir, and it costs roughly 600 new lines plus the conversion of
eight context writers, while deleting two of the three duplicated
settings-write blocks.
