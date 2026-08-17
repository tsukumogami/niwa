package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
)

// codexTrustSandbox points both home-derived paths -- the developer's Codex
// config and niwa's own lock directory -- at temp trees, so nothing in this
// file can reach the real home. It returns the config path.
func codexTrustSandbox(t *testing.T) string {
	t.Helper()

	codexHome := t.TempDir()
	lockRoot := t.TempDir()

	prevHome, prevLock := codexConfigHome, niwaLockRoot
	codexConfigHome = func() (string, error) { return codexHome, nil }
	niwaLockRoot = func() (string, error) { return lockRoot, nil }
	t.Cleanup(func() {
		codexConfigHome = prevHome
		niwaLockRoot = prevLock
	})

	path, err := CodexConfigPath()
	if err != nil {
		t.Fatalf("CodexConfigPath: %v", err)
	}
	return path
}

// repoFixture creates a directory standing in for a cloned repository and
// returns it alongside the key a correct write must use.
func repoFixture(t *testing.T, parent, name string) (string, string) {
	t.Helper()

	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	key, err := CanonicalTrustKey(dir)
	if err != nil {
		t.Fatalf("CanonicalTrustKey(%s): %v", dir, err)
	}
	return dir, key
}

// decodeTrustLevels returns the trust_level of every per-project entry in the
// config at path, failing the test if the file is not valid TOML.
func decodeTrustLevels(t *testing.T, path string) map[string]string {
	t.Helper()

	var doc struct {
		Projects map[string]struct {
			TrustLevel string `toml:"trust_level"`
		} `toml:"projects"`
	}
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	levels := map[string]string{}
	for key, entry := range doc.Projects {
		levels[key] = entry.TrustLevel
	}
	return levels
}

func TestEnsureCodexTrustWritesOneEntryPerRepository(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")
	libDir, libKey := repoFixture(t, instance, "lib")

	result, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir, libDir}})
	if err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}

	levels := decodeTrustLevels(t, configPath)
	if len(levels) != 2 {
		t.Fatalf("expected 2 per-project entries, got %d: %v", len(levels), levels)
	}
	for _, key := range []string{appKey, libKey} {
		if levels[key] != "trusted" {
			t.Errorf("trust_level for %s = %q, want %q", key, levels[key], "trusted")
		}
	}
	if len(result.Recorded) != 2 {
		t.Errorf("Recorded = %v, want both keys", result.Recorded)
	}
	if len(result.Added) != 2 {
		t.Errorf("Added = %v, want both keys", result.Added)
	}
}

func TestEnsureCodexTrustKeysASymlinkedInstancePathAtItsRealRoot(t *testing.T) {
	configPath := codexTrustSandbox(t)

	// The instance lives under a real directory reached through a symlinked
	// parent -- a linked home, an automounted volume, a symlinked workspace
	// root. Codex resolves the working directory before looking trust up, so
	// an entry keyed by the unsurfaced symlink path is silently miskeyed and
	// the session runs read-only with no error anywhere.
	base := t.TempDir()
	real := filepath.Join(base, "real")
	repoDir, realKey := repoFixture(t, real, "app")
	link := filepath.Join(base, "linked")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlinking %s: %v", real, err)
	}
	viaLink := filepath.Join(link, "app")

	if _, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{viaLink}}); err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}

	levels := decodeTrustLevels(t, configPath)
	if _, ok := levels[realKey]; !ok {
		t.Errorf("no entry at the resolved repository root %s; got %v", realKey, levels)
	}
	if _, ok := levels[viaLink]; ok {
		t.Errorf("entry keyed by the unresolved symlink path %s: that key is miskeyed and yields a read-only session", viaLink)
	}
	if len(levels) != 1 {
		t.Errorf("expected exactly one entry, got %v", levels)
	}
	if _, err := os.Lstat(repoDir); err != nil {
		t.Fatalf("fixture repository vanished: %v", err)
	}
}

func TestEnsureCodexTrustIsIdempotentAcrossThreeApplies(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")

	var recorded []string
	for i := 0; i < 3; i++ {
		result, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir}, Recorded: recorded})
		if err != nil {
			t.Fatalf("apply %d: %v", i+1, err)
		}
		recorded = result.Recorded
		if i > 0 && len(result.Added) != 0 {
			t.Errorf("apply %d added %v, want nothing", i+1, result.Added)
		}
	}

	if got := strings.Count(readFile(t, configPath), "[projects."); got != 1 {
		t.Errorf("found %d per-project table headers after three applies, want 1", got)
	}
	if len(recorded) != 1 || recorded[0] != appKey {
		t.Errorf("Recorded = %v, want [%s]", recorded, appKey)
	}
}

func TestEnsureCodexTrustLeavesPreExistingContentUntouched(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")

	// The developer's own file: a global key, a table, and an entry for a
	// repository that has nothing to do with niwa.
	original := `# the developer's own Codex config
model = "gpt-5-codex"
model_reasoning_effort = "high"

[tui]
notifications = true

[projects."/elsewhere/scratch"]
trust_level = "trusted"
`
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir}}); err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}

	after := readFile(t, configPath)
	if !strings.HasPrefix(after, original) {
		t.Fatalf("pre-existing bytes were not preserved verbatim; got:\n%s", after)
	}
	added := strings.TrimSpace(strings.TrimPrefix(after, original))
	want := fmt.Sprintf("[projects.%s]\ntrust_level = \"trusted\"", quoteTOMLKey(appKey))
	if added != want {
		t.Errorf("added content = %q, want %q", added, want)
	}

	// No global key gained, none lost: the top-level shape is what it was.
	var doc map[string]any
	if _, err := toml.Decode(after, &doc); err != nil {
		t.Fatalf("result is not valid TOML: %v", err)
	}
	for _, key := range []string{"model", "model_reasoning_effort", "tui", "projects"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("top-level key %q went missing", key)
		}
	}
	if len(doc) != 4 {
		t.Errorf("top-level keys = %v, want exactly the four that were there", doc)
	}

	levels := decodeTrustLevels(t, configPath)
	if levels["/elsewhere/scratch"] != "trusted" {
		t.Errorf("the developer's own entry was altered: %v", levels)
	}
	if levels[appKey] != "trusted" {
		t.Errorf("no entry written for %s: %v", appKey, levels)
	}
	if info, err := os.Stat(configPath); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o644 {
		t.Errorf("file mode = %v, want the pre-existing 0644", info.Mode().Perm())
	}
}

func TestEnsureCodexTrustRefusesAnUnparseableConfig(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, _ := repoFixture(t, instance, "app")

	garbage := "this is not = = valid TOML [[[\n"
	if err := os.WriteFile(configPath, []byte(garbage), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}

	result, err := EnsureCodexTrust(CodexTrustRequest{
		RepoRoots: []string{appDir},
		Recorded:  []string{"/previously/written"},
	})
	if err == nil {
		t.Fatal("expected an error for an unparseable config, got nil")
	}
	if !strings.Contains(err.Error(), configPath) {
		t.Errorf("error does not name the file: %v", err)
	}
	if got := readFile(t, configPath); got != garbage {
		t.Errorf("the file was rewritten; content = %q", got)
	}
	after, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("mtime changed on a file niwa refused to touch")
	}
	if len(result.Recorded) != 1 || result.Recorded[0] != "/previously/written" {
		t.Errorf("Recorded = %v, want the prior record carried forward", result.Recorded)
	}
	if len(result.Added) != 0 {
		t.Errorf("Added = %v, want nothing", result.Added)
	}
	if entries, err := os.ReadDir(filepath.Dir(configPath)); err != nil {
		t.Fatal(err)
	} else if len(entries) != 1 {
		t.Errorf("the config directory gained files: %v", entries)
	}
}

func TestEnsureCodexTrustLeavesTheDevelopersOwnVerdictAlone(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")

	// The developer answered Codex's own prompt with "no". niwa neither
	// overwrites the verdict nor claims it in the record: a recorded key is a
	// key niwa may later remove, and this one is not niwa's to remove.
	original := fmt.Sprintf("[projects.%s]\ntrust_level = \"untrusted\"\n", quoteTOMLKey(appKey))
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir}})
	if err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}
	if got := readFile(t, configPath); got != original {
		t.Errorf("the developer's entry was rewritten; content = %q", got)
	}
	if len(result.Added) != 0 {
		t.Errorf("Added = %v, want nothing", result.Added)
	}
	if len(result.Recorded) != 0 {
		t.Errorf("Recorded = %v, want nothing: niwa did not write this entry", result.Recorded)
	}
}

func TestEnsureCodexTrustReportsAProjectEntryCarryingNoVerdict(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")

	original := fmt.Sprintf("[projects.%s]\nsome_other_setting = 1\n", quoteTOMLKey(appKey))
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir}})
	if err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}
	if got := readFile(t, configPath); got != original {
		t.Errorf("a table niwa does not own was edited; content = %q", got)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], appKey) {
		t.Errorf("Warnings = %v, want one naming %s", result.Warnings, appKey)
	}
}

func TestEnsureCodexTrustReplacesTheConfigAtomically(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, _ := repoFixture(t, instance, "app")

	if err := os.WriteFile(configPath, []byte("model = \"gpt-5-codex\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir}}); err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}

	after, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// A rename over the target leaves a different file object at the path. The
	// same object would mean the original was opened and truncated in place --
	// exactly the state an interrupted apply must never be able to produce.
	if os.SameFile(before, after) {
		t.Error("the config was rewritten in place; an interrupted write would truncate the developer's file")
	}
	entries, err := os.ReadDir(filepath.Dir(configPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("staging file left behind: %v", entries)
	}
}

func TestEnsureCodexTrustLeavesTheOriginalIntactWhenTheWriteCannotStart(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, _ := repoFixture(t, instance, "app")

	original := "model = \"gpt-5-codex\"\n"
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(configPath)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	if _, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir}}); err == nil {
		t.Fatal("expected an error when the staging file cannot be created")
	}
	if got := readFile(t, configPath); got != original {
		t.Errorf("the original was disturbed; content = %q", got)
	}
}

func TestEnsureCodexTrustSerializesConcurrentWriters(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")
	libDir, libKey := repoFixture(t, instance, "lib")

	// Two instances applying at once against one developer config. Without the
	// lock across the read-modify-write, the later writer's document is built
	// from a read that predates the earlier writer's rename, and one set of
	// entries is lost.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	roots := [][]string{{appDir}, {libDir}}
	start := make(chan struct{})
	for i := range roots {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = EnsureCodexTrust(CodexTrustRequest{RepoRoots: roots[i]})
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	levels := decodeTrustLevels(t, configPath)
	for _, key := range []string{appKey, libKey} {
		if levels[key] != "trusted" {
			t.Errorf("entry for %s missing after concurrent applies: %v", key, levels)
		}
	}
}

func TestEnsureCodexTrustNeverReadsOrWritesCredentialFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, _ := repoFixture(t, instance, "app")

	// An unreadable credential file beside the config must not fail the write,
	// or be touched at all (R13).
	authPath := filepath.Join(filepath.Dir(configPath), "auth.json")
	authBody := "{\"tokens\":{\"access_token\":\"secret\"}}\n"
	if err := os.WriteFile(authPath, []byte(authBody), 0o000); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(authPath)
	if err != nil {
		t.Fatal(err)
	}
	// Read the bytes back through a temporarily readable mode so the
	// comparison below is over content, not just metadata.
	if err := os.Chmod(authPath, 0o600); err != nil {
		t.Fatal(err)
	}
	original := readFile(t, authPath)
	if err := os.Chmod(authPath, 0o000); err != nil {
		t.Fatal(err)
	}
	if original != authBody {
		t.Fatalf("fixture credential file content = %q", original)
	}

	time.Sleep(10 * time.Millisecond)
	if _, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir}}); err != nil {
		t.Fatalf("EnsureCodexTrust with an unreadable credential file: %v", err)
	}

	after, err := os.Stat(authPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("the credential file's mtime changed")
	}
	if after.Mode().Perm() != 0 {
		t.Errorf("the credential file's mode changed to %v", after.Mode().Perm())
	}
	if after.Size() != before.Size() {
		t.Error("the credential file's size changed")
	}
}

func TestEnsureCodexTrustCarriesForwardKeysItNoLongerWrites(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")

	// A repository from an earlier apply that has since left the workspace.
	// Its entry is still in the developer's file and still niwa's to clean up,
	// so dropping it from the record would strand it there for good.
	stale := "/gone/repo"
	result, err := EnsureCodexTrust(CodexTrustRequest{
		RepoRoots: []string{appDir},
		Recorded:  []string{stale},
	})
	if err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}
	want := []string{appKey, stale}
	if len(result.Recorded) != 2 {
		t.Fatalf("Recorded = %v, want %v", result.Recorded, want)
	}
	for _, key := range want {
		found := false
		for _, got := range result.Recorded {
			if got == key {
				found = true
			}
		}
		if !found {
			t.Errorf("Recorded = %v, missing %s", result.Recorded, key)
		}
	}
	if _, ok := decodeTrustLevels(t, configPath)[stale]; ok {
		t.Error("a recorded-but-unrequested key was re-written into the config")
	}
}

func TestEnsureCodexTrustReportsAnUnresolvableRepositoryRoot(t *testing.T) {
	configPath := codexTrustSandbox(t)
	missing := filepath.Join(t.TempDir(), "never-cloned")

	result, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{missing}})
	if err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], missing) {
		t.Errorf("Warnings = %v, want one naming %s", result.Warnings, missing)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Errorf("a config was written for a repository with no resolvable root: %v", err)
	}
}

func TestQuoteTOMLKeyRoundTripsAwkwardPaths(t *testing.T) {
	for _, key := range []string{
		"/plain/path",
		`/with "quotes"/repo`,
		`/with\backslash/repo`,
		"/with\tcontrol/repo",
		"/with unicode ⛩/repo",
	} {
		doc := fmt.Sprintf("[projects.%s]\ntrust_level = \"trusted\"\n", quoteTOMLKey(key))
		var parsed struct {
			Projects map[string]map[string]any `toml:"projects"`
		}
		if _, err := toml.Decode(doc, &parsed); err != nil {
			t.Errorf("key %q rendered unparseable TOML %q: %v", key, doc, err)
			continue
		}
		if _, ok := parsed.Projects[key]; !ok {
			t.Errorf("key %q did not round-trip; got %v", key, parsed.Projects)
		}
	}
}
