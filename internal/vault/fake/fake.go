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
//	    "no_client": bool              // when true, unknown keys return ErrClientNotInstalled
//	    "identity":  map[string]string // store identity (see Factory.Open)
//	    "fail_class": string           // classified failure for unknown keys (see Factory.Open)
//	    "fail_status": int             // HTTP status carried by fail_class
//	    "fail_plain": bool             // unknown keys return an unclassified error
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
//	"no_client" bool                    // return ErrClientNotInstalled for unknown keys
//	"identity"  map[string]string       // api_domain, project_id, environment, folder_path
//	"fail_class" string                 // unauthenticated | unreachable | timed_out | answered
//	"fail_status" int                   // FailureClass.HTTPStatus for fail_class
//	"fail_plain" bool                   // return an error with no sentinel and no class
//
// With "identity" set the provider answers StoreIdentity with kind
// "fake" and the given fields, taking the folder path from the
// reference when it names one; without it StoreIdentity reports no
// identity. "fail_class" makes unknown keys fail with a
// vault.ClassifiedError: "unauthenticated" (reason logged out),
// "unreachable" (reason unreachable), "timed_out" (unreachable, reason
// timed out) or "answered". For unknown keys no_client wins, then
// fail_plain, fail_class and fail_open; otherwise ErrKeyNotFound.
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

	if raw, ok := config["no_client"]; ok {
		noClient, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("fake: config[no_client] must be bool, got %T", raw)
		}
		p.noClient = noClient
	}

	if raw, ok := config["identity"]; ok {
		fields, err := stringMap(raw)
		if err != nil {
			return nil, fmt.Errorf("fake: config[identity]: %w", err)
		}
		p.identity = &vault.Identity{
			Kind:        Kind,
			APIDomain:   fields["api_domain"],
			ProjectID:   fields["project_id"],
			Environment: fields["environment"],
			FolderPath:  fields["folder_path"],
		}
	}

	if raw, ok := config["fail_class"]; ok {
		name, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("fake: config[fail_class] must be string, got %T", raw)
		}
		class, ok := failClasses[name]
		if !ok {
			return nil, fmt.Errorf("fake: config[fail_class] %q is not one of unauthenticated, unreachable, timed_out, answered", name)
		}
		p.failClass = &class
	}

	if raw, ok := config["fail_status"]; ok {
		switch status := raw.(type) {
		case int:
			p.failStatus = status
		case int64:
			p.failStatus = int(status)
		default:
			return nil, fmt.Errorf("fake: config[fail_status] must be an integer, got %T", raw)
		}
	}

	if raw, ok := config["fail_plain"]; ok {
		failPlain, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("fake: config[fail_plain] must be bool, got %T", raw)
		}
		p.failPlain = failPlain
	}

	return p, nil
}

// failClasses maps fail_class names to the class and reason they return.
var failClasses = map[string]vault.FailureClass{
	"unauthenticated": {Class: vault.ClassUnauthenticated, Reason: vault.ReasonLoggedOut},
	"unreachable":     {Class: vault.ClassUnreachable, Reason: vault.ReasonUnreachable},
	"timed_out":       {Class: vault.ClassUnreachable, Reason: vault.ReasonTimedOut},
	"answered":        {Class: vault.ClassAnswered},
}

// stringMap accepts a map[string]string, or a map[string]any whose
// values are all strings (the TOML decoder's shape).
func stringMap(raw any) (map[string]string, error) {
	switch m := raw.(type) {
	case map[string]string:
		return m, nil
	case map[string]any:
		out := make(map[string]string, len(m))
		for k, v := range m {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("[%q] must be string, got %T", k, v)
			}
			out[k] = s
		}
		return out, nil
	}
	return nil, fmt.Errorf("must be map[string]string or map[string]any, got %T", raw)
}

// Provider is the fake backend's vault.Provider implementation.
// Safe for concurrent Resolve/ResolveBatch calls. Close is one-shot:
// subsequent Resolve calls after Close return an error.
type Provider struct {
	name       string
	failOpen   bool
	noClient   bool
	failPlain  bool
	failClass  *vault.FailureClass
	failStatus int
	identity   *vault.Identity

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
// missing key returns vault.ErrKeyNotFound, unless a failure knob is
// set: no_client (vault.ErrClientNotInstalled; it wins, since a client
// that is not there cannot report anything else), then fail_plain (an
// unclassified error), fail_class (a vault.ClassifiedError) and
// fail_open (vault.ErrProviderUnreachable). The returned
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
		if p.noClient {
			return secret.Value{}, vault.VersionToken{}, fmt.Errorf("fake: provider %q: %w", p.name, vault.ErrClientNotInstalled)
		}
		if p.failPlain {
			return secret.Value{}, vault.VersionToken{}, fmt.Errorf("fake: provider %q key %q failed", p.name, ref.Key)
		}
		if p.failClass != nil {
			class := *p.failClass
			class.HTTPStatus = p.failStatus
			return secret.Value{}, vault.VersionToken{}, vault.Classify(
				fmt.Errorf("fake: provider %q key %q failed %s", p.name, ref.Key, class.Class), class)
		}
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

// StoreIdentity implements vault.StoreIdentifier. It reports no
// identity unless the provider was configured with one; the folder
// path is ref.Path when the reference names one.
func (p *Provider) StoreIdentity(ref vault.Ref) (vault.Identity, bool) {
	if p.identity == nil {
		return vault.Identity{}, false
	}
	id := *p.identity
	if ref.Path != "" {
		id.FolderPath = ref.Path
	}
	return vault.NormalizeIdentity(id), true
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
