package workspace

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// ContentWarning represents a non-fatal issue found during content installation.
type ContentWarning struct {
	RepoName string
	Message  string
}

func (w ContentWarning) String() string {
	return fmt.Sprintf("repo %q: %s", w.RepoName, w.Message)
}

// InstallWorkspaceContent reads the workspace content source file, expands
// template variables, and writes it to {instanceRoot}/CLAUDE.md.
// Returns the list of files written.
func InstallWorkspaceContent(cfg *config.WorkspaceConfig, configDir, instanceRoot string) ([]string, error) {
	if cfg.Claude.Content.Workspace.Source == "" {
		return nil, nil
	}

	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving instance root: %w", err)
	}

	vars := map[string]string{
		"{workspace}":      absInstance,
		"{workspace_name}": cfg.Workspace.Name,
	}

	source := cfg.Claude.Content.Workspace.Source
	target := filepath.Join(instanceRoot, "CLAUDE.md")

	if err := installContentFile(cfg, configDir, source, target, vars); err != nil {
		return nil, err
	}
	return []string{target}, nil
}

// InstallGroupContent reads the group content source file, expands template
// variables, and writes it to {instanceRoot}/{groupName}/CLAUDE.md.
// Group directories are non-git directories, so they get CLAUDE.md (not .local).
// Returns the list of files written.
func InstallGroupContent(cfg *config.WorkspaceConfig, configDir, instanceRoot, groupName string) ([]string, error) {
	entry, ok := cfg.Claude.Content.Groups[groupName]
	if !ok || entry.Source == "" {
		return nil, nil
	}

	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving instance root: %w", err)
	}

	vars := map[string]string{
		"{workspace}":      absInstance,
		"{workspace_name}": cfg.Workspace.Name,
		"{group_name}":     groupName,
	}

	target := filepath.Join(instanceRoot, groupName, "CLAUDE.md")

	if err := installContentFile(cfg, configDir, entry.Source, target, vars); err != nil {
		return nil, err
	}
	return []string{target}, nil
}

// RepoContentResult holds the results of installing repo content.
type RepoContentResult struct {
	Warnings     []ContentWarning
	WrittenFiles []string
}

// InstallRepoContent reads the repo content source file, expands template
// variables, and writes it to {instanceRoot}/{groupName}/{repoName}/CLAUDE.local.md.
// Repo directories are git directories, so they get CLAUDE.local.md.
//
// If no explicit content entry exists for the repo, auto-discovery checks for
// {content_dir}/repos/{repoName}.md and uses it if found.
//
// Returns a result with content warnings and files written.
func InstallRepoContent(cfg *config.WorkspaceConfig, configDir, instanceRoot, groupName, repoName string) (*RepoContentResult, error) {
	result := &RepoContentResult{}

	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving instance root: %w", err)
	}

	vars := map[string]string{
		"{workspace}":      absInstance,
		"{workspace_name}": cfg.Workspace.Name,
		"{group_name}":     groupName,
		"{repo_name}":      repoName,
	}

	repoDir := filepath.Join(instanceRoot, groupName, repoName)

	// Resolve source: explicit entry or auto-discovery.
	entry, hasExplicit := cfg.Claude.Content.Repos[repoName]
	source := ""
	if hasExplicit {
		source = entry.Source
	} else {
		source = autoDiscoverRepoSource(cfg, configDir, repoName)
	}

	if source != "" {
		target := filepath.Join(repoDir, "CLAUDE.local.md")
		if err := installContentFile(cfg, configDir, source, target, vars); err != nil {
			return nil, err
		}
		result.WrittenFiles = append(result.WrittenFiles, target)

		w := CheckGitignore(repoDir, repoName)
		result.Warnings = append(result.Warnings, w...)
	}

	// Install subdirectory content if present.
	if hasExplicit {
		for subdir, subdirSource := range entry.Subdirs {
			if subdirSource == "" {
				continue
			}
			subdirPath := filepath.Join(repoDir, subdir)
			if err := checkContainment(subdirPath, repoDir); err != nil {
				return nil, fmt.Errorf("subdirectory %q for repo %q: %w", subdir, repoName, err)
			}
			target := filepath.Join(subdirPath, "CLAUDE.local.md")
			if err := installContentFile(cfg, configDir, subdirSource, target, vars); err != nil {
				return nil, err
			}
			result.WrittenFiles = append(result.WrittenFiles, target)
		}
	}

	return result, nil
}

// CheckGitignore checks if a repo directory's .gitignore contains a *.local*
// pattern. Returns a warning if the pattern is missing.
func CheckGitignore(repoDir, repoName string) []ContentWarning {
	gitignorePath := filepath.Join(repoDir, ".gitignore")
	f, err := os.Open(gitignorePath)
	if err != nil {
		// No .gitignore at all means the pattern is missing.
		return []ContentWarning{{
			RepoName: repoName,
			Message:  ".gitignore missing or unreadable; add *.local* pattern to keep CLAUDE.local.md out of version control",
		}}
	}
	defer f.Close()

	if hasLocalPattern(f) {
		return nil
	}

	return []ContentWarning{{
		RepoName: repoName,
		Message:  ".gitignore does not contain *.local* pattern; add it to keep CLAUDE.local.md out of version control",
	}}
}

// hasLocalPattern scans a reader for a line containing "*.local*".
func hasLocalPattern(r io.Reader) bool {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "*.local*" {
			return true
		}
	}
	return false
}

// autoDiscoverRepoSource checks for {content_dir}/repos/{repoName}.md
// and returns the relative source path if found, or empty string if not.
func autoDiscoverRepoSource(cfg *config.WorkspaceConfig, configDir, repoName string) string {
	contentDir := cfg.Workspace.ContentDir
	if contentDir == "" {
		return ""
	}

	candidate := filepath.Join("repos", repoName+".md")
	fullPath := filepath.Join(configDir, contentDir, candidate)

	if _, err := os.Stat(fullPath); err == nil {
		return candidate
	}

	return ""
}

// installContentFile reads a source file relative to content_dir, expands
// template variables, and writes the result to the target path.
// It verifies that the resolved source path stays within the content directory.
func installContentFile(cfg *config.WorkspaceConfig, configDir, source, target string, vars map[string]string) error {
	contentDir := cfg.Workspace.ContentDir
	if contentDir == "" {
		contentDir = "."
	}

	contentRoot := filepath.Join(configDir, contentDir)
	sourcePath := filepath.Join(contentRoot, source)

	if err := checkContainment(sourcePath, contentRoot); err != nil {
		return fmt.Errorf("content source %q: %w", source, err)
	}

	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("reading content source %s: %w", sourcePath, err)
	}

	content := expandVars(string(data), vars)

	targetDir := filepath.Dir(target)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("creating directory %s: %w", targetDir, err)
	}

	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", target, err)
	}

	return nil
}

// checkContainment verifies that targetPath resolves within parentDir.
// It uses filepath.EvalSymlinks on any existing prefix to detect symlink escapes,
// then checks that the resolved path has the parent as a prefix.
func checkContainment(targetPath, parentDir string) error {
	absParent, err := filepath.Abs(parentDir)
	if err != nil {
		return fmt.Errorf("resolving parent directory: %w", err)
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("resolving target path: %w", err)
	}

	// Resolve symlinks for the parent directory (it must exist).
	realParent, err := filepath.EvalSymlinks(absParent)
	if err != nil {
		// If parent doesn't exist, fall back to the cleaned abs path.
		realParent = absParent
	}

	// For the target, resolve symlinks on the longest existing prefix.
	realTarget := resolveExistingPrefix(absTarget)

	// Ensure the resolved target starts with the resolved parent directory.
	parentPrefix := realParent + string(filepath.Separator)
	if realTarget != realParent && !strings.HasPrefix(realTarget, parentPrefix) {
		return fmt.Errorf("path escapes its allowed directory %q", parentDir)
	}

	return nil
}

// resolveExistingPrefix walks the path from root to leaf, resolving symlinks
// for each component that exists. This handles the case where the full path
// doesn't yet exist but an intermediate symlink could redirect it.
func resolveExistingPrefix(p string) string {
	resolved, err := filepath.EvalSymlinks(p)
	if err == nil {
		return resolved
	}

	// Walk up until we find a path that exists, resolve that, then append the rest.
	dir := filepath.Dir(p)
	base := filepath.Base(p)
	if dir == p {
		// Root -- nothing more to resolve.
		return p
	}

	resolvedDir := resolveExistingPrefix(dir)
	return filepath.Join(resolvedDir, base)
}

// expandVars performs simple string replacement for template variables.
// Only the declared variables are expanded; no code execution.
// Uses strings.NewReplacer to avoid ordering issues when one key is a
// substring of another (e.g., "{workspace}" vs "{workspace_name}").
func expandVars(s string, vars map[string]string) string {
	oldnew := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		oldnew = append(oldnew, k, v)
	}
	return strings.NewReplacer(oldnew...).Replace(s)
}
