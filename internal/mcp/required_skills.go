package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// checkRequiredSkills implements Issue 6 / #113's queue-time gate.
//
// It peeks `body.required_skills: []string` and intersects it with the
// workspace's skill manifest. On a miss, returns errResultCodeBody with
// code MISSING_SKILLS and a JSON body of shape:
//
//	{"missing": [...], "available": [...]}
//
// The gate's manifest covers two skill families:
//
//   - **Plain skills**: directories under
//     `<workspaceRoot>/.claude/skills/<name>/SKILL.md`. Examples in this
//     workspace include `niwa-mesh` and any skill the user adds. Required
//     entries with no colon (e.g. `niwa-mesh`) match by exact directory
//     name.
//
//   - **Plugin skills**: identified by namespace from the `enabledPlugins`
//     map in `<workspaceRoot>/.claude/settings.json`. Required entries
//     containing a colon (e.g. `shirabe:plan`) split into <namespace>:<skill>;
//     the gate verifies the namespace is enabled. Per-skill resolution
//     within a plugin is intentionally NOT performed — that would require
//     reading each plugin's manifest from the user-level plugin store, and
//     the gate's role under Issue 6 is typo-catching at queue time, not
//     deep capability auditing. A typo within an enabled namespace
//     (e.g. `shirabe:rpd` when only `shirabe:prd` exists) is the worker's
//     concern; the gate's job is to catch references to plugins or plain
//     skills that aren't installed at all.
//
// When body is empty or has no `required_skills` field, the function
// returns toolResult{} (no error). The body remains opaque to niwa
// otherwise — Issue 6 doesn't introduce a body schema.
func (s *Server) checkRequiredSkills(body json.RawMessage) toolResult {
	if len(body) == 0 || string(body) == "null" {
		return toolResult{}
	}
	var peek struct {
		RequiredSkills []string `json:"required_skills"`
	}
	if err := json.Unmarshal(body, &peek); err != nil {
		// Invalid JSON would have been caught earlier; here the field is
		// just missing or wrong type. No-op.
		return toolResult{}
	}
	if len(peek.RequiredSkills) == 0 {
		return toolResult{}
	}

	workspaceRoot := s.taskStoreRoot()
	plain := plainSkillsAt(workspaceRoot)
	pluginNamespaces := pluginNamespacesAt(workspaceRoot)

	available := make([]string, 0, len(plain)+len(pluginNamespaces))
	available = append(available, plain...)
	for _, ns := range pluginNamespaces {
		available = append(available, ns+":*")
	}
	sort.Strings(available)

	var missing []string
	for _, req := range peek.RequiredSkills {
		if skillIsAvailable(req, plain, pluginNamespaces) {
			continue
		}
		missing = append(missing, req)
	}
	if len(missing) == 0 {
		return toolResult{}
	}
	return errResultCodeBody("MISSING_SKILLS", map[string]any{
		"missing":   missing,
		"available": available,
		"detail":    fmt.Sprintf("body.required_skills references %d skill(s) not installed in this workspace", len(missing)),
	})
}

// skillIsAvailable returns true when req matches a plain skill exactly OR
// (when req is namespaced) when its namespace is in the enabled-plugin
// set.
func skillIsAvailable(req string, plain []string, pluginNamespaces []string) bool {
	if i := strings.Index(req, ":"); i > 0 {
		namespace := req[:i]
		for _, ns := range pluginNamespaces {
			if ns == namespace {
				return true
			}
		}
		return false
	}
	for _, p := range plain {
		if p == req {
			return true
		}
	}
	return false
}

// plainSkillsAt enumerates `<workspaceRoot>/.claude/skills/<name>/SKILL.md`
// and returns the directory names (skill ids). Returns nil when the
// directory is missing or unreadable — the gate degrades gracefully to
// "no plain skills installed" rather than failing closed.
func plainSkillsAt(workspaceRoot string) []string {
	skillsDir := filepath.Join(workspaceRoot, ".claude", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(skillsDir, e.Name(), "SKILL.md")); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// pluginNamespacesAt parses `<workspaceRoot>/.claude/settings.json`'s
// enabledPlugins map and returns the set of plugin namespaces (the part
// before the @ in keys like `shirabe@shirabe`). Returns nil when the file
// is missing, unreadable, or has no enabledPlugins.
func pluginNamespacesAt(workspaceRoot string) []string {
	path := filepath.Join(workspaceRoot, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	for key, enabled := range settings.EnabledPlugins {
		if !enabled {
			continue
		}
		ns := key
		if i := strings.Index(key, "@"); i > 0 {
			ns = key[:i]
		}
		seen[ns] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for ns := range seen {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}
