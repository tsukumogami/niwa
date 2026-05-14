package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorkspaceRootSentinel is the instance name returned when the instance
// root path equals the workspace root path. Reference-fleet workspaces
// (e.g., ~/dev/niwaw/tsuku/) typically run a niwa instance at the
// workspace root in addition to sibling instances (tsuku-2, tsuku-3,
// etc.), so the URL layer needs a stable identifier for that case.
const WorkspaceRootSentinel = "_root"

// WorkspaceInstance is one (workspace, instance, root) endpoint the
// machine-level surface serves. Root is the absolute path to the
// instance directory whose .niwa/changes/ the surface aggregates.
type WorkspaceInstance struct {
	Workspace string
	Instance  string
	Root      string
}

// EnumerateInstances walks the registry and discovers every instance
// (the workspace root itself, plus first-level sub-directories with a
// .niwa/ marker) for each registered workspace. Result is sorted by
// workspace name first, then instance name. The root sentinel ("_root")
// sorts ahead of named instances within the same workspace because '_'
// precedes ASCII letters.
//
// A registered workspace whose root is missing or unreadable contributes
// zero instances and does not abort enumeration — operators can have
// stale registry entries.
func EnumerateInstances(g *GlobalConfig) ([]WorkspaceInstance, error) {
	if g == nil || len(g.Registry) == 0 {
		return nil, nil
	}
	out := make([]WorkspaceInstance, 0, len(g.Registry))
	for name, entry := range g.Registry {
		roots, err := DiscoverInstances(entry.Root)
		if err != nil {
			// Per the doc comment: don't abort on a single bad workspace.
			continue
		}
		for _, root := range roots {
			inst, err := InstanceNameFromPath(entry.Root, root)
			if err != nil {
				continue
			}
			out = append(out, WorkspaceInstance{
				Workspace: name,
				Instance:  inst,
				Root:      root,
			})
		}
	}
	sortInstances(out)
	return out, nil
}

func sortInstances(in []WorkspaceInstance) {
	// Pure-stdlib sort to avoid pulling in sort.Slice's reflection cost
	// at startup. Simple insertion-sort suffices for the workspace counts
	// niwa targets (low tens).
	for i := 1; i < len(in); i++ {
		j := i
		for j > 0 && lessInstance(in[j], in[j-1]) {
			in[j-1], in[j] = in[j], in[j-1]
			j--
		}
	}
}

func lessInstance(a, b WorkspaceInstance) bool {
	if a.Workspace != b.Workspace {
		return a.Workspace < b.Workspace
	}
	return a.Instance < b.Instance
}

// SurfaceConfigDir returns the directory under XDG_CONFIG_HOME (or the
// fallback ~/.config/niwa) where the machine-level surface server keeps
// its lock, token, and port files. It is the parent directory of
// GlobalConfigPath()'s config.toml location.
func SurfaceConfigDir() (string, error) {
	path, err := GlobalConfigPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

// ErrInstanceNotUnderWorkspace is returned by ResolveWorkspaceInstance
// when the supplied instance root does not live under any registered
// workspace's root. Callers consulting the result for URL composition
// should treat this as a fatal mis-config — the surface server has no
// way to address the instance otherwise.
var ErrInstanceNotUnderWorkspace = errors.New("instance root is not under any registered workspace")

// ResolveWorkspaceInstance maps an instance root path to its
// (workspace, instance) identifiers using the global registry. The
// workspace identifier is the registry key whose `root` is an ancestor
// of instanceRoot. The instance identifier is the first path segment of
// instanceRoot relative to that workspace root, except when instanceRoot
// equals the workspace root — in which case WorkspaceRootSentinel is
// returned to keep the URL contract well-formed.
//
// When multiple registered workspaces could match (one nested inside
// another), the longest (most-specific) workspace root wins.
func ResolveWorkspaceInstance(g *GlobalConfig, instanceRoot string) (workspace, instance string, err error) {
	if g == nil || len(g.Registry) == 0 {
		return "", "", fmt.Errorf("registry is empty")
	}
	abs, err := filepath.Abs(instanceRoot)
	if err != nil {
		return "", "", fmt.Errorf("absolute path: %w", err)
	}
	abs = filepath.Clean(abs)

	var bestWorkspace, bestRoot string
	for name, entry := range g.Registry {
		wsRoot, err := filepath.Abs(entry.Root)
		if err != nil {
			continue
		}
		wsRoot = filepath.Clean(wsRoot)
		if abs != wsRoot && !strings.HasPrefix(abs, wsRoot+string(filepath.Separator)) {
			continue
		}
		if len(wsRoot) > len(bestRoot) {
			bestWorkspace = name
			bestRoot = wsRoot
		}
	}
	if bestWorkspace == "" {
		return "", "", fmt.Errorf("%w: %s", ErrInstanceNotUnderWorkspace, abs)
	}
	if abs == bestRoot {
		return bestWorkspace, WorkspaceRootSentinel, nil
	}
	rel, err := filepath.Rel(bestRoot, abs)
	if err != nil {
		return "", "", fmt.Errorf("rel path: %w", err)
	}
	parts := strings.SplitN(rel, string(filepath.Separator), 2)
	if parts[0] == "" || parts[0] == "." {
		return bestWorkspace, WorkspaceRootSentinel, nil
	}
	return bestWorkspace, parts[0], nil
}

// DiscoverInstances enumerates every directory under workspaceRoot
// (including workspaceRoot itself) that looks like a niwa instance —
// defined as having a `.niwa` subdirectory. The returned paths are
// absolute. Symlinks are followed only at the top level; descent stops
// at depth 1 (no recursion into sub-subdirectories), matching the
// reference-fleet shape where instances are direct children of the
// workspace root.
func DiscoverInstances(workspaceRoot string) ([]string, error) {
	abs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("absolute path: %w", err)
	}
	out := make([]string, 0, 4)
	if hasNiwaDir(abs) {
		out = append(out, abs)
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, fmt.Errorf("read workspace root: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Skip dotfile dirs (`.niwa`, `.git`, `.claude`...) — instance
		// directories are conventionally non-dot names.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		candidate := filepath.Join(abs, e.Name())
		if hasNiwaDir(candidate) {
			out = append(out, candidate)
		}
	}
	return out, nil
}

// hasNiwaDir reports whether `path` contains a `.niwa` subdirectory.
// Errors (permission, missing) collapse to false; the caller wanted to
// know whether the instance shape is present, not why a stat failed.
func hasNiwaDir(path string) bool {
	info, err := os.Stat(filepath.Join(path, ".niwa"))
	if err != nil {
		return false
	}
	return info.IsDir()
}

// InstanceNameFromPath returns the instance name segment that
// ResolveWorkspaceInstance would yield, given a workspace root and a
// specific instance root. Convenience wrapper for callers that already
// know the workspace root (e.g., DiscoverInstances results).
func InstanceNameFromPath(workspaceRoot, instanceRoot string) (string, error) {
	wsAbs, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("workspace abs: %w", err)
	}
	wsAbs = filepath.Clean(wsAbs)
	inAbs, err := filepath.Abs(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("instance abs: %w", err)
	}
	inAbs = filepath.Clean(inAbs)
	if inAbs == wsAbs {
		return WorkspaceRootSentinel, nil
	}
	rel, err := filepath.Rel(wsAbs, inAbs)
	if err != nil {
		return "", fmt.Errorf("rel: %w", err)
	}
	parts := strings.SplitN(rel, string(filepath.Separator), 2)
	if parts[0] == "" || parts[0] == "." {
		return WorkspaceRootSentinel, nil
	}
	if strings.HasPrefix(parts[0], "..") {
		return "", fmt.Errorf("instance root %s is not under workspace root %s", inAbs, wsAbs)
	}
	return parts[0], nil
}
