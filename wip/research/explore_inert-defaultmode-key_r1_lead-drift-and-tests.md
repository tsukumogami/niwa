# Lead: What depends on the key's presence — apply, drift, verification, tests

Baseline: `go test ./internal/workspace/ ./internal/cli/ ./internal/agentplan/ ./internal/watch/`
is green on this worktree (16.5s / 9.9s / 0.05s / 0.36s). Nothing below is a
pre-existing failure.

## Findings

### 1. Apply and drift

**Drift is whole-file content hashing, not key-level comparison, and it is
advisory.**

`internal/workspace/state.go:619-635` — `CheckDrift` re-hashes the file on disk
(SHA-256, `HashFile` at `state.go:601`) and compares it to
`ManagedFile.ContentHash` recorded in `.niwa/instance.json`. It knows nothing
about JSON keys.

`internal/workspace/apply.go:660-670` runs that check *before* re-materializing:

```go
// Check drift on existing managed files before overwriting.
for _, mf := range existingState.ManagedFiles {
    drift, err := CheckDrift(mf)
    ...
    if drift.Drifted() && !drift.FileRemoved {
        a.Reporter.DeferWarn("managed file %s has been modified outside niwa", mf.Path)
    }
}
```

It is a deferred warning. It never blocks the apply, and the file is
overwritten and re-hashed downstream (`hashManagedFile`, `apply.go:2237-2255`).

**Consequence for this exploration: a niwa-side change to what the document
says produces NO drift warning.** The on-disk file at check time is still
whatever the previous niwa version wrote, so it still matches the recorded
hash. Only *external* edits trip the warning. Renaming, removing, or annotating
the key is invisible to drift.

**There is no manifest, no "managed by niwa" marker inside the document, and no
schema/parity check that reads the written settings back.** Confirmed by
grepping `internal/workspace/preflight.go`, `validate.go`, `strict.go`,
`scan.go` for `settings.json` / `buildSettingsDoc` — zero hits. `Provenance`
(`internal/workspace/provenance.go:22-38`) is about the *config snapshot*
source identity, not materialized outputs.

**Source fingerprinting tracks the INPUT key name, not the OUTPUT key name.**
`internal/workspace/materialize.go:1288-1294`:

```go
for _, k := range sortedKeysSettings(settings) {
    v := settings[k]
    sources = append(sources, sourceForMaybeSecret("workspace.toml:claude.settings."+k, v))
}
```

The recorded `SourceID` is `workspace.toml:claude.settings.permissions` — the
TOML key. Renaming the emitted JSON key does not move the `SourceFingerprint`.
Renaming the TOML key would (and would also break every fixture and doc that
writes `permissions = "bypass"`).

**Instance-root settings.json IS a managed file; workspace-root settings.json
is NOT.** `internal/agentplan/settings.go:70-73` (`SettingsScope.managed()`),
pinned by `internal/agentplan/settings_test.go:36-40`:

```go
{SettingsAtWorkspaceRoot, filepath.Join(dir, ".claude", "settings.json"), false},
{SettingsAtInstanceRoot,  filepath.Join(dir, ".claude", "settings.json"), true},
{SettingsInRepo,          filepath.Join(dir, ".claude", "settings.local.json"), true},
```

So the file the dispatch derivation reads back (instance root) is
drift-tracked; the file a root-launched/ephemeral session loads (workspace
root) is not.

**A golden-hash characterization test pins the exact bytes of four settings
documents.** `internal/workspace/testdata/characterization/instance_managed_files.txt`:

```
.claude/settings.json	10558d382cb0a517b159ee4a830c00334bcc793a9f897e3514be953a03c724de
private/secrets/.claude/settings.local.json	fdc68968b5a12f95fc4016b336d89636aa5e7b4721e0f0015569814f76964f75
public/app/.claude/settings.local.json	00a48e5e65240f8b3f2416ce95d777f829613d5b51018b8893f84fcc28c71f01
```

plus `worktree_files.txt:3` for `.niwa/worktrees/app-ship/.claude/settings.local.json`.
The fixture declares `permissions = "bypass"`
(`internal/workspace/characterization_test.go:350-351`), and `compare`
(`characterization_test.go:613-634`) fails on any mismatch, regenerated only
with `NIWA_UPDATE_CHARACTERIZATION=1`. **This is the single test that breaks
under *every* candidate change**, including the "harmless" one of adding a
sibling annotation key — it is byte-exact, not key-aware.

**A real verification pass does exist, but it belongs to `niwa watch`, not
apply.** `internal/watch/containment.go:290-345`, `VerifyReviewSettings`:

```go
if mode, _ := perms["defaultMode"].(string); mode != "default" {
    return fmt.Errorf("review settings check: permissions.defaultMode must be \"default\" under the operator-approval posture, got %q", mode)
}
```

This is a hard error that aborts a PR-review launch. Its writer,
`ApplyReviewSettings` (`containment.go:201-279`), read-modify-writes
`<instancePath>/.claude/settings.json` — the *same file* the materializer owns
and the dispatch derivation reads — and re-reads it to self-verify
(`containment.go:269-278`). Callers: `internal/cli/watch.go:557` and `:831`.
Note the value it writes is `"default"`, a *restrictive* mode, which project
scope still honors; that half of the key is live, not inert.

### 2. Unit tests

Ten call sites across seven files. For each: what it asserts, then breakage
under (a) rename the key, (b) remove the key, (c) keep the key + add a sibling
annotation.

| File:line | What it asserts | (a) rename | (b) remove | (c) annotate |
|---|---|---|---|---|
| `internal/workspace/materialize_test.go:389` | After `SettingsMaterializer.Materialize` with `permissions = "bypass"`, unmarshals the repo's `settings.local.json` and requires `doc["permissions"]["defaultMode"] == "bypassPermissions"` | breaks | breaks | passes |
| `internal/workspace/materialize_test.go:434` | Same for `permissions = "ask"` → `"askPermissions"` | breaks | breaks | passes |
| `internal/workspace/materialize_test.go:571` | Same bypass assertion inside the hooks+permissions combined test | breaks | breaks | passes |
| `internal/workspace/materialize_worktree_test.go:190-195` | `TestSettingsMaterializerWorktreeUnsupportedPreservesDefaultMode`: with `WorktreeDelegation{Supported:false}`, requires `perms["defaultMode"] == "bypassPermissions"` **and** `perms["deny"]` present in the same map | breaks | breaks | passes |
| `internal/workspace/root_materializer_test.go:63-65` | Workspace-root `settings.json` from `MaterializeWorkspaceRoot` carries `permissions.defaultMode == "bypassPermissions"` (alongside `ephemeralSessionMode == true`, `:69`) | breaks | breaks | passes |
| `internal/workspace/apply_test.go:1040` | Substring assertion on the file text: `assertFileContains(t, settingsPath, `"defaultMode": "bypassPermissions"`)` — literal, including the JSON spacing | breaks | breaks | passes |
| `internal/workspace/permissions_test.go:9-64` | `TestWorkerPermissionMode` — six table cases feeding hand-written JSON bodies (`{"permissions":{"defaultMode":"bypassPermissions"}}` etc.) to `WorkerPermissionMode` | breaks only if the *reader* is changed | same | passes |
| `internal/workspace/worktree_secret_ref_test.go:104` | `strings.Contains(string(data), "bypassPermissions")` on the worktree's `settings.local.json` — value only, key name never mentioned | **passes** | breaks | passes |
| `internal/workspace/characterization_test.go:140` + `:199` (golden manifests) | Byte-exact SHA-256 of four materialized settings documents | breaks | breaks | **breaks** |
| `internal/cli/dispatch_permissionmode_test.go` (7 tests, lines 18, 28, 54, 85, 93, 120, 172, 223, 252) | Fixture JSON bodies written directly to disk by `provisionWithInstanceSettings`; assert on the derived **argv** (`--permission-mode bypassPermissions`) and the stderr notice | breaks only if the *reader* changes | same | passes |
| `internal/agentplan/settings_test.go:16, 25` | JSON marshalling shape only — `defaultMode` is arbitrary sample data the test supplies itself | passes | passes | passes |
| `internal/watch/containment_test.go:75, 140, 181, 211, 242, 248, 260` | `ApplyReviewSettings`/`VerifyReviewSettings` — must NOT set `defaultMode` in hard-deny posture; MUST set `defaultMode == "default"` in ask posture | breaks **if the watch key is renamed too** | breaks | passes |
| `internal/watch/adversarial_test.go:138` | Comment only, no assertion on the key | passes | passes | passes |

Two entries in the lead's file list do not assert on the key at all:
`internal/cli/dispatch_launcher_test.go` and `internal/cli/dispatch_test.go`
have zero matches for `defaultMode`/`bypassPermissions`, and
`internal/workspace/posture_test.go` is about **Codex** `config.toml` posture
delivery (`generatedCodexPayload`, `posture_test.go:24-46`), not Claude
settings.

**The most important structural fact here: no unit test binds the producer to
the consumer.** `provisionWithInstanceSettings`
(`internal/cli/dispatch_wiring_remotecontrol_test.go:33-53`) stubs
`provisionInstanceFunc` and writes a hand-authored `settings.json` body:

```go
if settingsBody != "" {
    ...
    os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settingsBody), 0o644)
}
```

The materializer never runs in those tests. So if the materializer stopped
writing `permissions.defaultMode` tomorrow, **every one of the seven
`dispatch_permissionmode_test.go` tests would still pass** — they would be
testing a reader against a fixture nothing produces.

### 3. Functional tests

`test/functional/features/dispatch.feature` carries two `@critical` scenarios,
introduced by a comment block that already states the premise this exploration
is built on (`dispatch.feature:41-49`):

```gherkin
  # --- Permission-mode derivation from the workspace's declared posture ---
  #
  # Claude Code 2.1.258 stopped honoring permissions.defaultMode from a
  # project's materialized .claude/settings.json; --permission-mode is one of
  # the two channels still honored. This is niwa's regression fix: a workspace
  # that declares permissions = "bypass" gets that posture forwarded to the
  # launched worker via --permission-mode, restoring pre-2.1.258 behavior.
  #
  # Design: docs/designs/current/DESIGN-dispatch-permission-mode.md
```

Scenario 1 (`dispatch.feature:51-68`):

```gherkin
  @critical
  Scenario: dispatch derives --permission-mode from a bypass-declared workspace
    ...
      [claude.settings]
      permissions = "bypass"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "12121212-1212-4212-8212-121212121212"
    When I run "niwa dispatch bypass-task --detach" from the workspace root
    Then the exit code is 0
    And the launched claude was invoked with "--permission-mode bypassPermissions"
```

Scenario 2 (`dispatch.feature:70-88`) adds
`--permission-mode acceptEdits` and asserts
`the launched claude was invoked with "--permission-mode acceptEdits"` plus
`the launched claude was not invoked with "bypassPermissions"`.

**What they assert: the derived argv only — never the on-disk key.** The step
implementations (`test/functional/dispatch_steps_test.go:425-458`) read
`$HOME/dispatch-launch-argv` and do a `strings.Contains` on the recorded argv.
Nothing opens `settings.json`.

**But they are genuinely end-to-end**, and that is what makes them the binding
constraint. The chain is real `niwa init` → real `RootSettingsMaterializer`
writes `settings.json` → real `niwa dispatch` → `readInstanceSettings` →
derivation. These two scenarios are the *only* place in the suite where the
producer and the consumer of this key meet. Change the emitted key name without
changing `instanceSettings.Permissions.DefaultMode`
(`internal/cli/dispatch_plugins.go:200-205`), or stop emitting it, and scenario
1 goes red.

`test/functional/features/workspace-config-sources.feature` has one relevant
`@critical` scenario (`:139-165`, the issue #214 same-apply reconcile test).
Its settings assertion is the last line:

```gherkin
    # A single apply must materialize the new posture -- not require a second run.
    And the file ".claude/settings.json" under the workspace root contains "bypassPermissions"
```

The step (`test/functional/steps_workspace_config_sources_test.go:141-155`) is
a raw `strings.Contains` on the file bytes — **it matches the VALUE, not the
key**. Renaming `defaultMode` to a niwa-owned name while keeping the value
string `"bypassPermissions"` would leave this scenario green. Removing the
value from the document breaks it — and note this scenario is really about
same-apply reconcile ordering, so it needs a substitute observable, not
deletion.

Two more feature files touch the surrounding file without touching the key:
`prewarm-settings-drift.feature:44` asserts
`the stderr does not contain "settings.json has been modified outside niwa"`
(the drift warning from `apply.go:667`), and
`worktree-delegation.feature:171-172` asserts root-settings hook commands.

### 4. Docs that would go stale

**`docs/guides/ephemeral-session-instances.md:327-334` — the worst offender.**
This is a user-facing guide making a behavioral promise that is now false:

> The root `.claude/settings.json` also carries a **permission posture**
> (`permissions.defaultMode`), sourced the same way instance materialization
> sources it. Note its scope: settings resolve at launch and cannot be scoped per
> session, so a root-level bypass-permissions posture applies to **every** session
> launched at the root, not only dispatched workers. This is wider than
> per-instance bypass; the opt-in ephemeral mode bounds it to workspaces that
> chose the feature.

The paragraph's entire point — that this is *wider* than per-instance bypass
and needs bounding — is inverted. The root settings key governs nothing;
`--permission-mode` reaches only the dispatched worker, and there is no
derivation for interactive root sessions at all.

**`docs/designs/current/DESIGN-mcp-root-instance-distribution.md:222-231` —
a decision resting on the dead behavior.** Decision 5, "MCP trust prompt",
chose *no code* on this reasoning:

> A workspace that sets `permissions = "bypass"` runs the root and instance sessions
> in `bypassPermissions` mode, under which Claude Code does not gate project MCP
> servers behind a per-session trust prompt. That covers the motivating case
> without new niwa code.

Root and interactive instance sessions no longer run in `bypassPermissions`
from this channel, so the motivating case is no longer covered. This is a
decision whose premise expired, not just a stale sentence.

**`docs/designs/current/DESIGN-workspace-root-claude.md:112-115` — an
experimental finding that no longer holds:**

> **Experimentally verified:**
> - `settings.json` with `bypassPermissions`: works in non-git (3/3 runs)

**`docs/guides/file-distribution.md:71-72`:**

> This is the existing settings surface (it maps to Claude Code's
> `bypassPermissions` mode); it is independent of the file tables above.

"maps to Claude Code's `bypassPermissions` mode" is the exact claim the
document can no longer keep.

**`docs/designs/current/DESIGN-config-distribution.md:169-172`** describes the
mechanism neutrally and would only need a pointer, not a rewrite:

> 2. Build Claude Code settings.local.json:
>    - `permissions.defaultMode` from settings `permissions` key

**`docs/designs/current/DESIGN-ephemeral-session-instances.md:100-101` and
`:376-378`** call it "the permission posture (`permissions.defaultMode`)" in a
component list — same category: naming, not a false claim.

**`docs/designs/current/DESIGN-agent-capability-contract.md:472-480` and
`:1230-1232`** use the Claude-side mapping as the *parity precedent* for
shipping Codex trust:

> A workspace author can already relax a developer's Claude Code approval
> posture through workspace config today. Declaring the second agent's
> equivalent unavailable on security grounds, while the first agent's ships,
> would build in exactly the asymmetry this work exists to remove

The premise survives (dispatch still relaxes the posture) but the citation
`materialize.go:669` no longer points at a channel Claude honors — the honored
channel is the dispatch flag. Worth a footnote so the parity argument isn't
read as resting on a dead mechanism.

**`docs/designs/current/DESIGN-dispatch-permission-mode.md` is already
correct.** It states the problem plainly at `:88-92` ("which since Claude Code
2.1.258 no longer includes the project `.claude/settings.json`'s
`permissions.defaultMode` value") and at `:305-318`, and it already records at
`:369-376` that `WorkerPermissionMode` is dead code. Nothing there goes stale
under any candidate; only its `permissions.defaultMode` path references would
need updating under a rename.

**`README.md` never mentions the key.** No config reference document exists in
`docs/` — the closest thing is `DESIGN-config-distribution.md` and the guides.

Archived docs (`docs/designs/archive/DESIGN-worker-permissions.md`, six hits)
and PRDs (`PRD-config-distribution.md:152,197,248`,
`PRD-ephemeral-session-instances.md:126,236`) are historical records and should
not be edited.

### 5. Blast radius table

| Breaks under → | (a) keep key, annotate sibling | (b) rename to niwa-owned key | (c) move signal to niwa metadata, stop writing key | (d) move key into `--settings` payload |
|---|---|---|---|---|
| `characterization_test.go` goldens (4 documents) | **BREAKS** (regen `NIWA_UPDATE_CHARACTERIZATION=1`) | **BREAKS** (regen) | **BREAKS** (regen) | **BREAKS** (regen; document shrinks) |
| `materialize_test.go:389,434,571` | ok | **BREAKS** ×3 | **BREAKS** ×3 | **BREAKS** ×3 |
| `materialize_worktree_test.go:190` | ok | **BREAKS** | **BREAKS** | **BREAKS** |
| `root_materializer_test.go:63` | ok | **BREAKS** | **BREAKS** | **BREAKS** |
| `apply_test.go:1040` (literal `"defaultMode": "bypassPermissions"`) | ok | **BREAKS** | **BREAKS** | **BREAKS** |
| `worktree_secret_ref_test.go:104` (value substring) | ok | ok | **BREAKS** | **BREAKS** |
| `permissions_test.go` (`WorkerPermissionMode`) | ok | ok if reader untouched | ok if reader untouched; the function is already dead — delete it with its test | ok |
| `dispatch_permissionmode_test.go` (7 tests, hand-written fixtures) | ok | ok until fixtures are corrected — **silently stale** | ok until fixtures are corrected — **silently stale** | **BREAKS** (argv now carries a `--settings` payload; also collides with remote control) |
| `watch/containment_test.go` (7 assertions) | ok | ok if the watch writer keeps `defaultMode` (it must: `"default"` is still honored) | ok, same reason | ok |
| `agentplan/settings_test.go` | ok | ok | ok | ok |
| `dispatch.feature` @critical ×2 | ok | **BREAKS unless reader renamed in lockstep** | **BREAKS unless reader repointed in lockstep** | ok if the payload still reaches the reader |
| `workspace-config-sources.feature:165` (value substring) | ok | ok (value string preserved) | **BREAKS** — needs a substitute observable for the same-apply reconcile assertion | **BREAKS** if the root document loses the value |
| `guides/ephemeral-session-instances.md:327-334` | **stale, must rewrite** | **stale, must rewrite** | **stale, must rewrite** | must rewrite (root sessions still unfixed) |
| `DESIGN-mcp-root-instance-distribution.md:222-231` | **premise expired, must revisit** | same | same | same |
| `DESIGN-workspace-root-claude.md:112-115` | **stale** | **stale** | **stale** | **stale** |
| `guides/file-distribution.md:71-72` | **stale** | **stale** | **stale** | **stale** |
| `DESIGN-config-distribution.md:171` | ok | **must update** | **must update** | **must update** |
| `DESIGN-ephemeral-session-instances.md:101,378` | ok | **must update** | **must update** | **must update** |
| `DESIGN-agent-capability-contract.md:475,1232` | footnote | **must update citation** | **must update citation** | **must update citation** |
| `DESIGN-dispatch-permission-mode.md` | ok | **must update path refs** | **must update path refs** | superseded in part |
| **Approximate cost** | 1 golden regen + 4 doc rewrites | 1 golden regen, 6 unit tests, 2 functional scenarios (lockstep), 8 docs | 1 golden regen, 7 unit tests, 3 functional scenarios (one needs a new observable), 8 docs, plus `WorkerPermissionMode` deletion | everything in (c) plus the single `--settings` argv slot already taken by remote control |

The four doc rows marked "stale" are stale **today**, under every option
including doing nothing. They are not a cost of change; they are outstanding
debt the change is an opportunity to clear.

## Implications

1. **Nothing in apply, drift, or verification depends on the key being
   present.** Drift is a whole-file hash checked before the overwrite, so
   niwa's own output changing is invisible to it; there is no manifest, no
   marker, no schema check on the written document. The only structural cost
   at the apply layer is regenerating two golden manifests — cheap, but it
   fires under *every* option including a comment-only annotation.

2. **The `@critical` `dispatch.feature` scenarios are the real contract, and
   they are argv-only.** They never look at the file, but they run the real
   materializer and the real dispatch, so they enforce
   producer↔consumer agreement without pinning the wire format. That means
   **any rename or relocation is safe as long as the producer and reader move
   in one commit** — the scenario is the exact regression net you want. It also
   means these two scenarios (and only these) must be run to have any
   confidence at all.

3. **The unit tests are the weak spot, not the strong one.** Every dispatch
   permission-mode test writes its own fixture bytes; none exercises the
   materializer. Under a rename or a relocation they go green while testing
   nothing real. If the change lands, those fixtures must be updated
   deliberately — CI will not tell you.

4. **`niwa watch` bounds candidate (c).** `ApplyReviewSettings` /
   `VerifyReviewSettings` write and hard-verify `permissions.defaultMode ==
   "default"` in the same instance-root `settings.json`. `"default"` is a
   *restrictive* mode, which project scope still honors, so that use is live
   and correct. Any "stop writing `permissions.defaultMode`" framing must be
   narrowed to "stop writing the *permissive* values" — the key itself has a
   legitimate resident.

5. **niwa already has the precedent for a niwa-owned key inside the settings
   document.** `ephemeralSessionMode` (`root_materializer.go:276`) and
   `keepAliveOnDispatch` (`materialize.go:725-740`,
   `internal/config/config.go:459`) are both niwa-defined keys Claude Code
   ignores, written into `settings.json` and read back by
   `readInstanceSettings` (`dispatch_plugins.go:185-206`). Candidate (b) — a
   niwa-owned key name — is not a new pattern; it is the pattern this file
   already uses twice. Its incremental cost over (a) is six unit tests and one
   lockstep edit.

6. **Candidate (c) is more expensive than it looks** and buys less than it
   seems: it costs a new observable for the `workspace-config-sources`
   `@critical` scenario, whose actual subject is same-apply reconcile ordering,
   not permissions.

## Surprises

- **`WorkerPermissionMode` is dead.** Grep for callers across the whole tree
  returns only `permissions.go` itself and `permissions_test.go`. Its doc
  comment still says "reads the coordinator's materialized permission mode"
  — mesh vocabulary from code niwa deleted. `DESIGN-dispatch-permission-mode.md:369-376`
  already documents this as "considered and rejected: reusing
  `WorkerPermissionMode` ... It is dead code (its only caller, the removed mesh
  daemon, is gone)" — and then the code was left in place with its test. That
  is a whole file and a 64-line table test maintaining a function nothing
  calls.

- **`niwa watch` writes into a niwa-managed, drift-tracked file.**
  `ApplyReviewSettings` (`containment.go:201-267`) read-modify-writes
  `<instancePath>/.claude/settings.json` with `os.WriteFile`, and that path is
  recorded as a `ManagedFile` (`SettingsAtInstanceRoot.managed() == true`). The
  next `niwa apply` on that instance will report "modified outside niwa" — the
  very warning `prewarm-settings-drift.feature` exists to prevent. Out of scope
  for this lead, but it is a latent instance of exactly the bug #179 fixed for
  plugin pre-warm.

- **Two functional assertions match the *value* `bypassPermissions`, not the
  key.** `workspace-config-sources.feature:165` and
  `worktree_secret_ref_test.go:104` would both stay green through a key rename.
  Convenient here, but it means the suite's coverage of "the posture reached
  the document" is weaker than it reads.

- **`internal/workspace/posture_test.go`, named in the lead, is about Codex.**
  It asserts the generated `.codex/config.toml` carries *no* posture when none
  is declared (`posture_test.go:47-60`) — a nice piece of prior art for the
  "absent by default" property, but unrelated to `defaultMode`.

- **The two-space-indented literal in `apply_test.go:1040`**
  (`"defaultMode": "bypassPermissions"`) pins JSON formatting incidentally.
  A change to `SettingsPlan`'s marshalling would break it, which is also what
  `agentplan/settings_test.go:25` exists to pin deliberately.

## Open Questions

- Does anything outside this repo read `permissions.defaultMode` out of a
  niwa-materialized settings document — a shirabe skill, a hook script, the
  `tsukumogami` plugin? Grep here covers `niwa` only.
- Is `ephemeralSessionMode` in the workspace-root document read by anything, or
  is it write-only like the key under study? `root_materializer.go:276` writes
  it; `readInstanceSettings` does not project it. If it is write-only, the
  "niwa writes inert keys into settings.json" pattern is broader than one key.
- Should `WorkerPermissionMode` and `permissions_test.go` simply be deleted as
  part of whatever lands? That is a strictly-subtractive change no scenario
  covers, and it removes one of the two readers the scope document asks about.
- The `workspace-config-sources` `@critical` scenario uses the posture value as
  a convenient marker for "the reconcile happened on this apply". What is the
  right substitute observable if the posture stops appearing in the root
  document?

## Summary

Nothing in `niwa apply`, drift detection, or any verification pass depends on
`permissions.defaultMode` being in the written document — drift is an advisory
whole-file hash checked *before* the overwrite, so a niwa-side change to the
document is invisible to it, and there is no manifest, marker, or schema check;
the only apply-layer cost of any change is regenerating the two golden
characterization manifests, which pin the settings documents byte-exactly and
therefore break even under a comment-only annotation. The load-bearing coverage
is the pair of `@critical` scenarios in `dispatch.feature:51-88`, which run the
real materializer and the real dispatch and assert only the derived argv
(`--permission-mode bypassPermissions`), so a rename or relocation is safe if
producer and reader move in one commit — while the seven unit tests in
`dispatch_permissionmode_test.go` write their own fixture bytes and would go
silently stale, and `niwa watch`'s `VerifyReviewSettings` hard-fails on
`defaultMode == "default"` in the same file, so the key itself has a live
resident and cannot simply disappear. Four docs are already false today
regardless of what changes — most sharply
`docs/guides/ephemeral-session-instances.md:327-334`, which promises a
root-level bypass posture applies to every root session, and
`DESIGN-mcp-root-instance-distribution.md:222-231`, whose "no new code needed"
decision rests entirely on the expired behavior.
