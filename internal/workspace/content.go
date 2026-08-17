package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

// materializedAgents lists the agents every apply prepares context for at the
// niwa-owned levels the existing writers cover: the workspace root and the
// instance root. Preparation is unconditional (PRD R1) -- the writers run once
// per agent, so CLAUDE.md and AGENTS.md land side by side whatever
// `default_agent` says. That setting now selects only which agent a
// niwa-launched session runs, not what preparation produces.
// See DESIGN-dual-agent-workspace.md Decision 7A.
var materializedAgents = []agent.Agent{agent.AgentClaude, agent.AgentCodex}

// ContentWarning represents a non-fatal issue found during content installation.
type ContentWarning struct {
	RepoName string
	Message  string
}

func (w ContentWarning) String() string {
	return fmt.Sprintf("repo %q: %s", w.RepoName, w.Message)
}

// InstallWorkspaceContent reads the workspace content source file, expands
// template variables, and writes it to {instanceRoot}/{agent context file}.
// The output filename is chosen by ag (CLAUDE.md for Claude, AGENTS.md for
// Codex); the content source is unchanged by agent.
//
// The apply pipeline calls this once per agent in materializedAgents, so an
// instance root carries both names after every apply. ag names which file this
// call writes, not which agent the instance is for.
// Returns the list of files written.
func InstallWorkspaceContent(cfg *config.WorkspaceConfig, configDir, instanceRoot string, ag agent.Agent) ([]string, error) {
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
	target := filepath.Join(instanceRoot, ag.RootContextFileName())

	if err := installContentFile(contentDirRoot(cfg, configDir), source, target, vars); err != nil {
		return nil, err
	}
	return []string{target}, nil
}

// InstallGroupContent reads the group content source file, expands template
// variables, and writes it to {instanceRoot}/{groupName}/CLAUDE.md.
// Group directories are non-git directories, so they get CLAUDE.md (not .local).
// Returns the list of files written.
func InstallGroupContent(cfg *config.WorkspaceConfig, configDir, instanceRoot, groupName string, ag agent.Agent) ([]string, error) {
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

	target := filepath.Join(instanceRoot, groupName, ag.RootContextFileName())

	// Overlay-added groups have OverlayDir set; source is resolved directly from
	// that directory (not from configDir/contentDir) because the overlay has its
	// own file layout independent of the workspace content_dir.
	contentRoot := contentDirRoot(cfg, configDir)
	if entry.OverlayDir != "" {
		contentRoot = entry.OverlayDir
	}

	if err := installContentFile(contentRoot, entry.Source, target, vars); err != nil {
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
// When the repo has an OverlaySource set in its content entry, the overlay
// content is appended to CLAUDE.local.md (separated by a blank line). In that
// case overlayDir must be non-empty; if overlayDir is empty and OverlaySource
// is set, an error is returned.
//
// Returns a result with content warnings and files written.
func InstallRepoContent(cfg *config.WorkspaceConfig, configDir, overlayDir, instanceRoot, groupName, repoName string, ag agent.Agent) (*RepoContentResult, error) {
	repoDir := filepath.Join(instanceRoot, groupName, repoName)
	return InstallRepoContentTo(cfg, configDir, overlayDir, instanceRoot, repoDir, groupName, repoName, ag)
}

// InstallRepoContentTo is the target-directory-parameterized form of
// InstallRepoContent. It installs the repo's CLAUDE.local.md (and subdir
// content) into repoDir, while still resolving the {workspace} template
// variable from instanceRoot. The instance apply path calls this with
// repoDir = {instanceRoot}/{group}/{repo}; ApplyToWorktree calls it with the
// worktree path so a worktree gets the same content a repo checkout does. Both
// callers share this single function (no forked installer).
//
// This runs unconditionally on the Claude pass regardless of the workspace's
// selected agent (Decision 7A): the Claude tree is materialized in full at
// every level no matter what default_agent says. Codex's repository-level
// context is a separate, net-new composition (AGENTS.override.md) written by
// the Codex materializer, not through this installer.
func InstallRepoContentTo(cfg *config.WorkspaceConfig, configDir, overlayDir, instanceRoot, repoDir, groupName, repoName string, ag agent.Agent) (*RepoContentResult, error) {
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

	// Source resolution (explicit entry, else auto-discovery) and the overlay
	// append live in renderRepoContextLayer, which the Codex composer reads the
	// repository layer from as well: one resolution, so the two agents cannot
	// end up building a repository's context from different sources.
	layer, err := renderRepoContextLayer(cfg, configDir, overlayDir, instanceRoot, groupName, repoName)
	if err != nil {
		return nil, err
	}
	if layer.Configured {
		target := filepath.Join(repoDir, "CLAUDE.local.md")
		if err := writeContextFile(target, layer.Content); err != nil {
			return nil, err
		}
		result.WrittenFiles = append(result.WrittenFiles, target)
	}

	// Install subdirectory content if present.
	entry, hasExplicit := cfg.Claude.Content.Repos[repoName]
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
			if err := installContentFile(contentDirRoot(cfg, configDir), subdirSource, target, vars); err != nil {
				return nil, err
			}
			result.WrittenFiles = append(result.WrittenFiles, target)
		}
	}

	return result, nil
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

// contentDirRoot returns the absolute content directory for a workspace config
// and configDir. This is the directory that source= paths are resolved against
// for base-config content entries.
func contentDirRoot(cfg *config.WorkspaceConfig, configDir string) string {
	contentDir := cfg.Workspace.ContentDir
	if contentDir == "" {
		contentDir = "."
	}
	return filepath.Join(configDir, contentDir)
}

// renderContentFile reads a source file relative to contentRoot, verifies the
// resolved source path stays within contentRoot, and returns the content with
// template variables expanded. It performs no write — callers that need to
// persist the result write the returned string themselves. This is the shared
// read+containment+expand core used by both installContentFile (write-to-file)
// and the worktree layer (render-to-string), so neither path can drift on the
// containment guarantee.
func renderContentFile(contentRoot, source string, vars map[string]string) (string, error) {
	sourcePath := filepath.Join(contentRoot, source)

	if err := checkContainment(sourcePath, contentRoot); err != nil {
		return "", fmt.Errorf("content source %q: %w", source, err)
	}

	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("reading content source %s: %w", sourcePath, err)
	}

	return expandVars(string(data), vars), nil
}

// installContentFile reads a source file relative to contentRoot, expands
// template variables, and writes the result to the target path.
// It verifies that the resolved source path stays within contentRoot.
func installContentFile(contentRoot, source, target string, vars map[string]string) error {
	content, err := renderContentFile(contentRoot, source, vars)
	if err != nil {
		return err
	}

	return writeContextFile(target, content)
}

// writeContextFile writes one context document, creating the parent directory
// if it is missing. Every apply rewrites these files from the current sources
// rather than appending to them, so nothing accumulates across applies.
func writeContextFile(target, content string) error {
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
