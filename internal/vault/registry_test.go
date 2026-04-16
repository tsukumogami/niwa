package vault_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// stubFactory is a vault.Factory for registry unit tests. It returns
// a stubProvider whose Close call increments closeCount on the
// factory. openErr, when non-nil, is returned by Open.
type stubFactory struct {
	kind    string
	openErr error

	opened []*stubProvider
}

func (f *stubFactory) Kind() string {
	return f.kind
}

func (f *stubFactory) Open(_ context.Context, config vault.ProviderConfig) (vault.Provider, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	name, _ := config["name"].(string)
	p := &stubProvider{kind: f.kind, name: name}
	f.opened = append(f.opened, p)
	return p, nil
}

type stubProvider struct {
	kind     string
	name     string
	closed   int
	closeErr error
}

func (p *stubProvider) Name() string { return p.name }
func (p *stubProvider) Kind() string { return p.kind }
func (p *stubProvider) Resolve(_ context.Context, _ vault.Ref) (secret.Value, vault.VersionToken, error) {
	return secret.Value{}, vault.VersionToken{}, nil
}
func (p *stubProvider) Close() error {
	p.closed++
	return p.closeErr
}

// TestRegistryRegisterAndBuild covers the happy path: registering a
// Factory, building a Bundle with one spec, and looking up the
// provider with Get.
func TestRegistryRegisterAndBuild(t *testing.T) {
	r := vault.NewRegistry()
	f := &stubFactory{kind: "fake"}
	if err := r.Register(f); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}

	specs := []vault.ProviderSpec{
		{Name: "team", Kind: "fake", Config: vault.ProviderConfig{"name": "team"}, Source: "niwa.toml"},
	}
	bundle, err := r.Build(context.Background(), specs)
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	defer bundle.CloseAll()

	got, err := bundle.Get("team")
	if err != nil {
		t.Fatalf("Bundle.Get returned error: %v", err)
	}
	if got.Name() != "team" {
		t.Fatalf("Get returned provider with name %q, want %q", got.Name(), "team")
	}
}

// TestRegistryRejectsDuplicateKind asserts AC: Registry.Register
// rejects duplicate Kind registration with a descriptive error.
func TestRegistryRejectsDuplicateKind(t *testing.T) {
	r := vault.NewRegistry()
	if err := r.Register(&stubFactory{kind: "fake"}); err != nil {
		t.Fatalf("first Register returned error: %v", err)
	}
	err := r.Register(&stubFactory{kind: "fake"})
	if err == nil {
		t.Fatalf("second Register did not return error")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected descriptive duplicate error, got: %v", err)
	}
}

// TestRegistryRejectsNilFactory guards against a nil deref crashing
// backend registrations that went wrong at compile time.
func TestRegistryRejectsNilFactory(t *testing.T) {
	r := vault.NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatalf("Register(nil) did not return error")
	}
}

// TestRegistryRejectsEmptyKind guards against a Factory returning ""
// from Kind(), which would collide with the Registry's sentinel for
// "no kind".
func TestRegistryRejectsEmptyKind(t *testing.T) {
	r := vault.NewRegistry()
	if err := r.Register(&stubFactory{kind: ""}); err == nil {
		t.Fatalf("Register with empty Kind did not return error")
	}
}

// TestBuildRejectsUnknownKind verifies Build fails cleanly when a
// spec refers to a kind that was never registered.
func TestBuildRejectsUnknownKind(t *testing.T) {
	r := vault.NewRegistry()
	_, err := r.Build(context.Background(), []vault.ProviderSpec{{Name: "x", Kind: "ghost"}})
	if err == nil {
		t.Fatalf("Build with unknown kind did not return error")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("expected error to name the unknown kind, got: %v", err)
	}
}

// TestBuildRejectsDuplicateProviderName covers the pre-flight
// duplicate-name check that runs before any subprocess work.
func TestBuildRejectsDuplicateProviderName(t *testing.T) {
	r := vault.NewRegistry()
	if err := r.Register(&stubFactory{kind: "fake"}); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	specs := []vault.ProviderSpec{
		{Name: "dup", Kind: "fake", Source: "a.toml"},
		{Name: "dup", Kind: "fake", Source: "b.toml"},
	}
	_, err := r.Build(context.Background(), specs)
	if err == nil {
		t.Fatalf("Build did not reject duplicate provider name")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected descriptive duplicate error, got: %v", err)
	}
}

// TestBuildClosesOpenedOnFailure: when a later Open call fails,
// Build must close the providers it already opened so we don't leak
// subprocess sessions.
func TestBuildClosesOpenedOnFailure(t *testing.T) {
	r := vault.NewRegistry()
	good := &stubFactory{kind: "good"}
	bad := &stubFactory{kind: "bad", openErr: errors.New("boom")}
	if err := r.Register(good); err != nil {
		t.Fatalf("Register good: %v", err)
	}
	if err := r.Register(bad); err != nil {
		t.Fatalf("Register bad: %v", err)
	}

	specs := []vault.ProviderSpec{
		{Name: "first", Kind: "good"},
		{Name: "second", Kind: "bad"},
	}
	_, err := r.Build(context.Background(), specs)
	if err == nil {
		t.Fatalf("Build did not propagate Open error")
	}
	if len(good.opened) != 1 {
		t.Fatalf("good factory opened %d providers, want 1", len(good.opened))
	}
	if good.opened[0].closed != 1 {
		t.Fatalf("opened provider closed %d times, want 1", good.opened[0].closed)
	}
}

// TestBundleGetUnknownProvider asserts AC: Bundle.Get returns an
// error for an unknown provider name.
func TestBundleGetUnknownProvider(t *testing.T) {
	r := vault.NewRegistry()
	if err := r.Register(&stubFactory{kind: "fake"}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	bundle, err := r.Build(context.Background(), []vault.ProviderSpec{{Name: "team", Kind: "fake"}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer bundle.CloseAll()

	_, err = bundle.Get("nope")
	if err == nil {
		t.Fatalf("Get(nope) returned no error")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected error to name missing provider, got: %v", err)
	}
}

// TestBundleNames asserts Names returns the sorted set of provider
// names held in the bundle, including the empty string for the
// anonymous singular shape. Added in Issue 4 alongside
// resolve.CheckProviderNameCollision to support R12 enforcement.
func TestBundleNames(t *testing.T) {
	r := vault.NewRegistry()
	if err := r.Register(&stubFactory{kind: "fake"}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Empty bundle: empty Names.
	empty, err := r.Build(context.Background(), nil)
	if err != nil {
		t.Fatalf("Build empty: %v", err)
	}
	if got := empty.Names(); len(got) != 0 {
		t.Errorf("empty Names should be empty, got %v", got)
	}
	defer empty.CloseAll()

	// Named + anonymous providers: Names includes both.
	bundle, err := r.Build(context.Background(), []vault.ProviderSpec{
		{Name: "beta", Kind: "fake"},
		{Name: "alpha", Kind: "fake"},
		{Name: "", Kind: "fake"},
	})
	if err != nil {
		t.Fatalf("Build named: %v", err)
	}
	defer bundle.CloseAll()

	got := bundle.Names()
	want := []string{"", "alpha", "beta"}
	if len(got) != len(want) {
		t.Fatalf("Names len = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Names[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestBundleCloseAllInvokesEveryProvider asserts AC: Bundle.CloseAll
// invokes Close on every opened provider.
func TestBundleCloseAllInvokesEveryProvider(t *testing.T) {
	r := vault.NewRegistry()
	f := &stubFactory{kind: "fake"}
	if err := r.Register(f); err != nil {
		t.Fatalf("Register: %v", err)
	}
	specs := []vault.ProviderSpec{
		{Name: "a", Kind: "fake"},
		{Name: "b", Kind: "fake"},
		{Name: "c", Kind: "fake"},
	}
	bundle, err := r.Build(context.Background(), specs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := bundle.CloseAll(); err != nil {
		t.Fatalf("CloseAll returned error: %v", err)
	}
	for _, p := range f.opened {
		if p.closed != 1 {
			t.Fatalf("provider %q closed %d times, want 1", p.name, p.closed)
		}
	}
}

// TestBundleCloseAllIdempotent covers the one-shot contract in the
// registry.go doc: CloseAll after a successful close is a no-op.
func TestBundleCloseAllIdempotent(t *testing.T) {
	r := vault.NewRegistry()
	f := &stubFactory{kind: "fake"}
	if err := r.Register(f); err != nil {
		t.Fatalf("Register: %v", err)
	}
	bundle, err := r.Build(context.Background(), []vault.ProviderSpec{{Name: "x", Kind: "fake"}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := bundle.CloseAll(); err != nil {
		t.Fatalf("first CloseAll: %v", err)
	}
	if err := bundle.CloseAll(); err != nil {
		t.Fatalf("second CloseAll: %v", err)
	}
	// Only the first close should have touched the provider.
	if f.opened[0].closed != 1 {
		t.Fatalf("provider closed %d times after double CloseAll, want 1", f.opened[0].closed)
	}
}

// TestBundleCloseAllAggregatesErrors checks that if multiple close
// calls fail, CloseAll aggregates them rather than stopping at the
// first.
func TestBundleCloseAllAggregatesErrors(t *testing.T) {
	r := vault.NewRegistry()
	f := &stubFactory{kind: "fake"}
	if err := r.Register(f); err != nil {
		t.Fatalf("Register: %v", err)
	}
	bundle, err := r.Build(context.Background(), []vault.ProviderSpec{
		{Name: "a", Kind: "fake"},
		{Name: "b", Kind: "fake"},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// Inject close errors on both.
	for _, p := range f.opened {
		p.closeErr = errors.New("close boom")
	}
	err = bundle.CloseAll()
	if err == nil {
		t.Fatalf("CloseAll did not return aggregated error")
	}
}

// TestRegistryUnregister covers the Unregister contract: a kind that
// was previously registered can be removed and then registered again;
// unknown and empty kinds return errors.
func TestRegistryUnregister(t *testing.T) {
	t.Run("register then unregister then register again", func(t *testing.T) {
		r := vault.NewRegistry()
		if err := r.Register(&stubFactory{kind: "fake"}); err != nil {
			t.Fatalf("first Register: %v", err)
		}
		if err := r.Unregister("fake"); err != nil {
			t.Fatalf("Unregister after Register returned error: %v", err)
		}
		if err := r.Register(&stubFactory{kind: "fake"}); err != nil {
			t.Fatalf("Register after Unregister returned error: %v", err)
		}
	})

	t.Run("unregister unknown kind returns error", func(t *testing.T) {
		r := vault.NewRegistry()
		err := r.Unregister("ghost")
		if err == nil {
			t.Fatalf("Unregister of unknown kind did not return error")
		}
		if !strings.Contains(err.Error(), "ghost") {
			t.Fatalf("expected error to name unknown kind, got: %v", err)
		}
	})

	t.Run("unregister empty kind returns error", func(t *testing.T) {
		r := vault.NewRegistry()
		err := r.Unregister("")
		if err == nil {
			t.Fatalf("Unregister of empty kind did not return error")
		}
	})
}

// TestDefaultRegistryInitialized asserts AC: DefaultRegistry is
// populated (initialised) by the package, ready for backend init()
// registration.
func TestDefaultRegistryInitialized(t *testing.T) {
	if vault.DefaultRegistry == nil {
		t.Fatalf("DefaultRegistry is nil")
	}
	// Registering a throwaway factory exercises the registry path;
	// use a unique kind so we don't collide with any real backend
	// that may init-register into DefaultRegistry.
	if err := vault.DefaultRegistry.Register(&stubFactory{kind: "registry-test-probe"}); err != nil {
		t.Fatalf("Register into DefaultRegistry failed: %v", err)
	}
}
