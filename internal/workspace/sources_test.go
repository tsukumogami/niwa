package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// TestEnvMaterializerRecordsSources locks in that Materialize fills
// MaterializeContext.SourceTuples for each written file with the
// appropriate SourceEntry list. The test mixes a plaintext env file
// with an inline secret to confirm both kinds of sources are
// recorded side-by-side.
func TestEnvMaterializerRecordsSources(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(configDir, "workspace.env")
	if err := os.WriteFile(envPath, []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repoDir := filepath.Join(tmpDir, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}

	v := secret.New([]byte("resolved-plaintext"), secret.Origin{
		ProviderName: "team",
		Key:          "API_TOKEN",
		VersionToken: "rev-42",
	})

	tuples := map[string][]SourceEntry{}
	ctx := &MaterializeContext{
		Effective: EffectiveConfig{
			Env: config.EnvConfig{
				Files: []string{"workspace.env"},
				Secrets: config.EnvVarsTable{Values: map[string]config.MaybeSecret{
					"API_TOKEN": {
						Secret: v,
						Token:  vault.VersionToken{Token: "rev-42", Provenance: "audit://log/42"},
					},
				}},
			},
		},
		RepoDir:      repoDir,
		ConfigDir:    configDir,
		SourceTuples: tuples,
	}

	m := &EnvMaterializer{}
	written, err := m.Materialize(ctx)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("written = %d, want 1", len(written))
	}

	sources, ok := tuples[written[0]]
	if !ok {
		t.Fatalf("no sources recorded for %s", written[0])
	}
	if len(sources) != 2 {
		t.Fatalf("sources = %d, want 2 (plaintext file + vault secret): %+v", len(sources), sources)
	}

	var sawFile, sawVault bool
	for _, s := range sources {
		switch s.Kind {
		case SourceKindPlaintext:
			if s.SourceID == "workspace.env" {
				sawFile = true
				if !strings.HasPrefix(s.VersionToken, "sha256:") {
					t.Errorf("plaintext VersionToken = %q, want sha256: prefix", s.VersionToken)
				}
			}
		case SourceKindVault:
			sawVault = true
			if s.VersionToken != "rev-42" {
				t.Errorf("vault VersionToken = %q, want rev-42", s.VersionToken)
			}
			if s.Provenance != "audit://log/42" {
				t.Errorf("vault Provenance = %q, want audit://log/42", s.Provenance)
			}
			if s.SourceID != "team/API_TOKEN" {
				t.Errorf("vault SourceID = %q, want team/API_TOKEN", s.SourceID)
			}
		}
	}
	if !sawFile {
		t.Error("plaintext env file source missing from Sources[]")
	}
	if !sawVault {
		t.Error("vault source missing from Sources[]")
	}

	// The fingerprint computed from these sources must be stable
	// and non-empty — downstream code relies on it to classify
	// stale vs drifted.
	fp := ComputeSourceFingerprint(sources)
	if fp == "" {
		t.Error("fingerprint is empty")
	}
	if len(fp) != 64 {
		t.Errorf("fingerprint length = %d, want 64", len(fp))
	}

	// Invariant: the fingerprint must NOT contain any secret bytes.
	// We can't grep for "resolved-plaintext" literally because the
	// hash would never contain it, but we assert the rollup is
	// stable across runs (any secret leakage would come from the
	// VersionToken field, which is derived from non-secret metadata).
	fp2 := ComputeSourceFingerprint(sources)
	if fp != fp2 {
		t.Error("fingerprint is not deterministic across calls")
	}
}

// TestApplyPopulatesSourceFingerprintEndToEnd runs the full Create
// pipeline against a workspace with both a plaintext env file and a
// vault-resolved secret, and asserts that the persisted state
// contains a non-empty SourceFingerprint and Sources[] for the
// .local.env file.
func TestApplyPopulatesSourceFingerprintEndToEnd(t *testing.T) {
	withFakeVaultBackend(t)

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A plaintext env file the workspace will read.
	if err := os.WriteFile(filepath.Join(niwaDir, "shared.env"), []byte("PLAIN=value\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgTOML := `
[workspace]
name = "fp-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[vault.provider]
kind = "fake"

[vault.provider.values]
TOKEN = "resolved-token-xxxxxxxxxxx"

[env]
files = ["shared.env"]

[env.secrets]
TOKEN = "vault://TOKEN"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(cfgTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "fp-ws")
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot); err != nil {
		t.Fatalf("Create: %v", err)
	}

	state, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.SchemaVersion != SchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d (v2)", state.SchemaVersion, SchemaVersion)
	}

	// Find the .local.env file in the managed files list.
	envFilePath := filepath.Join(instanceRoot, "default", "app", ".local.env")
	var mf *ManagedFile
	for i := range state.ManagedFiles {
		if state.ManagedFiles[i].Path == envFilePath {
			mf = &state.ManagedFiles[i]
			break
		}
	}
	if mf == nil {
		t.Fatalf("managed file %s not in state: %+v", envFilePath, state.ManagedFiles)
	}
	if mf.SourceFingerprint == "" {
		t.Error("SourceFingerprint is empty for .local.env")
	}
	if len(mf.Sources) == 0 {
		t.Fatal("Sources[] is empty for .local.env")
	}

	var sawPlaintextFile, sawVault bool
	for _, s := range mf.Sources {
		if s.Kind == SourceKindPlaintext && s.SourceID == "shared.env" {
			sawPlaintextFile = true
		}
		if s.Kind == SourceKindVault {
			sawVault = true
			if s.Provenance == "" {
				t.Error("vault source provenance is empty; expected fake backend to set it")
			}
			// Key invariant from the issue: Sources[] must never
			// contain secret bytes. VersionToken is the fake's
			// SHA-256 of the plaintext and the Provenance is a
			// fixture identifier — neither leaks the plaintext.
			if strings.Contains(s.VersionToken, "resolved-token") {
				t.Errorf("VersionToken leaks plaintext: %q", s.VersionToken)
			}
			if strings.Contains(s.Provenance, "resolved-token") {
				t.Errorf("Provenance leaks plaintext: %q", s.Provenance)
			}
		}
	}
	if !sawPlaintextFile {
		t.Error("plaintext env source missing from Sources[]")
	}
	if !sawVault {
		t.Error("vault source missing from Sources[]")
	}
}

// TestComputeStatusDriftOnlyUnchangedFingerprint simulates a user
// editing a managed file after apply without touching any source.
// The re-hash of plaintext sources still matches state, so status
// must classify the file as "drifted" (local edit), not "stale"
// (source rotation).
func TestComputeStatusDriftOnlyUnchangedFingerprint(t *testing.T) {
	root := t.TempDir()
	now := time.Now().Truncate(time.Second)

	// Layout: {root}/.niwa/... plus workspace config dir.
	configDir := filepath.Dir(root)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Source file in the config dir (one level up from the instance).
	srcPath := filepath.Join(configDir, "shared.env")
	if err := os.WriteFile(srcPath, []byte("FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcHash, err := HashFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	// A "materialized" file we pretend niwa apply wrote.
	managedPath := filepath.Join(root, "app", ".local.env")
	if err := os.MkdirAll(filepath.Dir(managedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedPath, []byte("FOO=bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	managedHash, err := HashFile(managedPath)
	if err != nil {
		t.Fatal(err)
	}

	sources := []SourceEntry{
		{Kind: SourceKindPlaintext, SourceID: "shared.env", VersionToken: srcHash},
	}
	state := &InstanceState{
		SchemaVersion: SchemaVersion,
		InstanceName:  "ws",
		Root:          root,
		Created:       now,
		LastApplied:   now,
		Repos:         map[string]RepoState{},
		ManagedFiles: []ManagedFile{{
			Path:              managedPath,
			ContentHash:       managedHash,
			SourceFingerprint: ComputeSourceFingerprint(sources),
			Sources:           sources,
			Generated:         now,
		}},
	}

	// User edit to the managed file without changing the source.
	if err := os.WriteFile(managedPath, []byte("FOO=edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := ComputeStatus(state, root)
	if err != nil {
		t.Fatalf("ComputeStatus: %v", err)
	}
	if len(status.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(status.Files))
	}
	if got := status.Files[0].Status; got != "drifted" {
		t.Errorf("status = %q, want drifted (user edit, source unchanged)", got)
	}
	if len(status.Files[0].ChangedSources) != 0 {
		t.Errorf("ChangedSources = %+v, want empty for drifted", status.Files[0].ChangedSources)
	}
}

// TestComputeStatusPlaintextRotationStale simulates the other side:
// the user rotates a plaintext source file (e.g., edits
// workspace.env), and without re-running apply the managed file's
// content is also out of date. Status must return "stale" with the
// changed source attributed, because the mismatch was driven by an
// upstream rotation, not a hand edit to the materialized file.
func TestComputeStatusPlaintextRotationStale(t *testing.T) {
	root := t.TempDir()
	now := time.Now().Truncate(time.Second)

	configDir := filepath.Dir(root)
	srcPath := filepath.Join(configDir, "shared.env")
	if err := os.WriteFile(srcPath, []byte("FOO=original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldHash, err := HashFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	managedPath := filepath.Join(root, "app", ".local.env")
	if err := os.MkdirAll(filepath.Dir(managedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedPath, []byte("FOO=original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	managedHash, err := HashFile(managedPath)
	if err != nil {
		t.Fatal(err)
	}

	sources := []SourceEntry{
		{Kind: SourceKindPlaintext, SourceID: "shared.env", VersionToken: oldHash},
	}
	state := &InstanceState{
		SchemaVersion: SchemaVersion,
		InstanceName:  "ws",
		Root:          root,
		Created:       now,
		LastApplied:   now,
		Repos:         map[string]RepoState{},
		ManagedFiles: []ManagedFile{{
			Path:              managedPath,
			ContentHash:       managedHash,
			SourceFingerprint: ComputeSourceFingerprint(sources),
			Sources:           sources,
			Generated:         now,
		}},
	}

	// Rotate the plaintext source AND the managed file (both changed
	// outside niwa; simulates a user editing the source directly, or
	// an upstream sync that rewrites both).
	if err := os.WriteFile(srcPath, []byte("FOO=rotated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedPath, []byte("FOO=rotated\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	status, err := ComputeStatus(state, root)
	if err != nil {
		t.Fatalf("ComputeStatus: %v", err)
	}
	if len(status.Files) != 1 {
		t.Fatalf("Files = %d, want 1", len(status.Files))
	}
	if got := status.Files[0].Status; got != "stale" {
		t.Errorf("status = %q, want stale (plaintext rotation detected)", got)
	}
	if len(status.Files[0].ChangedSources) != 1 {
		t.Fatalf("ChangedSources = %+v, want 1", status.Files[0].ChangedSources)
	}
	cs := status.Files[0].ChangedSources[0]
	if cs.Kind != SourceKindPlaintext {
		t.Errorf("ChangedSources[0].Kind = %q, want %q", cs.Kind, SourceKindPlaintext)
	}
	if cs.SourceID != "shared.env" {
		t.Errorf("ChangedSources[0].SourceID = %q, want shared.env", cs.SourceID)
	}
	if cs.OldToken != oldHash {
		t.Errorf("ChangedSources[0].OldToken = %q, want %q", cs.OldToken, oldHash)
	}
	if cs.NewToken == "" || cs.NewToken == oldHash {
		t.Errorf("ChangedSources[0].NewToken = %q, must differ from OldToken", cs.NewToken)
	}
}

// TestApplyVaultRotationUpdatesSourceFingerprint is the vault-side
// functional scenario: apply with a fake-backed secret, rotate the
// fake's value, re-apply, and assert that the stored
// SourceFingerprint changed and the Sources[] for the managed file
// surface the new provider VersionToken.
//
// Note: Issue 7 captures vault-side rotation reactively — at the
// point the next apply runs. The "stale" status label for vault
// rotations without a fresh apply is Issue 10's responsibility; this
// test exercises the piece Issue 7 owns (fingerprint capture at
// apply time).
func TestApplyVaultRotationUpdatesSourceFingerprint(t *testing.T) {
	withFakeVaultBackend(t)

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgTOML := `
[workspace]
name = "rot-ws"

[[sources]]
org = "testorg"

[groups.default]
repos = ["app"]

[vault.provider]
kind = "fake"

[vault.provider.values]
TOKEN = "first-value-aaaaaaaaaaaa"

[env.secrets]
TOKEN = "vault://TOKEN"
`
	writeCfg := func(body string) {
		if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCfg(cfgTOML)
	result, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg := result.Config

	mockClient := &mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "app", SSHURL: "git@github.com:testorg/app.git"}},
		},
	}

	workspaceRoot := tmpDir
	instanceRoot := filepath.Join(workspaceRoot, "rot-ws")
	groupDir := filepath.Join(instanceRoot, "default")
	if err := os.MkdirAll(filepath.Join(groupDir, "app", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	applier := NewApplier(mockClient)
	applier.Cloner = &Cloner{}
	if _, err := applier.Create(context.Background(), cfg, niwaDir, workspaceRoot); err != nil {
		t.Fatalf("Create: %v", err)
	}

	stateBefore, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState before rotation: %v", err)
	}
	envFilePath := filepath.Join(instanceRoot, "default", "app", ".local.env")
	findManaged := func(s *InstanceState) *ManagedFile {
		for i := range s.ManagedFiles {
			if s.ManagedFiles[i].Path == envFilePath {
				return &s.ManagedFiles[i]
			}
		}
		return nil
	}
	mfBefore := findManaged(stateBefore)
	if mfBefore == nil {
		t.Fatalf("managed .local.env not in state before rotation")
	}
	if mfBefore.SourceFingerprint == "" {
		t.Fatal("SourceFingerprint empty before rotation")
	}
	var vaultTokenBefore string
	for _, s := range mfBefore.Sources {
		if s.Kind == SourceKindVault {
			vaultTokenBefore = s.VersionToken
		}
	}
	if vaultTokenBefore == "" {
		t.Fatal("no vault source recorded before rotation")
	}

	// Rotate the fake's value by rewriting the config and reloading.
	rotatedTOML := strings.Replace(cfgTOML,
		"TOKEN = \"first-value-aaaaaaaaaaaa\"",
		"TOKEN = \"second-value-bbbbbbbbbbbb\"", 1)
	writeCfg(rotatedTOML)
	result2, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("config.Load after rotation: %v", err)
	}
	if err := applier.Apply(context.Background(), result2.Config, niwaDir, instanceRoot); err != nil {
		t.Fatalf("Apply after rotation: %v", err)
	}

	stateAfter, err := LoadState(instanceRoot)
	if err != nil {
		t.Fatalf("LoadState after rotation: %v", err)
	}
	mfAfter := findManaged(stateAfter)
	if mfAfter == nil {
		t.Fatalf("managed .local.env not in state after rotation")
	}
	if mfAfter.SourceFingerprint == mfBefore.SourceFingerprint {
		t.Error("SourceFingerprint did not change after vault rotation")
	}
	var vaultTokenAfter string
	var vaultProvAfter string
	for _, s := range mfAfter.Sources {
		if s.Kind == SourceKindVault {
			vaultTokenAfter = s.VersionToken
			vaultProvAfter = s.Provenance
		}
	}
	if vaultTokenAfter == "" {
		t.Fatal("no vault source recorded after rotation")
	}
	if vaultTokenAfter == vaultTokenBefore {
		t.Errorf("vault VersionToken unchanged after rotation: %q", vaultTokenAfter)
	}
	if vaultProvAfter == "" {
		t.Error("vault Provenance empty after rotation; fake backend should surface a fixture identifier")
	}
}
