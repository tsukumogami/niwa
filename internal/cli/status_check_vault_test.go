package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// countingProvider is a vault.Provider implementation that tracks how
// many Resolve calls it receives. It is used to assert two orthogonal
// invariants of Issue 10:
//
//  1. Default `niwa status` stays offline (resolveCalls == 0).
//  2. `niwa status --check-vault` does call the provider (>= 1 call
//     per distinct recorded vault source).
//
// The values map is addressed by ref.Key so tests can configure
// "same" (stable token) vs "rotated" (new token) fixtures per
// subtest.
type countingProvider struct {
	name         string
	values       map[string]string
	resolveCalls atomic.Int64
}

func (p *countingProvider) Name() string { return p.name }
func (p *countingProvider) Kind() string { return "counting-fake" }

func (p *countingProvider) Resolve(_ context.Context, ref vault.Ref) (secret.Value, vault.VersionToken, error) {
	p.resolveCalls.Add(1)
	v, ok := p.values[ref.Key]
	if !ok {
		return secret.Value{}, vault.VersionToken{}, vault.ErrKeyNotFound
	}
	return secret.Value{}, vault.VersionToken{Token: v, Provenance: "counting:" + ref.Key}, nil
}

func (p *countingProvider) Close() error { return nil }

func (p *countingProvider) Calls() int64 { return p.resolveCalls.Load() }

// countingFactory builds countingProvider instances from a preset
// values map supplied via registerCountingFactory.
type countingFactory struct {
	values map[string]string
	last   *countingProvider
}

func (f *countingFactory) Kind() string { return "counting-fake" }
func (f *countingFactory) Open(_ context.Context, cfg vault.ProviderConfig) (vault.Provider, error) {
	name, _ := cfg["name"].(string)
	p := &countingProvider{name: name, values: f.values}
	f.last = p
	return p, nil
}

// registerCountingFactory registers the custom factory on
// vault.DefaultRegistry and returns a cleanup hook. The factory keeps
// a pointer to the last-opened provider so the test can assert on
// Resolve call counts without digging through the bundle.
func registerCountingFactory(t *testing.T, values map[string]string) *countingFactory {
	t.Helper()
	f := &countingFactory{values: values}
	if err := vault.DefaultRegistry.Register(f); err != nil {
		t.Fatalf("registering counting factory: %v", err)
	}
	t.Cleanup(func() {
		if err := vault.DefaultRegistry.Unregister(f.Kind()); err != nil {
			t.Errorf("unregistering counting factory: %v", err)
		}
	})
	return f
}

// TestRefFromSourceID locks in the exact SourceID parsing contract:
// anonymous-provider SourceIDs look like "/key"; named-provider
// SourceIDs look like "name/key". No slashes, empty key, or any
// other shape is accepted.
func TestRefFromSourceID(t *testing.T) {
	tests := []struct {
		in       string
		wantProv string
		wantKey  string
		wantOk   bool
	}{
		{"team/API_KEY", "team", "API_KEY", true},
		{"/API_KEY", "", "API_KEY", true},
		{"team/", "", "", false},
		{"", "", "", false},
		{"no-slash", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := refFromSourceID(tt.in)
			if ok != tt.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOk)
			}
			if !ok {
				return
			}
			if got.ProviderName != tt.wantProv || got.Key != tt.wantKey {
				t.Errorf("got %+v, want provider=%q key=%q", got, tt.wantProv, tt.wantKey)
			}
		})
	}
}

// TestDetectVaultRotations_RotatedValueReportsChange configures the
// fake backend with NEW token values and asserts the rotation is
// reported. This is the headline AC for --check-vault.
func TestDetectVaultRotations_RotatedValueReportsChange(t *testing.T) {
	factory := registerCountingFactory(t, map[string]string{
		"API_KEY": "new-token-zzzz",
	})
	ctx := context.Background()
	bundle, err := vault.DefaultRegistry.Build(ctx, []vault.ProviderSpec{
		{Name: "team", Kind: factory.Kind(), Config: vault.ProviderConfig{"name": "team"}},
	})
	if err != nil {
		t.Fatalf("Build bundle: %v", err)
	}
	defer bundle.CloseAll()

	state := &workspace.InstanceState{
		ManagedFiles: []workspace.ManagedFile{
			{
				Path: "/tmp/rotated.env",
				Sources: []workspace.SourceEntry{
					{
						Kind:         workspace.SourceKindVault,
						SourceID:     "team/API_KEY",
						VersionToken: "old-token-aaaa",
					},
				},
			},
		},
	}

	rotations := detectVaultRotations(ctx, state, bundle)
	if len(rotations) != 1 {
		t.Fatalf("expected 1 rotation, got %d", len(rotations))
	}
	got := rotations[0]
	if got.Path != "/tmp/rotated.env" {
		t.Errorf("path = %q, want /tmp/rotated.env", got.Path)
	}
	if len(got.ChangedSources) != 1 {
		t.Fatalf("expected 1 changed source, got %d", len(got.ChangedSources))
	}
	cs := got.ChangedSources[0]
	if cs.OldToken != "old-token-aaaa" || cs.NewToken != "new-token-zzzz" {
		t.Errorf("tokens mismatch: %+v", cs)
	}
	// Assertion: the provider was actually invoked (not just a
	// structural stub).
	if factory.last.Calls() == 0 {
		t.Error("expected provider to be invoked, got 0 Resolve calls")
	}
}

// TestDetectVaultRotations_IdenticalValueNoChange configures the
// backend with the SAME token value as recorded in state, asserting
// no rotation is emitted.
func TestDetectVaultRotations_IdenticalValueNoChange(t *testing.T) {
	const sameToken = "stable-token-yyyy"
	factory := registerCountingFactory(t, map[string]string{
		"API_KEY": sameToken,
	})
	ctx := context.Background()
	bundle, err := vault.DefaultRegistry.Build(ctx, []vault.ProviderSpec{
		{Name: "team", Kind: factory.Kind(), Config: vault.ProviderConfig{"name": "team"}},
	})
	if err != nil {
		t.Fatalf("Build bundle: %v", err)
	}
	defer bundle.CloseAll()

	state := &workspace.InstanceState{
		ManagedFiles: []workspace.ManagedFile{
			{
				Path: "/tmp/stable.env",
				Sources: []workspace.SourceEntry{
					{
						Kind:         workspace.SourceKindVault,
						SourceID:     "team/API_KEY",
						VersionToken: sameToken,
					},
				},
			},
		},
	}

	rotations := detectVaultRotations(ctx, state, bundle)
	if len(rotations) != 0 {
		t.Errorf("expected no rotations, got %+v", rotations)
	}
}

// TestDefaultStatusStaysOffline asserts the invariant that
// `niwa status` without --check-vault MUST NOT invoke any provider.
// The assertion uses the counting factory's call counter: zero calls
// after a full showDetailView pass confirms the offline contract.
func TestDefaultStatusStaysOffline(t *testing.T) {
	factory := registerCountingFactory(t, map[string]string{
		"API_KEY": "never-observed",
	})

	root := t.TempDir()
	now := time.Now().Truncate(time.Second)
	configName := "offline-ws"
	// Construct a managed file that has a vault source -- if the
	// offline path accidentally called into the bundle, the counter
	// would fire.
	mf := filepath.Join(root, "some.env")
	if err := os.WriteFile(mf, []byte("KEY=value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := workspace.HashFile(mf)
	if err != nil {
		t.Fatal(err)
	}
	state := &workspace.InstanceState{
		SchemaVersion:  workspace.SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   "offline-ws",
		InstanceNumber: 1,
		Root:           root,
		Created:        now,
		LastApplied:    now,
		ManagedFiles: []workspace.ManagedFile{
			{
				Path:        mf,
				ContentHash: hash,
				Generated:   now,
				Sources: []workspace.SourceEntry{
					{Kind: workspace.SourceKindVault, SourceID: "team/API_KEY", VersionToken: "prev"},
				},
			},
		},
	}
	if err := workspace.SaveState(root, state); err != nil {
		t.Fatal(err)
	}

	buf := &strings.Builder{}
	statusCmd.SetOut(buf)
	defer statusCmd.SetOut(os.Stdout)

	if err := showDetailView(statusCmd, root); err != nil {
		t.Fatalf("showDetailView: %v", err)
	}

	// factory.last is only non-nil if Open() was called. The default
	// path never builds a bundle, so we expect last == nil.
	if factory.last != nil && factory.last.Calls() > 0 {
		t.Errorf("default status must not invoke providers, got %d Resolve calls", factory.last.Calls())
	}
}

// TestDetectVaultRotations_MalformedSourceIDReportsError confirms
// that a SourceID without a slash produces a non-fatal rotation entry
// carrying the error rather than crashing. Broken state files
// shouldn't take the command down.
func TestDetectVaultRotations_MalformedSourceIDReportsError(t *testing.T) {
	factory := registerCountingFactory(t, map[string]string{})
	ctx := context.Background()
	bundle, err := vault.DefaultRegistry.Build(ctx, []vault.ProviderSpec{
		{Name: "team", Kind: factory.Kind(), Config: vault.ProviderConfig{"name": "team"}},
	})
	if err != nil {
		t.Fatalf("Build bundle: %v", err)
	}
	defer bundle.CloseAll()

	state := &workspace.InstanceState{
		ManagedFiles: []workspace.ManagedFile{
			{
				Path: "/tmp/bad.env",
				Sources: []workspace.SourceEntry{
					{Kind: workspace.SourceKindVault, SourceID: "no-slash", VersionToken: "t"},
				},
			},
		},
	}

	rotations := detectVaultRotations(ctx, state, bundle)
	if len(rotations) != 1 {
		t.Fatalf("expected 1 rotation (carrying error), got %d", len(rotations))
	}
	if rotations[0].ChangedSources[0].Err == nil {
		t.Errorf("expected Err set on malformed SourceID entry")
	}
}
