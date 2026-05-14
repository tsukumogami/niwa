<!-- decision:start id="plugin-auto-install-consent" status="assumed" -->
### Decision: Plugin Auto-Install Consent Model

**Context**

PRD-config-source-discovery R16-R20 specifies a migration skill,
invocable as `/shirabe:niwa-migrate-config <workspace>`, that ships as a
Claude Code plugin. For the deprecation notice (R14) to point at a
literal invocable command — instead of pointing at "go install this
plugin first, then come back" — the plugin must be present in the
user's Claude Code config directory (`~/.claude/plugins/`) by the time
the user reads the notice. The user direction layered on top of the
PRD says niwa should auto-install the plugin so the migration entry
point is immediately reachable.

The auto-install is a real filesystem side effect under `$HOME`. niwa
already writes to `~/.config/niwa/`, `~/.local/share/niwa/`, the
workspace dir, and clones git repos without prompting; one more
directory under `~/.claude/plugins/<plugin-name>/` has the same
blast-radius shape. But `~/.claude/` is "Claude Code's config tree"
rather than "niwa's config tree", so the asymmetry — niwa writing to a
peer tool's config — deserves more disclosure than niwa's writes to its
own directories.

The binding constraint is PRD R21: "No existing user MUST be required
to take any action after upgrading … The rank-2 deprecation notice
(R14) is informational only and never blocks apply." A blocking
`[Y/n]` prompt on first apply after upgrade violates R21 — either by
hanging in non-TTY contexts (CI, hooks, scripted apply), by aborting
apply when stdin closes, or by introducing a stdin-read on a code path
that is currently non-interactive. The same constraint applies to any
design that makes apply's success conditional on a user response.

The decision space then narrows to: how much disclosure does niwa
provide for the write, and how does a power user opt out?

**Assumptions**

- The plugin target path is `~/.claude/plugins/niwa-migrate-config/`
  (or equivalent niwa-namespaced directory), namespaced so it cannot
  collide with any other Claude Code plugin. Collision detection is
  out of scope for this decision.
- The plugin contents are bounded (skills + command definitions, no
  binary blobs) and the write itself is atomic (staging-and-rename).
  The write-atomicity mechanism is decided separately; the consent
  model here is independent of it.
- `DisclosedNotices` (in `internal/workspace/state.go`) is the right
  carrier for the install disclosure. Its existing scope — "at most
  once per workspace per artifact per command-type" — matches the
  desired notice cardinality (one disclosure per workspace per
  command-type, not one per repo nor one per `niwa apply` invocation).
- A `DisclosedNotices` key collision between R14's rank-2 deprecation
  notice and this decision's install-disclosure notice is undesirable
  but easily avoided by using distinct keys (e.g.,
  `rank-2-deprecation:<workspace>:<artifact>` vs
  `plugin-installed:niwa-migrate-config`).
- Users who opt out of auto-install still want to read the rank-2
  deprecation notice itself. The two notices are orthogonal: opting
  out of auto-install does NOT suppress R14.
- The `GlobalSettings` struct in `internal/config/registry.go` is the
  right home for the opt-out toggle. It already carries opt-in keys
  (`clone_protocol`) with default-returning accessors and is the
  documented surface for "settings users edit in
  `~/.config/niwa/config.toml`".

**Chosen: Silent install by default with audited one-time disclosure,
opt-out via `~/.config/niwa/config.toml`**

The implementation has six parts:

**1. Write path.** On first rank-2 detection during `niwa apply` (or
`niwa init` against a rank-2 source), niwa checks
`GlobalSettings.AutoInstallPlugins`. The field is `*bool` so the
default-unset case is distinguishable from explicit `false`; the
accessor returns `true` for nil and for explicit `true`. When the
accessor returns `true`, niwa writes the plugin atomically to
`~/.claude/plugins/<plugin-name>/`:

- `mkdir -p` the parent (`~/.claude/plugins/`) if missing.
- Write to a staging path
  (`~/.claude/plugins/<plugin-name>.tmp-<pid>/`), populate it
  fully, then `rename` to the final path. The rename is atomic on
  POSIX filesystems and preserves the "either fully installed or not
  installed" invariant on crash or `Ctrl-C`.
- Include a marker file inside the plugin dir
  (`.niwa-install-marker.json`) recording the install version, install
  date, and the niwa binary version that performed the install.
  Subsequent applies read this marker to decide whether to overwrite,
  skip, or re-install. The overwrite policy is a separate decision;
  this consent model is forward-compatible with all three.

**2. Disclosure.** After a successful install, niwa emits a one-time
`note:`-prefixed stderr disclosure via the existing `DisclosedNotices`
mechanism, with key `plugin-installed:<plugin-name>`. The notice text:

```
note: niwa installed migration plugin to ~/.claude/plugins/<plugin-name>/.
      Invoke /shirabe:niwa-migrate-config <workspace> in Claude Code.
      To opt out of plugin auto-install, set auto_install_plugins = false
      in ~/.config/niwa/config.toml and remove the plugin directory
      manually if desired.
```

The disclosure fires at most once per command-type per workspace (init
vs apply are distinct command-types, matching the R14 contract). It is
informational, never blocks apply, and is independent of the R14
deprecation notice (both may fire on the same first-rank-2-apply, each
gated by its own key).

**3. Opt-out path.** When `AutoInstallPlugins` is explicitly set to
`false` in `~/.config/niwa/config.toml`, niwa skips the write entirely
and emits a different one-time disclosure with key
`plugin-install-skipped:<plugin-name>`:

```
note: rank-2 detected. Plugin auto-install is disabled via
      auto_install_plugins=false in ~/.config/niwa/config.toml.
      To use the migration skill, run:
        claude plugin install <plugin-source>
      Or unset auto_install_plugins and re-run niwa apply.
```

The R14 rank-2 deprecation notice fires alongside this notice on its
own key. Apply continues to success.

**4. No-op path.** On subsequent applies where the plugin marker
indicates a successful prior install at the expected version, niwa
emits neither disclosure (the `DisclosedNotices` keys have already
been recorded) and does not write. The plugin install is idempotent.

**5. CI / non-interactive behaviour.** No code path in the install
flow reads from stdin. There is no TTY check, no env-var precedence
rule, no `--yes` flag. CI environments and scripts that have not
configured the opt-out get the install on first apply with the
disclosure on stderr (which CI logs preserve); subsequent applies are
no-ops. Operators who want to disable installs in CI ship a
pre-seeded `~/.config/niwa/config.toml` with `auto_install_plugins =
false`, the same pattern they already use for `clone_protocol`.

**6. Per-invocation override flag.** A `--no-install-plugins` flag on
`niwa apply` and `niwa init` skips the install for that invocation
without persisting state. The flag is non-destructive (the default is
already to install) and matches niwa's `--no-progress` / `--skip-global`
flag style; no inverse `--install-plugins` flag is needed. The flag
takes precedence over `auto_install_plugins=true`; it does NOT
override `auto_install_plugins=false` upward, since explicit user
opt-out should not be silently re-enabled by a CLI flag.

**Where consent lives:**

| Surface | Mechanism | Disclosure |
|---|---|---|
| First install (default path) | Silent write to `~/.claude/plugins/<plugin-name>/` | Post-write one-time stderr notice |
| Subsequent applies | No write (marker file detects prior install) | No notice (already disclosed) |
| Opt-out (declarative) | `auto_install_plugins = false` in `~/.config/niwa/config.toml` | Pre-decline one-time stderr notice citing the manual install command |
| Opt-out (per-invocation) | `--no-install-plugins` flag | Pre-decline one-time stderr notice (same as declarative) |

**What happens if a user has a "carefully-curated Claude Code config":**
the install lands in a niwa-namespaced subdirectory. It does not modify
any existing plugin, does not register a new marketplace, and does not
touch the user's `~/.claude/settings.json`. The user can:
- delete the directory (niwa will re-install on next apply unless they
  also set the opt-out);
- set the opt-out and delete the directory (permanent);
- set the opt-out and leave the directory (the plugin remains, future
  niwa updates won't refresh it).

**Rationale**

This design satisfies all five decision drivers with the smallest
viable surface change and reuses niwa's existing mechanisms:

- **R21 compliance.** No code path blocks apply on a user response. The
  install is unconditional unless the user has pre-declared an opt-out;
  the disclosure is informational. CI, scripted apply, and hooks
  succeed without intervention. The R14 rank-2 notice fires
  independently and remains informational.
- **CI / non-TTY safety.** No stdin reads, no TTY check, no
  flag-or-env-var precedence rule to document. The flow is
  observationally identical across interactive and non-interactive
  contexts, eliminating "works on my terminal but hangs in CI" foot-
  guns.
- **Honors the auto-install goal.** Default behaviour is install. The
  user (project owner) gets the headline UX they asked for: the
  rank-2 notice points at a command that is immediately invocable in
  Claude Code, no preceding install step.
- **Power-user agency.** Opt-out is declarative and discoverable
  (`~/.config/niwa/config.toml` is where users already manage niwa
  settings) and supports both interactive workflows ("edit the file
  and re-run") and operator workflows ("ship a config-managed
  config.toml to all dev machines"). The `--no-install-plugins` flag
  covers the case where a user wants a one-off skip without persisting
  state.
- **Audited.** The post-write disclosure names the write target, the
  invocation command, and the opt-out mechanism in a single stderr
  notice. Users who later wonder "where did that plugin come from?"
  have a `DisclosedNotices` entry in their instance state and a
  stderr-log breadcrumb pointing at niwa. This is materially better
  than B (no disclosure) or D (no disclosure on install).
- **No new infrastructure.** `DisclosedNotices` and `GlobalSettings`
  are existing structures with documented contracts. The implementation
  adds one bool field on `GlobalSettings`, one accessor with a default,
  one notice-emission site in the apply pipeline, and one CLI flag.
  No new file formats, no new directories, no new config-discovery
  paths.

**Alternatives Considered**

- **A: Prompt with default-yes; `--yes` / `NIWA_AUTO_INSTALL=1` skips.**
  Rejected. R21 forbids a blocking prompt on first apply after upgrade.
  The only way to make A R21-compatible is to skip the prompt on
  non-TTY, which makes the prompt fire only for interactive humans —
  observationally identical to silent install for the dominant CI/script
  case, with the protective value of the prompt restricted to a
  minority of invocations. The design also introduces vocabulary forks
  (`--yes` alongside `--force`), an env-var precedence rule that must
  be documented, and a persistence problem: if the user denies the
  prompt, every subsequent apply re-prompts unless niwa persists the
  denial somewhere — at which point you've reinvented the opt-out
  toggle from D/E with a more complex front door.

- **B: Install silently with no prompt.** Rejected. The install is
  unannounced and unauditable. A user who later notices a new plugin
  in `~/.claude/plugins/` has no record of who put it there or why. No
  escape hatch for users who explicitly do not want niwa writing to
  Claude Code's config tree; the only remedy is "delete the dir, then
  niwa re-installs on next apply" with no way to break the cycle short
  of editing source.

- **C: Refuse to install; print the manual install command.** Rejected.
  Defeats the user-stated goal. The whole point of auto-install is to
  reduce the steps between "user is on deprecated rank-2" and "user
  can invoke the migration skill". C optimizes for maximum agency at
  the cost of the headline UX, and the manual-install fallback is
  already preserved in E as the opt-out path, so we lose nothing by
  making install the default.

- **D: Opt-out via `~/.niwa/config.toml`; default: silent install.**
  Partially adopted. The opt-out mechanism is correct; what D lacks
  is the audited disclosure on install. A user who never flips the
  toggle gets a silent install with no record. E adds the disclosure
  via `DisclosedNotices` for marginal additional cost (one
  notice-emission site) and a meaningful gain in auditability.

- **A variant with denial persistence.** A version of A that
  persists "user said no" to `~/.config/niwa/config.toml`
  automatically. Rejected because it has all of A's drawbacks
  (prompt-on-TTY-only, `--yes` vocabulary fork) without solving
  them; it just routes around the re-prompt issue by writing the
  opt-out toggle from E behind the scenes. If you're going to write
  the toggle, write it under user control (E) instead of inferring
  it from a prompt response.

- **A variant: blocking prompt with strict TTY requirement** (analogous
  to `niwa destroy`'s "stdin is not a terminal; aborting" pattern).
  Rejected outright by R21. Aborting apply on first rank-2 detection
  in CI is the exact regression R21 forbids.

**Consequences**

What changes:

- A new `*bool` field `AutoInstallPlugins` on `GlobalSettings` in
  `internal/config/registry.go`, with a default-returning accessor
  (`func (g *GlobalConfig) ShouldAutoInstallPlugins() bool` returns
  `true` for nil or explicit `true`, `false` for explicit `false`).
- A new install function (target package: `internal/workspace` or a
  sibling `internal/clauderc`) that performs the staging-and-rename
  write to `~/.claude/plugins/<plugin-name>/`. The function is
  idempotent: it reads `.niwa-install-marker.json` from the target
  dir and short-circuits if the marker indicates a current install.
- A new emission site in the apply pipeline (after snapshot promotion,
  before the apply summary) that calls the install function, records
  the disclosure via `DisclosedNotices`, and emits the stderr notice.
  The site fires only when rank-2 was detected during the just-
  completed apply (gated by the same probe-rank descriptor that
  drives R14's notice — decision 1's `RankDecider` output).
- A new `--no-install-plugins` flag on `niwa apply` and `niwa init`,
  threaded into the apply options struct.
- An entry in `docs/guides/workspace-config-sources.md`'s
  `#rank-2-deprecation` section explaining the auto-install behaviour
  and the two opt-out mechanisms (the global-config toggle and the
  per-invocation flag).
- Two new entries in the `DisclosedNotices` key namespace:
  `plugin-installed:<plugin-name>` and
  `plugin-install-skipped:<plugin-name>`.

What becomes easier:

- Future plugin auto-installs (e.g., a hypothetical second migration
  skill, or a workspace-config-distribution skill) reuse the install
  function and the disclosure mechanism with no further design work.
  The pattern is "namespaced subdir under `~/.claude/plugins/`,
  staging-and-rename, marker file, one-time disclosure".
- Operators managing fleets of dev machines can disable auto-install
  uniformly by shipping a pre-seeded `~/.config/niwa/config.toml`.
- Audit: support questions of the form "did niwa install anything
  under `~/.claude/`?" can be answered by reading the instance state's
  `DisclosedNotices` array or by `grep`ping the user's terminal log
  for the literal stderr notice.

What becomes harder:

- Removing the plugin requires the user to either delete the
  directory (one-shot, with the toggle still enabling re-install) or
  set the opt-out toggle AND delete the directory (permanent).
  Documenting this two-step is a guide-section addition.
- The plugin install path now reads `~/.config/niwa/config.toml` and
  writes `~/.claude/plugins/`. Test coverage for the apply path must
  exercise four combinations (toggle-unset/install, toggle-true/
  install, toggle-false/skip, flag-set/skip) plus the idempotency
  case (marker present/no-op). The existing apply tests have
  hookable filesystem layers (`t.TempDir`-based `$HOME`), so the new
  cases compose from existing primitives.
- Future plugin-overwrite policy decisions (refresh on apply vs leave
  alone vs version-marker check) inherit the consent model from this
  decision but require their own design. The marker file's schema
  (`version`, `install_date`, `niwa_version`) is forward-compatible
  with all three but does not pre-commit to one.

What does NOT change:

- The R14 rank-2 deprecation notice's contract is unchanged. It
  fires on its own `DisclosedNotices` key, scoped to workspace +
  artifact + command-type. The plugin-install disclosure is a
  sibling, not a replacement.
- The migration skill's behaviour, once installed, is unchanged: it
  is read-mostly per R20, never pushes to git, never runs `niwa
  apply`, never touches `<workspace>/.niwa/`. The consent model
  governs ONLY the install act, not the skill's runtime.
- Apply remains a non-interactive command on all code paths. No
  stdin reads, no TTY checks, no env-var precedence.
<!-- decision:end -->

---

## Structured Result

```yaml
decision_result:
  status: "COMPLETE"
  chosen: "Silent install by default with audited one-time disclosure; opt-out via auto_install_plugins=false in ~/.config/niwa/config.toml plus --no-install-plugins per-invocation flag"
  confidence: "high"
  rationale: |
    R21 forbids a blocking prompt on first apply after upgrade. Any consent
    model that gates apply on a user response either hangs in non-TTY contexts,
    aborts apply (violating R21), or degenerates to TTY-only-prompt + silent-
    install-everywhere-else (the worst of both worlds). The chosen design
    threads the needle by making the install unconditional on the default path
    but disclosing it via the existing DisclosedNotices mechanism (the same
    surface R14 uses), and offering two opt-out paths -- a declarative toggle
    in ~/.config/niwa/config.toml for persistent operator control and a
    --no-install-plugins flag for per-invocation skips. The design adds one
    bool field, one accessor, one notice-emission site, and one CLI flag; it
    reuses GlobalSettings and DisclosedNotices for zero new infrastructure.
    The audited disclosure is materially better than silent-install (B/D) at
    a marginal cost; the user-stated auto-install goal is preserved against
    refuse-and-print (C); R21's no-blocking-prompt rule is honoured against
    prompt-based (A).
  assumptions:
    - "The plugin lands at ~/.claude/plugins/<plugin-name>/ with a niwa-namespaced subdir name that cannot collide with other Claude Code plugins; collision detection is out of scope for this decision."
    - "DisclosedNotices is the right carrier; its 'once per workspace per artifact per command-type' contract matches the desired install-disclosure cardinality, and distinct keys avoid collision with R14's rank-2-deprecation notice."
    - "GlobalSettings in ~/.config/niwa/config.toml is the right home for the opt-out toggle, matching the precedent of clone_protocol."
    - "Opting out of auto-install does NOT suppress R14's rank-2 deprecation notice; the two notices are orthogonal."
    - "The plugin write is atomic (staging-and-rename) and bounded in size (skills + command definitions, no binary blobs); the write-atomicity mechanism is decided separately and is independent of this consent model."
    - "A separate decision will govern plugin-overwrite policy (refresh on apply, leave alone, or version-marker check); the marker file schema (version, install_date, niwa_version) is forward-compatible with all three."
  rejected:
    - name: "(A) Prompt with default-yes; --yes / NIWA_AUTO_INSTALL=1 skips"
      reason: "R21 forbids a blocking prompt on first apply after upgrade. The only R21-compatible variant skips the prompt on non-TTY, making the prompt fire only for interactive humans -- the protective value is restricted to a minority of invocations while introducing vocabulary forks (--yes vs --force), env-var precedence rules, and a re-prompt-persistence problem that re-invents the opt-out toggle from E with a more complex front door."
    - name: "(B) Install silently with no prompt"
      reason: "Unauditable. A user who later notices a new plugin in ~/.claude/plugins/ has no record of who put it there. No escape hatch for users who explicitly do not want niwa writing to Claude Code's config tree. The disclosure cost in E is marginal (one notice-emission site) for a meaningful auditability gain."
    - name: "(C) Refuse to install; print the manual install command"
      reason: "Defeats the user-stated auto-install goal. The manual-install fallback is preserved in E as the opt-out path, so making install the default loses nothing while gaining the headline UX."
    - name: "(D) Opt-out via ~/.niwa/config.toml; default: silent install (no disclosure)"
      reason: "Same opt-out mechanism as E, but without the audited disclosure on install. A user who never flips the toggle gets a silent install with no record. E adds the disclosure for marginal cost and meaningful auditability gain; D is a strict subset of E."
    - name: "Blocking prompt with strict TTY requirement (analogous to niwa destroy's 'stdin not a terminal; aborting')"
      reason: "Aborts apply on first rank-2 detection in CI, which is the exact regression R21 forbids."
  report_file: "wip/design_config-source-discovery_plugin-decision_3_report.md"
```
