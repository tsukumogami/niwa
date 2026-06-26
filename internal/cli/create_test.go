package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func TestComputeInstanceName_FirstInstance(t *testing.T) {
	dir := t.TempDir()

	name, err := computeInstanceName("tsuku", "", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "tsuku" {
		t.Errorf("expected %q, got %q", "tsuku", name)
	}
}

func TestComputeInstanceName_SubsequentInstance(t *testing.T) {
	dir := t.TempDir()

	// Create the first instance directory with state.
	firstDir := filepath.Join(dir, "tsuku")
	stateDir := filepath.Join(firstDir, ".niwa")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	state := workspace.InstanceState{
		SchemaVersion:  1,
		InstanceName:   "tsuku",
		InstanceNumber: 1,
		Root:           firstDir,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "instance.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	name, err := computeInstanceName("tsuku", "", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "tsuku-2" {
		t.Errorf("expected %q, got %q", "tsuku-2", name)
	}
}

func TestComputeInstanceName_CustomName(t *testing.T) {
	dir := t.TempDir()

	name, err := computeInstanceName("tsuku", "hotfix", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "tsuku-hotfix" {
		t.Errorf("expected %q, got %q", "tsuku-hotfix", name)
	}
}

func TestComputeInstanceName_CustomNameIgnoresExisting(t *testing.T) {
	dir := t.TempDir()

	// Even if no instances exist, --name always produces config-name.
	name, err := computeInstanceName("tsuku", "dev", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "tsuku-dev" {
		t.Errorf("expected %q, got %q", "tsuku-dev", name)
	}
}

func TestComputeInstanceName_DirExistsWithoutState(t *testing.T) {
	dir := t.TempDir()

	// Create a directory that exists but has no instance state.
	// Numbered suffixes start at 2, so we get tsuku-2 (not tsuku-1).
	firstDir := filepath.Join(dir, "tsuku")
	if err := os.MkdirAll(firstDir, 0o755); err != nil {
		t.Fatal(err)
	}

	name, err := computeInstanceName("tsuku", "", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Numbered suffixes start at 2, so the first numbered instance is tsuku-2.
	if name != "tsuku-2" {
		t.Errorf("expected %q, got %q", "tsuku-2", name)
	}
}

func TestComputeInstanceName_SkipsNonInstanceDir(t *testing.T) {
	dir := t.TempDir()

	// Create the first instance (valid, InstanceNumber=1).
	firstDir := filepath.Join(dir, "tsuku")
	stateDir := filepath.Join(firstDir, ".niwa")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := workspace.InstanceState{
		SchemaVersion:  1,
		InstanceName:   "tsuku",
		InstanceNumber: 1,
		Root:           firstDir,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "instance.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	// tsuku-2 exists but has no .niwa/instance.json (leftover or foreign dir).
	if err := os.MkdirAll(filepath.Join(dir, "tsuku-2", "some-content"), 0o755); err != nil {
		t.Fatal(err)
	}

	// computeInstanceName should skip tsuku-2 and return tsuku-3.
	name, err := computeInstanceName("tsuku", "", dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "tsuku-3" {
		t.Errorf("expected %q, got %q", "tsuku-3", name)
	}
}

// TestCreateName_SanitizedIntoSlug pins that a --name value is normalized into a
// lowercase slug before it becomes the instance suffix. The seam under test is
// the sanitize -> computeInstanceName composition runCreate performs: spaces and
// punctuation collapse to hyphens, and uppercase is lowercased, so the composed
// instance name is "<config>-<slug>".
func TestCreateName_SanitizedIntoSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"spaces and punctuation", "My Feature!", "tsuku-my-feature"},
		{"already a clean slug", "hotfix", "tsuku-hotfix"},
		{"uppercase lowercased", "Hotfix", "tsuku-hotfix"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			slug := sanitizeInstanceSlug(tc.in)
			if slug == "" {
				t.Fatalf("sanitizeInstanceSlug(%q) returned empty, expected a usable slug", tc.in)
			}
			got, err := computeInstanceName("tsuku", slug, dir)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("name = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCreateName_UnusableAfterSanitize asserts that a provided --name made up
// entirely of unusable characters sanitizes to "", which runCreate treats as a
// hard error rather than silently falling back to the numbered name.
func TestCreateName_UnusableAfterSanitize(t *testing.T) {
	if slug := sanitizeInstanceSlug("!!!"); slug != "" {
		t.Fatalf("sanitizeInstanceSlug(%q) = %q, want empty", "!!!", slug)
	}
}

func TestFindRepoDir_SingleMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "public", "niwa"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := findRepoDir(root, "niwa")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(root, "public", "niwa")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFindRepoDir_NotFound(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "public", "niwa"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := findRepoDir(root, "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestFindRepoDir_Ambiguous(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "public", "niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "private", "niwa"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := findRepoDir(root, "niwa")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected 'ambiguous' in error, got: %v", err)
	}
}

func TestFindRepoDir_PathTraversal(t *testing.T) {
	root := t.TempDir()

	_, err := findRepoDir(root, "../etc")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid repo name") {
		t.Errorf("expected 'invalid repo name' in error, got: %v", err)
	}

	_, err = findRepoDir(root, "foo/bar")
	if err == nil {
		t.Fatal("expected error for slash, got nil")
	}
	if !strings.Contains(err.Error(), "invalid repo name") {
		t.Errorf("expected 'invalid repo name' in error, got: %v", err)
	}
}

func TestFindRepoDir_SkipsDotDirs(t *testing.T) {
	root := t.TempDir()
	// Put repo under .niwa and .claude — should not be found.
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "myrepo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude", "myrepo"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := findRepoDir(root, "myrepo")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

// TestCreateCmd_HasJSONFlag pins the --json flag registration and default.
func TestCreateCmd_HasJSONFlag(t *testing.T) {
	flag := createCmd.Flags().Lookup("json")
	if flag == nil {
		t.Fatal("expected --json flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default false, got %q", flag.DefValue)
	}
}

// TestInstanceNumberFromState reads the number recorded in instance state.
func TestInstanceNumberFromState(t *testing.T) {
	dir := t.TempDir()
	instanceDir := filepath.Join(dir, "tsuku-2")
	stateDir := filepath.Join(instanceDir, ".niwa")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	state := workspace.InstanceState{
		SchemaVersion:  1,
		InstanceName:   "tsuku-2",
		InstanceNumber: 2,
		Root:           instanceDir,
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, "instance.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	if got := instanceNumberFromState(instanceDir); got != 2 {
		t.Errorf("got %d, want 2", got)
	}

	// A missing state file yields 0, never a panic.
	if got := instanceNumberFromState(filepath.Join(dir, "nope")); got != 0 {
		t.Errorf("got %d for missing state, want 0", got)
	}
}

// TestCreateResult_JSONShape asserts the emitted JSON carries exactly
// {name, number, path}, that path is the created instance directory, and
// that nothing else lands on stdout.
func TestCreateResult_JSONShape(t *testing.T) {
	instanceDir := filepath.Join(t.TempDir(), "tsuku-abc123")
	res := createResult{Name: "tsuku-abc123", Number: 3, Path: instanceDir}

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := json.NewEncoder(cmd.OutOrStdout()).Encode(res); err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Decode back into a generic map to assert the exact key set.
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantKeys := map[string]bool{"name": true, "number": true, "path": true}
	if len(got) != len(wantKeys) {
		t.Errorf("expected keys %v, got %v", wantKeys, got)
	}
	for k := range wantKeys {
		if _, ok := got[k]; !ok {
			t.Errorf("missing key %q in %v", k, got)
		}
	}
	if got["path"] != instanceDir {
		t.Errorf("path = %v, want %q (the created instance dir)", got["path"], instanceDir)
	}
	if got["name"] != "tsuku-abc123" {
		t.Errorf("name = %v, want tsuku-abc123", got["name"])
	}
	if got["number"].(float64) != 3 {
		t.Errorf("number = %v, want 3", got["number"])
	}
}

// TestCreateCmd_HasAllowMissingSecretsFlag mirrors the apply-side check.
// The flag plumbs into workspace.Applier.AllowMissingSecrets, which the
// Applier honors uniformly for both Create and Apply (it routes through
// the same runPipeline).
func TestCreateCmd_HasAllowMissingSecretsFlag(t *testing.T) {
	flag := createCmd.Flags().Lookup("allow-missing-secrets")
	if flag == nil {
		t.Fatal("expected --allow-missing-secrets flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default false, got %q", flag.DefValue)
	}
}

// TestCreateCmd_HasAllowPlaintextSecretsFlag mirrors the apply-side check.
// The error message emitted by the public-remote materializer guardrail
// recommends this flag; this test pins it so the suggestion stays
// actionable from create.
func TestCreateCmd_HasAllowPlaintextSecretsFlag(t *testing.T) {
	flag := createCmd.Flags().Lookup("allow-plaintext-secrets")
	if flag == nil {
		t.Fatal("expected --allow-plaintext-secrets flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Errorf("expected default false, got %q", flag.DefValue)
	}
}

// TestCreateCmd_AllowFlagsThreadToApplier mirrors the apply-side check
// that the parsed flags populate package-level vars runCreate copies
// onto the Applier struct. The pipeline integration that the Applier
// then honors these fields is already covered by the workspace and
// guardrail tests.
func TestCreateCmd_AllowFlagsThreadToApplier(t *testing.T) {
	savedMissing := createAllowMissingSecrets
	savedPlain := createAllowPlaintextSecrets
	t.Cleanup(func() {
		createAllowMissingSecrets = savedMissing
		createAllowPlaintextSecrets = savedPlain
	})

	createAllowMissingSecrets = false
	createAllowPlaintextSecrets = false

	if err := createCmd.ParseFlags([]string{"--allow-missing-secrets", "--allow-plaintext-secrets"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if !createAllowMissingSecrets {
		t.Error("expected createAllowMissingSecrets to be true after --allow-missing-secrets")
	}
	if !createAllowPlaintextSecrets {
		t.Error("expected createAllowPlaintextSecrets to be true after --allow-plaintext-secrets")
	}
}
