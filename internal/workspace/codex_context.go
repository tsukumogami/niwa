package workspace

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

// CodexOverrideFileName is the name niwa writes its composed per-repository
// context to. It is not a fallback keyed on what the repository ships: it is
// hardcoded first in Codex's per-directory precedence, so it is the one name
// that outranks a repository's own `AGENTS.md`, and niwa writes it the same way
// in every repository. Keying the write on the repository's own filename would
// work in most repositories and silently deliver nothing in any that commits an
// `AGENTS.md` -- first-match, no error, no warning.
// See DESIGN-dual-agent-workspace.md Decision 2A.
const CodexOverrideFileName = "AGENTS.override.md"

// CodexOverrideResult reports what one repository's Codex materialization
// produced.
type CodexOverrideResult struct {
	// WrittenFiles is the composed override, or nothing when no layer had
	// content (the never-empty rule: no file at all, so native discovery keeps
	// delivering the repository's own committed context file).
	WrittenFiles []string
	// Refusal is non-nil when the repository's committed context file existed
	// but was not readable as a regular file. The override is still written with
	// the workspace layers; the caller reports the refusal, since it is the only
	// signal that the repository's own content is missing from the document.
	Refusal *CodexInlineRefusal
}

// InstallGroupCodexContext composes and writes {instanceRoot}/{groupName}/AGENTS.md,
// carrying the instance layer followed by the group layer.
//
// This is net-new composition, not the group writer re-run under a second
// filename. Codex's context walk delivers only files at or below the directory
// the session starts in (no project-root marker exists above a group directory),
// so a group file holding the group layer alone would drop the instance layer
// for every session started there. Hence both layers, composed outermost-first.
//
// The never-empty rule applies: with no content at either layer nothing is
// written. A stale file from an earlier apply is removed by the pipeline's
// managed-file cleanup, which sees the path leave the written set.
func InstallGroupCodexContext(cfg *config.WorkspaceConfig, configDir, instanceRoot, groupName string) ([]string, error) {
	instanceLayer, err := renderInstanceContextLayer(cfg, configDir, instanceRoot)
	if err != nil {
		return nil, err
	}
	groupLayer, err := renderGroupContextLayer(cfg, configDir, instanceRoot, groupName)
	if err != nil {
		return nil, err
	}

	composed := ComposeCodexContext(CodexComposeRequest{
		Instance: instanceLayer,
		Group:    groupLayer,
	})
	if composed.Empty() {
		return nil, nil
	}

	target := filepath.Join(instanceRoot, groupName, agent.AgentCodex.RootContextFileName())
	if err := writeContextFile(target, composed.Content); err != nil {
		return nil, err
	}
	return []string{target}, nil
}

// InstallRepoCodexOverride composes and writes
// {instanceRoot}/{groupName}/{repoName}/AGENTS.override.md, carrying the
// instance, group, and repository layers, with the repository's own committed
// `AGENTS.md` inlined after them.
//
// The composed file carries the outer layers because Codex's walk stops at the
// repository root -- the `.git` directory is a project-root marker -- and never
// reaches the instance or group files above it. A per-repository file carrying
// only the repository's layer is the silent failure this composition exists to
// prevent.
//
// Inlining is what keeps a repository's committed context reaching the session:
// `AGENTS.override.md` wins first-match, so it displaces the committed file from
// the discovery slot. The committed file itself is never modified, and it is
// re-read on every apply, so an edit to it shows up in the next composition
// rather than going stale.
//
// Two boundary rules, both from Decision 2A:
//
//   - With no content at any layer niwa configures, no file is written and the
//     committed file is not even read. An override written from the committed
//     content alone would claim the directory's context slot to say what native
//     discovery already delivers.
//   - A committed file that is not a readable regular file is refused (the
//     composer's `O_NOFOLLOW` open), and the refusal is scoped to the inline:
//     the override is still written with the workspace layers, and the caller
//     reports what was left out.
func InstallRepoCodexOverride(cfg *config.WorkspaceConfig, configDir, overlayDir, instanceRoot, groupName, repoName string) (*CodexOverrideResult, error) {
	repoDir := filepath.Join(instanceRoot, groupName, repoName)
	return composeCodexOverride(cfg, configDir, overlayDir, instanceRoot, groupName, repoName, repoDir, "")
}

// InstallWorktreeCodexOverride composes and writes
// {worktreePath}/AGENTS.override.md: the same instance, group, and repository
// layers a clone's override carries, with the worktree's own framing appended
// last and the worktree checkout's committed `AGENTS.md` inlined.
//
// The full chain is the point. A worktree root is where Codex's walk stops (a
// linked worktree's `.git` is a regular file, and the project-root marker check
// is a bare metadata stat that a file satisfies), so a worktree file carrying
// the framing alone would leave a session there with no workspace context at
// all -- the exact silent failure the composition rule exists to prevent. It
// mirrors what a Claude worktree session already gets, where the lifecycle
// installs the repository content first and then appends the framing.
//
// A worktree is a checkout like any other, so its copy of a committed
// `AGENTS.md` is inlined under the same regular-file-only rule (see
// readRegularFileNoFollow) -- read from the worktree, since a worktree on
// another branch can hold different content than the clone.
//
// worktreeLayer is the framing (repository, purpose, branch). It is content
// data only and is never interpolated into a path.
func InstallWorktreeCodexOverride(cfg *config.WorkspaceConfig, configDir, overlayDir, instanceRoot, worktreePath, groupName, repoName, worktreeLayer string) (*CodexOverrideResult, error) {
	return composeCodexOverride(cfg, configDir, overlayDir, instanceRoot, groupName, repoName, worktreePath, worktreeLayer)
}

// composeCodexOverride is the single composer behind both per-tree overrides.
// targetDir is the working tree the override is written into -- a clone for
// InstallRepoCodexOverride, a worktree for InstallWorktreeCodexOverride -- and
// it is also where the committed context file is read from, so each tree
// inlines its own checkout's content. worktreeLayer is empty for a clone.
func composeCodexOverride(cfg *config.WorkspaceConfig, configDir, overlayDir, instanceRoot, groupName, repoName, targetDir, worktreeLayer string) (*CodexOverrideResult, error) {
	instanceLayer, err := renderInstanceContextLayer(cfg, configDir, instanceRoot)
	if err != nil {
		return nil, err
	}
	groupLayer, err := renderGroupContextLayer(cfg, configDir, instanceRoot, groupName)
	if err != nil {
		return nil, err
	}
	repoLayer, err := renderRepoContextLayer(cfg, configDir, overlayDir, instanceRoot, groupName, repoName)
	if err != nil {
		return nil, err
	}

	req := CodexComposeRequest{
		Instance:   instanceLayer,
		Group:      groupLayer,
		Repository: repoLayer.Content,
		Worktree:   worktreeLayer,
	}

	// The committed file joins the chain only when niwa has something of its own
	// to deliver. Composing it alone would produce a file whose entire content is
	// a copy of the file it displaces.
	if !ComposeCodexContext(req).Empty() {
		req.CommittedContextPath = filepath.Join(targetDir, agent.AgentCodex.RootContextFileName())
	}

	composed := ComposeCodexContext(req)
	if composed.Empty() {
		return &CodexOverrideResult{}, nil
	}

	target := filepath.Join(targetDir, CodexOverrideFileName)
	if err := writeContextFile(target, composed.Content); err != nil {
		return nil, err
	}
	return &CodexOverrideResult{
		WrittenFiles: []string{target},
		Refusal:      composed.Refusal,
	}, nil
}

// renderInstanceContextLayer renders the instance-root content entry without
// writing it. It is the same source InstallWorkspaceContent materializes as
// CLAUDE.md and AGENTS.md at the instance root; here it is a layer of a document
// written somewhere below. Returns "" when no instance content is configured.
func renderInstanceContextLayer(cfg *config.WorkspaceConfig, configDir, instanceRoot string) (string, error) {
	source := cfg.Claude.Content.Workspace.Source
	if source == "" {
		return "", nil
	}

	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("resolving instance root: %w", err)
	}

	vars := map[string]string{
		"{workspace}":      absInstance,
		"{workspace_name}": cfg.Workspace.Name,
	}
	return renderContentFile(contentDirRoot(cfg, configDir), source, vars)
}

// renderGroupContextLayer renders a group's content entry without writing it,
// resolving the source from the overlay directory when the group came from an
// overlay (which has its own layout, independent of the workspace content_dir).
// Returns "" when the group has no content entry.
func renderGroupContextLayer(cfg *config.WorkspaceConfig, configDir, instanceRoot, groupName string) (string, error) {
	entry, ok := cfg.Claude.Content.Groups[groupName]
	if !ok || entry.Source == "" {
		return "", nil
	}

	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("resolving instance root: %w", err)
	}

	vars := map[string]string{
		"{workspace}":      absInstance,
		"{workspace_name}": cfg.Workspace.Name,
		"{group_name}":     groupName,
	}

	contentRoot := contentDirRoot(cfg, configDir)
	if entry.OverlayDir != "" {
		contentRoot = entry.OverlayDir
	}
	return renderContentFile(contentRoot, entry.Source, vars)
}

// repoContextLayer is the repository-level content niwa configures, resolved
// once and used by both per-repository writers.
type repoContextLayer struct {
	// Content is the rendered content, which can be empty when a configured
	// source file is itself empty.
	Content string
	// Configured reports whether any source resolved at all. The Claude writer
	// keys its write on this rather than on Content being non-empty, so a
	// configured-but-empty source still produces CLAUDE.local.md as it always
	// has. The Codex composer needs no such distinction: an empty layer
	// contributes nothing to the composition either way.
	Configured bool
}

// renderRepoContextLayer resolves a repository's content entry -- explicit
// entry, else auto-discovery -- renders it, and appends any overlay content,
// without writing anything. InstallRepoContentTo writes exactly this string to
// CLAUDE.local.md and the Codex composer takes it as the repository layer, so
// the two agents cannot drift on which sources a repository's context is built
// from.
//
// Overlay content is appended verbatim, separated by a blank line, and is not
// template-expanded -- matching what the Claude writer has always done.
func renderRepoContextLayer(cfg *config.WorkspaceConfig, configDir, overlayDir, instanceRoot, groupName, repoName string) (repoContextLayer, error) {
	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return repoContextLayer{}, fmt.Errorf("resolving instance root: %w", err)
	}

	vars := map[string]string{
		"{workspace}":      absInstance,
		"{workspace_name}": cfg.Workspace.Name,
		"{group_name}":     groupName,
		"{repo_name}":      repoName,
	}

	entry, hasExplicit := cfg.Claude.Content.Repos[repoName]
	source := ""
	overlaySource := ""
	if hasExplicit {
		source = entry.Source
		overlaySource = entry.OverlaySource
	} else {
		source = autoDiscoverRepoSource(cfg, configDir, repoName)
	}

	if source == "" && overlaySource == "" {
		return repoContextLayer{}, nil
	}

	base := ""
	if source != "" {
		base, err = renderContentFile(contentDirRoot(cfg, configDir), source, vars)
		if err != nil {
			return repoContextLayer{}, err
		}
	}

	if overlaySource == "" {
		return repoContextLayer{Content: base, Configured: true}, nil
	}

	if overlayDir == "" {
		return repoContextLayer{}, fmt.Errorf("repo %q has OverlaySource %q but overlayDir is empty", repoName, overlaySource)
	}
	overlayData, err := os.ReadFile(filepath.Join(overlayDir, overlaySource))
	if err != nil {
		return repoContextLayer{}, fmt.Errorf("reading overlay content for repo %q: %w", repoName, err)
	}

	if source == "" {
		return repoContextLayer{Content: string(overlayData), Configured: true}, nil
	}
	return repoContextLayer{Content: base + "\n" + string(overlayData), Configured: true}, nil
}
