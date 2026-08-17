# Lead: How should CODEX_HOME reach the developer's codex invocation?

## Findings

### 1. What the shell wrapper actually is

`internal/cli/shell_init.go` generates a shell function, not a directory-change
hook. The template (`shellWrapperTemplate`, lines 37-72) defines a `niwa()`
shell function that intercepts exactly five subcommand shapes:
`create`, `destroy`, `go`, `init`, and `session create`. For those it calls
`__niwa_cd_wrap`, which runs the real `niwa` binary with a
`NIWA_RESPONSE_FILE` env var pointed at a temp file, reads a directory path the
Go binary writes into that file, and `builtin cd`s the interactive shell there
(lines 39-50). Every other subcommand (`apply`, `status`, `dispatch`, `codex`
if it existed, etc.) falls through to `command niwa "$@"` unchanged (line 68).

Activation is `eval "$(niwa shell-init auto)"`, sourced from `~/.niwa/env`
(written by `niwa shell-init install`, `runShellInitInstall`,
`shell_init.go:241-283`), which itself is sourced from the user's `.bashrc`,
`.zshenv`, or `.zshrc` via a `. "$HOME/.niwa/env"` line
(`shell_init.go:255-279`). Shell detection (`detectShell`, lines 166-174) is
`$ZSH_VERSION` / `$BASH_VERSION` presence — bash and zsh only; any other shell
(fish, dash-as-login-shell, csh, PowerShell) gets empty output and no
integration at all (`shellInitAutoCmd`, lines 148-164, "Unknown shell: empty
output, exit 0").

**Load-bearing fact:** the wrapper does not hook `cd` in general. There is no
`chpwd`/`PROMPT_COMMAND`/precmd-based "directory changed, react" mechanism
anywhere in this file. The one `precmd_functions` use that exists
(`zshCompdefDeferGuard`, lines 87-100) is a one-shot self-removing hook solely
to register `compdef` after `compinit` has run; it does nothing on subsequent
directory changes and is unrelated to env delivery. So even a developer who
has the integration installed and manually types `cd
/path/to/instance/public/tsuku` gets **no** reaction from niwa at all — the
wrapper only fires when the developer types a `niwa <verb>` command that niwa
itself intercepts.

### 2. Failure modes for the shell-wrapper approach (inferred from code, not run against a live shell except where noted)

- **Never installed the integration.** `niwa()` function never exists; plain
  `codex` is whatever's on `PATH`, defaulting to the host's `~/.codex`.
  Silent — nothing errors, codex just runs against the wrong home. (Inferred.)
- **Unsupported shell.** `detectShell()` returns `""`, `shellInitAutoCmd`
  emits nothing (lines 159-162). Silent no-op, same wrong-default outcome.
  (Inferred from code.)
- **Non-interactive shell / script / Makefile / CI step.** These typically
  don't source `.bashrc` (bash only sources it for interactive shells) and
  never source `.zshrc` either (zsh only sources it for interactive shells;
  `.zshenv` is the one that always loads, which is why `shellRCFiles`,
  lines 193-203, includes it). If the integration landed in `.bashrc` only, a
  script's `codex` call sees the host default silently. Even where it does
  load, the wrapper mechanism itself only reacts to the five `niwa` verbs, not
  to `codex` — so sourcing changes nothing for a bare `codex` invocation in a
  script. (Inferred.)
- **Editor-spawned terminal.** Most (VS Code, JetBrains) launch an interactive
  login or interactive-non-login shell and do source rc files, so the
  integration itself would load *if installed* — but see the next point.
  (Inferred; not verified against a specific editor here.)
- **New terminal opened directly inside a cloned repo several levels below
  the instance root** (e.g. `public/tsuku/`) — this is exactly today's live
  session's shape. The wrapper fires only on niwa-issued `cd`s from
  `create`/`go`/etc.; a terminal opened fresh via `cd
  .../instance/public/tsuku` never goes through niwa at all, so even with the
  integration loaded there is no signal that an instance root exists three
  levels up, let alone which one. (Inferred from the code; matches this
  session's own directory nesting, which was reached without any `niwa`
  command in this conversation.)
- **SSH.** A non-interactive `ssh host 'codex ...'` command runs a
  non-interactive shell; same rc-sourcing gap as the script case. An
  interactive SSH session sourcing `.bashrc`/`.zshrc` is no different from a
  local terminal — same "only reacts to niwa verbs" limitation. (Inferred.)
- **Detectability.** None of these are detectable by codex or the developer
  at the point of failure: `codex` has no project-level config to notice it's
  in the wrong home, and a wrong-but-present `~/.codex` means codex starts
  successfully, just against stale/unrelated auth, config, and history. This
  is a correctness bug with no error message, not a crash.

### 3. `.local.env`

`internal/config/env_output.go:127` defines `.local.env` as
`DefaultEnvOutputPath`, the fallback secret-output target
(`EffectiveEnvOutput`, lines 172-185) written per-repo by `EnvMaterializer`
(`internal/workspace/materialize.go:1268-1358`, doc comment: "generates a
`.local.env` file in the repository directory from explicit env config,
discovered env files, and inline variables"). It is a **per-repo** file (one
per cloned repo directory, written by `ResolveEnvVars` + a writer keyed by
`ctx.RepoName`), not a single per-instance file — this workspace's own
`niwa/.local.env`, read directly above, is the one materialized for the
`niwa` repo checkout specifically.

Nothing in the niwa codebase sources `.local.env` into a developer's shell.
It is a dotenv-format KEY=value file (`FormatDotenv`,
`env_output.go:16-18`) meant to be consumed by tools that read dotenv files
themselves (or, per the JSON/shell `OutputFormat` variants, machine-read or
`source`d manually) — not something niwa's own shell integration loads. The
one place niwa *does* auto-load env into an agent's process is different and
Claude-specific: `.claude/settings.json`'s `"env"` block (this instance's
copy currently carries `GH_TOKEN`), generated by `SettingsMaterializer`
(`materialize.go:1159-1171`, "generates the `.claude/settings.local.json`
file") from the same promoted-keys pipeline
(`materialize.go:960-1033`, "`[claude.env]` promoted keys"). Claude Code
reads `.claude/settings.json`'s `env` block itself, at its own startup, with
no shell involvement — this is exactly the "agent reads its own config file,
no env var needed" pattern, but it works because Claude Code *has*
project-level config discovery. Codex, per the lead's framing, doesn't: it
has no per-directory settings file, only `$CODEX_HOME`. So `.local.env` is
not a viable carrier (nothing loads it automatically), and the
`.claude/settings.json` precedent doesn't transfer because it depends on a
capability Codex lacks.

### 4. How niwa already launches/wraps agent processes

There is no `niwa claude` or `niwa codex` launcher subcommand today — I
confirmed this against the full `internal/cli/` file listing (no `claude.go`
or `codex.go`, no such `Use:` string anywhere in the command surface).

The closest precedent is `internal/cli/dispatch_launcher.go`
(`realDispatchLaunch`, lines 42-102), which niwa's own `dispatch` command
uses to spawn a **background** worker:

```go
bin, err := exec.LookPath("claude")
...
cmd := exec.CommandContext(ctx, bin, args...)
cmd.Dir = instanceDir
if env == nil {
    cmd.Env = os.Environ()   // inherit parent env
} else {
    cmd.Env = env            // explicit, credential-scrubbed env (watch path only)
}
```

This is hardcoded to the `claude` binary (`exec.LookPath("claude")`, line
82) — it is not a generic agent launcher, and per
`DESIGN-interactive-codex-session.md` (read in full; lines 354-366), the
design deliberately keeps it that way: "launch-coupled provisioning entry
points" (`SessionStart` hook via `instance_from_hook.go`, `niwa dispatch`,
`niwa watch`) all pin `applier.Agent = AgentClaude` regardless of
`default_agent`, and `niwa dispatch` **refuses to run** when the resolved
workspace agent is Codex, naming `NIWA_AGENT=claude` as the escape hatch.
That design explicitly scoped "launching, hooks/provisioning" for Codex as
out-of-scope future work (PRD line 24, "This PRD scopes the launch slice";
line 212, "SHALL NOT gain code that launches, spawns, or exec's an agent
session"). So the precedent that exists is real (niwa owning `cmd.Env` for a
spawned process) but it is currently Claude-only and background-worker-only
— it says nothing about the developer's own interactive `codex` invocation,
which is the case in question.

`resolveSessionAgent` (`internal/cli/agent.go:16-21`) is the one general
precedent worth noting for *resolution*, not delivery: `flag > NIWA_AGENT env
> [workspace].default_agent > claude`, mirroring `DispatchModel`'s shape. Any
CODEX_HOME mechanism should probably resolve its target directory the same
way the agent itself is resolved, for consistency, though CODEX_HOME's value
(a path) isn't a discriminator the way `Agent` is.

### 5. Whether codex has a flag/hook that avoids needing CODEX_HOME at all

Checked directly against the installed binary
(`/home/dgazineu/.tsuku/tools/current/codex --help` and `codex exec --help`,
run in this session). Full top-level and `exec` option lists were inspected;
there is **no** `--codex-home`, `--config-dir`, or equivalent flag. The two
config-related flags that exist are:

- `-c, --config <key=value>` — overrides individual TOML fields inside
  whatever `config.toml` was already loaded (from `$CODEX_HOME/config.toml`,
  default `~/.codex/config.toml`); it overrides *values*, not *which
  directory* config is loaded from.
- `-p, --profile <NAME>` — "Layer `$CODEX_HOME/<name>.config.toml` on top of
  the base user config." This flag's own help text names `$CODEX_HOME`
  explicitly as the base it layers onto — confirming CODEX_HOME must already
  be resolved before `--profile` means anything; it cannot redirect CODEX_HOME
  itself.

`strings` against the codex binary for `CODEX_HOME` (backgrounded, completed
with zero matches for the grep run against its output) turned up nothing
beyond what `--help` already shows — no hidden flag name surfaced. So: the
environment variable is the only lever. There is no CLI escape hatch, and
therefore no way to make plain `codex` pick up a different home without
either (a) that env var being set in the process's environment before exec,
or (b) something that intercepts the `codex` name on `PATH` and injects it
(a wrapper binary/script front-running the real `codex`, e.g. a
per-instance `PATH` entry — this is a variant of the shell-wrapper idea, not
an escape from the "someone must set CODEX_HOME" requirement, just a
different place to set it: a `PATH`-shadowing shim instead of a shell
function).

## Implications

The developer's own interactive `codex` invocation is a **plain, unwrapped
exec** the moment it's typed at any prompt niwa doesn't control — and niwa,
by design (`DESIGN-interactive-codex-session.md`'s "no launcher" boundary,
plus the total absence of a `niwa codex`/`niwa claude` subcommand), does not
currently stand between the developer and the agent binary for interactive
use. That absence is exactly why the shell wrapper is attractive as primary:
it's the one place niwa already sits in the developer's actual invocation
path (`niwa()` shadows `niwa` in the shell), and extending `EnvFileWithDelegation`
or the `niwa()` function to additionally export `CODEX_HOME` for a
`go`/`create`-entered instance is a small, in-character addition — reusing
the exact seam that already cd's the shell.

But the shell wrapper's real behavior is narrower than "direnv-style,
fires on entering the instance directory," which is how the lead's framing
poses the two options. It fires only on the five `niwa` verbs. Extending it
to "export CODEX_HOME whenever the shell is inside an instance directory"
would require net-new mechanism this file does not have today: a `cd`-hook
(zsh `chpwd`, bash has no equivalent — only lossy `PROMPT_COMMAND`
polling) that fires on *every* directory change, not just niwa-issued ones,
to catch the "opened a terminal three levels deep" and "manually cd'd"
cases — both of which are the common case, not the edge case (this very
session is one). Building and shipping that polling/hook logic for bash
(no native chpwd) is real, cross-shell engineering, not a one-line addition.
Even after building it, the failure modes in section 2 that don't route
through an interactive shell at all — scripts, Makefiles, non-interactive
SSH, and "integration never installed" — are structurally unfixable by any
shell hook, because the hook only exists inside a shell that sourced it.

The launcher fallback (`niwa codex ...` or `niwa run codex ...`) does not
have this ceiling: it is a real `exec.Command` niwa fully controls
(`dispatch_launcher.go` is the extant pattern —
`cmd.Env = append(os.Environ(), "CODEX_HOME=...")`, `exec.LookPath("codex")`,
run in `instanceDir`), so it is correct by construction every time it's
actually invoked, in a script, a Makefile, CI, or an interactive shell alike,
with no dependency on shell family, rc-sourcing, or directory depth. Its
cost is the opposite of the wrapper's: it only helps a developer who
remembers to type `niwa codex` instead of `codex`, so it doesn't fix "I
typed plain `codex`" — the exact failure mode the wrapper is meant to catch.
Because codex has no CLI flag to redirect its home (section 5), a wrapper
executable placed on `PATH` ahead of the real `codex` (shadowing the name
rather than requiring the developer to type a different command) is a third
variant worth naming: it gets "plain `codex` just works" without needing a
shell function at all, at the cost of PATH-ordering correctness (niwa's
bin dir must precede the real `codex`'s location, and this can silently
break if the developer's PATH is reordered) and needing a real per-shim
executable or script per instance (or one shim that resolves "which
instance am I in" from `$PWD`, which re-introduces the same "does this
directory count as inside an instance" resolution problem the shell wrapper
already needs — but at least this shim runs even under non-interactive
invocation, because it's not shell-integration-dependent, only PATH-order
dependent).

Given the actual failure surface, the shell wrapper is not "recommended
home, dedicated subcommand is the explicit fallback" as cleanly as the framing
suggests once you see it fires on five verbs, not on `cd`. The wrapper is a
reasonable **best-effort default** for the common interactive case (a
developer who ran `niwa create`/`niwa go` and stayed in that same shell), but
it needs to be paired with something guaranteed-correct for everything else
— which points to building both, not one as primary with the other purely
as a fallback the developer has to remember exists. The PATH-shim variant is
worth surfacing as a third option in the recommendation the lead asked for,
since it's the only one of the three that makes plain `codex` correct
*without* requiring shell-integration installation, while still working
non-interactively.

## Surprises

- The shell wrapper is much narrower than "activates on entering the
  instance directory" — it only reacts to five specific `niwa` subcommands.
  A manual `cd` (the normal way developers navigate after the first
  `create`) is invisible to it entirely. This matters for the decision: the
  lead's framing assumed direnv-like behavior that the code doesn't have.
- `niwa dispatch` **already refuses to run for a Codex-default workspace**
  (`DESIGN-interactive-codex-session.md` lines 354-366) — dual-agent support
  is explicitly half-built: context materialization exists for Codex, but
  the one place niwa spawns an agent process today actively rejects Codex.
  Any CODEX_HOME design should account for this: `dispatch` is not a model
  for "how niwa launches codex" yet, it's evidence that launching Codex is
  still an open gap end to end, not just for the interactive case.
- No `AGENTS.md` exists at this live instance's root despite
  `internal/agent` having full Codex filename support — this instance
  wasn't materialized under `default_agent = "codex"`, consistent with
  `agent.go:56` (workspace CLAUDE.md present, confirming Claude is the
  resolved agent here). Worth noting only as a "this instance doesn't
  exercise the Codex path" caveat on any inference drawn from its disk
  layout.
- Codex's own `--profile` flag help text names `$CODEX_HOME` directly in its
  description, which is a small but concrete confirmation that codex's own
  authors treat `$CODEX_HOME` as the sole home-resolution mechanism — there's
  no secondary discovery path (e.g. no walking up from cwd) hinted at
  anywhere in the flag surface.

## Open Questions

- Whether a bash-side `cd`-reaction mechanism (polling `PROMPT_COMMAND`,
  since bash has no native `chpwd`) is worth building at all, given it only
  closes the gap for interactive shells that already sourced the
  integration — a human call on whether the added complexity earns its
  keep versus just documenting "run `niwa go`/`niwa codex`, don't `cd`
  manually."
- Whether the PATH-shim variant (a real shim executable shadowing `codex`)
  is worth prototyping as a third option — it's the only one of the three
  that makes bare `codex` correct without shell-integration installation,
  but I have not verified whether niwa already puts any directory ahead of
  the rest of `PATH` per-instance (worth checking `~/.niwa/bin`'s ordering
  relative to wherever `codex` itself installs, e.g. via tsuku's own
  `~/.tsuku/bin`) — that's a concrete follow-up read, not done here.
- Whether `niwa dispatch`'s current Codex refusal is meant to be lifted in
  the same slice that solves interactive CODEX_HOME delivery, or is a
  separate, later piece of work — this affects whether the launcher
  subcommand should be scoped to interactive-only or designed to also
  become dispatch's Codex spawn path.

## Summary

Codex has no CLI flag to redirect its home directory — `$CODEX_HOME` is the only lever — and niwa's shell wrapper only reacts to five `niwa` subcommands (`create`, `destroy`, `go`, `init`, `session create`), not to a plain `cd` into an instance, so it silently misses the common cases of a manually-`cd`'d shell, a terminal opened deep inside a cloned repo, or any non-interactive invocation (scripts, Makefiles, CI, non-interactive SSH). `.local.env` and the `.claude/settings.json` env-block precedent both fail to transfer: neither is auto-loaded into a developer's shell, and the settings.json trick depends on Claude Code's project-config discovery, which Codex lacks entirely; niwa's one real "own the child process's env" precedent (`dispatch_launcher.go`) is hardcoded to `claude` and currently refuses to run at all for a Codex-default workspace. The open call for a human is whether to build a guaranteed-correct `niwa codex` launcher (or PATH-shadowing shim) as a first-class mechanism alongside the shell wrapper rather than as a secondary fallback, since the wrapper's coverage gap is structural, not an edge case.
