# Research: codebase seams for a `niwa onboard` wizard

Scope: what already exists in the niwa Go CLI that a new interactive `niwa
onboard` command would build on, and what is net-new. All cites are to files
under the worktree root
`/home/dgazineu/dev/niwaw/tsuku/tsuku+niwa_onboard-03ea755c/public/niwa/.claude/worktrees/niwa-onboard`.

## 1. Cobra command registration and exit-code mapping

Every subcommand lives in its own file under `internal/cli/` and follows the
same shape: package-level `var xCmd = &cobra.Command{...}`, flags declared in
`func init()`, and `rootCmd.AddCommand(xCmd)` also in `init()`. See
`internal/cli/init.go:43-56` for a representative example (the `niwa init`
command) — flags are bound to package-level vars (`initFrom`, `initBootstrap`,
etc.), and `initCmd.ValidArgsFunction` wires shell completion.

`internal/cli/root.go` defines `rootCmd` with `SilenceErrors: true` and
`SilenceUsage: true` (root.go:42-43) — cobra's own error/usage auto-printing is
suppressed so `Execute()` is the single place errors reach stderr.
`PersistentPreRunE` (root.go:44-56) runs before every command: it captures and
unsets `NIWA_RESPONSE_FILE` (the shell-wrapper cd-landing protocol) and reads
`NO_COLOR` into a package var.

Exit-code mapping happens entirely in `Execute()` (root.go:73-98):
1. Default: any error → printed to stderr, `os.Exit(1)`.
2. `*sessionattach.ExitCodeError` (via `errors.As`) → print `.Msg` if
   non-empty, exit with `.Code` (root.go:75-81).
3. `*workspace.InitConflictError` with `.ExitCode > 0` (via `errors.As`) →
   print the error's rendered text, exit with `.ExitCode` (root.go:90-94).
   `ExitCode == 0` falls through to the generic exit-1 path so older code that
   built `InitConflictError` without an explicit code keeps historical
   behavior.

**Pattern for a new command**: if `niwa onboard` needs a distinct exit code
(e.g., "user declined mid-wizard" vs. "wizard failed"), it should define its
own typed error (or reuse `*workspace.InitConflictError` with an `ExitCode`)
and it will be picked up by the existing `errors.As` chain in `Execute()` with
no changes to `root.go` — unless a *third* typed-error family is wanted, in
which case a new `errors.As` arm must be added there.

## 2. Existing interactive/prompt UX

Two independent interactive primitives already exist; neither uses a
third-party library (no survey/promptui/bubbletea import anywhere in the
module — confirmed by dependency and import search).

### a. `internal/cli/prompt.go` — generic TTY + typed-confirmation primitive

- `IsStdinTTY` (prompt.go:26-28) is a **package-level func variable** (not a
  plain func) wrapping `term.IsTerminal(int(os.Stdin.Fd()))`, specifically so
  tests can stub it. This is the golang.org/x/term dependency already in
  go.mod.
- `ReadConfirmation(prompt, expected string, in io.Reader, out io.Writer) (bool,
  error)` (prompt.go:42-57) writes a prompt, reads one line via
  `bufio.NewReader`, trims, and compares to an expected literal (typed
  confirmation, e.g. "yes" or a workspace name). A mismatch is `(false, nil)`,
  not an error — caller decides whether to hard-fail or retry.
- Used today by the `destroy` command (irreversible-operation confirmation).

### b. `internal/cli/init.go` — a second, independent Y/n prompt loop

`promptBootstrap(in io.Reader, out io.Writer) (bool, error)` (init.go:308-334)
is a hand-rolled Y/n loop: prints a fixed prompt string, reads a line,
switches on trimmed input (`""`, `"y"`, `"Y"` → true; `"n"`, `"N"` → false;
anything else → re-prompt in a loop). It does **not** reuse `ReadConfirmation`
— it's a parallel, purpose-built loop because the semantics differ (default-
yes-on-Enter, arbitrary re-prompt, vs. exact-string match). It's gated by the
same `IsStdinTTY()` check from prompt.go, called by its caller
`handleNoMarkerR13` (init.go:267-296), not internally — the TTY-vs-non-TTY
branch decision belongs to the caller, and the non-TTY path fails fast with a
fixed diagnostic (init.go:285-292) rather than blocking.

**This is the closest existing precedent for a multi-step wizard**: it shows
the established pattern of (1) flag override → (2) non-TTY fail-fast → (3) TTY
prompt loop with re-prompt-on-garbage, all as plain `bufio`/`io.Reader`
plumbing with no external TUI dependency. A `niwa onboard` wizard with several
sequential questions would almost certainly want to generalize this pattern
(e.g., a small internal helper for "ask a question, validate, re-prompt")
rather than reinvent it per-question — see Net-new section.

No other command reads stdin interactively. No `--yes` flag exists anywhere in
the CLI today (confirmed by flag-name search) — `destroy` and `init --bootstrap`
both use TTY detection + typed confirmation/Y-N, not a blanket `--yes` opt-out.

## 3. Vault layer

### Provider abstraction (`internal/vault/provider.go`)

- `Provider` interface (provider.go:36-60): `Name() string`, `Kind() string`,
  `Resolve(ctx, Ref) (secret.Value, VersionToken, error)`, `Close() error`.
  `Resolve` must return the sentinel `vault.ErrKeyNotFound` or
  `vault.ErrProviderUnreachable` for those specific failure classes (checked
  via `errors.Is` throughout the codebase — see area 5).
- `BatchResolver` (provider.go:70-72) is an optional extension detected via
  runtime type assertion, for backends that can resolve many refs in one RPC.
- `Factory` interface (provider.go:77-86): `Kind() string`,
  `Open(ctx, ProviderConfig) (Provider, error)`. Backends register a `Factory`
  with a `Registry` (typically `vault.DefaultRegistry`) via `init()` in their
  own package — this is how `internal/vault/infisical` plugs in without the
  `vault` package importing it.
- `Ref` (provider.go:89-116): parsed `vault://` URI — `ProviderName`, `Path`,
  `Key`, `Optional`.
- `ProviderConfig` is deliberately `map[string]any` (provider.go:147-155), not
  a typed struct per backend, so the config layer stays decoupled from which
  backends are compiled in.

### Infisical subprocess delegation (`internal/vault/infisical/subprocess.go`)

Niwa never re-implements Infisical's protocol; it shells out to the operator's
own `infisical` CLI. Key points:
- `commander` interface (subprocess.go:31-33) abstracts `exec.Cmd` for
  testability; production uses `defaultCommander` (subprocess.go:48-78).
- **`cmd.Env = nil`** (subprocess.go:62, comment at 38-42): the subprocess
  inherits the parent's environment completely unmodified — niwa does not
  filter or extend it. The Infisical CLI is expected to read its own auth from
  `INFISICAL_TOKEN` or `~/.infisical` config. This is a documented invariant
  (`R28: never extend Env with secrets`).
- stdout/stderr are fully captured into buffers, never streamed to niwa's own
  stdio (R22: no raw CLI stderr reaches niwa's stderr unfiltered) —
  `vault.ScrubStderr` runs on stderr before any error text is built
  (subprocess.go:149,164).
- `runInfisicalExport` (subprocess.go:119-172) builds `infisical export
  --projectId <p> --env <e> --path <path> --format json [--token <t>]` and
  parses stdout as either a flat JSON object or an array of `{key,value}`
  objects (`parseExportJSON`, subprocess.go:186-240).
- A per-project `VersionToken` is synthesized (never touches plaintext bytes)
  as a SHA-256 over sorted key names + value byte-lengths
  (`buildVersionToken`, subprocess.go:305-336) — a documented v1 coarse-grain
  trade-off pending native per-secret version IDs.

### Universal-auth login (`internal/vault/infisical/auth.go`)

- `Authenticate(ctx, entry map[string]any) (string, error)` (auth.go:33-56)
  performs Infisical's **machine-identity** universal-auth login via plain
  `net/http` + `encoding/json` (stdlib only, no vendor SDK) — POSTs
  `{clientId, clientSecret}` to `<api_url>/v1/auth/universal-auth/login` and
  returns the short-lived JWT `accessToken`.
- `client_secret` is sent **only in the HTTP POST body**, never on subprocess
  argv (R21) — the token IS then passed to the `infisical` CLI via `--token`
  (subprocess.go:134-136) once obtained, which is a defensible split (argv
  exposure vs. HTTPS body).
- The secret is registered on the context's redactor immediately
  (`secret.RedactorFrom(ctx).Register(...)`, auth.go:46-48) so every
  downstream `secret.Errorf` scrubs it automatically — this is the
  `internal/secret` package referenced below.
- `entry` requires `client_id` and `client_secret`; `api_url` is optional,
  defaulting to `https://app.infisical.com/api` (auth.go:14-16, 33-42).

### `internal/secret` package (Value / Redactor)

- `secret.Value` (`internal/secret/value.go`) is an opaque struct holding
  private `[]byte` plaintext plus non-secret `Origin` metadata
  (ProviderName, Key, VersionToken). Every standard Go emission path —
  `%s/%v/%+v/%q/%#v` via a direct `Format` implementation (value.go:122-133),
  `MarshalJSON`/`MarshalText` (value.go:137-145), and a refusing `GobEncode`
  (value.go:150-152) — emits `"***"` or refuses outright. Plaintext is
  reachable only via `internal/secret/reveal.UnsafeReveal`, a deliberately
  named escape hatch in its own sub-package so a linter can allowlist callers.
- `secret.Redactor` (`internal/secret/redactor.go`) accumulates known-secret
  byte fragments (min 6 bytes, `redactor.go:17`, shorter fragments are
  silently refused — collide too often with ordinary text) and does
  longest-first substring scrubbing. Threaded through `context.Context` via
  `secret.WithRedactor`/`secret.RedactorFrom`; every `secret.Errorf`/`Wrap`
  call in that context auto-registers/scrubs fragments.

**Implication for onboarding**: if the wizard ever prompts for a secret
(e.g., an Infisical client_secret while setting up vault config
interactively), it MUST route the entered value through `secret.Value`/the
context redactor rather than holding it as a bare `string`, to stay consistent
with the R18/R21/R22 invariants enforced everywhere else in the vault stack.

## 4. Config surfaces

### `internal/config/vault.go` — `VaultRegistry`

Parses the `[vault]` TOML table, accepting exactly one of two mutually
exclusive shapes (vault.go:5-31):
- `[vault.provider]` → `VaultRegistry.Provider *VaultProviderConfig`, anonymous
  (`Name() == ""`).
- `[vault.providers.<name>]` → `VaultRegistry.Providers map[string]VaultProviderConfig`.

`VaultProviderConfig` (vault.go:38-46) has a typed `Kind string` plus a
backend-agnostic `Config map[string]any` populated by a custom
`UnmarshalTOML` (vault.go:51-81) that captures every field except `kind` into
`Config`. `Validate(fileLabel string)` (vault.go:92-129) enforces: mutual
exclusivity, non-empty `kind` per provider, provider names matching
`NamePattern` (`[a-zA-Z0-9._-]+`). `IsEmpty()` (vault.go:136-141) and
`KnownProviderNames()` (vault.go:146-158) are small helpers used by the
resolver and by warnings.

### `internal/config/overlay.go` — `WorkspaceOverlay`

`WorkspaceOverlay.Vault *VaultRegistry` (overlay.go:25) is parsed from
`workspace-overlay.toml` in the overlay clone (`ParseOverlay`,
overlay.go:82-98). `WorkspaceOverlay` also carries `Sources`, `Groups`,
`Repos`, `Claude`, `Env`, `Files` — additive/override config layered onto the
base `WorkspaceConfig` by `MergeWorkspaceOverlay` at apply time. `validateOverlay`
(overlay.go:101-185) rejects absolute paths, `..` traversal, and writes into
protected `.claude/`/`.niwa/` destinations.

`DeriveOverlayURL` (overlay.go:202-215) and `OverlayDir` (overlay.go:285-312)
implement the `<owner>/<repo>-overlay` naming convention and the
`$XDG_CONFIG_HOME/niwa/overlays/<org>-<repo>/` clone location, respectively —
relevant if the wizard needs to explain or set up the personal overlay.

### `[global.vault.provider]` — the credential-sync provider

Parsed as part of `GlobalOverride` (`internal/config/config.go:609-622`),
which is the `[global]` (or `[workspaces.<name>]`) section of the **personal
global config overlay** (a `niwa.toml`-shaped repo registered via
`niwa config set global`, distinct from the per-workspace overlay above).
`GlobalOverride.Vault *VaultRegistry` uses the exact same anonymous-or-named
shape. The doc comment at config.go:602-608 states the load-bearing rule
plainly: **when the personal overlay declares an anonymous
`[global.vault.provider]`, that provider automatically ALSO serves as the
machine-identity credential-sync source** — no separate opt-in block exists.
This is exactly what `pickCredentialSyncSpec` (area 5) implements.

### `[env.secrets]` vault:// refs

`vault://` URIs are accepted (not required) in `[env.secrets]` values and
resolved by a later stage; parser-level handling lives in
`internal/config/maybesecret.go` (`MaybeSecret.Plain` stays untouched until
the resolver sees a `vault://` prefix — maybesecret.go:14-15, 41, 66-67) and
`internal/config/validate_vault_refs.go` (post-parse validation: R3 deny-list
of slots where `vault://` is forbidden, same-file scoping check — see
`hasVaultPrefix`, validate_vault_refs.go:13-21). `env_tables.go` documents
that `[env.vars]`, `[env.secrets]`, `[claude.env.vars]`, `[claude.env.secrets]`
(plus their `repos.<name>.` / `instance.` variants) are the four locations
this applies to (env_tables.go:111-156).

## 5. Credential-sync internals

### `internal/workspace/credentialsync.go`

- `pickCredentialSyncSpec(g config.GlobalOverride) *vault.ProviderSpec`
  (credentialsync.go:31-41): returns `nil` if `g.Vault == nil ||
  g.Vault.Provider == nil` (no anonymous provider declared — named-only
  providers never become the sync source); otherwise synthesizes a
  `vault.ProviderSpec{Name: "", Kind: ..., Config: ..., Source: "global
  overlay"}` directly from the already-authored `[global.vault.provider]`
  declaration. It's explicitly a *router*, not a parser.
- `openCredentialSyncProvider(ctx, syncSpec) (*vault.Bundle, vault.Provider,
  error)` (credentialsync.go:59-72): opens the provider via
  `vault.DefaultRegistry.Build`, and **deliberately does NOT call
  `injectProviderTokens`** against this spec (comment at 49-58) — that would
  be the chicken-and-egg cycle PRD R9 forbids (using machine-identity
  entries sourced FROM this vault to authenticate INTO this vault). The
  factory falls through to CLI-session auth (e.g., an active `infisical
  login` session) instead.
- Two-stage R9 "chicken-and-egg" validation:
  `validateCredentialSyncBootstrapPreOverlay` (credentialsync.go:117-150,
  runs before the workspace overlay is parsed — checks the local
  credential-pool file plus the global overlay's own vault specs) and
  `validateCredentialSyncBootstrapPostOverlay` (credentialsync.go:163-179,
  runs after, against the workspace-overlay's vault specs). Both reject via
  `chickenAndEggError` (credentialsync.go:186-195) if the sync provider's
  `(kind, project)` collides with anything that would need it to bootstrap
  itself.

### `internal/workspace/credentialpool.go`

- `parseProviderAuthBody(kind, project string, raw []byte)
  (*ProviderAuthEntry, error)` (credentialpool.go:65-...): parses a
  vault-fetched TOML credential body (`{version, client_id, client_secret,
  api_url}`) fetched from `CredentialSyncPathPrefix + "<kind>/p-<project>"`
  (the `/niwa/provider-auth/` convention, credentialpool.go:14-34). Caps body
  size at 8 KiB (`maxProviderAuthBodyBytes`, credentialpool.go:42) and never
  includes body bytes or field *values* in error text — only the vault
  key path and field *name* (PRD R18 / AC-36).
- `CredentialPool.lookupVault(ctx, kind, project)` (credentialpool.go:415+):
  the **dynamic** counterpart to the static R9 check. At line 428:
  ```go
  if p.vaultLoader.SelfKind != "" && kind == p.vaultLoader.SelfKind && project == p.vaultLoader.SelfProject {
      return nil, nil
  }
  ```
  — refuses to `Resolve` the credential-sync provider's own `(kind, project)`
  pair (it behaves like a silent `ErrKeyNotFound`, and the audit trail records
  `SourceCLISession` as the actual auth path used). This guard exists because
  `injectProviderTokens` iterates the global overlay's *entire* vault
  registry, which necessarily includes the sync spec itself, so without this
  check a self-referential `Resolve` call would fire.
- Cache-then-lookup pattern (credentialpool.go:432-447): checks
  `p.cache[kind+"/"+project]` first; on miss, builds a `vault.Ref{Path:
  PathPrefix+kind, Key: "p-"+project}` and calls `Provider.Resolve`.
  `ErrKeyNotFound` → cache the absence, silent fallthrough (PRD R13.3).
  `ErrProviderUnreachable` → wrapped into a typed `*vaultUnreachableError`
  (credentialpool.go:463-469) carrying Kind/Project/ProviderName so the apply
  orchestrator can aggregate one warning per unreachable provider.

### Three `VaultRegistry` sources in `internal/workspace/apply.go`

The apply pipeline calls `injectProviderTokens` against three distinct
`VaultRegistry` values, each independently:
1. **Workspace-overlay's own vault** (apply.go:915) —
   `injectProviderTokens(ctx, credentialPool, overlay.Vault)`, right before
   building the overlay's own vault bundle (apply.go:919-927). Preceded by
   the post-overlay R9 check (apply.go:905-909).
2. **Team config's vault** (apply.go:1035) —
   `injectProviderTokens(ctx, credentialPool, cfg.Vault)`.
3. **Personal global overlay's vault** (apply.go:1038-1041) — guarded by
   `if globalOverride != nil`, then
   `injectProviderTokens(ctx, credentialPool, globalOverride.Global.Vault)`.

Comment at apply.go:997-1012 explains why this must happen per-layer BEFORE
merge: file-local scoping (Decision D-6 of the vault-integration design) —
merging first would flatten provider declarations and make R12 collision
detection impossible.

## 6. `niwa status --audit-auth`

Implemented in `internal/cli/status_audit_auth.go`. `runAuditAuth`
(status_audit_auth.go:35-63) is entirely **offline**: it discovers the
instance (`workspace.DiscoverInstance`), loads `state.json`
(`workspace.LoadState`), and renders `state.AuthSources` — it "never makes a
vault or network call" (comment at line 29). Table columns: KIND,
PROJECT-UUID, SOURCE, FALLBACK (split from the `"<kind>/<project>"` map key
via `strings.Cut`, status_audit_auth.go:65-98).

- A row with **`Source == "none"`** means every credential source was tried
  and none resolved anything for that `(kind, project)` pair — no entry in
  `~/.config/niwa/provider-auth.toml`, no entry in the personal vault, and no
  usable CLI session (error text at status_audit_auth.go:54-59). This is the
  failure signal: `runAuditAuth` returns a non-zero exit in this case,
  telling the user to populate the credential and re-run `niwa apply`.
- A row with **`Source == "resolving"`** (or any non-"none" value like
  `"vault:personal-overlay"`, `"vault:personal-overlay(<name>)"`,
  `"cli-session"`, or a provider-auth-file source) means SOME source in the
  fallback chain produced a usable credential last apply — not necessarily
  the vault; `Fallback` names what would have been used if the primary
  source failed, or renders as an em-dash `—` when empty
  (status_audit_auth.go:132-134).

**Relevant precedent for onboarding**: this command shows the established
"discover instance → load `state.json` → render offline" pattern for any new
read-only diagnostic surface a wizard might want to add (e.g., "am I already
onboarded?").

## 7. Functional test conventions

`test/functional/features/*.feature` are Gherkin files run via `godog`
(cucumber-style), driven by `test/functional/suite_test.go` +
`steps_test.go` + per-feature step files (e.g.,
`steps_init_bootstrap_test.go`). Tests require a **prebuilt niwa binary**
(`make test-functional`), invoked as a real subprocess per scenario, not via
in-process Go calls.

- **`@critical` tag convention**: applied per-`Scenario` (not per-`Feature`)
  to mark scenarios that must pass on every PR / release gate — confirmed by
  grep across `critical-path.feature`, `claude-key-consolidation.feature`,
  `install-integration.feature`, `init_bootstrap_failures.feature`,
  `init_bootstrap_idempotency.feature`, `worktree-env-parity.feature`,
  `init-workspace-dir.feature`, `mcp-root-instance-distribution.feature`, and
  `workspace-imports.feature`. A new `onboard` feature file should tag its
  core happy-path and TTY-decline scenarios `@critical` following this
  precedent.
- **Hermetic execution**: `testState` (suite_test.go:19-56) sandboxes `$HOME`,
  `$TMPDIR`, and the workspace root per scenario; `sharedBinDir`
  (suite_test.go:36) is a directory **always prepended to `$PATH`** holding
  hermetic CLI stubs.
- **Infisical stub, confirmed**: `writeFakeInfisical(dir string) error`
  (`steps_test.go:118-139`) writes a shell-script stub named `infisical` into
  `sharedBinDir`. The stub only recognizes `export`: if `$1 == "export"` it
  echoes `{}` (empty JSON object), and it exits 0 unconditionally for
  anything else. This makes `vault.ErrKeyNotFound` the deterministic result
  of every credential resolution attempt in the functional suite, so
  scenarios that declare an `infisical` vault provider run fully offline
  with no real Infisical service or developer login. This is a **fake
  binary on PATH**, not an in-process fake — there is no Go-level fake
  Infisical *provider* used in functional tests (that exists only for unit
  tests, at `internal/vault/fake`, which deliberately does not
  self-register — see `internal/vault/provider.go` package doc, lines
  18-22).
- `critical-path.feature:279-305` ("provider-shadow notice...") is a good
  worked example combining `[vault.provider]` in both the team config and the
  personal overlay in one scenario.

## 8. `niwa init --bootstrap` and the frozen `workspace.toml` template

`--bootstrap` (init.go:53, flag registered in `init()`) triggers when the
remote source has no `.niwa/workspace.toml` (a `*config.NoMarkerError`).
`--no-bootstrap` (init.go:54) is the explicit-decline flag, mutually
exclusive with `--bootstrap` (enforced elsewhere in init.go per the comment
at init.go:442-451, PRD R25).

`handleNoMarkerR13` (init.go:267-296) implements the full flag-interaction
matrix: `--bootstrap` set → proceed unconditionally; `--no-bootstrap` set →
fail-fast with a fixed diagnostic and exit code 4 (via
`workspace.InitConflictError{ExitCode: 4}`, tying back to area 1's exit-code
mapping); neither flag + non-TTY → fail-fast with a different fixed string
("...re-run with --bootstrap to scaffold"); neither flag + TTY → the
`promptBootstrap` Y/n loop (area 2).

The scaffold itself lives in `internal/workspace/scaffold.go`:
`scaffoldTemplate` (scaffold.go:10, "the commented workspace.toml template")
feeds `Scaffold(dir, name string) error` (scaffold.go:108) and
`ScaffoldFromSource(dir string, opts ScaffoldOptions) error` (scaffold.go:209),
the latter used by the bootstrap orchestrator (`runBootstrap` /
`defaultRunBootstrap`, wired via package-level seams at init.go:74-104 so
tests can inject fixtures without a real GitHub client or git invoker).

**Why config authoring must target the upstream source repo, not the
materialized `.niwa/` snapshot** — per
`docs/guides/workspace-config-sources.md:128-140,163`: `<workspace>/.niwa/`
is a **pure file tree with no `.git/` directory** ("`git status` inside the
snapshot returns 'not a git repository'"). `niwa apply` **atomically replaces
the whole directory** on every run via a two-rename swap
(workspace-config-sources.md:165-176). Any manual edit inside `.niwa/`
"survives only until the next `niwa apply`, which replaces the directory
atomically from the upstream source" (line 139-140, restated at 163). This is
exactly why the bootstrap flow stages its scaffold on a **branch in the
upstream source repo** (`niwa-bootstrap`) rather than writing directly into
the local `.niwa/` snapshot and calling it done — the doc explicitly states
the intended follow-up steps are "push the bootstrap branch" (init.go:243)
and presumably open a PR against the source repo, not commit anything
locally. A `niwa onboard` wizard that helps a user set up vault/credential
config for the first time must follow the same rule: any TOML it writes
that's meant to persist has to land in the **upstream source repo** (the
team config repo, or the personal global-config-overlay repo, or the
workspace-overlay repo), never in the local `.niwa/` snapshot directory,
or the next `niwa apply` silently discards it.

## Net-new surface

Nothing in the codebase today implements a multi-step, multi-question
onboarding wizard. Specifically absent:

1. **No generalized "ask a sequence of questions" helper.** `prompt.go`
   gives a single typed-confirmation primitive; `promptBootstrap` in
   init.go gives a single Y/n loop. Both are one-shot, hand-rolled, and
   scoped to their one call site. A wizard with several sequential
   questions (vault kind? provider name? project id? client_id/secret?)
   needs a small reusable "ask(prompt, validate) → answer, re-prompt on
   invalid" abstraction that doesn't exist yet — it would naturally
   generalize the re-prompt loop shape from `promptBootstrap`.
2. **No interactive secret entry path.** Nothing in the CLI today prompts a
   user to type a secret value (e.g., an Infisical `client_secret`) and
   route it into a `secret.Value` / redactor immediately. `Authenticate` in
   `auth.go` only ever receives already-parsed `map[string]any` entries
   read from a TOML file (`ProviderAuthEntry`), never raw stdin. A wizard
   that asks for a secret interactively needs new plumbing: read a line
   (likely without echo, which also doesn't exist — no `term.ReadPassword`
   usage anywhere today), wrap it in `secret.Value`, register it on a
   `Redactor` before it touches any error path.
3. **No "detect current onboarding state" query.** `status --audit-auth`
   reads `state.json` from an *existing, applied* instance. There's no
   existing helper that inspects whether a *fresh* environment (no
   instance yet, or no vault config yet) is "onboarded" vs. not — a wizard
   entry point would need to define what "already onboarded" means and
   check for it (e.g., does `~/.config/niwa/config.toml` exist? does the
   personal overlay declare `[global.vault.provider]`? is `infisical`
   binary on PATH and logged in?) before deciding whether to run.
4. **No file-write-to-upstream-repo helper for a wizard-authored TOML
   block.** The bootstrap flow's `ScaffoldFromSource` writes a whole
   `workspace.toml` template and stages it on a git branch in the source
   repo (via a real git invoker + GitHub client, orchestrated by
   `workspace.RunBootstrap`). A vault-onboarding wizard likely wants a
   *smaller*-grained version of this: append/merge a `[global.vault.provider]`
   block into an existing personal-overlay `niwa.toml` (or scaffold that
   repo if it doesn't exist yet), then guide the user to commit/push it —
   no existing helper does partial-TOML-block insertion into an existing
   file; today's TOML writing is whole-file scaffold or programmatic
   struct marshaling, not surgical block insertion into hand-authored
   config.
5. **No provider-agnostic "walk me through choosing a vault kind" step.**
   The vault `Registry`/`Factory` abstraction is provider-agnostic in code
   (`internal/vault/provider.go`), but only `infisical` is actually
   registered today (confirmed: `internal/vault/fake` deliberately doesn't
   self-register, and no other backend package exists under
   `internal/vault/`). A wizard offering "which vault backend?" as a
   question currently has exactly one real answer; this isn't a code gap
   so much as a scope note — the wizard's vault-kind step will be trivial
   (or hardcoded) until more backends land.
6. **No functional-test feature file for an onboarding flow.** None of the
   25 existing `.feature` files touch an `onboard` command or a
   multi-step wizard interaction pattern; a new
   `test/functional/features/onboard.feature` plus step definitions would
   be net-new, following the `@critical`-tagging and `sharedBinDir`-stub
   conventions documented in area 7.
