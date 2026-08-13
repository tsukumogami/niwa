# Lead: Which declared secrets are genuinely load-bearing for cloning and materializing the tsukumogami workspace, versus only needed for a full development workflow — and is "no secret is required to build and operate the workspace" literally achievable?

Round 1, `oss-no-infisical`. Visibility: public.

All paths below are relative to either the workspace root
`/Users/danielgazineu/dev/niwaw/tsuku/tsuku+tsuku_oss_no_infisical-26c0f110/`
or to the niwa working copy
`/Users/danielgazineu/dev/niwaw/tsuku/tsuku+tsuku_oss_no_infisical-26c0f110/public/niwa/.claude/worktrees/oss-no-infisical/`
(the latter is implied for `internal/`, `cmd/`, `docs/`, `test/`, `.github/`).

---

## Findings

### 0. Where the enforcement sits, and what it can and cannot see

`checkRequiredKeys` (`internal/workspace/required.go:47`) walks the
**post-merge, post-resolve** `config.WorkspaceConfig` and, for every key
listed under a `*.required` sub-table, looks up `t.Values[key]`. A key
whose `MaybeSecret` carries neither a resolved secret nor a non-empty
`Plain` is a miss (`isEmptyMaybeSecret`, `internal/workspace/required.go:130`).
Misses are collected across:

- `env.vars` / `env.secrets` (`required.go:57-58`)
- `claude.env.vars` / `claude.env.secrets` (`required.go:61-62`)
- **every** `repos.<name>.env.*` and `repos.<name>.claude.env.*` (`required.go:65-73`)
- `instance.env.*` and `instance.claude.env.*` (`required.go:76-82`)

The per-repo loop iterates `cfg.Repos` — the **config** map, not the set
of repos actually classified for cloning. So `[repos.niwa.env.secrets.required]`
fires whether or not the `niwa` repo is in scope for this apply.

Two properties matter for this lead:

1. **The check runs before any repo is cloned.** In
   `internal/workspace/apply.go`, `checkRequiredKeys` is called at line
   1325; the comment at line 1329 begins "Step 3: Create group
   directories and clone repos concurrently." A required miss aborts
   `runPipeline` with `return nil, err` at line 1326. Nothing is
   materialized. This is why the observed symptom is "`niwa init` fails
   outright" rather than "workspace comes up degraded".

2. **The check reads config values only — never the ambient process
   environment.** `grep -rn "os.Environ()\|os.LookupEnv" internal/workspace/ internal/config/`
   returns three hits, none of them env-var seeding
   (`internal/workspace/bootstrap.go:258,260` sanitize a git commit env;
   `internal/workspace/worktree_content.go:852` builds a subprocess env).
   The value layers that *do* feed `Values` are: inline TOML in the team
   config, the team overlay, the personal/global overlay, and vault
   resolution. `[env] files` (e.g. `env/workspace.env`) are read later,
   inside `ResolveEnvVars` (`internal/workspace/materialize.go:1163-1185`),
   and never flow back into `cfg.Env.*.Values` — so an env file cannot
   satisfy a required key either.

`--allow-missing-secrets` does not help, and there are two distinct
reasons it does not:

- By design it does not downgrade `*.required` (documented at
  `internal/workspace/required.go:36-45` and `docs/guides/vault-integration.md:174-181,576`).
- More importantly, it only catches `vault.ErrKeyNotFound`. A provider
  that is **unreachable** — Infisical CLI not installed, or installed but
  unauthenticated — returns `vault.ErrProviderUnreachable`
  (`internal/vault/infisical/subprocess.go:139-146` for a start failure,
  `:150-156` for an auth-marker exit), and the resolver turns that into a
  hard error with no `AllowMissing` branch at all
  (`internal/vault/resolve/resolve.go:548-555`). The `AllowMissing`
  downgrade lives exclusively in the `ErrKeyNotFound` branch
  (`resolve.go:525-539`).

So the two personas fail at different stages:

| Persona | Overlay reachable? | Failure point | Error class |
|---|---|---|---|
| OSS contributor, no overlay access | No — silently skipped | `checkRequiredKeys`, `apply.go:1325` | 5 required keys unsupplied |
| Maintainer, fresh host, no Infisical | Yes | vault resolve, `resolve.go:548` | `ErrProviderUnreachable`, not downgradable |

The overlay silent-skip is confirmed at `internal/workspace/apply.go:989-994`:
a *fresh* clone attempt that fails (`wasCloneAttempt == true`) does
`break` with no error, on the theory that the overlay repo does not
exist. For an OSS contributor the overlay exists but is private, which is
indistinguishable from absent at this seam. Result: no `[vault.provider]`,
no `vault://` bindings, no vault calls at all — and then five required
keys that nothing can possibly fill.

Also relevant: `niwa init` does not even expose `--allow-missing-secrets`.
Flag registration at `internal/cli/init.go:45-54` has no such flag; only
`apply` (`internal/cli/apply.go:24`) and `create` (`internal/cli/create.go:22`)
do.

---

### 1. `GH_TOKEN` — declared required, but nothing in the clone path reads it, and the documented setup does not satisfy it

**What consumes a GitHub token in niwa.** Exactly one resolver:
`resolveGitHubToken()` at `internal/cli/token.go:12-24`. It reads
`GITHUB_TOKEN` from the process env, then `GH_TOKEN`, then shells out to
`gh auth token`. Callers: `init.go:140,639,1045,1068`, `apply.go:149`,
`create.go:160`, `reset.go:94`, `source_inspect.go:74`, `watch.go:198`,
`watch_check.go:70`, `config_set.go:69`, `instance_from_hook.go:362`.
Every one of them feeds `github.NewAPIClient(token)`.

**This is ambient-environment resolution, entirely separate from the
config pipeline.** The token that reaches the API client never passes
through `cfg.Env.Secrets.Values`, and conversely the `GH_TOKEN` declared
in `[env.secrets.required]` never reaches `resolveGitHubToken`. They are
two unconnected mechanisms that happen to share a name.

**Does niwa use a token to clone repos?** No. `Cloner.CloneWith`
(`internal/workspace/clone.go:42-77`) shells out to plain
`git clone [--depth N] [--branch ref] <url> <dir>` with no credential
injection and no env manipulation. `ResolveCloneURL`
(`clone.go:87-113`) produces `https://github.com/<org>/<repo>.git` or
`git@github.com:<org>/<repo>.git`. Auth, when needed, comes from git's
own credential helper (typically the one `gh auth login` installs) or
from SSH keys. The config-snapshot fallback path is the same story:
`shallowCloneToTemp` at `internal/workspace/fallback.go:135-161` runs
`git clone --depth 1` unadorned.

**Where a token does change behavior.** Two places, both HTTPS to
`api.github.com`:

- Org repo discovery: `APIClient.ListRepos`
  (`internal/github/client.go:54-104`) sets `Authorization: Bearer` only
  when `Token != ""` (line 65-67). Unauthenticated it still works for a
  public org, returning only public repos, subject to GitHub's 60
  req/hour unauthenticated limit.
- Config-snapshot tarball fetch: `internal/github/fetch.go:179-180`, same
  conditional-auth shape. Unauthenticated fetch of a public repo's
  tarball works; a private overlay's tarball needs a token (and this is
  precisely the case that silently skips at `apply.go:991-993`).

**So: does an OSS contributor cloning only public repos need `GH_TOKEN`?**
Functionally, no. Public clone works with no credential; public org
discovery and public tarball fetch work unauthenticated. The only
practical argument for a token is rate limits. What blocks them is not
the absence of a token but the *declaration* of one as required.

**The `[claude.env] promote = ["GH_TOKEN"]` twist — a second, independent
hard failure.** `resolveClaudeEnvVars`
(`internal/workspace/materialize.go:920-988`) resolves promoted keys out
of `ResolveEnvVars(ctx)` and, at lines 958-963:

```go
for _, key := range claudeEnv.Promote {
    val, found := resolvedEnv[key]
    if !found {
        return nil, nil, fmt.Errorf("claude.env: promoted key %q not found in resolved env vars", key)
    }
    envResult[key] = val
}
```

`ResolveEnvVars` draws from the `.env.example` pre-pass, `[env] files`,
and `env.vars`/`env.secrets` values — **not** from `os.Environ()`. So an
ambient `gh auth` session, or `GITHUB_TOKEN=$(gh auth token)` on the
command line, does not satisfy the promote either. Dropping `GH_TOKEN`
from `[env.secrets.required]` alone is therefore *not sufficient*: the
`promote` entry is a second hard failure downstream, at materialize time.
Whatever the corrected config looks like, it must address both.

**Empirical grounding on where `GH_TOKEN` actually comes from today.**
The private overlay
(`private/dot-niwa-overlay/.niwa/workspace-overlay.toml`) binds
`ANTHROPIC_API_KEY`, `TAVILY_API_KEY`, `BRAVE_API_KEY`,
`TELEGRAM_BOT_TOKEN` and the three `INFISICAL_*` keys to `vault://`
refs — but **not** `GH_TOKEN`. Inspecting this host's personal/global
overlay (`~/.config/niwa/global/niwa.toml`, key names only) shows it
supplies `GH_TOKEN`, `GITHUB_TOKEN`, and `ANTHROPIC_API_KEY`. That is
what makes the maintainer's workspace apply succeed. The personal
overlay is a *separate config repo registered per-user* via
`niwa config set global <org/repo>` (`docs/guides/vault-integration.md:246-249`).
In other words: the current tsukumogami workspace is only applicable by
someone who has independently set up a personal config repo that the
public base config never mentions and cannot bootstrap.

**Contradiction in the shipped docs.** `public/dot-niwa/README.md`
documents the setup as:

```bash
niwa init tsukumogami --from tsukumogami/dot-niwa
GITHUB_TOKEN=$(gh auth token) niwa create
```

That command satisfies `resolveGitHubToken()` and nothing else. It does
not satisfy `[env.secrets.required] GH_TOKEN` (wrong mechanism, and also
a different key name), and it does not satisfy the `promote` list. The
documented happy path does not work for anyone without a personal
overlay.

**Verdict:** `GH_TOKEN` is not load-bearing for clone or materialize. It
is a developer-workflow convenience (the `gh` CLI inside a Claude
session, plus API rate-limit headroom). It should not be `required`; at
most `recommended`. The `promote` entry needs a tolerant form or removal.

---

### 2. `ANTHROPIC_API_KEY` — nothing in niwa needs it, and setting it actively degrades a niwa feature

**niwa never reads it in production code.** Every non-test occurrence:

- `internal/cli/dispatch_remotecontrol.go:19-21,53-64` — reads it *to
  detect its presence*, see below.
- `internal/workspace/scaffold.go:60` — a commented-out example line in
  the scaffolded template.
- `test/functional/steps_test.go:1277-1287,1320-1321` — the
  `@claude-integration` scenario gate; the scenario returns
  `godog.ErrPending` (skip) when the key or the `claude` binary is
  absent.
- `.github/workflows/test.yml:86-96` — two CI steps, both guarded by
  `if: env.ANTHROPIC_API_KEY != ''`, that install the Claude CLI and run
  `make test-functional-claude-integration`. Absent → steps skip.

There is no code path in `init`, `create`, `apply`, `materialize`,
`clone`, or the vault stack that requires the key. It is consumed by
**Claude Code**, launched *inside* an already-materialized workspace.

**Subscription users do not have one, and niwa knows this.**
`internal/cli/dispatch_remotecontrol.go` is explicit:

```go
const apiKeyForcedWarning = "remote-control on dispatch is enabled, but ANTHROPIC_API_KEY is set, which forces API-key auth; Claude Code Remote requires a claude.ai login, so the worker will start without remote-control"
```

and `apiKeyAuthForced(env)` (`dispatch_remotecontrol.go:57-64`) returns
true on any non-empty `ANTHROPIC_API_KEY=` entry, which causes
`resolveDispatchRemoteControl` (`:37-50`) to refuse to inject the Remote
Control settings flag. So for a Claude Code subscription user —
Anthropic's dominant Claude Code auth mode — declaring
`ANTHROPIC_API_KEY` as *required* demands a credential they do not have,
and if they invent one to get past the check, it silently disables
`niwa dispatch`'s remote-control integration.

Note this key is currently vault-bound in the private overlay, so on the
maintainer's host it *is* set — meaning remote-control on dispatch is
presumably already being suppressed there. Worth flagging separately.

**Verdict:** `ANTHROPIC_API_KEY` is not load-bearing for anything niwa
does. It is a workflow key for one of two Claude Code auth modes.
`required` is wrong on both correctness and product grounds; `optional`
is the honest classification (`recommended` would emit a warning at
every apply for every subscription user).

---

### 3. `INFISICAL_TEST_PROJECT_ID` / `INFISICAL_CLIENT_ID` / `INFISICAL_CLIENT_SECRET` — maintainer-only CI test credentials, and two of the three are never read by anything in the repo

`grep -rn "INFISICAL_" --include="*.go" .` over the whole niwa tree
returns only:

- `internal/vault/infisical/integration_test.go:7,8,14,33` — the constant
  `testProjectEnvVar = "INFISICAL_TEST_PROJECT_ID"` and its doc comment.
- `internal/vault/infisical/infisical.go:13`, `subprocess.go:40,61` —
  prose comments noting the Infisical CLI reads its *own* `INFISICAL_TOKEN`
  / `~/.infisical` session.

**`INFISICAL_TEST_PROJECT_ID`** is the sole gate for eight integration
tests: `skipUnlessIntegration` (`integration_test.go:35-41`) calls
`t.Skipf` when it is unset. The tests are
`TestIntegration_ResolveKnownSecret` (`:62`),
`_ResolveMultipleSecrets` (`:90`), `_ResolveBatch` (`:114`),
`_ResolveMissingKeyReturnsErrKeyNotFound` (`:155`),
`_RedactionSurvivesRealCLI` (`:170`), `_SecretValueRedactedInError` (`:200`),
`_CachesAcrossMultipleResolves` (`:230`), `_ClosePreventsFurtherResolves` (`:251`).
Verified empirically: `go test -count=1 ./internal/vault/infisical/` with
the var unset returns `ok ... 0.424s`. **They skip cleanly.**

**`INFISICAL_CLIENT_ID` and `INFISICAL_CLIENT_SECRET` are read by nothing
in the Go tree at all.** Their only consumers are three CI steps in
`.github/workflows/test.yml:57-80`, and those read from
`${{ secrets.INFISICAL_* }}` (GitHub Actions repo secrets) — not from any
niwa-materialized `.local.env`. The steps are guarded by
`if: env.INFISICAL_CLIENT_ID != ''` (lines 58, 66), so a fork PR with no
secrets skips both the CLI install and the integration run. The workflow
uses them to mint a JWT via `infisical login --method=universal-auth`
(lines 69-74) and export it as `INFISICAL_TOKEN`.

**niwa's own machine-identity auth does not use these env vars either.**
`infisical.Authenticate` (`internal/vault/infisical/auth.go:33-55`) takes
`client_id` / `client_secret` from a `ProviderAuthEntry` map, sourced
from `provider-auth.toml` under `NiwaConfigDir()`
(`internal/workspace/providerauth.go:242-255`; credential-pool shape at
`internal/workspace/credentialpool.go:51,102,113`). Confirmed on this
host: `~/.config/niwa/provider-auth.toml` exists. So the runtime
credential path is a file in `~/.config/niwa/`, entirely unrelated to
these three env vars.

**Nothing else references them.** No hits in `test/functional/`
(`grep -rn "INFISICAL" test/functional/` returns nothing) and no
Makefile target consumes them — the Makefile has `test`, `build-test`,
`test-functional`, `test-functional-critical`,
`test-functional-claude-integration`, `test-install`, `test-live`, none
of which touch Infisical.

**Verdict:** all three are maintainer-only, and strictly for one
integration test suite that already skips cleanly by design. Two of the
three are dead weight in the workspace config — no local tooling reads
them. Declaring them `required` in a **public** base config means every
OSS contributor is hard-blocked by a credential set that exists solely so
one maintainer can run a vault test against a live project.

---

### 4. `TAVILY_API_KEY`, `BRAVE_API_KEY`, `TELEGRAM_BOT_TOKEN`

Nothing in niwa consumes any of them:
`grep -rn "TAVILY\|BRAVE\|TELEGRAM" --include="*.go" --include="*.json" --include="*.md" --include="*.sh" .`
over the niwa tree returns zero hits. A cross-repo sweep (details in the addendum) confirms the same for the
rest of the workspace: **no MCP server anywhere consumes any of them.**
The only `.mcp.json` files in the workspace are test-harness fixtures
under a private repo, and they set an unrelated variable. No hook script
and no `.claude/settings.json` reads them either.

Their real consumers:

- `TAVILY_API_KEY` and `BRAVE_API_KEY` are read by **tsuku's own Go
  code** — `public/tsuku/internal/search/factory.go:20,27` and the secret
  spec registry at `public/tsuku/internal/secrets/specs.go:28,32`.
  Failure mode is two-tier: an explicit `--search-provider=tavily|brave`
  with no key returns an error and exits non-zero; auto-selection with no
  key falls through silently to the other provider and then to
  DuckDuckGo. Nothing in CI or any hook touches them.
- `TELEGRAM_BOT_TOKEN` has **no consumer for the copy niwa
  materializes.** The Telegram plumbing in the private tools repo reads
  its token from `~/.tsuku/channels.json` (the shell wrapper) or greps it
  out of that repo's own `env/workspace.env` (the installer) — neither
  reads the `.local.env` niwa writes. Every skip path in the shell
  wrapper logs to stderr and falls through to a plain `claude` launch;
  the installer silently writes an empty token rather than failing.
  `TELEGRAM_CHAT_ID` does not appear anywhere in the workspace.

Both classifications are non-fatal in niwa: `recommended` misses emit one
stderr warning per key via `warnRecommended`
(`internal/workspace/required.go:141-158`) and apply continues; `optional`
is silent in v1 (`docs/guides/vault-integration.md:176`). These three are
the only keys in the config whose declared severity matches their actual
role, and they are the model the other five should follow — though
`TELEGRAM_BOT_TOKEN` arguably should not be distributed at all, since
nothing reads the distributed copy.

---

## Implications

**The required list can legitimately be empty.** Working through the
lifecycle: `niwa init` fetches a config snapshot (public tarball or
`git clone`, no auth needed for a public repo); overlay discovery either
succeeds or silently skips; `niwa create` / `apply` classify repos via
`ListRepos` (works unauthenticated for a public org) and clone them via
plain `git clone` (works unauthenticated for public repos); materialize
writes `CLAUDE.md`, settings, hooks, and `.local.env` files from config
values. **Not one step in that chain reads a secret.** The goal "no
secret is required to build and operate the workspace" is literally
achievable for the public-repo subset, and the correct required list for
the public base config is empty.

The honest reclassification:

| Key | Today | Should be | Why |
|---|---|---|---|
| `GH_TOKEN` | required | recommended (or drop) | Rate-limit headroom + `gh` inside sessions; clone needs nothing |
| `ANTHROPIC_API_KEY` | required | optional | Only one of two Claude Code auth modes; setting it breaks remote-control on dispatch |
| `TAVILY_API_KEY` | recommended | recommended | Correct as-is; tsuku search falls back silently |
| `BRAVE_API_KEY` | recommended | recommended | Correct as-is; same |
| `TELEGRAM_BOT_TOKEN` | optional | optional, or drop | Correct severity, but nothing reads the distributed copy |
| `INFISICAL_TEST_PROJECT_ID` | required (repos.niwa) | optional, or move out of the public base | Gates a test suite that skips cleanly |
| `INFISICAL_CLIENT_ID` | required (repos.niwa) | remove | Read by nothing in the repo; CI gets it from Actions secrets |
| `INFISICAL_CLIENT_SECRET` | required (repos.niwa) | remove | Same |

**The `INFISICAL_*` keys do not belong in a public base config.** Three
converging reasons. (a) Only maintainers can ever satisfy them, so they
are a permanent tripwire for the OSS persona the public config exists to
serve. (b) `dot-niwa` is explicitly positioned as "a reference example
for niwa workspace configuration" (workspace `CLAUDE.overlay.md`), and
teaching readers to mark test-only credentials `required` teaches the
exact anti-pattern this exploration is fixing. (c) The mechanism is
redundant: CI already sources them from GitHub Actions secrets, and
niwa's own machine-identity auth reads `~/.config/niwa/provider-auth.toml`.
If a maintainer wants them in their local `.local.env` for manual
`infisical login`, the private overlay is where the binding already
lives — the *declaration* can move there too, since a `[repos.niwa.env.secrets]`
binding in the overlay works without a matching declaration in the base.

**The config fix alone does not close the bug.** Two residues survive an
empty required list:

1. `[claude.env] promote = ["GH_TOKEN"]` still hard-errors at
   `materialize.go:960-962` when nothing supplies the key. This is a
   niwa-side gap as much as a config one: `promote` has no tolerant mode.
2. The maintainer-on-a-fresh-host persona is untouched.
   `ErrProviderUnreachable` is fatal at `resolve.go:548-555` regardless of
   required/recommended/optional classification and regardless of
   `--allow-missing-secrets`. Reclassifying keys does nothing for them.
   That persona needs the niwa-side fix.

**A third, quieter implication.** Because `checkRequiredKeys` reads only
config values and never `os.Environ()`, *no* ambient credential can ever
satisfy a required key. The only satisfying layers are inline TOML, the
team overlay, the personal overlay, and vault. That makes `*.required`
effectively "must be declared in some config layer" rather than "must be
present in the environment at apply time" — a narrower contract than the
PRD framing ("supplied via personal overlay") suggests, and one the base
config's own README contradicts.

---

## Surprises

**The documented setup command has never satisfied the config it ships
with.** `public/dot-niwa/README.md` says
`GITHUB_TOKEN=$(gh auth token) niwa create`. That sets an ambient var
that `resolveGitHubToken()` reads for API calls, and which
`checkRequiredKeys` cannot see. The base config's own quick-start is
inert with respect to its own required declaration.

**Setting `ANTHROPIC_API_KEY` is not neutral — it disables a niwa
feature.** `dispatch_remotecontrol.go:53-64` treats a non-empty key as
proof that Claude Code Remote is impossible, and skips injecting the
remote-control settings flag. Since the private overlay vault-binds this
key, the maintainer's own workspace is likely running with
remote-control-on-dispatch silently suppressed. This is orthogonal to
the OSS bug but probably worth its own issue.

**Two of the three `INFISICAL_*` keys are read by literally nothing in
the repository.** I expected them to be consumed by a Makefile target or
a local test helper. `INFISICAL_CLIENT_ID` and `INFISICAL_CLIENT_SECRET`
appear only in `.github/workflows/test.yml`, sourced from Actions
secrets. Their presence in the workspace config produces a `.local.env`
entry in the `niwa` repo that no tooling ever reads.

**`--allow-missing-secrets` is weaker than its name and its docs
suggest.** The guide (`docs/guides/vault-integration.md:576`) frames the
one caveat as "does NOT override `*.required` misses". The larger caveat
is undocumented: it does not apply to `ErrProviderUnreachable` at all
(`resolve.go:548`), which is the exact failure mode of "Infisical not
installed" and "Infisical session expired" — the two cases a user
reaching for the flag is most likely trying to route around.

**The per-repo required check is not scoped to cloned repos.**
`required.go:65` iterates `cfg.Repos` wholesale, so a
`[repos.<name>.env.secrets.required]` block fires even for a repo excluded
from the current apply. Not the cause here (niwa is in scope), but it
widens the blast radius of any per-repo required declaration.

**The `promote` entry writes a live PAT into a world-readable file.**
`[claude.env] promote = ["GH_TOKEN"]` lands the resolved token in
cleartext in the workspace-root `.claude/settings.json`, which on this
host is mode 0644 — while the `.local.env` files niwa writes are 0600.
So the one key this lead concludes should probably be dropped from
`promote` is also the one creating a credential-exposure problem. Worth
its own issue regardless of how this exploration lands.

**No MCP server in the workspace consumes any declared secret.** I
expected `TAVILY_API_KEY` / `BRAVE_API_KEY` to be feeding search MCP
servers. They are not — they are read by tsuku's own Go search-provider
factory, with silent fallback to DuckDuckGo. And nothing at all reads the
`TELEGRAM_BOT_TOKEN` that niwa distributes to ten `.local.env` files; the
Telegram plumbing sources its token from a different file entirely.

**Infisical `Factory.Open` is deliberately lazy** (`infisical.go:87-89`:
"Open is non-blocking: it does NOT invoke `infisical`"), so a missing CLI
is not caught at bundle-build time — it surfaces per-ref at the first
`Resolve`. Good for error attribution, but it means there is no early
"provider unavailable" signal a caller could downgrade wholesale today.

---

## Open Questions

1. **What should `promote` do when a key is absent?** Today it is a hard
   error (`materialize.go:960-962`). Options: skip-with-warning, a
   `promote_optional` list, or per-key `?required=false`-style syntax.
   This is a niwa-side design decision the config fix depends on. Owned
   by whichever lead covers the niwa-side remedy.

2. **Should the `INFISICAL_*` declarations move to the private overlay,
   or disappear entirely?** A `[repos.niwa.env.secrets]` binding in the
   overlay resolves fine without a base-config declaration, so moving is
   mechanically free. But if the declarations exist mainly as
   documentation of "what a maintainer needs", the private overlay is the
   right home; if they exist for no reason, deletion is cleaner. Needs a
   maintainer's intent.

3. **Is remote-control-on-dispatch currently broken for the maintainer
   because `ANTHROPIC_API_KEY` is vault-bound?** Worth a direct check
   (`niwa dispatch` with the host default on) — and if so, whether the
   right fix is to stop binding the key at all rather than reclassify it.

4. **Does anything downstream depend on `GH_TOKEN` reaching Claude Code's
   `settings.json` env block?** The `promote` entry puts it there, in
   cleartext, in a 0644 file. Nothing in the sweep reads `GH_TOKEN` from
   the environment except the `gh` CLI itself, which also works off its
   own keyring/`gh auth` state. If sessions already run with `gh`
   authenticated, `promote` is pure redundancy plus a credential-exposure
   liability, and simply removable. Not verified end-to-end.

5. **Does unauthenticated `ListRepos` actually return enough for
   tsukumogami?** The org has six public repos and `DefaultMaxRepos = 10`
   (`apply.go:200`), so it should. Unverified end-to-end because I did
   not run a live unauthenticated apply. Also unverified: how a 403
   rate-limit response is reported (`client.go:76-79` returns a generic
   "GitHub API returned status %d" — likely a poor message for the
   60/hour case).

6. **Should `recommended` be the right home for `GH_TOKEN`, given the
   warning fires on every apply?** For a subscription-only OSS
   contributor who genuinely does not need it, a per-apply stderr warning
   is noise. `optional` may be the better fit, with the rate-limit
   guidance living in the README instead.

---

## Addendum: cross-repo consumers of the search / notification keys

From a workspace-wide sweep of `.mcp.json`, `.claude/settings.json`, hook
scripts, CI workflows, Makefiles, and source across all ten repos.

**No MCP server consumes any of the four keys.** The only `.mcp.json`
files present are test-harness fixtures inside a private repo, wiring an
unrelated variable. **No hook script reads any of them** — the two hooks
the base config installs (`hooks/pre_tool_use/gate-online.sh`,
`hooks/stop/workflow-continue.sh`) never reference them, so no missing
key can block a tool call. **No Makefile** in the workspace references
them except a comment on niwa's `@claude-integration` target
(`Makefile:29`).

`TAVILY_API_KEY` — `public/tsuku/internal/search/factory.go:20`,
`public/tsuku/internal/secrets/specs.go:28`; docs at
`public/tsuku/docs/ENVIRONMENT.md:263-268,307` and
`public/tsuku/docs/designs/current/DESIGN-tavily-brave-providers.md`.
Hard error only on explicit provider selection; otherwise silent fallback.

`BRAVE_API_KEY` — `public/tsuku/internal/search/factory.go:27`,
`public/tsuku/internal/secrets/specs.go:32`;
`public/tsuku/docs/ENVIRONMENT.md:273-278,308`. Identical failure shape.

`TELEGRAM_BOT_TOKEN` — consumers live entirely in the private tools repo
(an installer that writes a channel `.env`, and a shell wrapper that
injects the token into a `claude --channels` launch). Both source the
token from elsewhere, not from the niwa-materialized `.local.env`. All
skip paths degrade gracefully to a plain `claude` launch.

`ANTHROPIC_API_KEY` outside niwa — the notable case is
`public/shirabe/.github/workflows/run-evals.yml:34,39,47`: three steps in
the weekly `run-evals` job with **no `if:` guard**. With an empty secret
they still execute and the unauthenticated `claude` CLI fails, so the job
fails rather than skipping. This is the opposite convention from niwa's
own workflows (`.github/workflows/test.yml:83,89` gate on
`if: env.ANTHROPIC_API_KEY != ''`). Separately,
`public/koto/docs/guides/custom-skill-authoring.md:518,524,527` documents
an eval script and a workflow secret requirement that no longer exist in
that repo — pure doc drift, unrelated to this lead. tsuku's own runtime
uses of the key (`internal/llm/factory.go:201`, `internal/discover/*`)
all degrade to an actionable error or an alternate path; its tests skip.

**Two security items the sweep surfaced incidentally** (out of scope for
this lead, but they should not be dropped on the floor):

1. The `[claude.env] promote = ["GH_TOKEN"]` mechanism writes a live
   GitHub PAT in cleartext into the workspace-root `.claude/settings.json`
   and every per-repo `settings.local.json`. On this host the root
   `settings.json` is mode **0644**, whereas the `.local.env` files and
   the per-repo `settings.local.json` files are 0600. That is a
   world-readable credential, and it is a direct consequence of the
   `promote` entry this lead recommends removing.
2. The private tools repo keeps live plaintext values for the Tavily,
   Brave, and Telegram keys in a checked-in-style env file, as a
   non-vault fallback layer.

---

## Summary

Not one of the eight declared secrets is read by niwa's clone or materialize path — `git clone` runs unadorned, the GitHub API client takes its token from the ambient environment via a mechanism `checkRequiredKeys` cannot even see, and the `INFISICAL_*` trio serves only an integration suite that already skips cleanly (two of the three are read by nothing in the repo at all, only by CI from Actions secrets). The required list can therefore legitimately be empty and the `INFISICAL_*` keys should leave the public base config entirely, but reclassification alone will not close the bug: `[claude.env] promote = ["GH_TOKEN"]` is a second hard failure at materialize time, and the maintainer-on-a-fresh-host persona dies earlier on `ErrProviderUnreachable`, which `--allow-missing-secrets` does not downgrade. The biggest open question is what `promote` should do when a key is absent, since the corrected config cannot be written until niwa offers a tolerant form.
