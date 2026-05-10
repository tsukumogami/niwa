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

// --- helpers --------------------------------------------------------------

func writePlainSkill(root, name string) error {
	dir := filepath.Join(root, ".claude", "skills", name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("# "+name+"\n"), 0o600)
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
