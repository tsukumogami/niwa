package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/tsukumogami/niwa/internal/config"
)

const defaultSetupDir = "scripts/setup"

// ScriptResult records the outcome of running a single setup script.
type ScriptResult struct {
	Name  string
	Error error // nil = success
}

// SetupResult records the outcome of running setup scripts for one repo.
type SetupResult struct {
	RepoName string
	Scripts  []ScriptResult
	Skipped  bool // directory not found
	Disabled bool // explicitly disabled via empty string
}

// ResolveSetupDir returns the effective setup directory for a repo. The
// resolution order is: repo override -> workspace default -> "scripts/setup".
// Returns empty string when explicitly disabled (repo override set to "").
func ResolveSetupDir(ws *config.WorkspaceConfig, repoName string) string {
	if override, ok := ws.Repos[repoName]; ok && override.SetupDir != nil {
		return *override.SetupDir
	}
	if ws.Workspace.SetupDir != "" {
		return ws.Workspace.SetupDir
	}
	return defaultSetupDir
}

// RunSetupScripts scans setupDir within repoDir for executable scripts and
// runs them in lexical order. Stops on the first script that exits non-zero.
// Returns nil if the directory doesn't exist or is empty.
// r receives all script output; pass a non-nil *Reporter.
func RunSetupScripts(repoDir, setupDir string, r *Reporter) *SetupResult {
	result := &SetupResult{RepoName: filepath.Base(repoDir)}

	if setupDir == "" {
		result.Disabled = true
		return result
	}

	dir := filepath.Join(repoDir, setupDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			result.Skipped = true
			return result
		}
		result.Scripts = append(result.Scripts, ScriptResult{
			Name:  setupDir,
			Error: fmt.Errorf("reading setup directory: %w", err),
		})
		return result
	}

	// Collect executable files in lexical order.
	var scripts []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		scripts = append(scripts, entry.Name())
	}
	sort.Strings(scripts)

	if len(scripts) == 0 {
		result.Skipped = true
		return result
	}

	for _, name := range scripts {
		scriptPath := filepath.Join(dir, name)

		info, err := os.Stat(scriptPath)
		if err != nil {
			result.Scripts = append(result.Scripts, ScriptResult{
				Name:  name,
				Error: fmt.Errorf("stat: %w", err),
			})
			break
		}

		// Check executable bit.
		if info.Mode()&0o111 == 0 {
			result.Scripts = append(result.Scripts, ScriptResult{
				Name:  name,
				Error: fmt.Errorf("not executable (chmod +x to enable)"),
			})
			continue // warn and skip, don't stop
		}

		cmd := exec.Command(scriptPath)
		cmd.Dir = repoDir

		if err := runCmdWithReporter(r, cmd); err != nil {
			result.Scripts = append(result.Scripts, ScriptResult{
				Name:  name,
				Error: err,
			})
			break // stop remaining scripts for this repo
		}

		result.Scripts = append(result.Scripts, ScriptResult{Name: name})
	}

	return result
}
