# Security Review: inert-defaultmode-key

Scope: `docs/designs/DESIGN-inert-defaultmode-key.md`, checked against its PRD,
the prior `docs/designs/current/DESIGN-dispatch-permission-mode.md`, and the
current code in `internal/cli/dispatch.go`, `internal/cli/dispatch_plugins.go`,
`internal/cli/watch.go`, `internal/watch/containment.go`,
`internal/workspace/{state.go,apply.go,materialize.go,permissions.go}`.

Bottom line: the design doesn't widen who can obtain `bypassPermissions`, and on
two paths it narrows it. No finding rises above Low. A few things should be
written into the design so an implementer, or a later feature, doesn't undo
the properties it depends on.

## Dimension Analysis

### External Artifact Handling
**Applies:** Yes, narrowly. Nothing is downloaded or executed, but the design
adds one new input to a privilege decision.

The new input is niwa's own `.niwa/instance.json`, read by
`workspace.LoadState` and compared by string equality to `"bypass"`. The rest
of the decision's inputs are the ones niwa already processes: `workspace.toml`,
the workspace overlay, the personal overlay, `[instance.claude.settings]`, and
any `vault://` value behind the `permissions` key.

- **Validation of the declared value.** The resolver runs the resolved
  plaintext through `claudeDefaultMode`, which accepts only `bypass` and `ask`
  and fails provisioning otherwise, before any document is written (Data
  Flow step 2). The persisted field can therefore only be `"bypass"`,
  `"ask"`, or empty.
- **Parsing the state file.** `LoadState` unmarshals into a fixed struct and
  rejects a forward schema version (state.go:260-278). Any value other than
  exactly `"bypass"` produces no flag. The forwarded value is the constant
  `"bypassPermissions"`, never the stored string, so a tampered field can't
  inject a flag value. Each argv element stays discrete (`buildDispatchPassthrough`,
  dispatch.go:974-992), so a tampered field can't inject a flag either.
- **The error path echoes the resolved plaintext.** Low severity; already
  true today, and the design copies it. Today's `buildSettingsDoc` returns
  `fmt.Errorf("unknown permissions value %q", permStr)`, where `permStr`
  comes from `maybeSecretString` (materialize.go:682-687). The design keeps
  that shape in `claudeDefaultMode` and adds a second caller,
  `instancePermissionsPosture(cfg)`, which takes no `context.Context`.

  The pipeline's redactor only scrubs errors built with `secret.Errorf` or
  `Wrap` from the context (apply.go:818-834). Plain `fmt.Errorf` is never
  scrubbed. So if a `vault://` reference behind `permissions` resolves to
  something that isn't `bypass`/`ask` (a mistyped path that points at a real
  secret, for example), the plaintext is printed. It lands on the terminal
  for create, apply, and dispatch. It lands in logs for `niwa watch`. It may
  reach session context through the provisioner the SessionStart hook shares.

  *Mitigation:* when the declared value is secret-backed (`IsSecret()`),
  leave the value out of the error: say that the vault-resolved `permissions`
  value isn't `"bypass"` or `"ask"`, and name the source. Apply this in both
  callers. Have the resolver return the literal constants `"bypass"`/`"ask"`
  rather than the revealed string, so no copy of a secret buffer is retained
  or persisted.

### Permission Scope
**Applies:** Yes. This is the dimension that matters for this design.

**1. The new grant source in `.niwa/instance.json` doesn't widen who can obtain bypass.**

- *Same declared inputs.* The recorded value comes from the same inputs the
  old path read indirectly: workspace overlay, workspace, personal overlay,
  then `[instance.claude.settings]`, with `MergeInstanceOverrides` precedence.
  Per-repo overrides don't count, as before. `RootSettingsMaterializer` runs
  unconditionally in `runPipeline` (apply.go:1587-1597), and the design routes
  both the document and the recorded field through one helper. So when nothing
  is tampered with, the set of workers that get bypass is identical to today,
  S1-S9 included.
- *Who can write the file.* `SaveState` writes `instance.json` with mode 0644
  in a 0755 `.niwa/` (state.go:280-297). The generated settings documents are
  0600 (`secretFileMode`, materialize.go:29). Neither is writable by another
  user. The only writers of either file are processes running as the
  developer: the developer, niwa, and any agent session running in the
  instance. That is the same population that could already write the old
  input, `.claude/settings.json`, in the same directory. Anything that can
  write either file can also run `claude --permission-mode bypassPermissions`
  directly, so neither file is a boundary between different levels of
  privilege.
- *Why a planted or edited value can't take effect today.* The derivation
  reads state that the same process wrote moments earlier. That state lives
  in a directory whose name ends in 8 hex characters from `crypto/rand`
  (dispatch.go:879-889), created by `Create`. `Create` builds `InstanceState`
  from scratch and overwrites the file (apply.go:569-587), so a value planted
  ahead of time is overwritten. Changing it would take a same-user process
  winning a race inside one `niwa dispatch` run.
- *Why a stale value can't survive.* `Apply` also rebuilds the struct from
  `pipelineResult` on every run (apply.go:763-781). If the new field is set
  from the result in both literals, a posture changed from `bypass` to
  undeclared can't leave a stale `"bypass"` behind. If an implementer forgets
  the field in `Apply`'s literal, the value falls to empty, which fails closed.
- *The property to write down.* All of this is safe only while the
  derivation reads state provisioned by the same process. Suppose a later
  feature derives posture for an *existing* instance: dispatching into an
  instance that already exists, a respawn, or reuse by `watch`.
  `instance.json` would then become a persistent grant input that any
  in-instance agent session could write, including one running in
  `acceptEdits`.

  Claude Code's own handling of edits to its `.claude/` settings files, if
  any, wouldn't cover `.niwa/instance.json`. Informational.

  *Mitigation:* state this in the design and in `derivePermissionMode`'s doc
  comment: the function is valid only for an instance the calling process
  just provisioned. Optionally, have it check that the loaded state's `Root`
  equals `instancePath`. Any future caller that reads posture for an existing
  instance should re-resolve it from config instead.

**2. The tamper-resistance claim holds.**

- *Overwrite case.* After this change, nothing on the dispatch path reads a
  permission mode out of `.claude/settings.json`. The `Permissions`
  projection on `instanceSettings` is removed (dispatch_plugins.go:198-204).
  `readInstanceSettings` stays only for remote control and keep-alive. A
  hand-edited `defaultMode: "bypassPermissions"` therefore grants nothing,
  which closes the one grant channel an in-instance agent could reach by
  editing a Claude settings file.
- *Deletion case.* Deleting the file no longer withholds bypass. That isn't
  an escalation, because the declaration is the authority and the flag
  simply follows it.
- *Caveat on scope.* The claim covers only what `niwa dispatch` forwards.
  An in-instance agent that can edit `.claude/settings.json` can still alter
  other behavior Claude Code reads from that file, such as hooks or deny
  rules. That was already true and isn't in scope here.
- *Deleting `WorkerPermissionMode`.* This removes a dead reader whose fallback
  returned `"acceptEdits"` whenever the file said anything but bypass
  (permissions.go:25-39). That stronger-than-declared default is gone for
  good.

**3. No path leaks a derived flag into a `niwa watch` launch.**

- *Today.* Both watch call sites call
  `buildDispatchPassthrough(claudeLaunchSpec().Flags, <slug-or-handle>, "")`:
  `stageReview` at watch.go:836 and `continueReview` at watch.go:576. The
  builder reads the package global `dispatchPermissionMode`, which only
  `runDispatch` and dispatch's Cobra flag write. Watch is empty today only
  because a watch process never sets the global.
- *After the change.* The builder takes the mode as a parameter, both watch
  sites pass `""`, `runDispatch` stops writing the global, and
  `derivePermissionMode` has one caller: `runDispatch`. `dispatchLaunch`,
  which dispatch and watch share, receives a finished argv and has no posture
  logic.
- *Nothing in watch reads the field.* Watch-provisioned instances also record
  `ClaudePermissions`, because they go through the same pipeline.
- *Continuation.* The continuation resumes a session that watch itself
  launched with no permission flag, so Claude Code has no flag to replay.
- *Remaining risk.* The only remaining risk is a future refactor that moves
  the derivation into a shared seam such as `provisionInstanceFunc` or
  `dispatchLaunch`. *Mitigation:* build AC9's watch argv tests on a real S1
  materialization, with `ClaudePermissions == "bypass"` present in state, so
  they fail if any shared layer starts reading it.

**4. Watch review posture in bypass and ask workspaces changes as a side effect.**

This isn't a derived flag. It follows from what the materializer now writes.

- *Hard-deny comment is already wrong.* `ApplyReviewSettings` doesn't set
  `defaultMode` in the hard-deny posture. Its comment says the session
  "inherits the bypassPermissions the dispatch applies" (containment.go:188-191),
  but watch never passes that flag.
- *Bypass workspace (S1), today.* On Claude Code 2.1.257 and later, the
  instance root carries `bypassPermissions`. Per the PRD's measurement it's
  downgraded to `default`, so hard-deny and `watch_sandbox = off` reviews run
  in `default`.
- *Bypass workspace (S1), after.* The file carries no `defaultMode`, so those
  reviews run in the developer's own user-level mode. That's `acceptEdits` or
  `plan` for most people, and could be bypass if the developer's user scope
  still honors it.
- *Sandboxed hard-deny is unaffected.* The OS no-egress sandbox, the
  egress-deny hook, and the filesystem-guard hook all apply regardless of
  permission mode; they're built to fire even under bypass
  (containment.go:78-139). The boundary doesn't move.
- *`watch_sandbox = off` loses its accidental floor.* Only the post-guard
  applies there, and it's accident prevention rather than a boundary. The
  review now runs in the developer's own mode instead of a `default` it got
  by accident. The operator explicitly chose that path as trusted, and the
  mode is the developer's own choice. Low severity.

  The PRD's Known Limitations mention this fall-through only for a
  concurrent `niwa apply` during a live review. It actually applies to every
  hard-deny and sandbox-off review in a bypass workspace. *Mitigation:*
  document it, and track it with the existing hard-deny follow-up.
- *Ask workspace (S2): a security fix, not a regression.* Today an `ask`
  instance root carries `askPermissions`. In the hard-deny posture
  `ApplyReviewSettings` keeps that key and merges its sandbox stanza and
  guard hooks into the same file. Per the PRD's measurement that a file
  carrying `askPermissions` is discarded whole, those hard-deny reviews ran
  with none of watch's containment settings in effect.
  `VerifyReviewSettings` passed anyway, because it checks bytes, not whether
  Claude Code accepts the file. The operator-approval posture wasn't
  affected, since it overwrites `defaultMode` with `"default"`. With this
  design, `ask` writes `default`, which closes the gap for every freshly
  provisioned review.

  *Mitigation, defense in depth, can be a follow-up:* have
  `VerifyReviewSettings` reject any `permissions.defaultMode` outside the set
  Claude Code honors from project scope. A future invalid value would then
  fail the review launch instead of silently voiding containment.

**5. The `ask` posture writing `default` restricts; nothing weakens a guard.**

- *It overrides any personal mode, not only more permissive ones.* An
  explicit project-scope `default` replaces every personal `defaultMode` in
  that scope. That includes stricter or differently shaped ones: `plan`,
  which blocks edits, and `dontAsk`, which auto-denies anything not already
  approved. The PRD's wording, "overrides a developer's more permissive
  personal setting", should say "any personal setting".
- *Why that's acceptable.* Under `default`, anything not already allowed by
  an allow rule still prompts, so no action becomes possible without a human.
- *What changes for developers.* Before this change the invalid file was
  thrown away and a developer's `plan` held. Now `ask` scopes run in
  `default`.
- *Guards.* Watch's operator-approval posture writes `default` anyway. The
  worktree-delegation `deny` entries are built into the same map
  independently, and the design keeps them. Hooks and deny rules that the
  invalid value voided in `ask` scopes start taking effect again, which is a
  net gain.

**6. The degraded path fails closed.**

- *What happens on failure.* When `LoadState` fails (missing, unreadable,
  malformed, or a forward schema), `derivePermissionMode` returns `""` and
  prints a warning, so no flag is forwarded.
- *What the worker gets.* It falls back to the instance-root document (no
  `defaultMode` for a bypass instance) and the developer's own settings. The
  failure moves toward prompting, never toward more privilege.
- *Cost and visibility.* The cost is a stalled unattended worker, which is
  an availability problem rather than a security one. The stderr warning
  naming the state file makes it visible.

**7. Containment for unattended workers is unchanged.**

- *The existing gap.* A `niwa dispatch` worker running with
  `bypassPermissions` has no hooks confining built-in file writes or network
  egress, unlike watch's sandbox mode. The prior design named this gap. This
  design neither closes nor widens it: the set of bypass workers is the same.
- *Related, also unchanged.* The worker launches at the instance root with a
  command-line flag that outranks every repo document. A per-repo `ask`
  therefore does *not* restrain it, even for files inside that repo. That's
  true today and the PRD documents it. It deserves a prominent place in the
  security section, because a maintainer might reasonably think a per-repo
  `ask` protects a sensitive repo from dispatched workers.

### Supply Chain or Dependency Trust
**Applies:** No.

- *No new dependencies or artifacts.* The design adds no dependency, fetches
  nothing, and runs nothing new. The change reads and writes niwa's own
  state file and changes which constant strings niwa writes into settings
  documents.
- *Existing trust, not changed here.* The grant sources are still config
  repositories niwa already syncs, and their authenticity is handled by the
  existing snapshot and overlay machinery. One of them is the workspace
  overlay, which niwa discovers by the `<org>/<repo>-overlay` naming
  convention. Whoever controls that repository can declare `bypass` for
  every dispatched worker in the workspace. That's true today through the
  materialized settings path as well, and it belongs to the config-source
  trust model rather than to this design.

### Data Exposure
**Applies:** Yes, low.

- *What's persisted.* One categorical value, `claude_permissions`: `"bypass"`,
  `"ask"`, or omitted. It goes into `.niwa/instance.json`, which is
  world-readable at 0644, unlike the 0600 settings documents. The value is
  validated to a closed set before it's persisted and has no secret content,
  even when it came from a `vault://` reference. At most it tells another
  local user that the workspace declared bypass, which the settings
  documents already record today.
- *What's transmitted.* Nothing new leaves the machine. The derived-flag
  notice and the degraded-path warning go to the operator's own stderr.
- *The one real exposure.* The plaintext echo in the invalid-value error
  path, covered under External Artifact Handling. The design should fix it
  in both validation callers.

## Recommended Outcome

**OPTION 2 - Document considerations.**

Nothing here requires changing the architecture. The implementer should
carry four small items into the design text and code:

1. **No secret plaintext in the invalid-value error** (Components: Posture
   mapping and Instance posture resolver). When the value is vault-backed,
   `claudeDefaultMode` and `instancePermissionsPosture` must not print it.
   The resolver should return the literal constants, not the revealed string.
2. **State the same-process property** (Components: Dispatch derivation).
   `derivePermissionMode` is valid only for an instance the calling process
   just provisioned. A future caller for an existing instance must
   re-resolve posture from config, not read `instance.json`. Optionally,
   check that `Root` matches.
3. **Watch tests use real state** (Decision 3 / AC9). Build the watch argv
   tests on a real S1 materialization with `ClaudePermissions = "bypass"`
   recorded, so a later read of state in a shared layer fails the test.
4. **Record the watch side effects** (Consequences). In a bypass workspace,
   hard-deny and sandbox-off reviews now run in the developer's own mode
   rather than `default`. In an `ask` workspace, hard-deny reviews regain
   the containment stanza the invalid value had been voiding. Note
   `VerifyReviewSettings` checking the recognized mode set as a follow-up.

Draft Security Considerations section, written for a public repository:

---

## Security Considerations

**Who can obtain bypass doesn't change.** Whether `niwa dispatch` forwards
`--permission-mode bypassPermissions` is still decided by the same
declaration: the `permissions` key in the workspace overlay, the workspace
config, the developer's personal overlay, or `[instance.claude.settings]`,
highest winning, with per-repo overrides ignored. When nothing has been
tampered with, the same workspaces get bypass workers as before. What
changes is where dispatch reads the resolved answer. It used to read
`.claude/settings.json`. It now reads a field niwa records in its own
`.niwa/instance.json` while materializing, from the same resolver that
builds the settings document.

**A settings file can no longer grant bypass.** A `.claude/settings.json`
hand-edited to say `bypassPermissions` no longer grants bypass to a
dispatched worker, and deleting the file no longer withholds it. Before this
change, any session able to edit the instance-root settings file could turn
a later dispatch into a bypass launch. After it, dispatch reads no permission
value from any Claude Code settings file.

**The state file is trustworthy only right after provisioning.**
`instance.json` is writable by the same local user, and by any agent session
running in the instance, just as the settings file is. That's acceptable
here for three reasons. Dispatch reads the field immediately after its own
process has created the instance. The instance directory has an unpredictable
name. And creating the instance writes the state file whole, so a value
planted in advance is overwritten. `derivePermissionMode` must only be called
for an instance the calling process just provisioned. Any future feature that
needs the posture of an *existing* instance should re-resolve it from
configuration rather than trust this field.

**Failure withholds the flag.** If the state file can't be read or parsed,
dispatch forwards no permission mode and prints a warning naming the file.
The worker then prompts instead of running unattended. An operator's
explicit `--permission-mode` still wins, and is still the only
`--permission-mode` on the command line.

**`niwa watch` never gets a derived flag.** The permission mode is now an
explicit argument to the shared argv builder. Dispatch passes the derived
value, and both watch launch sites, the fresh review and the continuation,
pass an empty string. Nothing in watch reads the recorded posture. The
operator-approval review posture relies on `permissions.defaultMode:
"default"`, and a command-line mode would outrank that, so tests pin watch's
empty value against an instance whose state records `bypass`.

**Side effects on watch reviews.** The hard-deny review posture sets no
permission mode of its own, so it runs in whatever the instance-root
document resolves to. In a `bypass` workspace that document no longer
carries a mode, so hard-deny and `watch_sandbox = off` reviews run in the
developer's own mode, not the `default` that Claude Code's downgrade used to
produce. The sandboxed boundary doesn't depend on the mode: the no-egress
sandbox, the egress-deny hook, and the filesystem-guard hook all apply under
any mode. In an `ask` workspace, the instance-root document previously
carried a mode Claude Code rejects. That caused it to discard the whole file,
including the containment settings watch merges into it. That file is now
valid, so hard-deny reviews in `ask` workspaces get their containment back.

**`ask` overrides every personal mode in its scope.** An `ask` scope now
writes `defaultMode: "default"`. That replaces the developer's own default
mode there, whether it was more permissive (`acceptEdits`) or shaped
differently (`plan`, `dontAsk`). Actions not covered by an existing allow
rule still need a person's approval. This is the one place a generated value
deliberately takes precedence over the developer's own setting. Hooks and
deny rules in `ask` scopes, which the previous invalid value silently
disabled, take effect again.

**Vault-backed values.** A `permissions` value can be a `vault://`
reference. niwa validates the resolved value against `bypass` and `ask`
before writing anything, and records only one of those two literals. No
secret material reaches `instance.json`. An invalid vault-backed value fails
the operation with an error that doesn't print the resolved value.

**Per-repo `ask` doesn't restrain dispatched workers.** A dispatched worker
starts at the instance root. The derivation follows the instance's posture,
and a command-line `bypassPermissions` outranks a repo's `default` for files
in that repo. A maintainer who wants dispatched workers to ask must declare
`ask` at the instance level.

**Containment gap carried over.** A worker running with
`bypassPermissions` skips the permission system for built-in file writes and
network tools. `niwa dispatch` has no containment equivalent to watch's
sandbox mode. This design doesn't change that gap, and it doesn't change how
many workers are exposed to it. Containment for dispatched workers remains a
separate follow-up.

---

## Summary

The resolved posture now lives in `.niwa/instance.json`. That file has the
same writers as the settings file it replaces, gets its value from the same
declared inputs, and is read only right after the same process writes it, so
it doesn't widen who can obtain `bypassPermissions`. It also closes the
tamper channel where a hand-edited settings file could grant bypass. Watch
can't receive a derived flag. The degraded path fails closed. Switching `ask`
to a valid `default` also restores the containment settings that watch's
hard-deny reviews had been losing. The outcome is Option 2, with four items
to document or add in implementation:
- keep vault plaintext out of the invalid-value error;
- state the same-process property on the derivation;
- base watch's argv tests on real `bypass` state;
- record how bypass and ask workspaces change watch's review posture.
