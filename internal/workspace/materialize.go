package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/config"
)

// MaterializeContext holds the state needed by materializers when installing
// configuration into a repository directory.
type MaterializeContext struct {
	Config         *config.WorkspaceConfig
	Effective      EffectiveConfig
	RepoName       string
	RepoDir        string
	ConfigDir      string
	InstalledHooks map[string][]string // event -> installed script paths, populated by hooks materializer
}

// Materializer is the interface for components that install workspace
// configuration artifacts into a repository directory.
type Materializer interface {
	Name() string
	Materialize(ctx *MaterializeContext) ([]string, error)
}

// HooksMaterializer installs hook scripts from the config directory into a
// repository's .claude/hooks/ directory structure. It reads the merged hooks
// from EffectiveConfig and copies each script file to the target location.
type HooksMaterializer struct{}

// Name returns the materializer identifier.
func (h *HooksMaterializer) Name() string {
	return "hooks"
}

// Materialize copies hook scripts into {repoDir}/.claude/hooks/{event}/ and
// sets them executable. It populates ctx.InstalledHooks with the mapping of
// event names to installed script paths and returns the list of all written
// file paths.
func (h *HooksMaterializer) Materialize(ctx *MaterializeContext) ([]string, error) {
	hooks := ctx.Effective.Claude.Hooks
	if len(hooks) == 0 {
		return nil, nil
	}

	installed := make(map[string][]string, len(hooks))
	var written []string

	for event, scripts := range hooks {
		for _, scriptPath := range scripts {
			src := filepath.Join(ctx.ConfigDir, scriptPath)

			if err := checkContainment(src, ctx.ConfigDir); err != nil {
				return nil, fmt.Errorf("hook script %q: %w", scriptPath, err)
			}

			targetDir := filepath.Join(ctx.RepoDir, ".claude", "hooks", event)
			target := filepath.Join(targetDir, filepath.Base(scriptPath))

			if err := os.MkdirAll(targetDir, 0o755); err != nil {
				return nil, fmt.Errorf("creating hooks directory %s: %w", targetDir, err)
			}

			data, err := os.ReadFile(src)
			if err != nil {
				return nil, fmt.Errorf("reading hook script %s: %w", src, err)
			}

			if err := os.WriteFile(target, data, 0o644); err != nil {
				return nil, fmt.Errorf("writing hook script %s: %w", target, err)
			}

			if err := os.Chmod(target, 0o755); err != nil {
				return nil, fmt.Errorf("setting executable permission on %s: %w", target, err)
			}

			installed[event] = append(installed[event], target)
			written = append(written, target)
		}
	}

	ctx.InstalledHooks = installed
	return written, nil
}
