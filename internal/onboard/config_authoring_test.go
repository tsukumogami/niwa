package onboard

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// --- EncodeTOMLString ---

func TestEncodeTOMLString_HostileCharactersRoundTrip(t *testing.T) {
	hostile := `https://evil.example/"] injected
[global.other]
kind = "hijacked"`

	encoded := EncodeTOMLString(hostile)
	doc := "value = " + encoded + "\n"

	var out struct {
		Value string `toml:"value"`
	}
	if err := toml.Unmarshal([]byte(doc), &out); err != nil {
		t.Fatalf("encoded value did not parse as valid TOML: %v\ndoc:\n%s", err, doc)
	}
	if out.Value != hostile {
		t.Errorf("round-trip mismatch:\ngot:  %q\nwant: %q", out.Value, hostile)
	}
}

func TestEncodeTOMLString_Backslash(t *testing.T) {
	got := EncodeTOMLString(`a\b`)
	want := `"a\\b"`
	if got != want {
		t.Errorf("EncodeTOMLString(%q) = %q, want %q", `a\b`, got, want)
	}
}

func TestEncodeTOMLString_ControlByte(t *testing.T) {
	got := EncodeTOMLString("a\x01b")
	want := `"a\u0001b"`
	if got != want {
		t.Errorf("EncodeTOMLString control byte = %q, want %q", got, want)
	}

	// The escaped form must still parse as valid TOML and round-trip to
	// the original control byte.
	var out struct {
		V string `toml:"v"`
	}
	if err := toml.Unmarshal([]byte("v = "+got+"\n"), &out); err != nil {
		t.Fatalf("escaped control byte did not parse as valid TOML: %v", err)
	}
	if out.V != "a\x01b" {
		t.Errorf("round-trip mismatch: got %q, want %q", out.V, "a\x01b")
	}
}

// --- InsertOrReplaceTable ---

func TestInsertOrReplaceTable_AppendWhenAbsent(t *testing.T) {
	existing := []byte(`[workspaces.foo]
name = "foo"
`)
	body := "[global.vault.provider]\nkind = \"infisical\"\n"

	got, changed := InsertOrReplaceTable(existing, "global.vault.provider", body)
	if !changed {
		t.Fatal("expected changed=true when table is absent")
	}
	want := `[workspaces.foo]
name = "foo"

[global.vault.provider]
kind = "infisical"
`
	if string(got) != want {
		t.Errorf("appended content mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestInsertOrReplaceTable_AppendToEmptyFile(t *testing.T) {
	body := "[global.vault.provider]\nkind = \"infisical\"\n"
	got, changed := InsertOrReplaceTable(nil, "global.vault.provider", body)
	if !changed {
		t.Fatal("expected changed=true for empty file")
	}
	if string(got) != body {
		t.Errorf("empty-file append mismatch:\ngot:\n%q\nwant:\n%q", got, body)
	}
}

func TestInsertOrReplaceTable_NoOpWhenIdentical(t *testing.T) {
	existing := []byte(`# a hand-written comment
[global.vault.provider]
kind = "infisical"
project = "acme"
api_url = "https://app.infisical.com"

[unrelated.table]
key = "value"
`)
	body := "[global.vault.provider]\nkind = \"infisical\"\nproject = \"acme\"\napi_url = \"https://app.infisical.com\"\n"

	got, changed := InsertOrReplaceTable(existing, "global.vault.provider", body)
	if changed {
		t.Error("expected changed=false when the exact table already exists")
	}
	if string(got) != string(existing) {
		t.Errorf("no-op result must be byte-identical to input:\ngot:\n%s\nwant:\n%s", got, existing)
	}
}

func TestInsertOrReplaceTable_ReplaceWhenDifferent_PreservesRestOfFile(t *testing.T) {
	existing := []byte(`# header comment, preserved
[workspaces.foo]
name = "foo"

# a comment right above the wizard-owned table
[global.vault.provider]
kind = "old-kind"
project = "old-project"
extra_hand_added_key = "gone after replace"

[[claude.marketplaces]]
source = "acme/plugins"

[unrelated.table]
# preserved comment
key = "value"
`)
	newBody := "[global.vault.provider]\nkind = \"infisical\"\nproject = \"acme\"\napi_url = \"https://app.infisical.com\"\n"

	got, changed := InsertOrReplaceTable(existing, "global.vault.provider", newBody)
	if !changed {
		t.Fatal("expected changed=true when table exists with different values")
	}
	gotStr := string(got)

	// Content before and after the target table's span must survive
	// verbatim, including comments and unrelated tables.
	if !strings.Contains(gotStr, "# header comment, preserved") {
		t.Error("leading comment was not preserved")
	}
	if !strings.Contains(gotStr, "[workspaces.foo]\nname = \"foo\"") {
		t.Error("unrelated preceding table was not preserved")
	}
	if !strings.Contains(gotStr, "# a comment right above the wizard-owned table") {
		t.Error("comment immediately preceding the header was not preserved")
	}
	if !strings.Contains(gotStr, "[[claude.marketplaces]]\nsource = \"acme/plugins\"") {
		t.Error("array-of-tables block after the target was not preserved")
	}
	if !strings.Contains(gotStr, "[unrelated.table]\n# preserved comment\nkey = \"value\"") {
		t.Error("unrelated trailing table and its comment were not preserved")
	}

	// The old table's content must be fully gone (whole-table replace,
	// not a merge) -- including the hand-added key inside that table.
	if strings.Contains(gotStr, "old-kind") || strings.Contains(gotStr, "old-project") {
		t.Error("old table values were not replaced")
	}
	if strings.Contains(gotStr, "extra_hand_added_key") {
		t.Error("hand-added key inside the wizard-owned table should not survive a whole-table replace")
	}
	if !strings.Contains(gotStr, "kind = \"infisical\"") || !strings.Contains(gotStr, "api_url = \"https://app.infisical.com\"") {
		t.Error("new table values are missing from the result")
	}

	// Re-running InsertOrReplaceTable with the same body must now be a
	// no-op (idempotence after a replace).
	_, changedAgain := InsertOrReplaceTable(got, "global.vault.provider", newBody)
	if changedAgain {
		t.Error("re-running with identical body after a replace should be a no-op")
	}
}

func TestInsertOrReplaceTable_ReplaceAtEOF(t *testing.T) {
	existing := []byte("[global.vault.provider]\nkind = \"old\"\n")
	newBody := "[global.vault.provider]\nkind = \"new\"\n"

	got, changed := InsertOrReplaceTable(existing, "global.vault.provider", newBody)
	if !changed {
		t.Fatal("expected changed=true")
	}
	if string(got) != newBody {
		t.Errorf("EOF-terminated span replace mismatch:\ngot:\n%q\nwant:\n%q", got, newBody)
	}
}

// --- WritePersonalOverlayVaultProvider ---

// recordingGitInvoker is a minimal test double mirroring
// workspace's recordingGitInvoker: it substitutes `true` for every
// invocation so no real git process runs, while still recording argv.
type recordingGitInvoker struct {
	invocations [][]string
}

func (r *recordingGitInvoker) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	r.invocations = append(r.invocations, append([]string(nil), args...))
	return exec.CommandContext(ctx, "true")
}

func TestWritePersonalOverlayVaultProvider_RealGitCommitNoPush(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	overlayDir := t.TempDir()
	runGit(t, overlayDir, "init")
	runGit(t, overlayDir, "commit", "--allow-empty", "-m", "initial")

	ctx := context.Background()
	res, err := WritePersonalOverlayVaultProvider(ctx, workspace.StdGitInvoker(), overlayDir, "infisical", "acme", "https://app.infisical.com")
	if err != nil {
		t.Fatalf("WritePersonalOverlayVaultProvider: %v", err)
	}
	if !res.Changed {
		t.Error("expected Changed=true on first write")
	}
	if res.Site != SitePersonalOverlay || res.Landed != LandedUpstreamRepo {
		t.Errorf("unexpected Site/Landed: %+v", res)
	}

	tomlPath := filepath.Join(overlayDir, "niwa.toml")
	data, err := os.ReadFile(tomlPath)
	if err != nil {
		t.Fatalf("reading niwa.toml: %v", err)
	}
	if !strings.Contains(string(data), "[global.vault.provider]") {
		t.Errorf("niwa.toml missing expected table:\n%s", data)
	}

	info, err := os.Stat(tomlPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("niwa.toml mode = %o, want 0600", info.Mode().Perm())
	}

	// Assert the commit landed and nothing was pushed (no remote exists,
	// so a push would fail structurally; also grep log for sanity).
	log := runGit(t, overlayDir, "log", "--oneline")
	if !strings.Contains(log, "onboard: update personal-overlay vault provider") {
		t.Errorf("commit not found in log:\n%s", log)
	}

	// Idempotent re-run: identical inputs must be a no-op, no second commit.
	res2, err := WritePersonalOverlayVaultProvider(ctx, workspace.StdGitInvoker(), overlayDir, "infisical", "acme", "https://app.infisical.com")
	if err != nil {
		t.Fatalf("second WritePersonalOverlayVaultProvider: %v", err)
	}
	if res2.Changed {
		t.Error("expected Changed=false on idempotent re-run")
	}
	log2 := runGit(t, overlayDir, "log", "--oneline")
	if log != log2 {
		t.Errorf("re-run produced a new commit:\nbefore:\n%s\nafter:\n%s", log, log2)
	}
}

func TestWritePersonalOverlayVaultProvider_AC24_DoesNotTouchSnapshotDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	overlayDir := filepath.Join(root, "overlay-clone")
	if err := os.MkdirAll(overlayDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, overlayDir, "init")
	runGit(t, overlayDir, "commit", "--allow-empty", "-m", "initial")

	snapshotDir := filepath.Join(root, "workspace", ".niwa")
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(snapshotDir, "workspace.toml")
	if err := os.WriteFile(sentinel, []byte("name = \"ws\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := WritePersonalOverlayVaultProvider(context.Background(), workspace.StdGitInvoker(), overlayDir, "infisical", "acme", "https://app.infisical.com"); err != nil {
		t.Fatalf("WritePersonalOverlayVaultProvider: %v", err)
	}

	after, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("the .niwa/ snapshot directory was modified by the overlay write")
	}
	if _, err := os.Stat(filepath.Join(snapshotDir, "niwa.toml")); !errors.Is(err, os.ErrNotExist) {
		t.Error("wizard write leaked into the .niwa/ snapshot directory")
	}
}

func TestWritePersonalOverlayVaultProvider_HostileCharacterFixture(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	overlayDir := t.TempDir()
	runGit(t, overlayDir, "init")
	runGit(t, overlayDir, "commit", "--allow-empty", "-m", "initial")

	hostileURL := "https://evil.example/\"]\ninjected = true\n[other]"

	if _, err := WritePersonalOverlayVaultProvider(context.Background(), workspace.StdGitInvoker(), overlayDir, "infisical", "acme", hostileURL); err != nil {
		t.Fatalf("WritePersonalOverlayVaultProvider: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(overlayDir, "niwa.toml"))
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := config.ParseGlobalConfigOverride(data)
	if err != nil {
		t.Fatalf("written niwa.toml is not valid TOML: %v\ncontent:\n%s", err, data)
	}
	if parsed.Global.Vault == nil || parsed.Global.Vault.Provider == nil {
		t.Fatal("parsed override missing [global.vault.provider]")
	}
	gotURL, _ := parsed.Global.Vault.Provider.Config["api_url"].(string)
	if gotURL != hostileURL {
		t.Errorf("api_url round-trip mismatch:\ngot:  %q\nwant: %q", gotURL, hostileURL)
	}
	// The hostile "[other]" text must not have become an actual table:
	// a stray top-level [other] would parse fine on its own but this
	// asserts it was contained inside the quoted string, not injected as
	// a sibling table with unexpected content.
	if _, present := parsed.Workspaces["other"]; present {
		t.Error("hostile value injected a spurious workspace/table entry")
	}
}

func TestWritePersonalOverlayVaultProvider_NoAuthorArgNoAuthorEnv(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "injected-by-parent")
	t.Setenv("GIT_COMMITTER_EMAIL", "evil@example.com")

	overlayDir := t.TempDir()
	rec := &recordingGitInvoker{}

	if _, err := WritePersonalOverlayVaultProvider(context.Background(), rec, overlayDir, "infisical", "acme", "https://app.infisical.com"); err != nil {
		t.Fatalf("WritePersonalOverlayVaultProvider: %v", err)
	}

	foundCommit := false
	foundPush := false
	for _, inv := range rec.invocations {
		for _, a := range inv {
			if a == "commit" {
				foundCommit = true
			}
			if a == "push" {
				foundPush = true
			}
			if a == "--author" {
				t.Error("commit invocation contains --author")
			}
		}
	}
	if !foundCommit {
		t.Error("no git commit invocation recorded")
	}
	if foundPush {
		t.Error("a git push invocation was recorded; the overlay driver must never push")
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// --- WriteLocalPointer ---

func TestWriteLocalPointer_DirectWriteNoGit(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	res, err := WriteLocalPointer("acme/dot-niwa-overlay")
	if err != nil {
		t.Fatalf("WriteLocalPointer: %v", err)
	}
	if res.Site != SiteLocalPointer || res.Landed != LandedOperatorLocal || !res.Changed {
		t.Errorf("unexpected result: %+v", res)
	}

	cfgPath := filepath.Join(tmp, "niwa", "config.toml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading config.toml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, "niwa", "config.toml", ".git")); err == nil {
		t.Error("no git repo should exist for the local pointer file")
	}
	if !strings.Contains(string(data), "acme/dot-niwa-overlay") {
		t.Errorf("config.toml missing registered repo:\n%s", data)
	}

	// Idempotent re-run.
	res2, err := WriteLocalPointer("acme/dot-niwa-overlay")
	if err != nil {
		t.Fatalf("second WriteLocalPointer: %v", err)
	}
	if res2.Changed {
		t.Error("expected Changed=false when the pointer already matches")
	}
}

// --- RenderTeamConfigSnippet ---

func TestRenderTeamConfigSnippet_NoFileWritten(t *testing.T) {
	tmp := t.TempDir()
	destPath := filepath.Join(tmp, "team-config-repo", "workspace.toml")

	res := RenderTeamConfigSnippet("vault.provider", "infisical", "acme", "https://app.infisical.com", destPath)

	if res.Site != SiteTeamConfig || res.Landed != LandedRenderOnly || res.Changed {
		t.Errorf("unexpected result: %+v", res)
	}
	if res.Location != destPath {
		t.Errorf("Location = %q, want %q", res.Location, destPath)
	}
	if !strings.Contains(res.Snippet, "[vault.provider]") {
		t.Errorf("snippet missing table header:\n%s", res.Snippet)
	}
	if !strings.Contains(res.Message, destPath) {
		t.Errorf("message does not name the destination path: %q", res.Message)
	}
	if _, err := os.Stat(destPath); !errors.Is(err, os.ErrNotExist) {
		t.Error("RenderTeamConfigSnippet must not write any file")
	}
}

func TestRenderTeamConfigSnippet_HostileCharacterEncoded(t *testing.T) {
	res := RenderTeamConfigSnippet("vault.provider", "infisical", "acme", "https://evil.example/\"]\ninjected", "workspace.toml")
	var out struct {
		VaultProvider struct {
			APIURL string `toml:"api_url"`
		} `toml:"vault.provider"`
	}
	// Parse just the snippet in isolation to confirm it's well-formed TOML
	// on its own.
	if err := toml.Unmarshal([]byte(res.Snippet), &out); err != nil {
		t.Fatalf("rendered snippet is not valid TOML: %v\nsnippet:\n%s", err, res.Snippet)
	}
}
