package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
