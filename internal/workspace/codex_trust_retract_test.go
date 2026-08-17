package workspace

import (
	"fmt"
	"strings"
	"testing"
)

// The trust half of the conflict rule (DESIGN-dual-agent-workspace Decision 7):
// a repository whose `.codex` is occupied by content niwa did not write gets no
// trust entry, and an entry niwa wrote before the conflict existed is retracted.
//
// What every test here is really pinning is the bound on that retraction. Codex
// writes an identically-shaped [projects."<path>"] entry when the developer
// answers its own trust prompt -- the prompt a conflicted repository is routed
// to -- so the removal is decided by the record of what niwa wrote, never by
// what an entry looks like.

// trustFixtureConfig writes a pre-existing developer config at path and returns
// what it wrote, so a test can assert the bytes around niwa's edit survived it.
func trustFixtureConfig(t *testing.T, path, content string) string {
	t.Helper()
	writeFileT(t, path, content)
	return content
}

// developerAnswer renders the entry Codex itself writes when the developer
// answers the startup trust prompt: the same table, the same key, the same
// verdict. Nothing about it tells niwa's writer apart from niwa's own.
func developerAnswer(key string) string {
	return fmt.Sprintf("\n[%s.%s]\n%s = %q\n", codexProjectsTable, quoteTOMLKey(key), codexTrustLevelKey, codexTrustLevel)
}

func TestEnsureCodexTrustWithholdsAConflictedRepositorysEntry(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")
	libDir, libKey := repoFixture(t, instance, "lib")

	result, err := EnsureCodexTrust(CodexTrustRequest{
		RepoRoots:  []string{appDir, libDir},
		Conflicted: []string{appDir},
	})
	if err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}

	levels := decodeTrustLevels(t, configPath)
	if _, vouched := levels[appKey]; vouched {
		t.Error("niwa vouched for a repository whose payload it refused to deliver")
	}
	if levels[libKey] != codexTrustLevel {
		t.Errorf("the clean repository lost its entry; levels = %v", levels)
	}
	if len(result.Recorded) != 1 || result.Recorded[0] != libKey {
		t.Errorf("Recorded = %v, want only %s", result.Recorded, libKey)
	}
}

func TestEnsureCodexTrustRetractsAnEntryWhenTheRepositoryBecomesConflicted(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")
	libDir, libKey := repoFixture(t, instance, "lib")

	preexisting := trustFixtureConfig(t, configPath,
		"# the developer's own config\nmodel = \"o3\"\n\n[projects.\"/somewhere/else\"]\ntrust_level = \"trusted\"\n")

	first, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir, libDir}})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if len(first.Recorded) != 2 {
		t.Fatalf("Recorded after the clean apply = %v, want both keys", first.Recorded)
	}

	// The repository acquires a `.codex` of its own between applies.
	second, err := EnsureCodexTrust(CodexTrustRequest{
		RepoRoots:  []string{appDir, libDir},
		Recorded:   first.Recorded,
		Conflicted: []string{appDir},
	})
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}

	levels := decodeTrustLevels(t, configPath)
	if _, vouched := levels[appKey]; vouched {
		t.Error("the entry for a now-conflicted repository was not retracted")
	}
	if levels[libKey] != codexTrustLevel {
		t.Errorf("the clean repository's entry went with it; levels = %v", levels)
	}
	if levels["/somewhere/else"] != codexTrustLevel {
		t.Errorf("the developer's own unrelated entry went with it; levels = %v", levels)
	}
	if len(second.Removed) != 1 || second.Removed[0] != appKey {
		t.Errorf("Removed = %v, want [%s]", second.Removed, appKey)
	}
	if len(second.Warnings) != 1 || !strings.Contains(second.Warnings[0], appKey) {
		t.Errorf("Warnings = %v, want one naming the retracted key: a withdrawn entry is not a silent change", second.Warnings)
	}

	// The record moves with the file: a record left behind would let the next
	// apply reason from a key niwa no longer owns.
	if len(second.Recorded) != 1 || second.Recorded[0] != libKey {
		t.Errorf("Recorded = %v, want only %s", second.Recorded, libKey)
	}

	if got := readFile(t, configPath); !strings.HasPrefix(got, preexisting) {
		t.Errorf("the developer's own bytes did not survive the retraction:\n%q", got)
	}
}

// TestEnsureCodexTrustLeavesTheDevelopersOwnAnswerAtARetractedKey is the
// decisive test for the record bound. After the retraction the developer
// answers Codex's prompt and Codex writes the same key back. That entry is
// theirs, sits in no niwa record, and no later apply may touch it -- a
// removal that keyed on the entry's shape would delete it.
func TestEnsureCodexTrustLeavesTheDevelopersOwnAnswerAtARetractedKey(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")

	first, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir}})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	retracted, err := EnsureCodexTrust(CodexTrustRequest{
		RepoRoots:  []string{appDir},
		Recorded:   first.Recorded,
		Conflicted: []string{appDir},
	})
	if err != nil {
		t.Fatalf("retracting apply: %v", err)
	}
	if len(retracted.Recorded) != 0 {
		t.Fatalf("Recorded = %v, want empty after the only key was retracted", retracted.Recorded)
	}

	// Codex prompts at session start and the developer says yes.
	answered := readFile(t, configPath) + developerAnswer(appKey)
	writeFileT(t, configPath, answered)

	recorded := retracted.Recorded
	for i := 0; i < 3; i++ {
		result, err := EnsureCodexTrust(CodexTrustRequest{
			RepoRoots:  []string{appDir},
			Recorded:   recorded,
			Conflicted: []string{appDir},
		})
		if err != nil {
			t.Fatalf("apply %d after the developer answered: %v", i+1, err)
		}
		if len(result.Removed) != 0 {
			t.Fatalf("apply %d removed %v; the developer's own answer is not niwa's to retract", i+1, result.Removed)
		}
		recorded = result.Recorded
	}

	if got := readFile(t, configPath); got != answered {
		t.Errorf("the developer's own answer did not survive:\n got %q\nwant %q", got, answered)
	}
	if decodeTrustLevels(t, configPath)[appKey] != codexTrustLevel {
		t.Error("the developer's own entry lost its verdict")
	}
	for _, key := range recorded {
		if key == appKey {
			t.Error("niwa recorded the developer's own entry as its own")
		}
	}
}

// TestEnsureCodexTrustNeverReinstatesWhileTheConflictStands is the other half of
// the guarantee, stated the accurate way: niwa promises not to re-add its own
// entry, not that no entry exists.
func TestEnsureCodexTrustNeverReinstatesWhileTheConflictStands(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")

	result, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir}})
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	recorded := result.Recorded

	for i := 0; i < 3; i++ {
		result, err = EnsureCodexTrust(CodexTrustRequest{
			RepoRoots:  []string{appDir},
			Recorded:   recorded,
			Conflicted: []string{appDir},
		})
		if err != nil {
			t.Fatalf("apply %d: %v", i+1, err)
		}
		recorded = result.Recorded
		if _, vouched := decodeTrustLevels(t, configPath)[appKey]; vouched {
			t.Fatalf("apply %d reinstated the entry while the conflict stood", i+1)
		}
	}

	// And the conflict clearing lets it come back: the withholding is a
	// per-apply verdict, not a permanent mark on the repository.
	if _, err := EnsureCodexTrust(CodexTrustRequest{RepoRoots: []string{appDir}, Recorded: recorded}); err != nil {
		t.Fatalf("apply after the conflict cleared: %v", err)
	}
	if decodeTrustLevels(t, configPath)[appKey] != codexTrustLevel {
		t.Error("the entry did not come back once the conflict cleared")
	}
}

func TestEnsureCodexTrustTreatsARecordedButAbsentKeyAsANoOp(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")
	libDir, libKey := repoFixture(t, instance, "lib")

	// The record names a key the file no longer carries -- the developer
	// deleted it by hand, or another instance already retracted it.
	trustFixtureConfig(t, configPath, "model = \"o3\"\n")

	result, err := EnsureCodexTrust(CodexTrustRequest{
		RepoRoots:  []string{appDir, libDir},
		Recorded:   []string{appKey},
		Conflicted: []string{appDir},
	})
	if err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Errorf("Removed = %v, want nothing removed for a key the file did not carry", result.Removed)
	}
	if len(result.Recorded) != 1 || result.Recorded[0] != libKey {
		t.Errorf("Recorded = %v, want only %s: niwa owns nothing at the absent key", result.Recorded, libKey)
	}
	if levels := decodeTrustLevels(t, configPath); levels[libKey] != codexTrustLevel {
		t.Errorf("the clean repository's entry was not written; levels = %v", levels)
	}
}

func TestEnsureCodexTrustLeavesAnUnrecordedEntryAlone(t *testing.T) {
	configPath := codexTrustSandbox(t)
	instance := t.TempDir()
	appDir, appKey := repoFixture(t, instance, "app")

	// An entry at a conflicted repository's key that no record names: the
	// developer's, whatever it looks like.
	before := trustFixtureConfig(t, configPath, "model = \"o3\"\n"+developerAnswer(appKey))

	result, err := EnsureCodexTrust(CodexTrustRequest{
		RepoRoots:  []string{appDir},
		Conflicted: []string{appDir},
	})
	if err != nil {
		t.Fatalf("EnsureCodexTrust: %v", err)
	}
	if len(result.Removed) != 0 {
		t.Errorf("Removed = %v, want nothing: no record named that key", result.Removed)
	}
	if got := readFile(t, configPath); got != before {
		t.Errorf("an unrecorded entry was edited:\n got %q\nwant %q", got, before)
	}
}

// TestRemoveTrustEntryTakesOnlyItsOwnBlock pins what a retraction takes out of
// the document: the table niwa wrote and nothing adjacent to it, byte for byte.
func TestRemoveTrustEntryTakesOnlyItsOwnBlock(t *testing.T) {
	const key = "/ws/tools/app"
	before := "# the developer's own config\n" +
		"model = \"o3\"\n" +
		"\n" +
		"[projects.\"/somewhere/else\"]\n" +
		"trust_level = \"trusted\"\n" +
		"\n" +
		"[projects.\"" + key + "\"]\n" +
		"trust_level = \"trusted\"\n" +
		"\n" +
		"[mcp_servers.fs]\n" +
		"command = \"fs-server\"\n" +
		"\n" +
		"[[history]]\n" +
		"id = 1\n"
	want := "# the developer's own config\n" +
		"model = \"o3\"\n" +
		"\n" +
		"[projects.\"/somewhere/else\"]\n" +
		"trust_level = \"trusted\"\n" +
		"\n" +
		"[mcp_servers.fs]\n" +
		"command = \"fs-server\"\n" +
		"\n" +
		"[[history]]\n" +
		"id = 1\n"

	got, found := removeTrustEntry([]byte(before), key)
	if !found {
		t.Fatal("removeTrustEntry did not find the block it wrote")
	}
	if string(got) != want {
		t.Errorf("retraction took more than its own block:\n got %q\nwant %q", got, want)
	}

	// A key the document does not carry leaves it untouched.
	same, found := removeTrustEntry([]byte(want), key)
	if found {
		t.Error("removeTrustEntry claimed to find a block that was already gone")
	}
	if string(same) != want {
		t.Errorf("a no-op retraction rewrote the document:\n%q", same)
	}
}

// TestRemoveTrustEntryMatchesKeysByTOMLRulesNotByText keeps the header match on
// the decoder's terms: an awkward path is quoted when it is written, so it must
// be decoded to be recognized again.
func TestRemoveTrustEntryMatchesKeysByTOMLRulesNotByText(t *testing.T) {
	for _, key := range []string{
		`/with "quotes"/repo`,
		`/with\backslash/repo`,
		"/with\tcontrol/repo",
		"/with unicode ⛩/repo",
	} {
		doc := appendTrustEntries([]byte("model = \"o3\"\n"), []string{key})
		got, found := removeTrustEntry(doc, key)
		if !found {
			t.Errorf("no block found for %q", key)
			continue
		}
		if strings.Contains(string(got), codexTrustLevelKey) {
			t.Errorf("the entry for %q survived its retraction:\n%q", key, got)
		}
	}
}
