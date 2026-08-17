package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// trustApplier builds an Applier with the trust seam wired to the real writer
// against a sandboxed config, which is what a CLI surface does in production.
func trustApplier(t *testing.T, configPath string) *Applier {
	t.Helper()

	applier := NewApplier(&mockGitHubClient{})
	applier.Cloner = &Cloner{}
	applier.EnsureCodexTrust = func(req CodexTrustRequest) (CodexTrustResult, error) {
		req.ConfigPath = configPath
		return EnsureCodexTrust(req)
	}
	return applier
}

func TestApplyWritesOneTrustEntryPerClonedRepository(t *testing.T) {
	configPath := codexTrustSandbox(t)
	niwaDir, root, cfg := dualAgentFixture(t, "")

	applier := trustApplier(t, configPath)
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	repoKey, err := CanonicalTrustKey(filepath.Join(instanceRoot, "tools", "app"))
	if err != nil {
		t.Fatal(err)
	}

	levels := decodeTrustLevels(t, configPath)
	if levels[repoKey] != "trusted" {
		t.Fatalf("no trust entry at the repository root %s; got %v", repoKey, levels)
	}
	if len(levels) != 1 {
		t.Errorf("expected exactly one entry, got %v", levels)
	}

	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.CodexTrustKeys) != 1 || state.CodexTrustKeys[0] != repoKey {
		t.Errorf("CodexTrustKeys = %v, want [%s]", state.CodexTrustKeys, repoKey)
	}
}

func TestApplyKeepsOneTrustEntryAcrossThreeApplies(t *testing.T) {
	configPath := codexTrustSandbox(t)
	niwaDir, root, cfg := dualAgentFixture(t, "")

	applier := trustApplier(t, configPath)
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
			t.Fatalf("apply %d: %v", i+1, err)
		}
	}

	if got := strings.Count(readFile(t, configPath), "[projects."); got != 1 {
		t.Errorf("found %d per-project table headers after three applies, want 1", got)
	}
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.CodexTrustKeys) != 1 {
		t.Errorf("CodexTrustKeys = %v, want one key", state.CodexTrustKeys)
	}
}

func TestApplyFinishesMaterializationThenFailsOnAnUnparseableCodexConfig(t *testing.T) {
	configPath := codexTrustSandbox(t)
	garbage := "= this never parsed [[\n"
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(garbage), 0o644); err != nil {
		t.Fatal(err)
	}

	niwaDir, root, cfg := dualAgentFixture(t, "")
	applier := trustApplier(t, configPath)
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err == nil {
		t.Fatal("expected Create to fail on an unparseable Codex config")
	}
	if !strings.Contains(err.Error(), configPath) {
		t.Errorf("error does not name the config: %v", err)
	}
	if instanceRoot == "" {
		t.Fatal("Create returned no instance root; the instance should survive a trust failure")
	}

	// The rest of materialization completed and was recorded: the failure is
	// fatal to the command, not to the instance.
	for _, rel := range []string{
		"AGENTS.md",
		filepath.Join("tools", "app", "AGENTS.override.md"),
		filepath.Join(CodexPayloadDirName, codexPayloadConfigName),
	} {
		if _, statErr := os.Stat(filepath.Join(instanceRoot, rel)); statErr != nil {
			t.Errorf("%s missing after a trust failure: %v", rel, statErr)
		}
	}
	if _, statErr := os.Stat(filepath.Join(instanceRoot, StateDir, StateFile)); statErr != nil {
		t.Errorf("instance state missing after a trust failure: %v", statErr)
	}
	if got := readFile(t, configPath); got != garbage {
		t.Errorf("the developer's config was rewritten; content = %q", got)
	}
}

func TestApplyCarriesTheTrustRecordForwardWhenTheSeamIsUnwired(t *testing.T) {
	niwaDir, root, cfg := dualAgentFixture(t, "")

	// No CLI surface wired the writer -- the shape every unit test sees. The
	// record from an earlier apply must survive rather than being cleared,
	// since it is the only authority for what may later be removed.
	applier := NewApplier(&mockGitHubClient{})
	applier.Cloner = &Cloner{}
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	state.CodexTrustKeys = []string{"/written/by/an/earlier/apply"}
	if err := SaveState(instanceRoot, state); err != nil {
		t.Fatal(err)
	}

	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	reloaded, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(reloaded.CodexTrustKeys) != 1 || reloaded.CodexTrustKeys[0] != "/written/by/an/earlier/apply" {
		t.Errorf("CodexTrustKeys = %v, want the prior record carried forward", reloaded.CodexTrustKeys)
	}
}

// twoRepoTrustFixture builds a workspace with two cloned repositories, so a
// conflict in one can be told apart from a workspace-wide failure to write
// anything. It returns the fixture paths plus the two checkouts.
func twoRepoTrustFixture(t *testing.T) (niwaDir, root string, cfg *config.WorkspaceConfig, appDir, libDir string) {
	t.Helper()

	root = t.TempDir()
	niwaDir = filepath.Join(root, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configTOML := `
[workspace]
name = "ws"

[groups.tools]

[repos.app]
url = "https://example.invalid/app.git"
group = "tools"

[repos.lib]
url = "https://example.invalid/lib.git"
group = "tools"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	appDir = filepath.Join(root, "ws", "tools", "app")
	libDir = filepath.Join(root, "ws", "tools", "lib")
	for _, dir := range []string{appDir, libDir} {
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	return niwaDir, root, loaded.Config, appDir, libDir
}

// occupyCodexName puts a repository's own `.codex` directory where niwa's
// delivery goes, replacing whatever is there. It carries no generation marker,
// which is what makes it foreign.
func occupyCodexName(t *testing.T, repoDir string) {
	t.Helper()
	link := filepath.Join(repoDir, CodexPayloadDirName)
	if err := os.RemoveAll(link); err != nil {
		t.Fatal(err)
	}
	writeFileT(t, filepath.Join(link, codexPayloadConfigName), "model = \"o3\"\napproval_policy = \"never\"\n")
}

func TestApplyWithholdsTrustFromARepositoryWhoseCodexNameIsOccupied(t *testing.T) {
	configPath := codexTrustSandbox(t)
	niwaDir, root, cfg, appDir, libDir := twoRepoTrustFixture(t)
	occupyCodexName(t, appDir)

	applier := trustApplier(t, configPath)
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	appKey := trustKey(t, appDir)
	libKey := trustKey(t, libDir)
	levels := decodeTrustLevels(t, configPath)
	if _, vouched := levels[appKey]; vouched {
		t.Error("niwa vouched for a repository it delivered no payload into")
	}
	if levels[libKey] != codexTrustLevel {
		t.Errorf("the clean repository lost its entry; levels = %v", levels)
	}

	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.CodexTrustKeys) != 1 || state.CodexTrustKeys[0] != libKey {
		t.Errorf("CodexTrustKeys = %v, want only %s", state.CodexTrustKeys, libKey)
	}
}

// TestApplyRetractsARecordedEntryWhenTheRepositoryBecomesConflicted runs the
// whole sequence through the pipeline: a repository trusted on one apply,
// conflicted on the next, and then answered for by the developer at the same
// key. Only the middle step is niwa's to undo.
func TestApplyRetractsARecordedEntryWhenTheRepositoryBecomesConflicted(t *testing.T) {
	configPath := codexTrustSandbox(t)
	niwaDir, root, cfg, appDir, libDir := twoRepoTrustFixture(t)

	out := &bytes.Buffer{}
	applier := trustApplier(t, configPath)
	applier.Reporter = NewReporterWithTTY(out, false)
	instanceRoot, err := applier.Create(context.Background(), cfg, niwaDir, root, "ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	appKey := trustKey(t, appDir)
	libKey := trustKey(t, libDir)
	if levels := decodeTrustLevels(t, configPath); levels[appKey] != codexTrustLevel {
		t.Fatalf("the first apply did not trust the clean repository; levels = %v", levels)
	}

	occupyCodexName(t, appDir)
	out.Reset()
	if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !strings.Contains(out.String(), appKey) {
		t.Errorf("the apply did not report the withdrawn trust entry:\n%s", out.String())
	}

	levels := decodeTrustLevels(t, configPath)
	if _, vouched := levels[appKey]; vouched {
		t.Error("the entry survived the conflict that made niwa stop delivering the payload")
	}
	if levels[libKey] != codexTrustLevel {
		t.Errorf("the clean repository's entry went with it; levels = %v", levels)
	}
	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.CodexTrustKeys) != 1 || state.CodexTrustKeys[0] != libKey {
		t.Errorf("CodexTrustKeys = %v, want the retracted key cleared", state.CodexTrustKeys)
	}

	// The developer is prompted at session start and says yes; Codex writes the
	// same key back. Nothing in the file distinguishes that entry from the one
	// niwa just removed, and no later apply may touch it.
	answered := readFile(t, configPath) + developerAnswer(appKey)
	writeFileT(t, configPath, answered)

	for i := 0; i < 2; i++ {
		if err := applier.Apply(context.Background(), cfg, niwaDir, instanceRoot); err != nil {
			t.Fatalf("apply %d after the developer answered: %v", i+1, err)
		}
	}
	if got := readFile(t, configPath); got != answered {
		t.Errorf("the developer's own answer did not survive later applies:\n got %q\nwant %q", got, answered)
	}
	if state, err = LoadState(instanceRoot); err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	for _, key := range state.CodexTrustKeys {
		if key == appKey {
			t.Error("niwa recorded the developer's own entry as its own")
		}
	}
}

// trustKey is CanonicalTrustKey with the test's error handling.
func trustKey(t *testing.T, dir string) string {
	t.Helper()
	key, err := CanonicalTrustKey(dir)
	if err != nil {
		t.Fatalf("CanonicalTrustKey(%s): %v", dir, err)
	}
	return key
}
