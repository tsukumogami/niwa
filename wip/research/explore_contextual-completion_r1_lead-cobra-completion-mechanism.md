# Lead: How does cobra's dynamic completion mechanism work, and where do bash and zsh diverge?

## Findings

### Cobra version and current niwa wiring

- `go.mod` pins `github.com/spf13/cobra v1.10.2` (line 8 of `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/go.mod`).
- `internal/cli/shell_init.go` already emits:
  - `GenBashCompletionV2(&buf, true)` for the `bash` subcommand (line 68), with `true` enabling descriptions.
  - `GenZshCompletion(&buf)` for the `zsh` subcommand (line 84), which always includes descriptions.
- Both are appended to the shell wrapper template (`shellWrapperTemplate`) and emitted from `niwa shell-init auto`, which is wired into `~/.niwa/env` via `EnvFileWithDelegation()`.

### The `__complete` subcommand (the dynamic-completion engine)

In `completions.go` of cobra v1.10.2:

- Lines 28-35: `ShellCompRequestCmd = "__complete"` and `ShellCompNoDescRequestCmd = "__completeNoDesc"` are the hidden subcommand names.
- Lines 231-306 (`initCompleteCmd`): cobra auto-registers a hidden `__complete [command-line]` subcommand on the root. It's only materialized when the program is actually invoked with `__complete` as the first arg (otherwise cobra calls `RemoveCommand` to avoid polluting the root).
- The subcommand's `Run` (lines 242-294) calls `getCompletions(args)`, prints one completion per line, then prints `:<directive>\n` as the last line. Stderr gets a human-readable "Completion ended with directive: ..." which the shell scripts discard.
- The `CompletionFunc` type alias (line 139) is `func(cmd *Command, args []string, toComplete string) ([]Completion, ShellCompDirective)`. This is what both `ValidArgsFunction` and `RegisterFlagCompletionFunc` accept. `Completion` is just a `string` alias; descriptions are appended after a literal TAB (see `CompletionWithDesc`, line 142).
- `RegisterFlagCompletionFunc` (lines 170-183) stores the function in a global `flagCompletionFunctions` map keyed by `*pflag.Flag`.

### `ShellCompDirective` flags (completions.go lines 56-96)

Relevant bit flags:
- `ShellCompDirectiveError` - signal error, ignore completions.
- `ShellCompDirectiveNoSpace` - don't add trailing space after single completion.
- `ShellCompDirectiveNoFileComp` - don't fall back to file completion when the list is empty. This is the one we'll almost always want for niwa's identifier completions (workspace/instance/repo names are never file paths).
- `ShellCompDirectiveFilterFileExt` - treat returned list as extension filters for file completion (not relevant to us).
- `ShellCompDirectiveFilterDirs` - restrict file completion to directories (not relevant).
- `ShellCompDirectiveKeepOrder` - preserve the order we return (useful if we sort by "most recently used" or want a stable pre-sorted list; bash < 4.4 silently ignores it, per the generated script lines 122-130).
- `ShellCompDirectiveDefault = 0` - fall through to shell default (file completion).

Helpers: `cobra.NoFileCompletions` (line 151) returns `(nil, NoFileComp)`; `cobra.FixedCompletions(choices, directive)` returns a closure (line 160). Both satisfy `CompletionFunc` and can be passed directly.

### How bash V2 calls back into the binary (bash_completionsV2.go)

- Lines 56-96 (`__<prog>_get_completion_results`): builds `requestComp="${words[0]} __complete ${args[*]}"`, adds an empty-string sentinel if the cursor sits past a space, and shells out via `eval`. The directive is parsed off the last line (`directive=${out##*:}`). This is the whole mechanism - no additional flags to pass at generation time.
- Lines 456-460 (`complete -F __start_<prog> <prog>`): registers the completion function. `complete -o default` means if our callback yields nothing and doesn't set `NoFileComp`, bash falls back to filenames.
- Descriptions are supported (V2-only feature). Padding/truncation logic at lines 400-420 renders them as `<completion>  (<desc>)`. Bash shows them only when there are multiple matches (otherwise bash auto-completes silently).

### How zsh calls back into the binary (zsh_completions.go)

- Lines 87-146: generates `_<prog>()` function and `compdef _<prog> <prog>` header.
- Line 140: `requestComp="${words[1]} __complete ${words[2,-1]}"` - same `__complete` subcommand, same protocol as bash V2.
- Zsh parses directives via the same `:<n>` trailing-line convention.
- `GenZshCompletion` always includes descriptions; there's a separate `GenZshCompletionNoDesc` (line 42) for opting out. We're calling the desc-including variant, which is correct for our purposes.

### V1 vs V2 bash - what's actually different

- V1 (`bash_completions.go`, 709 lines of generator code producing even larger scripts) also supports dynamic completion via `__<prog>_handle_go_custom_completion` (line 75). Contrary to an early hunch, V1 is *not* statically limited.
- V2 (`bash_completionsV2.go`, 484 lines producing ~300-line scripts) differs by: (a) adding description rendering, (b) delegating basically all completion logic to the Go binary via `__complete` rather than embedding a bash implementation of cobra's completion tree walk, (c) relying on `_init_completion` from `bash-completion` (with a fallback for macOS bash 3).
- niwa already uses V2, so we get descriptions and the lean script for free.

### Shell divergence summary

| Aspect | bash V2 | zsh |
|---|---|---|
| Dispatch mechanism | `complete -F __start_<prog>` | `compdef _<prog> <prog>` |
| Calls `__complete`? | Yes | Yes |
| Directive parsing | Yes, same `:<n>` format | Yes, same |
| Descriptions shown | Only when >1 match (bash limitation) | Always (zsh `_describe`) |
| `NoSpace` honored | Via `compopt -o nospace`; needs bash 4+ | Yes |
| `KeepOrder` honored | Needs bash >= 4.4 | Yes (`setopt no_sort`) |
| User setup | Source the script (our wrapper does `eval`) | Requires `compinit` loaded first |
| `ActiveHelp` rendering | On second TAB (`COMP_TYPE==63`) | `compadd -x` always |

### Caveats

- **Zsh `compinit`**: per cobra docs, users must have `autoload -U compinit; compinit` active. Most oh-my-zsh / prezto / modern `.zshrc` templates do this, but a bare zsh config will silently no-op `compdef`. We should document this; the env file we inject doesn't run `compinit`.
- **Descriptions in bash**: when only one candidate matches, bash auto-completes and the user never sees the description. This isn't fixable from our end; it's a bash limitation.
- **`NoSpace` on old bash**: no-ops on bash 3 (macOS default). If we rely on `NoSpace` for hierarchical completion (e.g., `workspace/instance`), macOS users get a stray space.
- **Shell wrapper interaction**: niwa wraps `niwa` as a shell function. The bash/zsh completion scripts emit `complete -F __start_niwa niwa` / `compdef _niwa niwa`. Shell functions *can* have completions attached in bash (since 4.x) and in zsh natively, so this should just work - but both shells call `${words[0]} __complete ...`, which will re-enter our function. The function falls through to `command niwa "$@"` for anything that isn't `create`/`go`, so `__complete` lands on the real binary. No changes needed, but this is worth a test.
- **`DisableFlagParsing` commands**: if we set it on any command, cobra still does flag-name completion but leaves value completion to us (see completions.go line 481-487).
- **Active Help**: cobra supports contextual help messages (`activeHelpMarker` prefix). Not needed for v1, but available.

### Sketch: adding dynamic completion for `niwa go <target>`

```go
// internal/cli/go.go (or wherever goCmd lives)
var goCmd = &cobra.Command{
    Use:               "go [target]",
    Short:             "Switch to a workspace/instance/repo",
    Args:              cobra.MaximumNArgs(1),
    ValidArgsFunction: completeGoTarget,
    RunE:              runGo,
}

// completeGoTarget runs inside `niwa __complete go <toComplete>`.
// cobra has already parsed flags; args is the slice *before* toComplete.
func completeGoTarget(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    // args is empty for the first positional; toComplete is the partial token.
    targets, err := resolveGoTargets()  // reads global registry + current workspace state
    if err != nil {
        // Surface silently: returning empty + NoFileComp is better than a shell error.
        return nil, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveError
    }

    out := make([]string, 0, len(targets))
    for _, t := range targets {
        if !strings.HasPrefix(t.Name, toComplete) {
            continue
        }
        // Optional description after TAB: shown in zsh always, bash only when >1 match.
        out = append(out, cobra.CompletionWithDesc(t.Name, t.Kind+": "+t.Path))
    }
    return out, cobra.ShellCompDirectiveNoFileComp
}
```

For flag values:

```go
goCmd.RegisterFlagCompletionFunc("workspace", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    names, _ := listWorkspaces()
    return filterPrefix(names, toComplete), cobra.ShellCompDirectiveNoFileComp
})
```

No changes to `shell_init.go` are required. The already-emitted V2/zsh scripts dispatch to `__complete` unconditionally.

## Implications

1. **No regeneration or additional generator flags needed.** The scripts we already emit (`GenBashCompletionV2(..., true)` and `GenZshCompletion`) route every positional and flag-value completion through `niwa __complete <args...>`. All we need to do is register `ValidArgsFunction` and `RegisterFlagCompletionFunc` on the relevant commands.
2. **Latency budget is driven by `niwa __complete` cold-start.** Every TAB press spawns a fresh `niwa` process. Go binary startup plus registry/state reads will dominate; we should measure and consider caching state reads. This becomes the real engineering concern, not cobra wiring.
3. **`ShellCompDirectiveNoFileComp` should be the default** for all niwa identifier completions. Omitting it means bash/zsh fall back to filename completion when state lookups return empty, which is both confusing and slow.
4. **Descriptions are free** in zsh and conditionally useful in bash. Including them (e.g., "workspace: /path/to/root") is low-cost and aids users when multiple candidates match.
5. **Test strategy can mostly bypass the shell.** We can drive `niwa __complete go wi<args>` directly in Go tests and assert the output lines and trailing `:<directive>`. Shell-level integration tests (bash and zsh subshells) only need to smoke-test the dispatch.
6. **The shell wrapper is transparent to completion** as long as we keep falling through to `command niwa` for non-wrapped commands. `__complete` is not in the wrapper's case list, so it goes straight to the binary.

## Surprises

- V1 bash completion also supports dynamic completion - I initially expected V2 to be a prerequisite. We're still right to use V2 because of descriptions and script size, but this was a misconception worth correcting.
- The `__complete` subcommand is only attached at runtime when it's actually invoked (via `RemoveCommand` immediately after registration if the user isn't calling `__complete`). This means it won't appear in `niwa help` or `niwa completion` output - no pollution to worry about.
- `ShellCompDirectiveKeepOrder` exists and actively toggles `compopt -o nosort` / `setopt no_sort`. This enables "most-recently-used first" orderings without alphabetization fighting us - could be a nice UX for `niwa go`.
- Descriptions separator is a literal TAB character in the return string. Easy to get wrong; use the `cobra.CompletionWithDesc` helper.
- Bash script registers `complete -o default ...` which means empty completions fall back to filenames *unless* we set `NoFileComp`. Easy footgun.

## Open Questions

1. **Latency**: how fast is `niwa __complete` in practice? Need a measurement on a realistic workspace (multiple instances, many repos). If > ~100ms, users will feel it. Follow-up lead should time this and decide whether caching/memoization is needed.
2. **Global vs workspace state**: do the completion functions need to import the workspace manager logic, or should we factor a lighter "read-only state lister" to keep startup cheap?
3. **Shell wrapper reentry**: need to confirm via an actual bash/zsh session that `complete -F __start_niwa niwa` attached to the niwa *function* (not just a binary on PATH) dispatches correctly. Expected to work, but worth verifying.
4. **`compinit` coverage**: should our `shell-init install` warn or auto-inject `autoload -U compinit; compinit` for zsh users on a minimal config? Current `EnvFileWithDelegation()` assumes the shell's own completion infrastructure is already up.
5. **macOS bash 3**: do we care enough to support `NoSpace` gracefully there, or document "upgrade bash"?
6. **Multi-token identifiers** (e.g., `workspace/instance`): would we implement these as a single completer with `NoSpace` + trailing `/`, or as separate positionals? Design choice for a later lead.

## Summary

Cobra v1.10.2 already gives niwa a complete dynamic-completion pipeline out of the box - the bash V2 and zsh scripts we emit in `shell_init.go` both call `niwa __complete ...` on every TAB, parse a `:<directive>` trailer, and dispatch directives identically; all we need to do is attach `ValidArgsFunction` and `RegisterFlagCompletionFunc` closures (plus `ShellCompDirectiveNoFileComp`) to each command that accepts a workspace/instance/repo name. The main consequence is that no shell-script authoring or regeneration is needed, but the engineering effort shifts to making the callbacks fast, since every keystroke-plus-TAB spawns a fresh `niwa` process that has to read registry and workspace state. The biggest open question is whether that cold-start latency is already imperceptible or whether we need a caching layer before rolling this out broadly.
