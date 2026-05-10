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
//     `<workspaceRoot>/.claude/skills/<name>/SKILL.md`. Required entries
//     with no colon (e.g. `niwa-mesh`) match by exact directory name.
//
//   - **Plugin skills**: identified by namespace from the `enabledPlugins`
//     map in `<workspaceRoot>/.claude/settings.json` AND user-level
//     `<userHomeDir>/.claude/settings.json`, merged (project takes
//     precedence on conflict). Required entries containing a colon
//     (e.g. `shirabe:plan`) split into `<namespace>:<skill>` and the gate
//     verifies the namespace is enabled. Per-skill resolution within a
//     plugin is intentionally NOT performed — the gate's role is
//     typo-catching at queue time, not deep capability auditing.
//
// **Version pinning** (extends the plugin-skill form): a required entry
// may carry an `@<version>` segment between namespace and the optional
// `:<skill>` to demand a specific installed plugin version, e.g.
// `shirabe@0.5.2:plan` or just `shirabe@0.5.2`. The version is matched
// against `<userHomeDir>/.claude/plugins/installed_plugins.json` for the
// enabled plugin's source. When no version segment is present, any
// installed version of the namespace satisfies the requirement.
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
		return toolResult{}
	}
	if len(peek.RequiredSkills) == 0 {
		return toolResult{}
	}

	workspaceRoot := s.taskStoreRoot()
	plain := plainSkillsAt(workspaceRoot)
	plugins := installedPluginsAt(workspaceRoot, s.userHomeDir)

	available := make([]string, 0, len(plain)+len(plugins))
	available = append(available, plain...)
	pluginKeys := make([]string, 0, len(plugins))
	for ns, versions := range plugins {
		// Surface each installed version once so callers can read the
		// actual versions the gate would accept; use a star sentinel when
		// no version is recorded (legacy install or registry not present).
		for _, v := range versions {
			if v == "" {
				pluginKeys = append(pluginKeys, ns+":*")
			} else {
				pluginKeys = append(pluginKeys, ns+"@"+v+":*")
			}
		}
		if len(versions) == 0 {
			pluginKeys = append(pluginKeys, ns+":*")
		}
	}
	sort.Strings(pluginKeys)
	available = append(available, pluginKeys...)

	var missing []string
	for _, req := range peek.RequiredSkills {
		if skillIsAvailable(req, plain, plugins) {
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

// skillIsAvailable returns true when req matches an installed plain skill
// exactly OR matches an enabled plugin namespace (with optional version
// pin). Required forms accepted:
//
//   - `plain-skill-name`            — exact match against plain skills.
//   - `<namespace>`                 — any installed version of the namespace.
//   - `<namespace>:<skill>`         — same as above; skill suffix is advisory.
//   - `<namespace>@<version>`       — namespace + exact version match.
//   - `<namespace>@<version>:<skill>` — namespace + exact version match.
//
// `plugins` maps namespace → installed versions; an entry's value list may
// be empty when the version registry is unavailable (gate degrades to
// "any version accepted" so missing-registry doesn't fail closed).
func skillIsAvailable(req string, plain []string, plugins map[string][]string) bool {
	// Strip the optional :<skill> suffix; we only validate down to namespace
	// (and version, if pinned).
	target := req
	if i := strings.Index(target, ":"); i > 0 {
		target = target[:i]
	}
	// Detect version pin via the @ separator.
	namespace := target
	wantVersion := ""
	if i := strings.Index(target, "@"); i > 0 {
		namespace = target[:i]
		wantVersion = target[i+1:]
	}

	// Plugin-namespace match (always the right reading when the original
	// form had a colon or @).
	if namespace != req || strings.ContainsAny(req, "@:") {
		versions, ok := plugins[namespace]
		if !ok {
			return false
		}
		if wantVersion == "" {
			return true
		}
		// Empty versions list = no registry data; degrade to "any version
		// accepted" so a missing installed_plugins.json doesn't reject
		// otherwise-valid pins. Tests can override by passing an empty list
		// vs. a version list with one entry.
		if len(versions) == 0 {
			return true
		}
		for _, v := range versions {
			if v == wantVersion {
				return true
			}
		}
		return false
	}

	// Plain-skill match (no colon, no @ in original).
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

// installedPluginsAt parses the union of project-level and user-level
// enabled plugins, looks up each enabled plugin's installed version from
// `<userHomeDir>/.claude/plugins/installed_plugins.json`, and returns a
// map from namespace → list of installed versions. The version list per
// namespace can be longer than one when the same namespace is enabled
// from multiple sources (e.g. `shirabe@shirabe` plus `shirabe@my-fork`).
//
// When the installed-plugins registry is missing or unreadable, the
// returned map still includes the namespace (with an empty version list)
// so the gate can match by namespace alone — degrading to "any version
// accepted" rather than failing closed.
//
// `userHomeDir` may be empty; in that case the user-level settings and
// plugin registry are skipped (test-friendly).
func installedPluginsAt(workspaceRoot, userHomeDir string) map[string][]string {
	enabled := readEnabledPluginKeys(workspaceRoot, userHomeDir)
	if len(enabled) == 0 {
		return nil
	}
	versions := readPluginVersions(userHomeDir)

	out := map[string][]string{}
	for key := range enabled {
		ns := key
		if i := strings.Index(key, "@"); i > 0 {
			ns = key[:i]
		}
		v := versions[key]
		if v == "" {
			out[ns] = append(out[ns], "")
		} else {
			out[ns] = append(out[ns], v)
		}
	}
	for ns := range out {
		seen := map[string]struct{}{}
		dedup := out[ns][:0]
		for _, v := range out[ns] {
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			dedup = append(dedup, v)
		}
		// Drop empty-string entries when at least one real version is present:
		// they only existed as a "no registry data" placeholder.
		hasReal := false
		for _, v := range dedup {
			if v != "" {
				hasReal = true
				break
			}
		}
		if hasReal {
			cleaned := dedup[:0]
			for _, v := range dedup {
				if v == "" {
					continue
				}
				cleaned = append(cleaned, v)
			}
			dedup = cleaned
		}
		sort.Strings(dedup)
		out[ns] = dedup
	}
	return out
}

// readEnabledPluginKeys merges enabledPlugins from user-level
// `<userHomeDir>/.claude/settings.json` and project-level
// `<workspaceRoot>/.claude/settings.json`. Project-level entries override
// user-level on key conflict (a project disabling a user-enabled plugin
// wins). Returns the set of enabled plugin keys (e.g. `shirabe@shirabe`).
func readEnabledPluginKeys(workspaceRoot, userHomeDir string) map[string]bool {
	out := map[string]bool{}
	for _, path := range []string{
		filepath.Join(userHomeDir, ".claude", "settings.json"),
		filepath.Join(workspaceRoot, ".claude", "settings.json"),
	} {
		if path == filepath.Join("", ".claude", "settings.json") {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var settings struct {
			EnabledPlugins map[string]bool `json:"enabledPlugins"`
		}
		if err := json.Unmarshal(data, &settings); err != nil {
			continue
		}
		for key, enabled := range settings.EnabledPlugins {
			out[key] = enabled
		}
	}
	// Strip explicit-disable entries so callers see only positively-enabled keys.
	for key, enabled := range out {
		if !enabled {
			delete(out, key)
		}
	}
	return out
}

// readPluginVersions reads `<userHomeDir>/.claude/plugins/installed_plugins.json`
// and returns a map from plugin key (e.g. `shirabe@shirabe`) to the
// installed version. Multiple installs of the same key choose the
// highest-versioned one alphabetically (sufficient for the gate's
// typo-catching role). Returns an empty map when the registry is
// missing or unreadable.
func readPluginVersions(userHomeDir string) map[string]string {
	out := map[string]string{}
	if userHomeDir == "" {
		return out
	}
	path := filepath.Join(userHomeDir, ".claude", "plugins", "installed_plugins.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	var registry struct {
		Plugins map[string][]struct {
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return out
	}
	for key, entries := range registry.Plugins {
		best := ""
		for _, e := range entries {
			if e.Version == "" {
				continue
			}
			if best == "" || e.Version > best {
				best = e.Version
			}
		}
		out[key] = best
	}
	return out
}
