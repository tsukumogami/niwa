package storefallback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/fallbacknotice"
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/store"
)

// storeDir returns a fresh store directory under a private
// XDG_STATE_HOME. The directory does not exist yet.
func storeDir(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir, err := store.Dir()
	if err != nil {
		t.Fatalf("store.Dir: %v", err)
	}
	return dir
}

var (
	sessionID = vault.Identity{Kind: "infisical", APIDomain: "https://app.infisical.com", ProjectID: "proj-session", Environment: "dev", FolderPath: "/"}
	mintedID  = vault.Identity{Kind: "infisical", APIDomain: "https://app.infisical.com", ProjectID: "proj-minted", Environment: "prod", FolderPath: "/ci"}
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// result is what stubProvider returns for one key.
type result struct {
	value string
	token vault.VersionToken
	err   error
}

// stubProvider is a vault.Provider and vault.StoreIdentifier with
// canned results per key. It counts Resolve calls.
type stubProvider struct {
	id      vault.Identity
	noID    map[string]bool // keys StoreIdentity reports no identity for
	results map[string]result
	calls   int
	closed  bool
}

func (p *stubProvider) Name() string { return "stub" }
func (p *stubProvider) Kind() string { return "infisical" }
func (p *stubProvider) Close() error { p.closed = true; return nil }

func (p *stubProvider) Resolve(_ context.Context, ref vault.Ref) (secret.Value, vault.VersionToken, error) {
	p.calls++
	r, ok := p.results[ref.Key]
	if !ok {
		return secret.Value{}, vault.VersionToken{}, fmt.Errorf("stub: no result for %q", ref.Key)
	}
	if r.err != nil {
		return secret.Value{}, vault.VersionToken{}, r.err
	}
	return secret.New([]byte(r.value), secret.Origin{Key: ref.Key}), r.token, nil
}

func (p *stubProvider) StoreIdentity(ref vault.Ref) (vault.Identity, bool) {
	if p.noID[ref.Key] {
		return vault.Identity{}, false
	}
	return p.id, true
}

// plainProvider implements vault.Provider only.
type plainProvider struct{}

func (plainProvider) Name() string { return "plain" }
func (plainProvider) Kind() string { return "plain" }
func (plainProvider) Close() error { return nil }
func (plainProvider) Resolve(context.Context, vault.Ref) (secret.Value, vault.VersionToken, error) {
	return secret.Value{}, vault.VersionToken{}, nil
}

func classified(class vault.FailureKind, reason vault.Reason, status int) error {
	return vault.Classify(errors.New("infisical: export exited 1"), vault.FailureClass{Class: class, Reason: reason, HTTPStatus: status})
}

// seed stores entries for id through the store package itself.
func seed(t *testing.T, dir string, id vault.Identity, entries map[string]store.Entry) {
	t.Helper()
	if err := store.Update(dir, id, entries, nil, false); err != nil {
		t.Fatalf("seeding the store: %v", err)
	}
}

func load(t *testing.T, dir string, id vault.Identity) map[string]store.Entry {
	t.Helper()
	entries, err := store.Load(dir, id)
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	return entries
}

func resolve(t *testing.T, p vault.Provider, key string) (secret.Value, vault.VersionToken, error) {
	t.Helper()
	return p.Resolve(context.Background(), vault.Ref{Key: key})
}

func TestWrapLeavesNonStoreIdentifierUnchanged(t *testing.T) {
	s := NewSession(storeDir(t), nil, nil)
	var p vault.Provider = plainProvider{}
	if got := s.Wrap(p); got != p {
		t.Fatalf("Wrap returned %T, want the provider itself", got)
	}
	stub := &stubProvider{}
	if _, same := s.Wrap(stub).(*stubProvider); same {
		t.Fatal("Wrap returned a StoreIdentifier unwrapped")
	}
}

func TestWrapperForwardsNameKindClose(t *testing.T) {
	stub := &stubProvider{}
	w := NewSession(storeDir(t), nil, nil).Wrap(stub)
	if w.Name() != "stub" || w.Kind() != "infisical" {
		t.Fatalf("Name/Kind = %q/%q", w.Name(), w.Kind())
	}
	if err := w.Close(); err != nil || !stub.closed {
		t.Fatalf("Close not forwarded (err %v, closed %v)", err, stub.closed)
	}
}

// A reference the provider can't name an identity for passes straight
// through: nothing buffered, loaded or noted, even on a failure that
// would otherwise fall back.
func TestPerReferenceNoIdentityPassesThrough(t *testing.T) {
	dir := storeDir(t)
	seed(t, dir, sessionID, map[string]store.Entry{"K": {Value: []byte("stored-value")}})
	innerErr := classified(vault.ClassUnauthenticated, vault.ReasonLoggedOut, 0)
	stub := &stubProvider{id: sessionID, noID: map[string]bool{"K": true, "OK": true}, results: map[string]result{
		"K":  {err: innerErr},
		"OK": {value: "fresh-value"},
	}}
	notices := fallbacknotice.New(nil)
	s := NewSession(dir, notices, nil)
	w := s.Wrap(stub)

	if _, _, err := resolve(t, w, "K"); err != innerErr {
		t.Fatalf("err = %v, want the inner error value", err)
	}
	if v, _, err := resolve(t, w, "OK"); err != nil || string(reveal.UnsafeReveal(v)) != "fresh-value" {
		t.Fatalf("OK = %v, %v", v, err)
	}
	if !notices.Empty() {
		t.Fatal("a pass-through reference produced a notice")
	}
	if len(s.loaded) != 0 || len(s.changes) != 0 {
		t.Fatalf("pass-through touched the session: loaded %d, changes %d", len(s.loaded), len(s.changes))
	}
	if stub.calls != 2 {
		t.Fatalf("inner Resolve calls = %d, want 2", stub.calls)
	}
}

// Every path that doesn't serve returns the inner error value itself.
func TestNonServedPathsReturnTheInnerErrorValue(t *testing.T) {
	cases := map[string]error{
		"key not found":                       fmt.Errorf("infisical: key %q: %w", "K", vault.ErrKeyNotFound),
		"unauthenticated 403, nothing stored": classified(vault.ClassUnauthenticated, vault.ReasonLoggedOut, 403),
		"unreachable, nothing stored":         classified(vault.ClassUnreachable, vault.ReasonTimedOut, 0),
		"answered 403":                        classified(vault.ClassAnswered, 0, 403),
		"answered 404":                        classified(vault.ClassAnswered, 0, 404),
		"answered 500":                        classified(vault.ClassAnswered, 0, 500),
		"plain unclassified":                  errors.New("something else"),
		"client not installed":                fmt.Errorf("infisical: %w", vault.ErrClientNotInstalled),
		"unclassified unreachable":            fmt.Errorf("infisical: %w", vault.ErrProviderUnreachable),
	}
	for name, innerErr := range cases {
		t.Run(name, func(t *testing.T) {
			stub := &stubProvider{id: sessionID, results: map[string]result{"K": {err: innerErr}}}
			w := NewSession(storeDir(t), fallbacknotice.New(nil), nil).Wrap(stub)
			if _, _, err := resolve(t, w, "K"); err != innerErr {
				t.Fatalf("err = %#v, want the identical inner error", err)
			}
			if stub.calls != 1 {
				t.Fatalf("inner Resolve calls = %d, want 1", stub.calls)
			}
		})
	}
}

// Every class and reason that serves does, for a CLI-session identity
// and a minted one alike, and the note carries the case's own reason.
func TestServedForEveryServingClassAndReason(t *testing.T) {
	resolvedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		class  vault.FailureKind
		reason vault.Reason
		want   fallbacknotice.Reason
	}{
		{"unauthenticated logged out", vault.ClassUnauthenticated, vault.ReasonLoggedOut, fallbacknotice.ReasonLoggedOut},
		{"unreachable timed out", vault.ClassUnreachable, vault.ReasonTimedOut, fallbacknotice.ReasonTimedOut},
		{"unreachable unreachable", vault.ClassUnreachable, vault.ReasonUnreachable, fallbacknotice.ReasonUnreachable},
	}
	for _, tc := range cases {
		for _, id := range []vault.Identity{sessionID, mintedID} {
			t.Run(tc.name+"/"+id.ProjectID, func(t *testing.T) {
				dir := storeDir(t)
				seed(t, dir, id, map[string]store.Entry{"K": {
					Value: []byte("stored-value-1"), ResolvedAt: resolvedAt,
					VersionToken: "v-stored", Provenance: "infisical:export",
				}})
				stub := &stubProvider{id: id, results: map[string]result{"K": {err: classified(tc.class, tc.reason, 0)}}}
				notices := fallbacknotice.New(nil)
				w := NewSession(dir, notices, nil).Wrap(stub)

				v, token, err := resolve(t, w, "K")
				if err != nil {
					t.Fatalf("err = %v, want the stored value", err)
				}
				if got := string(reveal.UnsafeReveal(v)); got != "stored-value-1" {
					t.Fatalf("value = %q", got)
				}
				if token.Token != "v-stored" || token.Provenance != "infisical:export" {
					t.Fatalf("token = %+v", token)
				}
				if v.Origin().VersionToken != "v-stored" || v.Origin().Key != "K" {
					t.Fatalf("origin = %+v", v.Origin())
				}
				notes := notices.ServedNotes()
				if len(notes) != 1 {
					t.Fatalf("served notes = %+v, want one", notes)
				}
				if notes[0].Identity != noticeIdentity(id) || notes[0].Reason != tc.want || !notes[0].OldestResolvedAt.Equal(resolvedAt) {
					t.Fatalf("note = %+v, want identity %+v reason %v at %v", notes[0], id, tc.want, resolvedAt)
				}
				if len(notices.Misses()) != 0 {
					t.Fatal("a served key was also noted as a miss")
				}
			})
		}
	}
}

func TestNothingStoredNotesAMiss(t *testing.T) {
	dir := storeDir(t)
	seed(t, dir, sessionID, map[string]store.Entry{"A": {Value: []byte("stored-a")}})
	innerErr := classified(vault.ClassUnauthenticated, vault.ReasonLoggedOut, 0)
	stub := &stubProvider{id: sessionID, results: map[string]result{"A": {err: innerErr}, "B": {err: innerErr}}}
	notices := fallbacknotice.New(nil)
	w := NewSession(dir, notices, nil).Wrap(stub)
	if _, _, err := resolve(t, w, "A"); err != nil {
		t.Fatalf("A: %v", err)
	}
	if _, _, err := resolve(t, w, "B"); err != innerErr {
		t.Fatalf("B err = %v", err)
	}
	if got := notices.Misses(); len(got) != 1 || got[0] != noticeIdentity(sessionID) {
		t.Fatalf("misses = %+v", got)
	}
	if len(notices.ServedNotes()) != 1 {
		t.Fatalf("served = %+v", notices.ServedNotes())
	}
}

func TestFlushRecordsSuccessesWithTokens(t *testing.T) {
	dir := storeDir(t)
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	stub := &stubProvider{id: sessionID, results: map[string]result{
		"A": {value: "value-a-1", token: vault.VersionToken{Token: "t-a", Provenance: "p-a"}},
		"B": {value: "value-b-1"},
	}}
	s := NewSession(dir, nil, fixedClock(now))
	w := s.Wrap(stub)
	for _, k := range []string{"A", "B"} {
		if _, _, err := resolve(t, w, k); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("the store was written before Flush (stat err %v)", err)
	}
	s.Flush()
	got := load(t, dir, sessionID)
	if len(got) != 2 {
		t.Fatalf("stored %d keys, want 2", len(got))
	}
	if string(got["A"].Value) != "value-a-1" || got["A"].VersionToken != "t-a" || got["A"].Provenance != "p-a" || !got["A"].ResolvedAt.Equal(now) {
		t.Fatalf("A = %+v", got["A"])
	}
	if got["B"].VersionToken != "" || got["B"].Provenance != "" {
		t.Fatalf("B token = %q/%q, want empty", got["B"].VersionToken, got["B"].Provenance)
	}
}

// A served value is not written back, and a run that only served makes
// no write at all.
func TestServedValueIsNotWrittenBack(t *testing.T) {
	dir := storeDir(t)
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seed(t, dir, sessionID, map[string]store.Entry{"A": {Value: []byte("stored-a"), ResolvedAt: old, VersionToken: "t"}})
	matches, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(matches) != 1 {
		t.Fatalf("data files = %v", matches)
	}
	before, _ := os.Stat(matches[0])

	stub := &stubProvider{id: sessionID, results: map[string]result{"A": {err: classified(vault.ClassUnreachable, vault.ReasonUnreachable, 0)}}}
	s := NewSession(dir, nil, fixedClock(time.Now()))
	if _, _, err := resolve(t, s.Wrap(stub), "A"); err != nil {
		t.Fatal(err)
	}
	s.Flush()
	after, _ := os.Stat(matches[0])
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("Flush rewrote the data file after a served-only run")
	}
	if got := load(t, dir, sessionID)["A"]; !got.ResolvedAt.Equal(old) {
		t.Fatalf("resolved_at = %v, want %v", got.ResolvedAt, old)
	}
}

func TestEviction(t *testing.T) {
	stored := map[string]store.Entry{"A": {Value: []byte("stored-a")}, "B": {Value: []byte("stored-b")}}
	cases := []struct {
		name string
		err  error
		want []string // keys left after Flush
	}{
		{"key not found evicts the key", fmt.Errorf("x: %w", vault.ErrKeyNotFound), []string{"B"}},
		{"answered 403 evicts the identity", classified(vault.ClassAnswered, 0, 403), nil},
		{"answered 404 evicts the identity", classified(vault.ClassAnswered, 0, 404), nil},
		{"answered 500 evicts nothing", classified(vault.ClassAnswered, 0, 500), []string{"A", "B"}},
		{"unauthenticated 403 evicts nothing", classified(vault.ClassUnauthenticated, vault.ReasonLoggedOut, 403), []string{"A", "B"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := storeDir(t)
			seed(t, dir, sessionID, stored)
			stub := &stubProvider{id: sessionID, results: map[string]result{"A": {err: tc.err}}}
			s := NewSession(dir, nil, nil)
			_, _, _ = resolve(t, s.Wrap(stub), "A")
			s.Flush()
			got := load(t, dir, sessionID)
			if len(got) != len(tc.want) {
				t.Fatalf("left %d keys, want %v", len(got), tc.want)
			}
			for _, k := range tc.want {
				if string(got[k].Value) != string(stored[k].Value) {
					t.Fatalf("key %s = %+v, want unchanged", k, got[k])
				}
			}
		})
	}
}

// The run's last word on a key is what Flush writes.
func TestBufferedChangesAreLastEventWins(t *testing.T) {
	dir := storeDir(t)
	seed(t, dir, sessionID, map[string]store.Entry{"OLD": {Value: []byte("stored-old")}})
	s := NewSession(dir, nil, nil)
	s.put(sessionID, "A", store.Entry{Value: []byte("a-1")})
	s.evictKey(sessionID, "A")
	s.evictKey(sessionID, "B")
	s.put(sessionID, "B", store.Entry{Value: []byte("b-1")})
	s.Flush()
	got := load(t, dir, sessionID)
	if _, ok := got["A"]; ok || string(got["B"].Value) != "b-1" || string(got["OLD"].Value) != "stored-old" {
		t.Fatalf("stored = %v", keys(got))
	}

	s.put(sessionID, "C", store.Entry{Value: []byte("c-1")})
	s.evictAll(sessionID)
	s.put(sessionID, "D", store.Entry{Value: []byte("d-1")})
	s.Flush()
	got = load(t, dir, sessionID)
	if len(got) != 1 || string(got["D"].Value) != "d-1" {
		t.Fatalf("after evictAll stored = %v", keys(got))
	}
}

func keys(m map[string]store.Entry) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The store is read once per identity per run: a key added to the file
// after the first load is not seen by the same session.
func TestLoadIsCachedPerIdentity(t *testing.T) {
	dir := storeDir(t)
	seed(t, dir, sessionID, map[string]store.Entry{"A": {Value: []byte("stored-a")}})
	innerErr := classified(vault.ClassUnauthenticated, vault.ReasonLoggedOut, 0)
	stub := &stubProvider{id: sessionID, results: map[string]result{"A": {err: innerErr}, "B": {err: innerErr}}}
	w := NewSession(dir, nil, nil).Wrap(stub)
	if _, _, err := resolve(t, w, "A"); err != nil {
		t.Fatal(err)
	}
	seed(t, dir, sessionID, map[string]store.Entry{"B": {Value: []byte("stored-b")}})
	if _, _, err := resolve(t, w, "B"); err != innerErr {
		t.Fatalf("B was read from a second load (err %v)", err)
	}
}

// An eviction buffered earlier in the run stops the evicted keys being
// served later in the same run.
func TestBufferedEvictionIsNotServed(t *testing.T) {
	dir := storeDir(t)
	seed(t, dir, sessionID, map[string]store.Entry{"A": {Value: []byte("stored-a")}, "B": {Value: []byte("stored-b")}, "C": {Value: []byte("stored-c")}})
	lapsed := classified(vault.ClassUnauthenticated, vault.ReasonLoggedOut, 0)
	stub := &stubProvider{id: sessionID, results: map[string]result{
		"A": {err: fmt.Errorf("x: %w", vault.ErrKeyNotFound)},
		"B": {err: lapsed},
		"C": {err: lapsed},
	}}
	s := NewSession(dir, nil, nil)
	w := s.Wrap(stub)
	_, _, _ = resolve(t, w, "A")
	stub.results["A"] = result{err: lapsed}
	if _, _, err := resolve(t, w, "A"); err != lapsed {
		t.Fatalf("A was served after its eviction (err %v)", err)
	}
	if _, _, err := resolve(t, w, "B"); err != nil {
		t.Fatalf("B: %v", err)
	}

	s.evictAll(sessionID)
	if _, _, err := resolve(t, w, "C"); err != lapsed {
		t.Fatalf("C was served after the identity was evicted (err %v)", err)
	}
}

func TestFlushNothingToWriteMakesNoStore(t *testing.T) {
	dir := storeDir(t)
	notices := fallbacknotice.New(nil)
	s := NewSession(dir, notices, nil)
	stub := &stubProvider{id: sessionID, results: map[string]result{"A": {err: classified(vault.ClassUnauthenticated, vault.ReasonLoggedOut, 0)}}}
	_, _, _ = resolve(t, s.Wrap(stub), "A")
	s.Flush()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("store directory created by a run with nothing to write (err %v)", err)
	}
	if got := notices.Misses(); len(got) != 1 {
		t.Fatalf("misses = %v", got)
	}
}

func TestFlushInWorkTreeNotesOnceAndWritesNothing(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "state", "niwa", "secret-cache")
	notices := fallbacknotice.New(nil)
	s := NewSession(dir, notices, nil)
	s.put(sessionID, "A", store.Entry{Value: []byte("a-1")})
	s.put(mintedID, "B", store.Entry{Value: []byte("b-1")})
	s.Flush()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("store written inside a work tree (err %v)", err)
	}
	got, ok := notices.WorkTreeDir()
	if !ok || got != dir {
		t.Fatalf("work-tree notice = %q, %v", got, ok)
	}
	if notices.LockTimeouts() != nil && len(notices.LockTimeouts()) != 0 {
		t.Fatal("unexpected lock-timeout notice")
	}
}

func TestUnwritableStoreNotesOnceAndDisablesTheStore(t *testing.T) {
	// A store directory that is a symlink fails the trust checks: Load
	// and Update both refuse it.
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "secret-cache")
	if err := os.Symlink(real, dir); err != nil {
		t.Fatal(err)
	}
	notices := fallbacknotice.New(nil)
	s := NewSession(dir, notices, nil)
	innerErr := classified(vault.ClassUnauthenticated, vault.ReasonLoggedOut, 0)
	stub := &stubProvider{id: sessionID, results: map[string]result{"A": {err: innerErr}, "B": {value: "fresh-b"}}}
	w := s.Wrap(stub)
	if _, _, err := resolve(t, w, "A"); err != innerErr {
		t.Fatalf("A err = %v", err)
	}
	if _, _, err := resolve(t, w, "B"); err != nil {
		t.Fatal(err)
	}
	s.Flush()
	got, ok := notices.UnwritableDir()
	if !ok || got != dir {
		t.Fatalf("unwritable notice = %q, %v", got, ok)
	}
	entries, _ := os.ReadDir(real)
	if len(entries) != 0 {
		t.Fatalf("a disabled store was written: %v", entries)
	}
}

func TestUpdateUnwritableIsNoted(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "secret-cache")
	if err := os.Symlink(real, dir); err != nil {
		t.Fatal(err)
	}
	notices := fallbacknotice.New(nil)
	s := NewSession(dir, notices, nil)
	s.put(sessionID, "A", store.Entry{Value: []byte("a-1")})
	s.put(mintedID, "B", store.Entry{Value: []byte("b-1")})
	s.Flush()
	if got, ok := notices.UnwritableDir(); !ok || got != dir {
		t.Fatalf("unwritable notice = %q, %v", got, ok)
	}
}

// A store directory that couldn't be located arrives as "": nothing is
// served or written, and the store is noted unwritable with no
// directory.
func TestNoStoreDirectory(t *testing.T) {
	notices := fallbacknotice.New(nil)
	s := NewSession("", notices, nil)
	innerErr := classified(vault.ClassUnreachable, vault.ReasonUnreachable, 0)
	stub := &stubProvider{id: sessionID, results: map[string]result{"A": {err: innerErr}}}
	if _, _, err := resolve(t, s.Wrap(stub), "A"); err != innerErr {
		t.Fatalf("err = %v", err)
	}
	s.put(sessionID, "B", store.Entry{Value: []byte("b-1")})
	s.Flush()
	if got, ok := notices.UnwritableDir(); !ok || got != "" {
		t.Fatalf("unwritable notice = %q, %v", got, ok)
	}

	// A run that never needs the store notes nothing.
	quiet := fallbacknotice.New(nil)
	NewSession("", quiet, nil).Flush()
	if !quiet.Empty() {
		t.Fatal("an unused session without a store directory produced a notice")
	}
}

// fallbacknotice.Identity mirrors vault.Identity by hand, since the
// collector imports nothing from vault. A field added to one and not the
// other would silently merge distinct identities into one notice.
func TestNoticeIdentityCopiesEveryField(t *testing.T) {
	vt := reflect.TypeOf(vault.Identity{})
	nt := reflect.TypeOf(fallbacknotice.Identity{})
	if vt.NumField() != nt.NumField() {
		t.Fatalf("vault.Identity has %d fields, fallbacknotice.Identity %d", vt.NumField(), nt.NumField())
	}
	id := vault.Identity{}
	iv := reflect.ValueOf(&id).Elem()
	for i := 0; i < vt.NumField(); i++ {
		if _, ok := nt.FieldByName(vt.Field(i).Name); !ok {
			t.Fatalf("fallbacknotice.Identity lacks %s", vt.Field(i).Name)
		}
		iv.Field(i).SetString("value-of-" + vt.Field(i).Name)
	}
	got := reflect.ValueOf(noticeIdentity(id))
	for i := 0; i < vt.NumField(); i++ {
		name := vt.Field(i).Name
		if got.FieldByName(name).String() != "value-of-"+name {
			t.Errorf("noticeIdentity dropped %s", name)
		}
	}
}

// lessIdentity must compare every vault.Identity field. One it skipped
// would leave two identities that differ only there unordered, and the
// order they come out in would depend on map iteration.
func TestLessIdentityOrdersByEveryField(t *testing.T) {
	vt := reflect.TypeOf(vault.Identity{})
	for i := 0; i < vt.NumField(); i++ {
		name := vt.Field(i).Name
		t.Run(name, func(t *testing.T) {
			var lo, hi vault.Identity
			lv, hv := reflect.ValueOf(&lo).Elem(), reflect.ValueOf(&hi).Elem()
			for j := 0; j < vt.NumField(); j++ {
				lv.Field(j).SetString("same")
				hv.Field(j).SetString("same")
			}
			lv.Field(i).SetString("a")
			hv.Field(i).SetString("b")
			if !lessIdentity(lo, hi) || lessIdentity(hi, lo) {
				t.Errorf("lessIdentity ignores %s", name)
			}
		})
	}
}

// R20: the reasons this package maps from vault.ReasonLoggedOut and then
// vault.ReasonTimedOut, recorded for one identity in that order, render as
// "is logged out or expired": the first reason wins.
func TestMappedFirstReasonWinsInTheRendering(t *testing.T) {
	c := fallbacknotice.New(nil)
	id := fallbacknotice.Identity{Kind: "infisical", APIDomain: "https://app.infisical.com", ProjectID: "p", Environment: "dev", FolderPath: "/"}
	now := time.Now()
	c.Served(id, noticeReason(vault.FailureClass{Class: vault.ClassUnauthenticated, Reason: vault.ReasonLoggedOut}), now)
	c.Served(id, noticeReason(vault.FailureClass{Class: vault.ClassUnreachable, Reason: vault.ReasonTimedOut}), now)
	if got := c.RenderText(); !strings.Contains(got, "the provider is logged out or expired;") {
		t.Errorf("rendered reason is not the first one:\n%s", got)
	}
}

func TestPolicy(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want decision
	}{
		{"success", nil, decision{action: actionRecord}},
		{"key not found", vault.ErrKeyNotFound, decision{action: actionEvictKey}},
		{"answered 403", classified(vault.ClassAnswered, 0, 403), decision{action: actionEvictAll}},
		{"answered 404", classified(vault.ClassAnswered, 0, 404), decision{action: actionEvictAll}},
		{"answered 401", classified(vault.ClassAnswered, 0, 401), decision{action: actionPass}},
		{"unauthenticated", classified(vault.ClassUnauthenticated, vault.ReasonLoggedOut, 403), decision{actionFallBack, fallbacknotice.ReasonLoggedOut}},
		{"unauthenticated, no reason", classified(vault.ClassUnauthenticated, 0, 0), decision{actionFallBack, fallbacknotice.ReasonLoggedOut}},
		{"timed out", classified(vault.ClassUnreachable, vault.ReasonTimedOut, 0), decision{actionFallBack, fallbacknotice.ReasonTimedOut}},
		{"unreachable", classified(vault.ClassUnreachable, vault.ReasonUnreachable, 0), decision{actionFallBack, fallbacknotice.ReasonUnreachable}},
		{"unreachable, no reason", classified(vault.ClassUnreachable, 0, 0), decision{actionFallBack, fallbacknotice.ReasonUnreachable}},
		{"client not installed", vault.ErrClientNotInstalled, decision{action: actionPass}},
		{"unclassified unreachable", vault.ErrProviderUnreachable, decision{action: actionPass}},
		{"plain", errors.New("x"), decision{action: actionPass}},
	}
	for _, tc := range cases {
		if got := policy(tc.err); got != tc.want {
			t.Errorf("%s: policy = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
