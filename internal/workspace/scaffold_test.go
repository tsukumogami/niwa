package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/github"
)

func stripTOMLComments(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestScaffold_WithName(t *testing.T) {
	dir := t.TempDir()

	if err := Scaffold(dir, "my-project"); err != nil {
		t.Fatalf("Scaffold returned error: %v", err)
	}

	// Verify .niwa/ directory exists.
	niwaDir := filepath.Join(dir, StateDir)
	info, err := os.Stat(niwaDir)
	if err != nil {
		t.Fatalf(".niwa/ directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal(".niwa/ is not a directory")
	}

	// Verify workspace.toml exists and contains the name.
	configPath := filepath.Join(niwaDir, WorkspaceConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("workspace.toml not created: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, `name = "my-project"`) {
		t.Errorf("expected name = \"my-project\" in workspace.toml, got:\n%s", content)
	}
	if !strings.Contains(content, `default_branch = "main"`) {
		t.Error("expected default_branch in workspace.toml")
	}
	if !strings.Contains(content, `content_dir = "claude"`) {
		t.Error("expected content_dir in workspace.toml")
	}

	// Verify commented sections are present.
	for _, section := range []string{"[[sources]]", "[groups.public]", "[repos.my-repo]", "[claude.content.workspace]", "[[claude.hooks.pre_tool_use]]", "[claude.settings]", "[env]"} {
		if !strings.Contains(content, "# "+section) {
			t.Errorf("expected commented section %q in template", section)
		}
	}

	// Verify claude/ content directory exists.
	claudeDir := filepath.Join(niwaDir, "claude")
	info, err = os.Stat(claudeDir)
	if err != nil {
		t.Fatalf("claude/ directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("claude/ is not a directory")
	}
}

func TestScaffold_EmptyName(t *testing.T) {
	dir := t.TempDir()

	if err := Scaffold(dir, ""); err != nil {
		t.Fatalf("Scaffold returned error: %v", err)
	}

	configPath := filepath.Join(dir, StateDir, WorkspaceConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("workspace.toml not created: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, `name = "workspace"`) {
		t.Errorf("expected default name = \"workspace\", got:\n%s", content)
	}
}

func TestScaffold_ValidTOMLWhenStripped(t *testing.T) {
	dir := t.TempDir()

	if err := Scaffold(dir, "test-project"); err != nil {
		t.Fatalf("Scaffold returned error: %v", err)
	}

	configPath := filepath.Join(dir, StateDir, WorkspaceConfigFile)
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("workspace.toml not created: %v", err)
	}

	stripped := stripTOMLComments(string(data))

	var parsed map[string]any
	if _, err := toml.Decode(stripped, &parsed); err != nil {
		t.Fatalf("template is not valid TOML when comments stripped: %v\nStripped content:\n%s", err, stripped)
	}

	ws, ok := parsed["workspace"]
	if !ok {
		t.Fatal("expected [workspace] section in parsed TOML")
	}
	wsMap, ok := ws.(map[string]any)
	if !ok {
		t.Fatal("expected [workspace] to be a table")
	}
	if wsMap["name"] != "test-project" {
		t.Errorf("expected name = \"test-project\", got %v", wsMap["name"])
	}
	if wsMap["default_branch"] != "main" {
		t.Errorf("expected default_branch = \"main\", got %v", wsMap["default_branch"])
	}
	if wsMap["content_dir"] != "claude" {
		t.Errorf("expected content_dir = \"claude\", got %v", wsMap["content_dir"])
	}
}

// appendixAGolden returns the PRD Appendix A body after placeholder
// substitution. It is duplicated here (not imported from scaffold.go)
// so the test catches accidental edits to scaffoldFromSourceTemplate —
// if you regenerate the golden, regenerate the PRD too.
func appendixAGolden(name, org, repo, visKey, visValue string) string {
	return strings.NewReplacer(
		"<workspace-name>", name,
		"<source-org>", org,
		"<bootstrap-repo>", repo,
		"<vis-key>", visKey,
		"<vis-value>", visValue,
	).Replace(`[workspace]
name = "<workspace-name>"
content_dir = "claude"

[[sources]]
org = "<source-org>"
repos = ["<bootstrap-repo>"]

[groups.<vis-key>]
visibility = "<vis-value>"
# Bind the bootstrap repo to this group by name: explicit-repos sources carry
# no live visibility, so name membership is what places the repo in a group.
repos = ["<bootstrap-repo>"]

# CLAUDE.md content hierarchy: drop a workspace.md in .niwa/claude/ to populate.
# [claude.content.workspace]
# source = "workspace.md"

# See https://github.com/tsukumogami/niwa/blob/main/docs/guides/workspace-config-sources.md
# for the full schema (claude.*, env.*, vault.*, files, instance).
`)
}

func TestScaffoldFromSource_AppendixA_Public(t *testing.T) {
	dir := t.TempDir()
	opts := ScaffoldOptions{
		Name:           "my-workspace",
		Org:            "owner",
		Repo:           "bootstrap-repo",
		Private:        false,
		IncludeGitkeep: true,
	}
	if err := ScaffoldFromSource(dir, opts); err != nil {
		t.Fatalf("ScaffoldFromSource: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, StateDir, WorkspaceConfigFile))
	if err != nil {
		t.Fatalf("read workspace.toml: %v", err)
	}
	want := appendixAGolden("my-workspace", "owner", "bootstrap-repo", "public", "public")
	if string(got) != want {
		t.Errorf("scaffold body mismatch:\n--- want ---\n%s\n--- got ---\n%s", want, string(got))
	}
}

func TestScaffoldFromSource_AppendixA_Private(t *testing.T) {
	dir := t.TempDir()
	opts := ScaffoldOptions{
		Name:           "my-private-ws",
		Org:            "owner",
		Repo:           "secret-repo",
		Private:        true,
		IncludeGitkeep: true,
	}
	if err := ScaffoldFromSource(dir, opts); err != nil {
		t.Fatalf("ScaffoldFromSource: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, StateDir, WorkspaceConfigFile))
	if err != nil {
		t.Fatalf("read workspace.toml: %v", err)
	}
	want := appendixAGolden("my-private-ws", "owner", "secret-repo", "private", "private")
	if string(got) != want {
		t.Errorf("scaffold body mismatch:\n--- want ---\n%s\n--- got ---\n%s", want, string(got))
	}
}

func TestScaffoldFromSource_Gitkeep_Empty(t *testing.T) {
	dir := t.TempDir()
	opts := ScaffoldOptions{
		Name: "ws", Org: "owner", Repo: "repo",
		IncludeGitkeep: true,
	}
	if err := ScaffoldFromSource(dir, opts); err != nil {
		t.Fatalf("ScaffoldFromSource: %v", err)
	}
	gitkeep := filepath.Join(dir, StateDir, "claude", ".gitkeep")
	info, err := os.Stat(gitkeep)
	if err != nil {
		t.Fatalf("expected .gitkeep present: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf(".gitkeep size = %d, want 0 bytes (R15 contract)", info.Size())
	}
	// Defensive: ReadFile and confirm zero bytes.
	data, err := os.ReadFile(gitkeep)
	if err != nil {
		t.Fatalf("read .gitkeep: %v", err)
	}
	if len(data) != 0 {
		t.Errorf(".gitkeep contents non-empty: %q", data)
	}
}

func TestScaffoldFromSource_NoGitkeep(t *testing.T) {
	dir := t.TempDir()
	opts := ScaffoldOptions{
		Name: "ws", Org: "owner", Repo: "repo",
		IncludeGitkeep: false,
	}
	if err := ScaffoldFromSource(dir, opts); err != nil {
		t.Fatalf("ScaffoldFromSource: %v", err)
	}
	gitkeep := filepath.Join(dir, StateDir, "claude", ".gitkeep")
	if _, err := os.Stat(gitkeep); !os.IsNotExist(err) {
		t.Errorf("expected .gitkeep absent (IncludeGitkeep=false), got err=%v", err)
	}
}

// TestScaffoldFromSource_R16_VisibilityFromBool is the load-bearing
// adversarial fixture. It constructs a *github.Repo whose Private bool
// and Visibility string DISAGREE (deliberately mismatched), then derives
// ScaffoldOptions.Private from r.Private only. The assertion is that
// the scaffold visibility tracks the bool (not the string), proving
// that no code path reads Visibility into the scaffold body.
func TestScaffoldFromSource_R16_VisibilityFromBool(t *testing.T) {
	// Mismatched fixture: Private=true but Visibility="public".
	// If a future refactor accidentally plumbed Visibility, the
	// scaffold would emit `[groups.public]` and this test would fail.
	r := &github.Repo{
		Name:       "repo",
		Private:    true,
		Visibility: "public",
	}
	dir := t.TempDir()
	opts := ScaffoldOptions{
		Name:           "ws",
		Org:            "owner",
		Repo:           "repo",
		Private:        r.Private, // <- ONLY field consulted (R16)
		IncludeGitkeep: true,
	}
	if err := ScaffoldFromSource(dir, opts); err != nil {
		t.Fatalf("ScaffoldFromSource: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, StateDir, WorkspaceConfigFile))
	if err != nil {
		t.Fatalf("read workspace.toml: %v", err)
	}
	if !strings.Contains(string(data), `[groups.private]`) {
		t.Errorf("expected [groups.private] (Private=true wins over Visibility=public), got:\n%s", data)
	}
	if !strings.Contains(string(data), `visibility = "private"`) {
		t.Errorf(`expected visibility = "private", got:\n%s`, data)
	}
	if strings.Contains(string(data), `[groups.public]`) {
		t.Errorf("scaffold contains [groups.public] — Visibility string leaked into output:\n%s", data)
	}
}

// TestScaffoldFromSource_R16_NoVisibilityInjection asserts that a
// TOML-metacharacter-shaped Visibility string from the API cannot reach
// the scaffold body. The Repo carries a malicious-looking Visibility
// value; ScaffoldOptions takes only Private (bool); the scaffold body
// contains neither the injection literal nor anything resembling it.
func TestScaffoldFromSource_R16_NoVisibilityInjection(t *testing.T) {
	injected := "]\n[evil.section]\nbypass = true # "
	r := &github.Repo{
		Name:       "repo",
		Private:    false,
		Visibility: injected,
	}
	dir := t.TempDir()
	opts := ScaffoldOptions{
		Name:           "ws",
		Org:            "owner",
		Repo:           "repo",
		Private:        r.Private,
		IncludeGitkeep: true,
	}
	if err := ScaffoldFromSource(dir, opts); err != nil {
		t.Fatalf("ScaffoldFromSource: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, StateDir, WorkspaceConfigFile))
	if err != nil {
		t.Fatalf("read workspace.toml: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, `[groups.public]`) {
		t.Errorf("expected [groups.public] from Private=false, got:\n%s", body)
	}
	if strings.Contains(body, "evil") {
		t.Errorf("scaffold contains injected 'evil' substring:\n%s", body)
	}
	if strings.Contains(body, "bypass") {
		t.Errorf("scaffold contains injected 'bypass' substring:\n%s", body)
	}
}

// TestScaffoldFromSource_NoSecretOnDisk asserts PRD N5 — secrets that
// happen to be in the process environment (GH_TOKEN, etc.) MUST NOT
// appear in the scaffolded bytes. ScaffoldFromSource never reads env,
// so this is a regression guard against a future refactor that does.
func TestScaffoldFromSource_NoSecretOnDisk(t *testing.T) {
	const token = "test-fixture-token-DEADBEEF"
	t.Setenv("GH_TOKEN", token)
	t.Setenv("GITHUB_TOKEN", token)

	dir := t.TempDir()
	opts := ScaffoldOptions{
		Name: "ws", Org: "owner", Repo: "repo",
		Private:        true,
		IncludeGitkeep: true,
	}
	if err := ScaffoldFromSource(dir, opts); err != nil {
		t.Fatalf("ScaffoldFromSource: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, StateDir, WorkspaceConfigFile))
	if err != nil {
		t.Fatalf("read workspace.toml: %v", err)
	}
	if strings.Contains(string(data), token) {
		t.Errorf("scaffold leaked GH_TOKEN literal onto disk:\n%s", data)
	}
	// Defensive: also check the .gitkeep is genuinely empty (no
	// token-bearing default text).
	gk, err := os.ReadFile(filepath.Join(dir, StateDir, "claude", ".gitkeep"))
	if err != nil {
		t.Fatalf("read .gitkeep: %v", err)
	}
	if len(gk) != 0 {
		t.Errorf(".gitkeep has unexpected contents: %q", gk)
	}
}

func TestScaffoldFromSource_RequiresFields(t *testing.T) {
	dir := t.TempDir()
	if err := ScaffoldFromSource(dir, ScaffoldOptions{Org: "o", Repo: "r"}); err == nil {
		t.Error("expected error for empty Name")
	}
	if err := ScaffoldFromSource(dir, ScaffoldOptions{Name: "n", Repo: "r"}); err == nil {
		t.Error("expected error for empty Org")
	}
	if err := ScaffoldFromSource(dir, ScaffoldOptions{Name: "n", Org: "o"}); err == nil {
		t.Error("expected error for empty Repo")
	}
}
