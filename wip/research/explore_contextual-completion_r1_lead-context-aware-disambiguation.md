# Lead: How should tab-completion behave for context-aware targets?

## Findings

### The resolution logic we must mirror (or knowingly diverge from)

`resolveContextAware` in `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/internal/cli/go.go` implements a
priority-ordered lookup, not a set-union:

1. If cwd is inside a workspace instance (`workspace.DiscoverInstance`), try the argument as a repo name first.
2. Independently look up the argument in the global workspace registry (`globalCfg.LookupWorkspace`).
3. Switch on the four cases:
   - Both match -> navigate to repo, print `(also a workspace; use -w to navigate there)` to stderr.
   - Repo only -> navigate to repo.
   - Workspace only -> navigate to workspace root.
   - Neither -> error with hints listing registered workspaces.

Three constraints fall out of this that bind the completion design:

- **Repo always wins over workspace** when the user is inside an instance and both resolve. The CLI already
  telegraphs this as a gentle warning. Completion must not hide the workspace collision; users already get told
  about it at run time.
- **Outside an instance, only workspaces resolve.** Repo lookup requires `DiscoverInstance` to succeed, which reads
  cwd. The completion helper runs in the same cwd as the eventual command, so completion context is identical.
- **Names are sanitized**: targets containing `/` or `..` are rejected (`resolveContextAware` line 165). Completion
  should never propose such names, which we already satisfy since repo dir names and registry keys can't legally
  contain them.

### How other CLIs handle overlapping namespaces

**git**: `git-completion.bash` enumerates refs from `refs/heads/`, `refs/tags/`, and `refs/remotes/` via
`git for-each-ref` and unions them for commands like `checkout`. It does *not* decorate with the ref kind - a
tag named `v1` and a branch named `v1` both appear as `v1`, once. `git` deals with the ambiguity at execution
time (DWIM: branches beat tags for `checkout`), not at completion time. The completion prioritizes showing
everything the user could plausibly mean; disambiguation is the user's job via `refs/tags/v1`.

**kubectl**: Cross-resource commands like `kubectl get` require a resource type before a name, so the namespace
isn't actually overlapping at completion time - by the time you're completing names, you've already picked the
kind. For verbs that accept `TYPE/NAME` syntax, completion emits namespaced pairs (`pod/web-1`) and doesn't union
across kinds for a bare name. This is the "force the user to pick a kind first" pattern, which maps onto our
`-w`/`-r` flags.

**docker**: The `docker` CLI historically exposed both container names and IDs as completion candidates for
`docker rm`, and got bug-reported for it ([docker/cli#5355](https://github.com/docker/cli/issues/5355)) because
completing to an ID replaced the typed prefix confusingly. Newer versions gate ID completion behind
`DOCKER_COMPLETION_SHOW_CONTAINER_IDS=yes` and default to names only. The lesson: when one set is
higher-signal than the other, don't clutter the default with the low-signal set.

**Cobra's rendering matrix** (confirmed from the spf13/cobra completions doc):

| Shell | Descriptions? | Format |
|-------|---------------|--------|
| bash V1 | No | ignored |
| bash V2 | Yes | `candidate  -- description` (two-column) |
| zsh | Yes | `candidate  -- description` |
| fish | Yes | `candidate (description)` |
| powershell | Yes | tooltip |

Descriptions are emitted via tab separation in the `ValidArgsFunction` return, e.g. `"tsuku\trepo"`. Cobra's
`__complete` hidden subcommand, which the generated shell snippets call, transports the tab-separated pair
unchanged. For bash V1 users, the description is silently dropped and only the candidate remains - so
descriptions are an enhancement, never load-bearing.

### Design options for `niwa go <TAB>`

**Option A: Union, undecorated.** Return `sort(repos_in_current_instance ++ registered_workspaces)` with
duplicates deduped. Same treatment whether inside or outside an instance.
- Pros: Simple; matches git's approach; users see everything they could type.
- Cons: Name collisions become invisible - a user sees `tsuku` and has no idea the CLI will pick repo over
  workspace. The stderr hint from `resolveContextAware` only fires after the command runs. For a user completing
  a name to then read/write it in a shell prompt, this is surprising.

**Option B: Union, decorated with kind.** Return `"tsuku\trepo"` and `"codespar\tworkspace"`. When the same name
is both, emit both entries so the user visually sees the duplicate and can pick via `-w` or `-r`.
- Pros: Fully discoverable. Ambiguity is visible at the point of decision. Matches the spirit of the existing
  stderr hint. Zsh and fish users get rich UX; bash V2 users get tabular layout.
- Cons: Bash V1 users see duplicates with no way to tell them apart (they'd see `tsuku` twice, indistinguishable).
  Rendering consistency is imperfect across shells. Slightly more expensive to build (must call both
  `DiscoverInstance` + load global config).

**Option C: Priority-only, matching `resolveContextAware` exactly.** Inside an instance, return only repos.
Outside an instance, return only workspaces. No decoration needed since there's only one kind on screen.
- Pros: Completion output is exactly the set of names that will resolve to the "winning" interpretation. Zero
  surprise: if you see it, it will work, and it will go where you expect.
- Cons: Hides workspace names when inside an instance, even though the user could legitimately want to
  `niwa go -w codespar`. Discoverability suffers badly for workspaces-from-inside-an-instance. Also breaks the
  expectation that tab completes "everything the command accepts as this arg".

**Option D: Prefix/sigil-based disambiguation (`@codespar` for workspaces).** Out of scope - invasive, requires
runtime parser changes to `go.go`, no precedent in the CLI today. Skip.

**Option E: Priority-first with workspace fallback + decoration when both exist.** Inside an instance: return
all repos undecorated, then append only those workspaces whose name does *not* collide with a repo, decorated as
`"name\tworkspace"`. For colliding names, append a second entry `"name\tworkspace (shadowed; use -w)"` so both
options are visible. Outside an instance: return workspaces undecorated.
- Pros: Matches runtime behavior while preserving discoverability. Handles collisions loudly. Minimal clutter.
- Cons: More complex to implement. Description-blind shells (bash V1) still see a duplicate `name` entry.

### Scoring

| Option | Discoverability | Unsurprising? | Cross-shell | Complexity |
|--------|-----------------|----------------|-------------|------------|
| A (union plain) | Good | Poor (silent collisions) | Good | Low |
| B (union decorated) | Excellent | Good | OK (bash V1 degrades) | Low-med |
| C (priority-only) | Poor | Excellent | Excellent | Low |
| E (priority + flagged fallback) | Excellent | Excellent | OK | Medium |

## Implications

**Recommendation: Option B (union, decorated).** The reasoning:

1. It mirrors the existing stderr message from `resolveContextAware`. The CLI already treats collisions as
   "worth mentioning, not fatal"; completion should do the same.
2. Cobra's tab-description mechanism is a native feature. Using it is cheap and the graceful degradation (bash V1
   drops descriptions) leaves users no worse off than Option A.
3. Discoverability matters more than keystroke economy here. Workspaces are a small set (tens at most);
   repos-per-instance likewise small. Total candidate count for `niwa go` stays well under 100 in realistic
   setups. No latency pressure from doing both lookups.
4. Option E is a strictly better version of B in the shadowing case, but the added code (detecting collisions,
   emitting two entries, customizing the description string) is not worth the marginal UX gain unless telemetry
   shows users actually hitting collisions. Start with B; promote to E if needed.

**Sketch of the completion function:**

```go
// In internal/cli/go.go init()
goCmd.ValidArgsFunction = completeGoTarget
goCmd.RegisterFlagCompletionFunc("workspace", completeWorkspaceName)
goCmd.RegisterFlagCompletionFunc("repo", completeRepoInCurrentInstance)

func completeGoTarget(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    if len(args) > 0 {
        return nil, cobra.ShellCompDirectiveNoFileComp
    }
    var out []string

    // Repos in current instance (if any).
    if cwd, err := os.Getwd(); err == nil {
        if instanceRoot, err := workspace.DiscoverInstance(cwd); err == nil {
            if repos, err := listReposInInstance(instanceRoot); err == nil {
                for _, r := range repos {
                    if strings.HasPrefix(r, toComplete) {
                        out = append(out, r+"\trepo")
                    }
                }
            }
        }
    }

    // Workspaces from global registry.
    if cfg, err := config.LoadGlobalConfig(); err == nil && cfg != nil {
        for name := range cfg.Registry {
            if strings.HasPrefix(name, toComplete) {
                out = append(out, name+"\tworkspace")
            }
        }
    }

    sort.Strings(out)
    return out, cobra.ShellCompDirectiveNoFileComp
}
```

- `ShellCompDirectiveNoFileComp` prevents cobra from falling back to filename completion when there are no
  matches, which would be nonsense here.
- Both lookups are filesystem-local and cheap; no network. Keep the calls sequential for simplicity.
- The `toComplete` prefix filter is done in Go rather than delegating to the shell, which matches cobra's
  convention and keeps the test surface inside our code.

**Flag-gated variants behave differently and cleanly:**

- `niwa go -w <TAB>` -> `completeWorkspaceName` returns workspaces only, no decoration needed (single kind).
  This is the right behavior because `-w` is an explicit opt-in to the workspace namespace.
- `niwa go -r <TAB>` -> `completeRepoInCurrentInstance` returns repos only. If cwd is not inside an instance,
  return no candidates (the command would error anyway). Don't fall back to workspaces - `-r` is an explicit opt-in.
- `niwa go -w <ws> -r <TAB>` -> complete repos in the first instance of `<ws>` (matching `resolveWorkspaceRepo`'s
  behavior). This requires reading the workspace root from global config and enumerating its instances;
  acceptable latency for a local filesystem scan.

The bare positional is where ambiguity lives, so it's the only variant that decorates.

## Surprises

- **git doesn't decorate** despite having an arguably worse namespace collision problem (branches vs tags are
  both refs, not separate concepts). It treats the union as a flat list and lets the user figure it out. Our
  context (repos vs workspaces are genuinely different things, not two flavors of one thing) argues for more
  structure than git provides, not less.
- **kubectl sidesteps the problem entirely** by requiring the resource type to be typed before the name. Our
  equivalent would be forcing `niwa go -r tsuku` or `niwa go -w tsuku` always, which contradicts the existing
  context-aware design choice. Not viable without undoing a shipped decision.
- **Bash V1 is still common on macOS defaults.** Users whose `/bin/bash` is 3.2 and who haven't installed a newer
  bash will see descriptions stripped. This is a known cobra limitation, not something we can fix. The
  mitigation is to keep decorations informational, not load-bearing - which Option B does.
- **`docker`'s env-var-gated ID completion** is a useful precedent for the idea that default completion should
  be the high-signal, low-cardinality set. Our analogue: decorating with kind so the higher-cardinality set
  (workspaces + repos combined) remains legible rather than noisy.

## Open Questions

- Should colliding names produce two completion entries (Option E refinement)? Needs dogfooding data on how often
  collisions happen in practice. Default to "no" for v1 implementation.
- What's the latency budget for `LoadGlobalConfig()` + `DiscoverInstance()` + `listReposInInstance()` on a warm
  FS? All three are local I/O, but exploration of the caching lead will tell us if we need memoization.
- Should `niwa go` (no args) complete to anything, or only flags? Current behavior navigates to workspace root;
  tab on bare `niwa go ` could suggest the workspace-root semantics, but cobra doesn't have a clean way to
  surface "no argument is valid too" hints. Probably leave it alone.
- If a workspace name and a repo name collide and the user types the shared prefix, is it better to show two
  entries or a single entry with a merged description like `"tsuku\trepo, also workspace"`? Latter is more
  compact but hides the flag hint. Defer until we see real collisions.
- How does this interact with the `niwa create <TAB>` and `niwa status <TAB>` leads? If those commands accept
  workspace names, they should share `completeWorkspaceName`. Identify shared completion helpers as part of
  implementation.

## Summary

Cobra's tab-separated description protocol plus a union-with-kind-decoration strategy (Option B) is the right
default for `niwa go <TAB>`: it mirrors the existing stderr collision hint, keeps the high-cardinality case
legible for zsh/fish/bash-v2, and degrades to a plain union on bash V1 - no worse than what git already does.
The main implication is that the bare positional gets decorated candidates while `-w` and `-r` each return a
single undecorated kind, which aligns completion output with the resolution priority encoded in
`resolveContextAware`. The biggest open question is whether to explicitly emit duplicate entries for colliding
names (Option E) to make shadowing visible on description-blind shells, or to defer that until real-world usage
shows collisions actually happen.
