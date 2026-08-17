package workspace

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// payloadFixture describes a workspace whose repo is pre-created (so the cloner
// is a no-op and no network is touched) and whose Claude plugin configuration is
// written verbatim into workspace.toml.
type payloadFixture struct {
	// instance is the instance-layer content body. Empty omits the entry.
	instance string
	// claudeBlock is appended to workspace.toml as-is: the [claude] plugins key
	// and any [[claude.marketplaces]] tables the test needs.
	claudeBlock string
}

// payloadWorkspace writes the fixture and returns the .niwa dir, the workspace
// root, and the loaded config.
func payloadWorkspace(t *testing.T, fx payloadFixture) (string, string, *config.WorkspaceConfig) {
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
	if fx.instance != "" {
		b.WriteString("\n[claude.content.workspace]\nsource = \"ws.md\"\n")
		if err := os.WriteFile(filepath.Join(contentDir, "ws.md"), []byte(fx.instance), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	b.WriteString(fx.claudeBlock)

	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, "ws", "tools", "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	return niwaDir, root, loaded.Config
}

// pluginSource is one plugin inside a marketplace fixture: the name the
// manifest declares and the source directory it declares for it.
type pluginSource struct {
	name string
	dir  string
}

// writeMarketplaceTree lays down a marketplace root: the marketplace manifest
// plus, for each plugin, a whole plugin tree -- manifest, a namespaced skill,
// and the plugin-root references/ and scripts/ content that skills point at and
// that a loose per-skill copy would orphan.
func writeMarketplaceTree(t *testing.T, marketplaceRoot, marketplaceName string, plugins []pluginSource) {
	t.Helper()

	var entries []string
	for _, p := range plugins {
		entries = append(entries, `{"name": "`+p.name+`", "source": "`+p.dir+`"}`)
	}
	manifest := `{"name": "` + marketplaceName + `", "plugins": [` + strings.Join(entries, ", ") + `]}`
	writeFixtureFile(t, filepath.Join(marketplaceRoot, ".claude-plugin", "marketplace.json"), manifest)

	for _, p := range plugins {
		pluginRoot := filepath.Join(marketplaceRoot, p.dir)
		writeFixtureFile(t, filepath.Join(pluginRoot, ".claude-plugin", "plugin.json"), `{"name": "`+p.name+`", "version": "1.0.0"}`)
		writeFixtureFile(t, filepath.Join(pluginRoot, "skills", "review", "SKILL.md"), "---\nname: review\n---\n\nSee ${CLAUDE_PLUGIN_ROOT}/references/checklist.md\n")
		writeFixtureFile(t, filepath.Join(pluginRoot, "references", "checklist.md"), "plugin-root reference content\n")
		writeFixtureFile(t, filepath.Join(pluginRoot, "scripts", "run.sh"), "#!/bin/sh\necho ${CLAUDE_PLUGIN_ROOT:-.}\n")
	}
}

func writeFixtureFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// withClaudePluginsRoot points the github-marketplace resolution at a fixture
// tree instead of the developer's real home directory.
func withClaudePluginsRoot(t *testing.T, dir string) {
	t.Helper()
	prev := claudePluginsRoot
	claudePluginsRoot = func() (string, error) { return dir, nil }
	t.Cleanup(func() { claudePluginsRoot = prev })
}

func createPayloadInstance(t *testing.T, niwaDir, root string, cfg *config.WorkspaceConfig) (*Applier, string) {
	t.Helper()
	applier := NewApplier(&mockGitHubClient{})
	applier.Cloner = &Cloner{}
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return applier, instanceRoot
}

func payloadConfigPath(instanceRoot string) string {
	return filepath.Join(instanceRoot, CodexPayloadDirName, codexPayloadConfigName)
}

func skillsLinkPath(instanceRoot, plugin string) string {
	return filepath.Join(instanceRoot, CodexPayloadDirName, codexPayloadSkillsDirName, plugin)
}

// declaredBudget reads project_doc_max_bytes back out of the written config.
func declaredBudget(t *testing.T, instanceRoot string) int {
	t.Helper()
	data, err := os.ReadFile(payloadConfigPath(instanceRoot))
	if err != nil {
		t.Fatalf("reading payload config: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(key) != "project_doc_max_bytes" {
			continue
		}
		n, convErr := strconv.Atoi(strings.TrimSpace(value))
		if convErr != nil {
			t.Fatalf("project_doc_max_bytes is not a number: %q", value)
		}
		return n
	}
	t.Fatalf("payload config declares no project_doc_max_bytes:\n%s", data)
	return 0
}

func fileSize(t *testing.T, path string) int {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return int(fi.Size())
}

// TestApply_BudgetIsDeclaredWithHeadroom is the criterion an exact-fit
// implementation fails. The chain shares one counter, spends it outermost-first,
// and truncates with no marker in the text and nothing on stderr -- so a budget
// sized to precisely today's bytes passes a naive "it all fits" test and starts
// silently cutting the innermost layer the moment any file in the chain grows.
// The margin is the whole defense, so the margin is what is asserted.
func TestApply_BudgetIsDeclaredWithHeadroom(t *testing.T) {
	niwaDir, root, cfg := payloadWorkspace(t, payloadFixture{
		instance: strings.Repeat("instance layer content\n", 2600),
	})
	// A committed context file in a subdirectory below the repository root: read
	// after the override, out of the same counter, and free to grow between
	// applies with no signal at all.
	writeFixtureFile(t, filepath.Join(root, "ws", "tools", "app", "docs", "AGENTS.md"), strings.Repeat("subdirectory context\n", 500))

	_, instanceRoot := createPayloadInstance(t, niwaDir, root, cfg)

	chain := fileSize(t, filepath.Join(instanceRoot, "tools", "app", CodexOverrideFileName)) +
		fileSize(t, filepath.Join(instanceRoot, "tools", "app", "docs", "AGENTS.md"))
	budget := declaredBudget(t, instanceRoot)

	if budget < 2*chain {
		t.Errorf("declared budget %d is not even double the %d-byte chain on disk: an exact-fit budget truncates the innermost layer as soon as anything grows", budget, chain)
	}
	if budget < chain+32768 {
		t.Errorf("declared budget %d leaves less than Codex's own 32768-byte default as absolute headroom over the %d-byte chain", budget, chain)
	}
}

// TestCodexBudgetFor_FloorAndFactor pins the two halves of the sizing rule
// directly: a tiny chain still declares a generous floor, and a large one is
// multiplied rather than matched.
func TestCodexBudgetFor_FloorAndFactor(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.md")
	writeFixtureFile(t, small, "tiny\n")

	if got := codexBudgetFor(CodexBudgetInputs{ComposedFiles: []string{small}}); got < codexBudgetFloor {
		t.Errorf("a tiny chain declared %d, below the %d floor", got, codexBudgetFloor)
	}

	big := filepath.Join(dir, "big.md")
	writeFixtureFile(t, big, strings.Repeat("x", 200000))
	got := codexBudgetFor(CodexBudgetInputs{ComposedFiles: []string{big}})
	if got < 200000*2 {
		t.Errorf("a 200000-byte chain declared %d, which is not headroom", got)
	}
}

// TestApply_RepoSourcedPluginLinksWholeTree covers the repository-sourced
// marketplace kind: the plugin root is the marketplace root joined with the
// source directory the marketplace manifest declares.
//
// The assertions are about the unit of delivery. The link must land on the
// plugin directory itself -- the one carrying plugin.json -- because that is
// what Codex probes for when it namespaces a skill, and because the plugin-root
// references/ and scripts/ content lives above the skill directories and would
// exist at no path at all if skills were copied loose.
func TestApply_RepoSourcedPluginLinksWholeTree(t *testing.T) {
	niwaDir, root, cfg := payloadWorkspace(t, payloadFixture{
		instance: "sentinel-instance\n",
		claudeBlock: `
[claude]
plugins = ["tools-plugin@app"]

[[claude.marketplaces]]
source = "repo:app/.claude-plugin/marketplace.json"
`,
	})
	marketplaceRoot := filepath.Join(root, "ws", "tools", "app")
	writeMarketplaceTree(t, marketplaceRoot, "app", []pluginSource{{name: "tools-plugin", dir: "./plugins/tools-plugin"}})

	_, instanceRoot := createPayloadInstance(t, niwaDir, root, cfg)

	wantRoot := filepath.Join(marketplaceRoot, "plugins", "tools-plugin")
	assertWholePluginLink(t, skillsLinkPath(instanceRoot, "tools-plugin"), wantRoot)
}

// TestApply_GithubSourcedPluginLinksWholeTree covers the other marketplace kind.
// niwa computes no on-disk path for a github source today -- the tree lives in
// Claude Code's user-global plugin directory, put there by a best-effort
// pre-warm -- but once that root is found the join is the same one the
// repository-sourced kind does.
func TestApply_GithubSourcedPluginLinksWholeTree(t *testing.T) {
	pluginsRoot := t.TempDir()
	withClaudePluginsRoot(t, pluginsRoot)

	niwaDir, root, cfg := payloadWorkspace(t, payloadFixture{
		instance: "sentinel-instance\n",
		claudeBlock: `
[claude]
plugins = ["shirabe@shirabe"]

[[claude.marketplaces]]
source = "tsukumogami/shirabe"
track = "main"
`,
	})
	marketplaceRoot := filepath.Join(pluginsRoot, "marketplaces", "shirabe")
	// A marketplace whose single plugin is the repository root itself, which is
	// the common shape for this kind.
	writeMarketplaceTree(t, marketplaceRoot, "shirabe", []pluginSource{{name: "shirabe", dir: "./"}})

	_, instanceRoot := createPayloadInstance(t, niwaDir, root, cfg)

	assertWholePluginLink(t, skillsLinkPath(instanceRoot, "shirabe"), marketplaceRoot)
}

// assertWholePluginLink checks that linkPath is a symlink onto wantRoot, that
// the target is the plugin root rather than a skill subdirectory, and that every
// file under the source arrives byte-identical through the link.
func assertWholePluginLink(t *testing.T, linkPath, wantRoot string) {
	t.Helper()

	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("no skills link at %s: %v", linkPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink (mode %s)", linkPath, info.Mode())
	}

	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("reading link %s: %v", linkPath, err)
	}
	if target != wantRoot {
		t.Fatalf("link points at %s, want the whole plugin root %s", target, wantRoot)
	}
	if filepath.Base(target) == "skills" || filepath.Base(filepath.Dir(target)) == "skills" {
		t.Fatalf("link points inside the skills directory (%s): the unit of delivery is the plugin, not the skill", target)
	}

	// The plugin manifest is what gives every skill its <plugin>:<skill> name,
	// and it sits at the plugin root, not beside the skills.
	if _, err := os.Stat(filepath.Join(linkPath, ".claude-plugin", "plugin.json")); err != nil {
		t.Errorf("plugin manifest not reachable through the link: %v", err)
	}

	// Every file, verbatim: no frontmatter rewriting, no variable substitution,
	// nothing added and nothing left out.
	err = filepath.WalkDir(wantRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(wantRoot, path)
		if relErr != nil {
			return relErr
		}
		want, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		got, readErr := os.ReadFile(filepath.Join(linkPath, rel))
		if readErr != nil {
			t.Errorf("%s is not reachable through the link: %v", rel, readErr)
			return nil
		}
		if string(got) != string(want) {
			t.Errorf("%s differs through the link: content was transformed", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the plugin source: %v", err)
	}
}

// TestApply_MissingPluginRootIsReportedNotSilentlySkipped is driver D4: the
// pre-warm that installs a plugin is best-effort, and a Codex session has no
// startup self-heal to fall back on. So a plugin root that is absent at apply
// time gets no link and a report naming the plugin and the path -- and the next
// apply materializes the link once the root exists.
func TestApply_MissingPluginRootIsReportedNotSilentlySkipped(t *testing.T) {
	pluginsRoot := t.TempDir()
	withClaudePluginsRoot(t, pluginsRoot)

	niwaDir, root, cfg := payloadWorkspace(t, payloadFixture{
		instance: "sentinel-instance\n",
		claudeBlock: `
[claude]
plugins = ["shirabe@shirabe"]

[[claude.marketplaces]]
source = "tsukumogami/shirabe"
track = "main"
`,
	})
	marketplaceRoot := filepath.Join(pluginsRoot, "marketplaces", "shirabe")
	// The marketplace is cloned but the plugin itself was never installed --
	// exactly what a skipped or failed pre-warm leaves behind.
	writeMarketplaceTree(t, marketplaceRoot, "shirabe", []pluginSource{{name: "shirabe", dir: "./plugins/shirabe"}})
	if err := os.RemoveAll(filepath.Join(marketplaceRoot, "plugins")); err != nil {
		t.Fatal(err)
	}

	reports := reportsFromPayload(t, cfg, filepath.Join(root, "ws"), marketplaceRoot)
	if len(reports) != 1 {
		t.Fatalf("want exactly one missing-root report, got %d: %v", len(reports), reports)
	}
	report := reports[0]
	if !strings.Contains(report, "shirabe") {
		t.Errorf("report does not name the plugin: %s", report)
	}
	wantPath := filepath.Join(marketplaceRoot, "plugins", "shirabe")
	if !strings.Contains(report, wantPath) {
		t.Errorf("report does not name the path it expected (%s): %s", wantPath, report)
	}

	applier, instanceRoot := createPayloadInstance(t, niwaDir, root, cfg)
	if _, err := os.Lstat(skillsLinkPath(instanceRoot, "shirabe")); !os.IsNotExist(err) {
		t.Errorf("a link was written for a plugin whose root is missing (lstat err: %v)", err)
	}
	if _, err := os.Stat(payloadConfigPath(instanceRoot)); err != nil {
		t.Errorf("apply did not complete the payload despite the missing root: %v", err)
	}

	// Install the plugin and re-apply: the link appears, no other action needed.
	writeMarketplaceTree(t, marketplaceRoot, "shirabe", []pluginSource{{name: "shirabe", dir: "./plugins/shirabe"}})
	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("re-Apply: %v", err)
	}
	assertWholePluginLink(t, skillsLinkPath(instanceRoot, "shirabe"), filepath.Join(marketplaceRoot, "plugins", "shirabe"))

	// And the reverse: a root that vanishes after the link was created leaves no
	// dangling link behind. Codex skips a dangling layer without complaint, so
	// leaving one would trade the report for silence.
	if err := os.RemoveAll(filepath.Join(marketplaceRoot, "plugins")); err != nil {
		t.Fatal(err)
	}
	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("re-Apply after the root vanished: %v", err)
	}
	if _, err := os.Lstat(skillsLinkPath(instanceRoot, "shirabe")); !os.IsNotExist(err) {
		t.Errorf("a link to a vanished plugin root survived (lstat err: %v)", err)
	}
}

// reportsFromPayload runs the payload writer directly against a scratch instance
// root so the missing-root reports can be read as values rather than scraped out
// of apply's warning stream.
func reportsFromPayload(t *testing.T, cfg *config.WorkspaceConfig, instanceRoot, _ string) []string {
	t.Helper()
	scratch := t.TempDir()
	result, err := InstallCodexPayload(cfg, scratch, map[string]string{"app": filepath.Join(instanceRoot, "tools", "app")}, CodexBudgetInputs{})
	if err != nil {
		t.Fatalf("InstallCodexPayload: %v", err)
	}
	reports := make([]string, 0, len(result.MissingRoots))
	for _, m := range result.MissingRoots {
		reports = append(reports, m.String())
	}
	return reports
}

// TestApply_PayloadReconcilesAcrossApplies: re-materialization is regeneration,
// not append. Three applies leave exactly what one apply leaves, and a plugin
// removed from the config takes its link with it.
func TestApply_PayloadReconcilesAcrossApplies(t *testing.T) {
	niwaDir, root, cfg := payloadWorkspace(t, payloadFixture{
		instance: "sentinel-instance\n",
		claudeBlock: `
[claude]
plugins = ["alpha@app", "beta@app"]

[[claude.marketplaces]]
source = "repo:app/.claude-plugin/marketplace.json"
`,
	})
	marketplaceRoot := filepath.Join(root, "ws", "tools", "app")
	writeMarketplaceTree(t, marketplaceRoot, "app", []pluginSource{
		{name: "alpha", dir: "./plugins/alpha"},
		{name: "beta", dir: "./plugins/beta"},
	})

	applier, instanceRoot := createPayloadInstance(t, niwaDir, root, cfg)
	for i := 0; i < 2; i++ {
		if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
			t.Fatalf("re-Apply %d: %v", i, err)
		}
	}

	payloadDir := filepath.Join(instanceRoot, CodexPayloadDirName)
	entries, err := os.ReadDir(payloadDir)
	if err != nil {
		t.Fatalf("reading payload dir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 2 {
		t.Errorf("payload holds %v after three applies, want exactly the config and the skills directory", names)
	}

	links, err := os.ReadDir(filepath.Join(payloadDir, codexPayloadSkillsDirName))
	if err != nil {
		t.Fatalf("reading skills dir: %v", err)
	}
	if len(links) != 2 {
		t.Errorf("skills directory holds %d entries after three applies, want one per configured plugin", len(links))
	}

	// De-configure beta: its link has to go, or the payload keeps advertising a
	// plugin the workspace no longer installs.
	deconfigured := strings.Replace(readComposed(t, filepath.Join(niwaDir, "workspace.toml")), `plugins = ["alpha@app", "beta@app"]`, `plugins = ["alpha@app"]`, 1)
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(deconfigured), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("reloading config: %v", err)
	}
	if err := applier.Apply(context.Background(), loaded.Config, niwaDir, instanceRoot); err != nil {
		t.Fatalf("re-Apply after de-configuring: %v", err)
	}

	if _, err := os.Lstat(skillsLinkPath(instanceRoot, "beta")); !os.IsNotExist(err) {
		t.Errorf("de-configured plugin's link survived (lstat err: %v)", err)
	}
	if _, err := os.Lstat(skillsLinkPath(instanceRoot, "alpha")); err != nil {
		t.Errorf("still-configured plugin lost its link: %v", err)
	}
}

// TestApply_PayloadCarriesNoAuthOrHookMaterial pins Decisions 5 and 6 on the one
// file niwa writes into the payload: the budget is its whole job. Credentials
// stay with the developer's own login, and niwa writes no hook definitions and
// no hook-state entries anywhere.
func TestApply_PayloadCarriesNoAuthOrHookMaterial(t *testing.T) {
	niwaDir, root, cfg := payloadWorkspace(t, payloadFixture{instance: "sentinel-instance\n"})
	_, instanceRoot := createPayloadInstance(t, niwaDir, root, cfg)

	body := readComposed(t, payloadConfigPath(instanceRoot))
	for _, forbidden := range []string{"OPENAI_API_KEY", "forced_login_method", "api_key", "hooks", "hook"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("payload config mentions %q:\n%s", forbidden, body)
		}
	}
	if !strings.Contains(body, "project_doc_max_bytes") {
		t.Errorf("payload config declares no budget:\n%s", body)
	}
}

// TestReconcileCodexSkillLinks_RepairsAndLeavesForeignEntries: a link whose
// target moved is niwa's to repair, and a real directory that somehow landed in
// the payload is not niwa's to delete.
func TestReconcileCodexSkillLinks_RepairsAndLeavesForeignEntries(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "skills")
	newRoot := filepath.Join(dir, "new-root")
	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "old-root"), filepath.Join(skillsDir, "alpha")); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(skillsDir, "beta")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}

	links, warnings, err := reconcileCodexSkillLinks(skillsDir, []codexPluginRoot{
		{name: "alpha", root: newRoot},
		{name: "beta", root: newRoot},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	target, err := os.Readlink(filepath.Join(skillsDir, "alpha"))
	if err != nil {
		t.Fatalf("reading repaired link: %v", err)
	}
	if target != newRoot {
		t.Errorf("stale link was not retargeted: %s", target)
	}
	if len(links) != 1 {
		t.Errorf("want one link written, got %v", links)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], foreign) {
		t.Errorf("a non-symlink entry was not reported: %v", warnings)
	}
	if fi, statErr := os.Lstat(foreign); statErr != nil || !fi.IsDir() {
		t.Errorf("the non-symlink entry was removed (stat err: %v)", statErr)
	}
}
