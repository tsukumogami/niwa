# Phase 6 Architecture Review: DESIGN-niwa-config-skill.md

Reviewer: architecture-review subagent, /shirabe:design Phase 6 (mandatory gate)
Repo: niwa, worktree `parallel-snuggling-lightning`, branch `docs/niwa-config-skill`
Doc reviewed: `docs/designs/DESIGN-niwa-config-skill.md`

## Method

Read the full design doc end to end, then spot-checked every concrete source
citation the doc makes against the actual checked-out source in this worktree
(not trusting the doc's own line numbers), plus a few citations the doc does
*not* make but whose correctness the architecture depends on.

## Verification results (claim vs. source)

All spot-checked claims verified accurate unless noted:

1. **`internal/workspace/apply.go` rank-2 call sites** — confirmed exactly
   four `teamConfigRank == 2` / `overlayRank == 2` guarded blocks at lines
   443, 595, 927, 956, each calling `EmitRank2Notice` then
   `a.InstallNiwaPlugin(nil, a.Reporter, a.SkipPluginInstall)`. Matches the
   doc's citations precisely.
   - Minor imprecision: the doc's prose genericizes the dedup slice as
     `disclosedNotices` at all four sites. The actual local/field names
     differ per site: `initDisclosedNotices` (443), `wsDisclosedNotices`
     (595), `opts.disclosedNotices` (927, 956). Not a correctness problem
     — the *shape* is identical — but an implementer copy-pasting the
     doc's own pseudo-code literally would get a compile error at three of
     the four sites. Worth a one-line doc fix, not a design flaw.

2. **`internal/workspace/disclosure.go`** — confirmed `EmitRank2Notice(id,
   identifier string, reporter *Reporter)`, `EmitPluginNotice`, and the
   `NoticeIDPluginInstalled` hardcoded log line at exactly line 41 (`"...
   Use /niwa:migrate-config to invoke the migration skill."`), as the doc
   claims needs a wording update.
   - **Real gap**: the doc's proposed new function signature,
     `EmitRank1Notice(identifier string, reporter *Reporter)` (2 params),
     does not actually mirror `EmitRank2Notice`'s shape, which is
     `(id, identifier string, reporter *Reporter)` — 3 params. The `id`
     param in `EmitRank2Notice` is functionally unused in the current code
     (`_ = id // surfaced for symmetry`) but its presence is why one
     function can serve both `NoticeIDRank2TeamConfig` and
     `NoticeIDRank2Overlay` call sites with the same log text while still
     recording distinct notice IDs at the call site. If `EmitRank1Notice`
     truly needs to be called for both `NoticeIDRank1TeamConfig` and
     `NoticeIDRank1Overlay`, an implementer following the doc's literal
     2-param signature has no way to keep the "mirrors EmitRank2Notice"
     symmetry claim consistent with the doc's own signature. This is a
     small but real inconsistency worth fixing before implementation
     starts (either add the vestigial `id` param back, or explicitly note
     the divergence and why).

3. **`internal/cli/init.go` `defaultRunBootstrap`** — confirmed. It builds
   `applier := workspace.NewApplier(gh)` (line 175) and wires only
   `Reporter`, `ConfigSourceURL`, and (conditionally) `GlobalConfigDir`,
   never `configurePluginAutoInstall`. Grepping all `configurePluginAutoInstall`
   call sites across `internal/cli/` confirms exactly four other call sites
   (`apply.go:159`, `create.go:217`, `instance_from_hook.go:389`,
   `reset.go:103`) and zero in `init.go` — `defaultRunBootstrap` is
   verified as the sole Applier-constructing exception, exactly as claimed.
   `applier.Create` is called at line 197 via a wrapper closure, confirming
   the call-graph half of the claim too.

4. **`internal/plugin/files/niwa/manifest.json`** — confirmed current shape:
   `name`, `version` ("0.1.0"), `description`, and a `skills` array with
   exactly one entry (`migrate-config`, path
   `skills/migrate-config/SKILL.md`). The doc's claimed change (add a
   second `skills[]` entry, bump `version`) is a minimal, mechanically
   correct diff against this actual shape.

## Additional verification beyond the requested spot-checks

- **`plugin.Install`'s idempotence and atomic install claims** (Security
  Considerations, "Install-mechanism integrity") — confirmed against
  `internal/plugin/installer.go`: version-equality check via
  `readInstalledManifest`, `stageAndRename` writing to `<path>.next`,
  swapping the existing install to `<path>.prev`, promoting `.next` into
  place, with rollback on failure. Matches the doc's description exactly.

- **The `project_id` vs. `project` field-name drift claim** (Decision 2,
  used to justify not trusting `scaffoldTemplate` as authoritative) — this
  is the single most load-bearing empirical claim in the whole design
  (it's the entire justification for Decision 2's "cross-check before
  trusting" stance on the scaffold template), so it got extra scrutiny.
  Confirmed independently: `internal/workspace/scaffold.go` lines 93 and
  100 write `# project_id = "your-project-id"` / `"team-project"` inside
  the commented `[vault.provider]` / `[vault.providers.team]` example
  blocks. But the actual Infisical provider implementation
  (`internal/vault/infisical/infisical.go:96`, `rawProject, ok :=
  config["project"]`) and every consumer of
  `VaultProviderConfig.Config` (`internal/workspace/providerauth.go`,
  `credentialpool.go`, `credentialsync.go`, `apply.go`) all read the key
  `"project"`, not `"project_id"`. The scaffold template's example is
  genuinely wrong — a workspace owner copy-pasting it would set a key the
  provider never reads. This independently validates Decision 2's core
  premise and is a good, correctly-sourced example for `SKILL.md` to use
  as its own cautionary illustration.

- **Rank is binary (only 1 or 2)** — confirmed via test names and
  `snapshotwriter.go`/`overlaysync.go`/`fallback_test.go`: every rank
  determination in the codebase resolves to exactly rank 1 or rank 2, no
  third state. See "Simpler alternatives" below — this fact changes the
  calculus on whether the four-site duplication is actually necessary for
  gating the install call itself.

- **Skill file/frontmatter convention** — `migrate-config/SKILL.md` has
  frontmatter `name: niwa-migrate-config` (prefixed) while
  `manifest.json`'s entry `name` is `"migrate-config"` (unprefixed). The
  doc's Components section only specifies the manifest-entry name
  (`edit-config`, correctly matching the unprefixed convention) and
  doesn't spell out that the new `SKILL.md`'s frontmatter `name:` field
  should be `niwa-edit-config` for consistency. Minor omission, low risk
  — an implementer copying the existing file as a template will get this
  right by construction, but it's technically an implicit rather than
  explicit specification.

- **Phase 2's proposed unit test is realistic** —
  `internal/cli/dispatch_plugins_test.go` already has
  `TestConfigurePluginAutoInstall_WiresPrewarm`, which asserts
  `applier.InstallNiwaPlugin != nil` after calling
  `configurePluginAutoInstall` on a bare `workspace.Applier{}`. Phase 2's
  planned test ("assert `Applier.InstallNiwaPlugin` is non-nil after
  `defaultRunBootstrap` constructs its `Applier`") has a direct, working
  precedent to model from in the same file/package family.

## Answers to the four review questions

**1. Is the architecture clear enough to implement?**

Mostly yes, with two small fixable gaps. File paths, function names, and
line numbers all check out against actual source — this is unusually
well-grounded for a design doc; several claims (four call sites, exact
line 41, the `project_id`/`project` drift, the bootstrap wiring omission)
were independently re-derived from source, not just trusted. The two gaps:
(a) `EmitRank1Notice`'s proposed 2-param signature doesn't actually match
what "mirrors EmitRank2Notice" would require if it needs to serve two
distinct notice IDs with meaningfully different text — this needs
resolving (even if trivially) before an implementer writes the function.
(b) the doc's own pseudo-code uses a single generic `disclosedNotices` name
for what are actually three different identifiers across the four call
sites; a literal reading would produce a compile error at three of them.
Neither is a structural problem — both are copyedits, not redesigns — but
either would cost an implementer a few minutes of confusion or a wrong
first attempt.

**2. Are there missing components or interfaces?**

No missing components. The doc is unusually complete: it covers the
notice-ID constants, the new function, the four call sites, the CLI wiring
fix, the manifest entry, the SKILL.md content strategy, functional test
scenarios, and even flags a pre-existing hardcoded string (line 41) that
would become misleading as a side effect. One thing not explicitly named
as a component but worth calling out in Phase 3: the `edit-config`
`SKILL.md`'s frontmatter `name:` field convention (`niwa-edit-config`,
prefixed) isn't spelled out the way the manifest-entry name is — trivial
to get right by copying the existing file, but the doc could say so
explicitly for a completely context-free implementer.

**3. Are the implementation phases correctly sequenced?**

Yes. Phase 1 (rank-1 trigger) and Phase 2 (bootstrap wiring fix) are
independent of each other and could be done in either order or in
parallel; Phase 3 (skill content) is independent of both. Phase 4
(functional tests) correctly comes last since its scenarios assert
outcomes that depend on all three of the preceding phases having landed
(the rank-1 trigger existing, the bootstrap wiring existing, and the
`edit-config` skill file existing in the embedded tree to check for). The
doc is explicit that all four phases land in one PR, which sidesteps the
one sequencing risk that would otherwise matter: if Phase 1 (rank-1 now
triggers `InstallNiwaPlugin`) shipped without Phase 3 (the skill file
itself), rank-1 users would get a plugin install that doesn't yet contain
`edit-config` — a real but PR-boundary, not phase-boundary, concern given
squash-merge semantics.

**4. Are there simpler alternatives overlooked?**

One concrete, source-grounded simplification is worth surfacing that the
doc doesn't take up: rank is binary — every source resolves to exactly
rank 1 or rank 2, confirmed across `snapshotwriter.go`, `overlaysync.go`,
and the fallback tests. Under the design as proposed, `InstallNiwaPlugin`
will now fire on *every* apply/create, regardless of rank, at each of the
four sites, once the rank-1 branch exists alongside the rank-2 one. Since
`plugin.Install` is already internally idempotent (version-equality check,
confirmed above) and the doc's own Consequences section already relies on
that idempotence to wave away same-apply double-install as "a harmless
no-op re-check," the four-way duplication of the *install-triggering*
logic is arguably unnecessary: the install call itself could be hoisted to
one unconditional `a.InstallNiwaPlugin(...)` per pipeline invocation
(still gated by `a.SkipPluginInstall`), fully decoupled from rank. The
per-rank, per-notice-ID branches would then exist *only* to drive the
"why" notice text (`EmitRank1Notice`/`EmitRank2Notice`), which genuinely
does need to differ by rank and does need once-per-workspace dedup. This
would cut the total new code (and the pre-existing duplicated code it's
patterned after) roughly in half without changing behavior. The design
explicitly rejects touching existing rank-2 branches at all ("zero lines
change inside any existing rank-2 branch") as a stated goal for minimizing
diff risk, so this simplification is a reasonable one to defer or reject
explicitly rather than adopt silently — but it should be a conscious,
recorded trade-off (diff-size safety vs. duplication) rather than an
unconsidered option, since the doc's current framing ("mirrors the
existing [rank-2] block structurally" x4) reads as if duplication were the
only path rather than a deliberately chosen one.

## Recommendation

Proceed to implementation with two small pre-implementation fixes to the
design doc itself:
1. Correct `EmitRank1Notice`'s proposed signature (either match
   `EmitRank2Notice`'s 3-param shape for real symmetry, or explicitly
   justify the 2-param divergence).
2. Either fix the doc's pseudo-code to name the three different
   dedup-slice identifiers correctly, or add one sentence noting they
   differ by call site.

Neither blocks starting implementation — both are copyedits an
implementer could also resolve inline in ~1 minute by reading the actual
source (which is exactly what this review did). Optionally record the
"hoist the install call out of the rank branches" simplification as an
explicitly-deferred alternative (parallel to how Decision 1 already
records the `[instance.files]` and hybrid alternatives as deferred) so a
future maintainer sees it was considered rather than missed.
