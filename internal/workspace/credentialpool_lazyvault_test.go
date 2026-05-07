package workspace

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// stubVaultProvider is a minimal vault.Provider implementation for
// I7's lazy-vault Lookup tests. It records every Resolve call (so
// AC-37 — un-referenced pairs trigger zero Resolve calls — is
// verifiable) and returns canned responses keyed by ref.
type stubVaultProvider struct {
	name     string
	kind     string
	calls    []vault.Ref
	response map[string]stubResponse // map key: ref.Path + "/" + ref.Key
}

type stubResponse struct {
	body string
	err  error
}

func (s *stubVaultProvider) Name() string { return s.name }
func (s *stubVaultProvider) Kind() string { return s.kind }
func (s *stubVaultProvider) Resolve(ctx context.Context, ref vault.Ref) (secret.Value, vault.VersionToken, error) {
	s.calls = append(s.calls, ref)
	key := ref.Path + "/" + ref.Key
	resp, ok := s.response[key]
	if !ok {
		return secret.Value{}, vault.VersionToken{}, vault.ErrKeyNotFound
	}
	if resp.err != nil {
		return secret.Value{}, vault.VersionToken{}, resp.err
	}
	return secret.New([]byte(resp.body), secret.Origin{}), vault.VersionToken{}, nil
}
func (s *stubVaultProvider) Close() error { return nil }

func newStubLoader(t *testing.T, providerName string, responses map[string]stubResponse) (*stubVaultProvider, *vaultCredLoader) {
	t.Helper()
	stub := &stubVaultProvider{
		name:     providerName,
		kind:     "infisical",
		response: responses,
	}
	return stub, &vaultCredLoader{
		Provider:     stub,
		ProviderName: providerName,
		PathPrefix:   CredentialSyncPathPrefix,
	}
}

// validBody returns a TOML body that satisfies parseProviderAuthBody.
func validBody(clientID, clientSecret string) string {
	return "version = \"1\"\n" +
		"client_id = \"" + clientID + "\"\n" +
		"client_secret = \"" + clientSecret + "\"\n"
}

// TestPool_VaultHitOnFileMiss covers PRD AC-1 / AC-2: file misses,
// vault has the entry, Source is vault. The vault key is prefixed
// with "p-" because Infisical rejects secret keys whose first
// character is a digit; the pool prepends "p-" to the project UUID
// before fetching.
func TestPool_VaultHitOnFileMiss(t *testing.T) {
	_, loader := newStubLoader(t, "personal", map[string]stubResponse{
		"/niwa/provider-auth/infisical/p-uuid-X": {body: validBody("cid-vault", "csec-vault")},
	})
	pool := NewCredentialPool(nil, loader)

	entry, rec, err := pool.Lookup(context.Background(), "infisical", "uuid-X")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry == nil {
		t.Fatal("expected vault entry")
	}
	if got, _ := entry.Config["client_id"].(string); got != "cid-vault" {
		t.Errorf("client_id = %q, want cid-vault", got)
	}
	if rec.Source != SourceVault {
		t.Errorf("rec.Source = %q, want %q", rec.Source, SourceVault)
	}
	if rec.Provider != "personal" {
		t.Errorf("rec.Provider = %q, want personal", rec.Provider)
	}
	if rec.Fallback != "" {
		t.Errorf("rec.Fallback = %q, want empty", rec.Fallback)
	}
}

// TestPool_FileWinsWithVaultFallback covers PRD R4 + AC-39: when
// both layers have an entry, the file wins and the audit records
// rec.Fallback = "vault:<provider-name>" using the AC-39 anonymous
// rendering when applicable.
func TestPool_FileWinsWithVaultFallback(t *testing.T) {
	file := []ProviderAuthEntry{
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "uuid-X",
				"client_id":     "cid-file",
				"client_secret": "csec-file",
			},
		},
	}
	_, loader := newStubLoader(t, "personal", map[string]stubResponse{
		"/niwa/provider-auth/infisical/p-uuid-X": {body: validBody("cid-vault", "csec-vault")},
	})
	pool := NewCredentialPool(file, loader)

	entry, rec, err := pool.Lookup(context.Background(), "infisical", "uuid-X")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, _ := entry.Config["client_id"].(string); got != "cid-file" {
		t.Errorf("client_id = %q, want cid-file (file wins)", got)
	}
	if rec.Source != SourceLocalFile {
		t.Errorf("rec.Source = %q, want %q", rec.Source, SourceLocalFile)
	}
	if rec.Fallback != "vault:personal" {
		t.Errorf("rec.Fallback = %q, want %q", rec.Fallback, "vault:personal")
	}
}

// TestPool_FileWinsWithAnonymousVaultFallback locks AC-39 for
// the anonymous-vault case: the Fallback string renders as
// "vault:(anonymous)", never bare "vault:".
func TestPool_FileWinsWithAnonymousVaultFallback(t *testing.T) {
	file := []ProviderAuthEntry{
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "uuid-Y",
				"client_id":     "cid-file",
				"client_secret": "csec-file",
			},
		},
	}
	_, loader := newStubLoader(t, "", map[string]stubResponse{
		"/niwa/provider-auth/infisical/p-uuid-Y": {body: validBody("cid-vault", "csec-vault")},
	})
	pool := NewCredentialPool(file, loader)

	_, rec, err := pool.Lookup(context.Background(), "infisical", "uuid-Y")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Fallback != "vault:(anonymous)" {
		t.Errorf("rec.Fallback = %q, want vault:(anonymous)", rec.Fallback)
	}
}

// TestPool_VaultKeyNotFoundIsSilent covers PRD R13.3: a missing
// vault key returns the cli-session tentative classification
// without error. The cache memoizes the absence so a repeat
// Lookup doesn't re-query.
func TestPool_VaultKeyNotFoundIsSilent(t *testing.T) {
	stub, loader := newStubLoader(t, "personal", map[string]stubResponse{
		// Empty — every Resolve returns ErrKeyNotFound.
	})
	pool := NewCredentialPool(nil, loader)
	ctx := context.Background()

	_, rec, err := pool.Lookup(ctx, "infisical", "uuid-missing")
	if err != nil {
		t.Fatalf("ErrKeyNotFound should NOT propagate as an error, got: %v", err)
	}
	if rec.Source != SourceCLISession {
		t.Errorf("rec.Source = %q, want %q (silent fallthrough)", rec.Source, SourceCLISession)
	}
	if len(stub.calls) != 1 {
		t.Fatalf("expected 1 Resolve call, got %d", len(stub.calls))
	}

	// Repeat: cache hit, no second Resolve call.
	_, _, _ = pool.Lookup(ctx, "infisical", "uuid-missing")
	if len(stub.calls) != 1 {
		t.Errorf("repeat Lookup should hit cache; got %d Resolve calls", len(stub.calls))
	}
}

// TestPool_VaultUnreachablePropagates covers PRD R13.1/R13.2: a
// network/auth failure surfaces as an error. I8 will classify it
// further; I7's contract is just to wrap-and-return.
func TestPool_VaultUnreachablePropagates(t *testing.T) {
	_, loader := newStubLoader(t, "personal", map[string]stubResponse{
		"/niwa/provider-auth/infisical/p-uuid-Z": {err: vault.ErrProviderUnreachable},
	})
	pool := NewCredentialPool(nil, loader)

	_, _, err := pool.Lookup(context.Background(), "infisical", "uuid-Z")
	if err == nil {
		t.Fatal("expected vault-unreachable error")
	}
	if !errors.Is(err, vault.ErrProviderUnreachable) {
		t.Errorf("error must wrap vault.ErrProviderUnreachable. Got: %v", err)
	}
}

// TestParseProviderAuthBody_HappyPath confirms a well-formed body
// produces a ProviderAuthEntry with the expected fields.
func TestParseProviderAuthBody_HappyPath(t *testing.T) {
	body := []byte("version = \"1\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\napi_url = \"https://example.com\"\n")
	entry, err := parseProviderAuthBody("infisical", "uuid", body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Kind != "infisical" {
		t.Errorf("Kind = %q, want infisical", entry.Kind)
	}
	if got, _ := entry.Config["project"].(string); got != "uuid" {
		t.Errorf("project = %q, want uuid", got)
	}
	if got, _ := entry.Config["client_id"].(string); got != "cid" {
		t.Errorf("client_id = %q, want cid", got)
	}
	if got, _ := entry.Config["api_url"].(string); got != "https://example.com" {
		t.Errorf("api_url = %q, want https://example.com", got)
	}
}

// TestParseProviderAuthBody_MissingVersionDefaultsToV1 covers PRD
// R8 backward-compat: a body without a version field is treated
// as version "1".
func TestParseProviderAuthBody_MissingVersionDefaultsToV1(t *testing.T) {
	body := []byte("client_id = \"cid\"\nclient_secret = \"csec\"\n")
	entry, err := parseProviderAuthBody("infisical", "uuid", body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil entry")
	}
}

// TestParseProviderAuthBody_UnsupportedVersion covers PRD AC-23.
func TestParseProviderAuthBody_UnsupportedVersion(t *testing.T) {
	body := []byte("version = \"2\"\nclient_id = \"cid\"\nclient_secret = \"csec\"\n")
	_, err := parseProviderAuthBody("infisical", "uuid", body)
	if err == nil {
		t.Fatal("expected unsupported-version error")
	}
	if !strings.Contains(err.Error(), "unsupported schema version") {
		t.Errorf("error should mention 'unsupported schema version'. Got: %v", err)
	}
	if !strings.Contains(err.Error(), `"2"`) {
		t.Errorf("error should name the version. Got: %v", err)
	}
}

// TestParseProviderAuthBody_MissingClientID covers PRD AC-20.
func TestParseProviderAuthBody_MissingClientID(t *testing.T) {
	body := []byte("version = \"1\"\nclient_secret = \"csec\"\n")
	_, err := parseProviderAuthBody("infisical", "uuid", body)
	if err == nil {
		t.Fatal("expected missing-field error")
	}
	if !strings.Contains(err.Error(), `"client_id"`) {
		t.Errorf("error should name client_id. Got: %v", err)
	}
}

// TestParseProviderAuthBody_MissingClientSecret covers PRD AC-21.
func TestParseProviderAuthBody_MissingClientSecret(t *testing.T) {
	body := []byte("version = \"1\"\nclient_id = \"cid\"\n")
	_, err := parseProviderAuthBody("infisical", "uuid", body)
	if err == nil {
		t.Fatal("expected missing-field error")
	}
	if !strings.Contains(err.Error(), `"client_secret"`) {
		t.Errorf("error should name client_secret. Got: %v", err)
	}
}

// TestParseProviderAuthBody_MalformedTOML covers PRD AC-19. The
// error must NOT contain body bytes (PRD AC-36).
func TestParseProviderAuthBody_MalformedTOML(t *testing.T) {
	body := []byte("not = valid toml [\nTESTCANARY = \"sentinel\"\n")
	_, err := parseProviderAuthBody("infisical", "uuid", body)
	if err == nil {
		t.Fatal("expected malformed-body error")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("error should mention 'malformed'. Got: %v", err)
	}
	// AC-36: error chain MUST NOT contain body bytes.
	if strings.Contains(err.Error(), "TESTCANARY") {
		t.Errorf("error must NOT echo body bytes; sentinel TESTCANARY appeared. Got: %v", err)
	}
}

// TestParseProviderAuthBody_OversizedBody covers the body-size
// cap (DESIGN Security § "New surfaces"). Bodies larger than
// maxProviderAuthBodyBytes are rejected before TOML parsing.
func TestParseProviderAuthBody_OversizedBody(t *testing.T) {
	// 9 KiB of zero bytes — well over the 8 KiB cap.
	body := make([]byte, maxProviderAuthBodyBytes+1024)
	_, err := parseProviderAuthBody("infisical", "uuid", body)
	if err == nil {
		t.Fatal("expected oversized-body error")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error should mention size cap. Got: %v", err)
	}
}

// TestPool_AC36_BodyBytesNeverInError is the focused canary test
// for PRD AC-36: every body-validation error (parse, missing
// field, unsupported version, oversized) must scrub the body
// bytes — only the path and field name should appear.
func TestPool_AC36_BodyBytesNeverInError(t *testing.T) {
	const sentinel = "TESTCANARY42"
	cases := []struct {
		name string
		body string
	}{
		{
			name: "malformed-toml",
			body: "not = valid toml [\nclient_secret = \"" + sentinel + "\"\n",
		},
		{
			name: "missing-client-id",
			body: "version = \"1\"\nclient_secret = \"" + sentinel + "\"\n",
		},
		{
			name: "missing-client-secret",
			body: "version = \"1\"\nclient_id = \"" + sentinel + "\"\n",
		},
		{
			name: "unsupported-version",
			body: "version = \"99\"\nclient_id = \"id\"\nclient_secret = \"" + sentinel + "\"\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseProviderAuthBody("infisical", "uuid", []byte(tc.body))
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), sentinel) {
				t.Errorf("error must NOT echo body bytes. Sentinel %q appeared in: %v", sentinel, err)
			}
		})
	}
}

// TestPool_AC37_NoResolveForUnreferencedPair covers PRD AC-37: a
// (kind, project) that no Lookup ever asks for must produce zero
// Resolve calls.
func TestPool_AC37_NoResolveForUnreferencedPair(t *testing.T) {
	stub, loader := newStubLoader(t, "personal", map[string]stubResponse{
		"/niwa/provider-auth/infisical/p-uuid-A": {body: validBody("cid-A", "csec-A")},
		"/niwa/provider-auth/infisical/p-uuid-B": {body: validBody("cid-B", "csec-B")},
	})
	pool := NewCredentialPool(nil, loader)
	ctx := context.Background()

	// Reference uuid-A only.
	_, _, _ = pool.Lookup(ctx, "infisical", "uuid-A")

	// uuid-B was never referenced — no Resolve call should have
	// happened for it.
	for _, ref := range stub.calls {
		if ref.Path == "/niwa/provider-auth/infisical" && ref.Key == "p-uuid-B" {
			t.Errorf("AC-37 violated: uuid-B was Resolved without being referenced")
		}
	}
	// Sanity: uuid-A WAS resolved (key prefixed with "p-" per
	// credentialSyncProjectKeyPrefix).
	foundA := false
	for _, ref := range stub.calls {
		if ref.Path == "/niwa/provider-auth/infisical" && ref.Key == "p-uuid-A" {
			foundA = true
			break
		}
	}
	if !foundA {
		t.Error("expected a Resolve call for the referenced pair uuid-A")
	}
}

// TestPool_R9_SelfLookupGuard locks the dynamic R9 enforcement
// in lookupVault. injectProviderTokens iterates the personal
// overlay's vault registry, which contains the credential-sync
// spec ITSELF; for that spec it asks the pool to look up
// credentials. Without the loader's SelfKind/SelfProject guard,
// the pool would Resolve the credential-sync provider for its
// own credentials — the chicken-and-egg cycle R9 forbids.
//
// This test wires SelfKind/SelfProject to a known pair, asks
// for that pair, and confirms (a) the guard fires (no Resolve
// call), (b) the audit record is SourceCLISession (apply
// continues; the credential-sync provider was opened via CLI
// session in I6, so falling through to that path is correct).
func TestPool_R9_SelfLookupGuard(t *testing.T) {
	stub, loader := newStubLoader(t, "personal", map[string]stubResponse{
		"/niwa/provider-auth/infisical/p-uuid-self": {body: validBody("cid-self", "csec-self")},
	})
	loader.SelfKind = "infisical"
	loader.SelfProject = "uuid-self"
	pool := NewCredentialPool(nil, loader)

	_, rec, err := pool.Lookup(context.Background(), "infisical", "uuid-self")
	if err != nil {
		t.Fatalf("self-lookup must NOT error, got: %v", err)
	}
	if rec.Source != SourceCLISession {
		t.Errorf("self-lookup rec.Source = %q, want %q", rec.Source, SourceCLISession)
	}
	if len(stub.calls) != 0 {
		t.Errorf("self-lookup must NOT issue a Resolve call (R9 cycle); got %d calls", len(stub.calls))
	}

	// Sanity: a different (kind, project) is still resolvable.
	stub.response["/niwa/provider-auth/infisical/p-uuid-other"] = stubResponse{body: validBody("cid-o", "csec-o")}
	_, _, err = pool.Lookup(context.Background(), "infisical", "uuid-other")
	if err != nil {
		t.Fatalf("non-self lookup must succeed, got: %v", err)
	}
	if len(stub.calls) != 1 {
		t.Errorf("non-self lookup should Resolve once; got %d calls", len(stub.calls))
	}
}

// TestPool_AC31_CacheWithinApply confirms PRD R6 + AC-31: within
// one apply (one CredentialPool), repeat Lookups for the same
// pair hit the cache (no re-fetch). A separate CredentialPool
// (next apply) does re-fetch — guaranteed by construction since
// the cache lives on the pool.
func TestPool_AC31_CacheWithinApply(t *testing.T) {
	stub, loader := newStubLoader(t, "personal", map[string]stubResponse{
		"/niwa/provider-auth/infisical/p-uuid-X": {body: validBody("cid", "csec")},
	})
	pool := NewCredentialPool(nil, loader)
	ctx := context.Background()

	_, _, _ = pool.Lookup(ctx, "infisical", "uuid-X")
	_, _, _ = pool.Lookup(ctx, "infisical", "uuid-X")
	_, _, _ = pool.Lookup(ctx, "infisical", "uuid-X")

	if len(stub.calls) != 1 {
		t.Errorf("3 Lookups for the same pair should yield 1 Resolve, got %d", len(stub.calls))
	}

	// "Next apply" — fresh pool with the same loader (loader can be
	// reused if the test wants; in production each apply opens a
	// fresh provider too). Behavior is the same: first Lookup on
	// the fresh pool re-fetches.
	pool2 := NewCredentialPool(nil, loader)
	_, _, _ = pool2.Lookup(ctx, "infisical", "uuid-X")
	if len(stub.calls) != 2 {
		t.Errorf("fresh pool should re-fetch; got %d total Resolve calls", len(stub.calls))
	}
}

// TestPool_PPrefixOnVaultKey locks the "p-" key prefix invariant:
// the lookup uses Path = "/niwa/provider-auth/<kind>" and
// Key = "p-<project>" regardless of the project's leading character.
// Infisical (and likely other backends) reject secret keys whose
// first character is a digit; UUIDs that happen to start with a
// digit (~37.5% of UUIDv4 values) would otherwise fail at the
// backend. The "p-" prefix sidesteps that validation while keeping
// the path shape clean for human inspection.
func TestPool_PPrefixOnVaultKey(t *testing.T) {
	cases := []struct {
		name    string
		project string
	}{
		{"digit-leading", "9abc1234-def5-6789-abcd-ef0123456789"},
		{"letter-leading", "abcdef12-3456-7890-abcd-ef0123456789"},
		{"already-p-prefixed", "p-confusing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expectedKey := "p-" + tc.project
			expectedPathKey := "/niwa/provider-auth/infisical/" + expectedKey
			stub, loader := newStubLoader(t, "personal", map[string]stubResponse{
				expectedPathKey: {body: validBody("cid", "csec")},
			})
			pool := NewCredentialPool(nil, loader)

			entry, _, err := pool.Lookup(context.Background(), "infisical", tc.project)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if entry == nil {
				t.Fatalf("expected entry for project %q (vault key %q)", tc.project, expectedKey)
			}
			if len(stub.calls) != 1 {
				t.Fatalf("expected 1 Resolve call, got %d", len(stub.calls))
			}
			got := stub.calls[0]
			if got.Path != "/niwa/provider-auth/infisical" {
				t.Errorf("Path = %q, want /niwa/provider-auth/infisical", got.Path)
			}
			if got.Key != expectedKey {
				t.Errorf("Key = %q, want %q (the project UUID with the p- prefix)", got.Key, expectedKey)
			}
		})
	}
}
