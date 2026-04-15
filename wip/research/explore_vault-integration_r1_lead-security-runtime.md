# Lead: Security and runtime secret handling

## Findings

### Current materialization pipeline

Reviewed `internal/workspace/materialize.go`, `apply.go`, and `status.go`. Key
observations relevant to future vault integration:

1. **Secrets today travel as plaintext end-to-end.** `EnvMaterializer`
   (`materialize.go:457`) reads env files from `ConfigDir` (the cloned config
   repo working tree), merges inline `[env].vars`, and writes a plaintext
   `.local.env` file to `{repoDir}/.local.env` with permission `0o644`
   (world-readable). `parseEnvFile` (`materialize.go:492`) does literal
   KEY=VALUE parsing with no redaction hook.

2. **Settings secrets are written to disk as JSON.** `SettingsMaterializer`
   (`materialize.go:333`) resolves Claude env vars via `resolveClaudeEnvVars`
   and serializes them into `.claude/settings.local.json`, also `0o644`. Both
   the env-materialized `.local.env` and `settings.local.json` end up on disk
   readable by any user on the machine.

3. **No subprocess env injection.** niwa does not currently exec Claude Code
   or git-with-secrets itself. `git clone` is handled by `Cloner`
   (`apply.go:241`) and relies on the user's ambient git credentials (SSH
   agent, osxkeychain, GCM). Secrets therefore don't cross a niwa→subprocess
   boundary yet — they're materialized *for* a later tool the user launches.

4. **Every written file is hashed and tracked.** `runPipeline` (`apply.go:438`)
   computes SHA-256 for every written path and stores `ManagedFile{Path, Hash,
   Generated}` in the instance state. `status.go` walks those files and
   reports `ok | drifted | removed`. This hash is computed over raw file
   content — including any plaintext secret bytes. The state.json file thus
   carries a content-addressed fingerprint of every secret-bearing file.

5. **Drift detection reads file content.** `CheckDrift` (referenced from
   `apply.go:121` and `status.go:65`) re-hashes the file on disk and compares.
   When drift is detected, the current Apply path emits a warning to stderr
   naming the path (`apply.go:127`) but does not print file contents. This is
   good — but there is nothing in the type system enforcing that future
   status output can't print the raw diff.

6. **Cleanup deletes managed files unconditionally.** `cleanRemovedFiles`
   (`apply.go:462`) removes any managed file that disappears from the
   pipeline output. For secret files this is correct behavior (no orphaned
   secret bytes after config change) but also means a partially-failed
   resolution could wipe a still-valid cached secret file.

7. **Config dir is the trust root.** Every materializer calls
   `checkContainment` to reject sources escaping `ConfigDir`. Secrets today
   live in that config dir's plaintext env files. Moving them to a vault
   shifts the trust root out of the cloned config repo and into the vault
   provider — which is the core goal of this PRD.

8. **Generated files get a `.local` infix.** `localRename`
   (`materialize.go:521`) inserts `.local` before the extension so files
   written into the cloned repo match a `*.local*` gitignore pattern. This is
   niwa's existing "don't commit this" convention and is the hook vault
   integration should ride.

### Sub-question answers (1-9)

#### 1. Where do resolved secrets live?

**Proposal:** a mix, with strong defaults toward disk-with-0o600 for
materialized files and strict no-env-inheritance for any subprocess niwa
spawns in the future.

- **In-memory during `niwa create` / `niwa apply`.** The resolved
  `map[string]string` already flows through `ResolveEnvVars` and
  `resolveClaudeEnvVars`. Keep it in memory only for the duration of a single
  command invocation. No global singleton, no `os.Setenv`.

- **Written to disk only when a downstream tool requires a file.** Claude
  Code today reads env from `settings.local.json`; the env materializer
  produces `.local.env` for direnv-style consumers. Both are file-based
  contracts we can't break. For those, materialize to disk but tighten the
  mode to `0o600` (currently `0o644` — this is a bug to fix independently).

- **Never injected into niwa's own env.** Secrets are project data, not
  niwa's operating data. The CLI process should never call `os.Setenv` with
  a resolved secret.

- **Subprocess env is opt-in per-subprocess and is not inherited from
  niwa's env.** When niwa eventually spawns hook scripts or Claude, it must
  build the child env explicitly from the resolved map, not from
  `os.Environ()` plus overlays.

#### 2. How do we prevent leaks?

Four defenses, each enforced at a distinct layer:

- **Log/error redaction.** Introduce a `secret.Value` type (opaque string
  wrapper) whose `String()` and `GoString()` return `***`. All error
  wrapping (`fmt.Errorf("...: %w", err)`) paths that touch resolved env must
  use this type. Banned: `%v` or `%s` on raw secret strings. Enforced with
  a lint check (string type must not be `secret.Value`) and unit tests that
  grep stderr for known secret values.

- **CLAUDE.md template injection is forbidden.** The current `InstallRepoContent`
  / `InstallGroupContent` path does not interpolate env into CLAUDE.md.
  Keep it that way. If template-like syntax is ever added, secrets must be
  a distinct type that the template engine refuses to render. Static rule:
  CLAUDE.md files never reference `${VAULT_*}` or equivalents.

- **`niwa status` shows path + status only.** The current `FileStatus` type
  (`status.go:17`) has `Path` and `Status` — no `Content` or `Diff`. Keep
  drift detection fingerprint-only. If we ever want a `--verbose` diff,
  secret-marked files must be excluded from the diff output.

- **No secrets on argv.** `niwa apply` and `niwa create` must not accept
  `--secret FOO=bar` on the command line. Resolution happens via config file
  references to the vault. Interactive `niwa vault login` is OK because the
  secret is read from a TTY prompt, not argv.

#### 3. Caching policy

**Proposal:** no caching by niwa at all in v1. Re-resolve on every
`niwa create` / `niwa apply`. Delegate caching to the vault CLI itself
(1Password's CLI caches session tokens; Infisical CLI caches auth; sops
uses age keys from disk).

- Rationale: caching inside niwa introduces its own key-storage problem
  (where does the cache live? at what perms? invalidated how?). The vault
  providers already solve this. niwa's job is to call the CLI and hand the
  result to materializers.

- If performance becomes a problem (a workspace with 50 secrets × 3s CLI
  round-trip = slow apply), revisit with a process-lifetime in-memory cache
  only. Never a disk cache.

- Rotation: because niwa re-resolves on every apply, the next `niwa apply`
  picks up the new value automatically. See sub-question 8.

#### 4. Drift detection interaction

Today: `ManagedFile.Hash` is SHA-256 of raw content. If a file contains a
rotated secret, the on-disk hash changes and `niwa status` reports
`drifted`.

**Proposal:** add `ManagedFile.SourceFingerprint` (a hash of the resolution
inputs — the config-file reference plus vault metadata like
`version/etag`), separate from the content hash. Two cases:

- **User edited the file locally.** Content hash differs, source fingerprint
  unchanged → report `drifted`. Same as today.

- **Secret rotated upstream.** Content hash differs because resolution
  produces new bytes, but source fingerprint also differs (new vault
  version). Report `stale` (not `drifted`) and recommend `niwa apply`.
  `niwa apply` re-resolves, writes the new bytes, and updates both hashes.

- **File is marked as secret-bearing.** `niwa status` never prints the path
  (optional) or prints only the path without the status reason. Simpler: a
  `ManagedFile.Sensitive bool` flag suppresses any future verbose diff.

#### 5. Threat-model boundary

**Trusted:**
- The user's machine (laptop filesystem, process memory, OS keychain).
- The vault provider CLI (1Password, Infisical, sops-with-age) — niwa
  invokes these as subprocesses and trusts their output.
- The git credentials on the machine used to clone config repos.

**Not defended against:**
- Malicious processes running as the same user (can read `0o600` files).
- Physical laptop theft without disk encryption.
- Compromised vault provider credentials.
- Attackers with root on the machine.
- Compromised subprocess binaries (e.g., a trojaned `op` CLI).

**Explicitly defended against:**
- **Accidental commit** of plaintext secrets into the config repo. niwa
  must never write resolved secrets back into `ConfigDir`.
- **Accidental commit** into the instance directory. Materialized secret
  files must carry a `.local` infix and the instance directory should ship
  with a `.gitignore` (see sub-question 7).
- **Accidental inclusion** in CLAUDE.md shared with the team. CLAUDE.md is
  a pure-text layered document; no secret interpolation.
- **CI logs.** Error messages and stderr from `niwa apply` must redact
  resolved values even when verbose/debug logging is enabled.
- **Shell history.** No `niwa` subcommand accepts a secret on argv.

#### 6. Process spawn

Today niwa does not spawn Claude Code or hook scripts — it writes files and
exits. The user launches Claude Code themselves (or via a setup script run
by `RunSetupScripts` in `apply.go:420`).

**For future subprocess cases:**

- **Vault CLI (resolve-time).** niwa execs `op`, `infisical`, `sops` etc.
  The child gets niwa's env *minus* any niwa-managed state plus whatever the
  vault CLI needs (e.g., `OP_SESSION`). Output read via stdout pipe, not
  temp file. Stderr relayed to user but filtered through the redactor.

- **Setup scripts (`RunSetupScripts`).** The current implementation runs
  per-repo setup scripts with the user's ambient env. If a setup script
  needs a resolved secret, the secret should come from the materialized
  `.local.env` at `0o600`, not from niwa's subprocess env block. Setup
  scripts are user-authored code; niwa trusts them as much as the user
  does.

- **Claude Code (if niwa ever launches it).** Build the child env
  explicitly: start with `os.Environ()` filtered of any secret-shaped
  variables, then overlay only the resolved Claude env block. Never pipe
  secrets on stdin (Claude Code doesn't accept that anyway).

- **Per-process temp file.** Reject unless the target tool can't read
  stdin/env. If used, create under `os.TempDir()` with `0o600`, `defer
  os.Remove`.

#### 7. `.gitignore` hygiene

**Proposal:** `niwa create` writes a `.gitignore` at the instance root if
one is missing, listing:

```
*.local
*.local.*
.local.env
.claude/settings.local.json
```

The `localRename` convention (`materialize.go:521`) was designed for this —
every materialized file already matches `*.local*`. This covers the case
where the instance directory is initialized as a git repo later. It's
defense-in-depth: even without `.gitignore`, materialized files carry the
`.local` infix so they match common ignore patterns.

**Stronger rule:** any file that resolved from a vault reference carries a
sentinel — either naming (`.secret.` infix) or a managed-files flag — and
niwa refuses to materialize it to a path that isn't `.local`-ignored.

#### 8. Rotation story

niwa does not watch the vault. Rotation is detected when the next
`niwa apply` runs and the resolution output changes.

**Proposal:**
- `niwa apply` always re-resolves. If the resolved value differs from the
  one that produced the current managed file, it re-materializes and
  reports `rotated <path>` to stderr.
- `niwa status --check-vault` (opt-in, costs a vault call) re-resolves
  without writing, and reports which managed files would change. This is
  the "warn me about rotation" mode. Default `niwa status` stays offline
  and hash-based.
- No daemon. No periodic refresh. User triggers.

#### 9. Plaintext holdover

During migration, a `workspace.toml` may mix plaintext `[env].vars` with
vault refs. Communicate this with:

- **A `niwa status --audit-secrets` subcommand** that enumerates
  `[env]`, `[claude.env]`, `[repos.*.env]` entries and classifies each as
  `plaintext | vault-ref | empty`. Prints a table and exits non-zero if any
  plaintext values are detected.
- **A loud warning on `niwa apply`** when plaintext values are present in a
  workspace config that also references a vault. Tone: "you've partially
  migrated; consider moving KEY1, KEY2, KEY3 to the vault."
- **No silent promotion.** niwa does not auto-upload plaintext values to
  the vault; the user runs an explicit `niwa vault import` flow (out of
  scope for v1 but signposted).
- **Public-repo guardrail.** If the config repo has `public: true` metadata
  (or niwa detects a public GitHub remote) and plaintext env values are
  present, refuse to apply and print instructions.

### Threat model diagram (text)

```
                          +----------------------------+
                          |  Vault Provider Service    |   <-- out of scope
                          |  (1Password / Infisical /  |       (trust boundary;
                          |   sops-encrypted file)     |        we trust its CLI)
                          +-------------+--------------+
                                        |
                                        | auth via vault CLI
                                        | (user creds in OS keychain)
                                        v
            +------------------------------------------------------+
            |                USER'S LOCAL MACHINE                  |
            |                                                      |
            |    +----------+        +---------------------+       |
            |    | niwa CLI |--exec->|  vault CLI (op,     |       |
            |    |          |<-stdout| infisical, sops)    |       |
            |    +----+-----+        +---------------------+       |
            |         |                                            |
            |         | resolved map[string]string (in-memory)     |
            |         v                                            |
            |    +----------------+                                |
            |    | materializers  |                                |
            |    +-------+--------+                                |
            |            |                                         |
            |  0o600     | writes to disk                          |
            |  .local    v                                         |
            |    +-----------------------------+                   |
            |    | instance dir (.local.env,   |                   |
            |    | settings.local.json)        |                   |
            |    +-----------------------------+                   |
            |            |                                         |
            |            | read by user-launched tools             |
            |            v                                         |
            |    +-------------------+                             |
            |    | Claude Code, git, | (unchanged; niwa doesn't    |
            |    | user shells       |  spawn these today)         |
            |    +-------------------+                             |
            +------------------------------------------------------+
                                        |
                                        | user git-commits
                                        v
                              +-------------------+
                              | config repo (git) |  <-- public or private;
                              | or team config    |      MUST NEVER contain
                              | repo (public ok)  |      resolved secrets
                              +-------------------+

Trust boundaries:
  B1: Vault service <-> niwa (user's vault CLI auth; niwa trusts CLI output)
  B2: niwa process <-> disk  (0o600 file mode; .local infix; instance-dir gitignore)
  B3: disk <-> git push      (guarded by .gitignore + .local convention)
  B4: niwa process <-> logs  (redactor around all log/error paths)

Known-risk flows (explicitly documented as "user problem, not niwa problem"):
  - User sudo's another process that reads $HOME
  - User disables disk encryption
  - User adds the instance dir to a git repo without .gitignore
```

### "Never leaks" invariants

These become the security requirements in the PRD:

1. **INV-NO-ARGV.** No niwa subcommand accepts a secret value on argv or
   as a short flag. All secret values arrive via vault refs in config or
   interactive TTY prompts.

2. **INV-REDACT-LOGS.** Any string value that originated from vault
   resolution MUST flow through the `secret.Value` type, whose default
   formatter emits `***`. Stderr, stdout, and any structured log output
   must not print the raw value.

3. **INV-NO-CONFIG-WRITEBACK.** niwa must never write a resolved secret
   into `ConfigDir` (the cloned config repo working tree). Resolution is
   read-only against config.

4. **INV-FILE-MODE.** Any materialized file containing a resolved secret
   is written with mode `0o600`. Parent directories needed for the file
   may be `0o755`.

5. **INV-LOCAL-INFIX.** Any materialized file containing a resolved secret
   carries the `.local` infix so it matches default niwa/Claude gitignore
   patterns.

6. **INV-GITIGNORE-ROOT.** `niwa create` ensures the instance root has a
   `.gitignore` covering `*.local*` (created if missing; merged if
   existing).

7. **INV-NO-CLAUDE-MD-INTERP.** CLAUDE.md, CLAUDE.local.md, and all
   materialized Markdown files are treated as opaque text — no secret
   interpolation syntax. A vault reference written into a `.md` file is a
   literal string, not a resolution target.

8. **INV-STATUS-PATH-ONLY.** `niwa status` output includes file paths and
   drift/stale/removed status only. It never echoes file contents for
   files marked sensitive.

9. **INV-NO-ENV-INHERIT.** When niwa spawns a subprocess that needs
   secrets, it constructs the child env explicitly. Secrets are never
   published into niwa's own `os.Environ()` first and inherited.

10. **INV-NO-PERSISTENT-CACHE.** niwa maintains no on-disk cache of
    resolved secret values outside the materialized files themselves.
    In-memory is bounded by the lifetime of a single command invocation.

11. **INV-FAIL-CLOSED.** If vault resolution fails for any referenced key,
    `niwa apply` aborts before any materializer runs. Partial
    materialization with missing secrets is forbidden. The prior state on
    disk is preserved.

12. **INV-REDACT-ERRORS.** Error messages emitted when resolution fails
    name the vault reference (e.g., `vault://team/GITHUB_TOKEN`) but never
    include the resolved value or anything adjacent to it in memory.

### Patterns to adopt

1. **1Password CLI `op run` — env injection via pipe, not environment.**
   `op run -- claude-code` resolves `OP_*` references and passes them to
   the child process, then the parent process never sees them. Adoption:
   when niwa eventually spawns tools, the subprocess-env approach should
   mirror this — build the child env explicitly, don't inherit.

2. **direnv `.envrc` + deny-list + `direnv allow` audit step.** direnv
   requires explicit user approval of any new `.envrc` before it loads.
   Adoption: `niwa apply` could gate first-time resolution against a new
   vault provider behind an explicit `niwa vault trust <provider>` step,
   making rogue vault references in a config repo require user
   acknowledgment.

3. **sops `--extract` streaming.** sops can decrypt and stream a single
   key to stdout without materializing the whole file. Adoption: niwa's
   resolver should request individual keys from the vault, not bulk-fetch
   a whole secret store. This keeps the in-memory footprint small and
   makes redaction targeting easier.

4. **git-secret / trufflehog-style pre-commit check (team config repo).**
   The PRD should recommend (not mandate) that config repos install a
   pre-commit hook that scans for high-entropy strings. niwa doesn't own
   the hook but can publish an example.

5. **gh auth token rotation model.** `gh auth login` stores tokens in the
   OS keychain and `gh` pulls them per-invocation. Adoption: niwa never
   stores its own auth tokens — it delegates to each vault CLI, which
   uses its provider's native storage.

6. **chezmoi template + secrets separation.** chezmoi keeps template
   content and secret values in different files and only fuses them at
   apply time. Adoption: niwa's materialization model already does this
   (templates in config repo, secrets in vault) — codify that this
   separation is non-negotiable.

### Anti-patterns to reject

1. **Writing a `.env.secrets` file with mode `0o644`.** The current
   `EnvMaterializer` writes `0o644` — that is already wrong for
   plaintext env files and becomes unacceptable for resolved secrets.
   Fix to `0o600` before vault refs land.

2. **`niwa --secret KEY=value apply`.** Any flag that puts secrets on
   argv is rejected outright. Shell history and `ps auxww` are public.

3. **`os.Setenv` of resolved values in the niwa process.** Publishes the
   secret into every child process, every plugin, every panic traceback.

4. **In-process disk cache keyed by workspace name.** Adds a new storage
   location with its own expiry, permission, and invalidation bugs.
   Vault CLIs already cache their auth; secret values themselves should
   not be cached by niwa.

5. **Template interpolation in CLAUDE.md.** Turning CLAUDE.md into a
   template language is a foot-cannon — a stray `${VAULT_GITHUB_TOKEN}`
   in a team-shared doc leaks on every render. Declare CLAUDE.md opaque.

6. **Hash-only drift detection for secret-bearing files.** When the
   vault rotates, the hash changes and the user sees `drifted` with no
   context. The `SourceFingerprint` addition (sub-question 4)
   distinguishes user-edit drift from upstream rotation.

7. **Silent partial resolution.** If three of four vault refs resolve and
   one fails, niwa must not materialize the three and skip the fourth.
   Fail the whole apply (INV-FAIL-CLOSED).

8. **Logging resolved env maps for debug.** `fmt.Printf("%+v\n",
   resolvedEnv)` is the most common leak path in tools like this. Ban
   the `map[string]string` shape; require
   `map[string]secret.Value` for anything downstream of vault.

9. **Materializing to a user-chosen absolute path outside the instance
   root.** Already blocked by `checkContainment`; preserve that for
   vault-resolved content too. No `[files]` entry can target
   `~/.bashrc`.

10. **Using the state.json hash as an audit log of secret values.** The
    SHA-256 is not a secret value but it is a strong fingerprint.
    State.json is tracked carefully (not committed), but we should
    document that even the hash should not be published in bug reports.

## Implications

- **Immediate pre-requisite work.** The existing `EnvMaterializer` and
  `SettingsMaterializer` write `0o644` files. Before vault integration
  ships, those modes must tighten to `0o600` for anything resolved from
  the env pipeline. This is a small, isolated change and could land as a
  standalone bug fix.

- **New package `internal/secret`.** Introduces the `secret.Value` opaque
  type, a `Resolver` interface, and redaction helpers. Every materializer
  that currently handles `map[string]string` env gets a parallel signature
  taking `map[string]secret.Value`.

- **State schema bump.** `ManagedFile` gains `Sensitive bool` and
  `SourceFingerprint string`. This is a SchemaVersion migration.

- **`niwa status` contract.** Keep it offline and hash-only by default;
  add `--check-vault` for active rotation detection. Add `--audit-secrets`
  as a read-only classifier.

- **Instance `.gitignore` bootstrap.** Add to the create pipeline. Small
  code change; material security benefit.

- **No subprocess execution required for v1.** Since niwa doesn't spawn
  Claude Code today, the env-inheritance problem is theoretical for now.
  The invariants still apply but implementation is simpler: just "write
  the right files with the right modes."

## Surprises

- **Env materializer already writes `0o644`.** The current plaintext env
  files are world-readable. This is a latent security issue even without
  vault integration. Worth flagging as a bug regardless of the PRD's
  outcome.

- **state.json hashes every secret file's content.** Not a disclosure
  (hashes aren't reversible), but it means state.json's integrity is
  surprisingly sensitive. Users who share state.json for debugging are
  exposing a structural map of their secret files.

- **`RunSetupScripts` runs arbitrary scripts from repos with ambient
  env.** If niwa injects secrets into its own env (which we're rejecting),
  setup scripts inherit them silently. The existing design (scripts read
  `.local.env` themselves) is actually safer and should be preserved.

- **Drift detection re-runs on every Apply** (`apply.go:119`). For
  secret-bearing files, drift warnings before overwrite could
  inadvertently leak the fact that a user manually edited a secret file.
  The current warning prints only the path, which is fine — but future
  "verbose" flags need guardrails.

- **`localRename` is a pre-existing security primitive** we didn't plan
  as such. It already ensures materialized files match `*.local*` ignore
  patterns, which vault-resolved files should ride rather than reinvent.

## Open Questions

1. **Who owns the `secret.Value` abstraction boundary?** If a hook script
   prints `$GITHUB_TOKEN` to stdout, niwa can't do anything about it.
   Where do we draw the "niwa is responsible for leaks" line exactly — at
   the file write, or at the process boundary?

2. **Does `niwa status --check-vault` count against vault rate limits?**
   For providers with strict free-tier limits (Infisical, Doppler), an
   overzealous status run could exhaust the daily read budget. Worth
   defaulting to a dry flag that prompts before hitting the network.

3. **Per-repo vs. per-workspace secret scoping.** If `[repos.X.env]`
   references a vault and `[repos.Y.env]` references the same vault
   differently, does niwa open one session or many? Affects the "single
   atomic resolution failure aborts the apply" story.

4. **What happens on Claude Code's side when settings.local.json changes
   between apply and launch?** If Claude Code is running and niwa rewrites
   settings, Claude may or may not pick up the new env. Document the
   expected restart discipline.

5. **Cross-instance secret isolation.** Two instances of the same
   workspace config (e.g., `niwa create` run twice) both resolve the same
   vault refs. Is that intentional (same secrets everywhere) or
   surprising (user wanted per-instance scoping)? Today it's intentional;
   vault integration probably keeps that, but worth naming in the PRD.

6. **Is an in-memory-only mode feasible for Claude Code?** If a future
   Claude Code adds a "read env from stdin" API, niwa could stop
   materializing settings.local.json entirely. Worth floating to
   Anthropic; meanwhile we ship the file-based path.

## Summary

niwa's current materialization pipeline writes env and settings files at 0o644 with plaintext content, hashes everything into state.json, and runs no subprocesses that consume secrets — so vault integration is mostly additive but must first fix the file-mode bug and introduce a `secret.Value` opaque type, fail-closed resolution, and a `SourceFingerprint` on ManagedFile to distinguish rotation from user-edit drift. The threat model treats the local machine as trusted and focuses defense on accidental git commits, shared CLAUDE.md leakage, and log/CI disclosure via redaction, `.local` infix, instance-root `.gitignore`, and path-only status output. Twelve "never leaks" invariants (no-argv, redact-logs, no-writeback, file-mode, local-infix, gitignore-root, no-md-interp, status-path-only, no-env-inherit, no-cache, fail-closed, redact-errors) form the security requirements; niwa should adopt `op run`-style explicit subprocess env, direnv-style trust-on-first-use for new providers, and sops-style per-key streaming, while rejecting argv secrets, `os.Setenv` publication, disk caches, and CLAUDE.md template interpolation.
