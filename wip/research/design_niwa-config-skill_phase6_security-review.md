# Phase 6 Security Review Verification — DESIGN-niwa-config-skill.md

Adversarial second pass over the Phase 5 "## Security Considerations" section.
Goal: find what that pass missed, not repeat what it already covered.

## Verdict

Phase 5's factual claims about the *code that exists today* all check out on
direct source reading (see "Spot checks" below) — including the one the task
specifically asked to verify (`offendingKeys` not walking `claude.settings` /
`vault.provider`). But the review has one real blind spot serious enough to
escalate rather than just note: Decision 2's entire "live-source-grounded
content" strategy assumes a niwa source checkout is present at skill-invocation
time, and for the exact population Decision 1 exists to serve — rank-1
single-repo adopters like the commuter/equity-planner pattern — that assumption
is false by construction. That gap also quietly invalidates the "External
artifact handling: no new download, fetch, or execution path" claim, because
it creates a strong incentive for the agent to go find `config.go` some other
way once the local read fails.

## 1. Attack vectors not considered

### 1a. [Escalate] "Live-source-grounded content" has no ground truth to read for its primary target population

Decision 2 has the `edit-config` skill teach an agent to read
`internal/config/config.go` / `internal/config/vault.go` "from the niwa repo
checkout the skill is invoked in" instead of hand-copying schema prose. The
Security Considerations section frames the residual risk narrowly: "a
compromised niwa fork or an attacker-modified local niwa checkout could in
principle steer the skill's live-read step toward bad advice... but this
requires an attacker who already has write access to a checkout the agent
trusts."

That framing implicitly assumes a niwa checkout exists in the session. It
usually won't, for the workspaces this design is built to reach. Per
`docs/guides/workspace-config-sources.md`'s own "Single-repo workspace"
section (verified directly, lines 516–549): a rank-1 workspace like commuter
or equity-planner is seeded from `niwa init --from owner/repo`, and niwa
"materializes only the `.niwa/` subtree into the workspace's snapshot
directory. The rest of the repo (your application code, README, src/, etc.)
is never fetched." There is no reason `internal/config/config.go` — niwa's
own source, a completely different repository — would be checked out
anywhere inside that instance. I also verified the embedded plugin ships no
vendored fallback: `internal/plugin/files/niwa/` contains only
`manifest.json` and `skills/migrate-config/SKILL.md` — no copy of the config
schema is `go:embed`'d alongside the skill for it to fall back to.

So for the common case this design targets, the agent invoking `/niwa:edit-config`
has no local ground truth to read. Two plausible failure paths follow, and
the design doesn't address either:

- **The agent fetches the file over the network instead.** An agent with
  `WebFetch`/`gh`/`git clone` available has an obvious move when a Read fails:
  go get `internal/config/config.go` from `github.com/tsukumogami/niwa` (or a
  differently-spelled/forked/typosquatted lookalike, or a stale cached
  mirror). This is a brand-new fetch/execution path the Security Considerations
  section's "External artifact handling" paragraph flatly denies exists
  ("This design introduces no new download, fetch, or execution path"). That
  claim is true of the *code change* in this design, but not of the emergent
  behavior Decision 2's own procedure invites once its precondition (a local
  checkout) fails. A spoofed or stale source here means the agent's "grounded"
  advice is neither grounded nor trustworthy, and the injected content is a
  path into influencing what the agent later writes into the user's
  `.niwa/workspace.toml`.
- **The agent silently falls back to training-data knowledge of niwa's
  schema**, presented with the same confidence as a real live-read. This
  reproduces, invisibly, exactly the staleness failure mode
  (`DESIGN-workspace-config.md` drifting 2+ months out of date) that this
  entire design exists to prevent — except now there's no `status: Current`
  frontmatter lying about freshness for someone to eventually notice; there's
  just an agent asserting field names with no signal anything went wrong.

**Recommendation:** this should block sign-off, not just get filed as a
deferred note. Phase 3's `SKILL.md` needs an explicit branch for "no niwa
checkout found nearby": at minimum, forbid network fallback and forbid
presenting un-grounded/trained knowledge as verified, and have the skill say
so plainly to the user rather than guessing. A more durable fix worth
considering before this ships: vendor a small, release-pinned fallback
(doc-comment excerpts or the struct shapes themselves) into the plugin tree
specifically for the no-checkout case, accepting release-pinned staleness
there as a deliberate, bounded trade-off — the same trade-off Decision 2
already accepts for `vault.*`, just extended to cover "checkout absent"
rather than only "checkout present but stale."

### 1b. [Low severity, worth a one-line note] Rank-1 broadening can cause repeated re-trigger, not just repeated install

The "fires once per workspace" guarantee is gated on
`sliceContains(disclosedNotices, NoticeID...)`, but `disclosedNotices` is only
persisted by `saveWorkspaceRootDisclosures`, called *after* `runPipeline`
returns successfully (verified in `apply.go` `Create`/`Apply`). A rank-1
source crafted (deliberately or accidentally) to make some later pipeline
step fail deterministically every time — a bad group reference, an
unresolvable repo entry — after the rank-1 branch has already fired but
before state save, would re-run the notice-emit + `InstallNiwaPlugin` call on
every subsequent apply attempt indefinitely, never converging to "shown
once." This isn't new: the identical shape already exists for the rank-2
branches. But broadening the trigger to rank-1 — the common, not the rare
deprecated case — makes hitting this edge case far more likely in practice.
Severity is low because `plugin.Install`'s `stageAndRename` (verified:
writes to `.next`, atomic `os.Rename` promote, `.prev` rollback on failure)
makes every retry a harmless idempotent no-op; the cost is redundant stderr
noise and repeated but safe disk writes, not corruption or elevated access.
Not blocking — a code comment near the rank-1 blocks noting the assumption
("this fires more than once only if apply fails after this point") would be
enough.

### 1c. claude.settings gap is broader across override positions than the prose suggests

Confirmed accurate that `offendingKeys` doesn't walk `claude.settings` or
`vault.provider`/`vault.providers.*` (see spot check below). One nuance the
design's prose glosses over: `Settings SettingsConfig` (the same
`map[string]MaybeSecret` type) is declared on both `ClaudeConfig`
(workspace-level, `config.go:36`) and `ClaudeOverride` (`config.go:73`, used
by `RepoOverride.Claude`, `InstanceConfig.Claude`, and the global-override
position). So the ungated surface isn't one field, it's the same gap
repeated at workspace, per-repo, instance, and personal-global-overlay
positions — four write sites, not one. Doesn't change the conclusion, but
worth stating explicitly since it affects how much surface the SKILL.md
guardrail language (and any future code-level fix, see §2) actually needs to
cover.

## 2. Are mitigations sufficient?

The `claude.settings` / `vault.*` mitigation is prose-only by the design's own
admission ("the skill is the only backstop here"). Given finding 1c, I don't
think that's sufficient, and there's a cheap code-level alternative the
design doesn't consider: `offendingKeys` already walks `EnvVarsTable.Values`,
which is `map[string]MaybeSecret` — structurally identical to
`SettingsConfig`. Extending the same `walk()` helper to also cover
`cfg.Claude.Settings`, each `repo.Claude.Settings`, `cfg.Instance.Claude.Settings`,
and (if reachable at that layer) the global-override's `Claude.Settings` would
convert this from "an LLM must remember to follow SKILL.md every time" into
an enforced, tested guardrail — for the cost of a few more `walk()` calls in
an already-existing function. This is materially cheaper than most of what
this design otherwise implements (a one-line CLI wiring fix, a notice
constant), so treating it as out-of-scope follow-up work rather than folding
it into this PR seems like an inconsistent bar. `vault.provider.Config`
(`map[string]any`, untyped) is a harder case to guardrail precisely since
field shape is provider-specific, but even a coarse heuristic (flag
suspiciously long/high-entropy string values, or common secret-key names like
`token`/`api_key`/`password`/`secret`) as a warn-only signal would raise the
bar above "purely relies on the agent reading and obeying a markdown file
every single time," including against a workspace.toml that itself contains
adversarial prose aimed at the agent (a rank-1 source is, after all,
untrusted third-party content the agent is asked to edit around).

The install-mechanism-integrity mitigation (atomic stage-and-rename) and the
functional-test mitigation for the manifest version-bump are both sufficient
and verified accurate — no changes needed there.

## 3. Spot checks against source

- **`internal/guardrail/githubpublic.go`'s `offendingKeys`** (lines 203–242):
  confirmed. It walks exactly `cfg.Env.Secrets`, `cfg.Claude.Env.Secrets`,
  each `repo.Env.Secrets` / `repo.Claude.Env.Secrets`, and
  `cfg.Instance.Env.Secrets` / `cfg.Instance.Claude.Env.Secrets` — all via a
  `walk(t config.EnvVarsTable)` closure. Nothing in the function or file
  touches `SettingsConfig` or `VaultProviderConfig`. The design's claim is
  accurate.
- **`SettingsConfig`** (`config.go:345`): `map[string]MaybeSecret`, confirmed
  distinct from and not fed into `offendingKeys`.
- **`VaultProviderConfig.Config`** (`vault.go:45`): `map[string]any`,
  populated via a custom `UnmarshalTOML`, confirmed distinct from and not fed
  into `offendingKeys`.
- **`NoticeIDPluginInstalled`'s hardcoded log line** (`disclosure.go:41`):
  confirmed still reads `"...Use /niwa:migrate-config to invoke the migration
  skill."` — the design's claim that this needs a wording update once rank-1
  installs also bring in `edit-config` is accurate and the Components section
  correctly flags it as a deliverable.
- **`plugin.Install`'s `stageAndRename`** (`internal/plugin/installer.go:123–163`):
  confirmed atomic `.next` staging → `os.Rename` promote → `.prev` rollback
  on failure. The "no window where a partially-written tree is visible"
  claim holds.
- **Rank-1 install gating in `apply.go`**: confirmed the design's structural
  description of the four call sites (`Create` ~443–453, `Apply` ~595–609,
  and the two overlay branches ~927–933 and ~956–962) — each guarded by
  `!sliceContains(disclosedNotices, NoticeID...)`, calling
  `a.InstallNiwaPlugin(nil, a.Reporter, a.SkipPluginInstall)`, sharing no
  mutated state beyond the append-only notices slice. Matches the design's
  "verified by direct source reading" claim, with the one caveat in §1b
  above about what "once per workspace" actually depends on.
- **`EmitRank2Notice`/`EmitPluginNotice`** (`disclosure.go`): confirmed
  no secret material in either — identifiers and fixed instructional text
  only, matching the "Data exposure" paragraph's claim.

No claim in the Security Considerations section was found to be factually
wrong about the code as it exists today. The gap is one of scope/completeness
(§1a), not accuracy.

## 4. Residual risk that should be escalated

1. **§1a (missing niwa checkout for the target population)** — escalate.
   This isn't a "document and move on" item: it undercuts the load-bearing
   rationale for Decision 2 (drift-resistance) for exactly the population
   Decision 1 was built to reach, and it opens a plausible path to a new,
   weakly-authenticated network-fetch surface that contradicts this design's
   own "no new fetch path" security claim. Should be resolved (SKILL.md
   explicit no-checkout branch, or a vendored fallback) before this design
   is implemented, not filed as follow-up.
2. **§2 (prose-only `claude.settings` guardrail)** — worth escalating from a
   "Phase 3 content requirement" to an actual Phase 1/4 code change, given
   how cheap the fix is relative to the rest of this design's diff and how
   directly it converts an unenforced backstop into an enforced one.
3. **§1b (repeated re-trigger on a persistently-failing rank-1 source)** —
   low severity, sufficiently addressed by noting it; not escalating.
4. **§1c (claude.settings gap spans 4 positions, not 1)** — informational,
   folds into the §2 fix if that's adopted; not independently escalating.
