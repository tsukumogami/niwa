# Lead: How do peer tools handle git-hosted configuration sourcing, especially subpath cases?

## Findings

### Per-tool summaries

**GitHub Actions reusable workflows.** Slug syntax is fixed: `org/repo/.github/workflows/<file>.yml@<ref>` (or local `./.github/workflows/foo.yml`). The "subpath" portion is hard-coded — files must live at `.github/workflows/`, and nested directories under `workflows/` are not supported. `<ref>` accepts branch, tag, or full SHA; SHA is recommended for security. The fetch is opaque: GitHub resolves the ref server-side and downloads only that workflow file's bytes for the run; nothing is materialized on the user's filesystem in a stable form. There is no lock file, but Dependabot can auto-update SHAs in workflow files. Auth is implicit via the `GITHUB_TOKEN` for same-org / public repos. Top pain point in `community/discussions` #66094 and #26245: users want nested workflow paths and relative same-repo references, both unsupported. Docs: https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows.

**Nix flakes.** Slug syntax is a typed URL: `git+https://host/repo?ref=branch&rev=sha&dir=subdir&submodules=1`, with a shorthand `github:owner/repo?dir=subdir`. The `?dir=` query parameter delimits the subpath, distinct from `?ref=`/`?rev=` for the git ref. Materialization is content-addressed: Nix downloads a tarball or shallow checkout, computes a NAR hash, and stores the result at `/nix/store/<hash>-source`. The `flake.lock` file records resolved sha + narHash + lastModified for every input, so every `flake.nix` becomes deterministic. Auth defers to git/SSH or `~/.netrc`. Top pain point: NixOS/nix #3121 ("Copy local flakes to the store lazily" — flakes always copy the entire tree even when only a subdir is referenced) and #7422 (registry indirection considered bug-prone). Docs: https://nix.dev/manual/nix/2.18/command-ref/new-cli/nix3-flake.

**Helm chart repos.** Helm doesn't use git URLs at all for installation: a "chart repo" is an HTTP-served directory containing `index.yaml` plus `name-version.tgz` archives. `helm install foo/bar` resolves to the entry under `bar` in `foo`'s index.yaml, fetches the .tgz, and unpacks it. There is no inherent subpath concept — each chart is a separate tarball. Subcharts live under `charts/` inside a parent tarball and travel together. Refs are versions (semver) declared in the index, not git refs. There is no lock file at the chart level (Chart.lock exists for dependency resolution, similar to package-lock). Auth is HTTP basic / OCI registry credentials. Top pain point: helm/helm #9928 (subcharts must be re-downloaded from external repos even when the parent already bundles them). Docs: https://helm.sh/docs/topics/chart_repository/.

**Terraform module sources.** Slug syntax: `git::https://host/repo.git//subdir?ref=v1.2&depth=1`. The `//` is the canonical subpath delimiter (the ref query must come *after* the subpath). Materialization is via go-getter: full clone of the repo into `.terraform/modules/<name>/`, then the `//subdir` is exposed as the module root. `depth=1` enables shallow clone (added late, not default). Lock file: `.terraform.lock.hcl` records *provider* hashes but not module shas — modules are re-resolved on every `init -upgrade`. Auth: SSH keys, `~/.netrc`, `git config insteadOf` for HTTPS tokens. Top pain point: hashicorp/terraform #19277 (39+ thumbs-up, "sparse checkout module") — users want to fetch *only* the subdir, not the entire repo, especially in monorepos where 100 modules pull 100 full clones. The maintainer admits it's intentional only because cross-module relative paths might exist. Docs: https://docs.hashicorp.com/terraform/language/modules/sources.

**chezmoi.** Slug-free: `chezmoi init <user>` discovers `https://github.com/<user>/dotfiles.git` by convention. Subpath is *not* in the URL — instead a `.chezmoiroot` file in the repo root contains a relative path string indicating where the source state actually lives. This decouples URL syntax from layout. Materialization is a normal `git clone` to `~/.local/share/chezmoi`. Ref pinning: not standard; users tend to track HEAD of `main`. No lock file. Auth: standard git credentials. Top pain point: twpayne/chezmoi #1657 — `.chezmoiroot` was initially not honored by `chezmoi init` for config-template discovery, requiring later patching. Conceptually clean, but the in-repo convention can surprise users who expect URL-driven configuration. Docs: https://www.chezmoi.io/reference/special-files/chezmoiroot/.

**Renovate config presets.** Slug syntax: `github>org/repo` (default file `default.json`), `github>org/repo:preset-name` (sub-preset within a JSON file), or `github>org/repo//path/to/file.json5` (file at subpath). The `//` again delimits subpath; `:` delimits sub-key within the resolved file. Cannot combine path and sub-preset (`provider>owner/repo//path:subkey` is unsupported per docs). Cache: resolved presets cached in Renovate's package cache with 15min TTL by default. No lock file (presets are re-resolved every run). Auth: platform token (e.g., `local>...` for same-platform same-token access). Top pain point: renovatebot/renovate #19443 — `globalExtends` cannot reference private presets because the preset resolver runs before per-repo credentials are loaded. Docs: https://docs.renovatebot.com/config-presets/.

**Cargo git deps.** Syntax: `foo = { git = "https://...", branch = "main", path = "subdir" }` or simply `git = "..."`. Cargo recursively traverses the cloned repo looking for any `Cargo.toml` matching the requested crate name — so multiple crates in a monorepo just work without a subpath argument, but you can't disambiguate same-named crates. The `--sub-path` add option exists for `cargo add` but full path-in-git is still requested (rust-lang/cargo #1462, #11858). Materialization: full clone to `$CARGO_HOME/git/db/<repo-name>-<urlhash>` plus a working checkout at `$CARGO_HOME/git/checkouts/<repo-name>-<urlhash>/<rev>`. Lock file: `Cargo.lock` records exact rev/hash. Auth: SSH keys, `cargo:credential-` helpers. Top pain point: ambiguity when multiple crates share names; full clone for one small subdir.

**npm / pnpm / yarn git deps.** npm: `git+https://host/user/repo.git#ref` — no subpath support. pnpm proposal in #7483 / #4765 (28+ thumbs-up): adopt `?path=/packages/util` query param mirroring Unity's UPM; not yet shipped. Yarn 2+ supports git workspaces but not arbitrary git subpath in classic spec. Materialization: full clone to a temp dir, all files (excluding `.git`) copied to the package store. Lock file: `package-lock.json`/`pnpm-lock.yaml`/`yarn.lock` record commit sha. Top pain point across all three: subpath support is the single most-requested missing feature in this area; users resort to `postinstall` sparse-checkout scripts or third-party services like GitPkg.

**Atlantis.** Doesn't fetch config from arbitrary URLs at runtime — instead a single `--repo-allowlist=github.com/myorg/*` flag controls which repos may host an `atlantis.yaml`. Server-side `repos.yaml` can pin a custom repo-config file path via `repo_config_file`. No subpath URI scheme; everything is repo-internal at known paths. Auth: server's GitHub App credentials. Top pain point relevant to this lead: server admins can't fetch shared workflow definitions from a *different* repo without baking them into the container image — there's no native "import preset from this other repo" feature. Docs: https://www.runatlantis.io/docs/server-side-repo-config.

**Backstage software templates.** Slug syntax: full URL to the template manifest, e.g., `https://github.com/org/repo/blob/main/templates/foo/template.yaml`. Backstage parses the URL, calls the GitHub API, and reads only that one file plus referenced asset paths. Auth: configured integrations (GitHub PAT or App). No lock file — catalog locations are re-fetched on a schedule. Materialization: just the one template file, plus on-demand reads of skeleton files referenced from it. Top pain point: backstage/backstage #20959 (auto-discovery of templates in a repo without enumerating each one in `app-config.yaml`). The blob-URL approach is human-readable and roundtrips through GitHub's UI cleanly. Docs: https://backstage.io/docs/features/software-templates/adding-templates/.

**Crossplane configurations.** Not git-based: packages are OCI images. A "Configuration" package is a directory of YAMLs built into an OCI image with `crossplane xpkg build`, then referenced as `xpkg.upbound.io/org/foo:v1.0`. Subpath is a build-time concept (`--package-root`), not a runtime URI. Lock file: `crossplane.lock`. Auth: standard OCI registry credentials. Top pain point relevant here: crossplane/crossplane #4299 — users want OCI registry rewrites for air-gapped environments, comparable to Docker's `imageContentSourcePolicy`. The OCI choice eliminates "subpath in URL" entirely by shifting subpath into the *build* step. Docs: https://docs.crossplane.io/latest/packages/configurations/.

## Cross-Tool Comparison

| Tool | Subpath syntax | Materialization | Lock file | Private auth | Top pain |
|---|---|---|---|---|---|
| GH Actions reusable workflow | fixed `.github/workflows/<file>` (no nested) | opaque server-side fetch | none (Dependabot updates SHA) | implicit token | nested paths unsupported |
| Nix flake | `?dir=subdir` query | content-addressed store; copies whole tree | `flake.lock` (sha + narHash) | git/SSH/netrc | always copies full tree even for subdir |
| Helm | none — each chart is a separate tarball | unpack tarball | `Chart.lock` | HTTP basic / OCI | bundled subcharts re-downloaded |
| Terraform | `//subdir?ref=…&depth=1` | full clone, expose subdir | `.terraform.lock.hcl` (providers only) | SSH/netrc/insteadOf | sparse checkout requested for years |
| chezmoi | none in URL; `.chezmoiroot` file inside repo | full clone | none | git creds | discovery surprises; init bugs |
| Renovate | `//path/to/file.json` | in-memory resolve, 15min cache | none | platform token | `globalExtends` private repo gap |
| Cargo | none (auto-discovers crate by name); `path` field optional | full clone to `$CARGO_HOME/git` | `Cargo.lock` | SSH/credential helpers | ambiguous crate names; full clone |
| npm/pnpm/yarn | none today; `?path=` proposed for pnpm | full clone, copy to store | `*-lock` (sha) | git creds | subpath is most-requested missing feature |
| Atlantis | none (allowlist + in-repo `atlantis.yaml`) | n/a | n/a | GitHub App | no cross-repo preset import |
| Backstage | full blob URL | API read of one file | none | configured integration | no auto-discovery |
| Crossplane | none in URI; baked at build time | OCI pull | `crossplane.lock` | OCI registry creds | air-gapped registry rewrites |

## Convergent Patterns

1. **`//` as the subpath delimiter is the de-facto standard** when a tool exposes subpath in the URL itself. Terraform, Renovate, and go-getter all use it. The convention reads "everything after `//` is *inside* the previously-named package."
2. **Query parameters are the de-facto standard for *non-path* attributes** (`?ref=`, `?rev=`, `?depth=`, `?dir=` for Nix, `?path=` proposed for pnpm). Nix is the outlier in using `?dir=` for the subpath itself rather than an in-path `//`.
3. **Lock files are universal** for any tool that takes correctness seriously — `flake.lock`, `Cargo.lock`, `*-lock.json|yaml`, `Chart.lock`, `crossplane.lock`. The shared shape: input-spec → resolved sha + content hash + (sometimes) timestamp.
4. **Materialization is almost always full-clone-then-narrow**. Only Backstage (single-file API read) and GitHub Actions (server-side opaque fetch) avoid the full clone. Nix avoids re-downloads via content addressing but still copies the full tree on first fetch.
5. **Authentication is delegated** — every tool punts to git's existing credential mechanisms (SSH agent, `~/.netrc`, `insteadOf`, credential helpers, platform tokens). No tool reinvents auth.
6. **Refs default to "track HEAD of main branch"** in casual usage, with optional pinning to tag or sha. Tools that emphasize reproducibility (Nix, Cargo, Crossplane) lock the resolved sha into a lock file regardless.

## Outliers Worth Borrowing

- **chezmoi's `.chezmoiroot`**: in-repo convention file that decouples URL syntax from filesystem layout. Lets users reorganize their dotfiles repo without breaking everyone's `chezmoi init` URLs. Genuinely solves the "ergonomic short URL + flexible internal layout" tension.
- **Nix's content-addressed store + `flake.lock`**: separates *identity* (a flake input by name) from *materialization* (a NAR-hashed snapshot). Two flakes pointing to the same git commit dedupe naturally on disk.
- **Renovate's preset cache TTL**: 15-minute reuse across repos avoids re-resolving the same preset on every run, without requiring a stale-prone lock file.
- **Cargo's "traverse the clone to find Cargo.toml by name"**: lets one repo host many crates with no subpath syntax at all, at the cost of name-collision ambiguity. Ergonomically lovely when it works.
- **Crossplane's OCI re-framing**: by switching the artifact format to OCI, "subpath" becomes a build-time decision and runtime URIs stay simple. Trades flexibility for operational clarity.

## Avoidable Mistakes

- **Don't ship without subpath support and then bolt it on later** (npm/pnpm/yarn). Users invent painful workarounds (`postinstall` sparse-checkout, third-party services like GitPkg) that become the de-facto interface and resist replacement.
- **Don't full-clone when you only need a subpath** (Terraform's #19277 has been open since 2018, accumulating 39 thumbs-up over seven years). The maintainer's "but cross-module relative paths might exist" justification has aged poorly — most users would happily opt out of relative-path resolution for the win of `git archive`-speed fetches.
- **Don't conflate sub-preset and subpath delimiters** (Renovate explicitly forbids `repo//path:subkey`). One delimiter for "path inside the repo," another for "key inside the resolved file" creates user confusion.
- **Don't make subpath syntax positional vs. query-string ambiguous** (Terraform requires `//subdir` *before* `?ref=` — getting the order wrong silently breaks). Either commit to query-string-only (Nix) or positional-only.
- **Don't depend on server-side opaque fetches with no local materialization** (GitHub Actions reusable workflows). When something breaks, users have nothing to inspect or debug.
- **Don't make discovery so implicit that `init` and `apply` disagree** (chezmoi #1657 — `.chezmoiroot` was respected by one command and not another for over a year).

## Recommendation

niwa should adopt the `//` subpath delimiter with optional `?ref=` query parameter, mirroring Terraform and go-getter: `https://github.com/org/brain.git//path/to/niwa-config?ref=v1.2`. The default ref should be `HEAD` of the default branch, but `niwa init` should resolve to a sha and write it into a workspace-local lock file (call it `.niwa/lock.json`) recording `{ source, ref, sha, fetchedAt, narHash }` — the Nix flake.lock pattern. Materialization should be a *snapshot* (not a working tree): `git archive --remote` or `git fetch --depth=1` followed by checkout to a content-addressed cache under `~/.cache/niwa/snapshots/<sha>`, then symlink/overlay into `<workspace>/.niwa/`. This solves issue #72 (remote rewrites stop mattering — there's no working tree to maintain) and the "single repo, many configs" use case (`brain.git//team-a` and `brain.git//team-b` cohabit). The whole-repo case is the degenerate `subpath = "/"` form. For private repos, defer to git's existing credential machinery (SSH agent, gh CLI's `git config insteadOf`, `~/.netrc`) — every peer tool does this and reinventing it is a lose. Optionally add a `.niwaroot` convention file (mirroring `.chezmoiroot`) so brain repos can move the niwa config inside without breaking external slug references.

## Implications

- niwa's source slug grammar must distinguish three things cleanly: repo URL, subpath, and ref. Adopting Terraform's `<repo-url>//<subpath>?ref=<ref>` is the lowest-surprise choice for users coming from Terraform/go-getter/Renovate.
- The CLI `niwa init <slug>` becomes the parser entry point; it must reject ambiguous forms early (e.g., `?ref=` before `//` should be an error, not silently ignored).
- A lock file (`.niwa/lock.json`) is non-negotiable if niwa wants reproducibility across teammates. Without it, "track main" resolves differently on different machines.
- Snapshot materialization (vs. working tree) means `niwa update` becomes "fetch new sha, materialize new snapshot, atomically swap symlink." This is conceptually closer to package-manager semantics than git-clone semantics — and it's what makes issue #72 go away.
- Discovery convention (`.niwaroot` analog or implicit `niwa.yaml` at the subpath root) lets users move config around inside a brain repo without breaking slugs.
- Auth gets free if niwa shells out to `git fetch` for materialization — every credential path users already configured for git Just Works.

## Surprises

- **Terraform #19277 has been open since 2018**, with the maintainer agreeing shallow-clone-by-default would be correct, and it *still* hasn't shipped seven years later. The lesson isn't "Terraform is slow" — it's "if you don't design subpath fetching into the materialization model up front, retrofitting it is socially harder than technically harder."
- **GitHub Actions made the subpath choice for users by mandating `.github/workflows/<file>.yml`** with no nesting. Three years of community discussions ask for nested directories; GitHub has not budged. The takeaway: a strict convention with no escape hatch generates persistent low-grade user frustration.
- **chezmoi's `.chezmoiroot` is a non-URL convention** that solved the same problem niwa faces (flexible internal layout + stable external slug). The trade-off — discovery happens *inside* the cloned repo, not in the URL — is exactly what niwa needs to make the "natural home is a subdir of an existing brain repo" case feel native.
- **Nix's `?dir=` is the outlier** in not using `//`. Given Terraform's prior art and Renovate's adoption, `//` is the dominant convention; `?dir=` is a smaller idiom.
- **Helm and Crossplane sidestep the whole problem** by changing the artifact format (tarball / OCI image). For niwa this isn't viable — the source-of-truth is a git repo by definition — but it's worth noting that *if* config repos eventually got large, OCI distribution would be the natural next step.

## Open Questions

- Should niwa's slug be `git+https://...//subdir?ref=…` (Nix-style explicit protocol prefix) or `https://...//subdir?ref=…` (Terraform-style implicit git)? Explicit is safer; implicit is shorter.
- Should the lock file pin only `sha` (cheap, fast) or also `narHash`/`treeHash` (Nix-style, paranoid about non-canonical git operations)? Nix's experience suggests the extra hash is worth it for true reproducibility.
- Should `.niwaroot` (or the equivalent discovery convention) be honored, and if so, what happens when the URL slug *also* specifies a subpath? Precedence rules need to be explicit.
- Is shallow clone (`git fetch --depth=1`) good enough for snapshot materialization, or does niwa need true partial-fetch (sparse-checkout / `git archive`)? This overlaps with the partial-fetch lead.
- For private repos, does niwa want to support OCI distribution as a future option (Crossplane-style), or commit to git-only forever?

## Summary

The dominant ecosystem pattern is `<repo-url>//<subpath>?ref=<ref>` (Terraform / go-getter / Renovate) with full-clone materialization and a lock file pinning the resolved sha — every serious tool has converged on this shape, with Nix as the principled outlier (content-addressed store, `?dir=` query). niwa should adopt the `//` delimiter for subpath plus a `flake.lock`-shaped `.niwa/lock.json`, but materialize as disposable snapshots in a content-addressed cache rather than as a working tree, which sidesteps Terraform's seven-year-old "sparse checkout module" pain and naturally fixes issue #72. The biggest open question is whether to also honor an in-repo `.niwaroot`-style discovery convention so brain-repo owners can reorganize without breaking external slugs, and how that interacts with explicit subpaths in the slug.
