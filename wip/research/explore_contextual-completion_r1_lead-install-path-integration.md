# Lead: How does completion land on a user's machine by default across install paths?

## Findings

### Shell integration vs. completion: one artifact, emitted together

`niwa shell-init bash` and `niwa shell-init zsh` each emit a single blob that
concatenates two things (see
`/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/internal/cli/shell_init.go`
lines 60-90):

1. `shellWrapperTemplate` (lines 37-58) — the `niwa()` shell function that
   intercepts `create`/`go` for cwd handoff, plus `export _NIWA_SHELL_INIT=1`.
2. Cobra's auto-generated completion script
   (`rootCmd.GenBashCompletionV2` / `rootCmd.GenZshCompletion`).

There is no code path in niwa that emits only the wrapper or only completion;
they travel as one artifact. From a delivery standpoint, "shell integration"
and "completion" are the same thing.

`niwa shell-init auto` (lines 92-108) detects `$ZSH_VERSION`/`$BASH_VERSION`
and dispatches to the right subcommand, or outputs nothing on an unknown
shell.

### Path A: niwa's own install.sh (root of the niwa repo)

`/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/install.sh` is the
curl-to-sh installer advertised in the README. It does **not** call
`niwa shell-init install`. Instead, it inlines the same env-file content
directly (lines 107-121), writing `~/.niwa/env` with:

```sh
export PATH="$HOME/.niwa/bin:$PATH"
if command -v niwa >/dev/null 2>&1; then
  eval "$(niwa shell-init auto 2>/dev/null)"
fi
```

Then it appends `. "$HOME/.niwa/env"` to `~/.bashrc`, `~/.bash_profile`/`~/.profile`, or `~/.zshenv` depending on `$SHELL` (lines 123-168), guarded by an idempotency check (`grep -qF "$ENV_FILE" "$config_file"`, line 132).

The `--no-shell-init` flag (line 24) lets the user opt out of the delegation
line, leaving only the PATH export in `~/.niwa/env`.

Net result: **completion is enabled by default** for bash/zsh users who run
the curl installer, because the eval-`niwa shell-init auto` line re-emits
wrapper+completion on every new shell.

### Path B: tsuku recipe for niwa

The recipe is **embedded inside the niwa repo**, not in the tsuku
monorepo's `recipes/` directory:
`/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/.tsuku-recipes/niwa.toml`.
`find` and `grep` for `niwa.toml` under tsuku's `recipes/` tree returned nothing
(tsuku commit `45e1df93 revert: remove niwa recipe (belongs in niwa repo)`
confirms the intentional move).

Current recipe contents:

```toml
[[steps]]
action = "install_shell_init"
phase = "post-install"
source_command = "{install_dir}/bin/niwa shell-init {shell}"
target = "niwa"
shells = ["bash", "zsh"]
```

The `install_shell_init` tsuku action
(`/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/tsuku/internal/actions/shell_init.go`)
runs `{install_dir}/bin/niwa shell-init bash` and `... shell-init zsh`, captures
stdout, and writes each to
`$TSUKU_HOME/share/shell.d/niwa.bash` and `$TSUKU_HOME/share/shell.d/niwa.zsh`
(mode 0600). Cleanup entries with SHA-256 content hashes are recorded in the
install state.

Per tsuku's shell-integration guide
(`/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/tsuku/docs/guides/shell-integration.md`
line 178), "The managed `$TSUKU_HOME/env` file sources the appropriate
per-shell init cache (`.init-cache.bash` or `.init-cache.zsh`) at startup,
so tools with shell functions are available in every new terminal after
install — no `tsuku shellenv` call required." This is materially different
from Path A: tsuku captures the wrapper+completion output at install time
and bakes it into a static cache under `$TSUKU_HOME/share/shell.d/`, then
sources that cache from the tsuku-managed env file. Path A runs `niwa
shell-init auto` live on every shell startup.

History note: `install_shell_init` was added (`ade997e`, PR #43), reverted
on a branch (`8faeb72`, "until tsuku bug is fixed" — tsukumogami/tsuku#2218),
but the revert branch `fix/43-revert-install-shell-init` is **not** merged
to main. The upstream fix landed in tsuku as commit `8a78659f fix(executor):
skip post-install phase steps in ExecutePlan main loop (#2225)`. `87d2b0e
Update niwa.toml` (current main) switched the command from bare `niwa
shell-init {shell}` to `{install_dir}/bin/niwa shell-init {shell}`, which
satisfies `validateCommandBinary` in the tsuku action (lines 239-282) that
requires the executable to resolve inside `ToolInstallDir`.

Net result: **completion is enabled by default** for users who install niwa
via tsuku, via a different file (`$TSUKU_HOME/share/shell.d/niwa.{bash,zsh}`)
and a different sourcing chain (tsuku's `$TSUKU_HOME/env`, not
`~/.niwa/env`).

### Chain confirmation (Path A)

`~/.bashrc` sources `~/.niwa/env` -> `~/.niwa/env` runs `eval "$(niwa
shell-init auto)"` -> `shell-init auto` dispatches to bash/zsh subcommand ->
subcommand prints wrapper + cobra completion to stdout -> eval registers
the `niwa()` function and the `_niwa` completion function. Dynamic
completion (which this exploration ultimately targets) works because cobra
completions call back into `niwa __complete ...` at tab time, picking up
whatever the currently-installed binary supports.

### Idempotency details

`addSourceLine` in `shell_init.go` (lines 151-166) reads the rc file and
skips the append if the exact `sourceLine` substring is present. It uses
`strings.Contains`, so whitespace variations will not match. The status
command (`shell-init status`, lines 233-260) reports whether the wrapper
is loaded in the current shell (`$_NIWA_SHELL_INIT`), and whether the env
file is "delegation block present" (contains the string `niwa shell-init
auto`) or "PATH-only".

`install.sh`'s `add_to_config` helper (lines 128-143) uses `grep -qF
"$ENV_FILE"` against the config file — matches on the env file path, not
the full source line. So a user who edits the path gets a duplicate;
otherwise idempotent.

`shell-init uninstall` (lines 208-231) rewrites `~/.niwa/env` to the
PATH-only variant but does not touch the rc file. Re-running `install`
afterwards restores the delegation block.

Path B (tsuku): `install_shell_init` always writes the shell.d files
unconditionally. Re-install overwrites with current `niwa shell-init`
output. The content hash in the cleanup record detects tampering during
uninstall.

## Implications

**Delivery is already done on both paths.** For the exploration's core
goal (completion wired on by default when niwa is installed), no new
install-path work is strictly required. Any user who runs the curl
installer or `tsuku install tsukumogami/niwa` gets completion in their
next shell.

**But the two paths produce different static artifacts in different
locations, sourced via different mechanisms.** This matters for dynamic
completion because:

- Path A sources `~/.niwa/env`, which re-runs `niwa shell-init auto` on
  every new shell. Upgrading niwa silently picks up new completions the
  next time a shell starts.
- Path B captures the output into `$TSUKU_HOME/share/shell.d/niwa.{bash,zsh}`
  at install time. Upgrading niwa through tsuku re-runs the
  `install_shell_init` post-install hook and rewrites the cache. But if a
  user upgrades niwa outside tsuku (e.g. by replacing the binary in
  `$TSUKU_HOME/tools/niwa-<version>/bin/`), their shell.d cache stays
  stale until tsuku re-runs the hook.

**Dynamic `__complete` callbacks work identically on both paths.** Since
cobra completion scripts call back into the niwa binary on the PATH, and
both paths ensure a recent niwa is on PATH, runtime name resolution
(workspaces, instances, repos) works regardless of which path was used to
install.

**No gap to close for delivery.** The gap, if any, is documentation: the
install-path matrix above is not explained anywhere.

**One cross-repo coordination note remains:** the recipe command in
`.tsuku-recipes/niwa.toml` lives in the niwa repo but is consumed by
tsuku. Any new flags or behaviors in `niwa shell-init` that would change
the emitted script need to be compatible with both (a) Path A's
eval-on-shell-start model and (b) Path B's bake-at-install-time model.

## Surprises

1. **The niwa tsuku recipe lives inside the niwa repo, not the tsuku
   monorepo.** tsuku even had a revert commit (`45e1df93`) removing a
   niwa.toml it had briefly owned. This is unusual for tsuku recipes (the
   other 40+ in `recipes/n/` all live under the tsuku monorepo) and
   suggests the project is iterating toward an "in-tree recipe" pattern
   for tools that want to own their packaging.

2. **install.sh does not call `niwa shell-init install`.** It duplicates
   the env file content inline. The `EnvFileWithDelegation()` helper in
   `shell_init.go` exists precisely to keep the two copies in sync, but
   install.sh does not call the binary — it hardcodes the same text. Any
   future change to the env file format has to be kept in lockstep
   across `install.sh` and `shell_init.go`.

3. **`shell-init install` and `install.sh` write slightly different
   things.** `shell-init install` writes `. "$HOME/.niwa/env"` as the rc
   source line. `install.sh` writes the same source line preceded by a
   blank line and a `# niwa` comment. Both are idempotent via substring
   match, but they are not byte-identical.

4. **The revert-until-tsuku-bug-fixed branch exists but is not merged.**
   Main has the `install_shell_init` active. The tsuku-side fix
   (`8a78659f`) is in tsuku, so the breakage is presumably resolved — but
   I did not verify this end-to-end by running a tsuku install.

5. **Path B uses a content-addressed cleanup record** with SHA-256 hashes
   (`recordCleanup`, shell_init.go action lines 142-149). This protects
   against uninstall deleting a file a user has customized. Path A has
   no such protection — `shell-init uninstall` rewrites the env file
   unconditionally (though it leaves the rc source line in place).

## Open Questions

1. Is the revert branch still needed? If tsukumogami/tsuku#2225 fixes the
   underlying executor bug, the branch can be deleted. Someone should
   confirm by running `tsuku install tsukumogami/niwa` with a current
   tsuku and verifying the shell.d files appear.

2. Should `install.sh` delegate to `niwa shell-init install` instead of
   duplicating the env file content? This would reduce drift risk but
   requires the newly-installed binary to run correctly on the user's
   system before rc files are touched. Maybe an acceptable trade-off.

3. For dynamic completion, does the Path B cache staleness matter? If a
   user's tsuku-installed niwa gets newer completion logic via a niwa
   upgrade (tsuku-managed), tsuku re-runs the hook and refreshes the
   cache. If completion is 100% dynamic via `__complete` callbacks, the
   cache content rarely changes — only the wrapper and command tree
   change. Human judgment needed: is this a real user-facing issue or
   theoretical?

4. Windows/PowerShell: neither path supports it. Is that in scope for the
   completion exploration?

5. Fish is in `allowedShells` for the tsuku action but not in niwa's
   `shellInitAutoCmd` detection or in the recipe's `shells` list. Does
   the completion effort include fish?

## Summary

On both install paths — niwa's own `install.sh` and the in-repo tsuku
recipe at `.tsuku-recipes/niwa.toml` — shell integration and tab
completion are the same artifact (cobra completion is concatenated to
`shellWrapperTemplate` in a single `shell-init bash/zsh` output), and
both paths enable it by default today, so the exploration has no
install-wiring delta to close for delivery. The two paths diverge in
mechanism: `install.sh` writes `~/.niwa/env` with a live `eval "$(niwa
shell-init auto)"` line, while the tsuku recipe bakes the output into
`$TSUKU_HOME/share/shell.d/niwa.{bash,zsh}` at post-install time, which
means tsuku-installed users can get stale shell caches if the niwa
binary is upgraded outside tsuku's own upgrade flow. The biggest open
question is whether the tsuku bug that triggered the
`fix/43-revert-install-shell-init` branch is actually resolved in the
tsuku release users currently run — this needs an end-to-end verification
before claiming Path B works.
