# Lead: Privacy audit of public workspace.toml

## Findings

### Section Severity Summary

| Section | Risk Level | Primary Concern |
|---------|-----------|----------------|
| `[workspace]` | Low | Generic metadata, safe to expose |
| `[[sources]]` | High | Exposes org names and explicit repo lists |
| `[groups.*]` | Medium-High | Group names reveal organizational taxonomy; visibility filters signal private repo categories |
| `[repos.*]` | Extreme | Keys are repo names; url field is a direct clone URL leak; scope reveals priority classification |
| `[claude.content.repos.*]` | High | Keys name private repos; subdirs reveal internal code structure |
| `[hooks]` | Low-Medium | Script names may reveal security/operational practices |
| `[settings]` | Low-Medium | Permissions reveal trust boundaries |
| `[env]` | Medium-High | File references and inline vars can expose infrastructure |
| `[channels]` | High | Access controls expose user IDs (PII) |

### `[workspace]` — Low Risk

Fields: `name`, `default_branch`, `content_dir`. All are generic metadata. `content_dir = "claude"` signals niwa infrastructure presence but doesn't identify private repos. Safe to keep public.

### `[[sources]]` — High Risk

- `org` field directly exposes which GitHub orgs the workspace pulls from. If any org is private or internal-only, its name must not appear in a public config.
- `repos[]` (explicit list) names specific repos. If `repos = ["vision", "research"]`, those names are now public knowledge even if the underlying repos are private GitHub repos.
- Auto-discovery (`max_repos` threshold without explicit list) signals "we have access to all repos in this org," revealing team membership scope.

**Must move:** Any `[[sources]]` block for a private org.

### `[groups.*]` — Medium-High Risk

The group name itself is a leak. `[groups.private]`, `[groups.internal]`, `[groups.infrastructure]` tell a reader what categories of secret repos exist, even without listing repo names. The `visibility = "private"` filter says "this team has private GitHub repos organized under niwa," which is operational intelligence.

Explicit `repos = [...]` in a group definition names repos directly — identical leak to `[repos.*]` keys.

**Must move:** Any group definition whose name or filter implies private repos.

### `[repos.*]` — Extreme Risk

Every field of a `[repos.PRIVATE]` entry leaks information:
- **The key** (e.g., `[repos.vision]`) names the repo. This is unavoidable in TOML — the presence of the section header is the leak.
- **`url`** is a complete leak: `git@github.com:internal-org/vision.git` exposes org, repo name, and host.
- **`scope = "strategic"`** reveals internal priority classification. Attackers prioritize strategic repos.
- **`group`** maps the repo to a category, revealing classification scheme.
- The TOML section key cannot be redacted partially. If `[repos.vision]` must not appear in public config, the entire section must be absent — you cannot have an empty `[repos.vision]` without leaking the repo name.

**Must move:** Every `[repos.PRIVATE_REPO]` section in its entirety.

### `[claude.content.repos.*]` — High Risk

`[claude.content.repos.vision]` identifies "vision" as a managed repo in a public config. Combined with `[repos.vision]`, it confirms the repo is real and niwa-managed. The `subdirs` mapping reveals internal code structure:

```toml
[claude.content.repos.vision.subdirs]
ml_models = "repos/vision-ml.md"
experiments = "repos/vision-experiments.md"
```

This tells a reader: vision has subdirectories for ml_models and experiments — structural intelligence about a private codebase.

**Must move:** All `[claude.content.repos.PRIVATE_REPO]` sections, including their subdirs.

### `[hooks]`, `[settings]` — Low-Medium Risk

Generic hooks (event names, standard scripts) are low risk. Hook script names like `security-audit.sh` or `ip-filter.sh` can reveal security practices. Workspace-level `permissions = "bypass"` is operational detail but not a direct privacy risk.

These can stay public at workspace level if script names are generic. Per-private-repo hooks and settings must move with the `[repos.*]` entries.

### `[env]` — Medium-High Risk

`files` references are path-based — the file path names themselves can reveal infrastructure (`env/internal-api.env`). Inline `vars` can leak infrastructure details (`INTERNAL_SERVICE_URL`, cost codes). Generic vars (`LOG_LEVEL`, `DEBUG`) are safe.

**Must move:** Per-private-repo env references and any workspace-level vars that name internal infrastructure.

### `[channels]` — High Risk

`allow_from = ["7902893668"]` exposes Telegram user IDs — PII for team members. Group IDs reveal which groups are integrated. The plugin declaration (`plugin = "telegram@..."`) is public, but the access control section is not.

**Must move:** `[channels.*.access]` sections entirely. Plugin declarations can stay public.

### Content Files (Transitive Leak)

Content files referenced by `[claude.content.*]` carry their own information. A `claude/repos/vision.md` that says "Vision is our strategic AI research project" publicly describes a private repo if the config repo is public. Even if the TOML config is clean (no private repo references), the content files can leak by proxy.

**Must move:** Content files for private repos along with their TOML references.

### The Five Direct Attack Vectors

1. `[repos.*]` keys — repo names
2. `[repos.*.url]` — clone URLs (complete org+repo+host identification)
3. `[repos.*.scope]` — internal priority classification
4. `[repos.*.group]` — category classification
5. `[claude.content.repos.*]` and subdirs — repo existence + internal structure

Together, these five fields provide a complete organizational map of private repos to anyone reading the public config.

## Implications

### Split Boundary

The split must happen at the section level, not the field level. You cannot partially expose a `[repos.vision]` section — the key itself is the leak. The public config must be entirely absent of private repo references.

**Public config contains:**
- `[workspace]` metadata
- `[[sources]]` for public orgs only
- `[groups.*]` for public groups only
- `[repos.*]` for public repos only
- `[claude.content.repos.*]` for public repos only
- Workspace-level hooks/settings with generic script names
- Generic env vars
- `[channels]` plugin declarations (not access controls)

**Private companion contains:**
- `[[sources]]` for private orgs
- `[groups.*]` for private categories
- `[repos.*]` for all private repos
- `[claude.content.repos.*]` for private repos
- Per-private-repo hooks, settings, env
- `[channels.*.access]` sections

### Schema Constraint

The private companion must be a full workspace extension (not just an override struct). It needs to add new sources, groups, repo entries, and content entries — not just override fields on existing entries. This is structurally different from GlobalOverride, which only overrides hooks/env/settings on existing repos.

## Surprises

- The presence of `[groups.private]` is itself a leak, independent of listing any repos. A reader sees the group name and knows private repos exist.
- Subdirectory mappings (`[claude.content.repos.*.subdirs]`) are a structural intelligence leak — they reveal module/component architecture of private codebases.
- `[repos.*]` section keys in TOML cannot be redacted in a public/private split — the binary choice is: the section is absent (safe) or present (leak).
- Channel access control IDs (`allow_from`) are PII and the most unexpected leak surface.
- `scope = "strategic"` is operational intelligence, not just workflow metadata — it signals which repos an attacker should prioritize.

## Open Questions

- Can the private companion's group definitions coexist with the public config's group definitions when the group names differ? What if both need a group that produces the same directory name?
- How does the private companion handle the `content_dir` convention — does it have its own content directory, or does it reference paths in the public config's directory?
- For teams using `auto-discovery` (`[[sources]] org = "public-org"` without explicit repos), how does niwa know which of the discovered repos are "public" and should be processed by the public config vs. private and should be processed by the private companion?
- What happens to the workspace directory layout when the private extension adds new groups? Do the group directories appear alongside public group directories?

## Summary

A public workspace.toml exposes five direct attack vectors for private repos: `[repos.*]` keys (repo names), `[repos.*.url]` (clone URLs), `[repos.*.scope]` (priority classification), group definitions for private categories, and `[claude.content.repos.*]` entries including subdirectory mappings (internal code structure). The split boundary must be binary at the TOML section level — partial exposure of a `[repos.PRIVATE]` section is impossible since the key itself is the leak. The private companion needs to carry full additive content (sources, groups, repos, content), not just overrides — which is structurally different from the existing GlobalOverride pattern.
