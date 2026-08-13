# Lead: Do three incidental findings from round 1 hold up end to end?

Round 2, `oss-no-infisical`. Visibility: public.

Paths are relative to the niwa working copy
`/Users/danielgazineu/dev/niwaw/tsuku/tsuku+tsuku_oss_no_infisical-26c0f110/public/niwa/.claude/worktrees/oss-no-infisical/`
unless prefixed with `public/`, `private/`, or given absolutely (those are the
workspace root `/Users/danielgazineu/dev/niwaw/tsuku/tsuku+tsuku_oss_no_infisical-26c0f110/`,
which is an **instance**, and its parent `/Users/danielgazineu/dev/niwaw/tsuku/`,
which is the **workspace root** — the distinction turns out to matter in item 3).

Two of the three do not survive. One does, and it dragged a fourth, more
serious finding out with it.

---

## Findings

### 1. Remote-control suppression on a non-empty `ANTHROPIC_API_KEY`

**Is the logic really "key present implies remote impossible"? Yes, literally —
but with two guards round 1 omitted.**

`resolveDispatchRemoteControl` (`internal/cli/dispatch_remotecontrol.go:37-50`)
reaches the API-key branch only after two earlier returns:

```go
if global.RemoteControlOnDispatch == nil || !*global.RemoteControlOnDispatch {
    return false, ""                       // :38-41  host default off or unset
}
if inst != nil && inst.RemoteControlAtStartup != nil {
    return false, ""                       // :42-45  downstream decided
}
if apiKeyAuthForced(env) {
    return false, apiKeyForcedWarning      // :46-48
}
return true, ""
```

`apiKeyAuthForced` (`:56-64`) scans the `KEY=VALUE` slice for a non-empty
`ANTHROPIC_API_KEY=` prefix. So the mechanism description in round 1 is
accurate as far as it goes.

**"Silently" is wrong.** The branch returns a warning string, and
`dispatch.go:376-379` prints it to stderr on every affected dispatch:

```
niwa dispatch: remote-control on dispatch is enabled, but ANTHROPIC_API_KEY is set,
which forces API-key auth; Claude Code Remote requires a claude.ai login, so the
worker will start without remote-control
```

This is not an accident of implementation. It is specified in
`docs/designs/current/DESIGN-remote-control-by-default.md:19-21` and `:172-193`
(the pseudocode there is a line-for-line match for the shipped resolver),
required by `docs/prds/PRD-remote-control-by-default.md:109-114` (surface a
clear single-line reason, still launch), and documented for users in
`docs/guides/remote-control-on-dispatch.md:56-67`, which quotes the exact
warning string.

**Test coverage asserting the current behaviour: extensive.**
- `internal/cli/dispatch_remotecontrol_test.go:41` — case `host-true/unset/api-key`
  asserts `inject == false` and a non-empty warning.
- `:75-93` `TestApiKeyAuthForced` — table covering absent, present,
  present-but-empty, and the `ANTHROPIC_API_KEY_EXTRA` prefix collision.
- `internal/cli/dispatch_wiring_remotecontrol_test.go:132-152`
  `TestDispatch_RemoteControl_APIKey_WarnsAndSkips` — end-to-end through
  `runDispatchCmd`, asserting both that `--settings` is absent from the argv
  and that the **exact** warning string reaches stderr.

**Is the maintainer's own workspace affected? No — it fails on two independent
grounds, either of which is sufficient.**

1. The host has no `remote_control_on_dispatch` preference at all.
   `~/.config/niwa/config.toml` contains, under `[global]`, only `clone_workers`
   and `watch_max_staged` (key names extracted; no values read). With
   `RemoteControlOnDispatch == nil` the resolver returns at `:38-41` and never
   evaluates the API key. Remote-control-on-dispatch is not enabled here, so
   there is nothing to suppress.
2. `ANTHROPIC_API_KEY` is not in the process environment. Observed directly:
   listing env **names** in this session matched only `GH_TOKEN` out of
   `{ANTHROPIC_API_KEY, GH_TOKEN, GITHUB_TOKEN, TAVILY_API_KEY, BRAVE_API_KEY}`.
   The vault-bound `ANTHROPIC_API_KEY` is materialized into per-repo
   `.local.env` files only — it is **not** in `[claude.env] promote`, so it
   never reaches a settings `env` block, and nothing sources `.local.env` into a
   shell. Round 1 inferred "the private overlay vault-binds this key, therefore
   the maintainer's env has it". The binding is real; the inference to
   `os.Environ()` does not hold.

**Is the assumption itself correct?** Partly outside this repo's reach. That an
`ANTHROPIC_API_KEY` forces Claude Code into API-key auth, and that Claude Code
Remote requires a claude.ai login, is asserted by niwa's own design doc and
guide but is a claim about Claude Code's auth precedence, not about niwa. I
found nothing in the repo that verifies it empirically —
`docs/spikes/SPIKE-remote-control-by-default.md` does not mention
`ANTHROPIC_API_KEY` at all, so the spike that validated the settings-key
mechanism did not validate this gate. Round 1's suggestion that it was "a proxy
for something else" has no support: `DESIGN-remote-control-by-default.md:234-235`
describes it plainly as a "definitive local ineligibility signal", and it is
deliberately the *only* eligibility check niwa performs — `:66-67` of the guide
says missing scopes, subscription, and org policy are left to Claude Code to
report at connect time. It is a conservative gate on the one signal niwa can
see locally, not a mistaken proxy.

**One narrow residual is real, and is not what round 1 reported.** The check
reads `os.Environ()` of the `niwa dispatch` process
(`dispatch.go:376`, with a comment at `:372-375` explaining the deliberate
coupling to `realDispatchLaunch`'s `cmd.Env = os.Environ()`). It does not
consider the `env` block of the settings.json the worker will materialize. A
workspace that put `ANTHROPIC_API_KEY` in `[claude.env]` (promote or inline)
would get remote-control injected, then have Claude Code read the key from
settings and fall back to API-key auth — the failure this warning exists to
announce, arriving with no warning. Today no workspace in this org does that,
so it is latent, and it is a much smaller issue than the one filed.

**Verdict: REFUTED.** The mechanism is exactly as described, but it is
documented, warned, tested, and inactive on the host round 1 claimed it was
degrading. Do not file the issue as written. At most, file the settings-env
blind spot in `apiKeyAuthForced` as a small hardening item.

---

### 2. Unauthenticated repo listing, and the quality of a 403

**Unauthenticated discovery is sufficient — verified live.** A read-only,
credential-free GET of
`https://api.github.com/orgs/tsukumogami/repos?per_page=100&page=1` (the exact
URL `client.go:61` builds) returned six repositories — `tsuku`, `koto`,
`.github`, `shirabe`, `niwa`, `dot-niwa` — all with `private: false`. Six is
under `DefaultMaxRepos = 10` (`internal/workspace/apply.go:200`), and the guard
is `len(repos) > maxRepos` (`:2360-2366`), so discovery passes.

The path is genuinely exercised: `public/dot-niwa/.niwa/workspace.toml:5-6`
declares `[[sources]] org = "tsukumogami"` with no `repos` list and no
`max_repos`, and `discoverRepos` short-circuits the API only when
`len(source.Repos) > 0` (`apply.go:2337-2347`). Discovery runs at
`apply.go:1100`, well before `checkRequiredKeys` at `:1325`, so an
unauthenticated contributor reaches the API first and gets past it. Round 1's
open question is closed affirmatively.

**The 403 message is as poor as suspected, and worse in context.**
`client.go:77-80` is the only non-200 handling in `ListRepos`:

```go
if resp.StatusCode != http.StatusOK {
    resp.Body.Close()
    return nil, fmt.Errorf("GitHub API returned status %d for org %q", resp.StatusCode, org)
}
```

`discoverAllRepos` then wraps it (`apply.go:2315`), so the contributor sees:

```
discovering repos for org "tsukumogami": GitHub API returned status 403 for org "tsukumogami"
```

The org name appears twice; the words "rate limit" and "authenticate" appear
nowhere; the `Retry-After` and `x-ratelimit-*` headers GitHub sends are read
and discarded along with the response; and no retry or backoff wraps the call.
An unauthenticated user is on the 60 req/hour primary limit, which is easy to
exhaust from a shared IP, and the message gives them nothing to act on.

**This is an outlier within its own package, not a house style.** Three sibling
call sites all do better:
- `GetRepo` (`client.go:149-155`) returns a typed `*StatusError` preserving the
  status code, which `ClassifyVisibilityCause` (`:184`) maps 401/403 →
  `"authentication"`.
- `HeadCommit` (`fetch.go:67-72`) and `FetchTarball` (`fetch.go:149-155`) both
  branch explicitly on 401/403 and emit `github: ... returned %d (verify
  GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo
  scope)`.

`ListRepos` alone returns an untyped error with no branch. The test file mirrors
the gap: `client_test.go:67-129` covers 404, 401, and 500 for `GetRepo`, and
`:208-222` covers the 401/403/404/500 classification table — but there is **no**
non-200 test for `ListRepos` anywhere.

Note also that the `fetch.go` hint text is itself wrong-footed for this persona:
"verify GH_TOKEN scopes" is unhelpful advice for someone who deliberately has no
token. Whatever wording `ListRepos` gains should distinguish "your token lacks
scope" from "you have no token and hit the anonymous limit".

**Bonus, and larger than the item it fell out of: the org sits exactly on the
`max_repos` boundary.** Authenticated, `orgs/tsukumogami/repos` returns **10**
repositories (6 public + 4 private; counts only, no names read). The guard is
`len(repos) > maxRepos` with `DefaultMaxRepos = 10`, so 10 passes by one. The
eleventh repository added to the org will hard-fail `niwa apply` for every
authenticated maintainer with:

```
org "tsukumogami" has 11 repos, which exceeds the max_repos threshold of 10;
set max_repos to a higher value in [[sources]] or provide an explicit repos list
```

The public base config sets no `max_repos`. The error is at least actionable,
but it is a scheduled outage triggered by an unrelated action (creating a repo),
and it lands on maintainers rather than contributors — the reverse of the
persona split this exploration has been tracking.

**Verdict: CONFIRMED**, both halves, plus one new confirmed finding.

*Issue-ready description (message quality):* `github.APIClient.ListRepos`
collapses every non-200 response into `GitHub API returned status %d for org
%q`, an untyped error with no status classification, which
`discoverAllRepos` then re-prefixes with the same org name. For the
no-token contributor the base config is designed to serve — unauthenticated
discovery of a public org, verified working against `tsukumogami` — the most
likely failure is the 60 req/hour anonymous primary rate limit, and the
resulting message names neither the rate limit, nor the `Retry-After` header the
response carried, nor the fact that authenticating raises the ceiling. Every
sibling in the same package already does better: `GetRepo` returns a typed
`*StatusError` (`client.go:149-155`) that `ClassifyVisibilityCause` maps 401/403
to "authentication", and `HeadCommit`/`FetchTarball` (`fetch.go:67-72`,
`:149-155`) both branch on 401/403 with an explicit hint. `ListRepos` should
return `*StatusError` too, branch 403/429 with rate-limit-specific wording that
distinguishes "no token, anonymous limit" from "token lacks scope", and gain the
non-200 test coverage `client_test.go` already has for `GetRepo` and lacks
entirely for `ListRepos`.

*Issue-ready description (max_repos boundary):* The `tsukumogami` org currently
holds exactly 10 repositories and `DefaultMaxRepos` is 10
(`internal/workspace/apply.go:200`), with the threshold checked as
`len(repos) > maxRepos` (`:2360`). The public base config
(`dot-niwa/.niwa/workspace.toml`) declares `[[sources]] org = "tsukumogami"` with
neither an explicit `repos` list nor a `max_repos` override, so authenticated
discovery returns all 10 and passes by a single repository. Creating an
eleventh repo in the org — an action with no apparent connection to niwa — will
break `niwa apply`, `create`, `dispatch`, and the SessionStart hook for every
maintainer until someone edits the config. Either set `max_repos` explicitly in
the base config with headroom, or pin an explicit `repos` list (which also
removes the API call from the critical path entirely).

---

### 3. `GH_TOKEN` promotion redundancy

Round 1's conclusion was that the `promote` entry is "pure redundancy plus a
credential-at-rest liability". Both clauses fail.

**Claim: "nothing reads `GH_TOKEN` from the environment except the `gh` CLI
itself." False.** niwa reads it directly: `resolveGitHubToken`
(`internal/cli/token.go:12-24`) checks `GITHUB_TOKEN`, then `GH_TOKEN`, then
falls back to `gh auth token`. Round 1 documented this function accurately in
its own section 1 and then contradicted it in its open question.

Beyond that, a cross-repo sweep for `GH_TOKEN` over `.sh`, `.rs`, `.py`, `.yml`,
`.json`, `.toml`, `.md`, and Go across all ten repos finds no other runtime
reader. The CI helper scripts in `public/tsuku/.github/scripts/` mention it only
in a header comment ("optional but recommended") and never dereference `$GH_TOKEN` —
they invoke `gh`, which reads it. The shirabe Rust hits are test fixtures and a
parser comment about skipping a leading `VAR=` assignment. So the consumer set
is: the `gh` CLI, and niwa itself.

**Claim: sessions don't need it in `settings.json`. False on this host — the
promote is what actually delivers it.** Three observations together:
- No shell profile exports it: `grep -l GH_TOKEN` over `~/.zshrc`, `~/.zshenv`,
  `~/.zprofile`, `~/.bashrc`, `~/.bash_profile`, `~/.profile` returns nothing.
- This session's `GH_TOKEN` is byte-identical to the promoted value: SHA-256
  prefix `6c004196a735` for both `os.environ['GH_TOKEN']` and
  `settings.local.json`'s `env.GH_TOKEN` (hashes compared; no value printed).
- The control case confirms the delivery mechanism. `ANTHROPIC_API_KEY` is
  materialized into `.local.env` but is **not** promoted, and it is absent from
  the session environment — because `.local.env` is never sourced. The settings
  `env` block is the only thing that reaches a session.

**Claim: it is a credential-at-rest liability. Substantially overstated, and
mis-attributed.**

- **Mode is 0600 by code, everywhere.** `secretFileMode os.FileMode = 0o600`
  (`internal/workspace/materialize.go:27`) is used unconditionally by
  `SettingsMaterializer.Materialize` (`:1071`, per-repo `settings.local.json`),
  `InstallWorkspaceRootSettings` (`internal/workspace/workspace_context.go:360`,
  instance-root `settings.json`), and `writeRootSettings`
  (`internal/workspace/root_materializer.go:288`, workspace-root
  `settings.json`). The doc comment at `:25-26` states this explicitly fixes "a
  pre-existing 0o644 bug where env and settings files were world-readable". So
  the 0644 round 1 observed is a historical artifact of a file written by an
  older niwa, not current behaviour. Confirmed on disk: every settings file and
  `.local.env` I stat'd is `-rw-------`. **Do not file the 0644 claim.**
- **The workspace-root attribution is wrong.** `writeRootSettings`
  (`root_materializer.go:238-250`) does not pass `ResolvedEnvVars` to
  `buildSettingsDoc` at all — the true workspace-root settings file carries no
  `env` block. Verified: `/Users/danielgazineu/dev/niwaw/tsuku/.claude/settings.json`
  contains zero occurrences of `"env"` and zero of `GH_TOKEN`. The file round 1
  inspected was the **instance**-root `.claude/settings.json`, written by
  `InstallWorkspaceRootSettings`, which *does* pass `ResolvedEnvVars`
  (`workspace_context.go:337`) and does carry the token. Two different files,
  two different functions, confusingly similar names.
- **The files are git-excluded.** `.git/info/exclude` in each managed repo
  carries a niwa-managed block containing `*.local*` and `.niwa/`, so
  `settings.local.json` and `.local.env` cannot be accidentally committed.
- What *is* true: the live token is a fine-grained PAT (`github_pat_` prefix,
  confirmed by pattern match without printing the value) and it exists in
  cleartext in 10 files on this host — nine per-repo `settings.local.json` plus
  the instance-root `settings.json`. Ten cleartext copies is a real
  duplication-of-blast-radius point. It is not a world-readable-credential point.

**What would removing the `promote` entry actually break? Little — but it would
widen privilege, not narrow it.** `gh auth status` on this host reports two
accounts: the active one sourced from `GH_TOKEN`, and an inactive keyring
credential from `gh auth login` carrying classic scopes
`admin:public_key, gist, read:org, repo`. With `GH_TOKEN` unset, `gh` falls back
to the keyring credential and keeps working; niwa's `resolveGitHubToken` reaches
its `gh auth token` fallback and keeps working too. But the promoted token is
fine-grained while the keyring token is a classic full-`repo` PAT, so removing
the promote hands sessions the *broader* credential. A sibling private repo's
installer implements the same settings-`env` injection and labels it "scoped
GitHub access" in its own output, which confirms that least-privilege scoping —
not convenience — is the intent behind putting a token there.

The genuine cost of removal lands on a host with neither a keyring login nor an
exported token: `gh` inside Claude sessions would go anonymous (public-only,
60 req/hour), and `niwa` invoked from such a session with it.

**Verdict: REFUTED.** `promote` is not redundant (niwa reads `GH_TOKEN`; the
promoted value is the actual delivery path into sessions), and the liability is
overstated (0600 by code, git-excluded, and the promoted token is narrower than
the fallback it would defer to). The one residual worth carrying forward is
already round 1's Open Question 1 and belongs to the core exploration, not a
separate issue: `promote` hard-errors when the key is absent
(`internal/workspace/materialize.go:958-963`), which is what blocks the OSS
contributor. That is a tolerance problem, not a security problem.

---

## Implications

The core exploration's remedy is untouched by all three verdicts. Item 3's only
surviving thread — `promote` needs a tolerant mode — was already the
exploration's Open Question 1, so nothing new lands on the remedy's scope.

Two of the three "incidental findings" were single-agent inferences from a
correct code reading to an incorrect runtime conclusion, and both inferences ran
the same way: from "the config declares X" to "X is therefore present in the
process environment". That is exactly the confusion the core exploration
identified in `checkRequiredKeys` — config values and ambient environment are
separate universes in niwa — reappearing in the research about it. Worth
naming, because the same trap will catch the next reader.

What did survive is the one item nobody framed as a security or product
question: `ListRepos` error quality, plus a `max_repos` boundary the org has
already reached. Both are small, both are unambiguous, both belong in the public
niwa repo, and neither depends on how the core question resolves.

## Surprises

**The org is at exactly 10 of 10 repos.** This was not on anyone's list. It
came out of double-checking the "six public repos is under the threshold" claim
by also asking what an authenticated client sees. The public-facing conclusion
was fine; the maintainer-facing one is a scheduled failure.

**The `.local.env` files are load-bearing for nothing observable.**
`ANTHROPIC_API_KEY` is materialized to `.local.env` and is provably not in the
session environment. Combined with round 1's finding that nothing reads the
distributed `TELEGRAM_BOT_TOKEN` either, the picture is that `.local.env` is a
write-only artifact for most declared keys — niwa spends its resolve budget
producing files nothing consumes. The keys that *do* reach a session reach it
via `[claude.env] promote`, and there is exactly one of those.

**The two settings files with near-identical names diverge on exactly the thing
that matters.** `writeRootSettings` (workspace root) omits `ResolvedEnvVars`;
`InstallWorkspaceRootSettings` (instance root) includes it. A function named
`InstallWorkspaceRootSettings` that writes the *instance* root is how round 1
ended up attributing a credential to the wrong file.

**The remote-control gate is better-engineered than almost anything else this
exploration has touched.** PRD requirement, design doc with matching
pseudocode, user guide quoting the exact string, a pure-function truth table,
and an end-to-end wiring test asserting the stderr text. It was reported as a
silent bug.

## Open Questions

1. **Does an `ANTHROPIC_API_KEY` genuinely preclude Claude Code Remote in every
   case?** Unsettleable from this repo — it is a claim about Claude Code's auth
   precedence, and the spike that validated the settings-key mechanism does not
   cover it. Settling it needs a live dispatch on a host with both a claude.ai
   login and an API key set. Low stakes: the current behaviour is conservative
   and warns, so a wrong assumption costs a feature, not correctness.

2. **Should the `apiKeyAuthForced` check also inspect the worker's materialized
   settings `env` block?** Today it reads only `os.Environ()`
   (`dispatch.go:376`). No workspace in this org promotes `ANTHROPIC_API_KEY`,
   so the gap is latent. Whether it is worth closing depends on whether
   promoting that key is a shape niwa wants to support at all.

3. **What is the right `max_repos` posture for the public base config?** An
   explicit generous value, an explicit `repos` list (which also removes the API
   call, the rate limit, and the 403 message from the critical path), or raising
   `DefaultMaxRepos`. The explicit-list option is attractive precisely because
   it makes item 2's message quality moot for this workspace — but it removes
   the auto-discovery the config exists to demonstrate.

4. **Should the promoted `GH_TOKEN` become optional rather than removed?** The
   scoping argument says keep it; the OSS-contributor argument says it must not
   be fatal when absent. Those are compatible if `promote` gains tolerance —
   which is the core exploration's open question, so this resolves there.

## Summary

Item 1 is REFUTED: the API-key gate is real and works as described, but it
prints an explicit stderr warning (`dispatch.go:376-379`), is specified in the
PRD, design doc, and user guide, is covered by three tests including an
end-to-end stderr assertion, and is inactive on the maintainer's host on two
independent grounds — `remote_control_on_dispatch` is not set at all, and
`ANTHROPIC_API_KEY` is not in the process environment because the vault-bound
key lands only in unsourced `.local.env` files. Item 2 is CONFIRMED end to end —
a live unauthenticated call returned exactly the six public `tsukumogami` repos,
comfortably under `DefaultMaxRepos = 10`, while a 403 collapses to
`GitHub API returned status 403 for org "tsukumogami"` with no rate-limit
wording, no typed error, and no test coverage, in a package where `GetRepo` and
the tarball fetchers all do better — and it surfaced a sharper finding: the org
holds exactly 10 repos against a threshold of 10, so the next repo created
breaks apply for every maintainer. Item 3 is REFUTED: niwa itself reads
`GH_TOKEN` (`internal/cli/token.go:16`), the promoted value is the actual
delivery path into sessions (no shell profile exports it, yet the session value
hash-matches `settings.local.json`), every settings file is 0600 by code with
the 0644 explicitly fixed at `materialize.go:25-27`, the true workspace-root
settings carries no `env` block at all, and the promoted fine-grained PAT is
*narrower* than the classic full-`repo` keyring token removal would fall back
to — leaving only the already-known `promote` intolerance
(`materialize.go:958-963`) as a live concern. The biggest open question is
whether an `ANTHROPIC_API_KEY` truly precludes Claude Code Remote, which this
repo cannot settle and which no spike ever tested.
