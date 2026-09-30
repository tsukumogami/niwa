package vault_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault"
)

// A ClassifiedError reads exactly like its inner error, still matches the
// inner error's sentinels, and exposes its class to errors.As.
func TestClassifiedError(t *testing.T) {
	inner := fmt.Errorf("infisical: export exited 1 (auth failure): Response Code: 403: %w", vault.ErrProviderUnreachable)
	ce := &vault.ClassifiedError{Err: inner, Class: vault.FailureClass{
		Class: vault.ClassUnauthenticated, Reason: vault.ReasonLoggedOut, HTTPStatus: 403,
	}}

	if ce.Error() != inner.Error() {
		t.Errorf("Error() = %q, want the inner text %q", ce.Error(), inner.Error())
	}
	if !errors.Is(ce, vault.ErrProviderUnreachable) {
		t.Error("errors.Is(ce, ErrProviderUnreachable) = false, want true")
	}
	if errors.Is(ce, vault.ErrKeyNotFound) {
		t.Error("errors.Is(ce, ErrKeyNotFound) = true, want false")
	}
	var fc *vault.FailureClass
	if !errors.As(ce, &fc) {
		t.Fatal("errors.As(ce, *FailureClass) = false, want true")
	}
	if fc.Class != vault.ClassUnauthenticated || fc.Reason != vault.ReasonLoggedOut || fc.HTTPStatus != 403 {
		t.Errorf("class = %+v, want unauthenticated / logged out / 403", *fc)
	}

	// Wrapped further up, the class and the text both survive.
	outer := fmt.Errorf("vault: resolving: %w", ce)
	if !errors.As(outer, &fc) || fc.Class != vault.ClassUnauthenticated {
		t.Errorf("class lost through an outer wrap: %v", outer)
	}
	if outer.Error() != "vault: resolving: "+inner.Error() {
		t.Errorf("outer text = %q", outer.Error())
	}

	// A plain error has no class.
	if errors.As(inner, &fc) {
		t.Error("an unclassified error reported a FailureClass")
	}
}

// Classify adds ErrProviderUnreachable to an unauthenticated or
// unreachable failure without changing its text, and leaves an answered
// failure's sentinels alone.
func TestClassify(t *testing.T) {
	plain := errors.New("infisical: export exited 1: error: No valid login session found")
	for _, kind := range []vault.FailureKind{vault.ClassUnauthenticated, vault.ClassUnreachable} {
		err := vault.Classify(plain, vault.FailureClass{Class: kind})
		if err.Error() != plain.Error() {
			t.Errorf("%s: text = %q, want %q", kind, err.Error(), plain.Error())
		}
		if !errors.Is(err, vault.ErrProviderUnreachable) || !errors.Is(err, plain) {
			t.Errorf("%s: want both ErrProviderUnreachable and the inner error in the chain", kind)
		}
		var fc *vault.FailureClass
		if !errors.As(err, &fc) || fc.Class != kind {
			t.Errorf("%s: class not found", kind)
		}
	}
	answered := vault.Classify(plain, vault.FailureClass{Class: vault.ClassAnswered})
	if errors.Is(answered, vault.ErrProviderUnreachable) || answered.Error() != plain.Error() {
		t.Errorf("answered: %v gained a sentinel or changed text", answered)
	}
	// An error that already carries the sentinel isn't wrapped again.
	already := fmt.Errorf("x: %w", vault.ErrProviderUnreachable)
	ce := vault.Classify(already, vault.FailureClass{Class: vault.ClassUnreachable}).(*vault.ClassifiedError)
	if ce.Err != already {
		t.Errorf("inner error rewrapped: %T", ce.Err)
	}
}

func TestNormalizeIdentity(t *testing.T) {
	cases := []struct {
		in, wantDomain, wantPath string
	}{
		{"https://app.infisical.com", "https://app.infisical.com", "/a/b"},
		{"https://App.Infisical.com/api", "https://app.infisical.com", "/a/b"},
		{"https://app.infisical.com/", "https://app.infisical.com", "/a/b"},
		{"HTTPS://user:pw@Secrets.Example.COM:8443/api/v1/", "https://secrets.example.com:8443", "/a/b"},
	}
	for _, c := range cases {
		for _, path := range []string{"/a/b", "a/b", "/a/b/"} {
			got := vault.NormalizeIdentity(vault.Identity{
				Kind: "infisical", APIDomain: c.in, ProjectID: "p", Environment: "dev", FolderPath: path,
			})
			want := vault.Identity{Kind: "infisical", APIDomain: c.wantDomain, ProjectID: "p", Environment: "dev", FolderPath: c.wantPath}
			if got != want {
				t.Errorf("NormalizeIdentity(%q, %q) = %+v, want %+v", c.in, path, got, want)
			}
		}
	}
	// A value that isn't an absolute URL keeps no credentials.
	if got := vault.NormalizeIdentity(vault.Identity{APIDomain: "user:secret@Vault.Example.com/"}).APIDomain; got != "vault.example.com" {
		t.Errorf("scheme-less domain normalised to %q, want vault.example.com", got)
	}
	for _, root := range []string{"", "/", "//"} {
		if got := vault.NormalizeIdentity(vault.Identity{FolderPath: root}).FolderPath; got != "/" {
			t.Errorf("folder %q normalised to %q, want /", root, got)
		}
	}
	// Normalising twice changes nothing.
	once := vault.NormalizeIdentity(vault.Identity{APIDomain: "https://App.Infisical.com/api", FolderPath: "x/"})
	if twice := vault.NormalizeIdentity(once); twice != once {
		t.Errorf("not idempotent: %+v then %+v", once, twice)
	}
}

// wrapper marks a provider as wrapped and counts its own Close calls.
type wrapper struct {
	*stubProvider
	closed int
}

func (w *wrapper) Close() error { w.closed++; return nil }

func TestBundleWrap(t *testing.T) {
	r := vault.NewRegistry()
	f := &stubFactory{kind: "fake"}
	if err := r.Register(f); err != nil {
		t.Fatal(err)
	}
	orig, err := r.Build(context.Background(), []vault.ProviderSpec{
		{Name: "", Kind: "fake"},
		{Name: "team", Kind: "fake", Config: vault.ProviderConfig{"name": "team"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var wrappers []*wrapper
	wrapped := orig.Wrap(func(p vault.Provider) vault.Provider {
		w := &wrapper{stubProvider: p.(*stubProvider)}
		wrappers = append(wrappers, w)
		return w
	})

	if len(wrappers) != 2 {
		t.Fatalf("wrap called %d times, want 2 (named and anonymous)", len(wrappers))
	}
	for _, name := range []string{"", "team"} {
		p, err := wrapped.Get(name)
		if err != nil {
			t.Fatalf("wrapped.Get(%q): %v", name, err)
		}
		if _, ok := p.(*wrapper); !ok {
			t.Errorf("wrapped.Get(%q) = %T, want the wrapper", name, p)
		}
		p, _ = orig.Get(name)
		if _, ok := p.(*stubProvider); !ok {
			t.Errorf("orig.Get(%q) = %T, want the original provider unchanged", name, p)
		}
	}
	if got, want := fmt.Sprint(wrapped.Names()), fmt.Sprint(orig.Names()); got != want {
		t.Errorf("Names() = %s, want %s", got, want)
	}
	if !wrapped.HasNamedProviders() {
		t.Error("HasNamedProviders() = false on the wrapped bundle")
	}

	if err := wrapped.CloseAll(); err != nil {
		t.Fatal(err)
	}
	if err := orig.CloseAll(); err != nil {
		t.Fatal(err)
	}
	if err := wrapped.CloseAll(); err != nil {
		t.Fatal(err)
	}
	for _, p := range f.opened {
		if p.closed != 1 {
			t.Errorf("provider %q closed %d times, want exactly 1", p.name, p.closed)
		}
	}
	for _, w := range wrappers {
		if w.closed != 0 {
			t.Errorf("wrapper closed %d times; the bundle closes the providers it wrapped", w.closed)
		}
	}
}

func TestRunStateNil(t *testing.T) {
	s := vault.RunStateFrom(context.Background())
	if s != nil {
		t.Fatalf("RunStateFrom(no state) = %v, want nil", s)
	}
	s.MarkUnreachable("https://a", vault.ReasonTimedOut)
	s.MarkUnauthenticated("https://a")
	if _, ok := s.Check("https://a", false); ok {
		t.Error("a nil state reported a verdict")
	}
}

func TestRunStateVerdicts(t *testing.T) {
	ctx := vault.WithRunState(context.Background())
	s := vault.RunStateFrom(ctx)
	if s == nil {
		t.Fatal("RunStateFrom after WithRunState = nil")
	}
	if vault.RunStateFrom(ctx) != s {
		t.Fatal("RunStateFrom returned a different state on the same ctx")
	}

	unauth := vault.FailureClass{Class: vault.ClassUnauthenticated, Reason: vault.ReasonLoggedOut}
	check := func(domain string, minted bool, want vault.FailureClass, wantOK bool) {
		t.Helper()
		got, ok := s.Check(domain, minted)
		if ok != wantOK || got != want {
			t.Errorf("Check(%q, minted=%v) = %+v, %v; want %+v, %v", domain, minted, got, ok, want, wantOK)
		}
	}

	// Unauthenticated applies only to CLI-session principals.
	s.MarkUnauthenticated("https://a")
	check("https://a", false, unauth, true)
	check("https://a", true, vault.FailureClass{}, false)
	check("https://other", false, vault.FailureClass{}, false)

	// Marked unreachable later: a CLI-session principal keeps the first
	// verdict; a minted one sees the unreachable one.
	s.MarkUnreachable("https://a", vault.ReasonTimedOut)
	check("https://a", false, unauth, true)
	check("https://a", true, vault.FailureClass{Class: vault.ClassUnreachable, Reason: vault.ReasonTimedOut}, true)

	// Unreachable first: it applies to both principals, a later
	// unauthenticated mark doesn't displace it, and neither does a
	// later unreachable reason.
	s.MarkUnreachable("https://b", vault.ReasonUnreachable)
	s.MarkUnauthenticated("https://b")
	s.MarkUnreachable("https://b", vault.ReasonTimedOut)
	unreach := vault.FailureClass{Class: vault.ClassUnreachable, Reason: vault.ReasonUnreachable}
	check("https://b", false, unreach, true)
	check("https://b", true, unreach, true)
}

func TestRunStateConcurrent(t *testing.T) {
	s := vault.RunStateFrom(vault.WithRunState(context.Background()))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(3)
		domain := fmt.Sprintf("https://d%d", i%5)
		go func() { defer wg.Done(); s.MarkUnreachable(domain, vault.ReasonTimedOut) }()
		go func() { defer wg.Done(); s.MarkUnauthenticated(domain) }()
		go func() { defer wg.Done(); s.Check(domain, false) }()
	}
	wg.Wait()
	for i := 0; i < 5; i++ {
		if _, ok := s.Check(fmt.Sprintf("https://d%d", i), true); !ok {
			t.Errorf("domain d%d lost its unreachable mark", i)
		}
	}
}
