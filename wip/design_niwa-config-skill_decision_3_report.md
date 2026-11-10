<!-- decision:start id="config-skill-bootstrap-seeding" status="confirmed" -->
### Decision: Should niwa init --bootstrap's scaffold template change to seed the rank-1 config-editing skill's discoverability for brand-new adopters?

**Context**
Decision 1 extended the embedded-plugin auto-install gate in `internal/workspace/apply.go` to fire on rank-1
detection, alongside the existing rank-2 branches, so every rank-1 workspace picks up the plugin (and the new
config-editing skill) automatically the next time its owner runs `niwa apply`/`niwa create`. The background for
this decision hypothesized that this already covers brand-new `niwa init --bootstrap` adopters for free, since
bootstrap's own `Create` call goes through the identical `runPipeline` that decision 1's new branch lives in --
narrowing this decision to, at most, a cosmetic doc-pointer fix in the bootstrap scaffold template's trailing
comment.

Direct verification of that premise against the current source (`internal/cli/init.go`, `internal/workspace/
apply.go`, `internal/workspace/bootstrap.go`, `internal/cli/plugin_adapter.go`) confirms half of it and falsifies
the other half. `defaultRunBootstrap` does call `applier.Create` -- the identical method `niwa create` calls -- and
`runPipeline` does compute `teamConfigRank` the normal way, which is always rank 1 for a freshly-bootstrapped
workspace. But `defaultRunBootstrap` constructs its own `Applier` (`workspace.NewApplier(gh)`) and wires only
`Reporter`, `ConfigSourceURL`, `GlobalConfigDir`, and `Agent` onto it -- it never calls `configurePluginAutoInstall`,
the one function (in `internal/cli/plugin_adapter.go`) that wires the `Applier.InstallNiwaPlugin` function-field
seam to the real `plugin.Install` implementation. `NewApplier` itself leaves that field nil by default. Every other
Applier-constructing call site in the CLI -- `create.go:217`, `apply.go:159`, `reset.go:103`,
`instance_from_hook.go:389` -- calls `configurePluginAutoInstall`; `defaultRunBootstrap` is the sole exception.

Consequence: every `if a.InstallNiwaPlugin != nil { ... }` check in `apply.go` (the four sites decision 1 extends,
plus the existing rank-2 ones) evaluates false for a bootstrap-created Applier, regardless of rank -- both today
and after decision 1's rank-1 branch lands, since decision 1's change only alters the trigger condition guarding
that same check, not the wiring behind it. So a freshly-bootstrapped rank-1 workspace's own `Create` call silently
skips the auto-install. This matters concretely because `RunBootstrap` immediately creates a worktree session and
`defaultRunBootstrap` prints landing instructions pointing the user directly into it (the R20 "landing path") --
the very first Claude Code session in a brand-new bootstrap adopter's worktree is the one moment guaranteed to lack
the plugin, which is precisely the "first contact" moment `--bootstrap` exists to optimize. The gap does self-heal
the next time the user runs `niwa apply` (correctly wired), which the bootstrap success message already tells them
to do as step 3 -- but that is a later, deferrable action, not the first session.

**Assumptions**
- None required. This decision's critical unknown (does bootstrap's pipeline wire plugin auto-install identically
  to `niwa create`'s) was fully resolved by direct source reading with no residual ambiguity, so status is
  `confirmed` rather than `assumed`.
- Decision 1's rank-1 branch in `apply.go` is assumed to land as described in its own report (four new
  `if teamConfigRank == 1 && ...` blocks calling `a.InstallNiwaPlugin`). This decision's fix is additive to and
  dependent on that landing, not a substitute for it -- if decision 1's branch is dropped or reworked, this fix
  alone still wires the seam but has nothing to trigger on rank 1 without it.

**Chosen: No scaffold-template content change; fix the CLI wiring gap in `internal/cli/init.go` instead**
Do not modify `scaffoldFromSourceTemplate` (`internal/workspace/scaffold.go:149-169`) -- its TOML content is not
the blocking factor, and its own trailing doc-pointer comment is already accurate (points to a live, existing
`docs/guides/workspace-config-sources.md` URL). Instead, add one line to `defaultRunBootstrap`
(`internal/cli/init.go`, immediately after `applier := workspace.NewApplier(gh)` at line 175, before
`applier.Create` is invoked via the `createWrapper` closure):

```go
configurePluginAutoInstall(applier, initNoInstallPlugins)
```

This mirrors, verbatim, the pattern already used at `create.go:217`, `apply.go:159`, `reset.go:103`, and
`instance_from_hook.go:389`. `initCmd` already declares the `--no-install-plugins` flag (`initNoInstallPlugins`,
init.go:52/66) for a different code path (rank-2 handling inside `runInit`'s non-bootstrap `modeClone` branch,
init.go:693-701); this change makes the bootstrap path honor the same, already-documented flag, at no cost of a
new flag or new user-facing surface. Once this lands together with decision 1's rank-1 branch in `apply.go`, a
freshly-bootstrapped rank-1 workspace's own `Create` call will populate `a.InstallNiwaPlugin`, the rank-1 branch
will fire, and the plugin (and config-editing skill) will be installed before the user's first worktree session
begins -- matching the reach guarantee decision 1's report assumed bootstrap already had.

A new functional-test scenario should assert this specifically: run `niwa init <name> --from <owner>/<repo>
--bootstrap` against a fixture with no prior `.niwa/workspace.toml`, then assert the plugin manifest and the new
skill's file exist in HOME after the bootstrap create step -- not merely after a subsequent `apply`, since that is
exactly the gap this decision closes. A `--no-install-plugins` opt-out variant (bootstrap path) should be added
alongside, symmetric with the existing `create`/`apply` coverage.

**Rationale**
This is the only candidate that closes the actual, source-verified gap: the background's premise ("bootstrap
already goes through the same runPipeline that calls InstallNiwaPlugin the same way niwa create does") is true for
the *call graph* but false for the *wiring* -- `runPipeline` is shared, but the function-field seam it calls through
is populated by a helper (`configurePluginAutoInstall`) that bootstrap's CLI entry point never invokes. No amount
of scaffold-template editing can fix a Go wiring omission; a comment in a TOML string cannot make code call a
function it doesn't call. The fix is a one-line, additive change matching an existing four-times-repeated pattern
exactly, touches zero bytes of the PRD Appendix A-pinned template string, and reuses a flag (`--no-install-plugins`)
that already exists on the same command. It directly serves the stated goal ("seed discoverability for brand-new
adopters") at the moment that matters most -- the first session after bootstrap -- rather than deferring reach to a
later `niwa apply` the user might not run promptly.

**Alternatives Considered**
- **No change at all** (as originally framed: decision 1 already covers this for free): rejected -- the premise it
  rests on is false. Verified by direct source reading, not merely re-asserted.
- **Scaffold-template doc-pointer fix**: the plain (non-bootstrap) `Scaffold()` template does have a genuinely
  stale doc pointer (`scaffold.go:45` says `docs/designs/DESIGN-workspace-config.md`; the file actually lives at
  `docs/designs/current/DESIGN-workspace-config.md`), but that lives in a different template used by a different
  init mode (`niwa init` without `--from`), not `scaffoldFromSourceTemplate`. Bundling it here would be scope
  creep onto an unrelated file and, more importantly, would do nothing to close the real gap. Noted as a
  low-priority, out-of-scope finding for separate future cleanup.
- **Substantive scaffold-template content change** (e.g., an explicit note or `[instance.files]`/`[claude]` block
  pointing at the skill): rejected on the same grounds decision 1 already used against a related alternative --
  this is a byte-equality-pinned string requiring PRD Appendix A-level rigor to touch, and by construction the
  scaffold path never re-fires for an already-bootstrapped repo, so it only helps *future* runs and does nothing
  for the mechanism-not-firing gap found here, which is unrelated to what the template's TOML text says.

**Consequences**
`internal/cli/init.go` gains one additive line inside `defaultRunBootstrap`, with no change to `scaffold.go`, no
change to the byte-equality-pinned template, and no new CLI flag. `internal/workspace/scaffold.go` is untouched by
this decision. A brand-new `niwa init --bootstrap` adopter's very first worktree session will have the plugin (and
config-editing skill) installed, closing the gap the background had assumed was already closed -- this decision's
research is itself a necessary correction to decision 1's own report, whose "Consequences" section characterized
bootstrap-time seeding as an independent, not-yet-decided question rather than a dependency decision 1 already had
a latent gap in. The plain `Scaffold()` template's stale doc-path (`scaffold.go:45`) and the dead
`schemaDocLinkFooter()` helper (`scaffold.go:171-178`, never called by either template) remain as separately
identified, low-priority cleanup items outside this decision's scope. A functional-test scenario must be added
asserting the plugin/skill are present in HOME immediately after a bootstrap `Create` call, to prevent this gap
from silently reopening.
<!-- decision:end -->
