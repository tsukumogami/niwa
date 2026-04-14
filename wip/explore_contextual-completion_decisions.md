# Exploration Decisions: contextual-completion

Running in `--auto` mode. Decisions recorded here follow the research-first
protocol: evidence from the round 1 research files grounds each choice.

## Round 1

- **Scope v1 covers 11 of 14 identified positions**: `apply [ws]`,
  `apply --instance`, `create [ws]`, `destroy [inst]`, `go [target]`,
  `go -w`, `go -r`, `reset [inst]`, `status [inst]`, `init [name]`
  (registry-only), and a path-planning notation for flag value completion.
  **Rationale:** Lead 2 maps three helpers — `completeWorkspaceNames`,
  `completeInstanceNames`, `completeRepoNames` — over 13 positions.
  Deferring `create -r` (needs pre-create config parse) and `config set
  global <repo>` (free-form URL/slug) keeps v1 tight without losing the
  common paths.

- **Disambiguation: Option B (union + TAB-decorated kind) for
  `niwa go [target]` only.** **Rationale:** Lead 4 scored Option B best on
  discoverability x unsurprising-x-cross-shell, matches the existing
  `resolveContextAware` stderr hint, and degrades gracefully on bash V1.
  `-w` and `-r` flag completions stay undecorated since they are explicit
  kind opt-ins.

- **Destroy/reset completion: no special treatment.** Complete normally.
  **Rationale:** Precedent from `git branch -D`, `docker rm`, and
  `kubectl delete pod` — none add completion-time friction. Adding
  friction is a separable UX change that belongs in its own design, not
  this one.

- **No caching layer.** **Rationale:** Lead 3 measured ~2ms cold start,
  ~3ms for a 500-workspace TOML, and <5ms for scoped filesystem walks
  even at 100k repo dirs. Only a pathological "all repos everywhere"
  walk crosses the 100ms bar, and that path is not reachable from any
  correctly-scoped completion handler. Revisit only if real-world
  measurements contradict.

- **Test strategy: 2 tiers (unit + functional) per Lead 5.** Unit tests
  in `internal/cli/completion_test.go` call completion funcs directly;
  functional tests reuse the godog harness to invoke `niwa __complete`,
  with one new step `aRegisteredWorkspaceExists` and a
  `completionSuggestions` helper that strips the `:<directive>` trailer
  and TAB descriptions. **Rationale:** The existing harness already
  sandboxes `XDG_CONFIG_HOME`; no new scaffolding needed.

- **Extract `EnumerateRepos(instanceRoot) []string` in
  `internal/workspace/`.** **Rationale:** Lead 2 flagged that the
  two-level group scan currently inlines in `findRepoDir` would copy a
  fourth time for completion. One small helper consolidates it and
  gives completion a shape it actually needs (full list, not
  short-circuit on first match).

- **No install-path changes.** **Rationale:** Lead 6 confirmed both
  `install.sh` and the in-repo tsuku recipe at
  `.tsuku-recipes/niwa.toml` already emit completion as part of
  `shell-init bash`/`shell-init zsh` output. The `__complete` callbacks
  dispatch back into the niwa binary on PATH regardless of how the
  user installed, so adding `ValidArgsFunction` closures "just works"
  on both paths.

- **Artifact type: Design Doc.** **Rationale:** Requirements are
  clear (Lead 2 table); approach is specified enough (three helpers,
  Option B, 2-tier tests) but has design-worthy depth around the
  `go -r` flag interdependency, the `EnumerateRepos` extraction, and
  destroy/reset UX policy. Phase 4 crystallize will confirm; Phase 5
  will route to `/design`.
