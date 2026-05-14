<!-- decision:start id="niwa-plugin-name-and-skill-invocation" status="assumed" -->
### Decision: niwa plugin name and skill invocation path

**Context**

niwa is shipping a Claude Code plugin that hosts the configuration-
migration skill defined elsewhere in `DESIGN-config-source-discovery.md`
(Decision 4 — the skill that probes a source slug and rewrites the user's
`niwa.toml` to point at the rank-1 `.niwa/` shape). Claude Code plugins
live at `~/.claude/plugins/<plugin-name>/`, and skills inside a plugin are
invoked as `/<plugin-name>:<skill-name>` from the chat surface.

The pre-existing PRD/design corpus encodes the invocation path as
`/shirabe:niwa-migrate-config`, organized around the prior assumption
that the migration skill would live inside the existing `shirabe` plugin
with a `niwa-` qualifier on the skill name. This decision pivots that
assumption: the skill ships in a niwa-owned plugin, which lets the skill
name drop the `niwa-` prefix (now redundant with the plugin namespace)
and changes every user-facing invocation string accordingly.

The decision space is small (four candidates) and hinges on two axes:
plugin-name shape (bare project name vs. suffixed) and skill-name shape
(qualified vs. bare verb). Sibling-plugin precedent in the user's
environment (the `shirabe` plugin, which uses its bare project name and
namespaces 13 skills under it) provides a strong anchor. No naming
collisions exist; no existing plugin claims `niwa`, `niwa-tools`,
`niwa-migration`, or any skill named `migrate-config` or `migrate`.

**Assumptions**

- niwa will likely ship at least one more Claude Code skill over its
  lifetime beyond `migrate-config`. The project shape — a workspace
  manager CLI with multiple JSON-emitting subcommands — invites future
  diagnostic, repair, or inspection skills. If wrong, the chosen name
  still costs nothing relative to the tighter-scoped alternatives.
- The decision does not need to preserve `/shirabe:niwa-migrate-config`
  as a back-compat alias. Ownership moves to niwa cleanly; if a future
  shirabe-side passthrough is wanted, it can be added without revisiting
  this decision.
- The plugin name does not need to be reserved against possible
  future Claude Code naming conflicts. The user's plugin marketplace
  surface is small and no organizational policy requires a suffixed
  plugin name to distinguish niwa's plugin from anyone else's.
- The `migrate-config` skill name remains the user-facing identifier
  already encoded throughout PRD/design strings, so the only rewrite
  on those sites is the namespace prefix (`shirabe:niwa-` becomes
  `niwa:`), not the local part.

**Chosen: Plugin `niwa`, skill `/niwa:migrate-config`**

The plugin directory under `~/.claude/plugins/` is `niwa`. The plugin's
`.claude-plugin/plugin.json` declares `"name": "niwa"` and points
`skills` at `./skills/`. The migration skill lives at
`skills/migrate-config/SKILL.md` (matching the directory-per-skill
layout the sibling `shirabe` plugin uses). Users invoke the skill as
`/niwa:migrate-config <workspace-name>` from Claude Code.

Concretely:

- Plugin manifest (`~/.claude/plugins/<source>/niwa/.claude-plugin/plugin.json`)
  uses `"name": "niwa"`, `"skills": "./skills/"`, with author/license/
  homepage/repository fields populated as for other tsukumogami plugins.
- Marketplace manifest (`.claude-plugin/marketplace.json`) declares one
  entry: `{"name": "niwa", "source": "./"}`.
- Skill directory `skills/migrate-config/` contains `SKILL.md` and any
  referenced phase/reference files. The skill's frontmatter `name` field
  is `migrate-config`; the invocation path `<plugin-name>:<skill-name>`
  resolves to `niwa:migrate-config` automatically from the plugin and
  skill names — no manifest declaration of the slash command itself.
- All user-facing strings in the corpus that currently read
  `/shirabe:niwa-migrate-config` rewrite to `/niwa:migrate-config`.
  Sites include the CLI deprecation notice format string (`internal/
  config/registry.go` per Decision 4), the design-doc ASCII diagrams,
  the deprecation-notice substring assertion in the PRD's acceptance
  criteria, and the PRD prose references.

**Rationale**

The chosen option is the only candidate that wins on every decision
driver:

- **Parity with sibling plugins.** `shirabe` uses its bare project name
  as the plugin namespace and packages 13 skills under it. `niwa`
  matches that precedent exactly. Adopting a suffixed name (`niwa-tools`,
  `niwa-migration`) would introduce a new naming pattern with no
  compensating benefit.
- **Future expansion accommodated without renaming.** If niwa ships a
  diagnostic skill, a recipe-explainer skill, an overlay-state inspector,
  or any other Claude Code skill in the future, the new skills land
  inside the same plugin as additional `skills/<name>/` directories. No
  plugin-level rename, no manifest churn, no fragmented multi-plugin
  surface for users to navigate.
- **Discoverability.** A user typing `/niwa:` in Claude Code gets
  autocomplete on every niwa-owned skill in one place. With a tighter-
  scoped plugin name (option c), niwa skills fragment across `niwa-`
  prefixed plugins and the user has to know which plugin owns which
  skill.
- **Typing economy.** `/niwa:migrate-config` is 21 characters versus
  29 for option (b) and 33 for option (c). The cost differential
  multiplies across every PRD, design-doc, deprecation string, and
  AC reference that quotes the invocation path. Option (d) saves
  more characters but at the cost of skill-name distinctness.
- **Skill name preserves the user-facing identifier.** The PRD/design
  corpus already uses `migrate-config` as the migration skill's
  identifier. Keeping that name unchanged means the rewrite from the
  prior `/shirabe:niwa-migrate-config` invocation to the new one is a
  namespace-only edit at every site, with no risk of accidentally
  changing the local part to something subtly different (`migrate`,
  `do-migration`, etc.).
- **No naming collision risk.** The user's plugin environment contains
  shirabe and no niwa-prefixed plugin. The skill name `migrate-config`
  does not appear in any installed plugin. The chosen pair shadows
  nothing and is shadowed by nothing.
- **The "is there a CLI inside the plugin?" concern is unfounded.** The
  prompt raises the question of whether plugin name `niwa` matching the
  binary name creates conceptual confusion. The shirabe precedent
  disproves this: plugin namespaces are slash-command prefixes, not
  binary names, and users distinguish `niwa <subcommand>` (shell) from
  `/niwa:<skill>` (Claude Code) syntactically. No observed confusion
  exists in equivalent setups.

**Alternatives Considered**

- **Plugin `niwa-tools`, skill `/niwa-tools:migrate-config` (option b).**
  Rejected. The `-tools` suffix is an attempt to signal "tooling around
  niwa, not the niwa binary itself", but this disambiguation isn't
  needed (see binary-confusion analysis above). The suffix breaks parity
  with the only sibling plugin's naming pattern and adds 7 characters to
  every user-facing invocation string with no compensating benefit. The
  plugin scope is the same as option (a) in practice — "niwa-adjacent
  tooling" and "all niwa-owned skills" describe the same set.

- **Plugin `niwa-migration`, skill `/niwa-migration:migrate-config`
  (option c).** Rejected. Tightest-scoping option, which is exactly its
  flaw: it locks niwa into a one-skill-per-plugin policy. Future niwa
  skills (`doctor`, `repair`, `explain`) either need their own
  `niwa-<purpose>` plugins — fragmenting niwa's slash-command surface —
  or get crammed into a plugin whose name no longer matches their
  purpose. The pluralization mismatch (`migration:migrate-config` has
  the verb-noun shape repeated) is a minor noise tax. Length cost is
  the highest of the four candidates.

- **Plugin `niwa`, skill `/niwa:migrate` (option d).** Rejected. The
  bare-verb skill name introduces ambiguity in niwa's domain:
  `migrate-config` (this skill), `migrate-vault` (vault-format
  migrations), `migrate-snapshot` (snapshot-format migrations), and
  `migrate-overlay` (overlay-schema migrations) are all defensible
  future skills. Claiming `migrate` for the config-migration skill
  forces every future migration skill to take a less natural name, or
  forces a rename when the first sibling lands. Additionally, the
  existing PRD/design corpus already uses `migrate-config` as the
  user-facing identifier; choosing (d) would mean every rename site
  changes both the namespace and the local part, doubling the diff
  surface and increasing the risk of inconsistency between rewrites.

**Consequences**

What changes:

- A new plugin directory `niwa/` lands under the tsukumogami marketplace
  source (sibling to the existing `shirabe/` plugin), with
  `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`, and
  `skills/migrate-config/SKILL.md` populated.
- Every site in the niwa repo's PRD/design corpus that references
  `/shirabe:niwa-migrate-config` is rewritten to `/niwa:migrate-config`.
  Sites enumerated in research: 5 in
  `docs/designs/DESIGN-config-source-discovery.md` and 14+ in
  `docs/prds/PRD-config-source-discovery.md`, plus the downstream Go
  string in `internal/config/registry.go` that Decision 4 introduces.
- The PRD's acceptance criterion that asserts the deprecation notice
  contains the substring `/shirabe:niwa-migrate-config` updates to
  assert `/niwa:migrate-config` instead. The substring assertion's
  shape (literal slash-command substring match) is unchanged; only the
  expected value changes.

What becomes easier:

- Adding a second niwa skill in the future (e.g., `/niwa:doctor`) is a
  drop-in: new directory under `skills/`, new `SKILL.md`, plugin
  manifest unchanged. No plugin-creation overhead, no marketplace
  registration, no user-side install for an additional plugin.
- Cross-skill references inside the niwa plugin (one skill linking to
  another) use stable relative paths under the same plugin tree, with
  no namespace gymnastics across plugin boundaries.
- Discoverability stays single-namespace: a user typing `/niwa:` gets
  every niwa-owned skill via autocomplete.

What becomes harder:

- The rename across PRD/design strings is a one-time edit. The
  mechanical cost is small (literal find-and-replace from
  `/shirabe:niwa-migrate-config` to `/niwa:migrate-config`), but any
  in-flight branch that touches those strings will need a small
  rebase. This is the only friction introduced by the decision.
- Documentation that previously framed the skill as "shirabe's niwa
  helper" now reframes it as "niwa's migration skill". This is a prose
  rewrite, not a structural change.
- If, in the future, an organizational policy ever requires suffixed
  plugin names to distinguish multiple owners' plugins from each other
  (a hypothetical not present in the current environment), the niwa
  plugin would need a rename then. The cost would be identical
  regardless of which option is chosen here, since options (b) and (c)
  would also need to conform to whatever new convention emerged. This
  is not a real cost differential.

<!-- decision:end -->

---

## Structured Result

```yaml
decision_result:
  status: "COMPLETE"
  chosen: "Plugin `niwa`, skill `/niwa:migrate-config`"
  confidence: "high"
  rationale: |
    The bare-project-name plugin with a qualified skill name wins on every
    decision driver: it matches the sole sibling-plugin precedent (shirabe),
    accommodates future niwa-owned skills without renaming or fragmenting the
    namespace, preserves the existing user-facing `migrate-config` identifier
    so the PRD/design corpus rewrite is namespace-only, keeps the
    invocation string at 21 characters (shortest of the unambiguous options),
    and carries no naming-collision risk in the user's environment. The
    tighter-scoped alternatives (`niwa-tools`, `niwa-migration`) either break
    parity or lock the plugin into one-skill scope; the shorter alternative
    (`/niwa:migrate`) sacrifices skill-name distinctness for a small
    character saving.
  assumptions:
    - "niwa will likely ship at least one more Claude Code skill in its lifetime beyond `migrate-config` (workspace-doctor, recipe-explainer, overlay-inspector are realistic candidates given the CLI's shape). If wrong, option (a) still costs nothing relative to the tighter-scoped alternatives."
    - "No back-compat alias for `/shirabe:niwa-migrate-config` is needed; ownership moves cleanly to niwa. A shirabe-side passthrough remains addable later if needed, outside this decision's scope."
    - "Plugin names are namespaces, not binary names; the shirabe precedent demonstrates that a plugin matching a project name does not imply a CLI presence inside the plugin, and users distinguish `niwa <subcommand>` from `/niwa:<skill>` by surface (shell vs. Claude Code)."
    - "The `migrate-config` skill name remains the user-facing identifier already used throughout PRD/design strings, so corpus rewrites are namespace-only edits."
  rejected:
    - name: "Plugin `niwa-tools`, skill `/niwa-tools:migrate-config`"
      reason: "Suffixed plugin name addresses a binary-collision concern that doesn't exist in practice (shirabe sets the precedent). Breaks parity with the only sibling plugin and adds 7 characters to every user-facing invocation string with no compensating benefit."
    - name: "Plugin `niwa-migration`, skill `/niwa-migration:migrate-config`"
      reason: "Locks niwa into one-skill-per-plugin scope. Future niwa skills must either spawn sibling plugins (fragmenting the user's slash-command surface) or live in a plugin whose name no longer matches their purpose. Highest length cost of the four candidates and a minor verb-noun-repetition noise tax."
    - name: "Plugin `niwa`, skill `/niwa:migrate`"
      reason: "Bare-verb skill name introduces domain ambiguity (migrate-vault, migrate-snapshot, migrate-overlay are defensible future skills). Forces every future migration skill into a less natural name or triggers a rename when the first sibling lands. Also doubles the corpus-rewrite diff surface relative to option (a) by changing both the namespace and the local part."
  report_file: "wip/design_config-source-discovery_plugin-decision_4_report.md"
```
