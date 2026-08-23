# How should an agent obtain credentials for niwa's gated live tests?

Repo under investigation: `public/niwa/.claude/worktrees/root-orientation`
(HEAD as checked out; read-only investigation, no changes made).

## 1. What the suite does today — the sandbox and the two live paths

`test/functional/suite_test.go` allocates one process-wide sandbox
(`processSandboxRoot`, a fresh `os.MkdirTemp`) and refuses to run if any
ancestor of it is a git repo (`checkSandboxIsOutsideAnyRepo`) — the comment
notes this guards against a change that "someday re-parents the sandbox back
under the checkout" after a real incident where a scenario committed a
developer's working tree onto `main` and pushed it (issue #249, referenced in
CI). Every scenario gets its own child sandbox with `homeDir`, `tmpDir`, and
`workspaceRoot` under it.

`steps_test.go`'s `buildEnv()` is what every ordinary `niwa` invocation runs
under: it strips `HOME`, `XDG_CONFIG_HOME`, `TMPDIR` (and `PATH` when a fake
binary needs priority) from the inherited environment and replaces them with
sandbox paths. This is deliberate isolation — it means a plain scenario
never sees the real `~/.claude` or `~/.codex`, by design.

The two agent-gated paths diverge sharply on whether that isolation is
preserved for the *live* case:

- **Claude** (`claudeIsAvailable` / `runClaudeP`, `steps_test.go`): gates on
  `claude` being on `PATH` and `ANTHROPIC_API_KEY` being set in the *host*
  environment. `runClaudeP` still starts from `s.buildEnv()` (sandboxed HOME)
  and only additively appends `ANTHROPIC_API_KEY` if present. It does **not**
  copy `~/.claude` credentials into the sandbox. So a live Claude scenario
  runs `claude -p` with a sandboxed `$HOME` and no subscription login state —
  only an API key can possibly authenticate it. If the developer/agent has a
  Claude subscription (OAuth login state in the real `~/.claude`), that state
  is invisible to the run and the scenario gets skipped for lack of
  `ANTHROPIC_API_KEY`, not run against the working login.

- **Codex** (`codexIsAvailable` / `codexLiveEnv`, `codex_agent_steps_test.go`):
  gates on `codex` on `PATH` *and* a login. But unlike Claude, it does not
  rely on an env var for the login — it explicitly reads the *developer's
  real* `~/.codex/auth.json` (or `$CODEX_HOME/auth.json`) and copies just
  that credential file, read-only-in-spirit (only ever a copy, never a
  read/write of the original), into the scenario's sandboxed `~/.codex`. The
  comment in `codexIsAvailable` is explicit about why: "the sandbox redirects
  $HOME, and that redirection is the point... niwa must never be pointed at
  the developer's real one. So the developer's own credential file is copied
  into the sandbox Codex home." `codexLiveEnv` then builds the live-process
  environment by stripping every `NIWA_*` var and `CODEX_HOME` from
  `buildEnv()`, i.e. a plain shell with sandboxed `$HOME` but the copied
  subscription-style credential in place. This is a login-token copy, not an
  API key — it works with subscription auth.

So today: Codex's live gate already answers "how does an agent get
credentials into the sandbox" for subscription-style auth — it seeds the
sandbox from whatever the real `~/.codex/auth.json` holds, whether that's an
API key or an OAuth/subscription token, because it copies the file verbatim
without caring about its contents. Claude's live gate does not have an
equivalent step; it only ever plumbs in `ANTHROPIC_API_KEY` as an env var, so
subscription-based Claude auth (the real `~/.claude` OAuth state) is never
copied in and never exercised by the suite.

## 2. Is `ANTHROPIC_API_KEY` load-bearing, or a proxy?

Reading `claudeIsAvailable` and `runClaudeP` (`test/functional/steps_test.go`
lines ~1279–1342): the check is literally `os.Getenv("ANTHROPIC_API_KEY") ==
""` → skip. It is not phrased as "can claude authenticate at all" — it is
phrased as "is this specific env var present." Given the sandboxed `$HOME`
that `runClaudeP` still uses (no copy of `~/.claude`'s OAuth credentials, no
analog to Codex's `codexCredentialName` copy), the only way `claude -p` can
authenticate inside that sandbox today is via the API key. A subscription
login on the host machine cannot satisfy this gate as written, because the
subscription's login state lives in `~/.claude` and that directory is never
read or copied anywhere in this file. This is asymmetric with Codex on
purpose or by omission — the code doesn't say which, but nothing in the repo
suggests Claude's live gate was ever extended the way Codex's was; Codex's is
newer (per the `codex_agent_steps_test.go` doc-comment referencing "the
standing spike" and design docs) and explicitly reasoned about credential
copying, while Claude's predates that reasoning and was never revisited.

Net: today `ANTHROPIC_API_KEY` is load-bearing, not a proxy. A working
subscription login would not currently make `claudeIsAvailable` pass, and
would not currently let `runClaudeP` succeed, because the credential never
reaches the sandbox.

## 3. Existing precedent: vault integration and CI's Infisical step

Two separate mechanisms exist in the repo and it's easy to conflate them:

**(a) `internal/vault/infisical/integration_test.go`** — a *unit-level*
integration test (`TestIntegration_*`), unrelated to the functional/godog
suite. It gates on `INFISICAL_TEST_PROJECT_ID` being set
(`skipUnlessIntegration`), and expects `INFISICAL_TOKEN` (a Machine Identity
access token) to already be exported by the caller — CI's
"Vault integration tests" step in `.github/workflows/test.yml` does exactly
that: it runs `infisical login --method=universal-auth --client-id=...
--client-secret=...` using `secrets.INFISICAL_CLIENT_ID` /
`secrets.INFISICAL_CLIENT_SECRET` (GitHub Actions secrets), captures the
resulting `INFISICAL_TOKEN`, and exports it before calling `go test -run
TestIntegration ./internal/vault/infisical/`. This proves the CI job holds
Infisical machine-identity credentials, but this pathway resolves generic
test secrets (`NIWA_TEST_API_KEY`, etc. — see the pre-seeded project
described in the test file header) into niwa's own vault abstraction. It has
nothing to do with authenticating `claude` or `codex` binaries, and it never
touches `~/.claude` or `~/.codex`.

**(b) niwa's production vault feature** (`docs/guides/vault-integration.md`)
— this is the mechanism that *could* plausibly supply agent credentials, and
is the real precedent worth following. A workspace can declare
`[claude.env.secrets] ANTHROPIC_API_KEY = "vault://ANTHROPIC_API_KEY"` (and
symmetrically `OPENAI_API_KEY` for a codex-default workspace — "Binding
multiple agents' keys" section, line ~223). At apply time niwa resolves the
`vault://` reference against the configured provider (Infisical CLI) and
materializes a `.local.env` file at mode `0o600` (dotenv form by default;
configurable via `env_output`). This is API-key provisioning, not
subscription-login provisioning, and it lands in a file that must be
sourced/loaded by whatever process runs `claude`/`codex` — it is not
automatically exported into a live shell's environment by niwa itself. It is
the closest thing in the codebase to "niwa's own answer to handing an agent
a credential," and it already exists, is documented, and is designed for
exactly this kind of API-key distribution, scoped per-workspace, with a
public-repo guardrail (`niwa apply` refuses when a public GitHub remote
still has plaintext in `[env.secrets]`).

Neither `docs/guides/vault-integration.md` nor
`docs/guides/machine-identity-vault-sync.md` mentions Codex's or Claude's
subscription-login files (`~/.codex/auth.json`, `~/.claude`) as something
vault distributes — vault distributes *keys* (`ANTHROPIC_API_KEY`,
`OPENAI_API_KEY`), not session/login state. `machine-identity-vault-sync.md`
is about niwa syncing *its own* per-org machine-identity credentials
(GitHub PATs, etc.) across a developer's machines via a personal vault
overlay — a different, unrelated credential class.

## 4. The complete environment-variable contract (functional suite)

| Variable | Where read | Purpose |
|---|---|---|
| `NIWA_TEST_BINARY` | `suite_test.go` | Path to the prebuilt `niwa` binary under test. `TestFeatures` skips entirely (not just gated scenarios) if unset — this is how `go test ./...` avoids invoking a missing binary outside `make test-functional`. |
| `NIWA_TEST_PATHS` | `suite_test.go` | Overrides which `.feature` file(s)/dirs godog runs; defaults to `features`. Colon(`:`)/`os.PathListSeparator`-joined. |
| `NIWA_TEST_TAGS` | `suite_test.go` | godog tag filter, e.g. `@critical` (used by `make test-functional-critical`) or `@claude-integration` (used by `make test-functional-claude-integration`). |
| `NIWA_TEST_KEEP_SANDBOX` | `suite_test.go` (TestMain and the After hook) | If set, the process sandbox is not deleted at the end of the run, and any failed scenario's sandbox path is printed to stderr — for post-mortem inspection. |
| `HOME`, `XDG_CONFIG_HOME`, `TMPDIR` | `steps_test.go` `buildEnv()` | Stripped from the inherited environment and replaced with per-scenario sandbox paths for every ordinary `niwa` invocation. This is the isolation boundary. |
| `PATH` | `steps_test.go` `buildEnv()` | Rebuilt (inherited PATH stripped and prepended with `pathPrefix`/`sharedBinDir`) only when a scenario needs a fake binary (fake `claude`, fake `codex`, fake `infisical`) to take priority. |
| `ANTHROPIC_API_KEY` | `steps_test.go` `claudeIsAvailable`/`runClaudeP` | Gate + credential for live Claude scenarios. Read from the *host* environment, appended on top of the sandboxed env. See §2 — this is the only credential path Claude has. |
| `CODEX_HOME` | `codex_agent_steps_test.go` `codexIsAvailable` | Optional override for where the developer's real Codex home lives (defaults to `$HOME/.codex` via `os.UserHomeDir()`), used only to locate the credential file to copy into the sandbox. Stripped back out again in `codexLiveEnv` before the live process actually runs, so the live `codex` binary always resolves `CODEX_HOME` from the (sandboxed) `$HOME`, not from this override. |
| `FAKE_CODEX_SESSION_ID` | `codex_agent_steps_test.go` `aFakeCodexForDispatchWithSession` | Test-only — pins the session id the fake `codex` stub in dispatch-path scenarios writes into its recorded session file. Not a live-agent credential. |
| `NIWA_GITHUB_API_URL` | `internal/github/client.go`, wired via `tarball_fake_server.go`/`steps_init_bootstrap_test.go` | Override for the GitHub API base URL, pointed at a local fake tarball server for init-bootstrap fixture scenarios. Unrelated to agent auth. |
| `NIWA_RESPONSE_FILE` | set per-scenario via `iSetEnv`/`iSetEnvToTempPath` steps, consumed by the `niwa` binary itself | Not suite-level, but the most common per-scenario override — the landing-path IPC file the shell wrapper reads. |
| `INFISICAL_TEST_PROJECT_ID`, `INFISICAL_TOKEN` | `internal/vault/infisical/integration_test.go` (a *different* test binary, not the functional suite) | Gate and auth for the vault backend's own integration tests. See §3(a). |

Every `NIWA_*` var and `CODEX_HOME` are explicitly stripped again in
`codexLiveEnv` before a live Codex invocation, so "a shell with no
environment preparation" (the doc-comment's own phrase) is what the live
binary actually sees, apart from the copied credential file on disk.

## 5. Safety hazards of a credentialed agent run, and what already guards them

Hazards, concretely:

- **Leakage into committed artifacts.** `wip/` files, PR bodies, commit
  messages, or code comments could echo an API key or a session-output
  transcript that happened to include it. Nothing in the functional suite
  scrubs `codexSessionOutput`/`claude -p` stdout for secret-shaped strings
  before it's available to be logged or asserted on — the harness trusts the
  scenario author not to print it.
- **Leakage into `wip/research/*.md` or other durable branch artifacts.**
  This is the exact class of file this very research task was told to write
  (with the instruction never to print a credential value) — the workspace's
  own `wip-hygiene` rule (project CLAUDE.md) governs cleanup but says nothing
  about *content* scanning; nothing in niwa's repo greps for secret material
  before a wip commit, only for `wip/` path references.
- **Leakage into a container image.** If an agent's sandbox/session
  persists a copied `~/.codex/auth.json` or a materialized `.local.env`
  (vault) across a container snapshot or checkpoint, that credential
  outlives the run it was scoped to.
- **Leakage into `git`.** The gitfixture_test.go comment is explicit that
  this has already happened once for a different reason (a fixture escaping
  its sandbox and committing/pushing the real checkout, tsukumogami/niwa#249)
  — the mechanism is real, not hypothetical, even though that incident
  wasn't about credentials. A credential-bearing sandbox escape is the same
  shape of failure with worse consequences.

What already guards this, today:

- The process/scenario sandbox (`processSandboxRoot`, `sandboxPath` in
  `gitfixture_test.go`) bounds every fixture `git` call and refuses to run
  outside it — this is the mechanism that would have caught #249's class of
  bug if it had existed at the time, and does now.
- `fixtureGitEnv()` sets `GIT_CEILING_DIRECTORIES` and points global/system
  git config at `/dev/null`, so a developer's real `~/.gitconfig` (which
  could carry credential helpers) can't influence fixture git calls.
- CI's `permissions: contents: read` plus `persist-credentials: false` on
  checkout — explicitly commented as a response to the sandbox-escape
  incident: "a suite that escaped its sandbox once already reached a
  checkout that had push credentials sitting in `.git/config`... a future
  escape has nothing to push with."
- CI's "Verify the suite left the checkout alone" step: snapshots `HEAD` and
  `git status --porcelain` before the functional suite runs, and diffs both
  after, `always()` (so it still runs even if the suite itself failed) —
  this is a tripwire for exactly the failure mode described above, generic
  to *any* unexpected write, not specific to credentials.
- The Codex credential copy is narrowly scoped: only the single named file
  (`codexCredentialName = "auth.json"`), only ever read from the developer's
  real home and written into the sandbox (never the reverse — the suite
  separately asserts `theDeveloperCodexCredentialFileIsUnchanged` in other
  scenarios, proving niwa's own code never mutates it), and it lives inside
  a sandbox `os.RemoveAll`'d at the end of the run (unless
  `NIWA_TEST_KEEP_SANDBOX` is set, which is explicitly a debugging escape
  hatch a developer opts into).
- The generated Codex payload itself is asserted to declare no credentials
  (`theCodexPayloadAtDeclaresNoCredentials` forbids `OPENAI_API_KEY`,
  `forced_login_method`, `api_key`, `auth.json` appearing in any file niwa
  writes into a repo) — niwa's own preparation logic is credential-blind by
  design, on top of the test suite's sandboxing.

What would need to exist for a *credentialed agent run* (not just a
human running `make test-functional-claude-integration` locally) to be
safe, beyond what's above:

- A scoped, single-purpose credential — a throwaway/rotatable API key or a
  Codex login dedicated to test runs, not a developer's primary subscription
  login or personal API key — so a leak's blast radius is bounded and the
  credential is revocable without disrupting anything else. `NIWA_TEST_KEEP_SANDBOX`
  in particular is dangerous if an agent sets it while holding a live
  credential: the sandbox (including any copied `auth.json` or resolved
  `.local.env`) survives the run on disk.
- Output redaction before anything an agent produces (PR body, commit
  message, wip artifact, its own final response) is allowed to include
  captured `stdout`/`stderr` from a live `claude -p` or `codex exec` call —
  today `s.stdout`/`s.codexSessionOutput` are raw and unscrubbed.
  This document itself follows that constraint by design (report only
  whether credentials exist, never their value); an agent's own generated
  artifacts need the same discipline enforced structurally, not just by
  instruction.
- An explicit block on the agent ever setting `NIWA_TEST_KEEP_SANDBOX`, or
  on the credential-bearing sandbox being written anywhere outside a
  guaranteed-ephemeral location the agent's own run doesn't control past its
  lifetime (e.g. not inside a git worktree that might get committed, not
  inside a container layer that gets snapshotted).
- CI's checkout-integrity tripwire generalized to whatever filesystem
  surface an agent's sandbox actually runs on (a niwa worktree/instance),
  since the CI version specifically diffs a repo checkout and an agent
  session may not be running inside CI's exact checkout shape.

## Recommendation

For the **Claude** gate: this should stay a human-initiated, API-key-only
path. Nothing in the codebase copies subscription auth into the sandbox for
Claude the way it does for Codex, so a subscription login held by an agent
cannot satisfy `claudeIsAvailable` as written — it would need new code (a
Claude analog to `codexIsAvailable`'s credential copy) to change that, and
that code doesn't exist today. Until it does, the only way to make this gate
pass is `ANTHROPIC_API_KEY`, and the safest way to hand an agent that key is
niwa's own existing vault mechanism (§3b) — scoped per-workspace, backed by
a `vault://` reference resolved from a dedicated Infisical project (mirroring
how CI already gets a machine-identity-scoped `INFISICAL_TOKEN` rather than
a personal login), materialized to a `0o600` file the agent's run sources
explicitly, and ideally pointed at a *test-scoped* key distinct from any
production `ANTHROPIC_API_KEY` a workspace might also bind.

For the **Codex** gate: it already has a working, reasoned precedent for
subscription-style credential handoff — it copies `~/.codex/auth.json`
into the sandbox and nothing else. The same pattern (copy the login-state
file, never the API key form, into a disposable sandbox, never touch the
original) is the template to extend to Claude if agent-driven live Claude
runs become a real need, rather than inventing something new.

Either way, this is not a "should stay human-initiated forever" case in the
way, say, destructive `git push --force` is — the credential path itself
(vault-resolved API key, or copied login file) is mechanically no different
whether a human or an agent triggers it. What makes it worth pausing on is
narrower: the *output-handling* discipline (never let a captured
`stdout`/session transcript or a leftover sandbox reach a durable artifact)
isn't structurally enforced anywhere in this suite today, it's only
enforced by scenario authors being careful. An agent should get these
credentials only once that redaction is enforced by code, not by
convention — because an agent, unlike a human running this locally and
watching the terminal, has no equivalent "if I see a key on screen I'll
notice and stop" safety net; its transcript is exactly what a leak would
travel through.
