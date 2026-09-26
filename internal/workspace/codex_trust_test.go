package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every test here writes into a scratch home. The writer takes the home as
// data and never resolves one itself, and CODEX_HOME -- the developer's own
// override, which the writer honors -- is neutralized per test, so a developer
// running the suite with one set does not have their real config edited.

// trustScratch returns a scratch home directory with CODEX_HOME cleared, plus
// the config path the writer will resolve under it.
func trustScratch(t *testing.T) (home, configPath string) {
	t.Helper()
	t.Setenv(codexHomeEnv, "")
	home = t.TempDir()
	return home, filepath.Join(home, ".codex", "config.toml")
}

// trustRepo makes a directory to stand in for a cloned repository and returns
// the canonical key the writer will file it under.
func trustRepo(t *testing.T, parent, name string) (dir, key string) {
	t.Helper()
	dir = filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	key, err := CanonicalTrustKey(dir)
	if err != nil {
		t.Fatalf("canonicalizing %s: %v", dir, err)
	}
	return dir, key
}

// readTrustConfig returns the config's bytes, treating an absent file as empty.
func readTrustConfig(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

// countTrustBlocks counts the [projects."<key>"] headers for one key.
func countTrustBlocks(config, key string) int {
	return strings.Count(config, "[projects."+quoteTOMLKey(key)+"]")
}

// TestEnsureCodexTrustIsSingularPerRepositoryAcrossApplies is the repeated-apply
// property: three runs leave one entry per repository, not three, and the
// record they persist stops moving after the first.
func TestEnsureCodexTrustIsSingularPerRepositoryAcrossApplies(t *testing.T) {
	home, configPath := trustScratch(t)
	instance := t.TempDir()
	alphaDir, alphaKey := trustRepo(t, instance, "alpha")
	betaDir, betaKey := trustRepo(t, instance, "beta")

	var recorded []string
	for run := 1; run <= 3; run++ {
		res, err := EnsureCodexTrust(CodexTrustRequest{
			DeveloperHome: home,
			RepoRoots:     []string{alphaDir, betaDir},
			Recorded:      recorded,
		})
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if run > 1 && len(res.Added) != 0 {
			t.Errorf("run %d added %v; a repeated apply must add nothing", run, res.Added)
		}
		recorded = res.Recorded
	}

	if len(recorded) != 2 || recorded[0] > recorded[1] {
		t.Errorf("record is %v, want both keys in sorted order", recorded)
	}
	config := readTrustConfig(t, configPath)
	for _, key := range []string{alphaKey, betaKey} {
		if n := countTrustBlocks(config, key); n != 1 {
			t.Errorf("%s appears in %d blocks, want exactly 1:\n%s", key, n, config)
		}
	}
	if !strings.Contains(config, codexTrustLevelKey+` = "`+codexTrustLevel+`"`) {
		t.Errorf("no trust verdict written:\n%s", config)
	}
}

// TestEnsureCodexTrustCreatesAPrivateConfig covers the first-run case: no file
// at all. What niwa creates in the developer's home is theirs alone to read.
func TestEnsureCodexTrustCreatesAPrivateConfig(t *testing.T) {
	home, configPath := trustScratch(t)
	repoDir, _ := trustRepo(t, t.TempDir(), "alpha")

	if _, err := EnsureCodexTrust(CodexTrustRequest{DeveloperHome: home, RepoRoots: []string{repoDir}}); err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("stat %s: %v", configPath, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("created config mode is %o, want 600", got)
	}
}

// TestEnsureCodexTrustLeavesTheDeveloperSOwnContentAlone is the additive half of
// the write discipline: every pre-existing byte survives, in order, and the
// developer's own project entry keeps its own settings.
func TestEnsureCodexTrustLeavesTheDeveloperSOwnContentAlone(t *testing.T) {
	home, configPath := trustScratch(t)
	repoDir, repoKey := trustRepo(t, t.TempDir(), "alpha")

	existing := "model = \"o3\"\napproval_policy = \"untrusted\"\n\n[projects.\"/somewhere/else\"]\ntrust_level = \"trusted\"\n"
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureCodexTrust(CodexTrustRequest{DeveloperHome: home, RepoRoots: []string{repoDir}}); err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}

	config := readTrustConfig(t, configPath)
	if !strings.HasPrefix(config, existing) {
		t.Errorf("the developer's own bytes were rewritten:\n%s", config)
	}
	if countTrustBlocks(config, repoKey) != 1 {
		t.Errorf("niwa's own entry is missing or duplicated:\n%s", config)
	}
}

// TestEnsureCodexTrustRetractsOnlyWhatItRecordedWriting is the safety property
// the record exists for. Codex writes an identically shaped entry when the
// developer answers its own trust prompt, so an entry absent from the record is
// never niwa's to remove -- even when it is withheld and looks exactly like one
// niwa would have written.
func TestEnsureCodexTrustRetractsOnlyWhatItRecordedWriting(t *testing.T) {
	home, configPath := trustScratch(t)
	instance := t.TempDir()
	niwaDir, niwaKey := trustRepo(t, instance, "niwa-wrote-this")
	devDir, devKey := trustRepo(t, instance, "developer-answered-this")

	// Both entries are in the file and look alike; only one is recorded.
	seeded := "[projects." + quoteTOMLKey(niwaKey) + "]\ntrust_level = \"trusted\"\n\n" +
		"[projects." + quoteTOMLKey(devKey) + "]\ntrust_level = \"trusted\"\n"
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(seeded), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := EnsureCodexTrust(CodexTrustRequest{
		DeveloperHome: home,
		RepoRoots:     []string{niwaDir, devDir},
		Recorded:      []string{niwaKey},
		Conflicted:    []string{niwaDir, devDir},
	})
	if err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}

	if len(res.Removed) != 1 || res.Removed[0] != niwaKey {
		t.Errorf("Removed = %v, want only the recorded key %s", res.Removed, niwaKey)
	}
	config := readTrustConfig(t, configPath)
	if countTrustBlocks(config, niwaKey) != 0 {
		t.Errorf("niwa's own recorded entry survived a retraction:\n%s", config)
	}
	if countTrustBlocks(config, devKey) != 1 {
		t.Errorf("the developer's own answer was removed:\n%s", config)
	}
	if len(res.Recorded) != 0 {
		t.Errorf("Recorded = %v, want empty: niwa owns nothing after retracting its last key", res.Recorded)
	}
	if len(res.Warnings) == 0 {
		t.Error("a retraction was silent; a trust entry disappearing is not something niwa does quietly")
	}
}

// TestEnsureCodexTrustWithholdsAConflictedRepository checks the other half of
// the withhold set: a conflicted repository gets no entry in the first place,
// even when it is handed over as a root to trust.
func TestEnsureCodexTrustWithholdsAConflictedRepository(t *testing.T) {
	home, configPath := trustScratch(t)
	instance := t.TempDir()
	okDir, okKey := trustRepo(t, instance, "fine")
	badDir, badKey := trustRepo(t, instance, "conflicted")

	res, err := EnsureCodexTrust(CodexTrustRequest{
		DeveloperHome: home,
		RepoRoots:     []string{okDir, badDir},
		Conflicted:    []string{badDir},
	})
	if err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}

	config := readTrustConfig(t, configPath)
	if countTrustBlocks(config, okKey) != 1 {
		t.Errorf("the unconflicted repository got no entry:\n%s", config)
	}
	if countTrustBlocks(config, badKey) != 0 {
		t.Errorf("a conflicted repository was vouched for:\n%s", config)
	}
	if len(res.Recorded) != 1 || res.Recorded[0] != okKey {
		t.Errorf("Recorded = %v, want only %s", res.Recorded, okKey)
	}
}

// TestEnsureCodexTrustKeysEntriesByTheResolvedPath is why canonicalization is
// substance rather than hygiene: Codex resolves the working directory before
// looking trust up, so an entry keyed through a symlink is silently miskeyed
// and the session it was meant to enable runs read-only with no error anywhere.
func TestEnsureCodexTrustKeysEntriesByTheResolvedPath(t *testing.T) {
	home, configPath := trustScratch(t)
	real := t.TempDir()
	_, repoKey := trustRepo(t, real, "alpha")

	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	viaLink := filepath.Join(link, "alpha")

	if _, err := EnsureCodexTrust(CodexTrustRequest{DeveloperHome: home, RepoRoots: []string{viaLink}}); err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}

	config := readTrustConfig(t, configPath)
	if countTrustBlocks(config, repoKey) != 1 {
		t.Errorf("entry is not keyed by the resolved path %s:\n%s", repoKey, config)
	}
	if strings.Contains(config, viaLink) {
		t.Errorf("entry is keyed by the symlinked path:\n%s", config)
	}
}

// TestEnsureCodexTrustToleratesAMalformedConfig covers R17 from the malformed
// side: a developer's own broken file is left byte-untouched, reported, and
// fails neither create nor apply.
func TestEnsureCodexTrustToleratesAMalformedConfig(t *testing.T) {
	home, configPath := trustScratch(t)
	repoDir, _ := trustRepo(t, t.TempDir(), "alpha")

	broken := "this is not = = valid toml [[\n"
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := EnsureCodexTrust(CodexTrustRequest{DeveloperHome: home, RepoRoots: []string{repoDir}})
	if err != nil {
		t.Fatalf("a malformed developer config failed the apply: %v", err)
	}
	if got := readTrustConfig(t, configPath); got != broken {
		t.Errorf("the malformed config was rewritten:\n%s", got)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], configPath) {
		t.Errorf("Warnings = %v, want one naming %s", res.Warnings, configPath)
	}
}

// TestEnsureCodexTrustToleratesAnUnreadableConfig is the same requirement from
// the unreadable side.
func TestEnsureCodexTrustToleratesAnUnreadableConfig(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unreadable file is still readable")
	}
	home, configPath := trustScratch(t)
	repoDir, _ := trustRepo(t, t.TempDir(), "alpha")

	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("model = \"o3\"\n"), 0o000); err != nil {
		t.Fatal(err)
	}

	res, err := EnsureCodexTrust(CodexTrustRequest{DeveloperHome: home, RepoRoots: []string{repoDir}})
	if err != nil {
		t.Fatalf("an unreadable developer config failed the apply: %v", err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], configPath) {
		t.Errorf("Warnings = %v, want one naming %s", res.Warnings, configPath)
	}
	if len(res.Added) != 0 {
		t.Errorf("Added = %v, want nothing written", res.Added)
	}
}

// TestEnsureCodexTrustLeavesAVerdictlessProjectTableAlone: a [projects."<path>"]
// table someone else already owns gets a warning rather than a second table
// header at the same key, which would make the document invalid TOML.
func TestEnsureCodexTrustLeavesAVerdictlessProjectTableAlone(t *testing.T) {
	home, configPath := trustScratch(t)
	repoDir, repoKey := trustRepo(t, t.TempDir(), "alpha")

	existing := "[projects." + quoteTOMLKey(repoKey) + "]\nsome_other_setting = 1\n"
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(existing), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := EnsureCodexTrust(CodexTrustRequest{DeveloperHome: home, RepoRoots: []string{repoDir}})
	if err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}
	if got := readTrustConfig(t, configPath); got != existing {
		t.Errorf("someone else's project table was edited:\n%s", got)
	}
	if len(res.Warnings) != 1 {
		t.Errorf("Warnings = %v, want one about the untouched entry", res.Warnings)
	}
}

// TestEnsureCodexTrustHonorsCodexHome: the override Codex itself reads is the
// one niwa writes under, because the file it names is the file a session loads.
func TestEnsureCodexTrustHonorsCodexHome(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(t.TempDir(), "elsewhere")
	t.Setenv(codexHomeEnv, codexHome)
	repoDir, repoKey := trustRepo(t, t.TempDir(), "alpha")

	if _, err := EnsureCodexTrust(CodexTrustRequest{DeveloperHome: home, RepoRoots: []string{repoDir}}); err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}

	if countTrustBlocks(readTrustConfig(t, filepath.Join(codexHome, "config.toml")), repoKey) != 1 {
		t.Error("no entry landed under CODEX_HOME")
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Error("an entry landed under the home directory while CODEX_HOME was set")
	}
}

// TestApplyDeliversDirectoryTrustThroughTheContract is the end-to-end shape of
// the delivery: a create followed by two applies leaves one entry per
// repository, and the record of what was written survives in instance state --
// which is what a later apply reads before it retracts anything.
func TestApplyDeliversDirectoryTrustThroughTheContract(t *testing.T) {
	t.Setenv(codexHomeEnv, "")
	home := t.TempDir()

	h := newSetupVerdictHarness(t, []string{"alpha", "beta"}, nil)
	h.applier.DeveloperHome = home

	instanceRoot, err := h.applier.Create(context.Background(), h.cfg, h.niwaDir, h.workspaceRoot, h.instanceName)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := h.applier.Apply(context.Background(), h.cfg, h.niwaDir, instanceRoot); err != nil {
			t.Fatalf("Apply %d: %v", i, err)
		}
	}

	config := readTrustConfig(t, filepath.Join(home, ".codex", "config.toml"))
	var keys []string
	for _, name := range []string{"alpha", "beta"} {
		key, err := CanonicalTrustKey(filepath.Join(instanceRoot, "all", name))
		if err != nil {
			t.Fatalf("canonicalizing %s: %v", name, err)
		}
		keys = append(keys, key)
		if n := countTrustBlocks(config, key); n != 1 {
			t.Errorf("%s appears in %d blocks after three applies, want 1:\n%s", name, n, config)
		}
	}

	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if len(state.TrustKeys) != len(keys) {
		t.Errorf("state records %v, want the two written keys %v", state.TrustKeys, keys)
	}
}

// TestApplyWithNoDeveloperHomeWritesNothingOutsideTheInstance pins the default
// every unit suite in this package relies on. An Applier nobody wired a home
// into must not reach a developer's configuration, because the suites build
// them by the dozen and none of them opts out of anything.
func TestApplyWithNoDeveloperHomeWritesNothingOutsideTheInstance(t *testing.T) {
	codexHome := filepath.Join(t.TempDir(), "codex")
	t.Setenv(codexHomeEnv, codexHome)

	h := newSetupVerdictHarness(t, []string{"alpha"}, nil)
	if h.applier.DeveloperHome != "" {
		t.Fatal("NewApplier resolved a developer home; the default must be empty")
	}

	if _, err := h.applier.Create(context.Background(), h.cfg, h.niwaDir, h.workspaceRoot, h.instanceName); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := os.Stat(codexHome); !os.IsNotExist(err) {
		t.Errorf("an unwired applier wrote into %s", codexHome)
	}
}
