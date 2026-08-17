# Lead: Should niwa export OPENAI_API_KEY at all?

Measured against codex-cli 0.147.0 (`/home/dgazineu/.tsuku/tools/codex-0.147.0/bin/codex`,
ELF x86-64, static-pie, stripped, 247 MB — no shipped source or JS bundle, so binary
evidence is `strings`-based and every behavioral claim below is backed by a live run).

All experiments used isolated homes under `/home/dgazineu/.claude/jobs/7838923c/tmp/`.
The host's `~/.codex` was read once (to learn the auth-file *shape*) and never written.
No real credential was copied out of `~/.codex`; the ChatGPT-login cases used a
**synthetic** `auth.json` with fabricated tokens. No `codex login`/`logout` was run.
Every model round-trip used deliberately invalid credentials, so **no paid API call
was made** — all requests 401'd at the edge.

## Findings

### 1. Which credential a live session actually uses (VERIFIED BY EXPERIMENT)

**The stored ChatGPT login wins. The `OPENAI_API_KEY` env var is ignored.**

The discriminator is the endpoint the session dials, which is unambiguous about which
billing path is in play: ChatGPT-subscription traffic goes to `chatgpt.com/backend-api/codex`,
metered-API traffic goes to `api.openai.com/v1`. Three runs of
`codex exec --skip-git-repo-check "hi"`, endpoints extracted from the session log:

| Case | `CODEX_HOME` state | `OPENAI_API_KEY` | Endpoint dialed | Final error |
|---|---|---|---|---|
| mixed | synthetic ChatGPT `auth.json` | set | `wss://chatgpt.com/backend-api/codex/responses` | `Your access token could not be refreshed.` |
| mixed + `forced_login_method="chatgpt"` | synthetic ChatGPT `auth.json` | set | `wss://chatgpt.com/backend-api/codex/responses` | `Your access token could not be refreshed.` |
| key only (control) | empty home, no `auth.json` | set | `wss://api.openai.com/v1/responses` | `401 Unauthorized: Missing bearer or basic authentication in header` |

The control matters: it proves the two failure signatures are categorically different
(OAuth-refresh failure vs. bearer-auth 401 at a different host), so the mixed case is
genuinely selecting the ChatGPT credential rather than merely failing early. In the
mixed case the client never contacted `api.openai.com` at all — the only hosts in the
log were `chatgpt.com/backend-api/codex/models` and
`wss://chatgpt.com/backend-api/codex/responses`.

Corroborating, `codex login status` with both signals present prints
`Logged in using ChatGPT`.

**The doctor warning is misleading about sessions.** `codex doctor` in the mixed case
emits `mixed auth signals: ChatGPT login plus API key env var; HTTP reachability uses
API-key mode`. The clause "HTTP reachability uses API-key mode" describes *doctor's own
reachability probe*, not the session's request path. Taking that warning at face value
— as the exploration's stated assumption did — would have led to the wrong conclusion.

**Consequence for the motivating use case:** an exported `OPENAI_API_KEY` does **not**
divert a session away from the ChatGPT subscription, *provided a ChatGPT login is
actually present in the effective `CODEX_HOME`*. The feared silent-billing-diversion
does not occur in that configuration. The real risk is the adjacent case, below.

### 2. The config key that pins auth mode: `forced_login_method`

`preferred_auth_method` **does not exist** in 0.147.0 —
`strings -a codex | grep -c preferred_auth_method` returns `0`. It has been replaced by
`forced_login_method` (19 occurrences), which appears in the same serde field blob as
`forced_chatgpt_workspace_id`, `managed_auth_policy`, `auth_route_config` and
`has_external_auth`.

Accepted values come straight from the deserializer's own error message:

```
$ codex -c forced_login_method=bogus login status
Error loading configuration: unknown variant `bogus`, expected `chatgpt` or `api`
in `forced_login_method`
```

So the values are `chatgpt` and `api` — note **not** `apikey`, which is also rejected.
It is honored both as a `-c` override and as a plain line in `CODEX_HOME/config.toml`
(verified: writing `forced_login_method = "chatgpt"` into the isolated
`ch_chatgpt/config.toml` and running `codex login status` returned `Logged in using
ChatGPT`). I found no documentation shipped alongside the binary — there is no
docs directory in the tsuku package, so "documented" here means only "present and
self-describing via its error message".

**Two results that disqualify it as a fix, both verified:**

- **It does not silence the warning.** With `forced_login_method = "chatgpt"` in
  `config.toml` and the key present, `codex doctor` still prints the mixed-auth-signals
  warning. Pinning does not buy a clean doctor run.
- **It does not fail closed, which is the important one.** With `forced_login_method =
  "chatgpt"`, *no* login, and `OPENAI_API_KEY` set, the session still dialed
  `https://api.openai.com/v1/responses` — the metered endpoint. `codex doctor` reported
  a green `✓ auth — auth is provided by environment`. Pinning `chatgpt` therefore does
  **not** prevent the metered fallback; it does not protect the subscription at all.

**Safety hazard — `forced_login_method = "api"` is destructive.** Running the mixed case
with `-c forced_login_method=api` printed `API key login is required, but ChatGPT is
currently being used. Logging out.` and **deleted `auth.json` from the CODEX_HOME.**
(Confirmed: `ls ch_chatgpt/auth.json` → `No such file or directory`.) The binary carries
the symmetric string for the other direction too: `ChatGPT login is required, but an API
key is currently being used. Logging out.` This is a direct hazard for the per-instance
`CODEX_HOME` layout with `auth.json` symlinked back to the shared host home: an implicit
logout there could destroy the developer's real host login. niwa must never write
`forced_login_method = "api"`, and the symlink design should be checked against
codex's logout write path. Using a synthetic auth file rather than a copy of the real
one is what kept this experiment harmless.

### 3. Baselines (VERIFIED BY EXPERIMENT)

- **API key, no ChatGPT login:** `✓ auth — auth is provided by environment`, with
  `auth env vars present: OPENAI_API_KEY`. No warning of any kind. Sessions route to
  `api.openai.com/v1` (metered). Codex did **not** create an `auth.json`.
- **ChatGPT login, no key:** `✓ auth — auth is configured`, `stored auth mode: chatgpt`,
  `stored API key: false`, `stored ChatGPT tokens: true`. Sessions route to
  `chatgpt.com/backend-api/codex` (subscription).
- **Neither:** `✗ auth — no Codex credentials were found - Run codex login or provide
  an API key through a supported auth env var.` A loud, obvious failure.

The auth file's structure (values redacted, read from the host once): a top-level
`auth_mode` string, a top-level `OPENAI_API_KEY` field (null on a ChatGPT-login host),
a `tokens` object holding `id_token`/`access_token`/`refresh_token`/`account_id`, and
`last_refresh`. Notably codex stores an API key *in the auth file*, so the env var is
one of two possible API-key sources.

**The pair of baselines is the whole argument.** Without a key, a broken subscription
login produces a loud red `✗` that stops the developer. With a key exported, the exact
same broken state produces a green `✓` and silently bills metered credits.

### 4. What niwa does with `OPENAI_API_KEY` today — the claim is CONFIRMED

`[claude.env.secrets]` has **no materializer**. Read directly from the implementation:

- `resolveClaudeEnvVars` — `internal/workspace/materialize.go:961`. Its gate at
  **line 963** is `hasEnv := len(claudeEnv.Promote) > 0 || len(claudeEnv.Vars.Values) > 0`
  — `claudeEnv.Secrets` is not consulted. The body reads only `claudeEnv.Promote`
  (step 1) and `claudeEnv.Vars.Values` (step 2, `materialize.go:1040-1050`). Scanning
  lines 956–1055 for `Secret` yields only `StrictSecrets`, `MaybeSecret` and
  `maybeSecretString` — incidental type/helper names, never `claudeEnv.Secrets`.
- By contrast `ResolveEnvVars` (`materialize.go:1307`) — the function that builds
  `.local.env` — **does** read the top-level secrets table:
  `materialize.go:1316` (`hasVars := len(envCfg.Vars.Values) > 0 || len(envCfg.Secrets.Values) > 0`)
  and the loop at `materialize.go:1386-1387` over `envCfg.Secrets.Values`, where
  `envCfg := ctx.Effective.Env`.

Every other consumer of `Claude.Env.Secrets` in the tree is merge, validation, reporting
or guardrail — never materialization:

| Site | Role |
|---|---|
| `internal/workspace/override.go:124,243,406,575` | merge/precedence |
| `internal/workspace/required.go:170,178,186` | requirement validation |
| `internal/workspace/materialize.go:1105` | unresolved-key reporting (the `tables` slice feeding `forEachDeclaredWithNoValue`) |
| `internal/workspace/env_example_prepass.go:165` | `.env.example` pre-pass |
| `internal/workspace/shadows.go:132-133` | team-only shadow check |
| `internal/cli/status_audit.go:157,169,175` | audit output |
| `internal/guardrail/githubpublic.go:219,224,230` | public-repo guardrail |

**So: which table would actually deliver an env var today?**

- `[env.vars]` / `[env.secrets]` (top level) → `ResolveEnvVars` → written to a
  `.local.env` file in the repo directory. This is the only table whose values reach a
  file a developer's shell could use. Note it is a *file*, not an export: nothing in the
  Go tree sources it into a shell (`grep` for source/export/bashrc against `.local.env`
  hits only `sources_test.go`). A developer must source it themselves.
- `[claude.env] promote` + `[claude.env.vars]` → `resolveClaudeEnvVars` → the `env`
  block of Claude Code's `settings.json` (`materialize.go:1178`, feeding the settings
  document alongside `Claude.Settings`, hooks, plugins, marketplaces).
- `[claude.env.secrets]` → **nowhere**. Parsed, merged, vault-resolved, validated,
  guardrail-checked, reported and audited, then dropped.

And a second, independent defect on top of the missing materializer: even if
`[claude.env.secrets]` were wired up, its destination is Claude Code's `settings.json`
`env` block. Codex does not read `settings.json` — it reads `CODEX_HOME/config.toml`
and the process environment. So the `[claude.*]` route could never deliver a credential
to Codex even once fixed. The scaffold points users at a table that is wrong twice over.

I found no test asserting a `[claude.env.secrets]` value reaches any output file. The
one dedicated test, `internal/config/openai_key_test.go`, is a pure `config.Parse`
round-trip: it asserts `ce.Secrets.Values["OPENAI_API_KEY"].Plain`, the `required` note,
and that the two keys coexist. It never invokes a materializer. The prior increment is
therefore config parsing, scaffold comments and guide prose — it delivers the key
nowhere, exactly as the brief claimed.

### 5. What the docs promise (a real defect)

- `internal/workspace/scaffold.go:59-63` emits, under a `# [claude.env.secrets]` heading,
  the commented example `# OPENAI_API_KEY = "vault://team/OPENAI_API_KEY"` with the note
  that the table "is agent-neutral -- bind any agent's key the same way."
- `docs/guides/vault-integration.md:221-239`, in a section titled "Binding multiple
  agents' keys (OpenAI Codex)", states a workspace "binds `OPENAI_API_KEY` exactly the
  way a Claude workspace binds `ANTHROPIC_API_KEY`" and shows a `[claude.env.secrets]`
  block containing both keys, closing with "No mechanism change is needed to bind a
  Codex key."
- `docs/designs/current/DESIGN-interactive-codex-session.md:411` asserts
  "`OPENAI_API_KEY` requires no new code"; `docs/prds/PRD-interactive-codex-session.md:179`
  states niwa "SHALL bind `OPENAI_API_KEY` into a prepared workspace".

The scaffold and the guide both instruct users to write into the one table that reaches
no output file. A developer following the guide gets a vault-resolved secret that is
validated and audited and then silently discarded, with the audit output actively
reinforcing the belief that it was delivered. **This is worth filing independently of
the export decision**, because the same guidance misleads for `ANTHROPIC_API_KEY` —
the guide's Anthropic example sits in the identical non-delivering table.

## Implications

**Recommendation: niwa should not export `OPENAI_API_KEY` into a workspace instance by
default, and should not implement the `[claude.env.secrets]` → Codex binding the docs
currently promise.**

The evidence makes this an unusually clean call, because exporting the key has no upside
in either state of the world:

- **When the ChatGPT login is present and working** — the intended configuration — the
  key is *inert*. Verified: the session dials `chatgpt.com/backend-api/codex` and never
  touches `api.openai.com`. Exporting it changes nothing except adding a permanent
  warning to every `codex doctor` run.
- **When the ChatGPT login is missing or broken** — a stale symlink, an unmigrated
  `CODEX_HOME`, an expired login — the key becomes the *silent* fallback. Verified: the
  session dials `api.openai.com/v1` and doctor reports a green `✓ auth`. The developer
  gets metered billing with no warning at all.

The failure mode I am protecting against is therefore precise: **an exported key converts
the loud failure into a silent paid one, exactly when the subscription wiring is broken.**
Without the key, a broken login yields `✗ auth — no Codex credentials were found` and the
developer fixes the symlink. With the key, the same breakage produces a working session
that quietly bills metered credits — which is the outcome the feature exists to prevent,
arriving through a different door than the one anyone was watching.

Since the per-instance `CODEX_HOME` design deliberately symlinks `auth.json` back to a
shared host home, a broken symlink is not a hypothetical: it is the single most likely
way this layout fails, and it is the exact condition under which the exported key does
damage. Not exporting means that failure is loud.

**On the alternatives:**

- *Export it but pin the auth mode in `config.toml`* — **rejected, and disqualified by
  experiment, not by preference.** `forced_login_method = "chatgpt"` does not fail
  closed: with no login and a key set, the session still dialed the metered endpoint.
  It also fails to silence the doctor warning. It buys nothing the default does not
  already give. And its sibling value `api` is actively destructive (deletes
  `auth.json`), so the key is a hazard to have in reach of a config generator at all.
- *Export only when the workspace explicitly declares it* — **acceptable as an opt-in**,
  and the right home for the genuine metered-API use case (a developer with no
  subscription, or CI). If niwa supports it, the value must travel the table that
  actually works — top-level `[env.secrets]` → `.local.env` — not `[claude.env.secrets]`,
  which reaches nothing and whose destination Codex does not read even when fixed. It
  should be off unless declared, and the guide should state plainly that declaring it
  makes a broken subscription login degrade silently into metered billing.
- *Leave it entirely to the developer's own environment* — effectively the same as not
  exporting, and it is what already happens. A developer who exports the key in their
  own shell gets the ChatGPT-wins behavior anyway, so niwa staying out of it costs them
  nothing.

**Concrete actions:**

1. Do not export `OPENAI_API_KEY`; do not add a `[claude.env.secrets]` materializer for
   the purpose of delivering it to Codex.
2. Fix the scaffold (`scaffold.go:59-63`) and the guide
   (`docs/guides/vault-integration.md:221-239`), which currently promise a binding no
   code implements. This is a defect on its own terms — the same wrong table is shown
   for `ANTHROPIC_API_KEY`.
3. Never emit `forced_login_method = "api"` into a generated `config.toml`, and audit
   the `auth.json` symlink design against codex's logout write path (relevant to the
   `CODEX_HOME` delivery lead).
4. If a metered-API escape hatch is wanted later, make it explicit opt-in through
   `[env.secrets]`, and document the silent-degradation consequence.

## Surprises

1. **The doctor warning is wrong about sessions, and the brief's assumption inverted the
   real risk.** "HTTP reachability uses API-key mode" describes doctor's own probe. The
   session uses the ChatGPT login. Had I trusted the warning I would have reported that
   exporting the key diverts billing — the opposite of what happens.
2. **`preferred_auth_method` does not exist in 0.147.0** (zero occurrences). Any guidance
   or prior art referencing it is stale; the key is `forced_login_method`, values
   `chatgpt` | `api`.
3. **`forced_login_method = "api"` silently deleted `auth.json`.** A config key that
   looks purely declarative performs a destructive credential write as a side effect.
   With the planned symlinked-`auth.json` layout this could destroy a real host login.
4. **Pinning `chatgpt` does not fail closed.** I expected the pin to refuse to run
   without a ChatGPT login; it happily used the metered key instead. This is what
   removed the "export it but pin it" option.
5. **The key-only state is completely silent** — a green `✓` and no warning. The
   configuration that costs money is the one Codex is quietest about.
6. `[claude.env.secrets]` is wrong on two independent axes, not one: no materializer,
   *and* a destination (`settings.json`) that Codex never reads.

## Open Questions

- **Real-credential confirmation.** Every ChatGPT-login case used a synthetic `auth.json`
  with fabricated tokens, so I observed *credential selection and endpoint routing*, not
  a completed billed turn. The routing evidence is strong — the mixed case never
  contacted `api.openai.com` — but a fully authoritative answer would need one real
  subscription turn with the key set, then reading the account's usage records. That
  costs a live session and access to billing history; I judged the endpoint evidence
  sufficient and the spend unjustified.
- **Symlink behavior under logout.** I verified codex *deletes* a regular-file
  `auth.json`. Whether it unlinks the symlink (harmless to the host) or truncates through
  it (destroys the host login) is untested — testing needs a symlink pointing at a
  disposable copy, deliberately out of scope here under the no-writes-to-`~/.codex` rule.
  This belongs to the `CODEX_HOME` delivery lead and I have flagged it as a hazard.
- **`managed_auth_policy` / `auth_route_config` / `has_external_auth`.** Present in the
  binary's config blob, unexercised. One may offer a non-destructive way to express
  "subscription only" that `forced_login_method` does not. Determining this needs either
  upstream docs or systematic probing of the deserializer's error messages.
- **Precedence against a *stored* API key.** The auth file has its own `OPENAI_API_KEY`
  field. I tested env-var vs. ChatGPT login; stored-key vs. ChatGPT login is untested,
  and only matters if niwa ever writes an auth file, which the recommendation avoids.
- **Version durability.** All of this is 0.147.0. `preferred_auth_method` → `forced_login_method`
  shows the auth surface churns across releases, so a niwa behavior depending on this
  precedence would need a regression check pinned to a codex version.

## Summary

Verified by experiment that a live Codex session with both a ChatGPT login and `OPENAI_API_KEY` present uses the ChatGPT login — it dials `chatgpt.com/backend-api/codex` and never contacts `api.openai.com` — so the doctor warning's "HTTP reachability uses API-key mode" describes only doctor's own probe, and an exported key does not divert billing away from the subscription; meanwhile niwa's `[claude.env.secrets]` table is confirmed to have no materializer (`resolveClaudeEnvVars`, `internal/workspace/materialize.go:961-963`), so the scaffold and vault guide promise a binding no code implements.
The recommendation is still to not export the key, but for the inverted reason: with a working login the key is inert and buys nothing, while with a broken login it silently becomes the metered fallback behind a green `✓ auth`, converting the loud "no credentials found" failure into a quiet paid one — and `forced_login_method="chatgpt"` does not prevent this (verified: it still dialed the metered endpoint), while its sibling value `api` deletes `auth.json` outright.
The biggest open question is whether codex's logout write path follows a symlinked `auth.json` through to the host's real login, which would make the planned per-instance `CODEX_HOME` layout capable of destroying the very subscription credential the feature depends on.
