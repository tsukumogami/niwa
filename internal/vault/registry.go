package vault

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Registry indexes Factory instances by Kind. Backends register
// their Factory with a Registry (typically DefaultRegistry) via
// init(); the resolver stage then calls Build with the set of
// ProviderSpecs parsed from config to obtain a ready-to-use Bundle.
//
// A Registry is safe for concurrent use: Register and Build hold a
// RWMutex so backends registering from init() do not race with the
// resolver stage reading factories.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry returns an empty Registry. Tests should construct a
// fresh Registry rather than mutating DefaultRegistry so test order
// does not influence behavior.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// DefaultRegistry is the production-time registry that real backends
// register their Factory into via init(). Tests that exercise the
// fake backend should use NewRegistry() and Register explicitly; the
// fake backend intentionally does NOT register with DefaultRegistry
// so that production code paths never see it.
var DefaultRegistry = NewRegistry()

// Register adds f to the registry. Register returns an error if a
// Factory with the same Kind is already registered; callers that
// init()-register via a package-level side effect should treat that
// error as a programming error (panic if desired). The empty string
// is not a valid Kind and is rejected.
func (r *Registry) Register(f Factory) error {
	if f == nil {
		return fmt.Errorf("vault: cannot register nil Factory")
	}
	kind := f.Kind()
	if kind == "" {
		return fmt.Errorf("vault: Factory.Kind() returned empty string")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[kind]; exists {
		return fmt.Errorf("vault: Factory kind %q is already registered", kind)
	}
	r.factories[kind] = f
	return nil
}

// Unregister removes the Factory registered for kind. It returns an
// error if kind is empty or if no Factory is registered for kind.
//
// Unregister is primarily intended for tests that need to probe the
// Register path against DefaultRegistry and then roll back so they do
// not leak registrations into later tests. Production code should not
// call Unregister: backend registrations via init() are intended to
// be permanent for the process lifetime.
func (r *Registry) Unregister(kind string) error {
	if kind == "" {
		return fmt.Errorf("vault: cannot unregister empty Kind")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[kind]; !exists {
		return fmt.Errorf("vault: Factory kind %q is not registered", kind)
	}
	delete(r.factories, kind)
	return nil
}

// Build opens a Provider for each ProviderSpec and returns a Bundle
// holding them. Provider names in specs must be unique within the
// slice; Build returns an error on duplicates. If any Factory.Open
// fails, Build closes the Providers it already opened and returns
// the error.
//
// Build takes ctx as a hand to both the Factory and so individual
// providers can register their cancellation signal. ctx is not
// stored in the Bundle.
func (r *Registry) Build(ctx context.Context, specs []ProviderSpec) (*Bundle, error) {
	b := &Bundle{providers: map[string]Provider{}}

	// Early duplicate-name detection — return before opening any
	// subprocesses so we don't leak a half-opened bundle on a pure
	// config error.
	seen := map[string]string{}
	for _, spec := range specs {
		if prev, ok := seen[spec.Name]; ok {
			return nil, fmt.Errorf("vault: duplicate provider name %q (already declared in %s)", spec.Name, prev)
		}
		seen[spec.Name] = spec.Source
	}

	for _, spec := range specs {
		r.mu.RLock()
		factory, ok := r.factories[spec.Kind]
		r.mu.RUnlock()
		if !ok {
			_ = b.CloseAll()
			return nil, fmt.Errorf("vault: no factory registered for kind %q (provider %q)", spec.Kind, spec.Name)
		}
		p, err := factory.Open(ctx, spec.Config)
		if err != nil {
			_ = b.CloseAll()
			return nil, fmt.Errorf("vault: factory %q failed to open provider %q: %w", spec.Kind, spec.Name, err)
		}
		b.providers[spec.Name] = p
	}
	return b, nil
}

// Bundle holds the set of Providers opened for a single apply (or
// resolve call). Bundle is read-only after Build returns: callers
// look up a provider with Get, and release all subprocess resources
// with CloseAll.
//
// Bundle is safe for concurrent Get calls. CloseAll is one-shot:
// calling it twice is a no-op (the internal map is cleared after the
// first successful pass).
type Bundle struct {
	mu        sync.Mutex
	providers map[string]Provider
}

// Get returns the Provider registered under name, or an error if no
// such provider exists. An empty name selects the anonymous singular
// provider (the Bundle entry whose Name is "").
func (b *Bundle) Get(name string) (Provider, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.providers[name]
	if !ok {
		return nil, fmt.Errorf("vault: no provider named %q (known: %s)", name, strings.Join(knownProviderNames(b.providers), ", "))
	}
	return p, nil
}

// HasNamedProviders reports whether the Bundle contains any provider
// registered under a non-empty name. Callers use this to select the
// URI parse mode for references destined against this bundle —
// anonymous-provider files use ParseAnonymous; named-provider files
// use ParseNamed.
func (b *Bundle) HasNamedProviders() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for name := range b.providers {
		if name != "" {
			return true
		}
	}
	return false
}

// Names returns the sorted list of provider names held by this
// Bundle. The anonymous singular provider contributes the empty
// string; named providers contribute their respective keys.
//
// Used by the resolver stage to detect R12 provider-name collisions
// between team and personal bundles without exposing the underlying
// map. Safe for concurrent use alongside Get.
func (b *Bundle) Names() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	names := make([]string, 0, len(b.providers))
	for name := range b.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CloseAll invokes Close on every Provider in the bundle. It
// aggregates any errors into a single error value. CloseAll is
// idempotent: calling it after a successful close is a no-op.
func (b *Bundle) CloseAll() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.providers) == 0 {
		return nil
	}
	// Copy the providers out so we can release them deterministically
	// even if Close panics on one backend.
	names := make([]string, 0, len(b.providers))
	for name := range b.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	var errs []error
	for _, name := range names {
		if err := b.providers[name].Close(); err != nil {
			errs = append(errs, fmt.Errorf("vault: closing provider %q: %w", name, err))
		}
	}
	// Clear the map so subsequent calls are no-ops.
	b.providers = map[string]Provider{}
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	return fmt.Errorf("vault: %d errors closing providers: %v", len(errs), errs)
}

// knownProviderNames returns the sorted list of provider names in m,
// used only to compose helpful error messages.
func knownProviderNames(m map[string]Provider) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		if name == "" {
			names = append(names, "(anonymous)")
		} else {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
