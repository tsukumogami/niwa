package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
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
// template variables, and installs it at the instance root for one agent.
//
// The producer decides both the filename and whether the document is written at
// all. An agent that finds context by walking up from its working directory
// reads it; an agent that finds a project root first and reads downward from
// there never would, and its declaration says so, so nothing is written for it
// rather than a file nothing reads. Returns the list of files written.
func InstallWorkspaceContent(cfg *config.WorkspaceConfig, configDir, instanceRoot string, ag agent.Agent) ([]string, error) {
	body, ok, err := renderWorkspaceLayer(cfg, configDir, instanceRoot)
	if err != nil {
		return nil, err
	}

	plan, err := agentplan.For(ag).RootContextPlan(agentplan.RootContextInputs{
		Dir:     instanceRoot,
		Body:    []byte(body),
		HasBody: ok,
	})
	if err != nil {
		return nil, err
	}
	written, _, err := applyPlan(plan)
	return written, err
}

// InstallGroupContent reads the group content source file, expands template
// variables, and installs it in the group directory for one agent.
//
// Group directories are not repositories, so they get the agent's plain context
// filename. As with the instance root, the producer decides whether there is
// anything to write: an agent whose discovery never visits a group directory
// receives the group layer inside each repository's own document instead.
// Returns the list of files written.
func InstallGroupContent(cfg *config.WorkspaceConfig, configDir, instanceRoot, groupName string, ag agent.Agent) ([]string, error) {
	body, ok, err := renderGroupLayer(cfg, configDir, instanceRoot, groupName)
	if err != nil {
		return nil, err
	}

	plan, err := agentplan.For(ag).GroupContextPlan(agentplan.RootContextInputs{
		Dir:     filepath.Join(instanceRoot, groupName),
		Body:    []byte(body),
		HasBody: ok,
	})
	if err != nil {
		return nil, err
	}
	written, _, err := applyPlan(plan)
	return written, err
}

// renderWorkspaceLayer renders the instance-root content entry without writing
// it, reporting whether the configuration declares one at all. It is the single
// resolution of that source: the instance-root document is written from it, and
// so is the outermost layer of every composed repository document, so the two
// cannot drift on which file the workspace layer comes from.
func renderWorkspaceLayer(cfg *config.WorkspaceConfig, configDir, instanceRoot string) (string, bool, error) {
	source := cfg.Claude.Content.Workspace.Source
	if source == "" {
		return "", false, nil
	}

	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return "", false, fmt.Errorf("resolving instance root: %w", err)
	}

	body, err := renderContentFile(contentDirRoot(cfg, configDir), source, map[string]string{
		"{workspace}":      absInstance,
		"{workspace_name}": cfg.Workspace.Name,
	})
	if err != nil {
		return "", false, err
	}
	return body, true, nil
}

// renderGroupLayer renders a group's content entry without writing it. An
// overlay-added group resolves its source from the overlay directory rather
// than from the workspace content_dir, because the overlay carries its own file
// layout.
func renderGroupLayer(cfg *config.WorkspaceConfig, configDir, instanceRoot, groupName string) (string, bool, error) {
	entry, ok := cfg.Claude.Content.Groups[groupName]
	if !ok || entry.Source == "" {
		return "", false, nil
	}

	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return "", false, fmt.Errorf("resolving instance root: %w", err)
	}

	contentRoot := contentDirRoot(cfg, configDir)
	if entry.OverlayDir != "" {
		contentRoot = entry.OverlayDir
	}

	body, err := renderContentFile(contentRoot, entry.Source, map[string]string{
		"{workspace}":      absInstance,
		"{workspace_name}": cfg.Workspace.Name,
		"{group_name}":     groupName,
	})
	if err != nil {
		return "", false, err
	}
	return body, true, nil
}

// RepoContentResult holds the results of installing repo content.
type RepoContentResult struct {
	Warnings     []ContentWarning
	WrittenFiles []string
	// Exempt lists paths the install refused to write because the repository
	// commits its own file at one of niwa's names. The managed-file cleanup
	// consults it so a refusal is not undone by the deletion pass that follows.
	Exempt []string
	// Excludes lists the git-exclude patterns the written documents imply,
	// relative to the working tree they landed in. They travel out with the
	// write rather than being restated by the caller, so a document niwa adds
	// cannot arrive without the coverage that keeps the tree clean.
	Excludes []string
}

// InstallRepoContent reads the repo content source file, expands template
// variables, and installs the repository's context document for one agent, in
// the repository's checkout under the instance root.
//
// If no explicit content entry exists for the repo, auto-discovery checks for
// {content_dir}/repos/{repoName}.md and uses it if found.
//
// When the repo has an OverlaySource set in its content entry, the overlay
// content is appended to the document (separated by a blank line). In that case
// overlayDir must be non-empty; if overlayDir is empty and OverlaySource is set,
// an error is returned.
//
// Returns a result with content warnings, files written, refused paths, and the
// git-exclude patterns the writes imply.
func InstallRepoContent(cfg *config.WorkspaceConfig, configDir, overlayDir, instanceRoot, groupName, repoName string, ag agent.Agent) (*RepoContentResult, error) {
	repoDir := filepath.Join(instanceRoot, groupName, repoName)
	return InstallRepoContentTo(cfg, configDir, overlayDir, instanceRoot, repoDir, groupName, repoName, agentplan.For(ag))
}

// InstallRepoContentTo is the target-directory-parameterized form of
// InstallRepoContent. It installs the repo's context document (and subdir
// content) into repoDir, while still resolving the {workspace} template
// variable from instanceRoot. The instance apply path calls this with
// repoDir = {instanceRoot}/{group}/{repo}; ApplyToWorktree calls it with the
// worktree path so a worktree gets the same content a repo checkout does. Both
// callers share this single function (no forked installer).
//
// The producer decides the filename, the composition, and whether there is
// anything to write at all. This function resolves sources, renders bodies,
// runs whatever probes the producer asked for, and executes the resulting plan;
// it never learns which agent it is installing for, which is what keeps the
// difference between "one document per level" and "the whole chain composed
// into the repository's own document" inside the producer where it can be
// reviewed as one decision.
func InstallRepoContentTo(cfg *config.WorkspaceConfig, configDir, overlayDir, instanceRoot, repoDir, groupName, repoName string, producer agentplan.Producer) (*RepoContentResult, error) {
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

	// Resolve source: explicit entry or auto-discovery.
	entry, hasExplicit := cfg.Claude.Content.Repos[repoName]
	source := ""
	if hasExplicit {
		source = entry.Source
	} else {
		source = autoDiscoverRepoSource(cfg, configDir, repoName)
	}

	// Everything below resolves content; nothing writes. The rendered bodies
	// become plan entries and the executor puts them on disk, so the decision
	// about which file each body lands in stays with the producer.
	in := agentplan.RepoContextInputs{Dir: repoDir}

	if source != "" {
		body, err := renderContentFile(contentDirRoot(cfg, configDir), source, vars)
		if err != nil {
			return nil, err
		}
		in.Body, in.HasBody = []byte(body), true

		// Append overlay content if present.
		if hasExplicit && entry.OverlaySource != "" {
			overlayData, err := readRepoOverlaySource(overlayDir, repoName, entry.OverlaySource)
			if err != nil {
				return nil, err
			}
			in.Overlay, in.HasOverlay = overlayData, true
		}
	} else if hasExplicit && entry.OverlaySource != "" {
		// No base source, but OverlaySource is set — the overlay content is the
		// whole document.
		overlayData, err := readRepoOverlaySource(overlayDir, repoName, entry.OverlaySource)
		if err != nil {
			return nil, err
		}
		in.Overlay, in.HasOverlay = overlayData, true
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
			body, err := renderContentFile(contentDirRoot(cfg, configDir), subdirSource, vars)
			if err != nil {
				return nil, err
			}
			in.Subdirs = append(in.Subdirs, agentplan.SubdirContext{Dir: subdirPath, Body: []byte(body)})
		}
	}

	// The outer layers, for a producer that folds them into this document. One
	// whose discovery reaches them where they are written ignores them.
	if wsBody, ok, err := renderWorkspaceLayer(cfg, configDir, instanceRoot); err != nil {
		return nil, err
	} else if ok {
		in.Workspace = []byte(wsBody)
	}
	if grpBody, ok, err := renderGroupLayer(cfg, configDir, instanceRoot, groupName); err != nil {
		return nil, err
	} else if ok {
		in.Group = []byte(grpBody)
	}

	// Probes: the producer names the paths whose current state its plan depends
	// on, this side looks, and the answers go back in as data.
	if in.Probe, err = probeContextTree(producer.ContextProbeSpec(repoDir)); err != nil {
		return nil, err
	}
	for i := range in.Subdirs {
		if in.Subdirs[i].Probe, err = probeContextTree(producer.ContextProbeSpec(in.Subdirs[i].Dir)); err != nil {
			return nil, err
		}
	}

	plan, err := producer.RepoContextPlan(in)
	if err != nil {
		return nil, err
	}
	written, excludes, err := applyPlan(plan)
	if err != nil {
		return nil, err
	}
	result.WrittenFiles = append(result.WrittenFiles, written...)
	result.Exempt = append(result.Exempt, plan.Exempt...)
	result.Excludes = append(result.Excludes, excludes...)
	for _, w := range plan.Warnings {
		result.Warnings = append(result.Warnings, ContentWarning{RepoName: repoName, Message: w})
	}

	return result, nil
}

// readRepoOverlaySource reads a repo's overlay addendum from the overlay clone.
// An OverlaySource with no overlay directory behind it is a configuration error
// rather than a missing file: the entry names content that cannot be resolved,
// and silently dropping it would ship a repo the private half of its context.
func readRepoOverlaySource(overlayDir, repoName, overlaySource string) ([]byte, error) {
	if overlayDir == "" {
		return nil, fmt.Errorf("repo %q has OverlaySource %q but overlayDir is empty", repoName, overlaySource)
	}
	data, err := os.ReadFile(filepath.Join(overlayDir, overlaySource))
	if err != nil {
		return nil, fmt.Errorf("reading overlay content for repo %q: %w", repoName, err)
	}
	return data, nil
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
// read+containment+expand core every content path shares -- the instance-root,
// group, repository, and worktree layers all resolve their sources through it --
// so none of them can drift on the containment guarantee.
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
