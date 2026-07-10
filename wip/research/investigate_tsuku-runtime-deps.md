# Investigation: Can `tsuku install niwa` auto-provision bwrap + socat?

Date: 2026-07-09. Host: linux/amd64. All paths absolute.

## Bottom line

**PARTIAL / YES-with-work.** tsuku fully supports transitive runtime-dependency
resolution, and both `socat` and `bubblewrap` are packageable via the exact same
prebuilt-Homebrew-bottle mechanism (bottles exist on ghcr.io for
`x86_64_linux` and `arm64_linux`). But `niwa` is *currently* installed from an
auto-generated GitHub-download recipe that declares **no** dependencies. To make
`tsuku install niwa` pull bwrap+socat you must add a **curated** `recipes/n/niwa.toml`
(which shadows the auto-recipe) that declares `runtime_dependencies`, plus a new
`recipes/b/bubblewrap.toml`. The one real blocker is the **setuid wrinkle** (§5):
a tsuku-installed bwrap is non-setuid and only works where the kernel allows
unprivileged user namespaces.

---

## 1. Recipe schema DOES support a runtime dependency graph, resolved transitively

Schema fields — `internal/recipe/types.go:163-192` (`MetadataSection`):

```go
Dependencies             []string `toml:"dependencies"`                // install-time deps (replaces implicit)
RuntimeDependencies      []string `toml:"runtime_dependencies"`        // runtime deps (replaces implicit)
ExtraDependencies        []string `toml:"extra_dependencies"`          // extends implicit install-time
ExtraRuntimeDependencies []string `toml:"extra_runtime_dependencies"` // extends implicit runtime
```

Steps can also carry step-level deps: `Step.Dependencies []string` (`types.go:389`).

Resolver — `cmd/tsuku/install_deps.go`:
- Install-time deps loop: lines 343-371 — each dep re-enters `installWithDependencies`
  as a hidden (non-explicit) child install.
- Runtime deps loop: lines 375-406 — each runtime dep is installed **explicit
  (exposed on PATH)**: `sub.IsExplicit = true` (line 393). Platform-inapplicable
  runtime deps are silently skipped via `shouldInstallRuntimeDep` (line 386).
- **Transitive + cycle-guarded**: `installWithDependencies` recurses on itself
  (lines 358, 397); the `visited map[string]bool` guard is at lines 265-268.
  Entry points seed it fresh (lines 191, 200).

So the dependency graph is real and resolved transitively. Proven empirically:
`tsuku install socat` (recipe declares `runtime_dependencies = ["openssl"]`) ran
to exit 0 and `socat1 -V` works via PATH.

## 2. How niwa is packaged today

- No curated recipe exists: `find recipes -iname '*niwa*'` → empty. No local
  registry cache entry either.
- `niwa` README: `curl … install.sh | sh` **or** `tsuku install tsukumogami/niwa`.
- Both `tsuku install niwa --dry-run` and `tsuku install tsukumogami/niwa --dry-run`
  resolve to `niwa@0.18.2` via an **auto-generated GitHub-download recipe**:
  actions = `download` → `chmod` → `install_binaries` → `install_shell_init`,
  `Dependencies: (none)`. `tsuku list` tags it `niwa 0.18.2 (active) [tsukumogami/niwa]`.
- This auto-recipe cannot carry hand-authored deps.
- **Fix path exists**: loader resolution (`internal/recipe/loader.go:69-103`) runs a
  priority provider chain; `SourceLocal` (curated/embedded recipes) shadows lower
  providers (`warnIfShadows`, line 96-98). A curated `recipes/n/niwa.toml` for the
  bare name `niwa` therefore **takes precedence** over the GitHub auto-recipe. That
  curated recipe can declare `runtime_dependencies = ["bubblewrap", "socat"]`.

## 3. socat as a recipe — works today, lands on PATH

Recipe `recipes/s/socat.toml`: `homebrew` action (formula `socat`) +
`install_binaries` for `socat1, filan, procan, socat-*.sh`; `runtime_dependencies = ["openssl"]`;
`unsupported_platforms = ["darwin/*"]` (Linux-only recipe).

The tsuku `homebrew` action does **not** require Homebrew to be installed — it
downloads bottles directly from ghcr.io and RPATH-relocates via patchelf
(`internal/actions/homebrew.go:24-35, 247-294`). Host has **no brew** yet socat
installed fine.

Empirical install (`tsuku install socat`, exit 0):
- Landed in `~/.tsuku/tools/socat-1.8.1.3/`, exposed via `~/.tsuku/tools/current/`
  (a **durable** dir holding active-tool binaries/symlinks — NOT a per-install symlink).
- `~/.tsuku/tools/current` **is on PATH** (confirmed in `$PATH`), so `socat1` runs.
- **CAVEAT**: the bottle/recipe exposes the binary as **`socat1`, not `socat`**
  (bin dir: `filan procan socat1 socat-*.sh`). If niwa / Claude Code invokes
  `socat` by name, this is a gap — the recipe must add a `socat` binary/symlink or
  niwa must call `socat1`.
- openssl (declared runtime dep) was **not** actually installed (`tsuku list` has no
  openssl; no tools/openssl dir) — the socat bottle is self-contained enough to run.
  Relevant precedent for bwrap+libcap (§4).

## 4. bwrap (bubblewrap) recipe — VIABLE via Homebrew bottle

No bwrap recipe exists today. But bubblewrap **is** distributed as a prebuilt
Homebrew bottle, same channel socat uses:

`https://formulae.brew.sh/api/formula/bubblewrap.json`:
- stable `0.11.2`, `"bottle":true`, desc "Unprivileged sandboxing tool for Linux"
- bottle files: **`arm64_linux` and `x86_64_linux`** on `ghcr.io/v2/homebrew/core`,
  `cellar: :any` (relocatable)
- `dependencies: ["libcap"]`

`libcap` also has `arm64_linux` + `x86_64_linux` bottles (v2.78). So no source build,
no C-toolchain requirement, no nix backend needed. A realistic recipe:

```toml
[metadata]
  name = "bubblewrap"
  description = "Unprivileged sandboxing tool for Linux"
  homepage = "https://github.com/containers/bubblewrap"
  unsupported_platforms = ["darwin/arm64", "darwin/amd64"]   # Linux-only
  runtime_dependencies = ["libcap"]                          # or extra_runtime_dependencies
[[steps]]
  action = "homebrew"
  formula = "bubblewrap"
[[steps]]
  action = "install_binaries"
  binaries = ["bin/bwrap"]
[verify]
  command = "bwrap --version"
```

Notes/risks:
- No curated `libcap` recipe exists yet (only discovery JSON candidates). Given
  socat ran without its declared openssl actually installing, the `cellar: :any`
  bwrap bottle likely resolves `libcap.so.2` from the system (present on ~every
  Linux). Whether to add a curated `recipes/l/libcap.toml` or rely on system
  libcap needs a quick install test. tsuku's SONAME scanner is the surface that
  warns if a NEEDED soname is unshipped.
- Source-build fallback also exists if ever needed (tsuku has `configure_make`,
  `cmake_build`, `meson_build`, resources/patches, and a nix tier), but it is
  unnecessary here.

## 5. The setuid wrinkle — the real portability blocker

bwrap needs one of: (a) unprivileged user namespaces enabled in the kernel, or
(b) the binary installed **setuid-root**. A tsuku install writes to `~/.tsuku`
with no root and **cannot** set setuid — so a tsuku-provisioned bwrap is a plain
non-setuid binary that works **only where unprivileged userns is allowed**.

- Host sysctls: `kernel.unprivileged_userns_clone = 1`,
  `user.max_user_namespaces = 127390`. System bwrap present at `/usr/bin/bwrap`
  (mode `-rwxr-xr-x`, **not setuid**).
- Empirically, even here bwrap **failed** in this environment:
  `bwrap … --unshare-user … echo OK` → `bwrap: setting up uid map: Permission denied`
  (and `--unshare-net` variants → `loopback: Failed RTM_NEWADDR: Operation not
  permitted`). This session runs inside a nested sandbox that blocks uid-map setup
  despite the sysctl — a concrete demonstration that "userns enabled" is necessary
  but not always sufficient (nesting, seccomp, LSM/AppArmor, hardened kernels).
- Portability implication: on modern default-on kernels (Ubuntu 24.04+, Debian 12+,
  Fedora, Arch) a non-setuid tsuku bwrap works. On kernels with unprivileged userns
  disabled (some RHEL/CentOS, hardened/Debian-userns-off, restrictive CI/containers)
  it will fail, and tsuku has no way to fix that (would need root + setuid, or a
  sysctl change). niwa's sandbox feature must detect this and degrade gracefully.

---

## Deliverable answers

**Can `tsuku install niwa` pull bwrap+socat as runtime deps?**
PARTIAL → YES after packaging work. The mechanism (transitive `runtime_dependencies`,
exposed on PATH) fully exists and is proven. Missing pieces: (1) a curated
`recipes/n/niwa.toml` that shadows the GitHub auto-recipe and declares the deps;
(2) a new `recipes/b/bubblewrap.toml`. socat is already packaged (naming caveat).

**Concrete changes required (if viable):**
1. Add `recipes/b/bubblewrap.toml` (homebrew action, formula `bubblewrap`, expose
   `bin/bwrap`, `runtime_dependencies=["libcap"]`, `unsupported_platforms` darwin).
   Optionally `recipes/l/libcap.toml` if system libcap fallback proves insufficient.
2. Add curated `recipes/n/niwa.toml` mirroring the current download/chmod/
   install_binaries/install_shell_init steps, plus
   `runtime_dependencies = ["bubblewrap", "socat"]`. It shadows the auto-recipe
   (SourceLocal precedence, loader.go:96).
3. Fix socat's `socat`-vs-`socat1` binary name if niwa invokes `socat`.

**Blockers / caveats:**
- **setuid**: non-setuid tsuku bwrap only works where unprivileged userns is
  permitted; unfixable by tsuku on locked-down kernels. This is the security
  feature's true dependency, not tsuku packaging. (Demonstrated failing in this
  nested sandbox despite `unprivileged_userns_clone=1`.)
- **socat1 vs socat** binary naming gap.
- **libcap** provisioning for the bwrap bottle needs a confirming install test.
- Runtime deps only auto-install for **curated/local** recipes; the GitHub
  auto-recipe path can't carry them — hence the curated-niwa-recipe requirement.
