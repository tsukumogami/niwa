package fake_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret/reveal"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/fake"
)

// fakePlaintext is a synthetic fixture value; nothing real depends
// on it.
const fakePlaintext = "very-fake-plaintext-not-a-real-secret"

// TestFactoryKind locks in the constant "fake" advertised by the
// factory — the Registry indexes on exactly this string.
func TestFactoryKind(t *testing.T) {
	if got := fake.NewFactory().Kind(); got != "fake" {
		t.Fatalf("Factory.Kind() = %q, want %q", got, "fake")
	}
}

// TestNotRegisteredInDefaultRegistry asserts the deliberate design
// choice: the fake backend must NOT be registered in
// vault.DefaultRegistry. If this test fails, someone added an init()
// somewhere that auto-registers the fake, which would let production
// code paths (any caller using DefaultRegistry) accidentally open the
// test fixture.
//
// We assert by trying to register the fake into DefaultRegistry
// directly: if it's already there, Register returns an error; if it
// isn't, Register succeeds and we roll it back with Unregister so we
// don't pollute DefaultRegistry for later tests.
func TestNotRegisteredInDefaultRegistry(t *testing.T) {
	factory := fake.NewFactory()
	if err := vault.DefaultRegistry.Register(factory); err != nil {
		// Already registered — that's the failure mode this test
		// exists to prevent.
		t.Fatalf("fake backend is registered in DefaultRegistry; "+
			"this violates the fixture-isolation invariant. "+
			"Register error: %v", err)
	}
	// Rollback: we just registered it, so un-register it now so the
	// rest of the test suite sees a clean DefaultRegistry.
	if err := vault.DefaultRegistry.Unregister(factory.Kind()); err != nil {
		t.Fatalf("rollback Unregister failed: %v", err)
	}
}

// TestFactoryOpenAndResolve covers the end-to-end happy path:
// Factory.Open returns a Provider that resolves preconfigured
// values.
func TestFactoryOpenAndResolve(t *testing.T) {
	f := fake.NewFactory()
	cfg := vault.ProviderConfig{
		"name": "team",
		"values": map[string]string{
			"api-token": fakePlaintext,
		},
	}
	provider, err := f.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer provider.Close()

	if provider.Name() != "team" {
		t.Fatalf("Name() = %q, want %q", provider.Name(), "team")
	}
	if provider.Kind() != "fake" {
		t.Fatalf("Kind() = %q, want %q", provider.Kind(), "fake")
	}

	val, token, err := provider.Resolve(context.Background(), vault.Ref{ProviderName: "team", Key: "api-token"})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if got := string(reveal.UnsafeReveal(val)); got != fakePlaintext {
		t.Fatalf("Resolve returned value %q, want %q", got, fakePlaintext)
	}
	if token.Token == "" {
		t.Fatalf("Resolve returned empty VersionToken.Token")
	}
	if !strings.HasPrefix(token.Provenance, "fake:team:api-token") {
		t.Fatalf("Provenance = %q, want fake:team:api-token prefix", token.Provenance)
	}
}

// TestResolveDeterministicToken asserts the doc contract: the fake's
// VersionToken.Token is a deterministic SHA-256 of the value bytes.
func TestResolveDeterministicToken(t *testing.T) {
	provider, err := fake.NewFactory().Open(context.Background(), vault.ProviderConfig{
		"values": map[string]string{"k": "same-value-long"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer provider.Close()

	_, t1, _ := provider.Resolve(context.Background(), vault.Ref{Key: "k"})
	_, t2, _ := provider.Resolve(context.Background(), vault.Ref{Key: "k"})
	if t1.Token == "" || t1.Token != t2.Token {
		t.Fatalf("tokens differ or empty: %q vs %q", t1.Token, t2.Token)
	}
}

// TestResolveMissingKeyReturnsErrKeyNotFound asserts AC: Resolve
// returns ErrKeyNotFound for a missing key.
func TestResolveMissingKeyReturnsErrKeyNotFound(t *testing.T) {
	provider, err := fake.NewFactory().Open(context.Background(), vault.ProviderConfig{
		"values": map[string]string{"present": "x-marks-the-spot"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer provider.Close()

	_, _, err = provider.Resolve(context.Background(), vault.Ref{Key: "missing"})
	if err == nil {
		t.Fatalf("Resolve(missing) returned no error")
	}
	if !errors.Is(err, vault.ErrKeyNotFound) {
		t.Fatalf("expected errors.Is(err, ErrKeyNotFound), got: %v", err)
	}
}

// TestFailOpenReturnsUnreachable exercises the fail_open config:
// unknown keys bubble up as ErrProviderUnreachable, modelling a
// transient-outage fixture for the resolver-layer tests.
func TestFailOpenReturnsUnreachable(t *testing.T) {
	provider, err := fake.NewFactory().Open(context.Background(), vault.ProviderConfig{
		"fail_open": true,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer provider.Close()

	_, _, err = provider.Resolve(context.Background(), vault.Ref{Key: "any"})
	if !errors.Is(err, vault.ErrProviderUnreachable) {
		t.Fatalf("expected ErrProviderUnreachable, got: %v", err)
	}
}

// TestResolveBatch asserts AC: ResolveBatch returns a result for
// every requested ref, marking missing keys with ErrKeyNotFound.
func TestResolveBatch(t *testing.T) {
	provider, err := fake.NewFactory().Open(context.Background(), vault.ProviderConfig{
		"values": map[string]string{
			"a": "alpha-long-enough",
			"b": "bravo-long-enough",
		},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer provider.Close()

	br, ok := provider.(vault.BatchResolver)
	if !ok {
		t.Fatalf("fake provider does not satisfy BatchResolver")
	}

	refs := []vault.Ref{{Key: "a"}, {Key: "missing"}, {Key: "b"}}
	results, err := br.ResolveBatch(context.Background(), refs)
	if err != nil {
		t.Fatalf("ResolveBatch returned error: %v", err)
	}
	if len(results) != len(refs) {
		t.Fatalf("ResolveBatch returned %d results, want %d", len(results), len(refs))
	}
	if results[0].Err != nil {
		t.Fatalf("result[0].Err = %v, want nil", results[0].Err)
	}
	if got := string(reveal.UnsafeReveal(results[0].Value)); got != "alpha-long-enough" {
		t.Fatalf("result[0].Value = %q, want alpha-long-enough", got)
	}
	if !errors.Is(results[1].Err, vault.ErrKeyNotFound) {
		t.Fatalf("result[1].Err = %v, want ErrKeyNotFound", results[1].Err)
	}
	if results[2].Err != nil {
		t.Fatalf("result[2].Err = %v, want nil", results[2].Err)
	}
}

// TestCloseClearsValues asserts that after Close, Resolve returns an
// ErrProviderUnreachable wrap. Close is idempotent: a second call
// MUST succeed.
func TestCloseClearsValues(t *testing.T) {
	provider, err := fake.NewFactory().Open(context.Background(), vault.ProviderConfig{
		"values": map[string]string{"k": "present-long-enough"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, _, err := provider.Resolve(context.Background(), vault.Ref{Key: "k"}); err != nil {
		t.Fatalf("pre-close Resolve failed: %v", err)
	}

	if err := provider.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if err := provider.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}

	_, _, err = provider.Resolve(context.Background(), vault.Ref{Key: "k"})
	if !errors.Is(err, vault.ErrProviderUnreachable) {
		t.Fatalf("after Close, Resolve returned %v, want ErrProviderUnreachable", err)
	}
}

// TestResolveReturnsSecretValue ensures the returned Value redacts
// through fmt — a cheap integration check that the fake backend is
// correctly wrapping plaintext in secret.Value and not accidentally
// leaking it through a formatter.
func TestResolveReturnsSecretValue(t *testing.T) {
	provider, err := fake.NewFactory().Open(context.Background(), vault.ProviderConfig{
		"values": map[string]string{"k": fakePlaintext},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer provider.Close()

	val, _, err := provider.Resolve(context.Background(), vault.Ref{Key: "k"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	for _, format := range []string{"%s", "%v", "%+v", "%q", "%#v"} {
		got := fmt.Sprintf(format, val)
		if strings.Contains(got, fakePlaintext) {
			t.Fatalf("format %s leaked plaintext: %q", format, got)
		}
		if !strings.Contains(got, "***") {
			t.Fatalf("format %s missing placeholder: %q", format, got)
		}
	}
}

// TestOpenRejectsMalformedConfig covers defensive parsing: wrong
// types for recognised keys surface as Open errors so tests catch
// typos in ProviderConfig construction.
func TestOpenRejectsMalformedConfig(t *testing.T) {
	cases := []struct {
		name   string
		config vault.ProviderConfig
	}{
		{"name wrong type", vault.ProviderConfig{"name": 42}},
		{"values wrong type", vault.ProviderConfig{"values": "not a map"}},
		{"fail_open wrong type", vault.ProviderConfig{"fail_open": "yes"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := fake.NewFactory().Open(context.Background(), c.config)
			if err == nil {
				t.Fatalf("Open with %s should have failed", c.name)
			}
		})
	}
}
