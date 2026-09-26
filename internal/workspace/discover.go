package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// DiscoverHooks scans configDir/hooks/ for hook scripts and returns a
// HooksConfig mapping event names to script paths.
//
// Two layout styles are supported:
//   - hooks/{event}.sh       -> maps to event name (extension stripped)
//   - hooks/{event}/*.sh     -> each file maps to that event
//
// Non-.sh files are ignored. A missing hooks/ directory returns an empty
// HooksConfig without error.
func DiscoverHooks(configDir string) (config.HooksConfig, error) {
	hooksDir := filepath.Join(configDir, "hooks")

	if err := validateWithinDir(configDir, hooksDir); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(hooksDir)
	if os.IsNotExist(err) {
		return config.HooksConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading hooks directory: %w", err)
	}

	hooks := config.HooksConfig{}

	for _, entry := range entries {
		entryPath := filepath.Join(hooksDir, entry.Name())

		if entry.IsDir() {
			// Subdirectory: each .sh file inside maps to the directory name as event.
			event := entry.Name()
			subEntries, err := os.ReadDir(entryPath)
			if err != nil {
				return nil, fmt.Errorf("reading hooks subdirectory %q: %w", event, err)
			}
			for _, sub := range subEntries {
				if sub.IsDir() || !strings.HasSuffix(sub.Name(), ".sh") {
					continue
				}
				scriptPath := filepath.Join(entryPath, sub.Name())
				if err := validateWithinDir(configDir, scriptPath); err != nil {
					return nil, err
				}
				hooks[event] = append(hooks[event], config.HookEntry{Scripts: []string{scriptPath}})
			}
		} else if strings.HasSuffix(entry.Name(), ".sh") {
			// Top-level .sh file: event name is filename without extension.
			event := strings.TrimSuffix(entry.Name(), ".sh")
			if err := validateWithinDir(configDir, entryPath); err != nil {
				return nil, err
			}
			hooks[event] = append(hooks[event], config.HookEntry{Scripts: []string{entryPath}})
		}
	}

	return hooks, nil
}

// DiscoverWorktreeHooks scans configDir/worktree-hooks/ for worktree-event
// hook scripts and returns a HooksConfig mapping event names to script paths.
// It is the worktree-lifecycle analog of DiscoverHooks: where DiscoverHooks
// feeds Claude Code lifecycle hooks into a repo's settings, these scripts are
// executed by niwa itself when ApplyToWorktree runs (on `niwa worktree
// create`/`apply`).
//
// The same two layout styles DiscoverHooks supports are accepted:
//   - worktree-hooks/{event}.sh   -> maps to event name (extension stripped)
//   - worktree-hooks/{event}/*.sh -> each file maps to that event
//
// Non-.sh files are ignored. A missing worktree-hooks/ directory returns an
// empty HooksConfig without error.
//
// Script paths go through validateWithinDir, which is a LEXICAL containment
// check against ".." traversal -- see its own comment. It does not resolve
// symlinks, so it does not detect a script symlinked outside configDir, and
// every path it guards here is filepath.Joined from a bare os.ReadDir entry
// name, so in this function it cannot fail. An earlier version of this comment
// claimed scripts were "validated to stay within configDir (no symlink
// escape)"; that sentence was used to justify a security argument it could not
// support. Whether these paths should resolve symlinks is niwa#290.
//
// Event names are validated against worktreeHookEvents. A hook registered under
// a name niwa does not consume is reported through an error wrapping
// ErrUnknownWorktreeHookEvent -- and the hooks that ARE valid come back
// alongside it. That pairing is deliberate and load-bearing: every other error
// path here returns a nil map, so reporting an unknown event the same way would
// let one stale worktree-hooks/create/ directory silently disable a live
// worktree-hooks/apply/ one. A configuration that works today would stop
// working, which is the exact silent-provisioning failure this validation was
// added to remove.
//
// A containment or directory-read failure returns immediately with that error
// ALONE, never joined with unknown-event diagnostics collected earlier in the
// same walk. errors.Is matches a sentinel anywhere inside a joined error, so a
// combined return would let a containment failure ride inside what the caller
// treats as the non-fatal case and be swallowed.
func DiscoverWorktreeHooks(configDir string) (config.HooksConfig, error) {
	hooksDir := filepath.Join(configDir, "worktree-hooks")

	if err := validateWithinDir(configDir, hooksDir); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(hooksDir)
	if os.IsNotExist(err) {
		return config.HooksConfig{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading worktree-hooks directory: %w", err)
	}

	hooks := config.HooksConfig{}
	var unknown []string

	for _, entry := range entries {
		entryPath := filepath.Join(hooksDir, entry.Name())

		if entry.IsDir() {
			event := entry.Name()
			if !isKnownWorktreeHookEvent(event) {
				unknown = append(unknown, entryPath+string(filepath.Separator))
				continue
			}
			subEntries, err := os.ReadDir(entryPath)
			if err != nil {
				return nil, fmt.Errorf("reading worktree-hooks subdirectory %q: %w", event, err)
			}
			for _, sub := range subEntries {
				if sub.IsDir() || !strings.HasSuffix(sub.Name(), ".sh") {
					continue
				}
				scriptPath := filepath.Join(entryPath, sub.Name())
				if err := validateWithinDir(configDir, scriptPath); err != nil {
					return nil, err
				}
				hooks[event] = append(hooks[event], config.HookEntry{Scripts: []string{scriptPath}})
			}
		} else if strings.HasSuffix(entry.Name(), ".sh") {
			event := strings.TrimSuffix(entry.Name(), ".sh")
			if !isKnownWorktreeHookEvent(event) {
				unknown = append(unknown, entryPath)
				continue
			}
			if err := validateWithinDir(configDir, entryPath); err != nil {
				return nil, err
			}
			hooks[event] = append(hooks[event], config.HookEntry{Scripts: []string{entryPath}})
		}
	}

	if len(unknown) > 0 {
		sort.Strings(unknown)
		return hooks, fmt.Errorf("%w: %s (niwa consumes only: %s)",
			ErrUnknownWorktreeHookEvent,
			strings.Join(unknown, ", "),
			strings.Join(worktreeHookEvents, ", "))
	}

	return hooks, nil
}

// DiscoverEnvFiles scans configDir/env/ for environment files.
//
// It looks for:
//   - env/workspace.env          -> returned as workspaceFile (empty string if absent)
//   - env/repos/{repoName}.env   -> returned in repoFiles map (name without .env -> path)
//
// Missing env/ or env/repos/ directories return empty results without error.
func DiscoverEnvFiles(configDir string) (workspaceFile string, repoFiles map[string]string, err error) {
	envDir := filepath.Join(configDir, "env")

	if err := validateWithinDir(configDir, envDir); err != nil {
		return "", nil, err
	}

	repoFiles = make(map[string]string)

	// Check for workspace.env.
	wsPath := filepath.Join(envDir, "workspace.env")
	if err := validateWithinDir(configDir, wsPath); err != nil {
		return "", nil, err
	}
	if _, err := os.Stat(wsPath); err == nil {
		workspaceFile = wsPath
	} else if !os.IsNotExist(err) {
		return "", nil, fmt.Errorf("checking workspace.env: %w", err)
	}

	// Scan env/repos/.
	reposDir := filepath.Join(envDir, "repos")
	if err := validateWithinDir(configDir, reposDir); err != nil {
		return "", nil, err
	}

	entries, err := os.ReadDir(reposDir)
	if os.IsNotExist(err) {
		return workspaceFile, repoFiles, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("reading env/repos directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".env") {
			continue
		}
		repoName := strings.TrimSuffix(entry.Name(), ".env")
		filePath := filepath.Join(reposDir, entry.Name())
		if err := validateWithinDir(configDir, filePath); err != nil {
			return "", nil, err
		}
		repoFiles[repoName] = filePath
	}

	return workspaceFile, repoFiles, nil
}

// validateWithinDir reports whether targetPath is lexically inside baseDir.
//
// It makes both paths absolute, cleans them, and compares prefixes. It does NOT
// resolve symlinks -- filepath.Clean removes ".." textually and never touches
// the filesystem -- so a symlink inside baseDir pointing outside it passes.
// The check this actually provides is against ".." traversal in a
// caller-supplied path.
//
// This comment previously claimed the containment held "after symlink
// resolution", and that sentence was read by three reviewers in sequence and
// restated as a symlink-escape control in a design document, a requirements
// document and an acceptance criterion. Nobody read the body. So it is worded
// here as what the function does rather than what it guards against, because
// the next reader will verify against this comment and stop at the same depth.
//
// Note also that every current call site passes either
// filepath.Join(dir, "<literal>") or filepath.Join(dir, entry.Name()) from an
// os.ReadDir walk, and neither can produce a separator or a "..", so no call
// site can currently fail. The calls are a defensive floor against a future
// caller that passes something less constrained. Whether these paths should
// resolve symlinks at all is niwa#290.
func validateWithinDir(baseDir, targetPath string) error {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("resolving base directory: %w", err)
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("resolving target path: %w", err)
	}

	// Clean both paths to normalize.
	cleanBase := filepath.Clean(absBase)
	cleanTarget := filepath.Clean(absTarget)

	// Target must be under base (or equal to base).
	if cleanTarget != cleanBase && !strings.HasPrefix(cleanTarget, cleanBase+string(filepath.Separator)) {
		return fmt.Errorf("path %q escapes config directory %q", targetPath, baseDir)
	}

	return nil
}
