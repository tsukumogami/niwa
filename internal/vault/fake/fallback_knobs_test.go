package fake_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/fake"
)

func open(t *testing.T, cfg vault.ProviderConfig) vault.Provider {
	t.Helper()
	p, err := fake.NewFactory().Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestStoreIdentity(t *testing.T) {
	p := open(t, vault.ProviderConfig{})
	if _, ok := p.(vault.StoreIdentifier).StoreIdentity(vault.Ref{Key: "K"}); ok {
		t.Fatal("a provider without identity reported one")
	}

	p = open(t, vault.ProviderConfig{"identity": map[string]any{
		"api_domain": "HTTPS://Vault.Example/", "project_id": "p1", "environment": "dev", "folder_path": "base/",
	}})
	ider := p.(vault.StoreIdentifier)
	id, ok := ider.StoreIdentity(vault.Ref{Key: "K"})
	want := vault.Identity{Kind: "fake", APIDomain: "https://vault.example", ProjectID: "p1", Environment: "dev", FolderPath: "/base"}
	if !ok || id != want {
		t.Fatalf("identity = %+v, %v; want %+v", id, ok, want)
	}
	id, _ = ider.StoreIdentity(vault.Ref{Key: "K", Path: "/other"})
	if id.FolderPath != "/other" {
		t.Fatalf("folder path = %q, want the reference's", id.FolderPath)
	}
}

func TestFailClassKnobs(t *testing.T) {
	cases := []struct {
		name   string
		want   vault.FailureClass
		status int64
	}{
		{"unauthenticated", vault.FailureClass{Class: vault.ClassUnauthenticated, Reason: vault.ReasonLoggedOut, HTTPStatus: 403}, 403},
		{"unreachable", vault.FailureClass{Class: vault.ClassUnreachable, Reason: vault.ReasonUnreachable}, 0},
		{"timed_out", vault.FailureClass{Class: vault.ClassUnreachable, Reason: vault.ReasonTimedOut}, 0},
		{"answered", vault.FailureClass{Class: vault.ClassAnswered, HTTPStatus: 404}, 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := open(t, vault.ProviderConfig{
				"values":      map[string]any{"KNOWN": "known-value-1"},
				"fail_class":  tc.name,
				"fail_status": tc.status,
			})
			if _, _, err := p.Resolve(context.Background(), vault.Ref{Key: "KNOWN"}); err != nil {
				t.Fatalf("a known key failed: %v", err)
			}
			_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "OTHER"})
			var class *vault.FailureClass
			if !errors.As(err, &class) || *class != tc.want {
				t.Fatalf("class = %+v, want %+v (err %v)", class, tc.want, err)
			}
			wantUnreachable := tc.want.Class != vault.ClassAnswered
			if errors.Is(err, vault.ErrProviderUnreachable) != wantUnreachable {
				t.Fatalf("errors.Is(ErrProviderUnreachable) = %v", !wantUnreachable)
			}
		})
	}
}

func TestFailPlainIsUnclassified(t *testing.T) {
	p := open(t, vault.ProviderConfig{"fail_plain": true, "fail_class": "unauthenticated"})
	_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "OTHER"})
	var class *vault.FailureClass
	if err == nil || errors.As(err, &class) || errors.Is(err, vault.ErrProviderUnreachable) || errors.Is(err, vault.ErrKeyNotFound) {
		t.Fatalf("fail_plain returned %v", err)
	}
}

func TestFallbackKnobsRejectMalformedConfig(t *testing.T) {
	for name, cfg := range map[string]vault.ProviderConfig{
		"identity type":    {"identity": "x"},
		"identity field":   {"identity": map[string]any{"project_id": 1}},
		"fail_class type":  {"fail_class": 1},
		"fail_class value": {"fail_class": "sideways"},
		"fail_status type": {"fail_status": "403"},
		"fail_plain type":  {"fail_plain": "yes"},
	} {
		if _, err := fake.NewFactory().Open(context.Background(), cfg); err == nil {
			t.Errorf("%s: Open accepted %v", name, cfg)
		}
	}
}
