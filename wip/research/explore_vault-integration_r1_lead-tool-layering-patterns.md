# Lead: How similar tools handle team-vs-personal secret layering

## Findings

### Per-tool summary table

| Tool | Team/personal distinction? | Per-project scoping? | Resolution algorithm | Bootstrap UX | git-tracked config friendly? |
|------|---------------------------|----------------------|----------------------|--------------|------------------------------|
| direnv | No native concept; pattern via `source_env_if_exists .envrc.private` | Yes, directory-based; walks up tree via `source_up_if_exists` | Nearest `.envrc` wins; parent `.envrc` only loaded if child calls `source_up` | Install direnv, `direnv allow`, manually create `.envrc.private` out-of-band | Yes (`.envrc` committed, `.envrc.private` gitignored) |
| mise | Implicit via `mise.toml` (committed) vs `mise.local.toml` (gitignored) | Yes, directory/file-based | More specific overrides broader; `mise.local.toml` wins over `mise.toml` | Install mise, clone repo, `mise trust`; manually populate `mise.local.toml` | Yes, by convention |
| asdf | No; single `.tool-versions` file | Yes, directory-based | Nearest wins | Clone repo, `asdf install` | Yes but limited (versions only, no env) |
| 1Password CLI (`op run`) | Team vault vs private vault (1P account-level), but not layered — each reference picks one | Per-project via `.env` template with `op://vault/item/field` references | Template references resolve literally to one vault each; no precedence merge | New user signs into 1P, may need vault access granted, then `op run -- cmd` | Yes (`.env` template committed; no secrets in git) |
| Doppler | Explicit via Root Config (team) and Personal Config (per-developer branch) | Per-project via `doppler.yaml` in repo pinning project/config | Personal Config inherits from Root; personal values override on same key | `doppler login`, `doppler setup` from repo root; Personal Config auto-created | Yes (`doppler.yaml` committed) |
| Infisical | Environments + path-based folders; no per-developer override primitive | Project + environment + folder path | Path-based lookup; no per-user overlay documented | `infisical login`, `infisical init` binds local dir | Partial (project binding committed) |
| Pulumi ESC | Composition via `imports` in YAML environment docs | Environments stack-agnostic; stack pins environment | Imported values overlaid by local `values`; later overrides earlier | `pulumi env` CLI, cloud account | Yes (environment YAML committed) |
| teller | No built-in team/personal — pull from many providers | Per-repo `.teller.yml` | Aggregation across providers; conflict behavior undocumented | Clone repo, install teller, auth to each provider separately | Yes (`.teller.yml` committed) |
| chezmoi | Delegates to external password manager | Per-dotfile templates | Template functions resolve at apply time | `chezmoi init <repo>` bootstraps; password manager auth separate | Yes (templates committed) |
| sops | Per-file encrypted; recipients = authorized team | Per-file `.sops.yaml` creation rules by path | Decryption depends on key possession; no layering — one file, one secret set | Clone, import age/PGP key, `sops -d` | Yes (encrypted files committed) |
| envchain | No; single-user keychain | Namespace-based (e.g., `aws`, `project-x`) | `envchain ns1,ns2 -- cmd` merges in order specified | Manually `envchain --set NS KEY` per var | No (keychain is out-of-band per machine) |
| pass / passage | Multi-GPG-recipient folders enable team shares | Folder hierarchy | File-path lookup; no override layer | Clone password-store repo, import GPG key | Partially (encrypted tree in git) |
| gh CLI | Per-host token only; no per-org primitive | Global only | Nearest = `GH_TOKEN` env > stored token | `gh auth login` | No (stored in system credential store) |
| GitHub Actions secrets | Org / Repo / Environment levels | Yes at each level | **Environment > Repository > Organization** (most specific wins) | Admin grants repo access to org secrets | N/A (secrets defined in GitHub UI, not git) |
| direnv + sops-nix | Team file (`secrets.yaml` encrypted) + personal key | Flake-scoped | Nix module merging; user-specific decrypt key | `nix develop` auto-runs decryption | Yes |

### Reusable patterns

1. **Two-file convention with implicit precedence (mise pattern)**
   A committed file (`mise.toml`) defines team-shared values; a gitignored sibling (`mise.local.toml`) overrides per-developer. Precedence is documented, not configurable. Also used by direnv (`.envrc` + `.envrc.private` idiom) and docker-compose (`docker-compose.override.yml`). This is the simplest, most battle-tested layering pattern.

2. **Auto-provisioned private branch off a shared root (Doppler Personal Configs)**
   Every developer granted write access to the `dev` environment automatically gets their own private branch config (`dev_personal`). They inherit everything from root and override only what they need, with strict visibility isolation. The `doppler.yaml` in the repo pins *which project/config to use* but the per-user binding resolves at CLI-auth time. This is the closest match to niwa's "per-project-scoped personal vault" requirement.

3. **Secret references as pointers, not values (1Password `op://` + sops-nix)**
   The committed file contains *references* (`op://Team/GitHub/pat`) that resolve at runtime from a store the user already has access to. Git never sees plaintext; the user's identity + their vault membership determines what actually resolves. niwa could adopt a `niwa://team/key` vs `niwa://personal/key` URI scheme.

4. **Compositional imports with explicit override layer (Pulumi ESC)**
   Environments declare `imports: [team-base, personal-tsukumogami]` and a local `values:` block overrides them. Ordering is explicit and documented — later imports override earlier. This makes the resolution algorithm legible and debuggable, unlike implicit precedence chains.

5. **Scoped-access tokens as the personal layer (GitHub fine-grained PATs)**
   The "personal" vault isn't really a vault — it's a token created by the user, scoped to exactly one org's resources, and stored wherever they like. niwa's per-project-scoped PAT requirement (tsukumogami PAT applies only to tsukumogami workspaces) maps naturally: one fine-grained PAT per org, selection driven by the workspace's declared org. No niwa-specific vault needed on the personal side.

### Anti-patterns

- **Silent fallback to unset** (teller's undocumented conflict resolution): when two sources supply the same key, teller's behavior isn't specified. niwa must document and surface which source won.
- **Global environment pollution** (envchain requires explicit namespace selection, but users often alias it globally, leaking per-project secrets across sessions). niwa's scope must be enforced at lookup time, not by user discipline.
- **Secret URL schemes with embedded creds** — e.g., `https://user:pat@github.com/...` in `.git/config` or `.netrc` with no scoping. Historically used by git credentials; has no revocation story. niwa should never materialize secrets into URL-embedded form.
- **Unscoped personal access tokens** — classic (non-fine-grained) GitHub PATs grant access to every org the user belongs to. niwa's personal layer must *require* fine-grained / org-scoped tokens to enforce the per-project scoping claim.
- **Undocumented precedence chains** (direnv's multi-`.envrc` behavior isn't in the hook docs; users infer from trial and error). Doppler and GitHub Actions do this right by publishing an explicit precedence table.
- **Out-of-band bootstrap with no self-check** (pass, envchain, sops without a discoverable `.sops.yaml` creation rule): new team member clones, runs command, gets cryptic failure. niwa needs an explicit `niwa doctor` equivalent that reports which layer is missing.
- **Mixing write access with read-only usage** (Doppler CLI/Personal tokens have account-wide write scope — warned against in production). niwa's personal layer should be read-only for lookup operations.
- **Committing `.env.example` with fake values that drift from real schema**: weaker than Pulumi/Doppler's model where the schema lives in the backend and the bootstrap validates against it.

### GitHub-as-vault verdict

**Team-shared side: strong fit.**
- GitHub org/repo secrets already have a precedence model (Env > Repo > Org) that mirrors what niwa needs.
- Access control is already tied to org/repo membership — no separate ACL system to reinvent.
- Works for Actions natively; reading org secrets from a local CLI is harder (requires a GitHub App or PAT with `secrets:read`), which pushes toward a "mirror into another store" pattern rather than true GitHub-as-vault for local dev.

**Personal side (fine-grained PATs): strong fit for the specific scoping claim.**
- Fine-grained PATs can be restricted to a single org's repos — exactly the "tsukumogami PAT for tsukumogami workspaces" scoping niwa wants.
- User creates one PAT per org; niwa's workspace declares its org; niwa selects the correct PAT at lookup.
- Storage is the user's problem (system keychain via `gh auth`, or their password manager). niwa doesn't need to invent a personal vault — it needs a lookup adapter.

**Caveats:**
- Fine-grained PATs can't easily be programmatically created (no API for user-PAT creation), so bootstrap still requires a manual step: "create a fine-grained PAT for org X, here's the permission list, paste it here."
- GitHub org secrets aren't readable by arbitrary local processes; accessing them requires either a GitHub App, a PAT with elevated scopes, or mirroring to a developer-accessible store (e.g., Actions secrets → Doppler, Actions secrets → a niwa-specific sync). Using GitHub as the *authoritative* team vault with direct local read is awkward.
- Expiry: fine-grained PATs have mandatory expiry. niwa must handle "PAT expired, prompt user to re-create" gracefully.
- Rate limits on authenticated API reads will matter if niwa resolves secrets on every command.

**Verdict:** Use GitHub *identity* (PATs, org membership) as the **authorization** substrate for both layers, but don't use GitHub's secrets API as the direct team *storage* backend. Storage should be pluggable (sops-in-repo, 1Password team vault, cloud secret manager) with GitHub org membership gating access. The personal layer is genuinely served by fine-grained PATs plus a local keychain lookup.

## Implications

- **niwa has a real unmet need.** The closest prior art for "per-project-scoped personal vault that layers over a team vault" is Doppler's Personal Configs, but Doppler scopes per-project via its own `doppler.yaml` file, not per-org. No surveyed tool cleanly does "personal secrets apply only to workspaces under a specific org."
- **Resolution algorithm must be explicit.** The tools that got this right (GitHub Actions, Doppler, Pulumi ESC) publish their precedence table. The ones that didn't (direnv, teller) cause user confusion. niwa must document: `workspace.local > workspace.inherited-from-team > org-scoped-personal > team-root`.
- **Bootstrap must be single-command with a doctor check.** Doppler's `doppler setup` and Pulumi's `esc env init` pattern (reads repo-committed config, asks user to auth, validates access) is the target UX. mise's "clone, `mise trust`, fill `mise.local.toml`" works but leaves gaps.
- **The committed layer should reference, not contain.** Adopt the 1P/sops-nix pattern: team repo contains references and access metadata; actual secret resolution happens against a backend the user authenticates to.
- **Fine-grained PATs are the natural "per-org personal" primitive.** niwa should build around them rather than invent a new per-org personal vault. The team layer is where niwa picks a backend (GitHub org secrets via App, Doppler, sops-in-repo, 1P team vault, etc.).

## Surprises

- **Doppler's Personal Configs are almost exactly the feature niwa wants** — except scoped per-project rather than per-org. The "auto-provision a private overlay for each member who has access" pattern is more mature than expected.
- **direnv has no first-class team-vs-personal story** despite being the default dev-env tool for many teams. The idiomatic solution is a community convention (`source_env_if_exists .envrc.private`), not a documented feature.
- **Nobody does per-org-scoped personal tokens at the tool level.** GitHub fine-grained PATs enable it at the protocol level, but no surveyed tool treats "which PAT applies here" as a first-class resolution question. This is a genuine niwa opportunity.
- **teller's documentation doesn't specify conflict resolution** despite being explicitly a multi-provider aggregator. This is the exact failure mode niwa must avoid.
- **sops + multi-recipient + path-based `.sops.yaml` creation rules** gets surprisingly close to niwa's needs for the team layer, and it's fully git-native. Worth evaluating as the default team backend.

## Open Questions

- Does niwa need to support the team-vault being in something other than the team config repo (e.g., team uses Doppler but niwa config is in GitHub)? Or is "team secrets live in the team config repo, encrypted with sops" good enough for v1?
- For the personal layer, does niwa manage PATs itself (create/rotate/revoke) or delegate entirely to the user + `gh auth`? Delegating is simpler but weakens the scoping guarantee.
- How does niwa handle the "user has no `codespar` PAT but enters a `codespar` workspace" case? Fail loudly, silently skip, prompt to create? Doppler fails loudly; this is likely the right choice.
- Is there a per-workspace-instance override layer below per-project-personal, or does personal = final? (e.g., "I'm testing locally with a different token just this once" — do we need a `niwa secrets set --instance` escape hatch?)
- Should the team layer support multiple backends pluggably (sops, Doppler, 1P, Vault) or commit to one for v1? Pluggability is a known rabbit hole (teller chose it and still doesn't have clean semantics).
- How does niwa's resolution interact with tools like direnv/mise that workspace consumers already use? Does niwa emit an `.envrc` snippet, or does it expose a lookup command (`niwa secret get KEY`) that users wire into their existing tooling?

## Summary

No surveyed tool cleanly solves niwa's exact requirement — a per-org-scoped personal layer over a team-shared vault — but Doppler Personal Configs, 1Password secret references, and GitHub fine-grained PATs each cover one axis of the problem and can be composed. The strongest recommendation is to treat GitHub org membership + fine-grained PATs as the authorization substrate, adopt mise's two-file implicit-precedence convention for configuration layering, and publish an explicit Pulumi-ESC-style resolution table so users never have to guess which layer supplied a value. The team-storage backend should be pluggable but start with sops-in-repo for git-nativity, with the committed layer containing references (1P-style) rather than plaintext.
