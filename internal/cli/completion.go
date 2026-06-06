package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
	"github.com/tsukumogami/niwa/internal/worktree"
)

// completeWorkspaceNames is the completion closure for any position that
// accepts a registered workspace name: the positional arg of `niwa apply`,
// `niwa create`, `niwa init`, and the value of `niwa go -w`.
//
// Errors are swallowed (see Implicit Decision C in the design): any failure
// collapses to an empty candidate list so the shell doesn't surface a
// completion banner during transient filesystem / config problems.
func completeWorkspaceNames(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	names, err := config.ListRegisteredWorkspaces()
	if err != nil || len(names) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return filterPrefix(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeInstanceNames is the completion closure for positions that accept
// an instance name within the current workspace: positional args of
// `niwa destroy`, `niwa reset`, `niwa status`, and the value of
// `niwa apply --instance`. cwd must be inside a workspace for results to
// appear; otherwise the closure returns an empty list.
func completeInstanceNames(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	_, configDir, err := config.Discover(cwd)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	workspaceRoot := filepath.Dir(configDir)
	instances, err := workspace.EnumerateInstances(workspaceRoot)
	if err != nil || len(instances) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	names := make([]string, 0, len(instances))
	for _, dir := range instances {
		state, err := workspace.LoadState(dir)
		if err != nil || state == nil {
			continue
		}
		if !workspace.ValidName(state.InstanceName) {
			continue
		}
		names = append(names, state.InstanceName)
	}
	sort.Strings(names)
	return filterPrefix(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeRepoNames is the completion closure for positions that accept a
// repo name. When the command has `-w <workspace>` set, the closure scopes
// to the sorted-first instance of that workspace (mirroring the runtime
// behavior in resolveWorkspaceRepo). Otherwise it scopes to the instance
// containing cwd.
func completeRepoNames(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	instanceRoot, ok := resolveCompletionInstanceRoot(cmd)
	if !ok {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names, err := workspace.EnumerateRepos(instanceRoot)
	if err != nil || len(names) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return filterPrefix(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeSessionIDs is the completion closure for positions that accept a
// session ID: the positional arg of `niwa session destroy`,
// `niwa session attach`, and `niwa session detach`. It enumerates session
// lifecycle state files under <instanceRoot>/.niwa/sessions/ via
// worktree.ListSessionLifecycleStates.
//
// Instance resolution matches the runtime path used by the same commands
// (resolveInstanceRoot): NIWA_INSTANCE_ROOT wins if set, otherwise we walk
// up from cwd. Diverging here would create a discoverability trap where
// tab completion returns empty even though the command itself runs fine.
//
// Errors are swallowed to match the convention used by the other
// completers in this file: a transient failure should not surface a
// completion banner.
func completeSessionIDs(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	instanceRoot, err := resolveInstanceRoot()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	states, err := worktree.ListSessionLifecycleStates(sessionsDir)
	if err != nil || len(states) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	ids := make([]string, 0, len(states))
	for _, s := range states {
		ids = append(ids, s.SessionID)
	}
	sort.Strings(ids)
	return filterPrefix(ids, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeGoTarget is the specialized closure for `niwa go [target]`. It
// decorates candidates with kind so the user can visually distinguish a
// repo from a workspace when names collide: `<name>\trepo in <N>` for repos
// in the current instance, `<name>\tworkspace` for registered workspaces.
// Collisions produce two entries, one per kind.
func completeGoTarget(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var out []cobra.Completion

	// Repos in the current instance (if any).
	cwd, err := os.Getwd()
	if err == nil {
		if instanceRoot, err := workspace.DiscoverInstance(cwd); err == nil {
			if repos, err := workspace.EnumerateRepos(instanceRoot); err == nil {
				state, _ := workspace.LoadState(instanceRoot)
				instanceNum := 0
				if state != nil {
					instanceNum = state.InstanceNumber
				}
				desc := "repo"
				if instanceNum > 0 {
					desc = fmt.Sprintf("repo in %d", instanceNum)
				}
				for _, r := range repos {
					if strings.HasPrefix(r, toComplete) {
						out = append(out, cobra.CompletionWithDesc(r, desc))
					}
				}
			}
		}
	}

	// Registered workspaces.
	if names, err := config.ListRegisteredWorkspaces(); err == nil {
		for _, n := range names {
			if strings.HasPrefix(n, toComplete) {
				out = append(out, cobra.CompletionWithDesc(n, "workspace"))
			}
		}
	}

	if len(out) == 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

// resolveCompletionInstanceRoot returns the instance root that `niwa go -r`
// completion should enumerate against. When -w is set on cmd, it mirrors
// runtime by picking the sorted-first instance of the named workspace.
// Otherwise it falls back to the instance containing cwd.
func resolveCompletionInstanceRoot(cmd *cobra.Command) (string, bool) {
	if cmd != nil {
		if wsFlag := cmd.Flag("workspace"); wsFlag != nil && wsFlag.Value.String() != "" {
			wsName := wsFlag.Value.String()
			globalCfg, err := config.LoadGlobalConfig()
			if err != nil {
				return "", false
			}
			entry := globalCfg.LookupWorkspace(wsName)
			if entry == nil {
				return "", false
			}
			instances, err := workspace.EnumerateInstances(entry.Root)
			if err != nil || len(instances) == 0 {
				return "", false
			}
			sort.Strings(instances)
			return instances[0], true
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	instanceRoot, err := workspace.DiscoverInstance(cwd)
	if err != nil {
		return "", false
	}
	return instanceRoot, true
}

// filterPrefix returns the subset of names that begin with prefix, converted
// to []cobra.Completion (alias of []string).
func filterPrefix(names []string, prefix string) []cobra.Completion {
	out := make([]cobra.Completion, 0, len(names))
	for _, n := range names {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	return out
}
