package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRequiredSkills_TypoMissDetailed verifies the canonical Issue 6 path:
// a typo in `body.required_skills` causes a synchronous MISSING_SKILLS
// response, no task ID is allocated, and the response body carries
// {missing: [...], available: [...]}.
func TestRequiredSkills_TypoMissDetailed(t *testing.T) {
	root := t.TempDir()
	if err := writeSettingsWithPlugins(root, "shirabe@shirabe"); err != nil {
		t.Fatal(err)
	}
	if err := writePlainSkill(root, "niwa-mesh"); err != nil {
		t.Fatal(err)
	}
	s := &Server{instanceRoot: root}

	// "shirabe:plan" namespaced is fine. "shirabe:rpd" namespaced is
	// fine (gate doesn't check skill IDs within an enabled namespace).
	// But "shrabe:plan" misspells the namespace itself — caught.
	body := json.RawMessage(`{"required_skills":["shrabe:plan"]}`)
	res := s.checkRequiredSkills(body)
	if !res.IsError {
		t.Fatalf("expected error result, got success")
	}
	if errorCode(&res) != "MISSING_SKILLS" {
		t.Errorf("error_code = %q, want MISSING_SKILLS", errorCode(&res))
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &resp); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	missing, _ := resp["missing"].([]any)
	if len(missing) != 1 || missing[0] != "shrabe:plan" {
		t.Errorf("missing = %v, want [shrabe:plan]", resp["missing"])
	}
	available, _ := resp["available"].([]any)
	hasNiwaMesh, hasShirabe := false, false
	for _, a := range available {
		if a == "niwa-mesh" {
			hasNiwaMesh = true
		}
		if a == "shirabe:*" {
			hasShirabe = true
		}
	}
	if !hasNiwaMesh {
		t.Errorf("available = %v, want to include niwa-mesh", available)
	}
	if !hasShirabe {
		t.Errorf("available = %v, want to include shirabe:*", available)
	}
}

// TestRequiredSkills_ExactMatchPlain verifies a plain skill name matches
// the directory under .claude/skills/.
func TestRequiredSkills_ExactMatchPlain(t *testing.T) {
	root := t.TempDir()
	if err := writePlainSkill(root, "niwa-mesh"); err != nil {
		t.Fatal(err)
	}
	s := &Server{instanceRoot: root}
	body := json.RawMessage(`{"required_skills":["niwa-mesh"]}`)
	res := s.checkRequiredSkills(body)
	if res.IsError {
		t.Errorf("plain skill should match; got error: %s", res.Content[0].Text)
	}
}

// TestRequiredSkills_NamespacedMatch verifies a namespaced skill matches
// against any plugin namespace in enabledPlugins.
func TestRequiredSkills_NamespacedMatch(t *testing.T) {
	root := t.TempDir()
	if err := writeSettingsWithPlugins(root, "shirabe@shirabe", "tsukumogami@tsukumogami"); err != nil {
		t.Fatal(err)
	}
	s := &Server{instanceRoot: root}
	body := json.RawMessage(`{"required_skills":["shirabe:plan","tsukumogami:work-on"]}`)
	res := s.checkRequiredSkills(body)
	if res.IsError {
		t.Errorf("namespaced skills with enabled namespaces should match; got error: %s", res.Content[0].Text)
	}
}

// TestRequiredSkills_OmittedNoOp verifies that omitting required_skills
// returns no error — Issue 6's body convention is purely additive.
func TestRequiredSkills_OmittedNoOp(t *testing.T) {
	s := &Server{instanceRoot: t.TempDir()}
	for _, body := range []string{
		`{}`,
		`{"foo":"bar"}`,
		`{"required_skills":[]}`,
		``,
		`null`,
	} {
		res := s.checkRequiredSkills(json.RawMessage(body))
		if res.IsError {
			t.Errorf("body %q should be a no-op; got error: %s", body, res.Content[0].Text)
		}
	}
}

// TestRequiredSkills_EmptyManifestRejects verifies the gate fires when no
// workspace skills are installed at all.
func TestRequiredSkills_EmptyManifestRejects(t *testing.T) {
	s := &Server{instanceRoot: t.TempDir()}
	body := json.RawMessage(`{"required_skills":["anything"]}`)
	res := s.checkRequiredSkills(body)
	if !res.IsError {
		t.Errorf("expected MISSING_SKILLS for unrecognized skill in empty manifest")
	}
}

// TestRequiredSkills_TaskStoreRootRedirect verifies the manifest is read
// from taskStoreRoot (the workspace's main instance) for session workers,
// not from instanceRoot (the worktree without a .claude/ tree).
func TestRequiredSkills_TaskStoreRootRedirect(t *testing.T) {
	mainRoot := t.TempDir()
	worktreeRoot := t.TempDir()
	if err := writePlainSkill(mainRoot, "niwa-mesh"); err != nil {
		t.Fatal(err)
	}
	// Worktree has NO skills installed (mirrors scaffoldWorktreeNiwa).

	s := &Server{
		instanceRoot:     worktreeRoot,
		mainInstanceRoot: mainRoot,
	}
	body := json.RawMessage(`{"required_skills":["niwa-mesh"]}`)
	res := s.checkRequiredSkills(body)
	if res.IsError {
		t.Errorf("manifest must read from taskStoreRoot; got error: %s", res.Content[0].Text)
	}
}

// TestRequiredSkills_VersionPinMatch asserts that a `<ns>@<version>` form
// matches when the installed plugin's version equals the pin, and rejects
// when it does not. Reads the version from
// `<userHomeDir>/.claude/plugins/installed_plugins.json`.
func TestRequiredSkills_VersionPinMatch(t *testing.T) {
	root := t.TempDir()
	homeDir := t.TempDir()
	if err := writeSettingsWithPlugins(root, "shirabe@shirabe"); err != nil {
		t.Fatal(err)
	}
	if err := writeInstalledPluginRegistry(homeDir, map[string]string{
		"shirabe@shirabe": "0.5.2",
	}); err != nil {
		t.Fatal(err)
	}
	s := &Server{instanceRoot: root, userHomeDir: homeDir}

	// Matching pin: success.
	body := json.RawMessage(`{"required_skills":["shirabe@0.5.2:plan"]}`)
	if res := s.checkRequiredSkills(body); res.IsError {
		t.Errorf("matching version pin should succeed; got error: %s", res.Content[0].Text)
	}

	// Mismatching pin: rejected.
	body = json.RawMessage(`{"required_skills":["shirabe@0.5.1:plan"]}`)
	res := s.checkRequiredSkills(body)
	if !res.IsError {
		t.Fatalf("mismatching version pin should fail")
	}
	if errorCode(&res) != "MISSING_SKILLS" {
		t.Errorf("error_code = %q, want MISSING_SKILLS", errorCode(&res))
	}
	var resp map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &resp)
	missing, _ := resp["missing"].([]any)
	if len(missing) != 1 || missing[0] != "shirabe@0.5.1:plan" {
		t.Errorf("missing = %v, want [shirabe@0.5.1:plan]", resp["missing"])
	}
}

// TestRequiredSkills_UserLevelPluginsHonored asserts that a plugin enabled
// only at user-level `~/.claude/settings.json` (not in the project's
// `.claude/settings.json`) satisfies a required skill, demonstrating the
// project + user merge.
func TestRequiredSkills_UserLevelPluginsHonored(t *testing.T) {
	root := t.TempDir()
	homeDir := t.TempDir()

	// Project-level: empty enabledPlugins.
	if err := writeSettingsWithPlugins(root); err != nil {
		t.Fatal(err)
	}
	// User-level: shirabe@shirabe enabled.
	if err := writeUserSettings(homeDir, "shirabe@shirabe"); err != nil {
		t.Fatal(err)
	}
	s := &Server{instanceRoot: root, userHomeDir: homeDir}

	body := json.RawMessage(`{"required_skills":["shirabe:plan"]}`)
	if res := s.checkRequiredSkills(body); res.IsError {
		t.Errorf("user-level plugin should satisfy required_skills; got: %s", res.Content[0].Text)
	}
}

// TestRequiredSkills_ProjectOverridesUserDisable asserts that a plugin
// enabled at user-level but disabled at project-level (`enabledPlugins`
// entry set to false) is treated as not enabled — the project's "no"
// wins over the user's "yes" on key conflict, mirroring Claude Code's
// project-takes-precedence convention.
func TestRequiredSkills_ProjectOverridesUserDisable(t *testing.T) {
	root := t.TempDir()
	homeDir := t.TempDir()

	if err := writeUserSettings(homeDir, "shirabe@shirabe"); err != nil {
		t.Fatal(err)
	}
	// Project-level: shirabe@shirabe explicitly disabled.
	projectDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	disableJSON := `{"enabledPlugins":{"shirabe@shirabe":false}}`
	if err := os.WriteFile(filepath.Join(projectDir, "settings.json"), []byte(disableJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	s := &Server{instanceRoot: root, userHomeDir: homeDir}
	body := json.RawMessage(`{"required_skills":["shirabe:plan"]}`)
	res := s.checkRequiredSkills(body)
	if !res.IsError {
		t.Fatalf("project-level disable should override user-level enable")
	}
	if errorCode(&res) != "MISSING_SKILLS" {
		t.Errorf("error_code = %q, want MISSING_SKILLS", errorCode(&res))
	}
}

// --- helpers --------------------------------------------------------------

func writePlainSkill(root, name string) error {
	dir := filepath.Join(root, ".claude", "skills", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("# "+name+"\n"), 0o600)
}

// writeUserSettings writes <homeDir>/.claude/settings.json with the given
// plugin keys enabled. Mirrors writeSettingsWithPlugins but at the
// user-level path the production code reads.
func writeUserSettings(homeDir string, plugins ...string) error {
	dir := filepath.Join(homeDir, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	enabled := map[string]bool{}
	for _, p := range plugins {
		enabled[p] = true
	}
	doc := map[string]any{"enabledPlugins": enabled}
	data, _ := json.Marshal(doc)
	return os.WriteFile(filepath.Join(dir, "settings.json"), data, 0o600)
}

// writeInstalledPluginRegistry writes a minimal installed_plugins.json at
// <homeDir>/.claude/plugins/installed_plugins.json with one entry per key
// of the form `{"plugins": {"<key>": [{"version": "<v>"}]}}`. Used to
// drive the version-pin path in checkRequiredSkills tests.
func writeInstalledPluginRegistry(homeDir string, keyVersions map[string]string) error {
	dir := filepath.Join(homeDir, ".claude", "plugins")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	plugins := map[string][]map[string]string{}
	for key, ver := range keyVersions {
		plugins[key] = []map[string]string{{"version": ver}}
	}
	doc := map[string]any{"version": 2, "plugins": plugins}
	data, _ := json.Marshal(doc)
	return os.WriteFile(filepath.Join(dir, "installed_plugins.json"), data, 0o600)
}

func writeSettingsWithPlugins(root string, plugins ...string) error {
	dir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	enabled := map[string]bool{}
	for _, p := range plugins {
		enabled[p] = true
	}
	doc := map[string]any{"enabledPlugins": enabled}
	data, _ := json.Marshal(doc)
	if !strings.Contains(string(data), "enabledPlugins") {
		return os.ErrInvalid
	}
	return os.WriteFile(filepath.Join(dir, "settings.json"), data, 0o600)
}
