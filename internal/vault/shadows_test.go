package vault_test

import (
	"context"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault"
)

// buildBundleWithNames returns a Bundle that has a provider registered
// under each of the given names. The backend is the stubFactory from
// registry_test.go; stub providers don't need real resolution because
// DetectProviderShadows only inspects Bundle.Names().
func buildBundleWithNames(t *testing.T, names ...string) *vault.Bundle {
	t.Helper()
	r := vault.NewRegistry()
	f := &stubFactory{kind: "fake"}
	if err := r.Register(f); err != nil {
		t.Fatalf("Register: %v", err)
	}
	specs := make([]vault.ProviderSpec, 0, len(names))
	for _, n := range names {
		specs = append(specs, vault.ProviderSpec{
			Name:   n,
			Kind:   "fake",
			Config: vault.ProviderConfig{"name": n},
			Source: "test-fixture",
		})
	}
	b, err := r.Build(context.Background(), specs)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	t.Cleanup(func() { _ = b.CloseAll() })
	return b
}

func TestDetectProviderShadowsNameCollision(t *testing.T) {
	team := buildBundleWithNames(t, "corp", "dev")
	personal := buildBundleWithNames(t, "corp", "extra")

	got := vault.DetectProviderShadows(team, personal)
	if len(got) != 1 {
		t.Fatalf("want 1 shadow, got %d: %+v", len(got), got)
	}
	if got[0].Name != "corp" {
		t.Errorf("Name = %q, want corp", got[0].Name)
	}
}

func TestDetectProviderShadowsAnonymous(t *testing.T) {
	// Both bundles declare the anonymous singular provider (empty
	// name). DetectProviderShadows MUST report the collision.
	team := buildBundleWithNames(t, "")
	personal := buildBundleWithNames(t, "")

	got := vault.DetectProviderShadows(team, personal)
	if len(got) != 1 {
		t.Fatalf("want 1 shadow, got %d", len(got))
	}
	if got[0].Name != "" {
		t.Errorf("Name = %q, want empty (anonymous)", got[0].Name)
	}
}

func TestDetectProviderShadowsNoCollision(t *testing.T) {
	team := buildBundleWithNames(t, "corp")
	personal := buildBundleWithNames(t, "personal")

	if got := vault.DetectProviderShadows(team, personal); got != nil {
		t.Errorf("want nil, got %+v", got)
	}
}

func TestDetectProviderShadowsNilBundles(t *testing.T) {
	personal := buildBundleWithNames(t, "personal")
	if got := vault.DetectProviderShadows(nil, personal); got != nil {
		t.Errorf("nil team must yield nil, got %+v", got)
	}
	if got := vault.DetectProviderShadows(personal, nil); got != nil {
		t.Errorf("nil personal must yield nil, got %+v", got)
	}
	if got := vault.DetectProviderShadows(nil, nil); got != nil {
		t.Errorf("both nil must yield nil, got %+v", got)
	}
}

func TestDetectProviderShadowsSorted(t *testing.T) {
	team := buildBundleWithNames(t, "zebra", "alpha", "mango")
	personal := buildBundleWithNames(t, "mango", "alpha", "zebra")

	got := vault.DetectProviderShadows(team, personal)
	if len(got) != 3 {
		t.Fatalf("want 3 shadows, got %d: %+v", len(got), got)
	}
	want := []string{"alpha", "mango", "zebra"}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("got[%d].Name = %q, want %q", i, got[i].Name, name)
		}
	}
}
