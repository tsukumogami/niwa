// Package fake provides an in-memory vault backend for tests. It is
// intentionally NOT registered with vault.DefaultRegistry — tests
// that use the fake build a fresh Registry via vault.NewRegistry and
// call Register themselves. Keeping fake out of the production
// registry ensures shipping code can never accidentally resolve
// against a test fixture.
//
// Config shape:
//
//	{
//	    "values":    map[string]string // key → plaintext
//	    "fail_open": bool              // when true, unknown keys return ErrProviderUnreachable
//	}
//
// VersionToken.Token is a deterministic SHA-256 hex digest of the
// value bytes. This is a derivation from post-decrypt plaintext,
// which real backends MUST NOT do (per DESIGN-vault-integration.md
// Decision 3 notes on version-token derivation). It is acceptable
// for the fake because the fixture values are not real secrets —
// they exist only to exercise the plumbing.
package fake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// Kind is the factory kind string used by the fake backend.
const Kind = "fake"

// Factory is the vault.Factory implementation for the fake backend.
// Construct one with NewFactory and register it on a fresh
// vault.Registry via Registry.Register.
type Factory struct{}

// NewFactory returns a ready-to-register Factory.
func NewFactory() *Factory {
	return &Factory{}
}

// Kind returns the factory kind (constant "fake").
func (Factory) Kind() string {
	return Kind
}

// Open constructs a Provider from config. Recognised keys:
//
//	"name"      string                  // provider name (defaults to "")
//	"values"    map[string]string       // preconfigured values
//	"fail_open" bool                    // return ErrProviderUnreachable for unknown keys
//
// Other keys are ignored; malformed types for recognised keys cause
// Open to return an error.
func (Factory) Open(_ context.Context, config vault.ProviderConfig) (vault.Provider, error) {
	p := &Provider{values: map[string]string{}}

	if raw, ok := config["name"]; ok {
		name, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("fake: config[name] must be string, got %T", raw)
		}
		p.name = name
	}

	if raw, ok := config["values"]; ok {
		switch values := raw.(type) {
		case map[string]string:
			for k, v := range values {
				p.values[k] = v
			}
		case map[string]any:
			// TOML decoding produces map[string]any by default; accept
			// it as long as every entry is a string. This keeps the
			// fake usable both from Go-level tests (that build their
			// own map[string]string) and from tests that drive the
			// pipeline through a TOML fixture.
			for k, v := range values {
				s, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("fake: config[values][%q] must be string, got %T", k, v)
				}
				p.values[k] = s
			}
		default:
			return nil, fmt.Errorf("fake: config[values] must be map[string]string or map[string]any, got %T", raw)
		}
	}

	if raw, ok := config["fail_open"]; ok {
		failOpen, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("fake: config[fail_open] must be bool, got %T", raw)
		}
		p.failOpen = failOpen
	}

	return p, nil
}

// Provider is the fake backend's vault.Provider implementation.
// Safe for concurrent Resolve/ResolveBatch calls. Close is one-shot:
// subsequent Resolve calls after Close return an error.
type Provider struct {
	name     string
	failOpen bool

	mu     sync.Mutex
	values map[string]string
	closed bool
}

// Name returns the configured provider name (empty for anonymous).
func (p *Provider) Name() string {
	return p.name
}

// Kind returns "fake".
func (p *Provider) Kind() string {
	return Kind
}

// Resolve looks up ref.Key in the preconfigured values map. A
// missing key returns vault.ErrKeyNotFound (or
// vault.ErrProviderUnreachable when fail_open is true). The returned
// VersionToken is a deterministic SHA-256 of the value bytes;
// Provenance is "fake:<provider-name>:<key>".
func (p *Provider) Resolve(_ context.Context, ref vault.Ref) (secret.Value, vault.VersionToken, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return secret.Value{}, vault.VersionToken{}, fmt.Errorf("fake: provider %q: %w", p.name, vault.ErrProviderUnreachable)
	}
	raw, ok := p.values[ref.Key]
	if !ok {
		if p.failOpen {
			return secret.Value{}, vault.VersionToken{}, fmt.Errorf("fake: provider %q unreachable: %w", p.name, vault.ErrProviderUnreachable)
		}
		return secret.Value{}, vault.VersionToken{}, fmt.Errorf("fake: provider %q key %q: %w", p.name, ref.Key, vault.ErrKeyNotFound)
	}
	val := secret.New([]byte(raw), secret.Origin{
		ProviderName: p.name,
		Key:          ref.Key,
		VersionToken: tokenFor(raw),
	})
	return val, vault.VersionToken{
		Token:      tokenFor(raw),
		Provenance: fmt.Sprintf("fake:%s:%s", p.name, ref.Key),
	}, nil
}

// ResolveBatch satisfies vault.BatchResolver. It resolves every ref
// and returns a slice of BatchResults in input order; missing keys
// are signaled by setting BatchResult.Err, not by dropping the
// result.
func (p *Provider) ResolveBatch(ctx context.Context, refs []vault.Ref) ([]vault.BatchResult, error) {
	results := make([]vault.BatchResult, len(refs))
	for i, ref := range refs {
		val, token, err := p.Resolve(ctx, ref)
		results[i] = vault.BatchResult{
			Ref:   ref,
			Value: val,
			Token: token,
			Err:   err,
		}
	}
	return results, nil
}

// Close clears the preconfigured values map. Subsequent Resolve
// calls return ErrProviderUnreachable. Close is idempotent: calling
// it twice is a no-op and never returns an error.
func (p *Provider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.values = nil
	return nil
}

// tokenFor returns the SHA-256 hex digest of the value bytes. This
// is the fake's deterministic version-token derivation; see the
// package doc for why it is acceptable only for a test fixture.
func tokenFor(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
