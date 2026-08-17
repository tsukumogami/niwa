package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// codexFixture is a workspace whose content is configured layer by layer, so a
// test can remove exactly one layer and see what the composed files lose.
type codexFixture struct {
	// instance, group, repo are the content bodies for each layer. An empty
	// body omits that layer's config entry entirely.
	instance string
	group    string
	repo     string
	// committed, when non-empty, is written into the repo checkout as its own
	// committed AGENTS.md before the apply.
	committed string
}

// codexWorkspace writes the fixture to disk and returns the config dir, the
// workspace root, and the loaded config. The repo checkout is pre-created with
// a .git marker so the cloner is a no-op and no network is touched.
func codexWorkspace(t *testing.T, fx codexFixture) (string, string, *config.WorkspaceConfig) {
	t.Helper()

	root := t.TempDir()
	niwaDir := filepath.Join(root, ".niwa")
	contentDir := filepath.Join(niwaDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	b.WriteString(`
[workspace]
name = "ws"
content_dir = "claude"

[groups.tools]

[repos.app]
url = "https://example.invalid/app.git"
group = "tools"
`)
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(contentDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if fx.instance != "" {
		b.WriteString("\n[claude.content.workspace]\nsource = \"ws.md\"\n")
		write("ws.md", fx.instance)
	}
	if fx.group != "" {
		b.WriteString("\n[claude.content.groups.tools]\nsource = \"grp.md\"\n")
		write("grp.md", fx.group)
	}
	if fx.repo != "" {
		b.WriteString("\n[claude.content.repos.app]\nsource = \"app.md\"\n")
		write("app.md", fx.repo)
	}

	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	repoDir := filepath.Join(root, "ws", "tools", "app")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if fx.committed != "" {
		if err := os.WriteFile(filepath.Join(repoDir, "AGENTS.md"), []byte(fx.committed), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	return niwaDir, root, loaded.Config
}

// createCodexInstance runs Create over the fixture and returns the instance root.
func createCodexInstance(t *testing.T, fx codexFixture) string {
	t.Helper()

	niwaDir, root, cfg := codexWorkspace(t, fx)
	applier := NewApplier(&mockGitHubClient{})
	applier.Cloner = &Cloner{}

	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return instanceRoot
}

func readComposed(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// TestApply_ComposedCodexFilesCarryTheOuterLayers is the criterion the whole
// composition rule exists for: a Codex session's walk stops at the directory it
// starts in (or at the repository root), so each composed file has to carry
// every layer above it. A group file holding only the group layer, or an
// override holding only the repository layer, would pass a naive "the file
// exists and mentions this level" check and still deliver nothing from the
// workspace.
func TestApply_ComposedCodexFilesCarryTheOuterLayers(t *testing.T) {
	instanceRoot := createCodexInstance(t, codexFixture{
		instance: "sentinel-instance\n",
		group:    "sentinel-group\n",
		repo:     "sentinel-repo\n",
	})

	groupDoc := readComposed(t, filepath.Join(instanceRoot, "tools", "AGENTS.md"))
	if !strings.HasPrefix(groupDoc, CodexGenerationMarker+"\n") {
		t.Errorf("group AGENTS.md does not open with the generation marker; got:\n%s", groupDoc)
	}
	for _, want := range []string{"sentinel-instance", "sentinel-group"} {
		if !strings.Contains(groupDoc, want) {
			t.Errorf("group AGENTS.md missing %q; got:\n%s", want, groupDoc)
		}
	}
	if strings.Contains(groupDoc, "sentinel-repo") {
		t.Errorf("group AGENTS.md carries a layer from below it; got:\n%s", groupDoc)
	}

	override := readComposed(t, filepath.Join(instanceRoot, "tools", "app", CodexOverrideFileName))
	if !strings.HasPrefix(override, CodexGenerationMarker+"\n") {
		t.Errorf("override does not open with the generation marker; got:\n%s", override)
	}
	for _, want := range []string{"sentinel-instance", "sentinel-group", "sentinel-repo"} {
		if !strings.Contains(override, want) {
			t.Errorf("override missing %q -- the outer layers do not reach this repository; got:\n%s", want, override)
		}
	}

	// Outermost first, matching the order Codex concatenates a native chain.
	instanceAt := strings.Index(override, "sentinel-instance")
	groupAt := strings.Index(override, "sentinel-group")
	repoAt := strings.Index(override, "sentinel-repo")
	if !(instanceAt < groupAt && groupAt < repoAt) {
		t.Errorf("override layers are not ordered instance, group, repository; got:\n%s", override)
	}
}

// TestApply_OverrideInlinesCommittedContext covers the repository that ships its
// own AGENTS.md. AGENTS.override.md wins Codex's first-match, so the committed
// file loses the discovery slot -- inlining is what still gets its content to
// the session, and the committed file itself must come out of the apply
// untouched (PRD criterion 5, R12).
func TestApply_OverrideInlinesCommittedContext(t *testing.T) {
	const committed = "# app\n\ncommitted-repository-rules\n"
	instanceRoot := createCodexInstance(t, codexFixture{
		instance:  "sentinel-instance\n",
		group:     "sentinel-group\n",
		repo:      "sentinel-repo\n",
		committed: committed,
	})

	repoDir := filepath.Join(instanceRoot, "tools", "app")
	override := readComposed(t, filepath.Join(repoDir, CodexOverrideFileName))
	for _, want := range []string{"sentinel-instance", "sentinel-group", "sentinel-repo", "committed-repository-rules"} {
		if !strings.Contains(override, want) {
			t.Errorf("override missing %q; got:\n%s", want, override)
		}
	}
	if got := readComposed(t, filepath.Join(repoDir, "AGENTS.md")); got != committed {
		t.Errorf("committed AGENTS.md was modified by the apply:\ngot:  %q\nwant: %q", got, committed)
	}
}

// TestApply_NoConfiguredContentWritesNoOverride is the degenerate case the
// never-empty rule protects. A file at AGENTS.override.md claims the
// directory's single context slot whatever it holds, so with nothing to add
// niwa must write nothing at all and let native discovery deliver the
// repository's own committed file (PRD criterion 6).
func TestApply_NoConfiguredContentWritesNoOverride(t *testing.T) {
	const committed = "committed-repository-rules\n"
	instanceRoot := createCodexInstance(t, codexFixture{committed: committed})

	repoDir := filepath.Join(instanceRoot, "tools", "app")
	if _, err := os.Stat(filepath.Join(repoDir, CodexOverrideFileName)); !os.IsNotExist(err) {
		t.Errorf("an override was written with no configured content at any layer (stat err: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(instanceRoot, "tools", "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("a group AGENTS.md was written with no configured content at any layer (stat err: %v)", err)
	}
	if got := readComposed(t, filepath.Join(repoDir, "AGENTS.md")); got != committed {
		t.Errorf("committed AGENTS.md was modified:\ngot:  %q\nwant: %q", got, committed)
	}
}

// TestApply_OnlyInstanceContentStillReachesTheRepository guards the other side
// of the never-empty rule: "no content configured for this repository" is not
// the same condition as "no content configured anywhere". A repository with no
// entry of its own still needs the instance and group layers, which its
// session's walk cannot reach.
func TestApply_OnlyInstanceContentStillReachesTheRepository(t *testing.T) {
	instanceRoot := createCodexInstance(t, codexFixture{instance: "sentinel-instance\n"})

	override := readComposed(t, filepath.Join(instanceRoot, "tools", "app", CodexOverrideFileName))
	if !strings.Contains(override, "sentinel-instance") {
		t.Errorf("override missing the instance layer; got:\n%s", override)
	}
}

// TestApply_RecomposesRatherThanAccumulates pins regeneration: after the
// configured content changes, the composed files carry the new content and no
// trace of the old one (PRD criterion 19). Editing the repository's committed
// AGENTS.md refreshes the inlined copy the same way -- it is re-read on every
// apply, not captured once at clone time.
func TestApply_RecomposesRatherThanAccumulates(t *testing.T) {
	niwaDir, root, cfg := codexWorkspace(t, codexFixture{
		instance:  "sentinel-instance-v1\n",
		group:     "sentinel-group\n",
		repo:      "sentinel-repo\n",
		committed: "committed-v1\n",
	})

	applier := NewApplier(&mockGitHubClient{})
	applier.Cloner = &Cloner{}
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	repoDir := filepath.Join(instanceRoot, "tools", "app")
	contentDir := filepath.Join(niwaDir, "claude")
	if err := os.WriteFile(filepath.Join(contentDir, "ws.md"), []byte("sentinel-instance-v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "AGENTS.md"), []byte("committed-v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("re-Apply: %v", err)
	}

	override := readComposed(t, filepath.Join(repoDir, CodexOverrideFileName))
	groupDoc := readComposed(t, filepath.Join(instanceRoot, "tools", "AGENTS.md"))
	for _, doc := range []struct {
		name    string
		content string
	}{{"override", override}, {"group AGENTS.md", groupDoc}} {
		if !strings.Contains(doc.content, "sentinel-instance-v2") {
			t.Errorf("%s missing the new instance content; got:\n%s", doc.name, doc.content)
		}
		if strings.Contains(doc.content, "sentinel-instance-v1") {
			t.Errorf("%s still carries the previous instance content; got:\n%s", doc.name, doc.content)
		}
	}
	if !strings.Contains(override, "committed-v2") || strings.Contains(override, "committed-v1") {
		t.Errorf("override did not refresh the inlined committed content; got:\n%s", override)
	}
}

// TestApply_RemovedContentRemovesTheComposedFiles closes the loop on the
// never-empty rule across applies: once the last layer is de-configured, the
// files niwa wrote have to go, or the repository keeps a stale override in the
// slot its own committed file should hold.
func TestApply_RemovedContentRemovesTheComposedFiles(t *testing.T) {
	niwaDir, root, cfg := codexWorkspace(t, codexFixture{
		instance: "sentinel-instance\n",
		group:    "sentinel-group\n",
		repo:     "sentinel-repo\n",
	})

	applier := NewApplier(&mockGitHubClient{})
	applier.Cloner = &Cloner{}
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	stripped := `
[workspace]
name = "ws"
content_dir = "claude"

[groups.tools]

[repos.app]
url = "https://example.invalid/app.git"
group = "tools"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(stripped), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if err := applier.Apply(context.Background(), loaded.Config, niwaDir, instanceRoot); err != nil {
		t.Fatalf("re-Apply: %v", err)
	}

	for _, path := range []string{
		filepath.Join(instanceRoot, "tools", "AGENTS.md"),
		filepath.Join(instanceRoot, "tools", "app", CodexOverrideFileName),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s survived the removal of all configured content (stat err: %v)", path, err)
		}
	}
}

// TestApply_ClaudeDisabledRepoGetsNoOverride: `claude = false` opts a
// repository out of niwa-written context. Writing an override there would do
// worse than ignore the opt-out -- the override wins first-match, so it would
// take the context slot away from whatever the repository commits.
func TestApply_ClaudeDisabledRepoGetsNoOverride(t *testing.T) {
	niwaDir, root, _ := codexWorkspace(t, codexFixture{
		instance:  "sentinel-instance\n",
		group:     "sentinel-group\n",
		committed: "committed-repository-rules\n",
	})

	existing, err := os.ReadFile(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatal(err)
	}
	withOptOut := string(existing) + "\n[repos.app.claude]\nenabled = false\n"
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(withOptOut), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	applier := NewApplier(&mockGitHubClient{})
	applier.Cloner = &Cloner{}
	instanceRoot, err := applier.Create(context.Background(), loaded.Config, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := os.Stat(filepath.Join(instanceRoot, "tools", "app", CodexOverrideFileName)); !os.IsNotExist(err) {
		t.Errorf("an override was written into a repo with claude = false (stat err: %v)", err)
	}
	// Guard against a vacuous pass: this workspace does configure content, and
	// the group above the opted-out repo still gets its composed file.
	if _, err := os.Stat(filepath.Join(instanceRoot, "tools", "AGENTS.md")); err != nil {
		t.Errorf("group AGENTS.md missing, so the opt-out assertion above proves nothing: %v", err)
	}
}

// TestInstallRepoCodexOverride_ReportsInlineRefusal covers the caller half of
// the composer's regular-file-only rule: the refusal is scoped to the inline,
// so the override still carries every workspace layer, and the returned refusal
// is the only signal that the repository's own content is missing from it.
func TestInstallRepoCodexOverride_ReportsInlineRefusal(t *testing.T) {
	niwaDir, root, cfg := codexWorkspace(t, codexFixture{
		instance: "sentinel-instance\n",
		group:    "sentinel-group\n",
	})

	instanceRoot := filepath.Join(root, "ws")
	repoDir := filepath.Join(instanceRoot, "tools", "app")
	secret := filepath.Join(root, "secret.md")
	if err := os.WriteFile(secret, []byte("credentials-that-must-not-be-inlined\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(repoDir, "AGENTS.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	result, err := InstallRepoCodexOverride(cfg, niwaDir, "", instanceRoot, "tools", "app")
	if err != nil {
		t.Fatalf("InstallRepoCodexOverride: %v", err)
	}
	if result.Refusal == nil {
		t.Fatal("a symlinked committed AGENTS.md produced no refusal")
	}
	if len(result.WrittenFiles) != 1 {
		t.Fatalf("expected the override to be written anyway; got %v", result.WrittenFiles)
	}

	override := readComposed(t, result.WrittenFiles[0])
	if !strings.Contains(override, "sentinel-instance") || !strings.Contains(override, "sentinel-group") {
		t.Errorf("override lost the workspace layers over an inline refusal; got:\n%s", override)
	}
	if strings.Contains(override, "credentials-that-must-not-be-inlined") {
		t.Errorf("the symlink target was read into the override; got:\n%s", override)
	}
}
