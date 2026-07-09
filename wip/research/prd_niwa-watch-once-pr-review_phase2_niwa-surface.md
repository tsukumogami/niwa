# niwa surface map (grounding for PRD/DESIGN of watch --once)

Reusable code-surface facts (file:line) for the DESIGN phase.

## Dispatch
- `internal/cli/dispatch.go` — `runDispatch` (:123-327): validates prompt (<128KiB),
  resolves workspace root (`workspace.ClassifyCwd`), preflights `claude`, provisions
  instance via `provisionInstanceFunc` -> `applier.Create`, builds passthrough argv,
  launches, captures session, writes durable `SessionMapping`, attaches unless `--detach`.
- Flags via `init()` (:19-27): `--label`, `--name/-n`, `--model`, `--permission-mode`,
  `--agent`, `--detach/-d`. Prompt = single positional `args[0]` (:124), passed as one
  discrete argv element to `claude --bg <prompt>` (injection-safe).
- `--settings` injection seam (:248-261): appends `--settings <json>` when host global
  config loads. `remoteControlSettingsJSON` built in dispatch_remotecontrol.go:16.
- `--detach` false => `dispatchAttach(shortID)` at end (:319-324); true => skip attach,
  print hints, return (fan-out mode). watch --once wants --detach.

## Launcher + env (the credential-inheritance vector)
- `internal/cli/dispatch_launcher.go` — `realDispatchLaunch` (:25-46): `cmd.Env = os.Environ()`
  at :40 with NO filtering. Worker inherits FULL parent env. Confirmed vector for R8.
- Only "scrub" in tree is secret-string redaction (`internal/secret/`, `internal/vault/scrub.go`),
  NOT process-env filtering.

## Instance provisioning + settings.json merge seam (KEY for containment carrier)
- `provisionInstanceFunc = realProvisionInstance` (instance_from_hook.go:344-388): resolves
  GitHub token (`resolveGitHubToken`), builds `github.NewAPIClient`, `workspace.NewApplier(gh)`,
  `applier.Create(...)` (same path as `niwa create`).
- Instance root `.claude/settings.json` IS written/merged:
  `InstallWorkspaceRootSettings` (workspace/workspace_context.go:242-360),
  `buildSettingsDoc` + `MergeInstanceOverrides` (workspace/materialize.go:561),
  `writeRootSettings` (workspace/root_materializer.go:212-273).
  => The no-egress sandbox profile can ride this existing settings-merge seam.
- NO network/process sandbox exists today (confirmed). Sandbox profile is net-new.

## Workspace repo enumeration (for the poll intersection)
- `internal/config/config.go`: `WorkspaceConfig.Sources []SourceConfig` ([[sources]]),
  `.Repos map[string]RepoOverride` ([repos.<name>]). `SourceConfig{Org, Repos []string, MaxRepos}`
  (:307), `RepoOverride.URL` (:360). `workspace.toml` via `config.Discover`.
- `Applier.discoverRepos` (workspace/apply.go:2131-2166): explicit `source.Repos` -> synthesize
  `github.Repo` (SSH/clone URL from org+name); else `GitHubClient.ListRepos(org)`.
- Canonical identity: `source.Source{Host,Owner,Repo,...}` (internal/source/source.go:27),
  `DefaultHost="github.com"`, `IsGitHub()`. owner/repo derivable from Org+name or RepoOverride.URL.

## Command registration
- `rootCmd` (internal/cli/root.go:26). Pattern: new `internal/cli/watch.go` with `init()`
  defining flags on `watchCmd` (incl. `--once`) + `rootCmd.AddCommand(watchCmd)`.

## GitHub access (reuse) + the net-new gap
- `resolveGitHubToken()` (internal/cli/token.go:12-25): checks GITHUB_TOKEN/GH_TOKEN, then
  `gh auth token`.
- `github.APIClient` (internal/github/client.go): `NewAPIClient(token)` (:42), `ListRepos` (:56),
  `GetRepo` (:129); base URL overridable via NIWA_GITHUB_API_URL.
- NET-NEW: no `review-requested`/PR-search/pulls path exists. The poll must add a GitHub query
  (e.g. GET /search/issues?q=is:pr+user-review-requested:@me) and likely a new `github.Client`
  method. Contained review agents can reuse `dispatchLaunch`/`runDispatch`.

## Naming
- `sanitizeInstanceSlug` (dispatch.go:375-394): lowercases, non-[a-z0-9]->_, cap 40. Dash-free.
- Instance dir `<config>+<slug>-<8hex>` (dispatch, sep="+"). Dispatch-instance regex
  `\+[a-z0-9_]*-[0-9a-f]{8}$`.
