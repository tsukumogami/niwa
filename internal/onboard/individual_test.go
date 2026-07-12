package onboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// individualFakeServer is a minimal httptest double modeling the four
// REST endpoints RunIndividualSetup drives: read-identity, mint,
// universal-auth login (the R9 login-exchange hop), the environment
// secrets-read (the R9 read hop), and revoke. Local to this file
// rather than test/functional's infisicalFakeServer, which is reserved
// for the Gherkin functional suite (Issue 9); package-level unit tests
// here follow the same inline-httptest idiom detect_test.go and
// management_test.go already use, just consolidated for reuse across
// this file's many scenarios.
type individualFakeServer struct {
	mu sync.Mutex

	requests []string

	clientID      string
	secretCounter int
	lastSecretVal string
	lastSecretID  string

	failLoginExchange bool
	failReadEnv       bool
	failMint          bool

	revoked       []string
	failRevokeFor map[string]bool
}

func newIndividualFakeServer() *individualFakeServer {
	return &individualFakeServer{clientID: "client-abc", failRevokeFor: map[string]bool{}}
}

func (s *individualFakeServer) Start() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(s.handle))
}

func (s *individualFakeServer) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.requests))
	copy(out, s.requests)
	return out
}

func (s *individualFakeServer) CountRequests(substr string) int {
	n := 0
	for _, r := range s.Requests() {
		if strings.Contains(r, substr) {
			n++
		}
	}
	return n
}

func (s *individualFakeServer) Revoked() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.revoked))
	copy(out, s.revoked)
	return out
}

func (s *individualFakeServer) LastSecretValue() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSecretVal
}

func (s *individualFakeServer) handle(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	s.mu.Lock()
	s.requests = append(s.requests, r.Method+" "+path)
	s.mu.Unlock()

	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/revoke"):
		s.handleRevoke(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/client-secrets"):
		s.handleMint(w)
	case r.Method == http.MethodGet && strings.Contains(path, "/universal-auth/identities/"):
		s.handleReadIdentity(w)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/universal-auth/login"):
		s.handleLogin(w)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/v4/secrets"):
		s.handleReadEnv(w)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *individualFakeServer) handleReadIdentity(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"identityUniversalAuth": map[string]string{"clientId": s.clientID},
	})
}

func (s *individualFakeServer) handleMint(w http.ResponseWriter) {
	if s.failMint {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	s.mu.Lock()
	s.secretCounter++
	secretVal := fmt.Sprintf("minted-secret-value-%d", s.secretCounter)
	secretID := fmt.Sprintf("secret-id-%d", s.secretCounter)
	s.lastSecretVal = secretVal
	s.lastSecretID = secretID
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"clientSecret":     secretVal,
		"clientSecretData": map[string]string{"id": secretID},
	})
}

func (s *individualFakeServer) handleLogin(w http.ResponseWriter) {
	if s.failLoginExchange {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "access-token-xyz"})
}

func (s *individualFakeServer) handleReadEnv(w http.ResponseWriter) {
	if s.failReadEnv {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{}`))
}

func (s *individualFakeServer) handleRevoke(w http.ResponseWriter, r *http.Request) {
	// Use the raw (still-escaped) path and unescape only the final
	// segment: RevokeClientSecret percent-escapes secretID via
	// url.PathEscape, so a hostile id carrying "/" is encoded as
	// "%2F" and must not be re-split on a literal "/" here -- that
	// would be this test double re-introducing the very bug the
	// production code's escaping avoids.
	rawPath := strings.TrimSuffix(r.URL.EscapedPath(), "/revoke")
	idx := strings.LastIndex(rawPath, "/")
	rawSecretID := rawPath[idx+1:]
	secretID, err := url.PathUnescape(rawSecretID)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	fail := s.failRevokeFor[secretID]
	if !fail {
		s.revoked = append(s.revoked, secretID)
	}
	s.mu.Unlock()
	if fail {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// fakeSecretsSetRunner is the store-side test double: it records every
// invocation (argv and stdin) and returns a configurable stdout/
// stderr/exit-code/error triple.
type fakeSecretsSetRunner struct {
	mu    sync.Mutex
	calls []secretsSetCall

	exitCode int
	stderr   []byte
	stdout   []byte
	err      error
}

type secretsSetCall struct {
	args  []string
	stdin []byte
}

func (f *fakeSecretsSetRunner) Run(_ context.Context, args []string, stdin []byte) ([]byte, []byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, secretsSetCall{
		args:  append([]string(nil), args...),
		stdin: append([]byte(nil), stdin...),
	})
	return f.stdout, f.stderr, f.exitCode, f.err
}

func (f *fakeSecretsSetRunner) Calls() []secretsSetCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]secretsSetCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func testCtx() context.Context {
	return secret.WithRedactor(context.Background(), secret.NewRedactor())
}

const testWorkspaceProject = "11111111-AAAA-4bbb-8ccc-222222222222"

func baseIndividualParams(srv *httptest.Server, recordDir string) IndividualSetupParams {
	return IndividualSetupParams{
		APIURL:      srv.URL,
		Bearer:      secret.New([]byte("operator-bearer-token"), secret.Origin{}),
		IdentityID:  "ident-123",
		Kind:        "infisical",
		Project:     testWorkspaceProject,
		Environment: "dev",
		SecretPath:  "/",
		SyncSpec: vault.ProviderSpec{
			Kind:   "infisical",
			Config: vault.ProviderConfig{"project": "personal-vault-project", "env": "dev"},
		},
		Topology:  TopologySameLogin,
		RecordDir: recordDir,
	}
}

// storedBody is the test-local unmarshal target for the stored
// credential's TOML document, mirroring workspace's unexported
// providerAuthBody shape (internal/workspace/credentialpool.go) so
// this test can assert the produced body actually parses the way
// niwa apply's read side expects.
type storedBody struct {
	Version      string `toml:"version"`
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
	APIURL       string `toml:"api_url"`
}

func TestRunIndividualSetup_HappyPath(t *testing.T) {
	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	runner := &fakeSecretsSetRunner{exitCode: 0}
	p := baseIndividualParams(srv, t.TempDir())

	result, err := runIndividualSetup(testCtx(), p, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.StoredPath != "/niwa/provider-auth/infisical" {
		t.Errorf("StoredPath = %q, want /niwa/provider-auth/infisical", result.StoredPath)
	}
	if result.StoredKey != "p-"+testWorkspaceProject {
		t.Errorf("StoredKey = %q, want p-%s", result.StoredKey, testWorkspaceProject)
	}

	// AC-13: GET-identity and POST-client-secrets recorded, no
	// create-identity request (nothing else is modeled at all, so any
	// unexpected call would have 404'd and surfaced as an error).
	if n := fake.CountRequests("GET /v1/auth/universal-auth/identities/ident-123"); n != 1 {
		t.Errorf("read-identity requests = %d, want 1", n)
	}
	if n := fake.CountRequests("POST /v1/auth/universal-auth/identities/ident-123/client-secrets"); n != 1 {
		t.Errorf("mint requests = %d, want 1", n)
	}

	// AC-14 (positive half): verification ran before the store.
	if n := fake.CountRequests("/universal-auth/login"); n != 1 {
		t.Errorf("login-exchange requests = %d, want 1", n)
	}
	if n := fake.CountRequests("/v4/secrets"); n != 1 {
		t.Errorf("target-environment read requests = %d, want 1", n)
	}

	calls := runner.Calls()
	if len(calls) != 1 {
		t.Fatalf("secrets set calls = %d, want 1", len(calls))
	}
	call := calls[0]

	// AC-15/16/17: exact path/key shape, verbatim mixed-case uuid, no
	// human-typed fields -- all derived by construction.
	wantKeyArg := "p-" + testWorkspaceProject + "=@/dev/stdin"
	if !containsArg(call.args, wantKeyArg) {
		t.Errorf("args %v missing key arg %q", call.args, wantKeyArg)
	}
	if !containsArgPair(call.args, "--path", "/niwa/provider-auth/infisical") {
		t.Errorf("args %v missing --path /niwa/provider-auth/infisical", call.args)
	}
	if !containsArgPair(call.args, "--projectId", "personal-vault-project") {
		t.Errorf("args %v missing --projectId personal-vault-project", call.args)
	}
	if !containsArgPair(call.args, "--env", "dev") {
		t.Errorf("args %v missing --env dev", call.args)
	}

	// AC-28: no secret value on argv.
	for _, a := range call.args {
		if strings.Contains(a, fake.LastSecretValue()) {
			t.Errorf("argv leaked the client secret: %q", a)
		}
	}

	var body storedBody
	if _, err := toml.Decode(string(call.stdin), &body); err != nil {
		t.Fatalf("stored body did not parse as TOML: %v\nbody:\n%s", err, call.stdin)
	}
	if body.Version != "1" {
		t.Errorf("version = %q, want 1", body.Version)
	}
	if body.ClientID != "client-abc" {
		t.Errorf("client_id = %q, want client-abc", body.ClientID)
	}
	if body.ClientSecret != fake.LastSecretValue() {
		t.Errorf("client_secret = %q, want %q", body.ClientSecret, fake.LastSecretValue())
	}
	if body.APIURL != "" {
		t.Errorf("api_url = %q, want empty (default not overridden)", body.APIURL)
	}
}

func TestRunIndividualSetup_CredentialAPIURLEmbeddedWhenNonDefault(t *testing.T) {
	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	runner := &fakeSecretsSetRunner{exitCode: 0}
	p := baseIndividualParams(srv, t.TempDir())
	p.CredentialAPIURL = "https://selfhosted.example.com/api"

	if _, err := runIndividualSetup(testCtx(), p, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := runner.Calls()
	var body storedBody
	if _, err := toml.Decode(string(calls[0].stdin), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.APIURL != "https://selfhosted.example.com/api" {
		t.Errorf("api_url = %q, want the configured non-default value", body.APIURL)
	}
}

func TestRunIndividualSetup_HostileCharactersInStoredBodyStayWellFormed(t *testing.T) {
	fake := newIndividualFakeServer()
	fake.clientID = `client"with` + "\n" + `newline]bracket`
	srv := fake.Start()
	defer srv.Close()

	runner := &fakeSecretsSetRunner{exitCode: 0}
	p := baseIndividualParams(srv, t.TempDir())
	p.CredentialAPIURL = `https://example.com/"api]` + "\n"

	if _, err := runIndividualSetup(testCtx(), p, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	calls := runner.Calls()
	var body storedBody
	if _, err := toml.Decode(string(calls[0].stdin), &body); err != nil {
		t.Fatalf("hostile-character body did not parse as well-formed TOML: %v\nbody:\n%s", err, calls[0].stdin)
	}
	if body.ClientID != fake.clientID {
		t.Errorf("client_id round-trip = %q, want %q", body.ClientID, fake.clientID)
	}
	if body.APIURL != p.CredentialAPIURL {
		t.Errorf("api_url round-trip = %q, want %q", body.APIURL, p.CredentialAPIURL)
	}
}

func TestRunIndividualSetup_LoginExchangeFailureDoesNotStore(t *testing.T) {
	fake := newIndividualFakeServer()
	fake.failLoginExchange = true
	srv := fake.Start()
	defer srv.Close()

	runner := &fakeSecretsSetRunner{}
	p := baseIndividualParams(srv, t.TempDir())

	_, err := runIndividualSetup(testCtx(), p, runner)
	if err == nil {
		t.Fatal("expected an error on login-exchange failure")
	}
	var exitErr *ExitCodeError
	if !asExitCodeError(err, &exitErr) {
		t.Fatalf("error is not an *ExitCodeError: %v", err)
	}
	if exitErr.Code != ExitAuthFailure {
		t.Errorf("code = %d, want %d", exitErr.Code, ExitAuthFailure)
	}
	if calls := runner.Calls(); len(calls) != 0 {
		t.Errorf("secrets set fired %d times, want 0 on login-exchange failure (AC-14)", len(calls))
	}
	if n := len(fake.Revoked()); n != 0 {
		t.Errorf("revoked %d secrets, want 0 -- R9 verify failure is not a revocation trigger", n)
	}
}

func TestRunIndividualSetup_ReadHopFailureDoesNotStore(t *testing.T) {
	fake := newIndividualFakeServer()
	fake.failReadEnv = true
	srv := fake.Start()
	defer srv.Close()

	runner := &fakeSecretsSetRunner{}
	p := baseIndividualParams(srv, t.TempDir())

	_, err := runIndividualSetup(testCtx(), p, runner)
	if err == nil {
		t.Fatal("expected an error on read-hop failure")
	}
	if calls := runner.Calls(); len(calls) != 0 {
		t.Errorf("secrets set fired %d times, want 0 on read-hop failure", len(calls))
	}
}

func TestRunIndividualSetup_SplitLoginPausesExactlyOnce(t *testing.T) {
	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	runner := &fakeSecretsSetRunner{}
	p := baseIndividualParams(srv, t.TempDir())
	p.Topology = TopologySplitLogin
	pauseCount := 0
	p.Pause = func(prompt string) error {
		pauseCount++
		if prompt == "" {
			t.Error("Pause called with an empty prompt")
		}
		return nil
	}

	if _, err := runIndividualSetup(testCtx(), p, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pauseCount != 1 {
		t.Errorf("Pause called %d times, want exactly 1 (AC-6)", pauseCount)
	}
}

func TestRunIndividualSetup_SameLoginNeverPauses(t *testing.T) {
	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	runner := &fakeSecretsSetRunner{}
	p := baseIndividualParams(srv, t.TempDir())
	p.Topology = TopologySameLogin
	p.Pause = func(string) error {
		t.Fatal("Pause must never be called in same-login topology (AC-7)")
		return nil
	}

	if _, err := runIndividualSetup(testCtx(), p, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunIndividualSetup_SelfReferentialGuardRefusesAndRevokes(t *testing.T) {
	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	runner := &fakeSecretsSetRunner{}
	p := baseIndividualParams(srv, t.TempDir())
	// Make the sync spec's own project match the target project --
	// the self-referential condition (AC-22).
	p.SyncSpec.Config = vault.ProviderConfig{"project": p.Project, "env": "dev"}

	_, err := runIndividualSetup(testCtx(), p, runner)
	if err == nil {
		t.Fatal("expected a self-referential refusal error")
	}
	if !strings.Contains(err.Error(), "refusing to write") {
		t.Errorf("error = %q, want it to name the self-referential refusal", err.Error())
	}
	if calls := runner.Calls(); len(calls) != 0 {
		t.Errorf("secrets set fired %d times, want 0 -- guard runs before any write", len(calls))
	}
	if n := len(fake.Revoked()); n != 1 {
		t.Errorf("revoked %d secrets, want exactly 1 (the just-minted secret, best-effort)", n)
	}
}

func TestRunIndividualSetup_UnrelatedSyncSpecDoesNotTriggerGuard(t *testing.T) {
	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	runner := &fakeSecretsSetRunner{}
	p := baseIndividualParams(srv, t.TempDir())
	// Different project: must not be treated as self-referential.
	p.SyncSpec.Config = vault.ProviderConfig{"project": "some-other-project", "env": "dev"}

	if _, err := runIndividualSetup(testCtx(), p, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls := runner.Calls(); len(calls) != 1 {
		t.Errorf("secrets set fired %d times, want 1", len(calls))
	}
}

func TestRunIndividualSetup_StoreFailureRevokesJustMinted(t *testing.T) {
	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	runner := &fakeSecretsSetRunner{exitCode: 1, stderr: []byte("error: write failed")}
	p := baseIndividualParams(srv, t.TempDir())

	_, err := runIndividualSetup(testCtx(), p, runner)
	if err == nil {
		t.Fatal("expected a storage-write error")
	}
	var exitErr *ExitCodeError
	if !asExitCodeError(err, &exitErr) {
		t.Fatalf("error is not an *ExitCodeError: %v", err)
	}
	if exitErr.Code != ExitStorageWrite {
		t.Errorf("code = %d, want %d", exitErr.Code, ExitStorageWrite)
	}
	if n := len(fake.Revoked()); n != 1 {
		t.Errorf("revoked %d secrets, want exactly 1 (AC-34: revoke the just-minted secret before exiting)", n)
	}

	// R20: no record should be written on a failed run.
	rec, found, rerr := readMintRecord(p.RecordDir, p.Kind, p.Project)
	if rerr != nil {
		t.Fatalf("readMintRecord: %v", rerr)
	}
	if found {
		t.Errorf("a mint record was persisted despite the store failure: %+v", rec)
	}
}

func TestRunIndividualSetup_SupersessionRevokesPriorSecret(t *testing.T) {
	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	dir := t.TempDir()
	if err := writeMintRecord(dir, "infisical", testWorkspaceProject, mintRecord{SecretID: "old-secret-id"}); err != nil {
		t.Fatalf("seeding prior record: %v", err)
	}

	runner := &fakeSecretsSetRunner{}
	p := baseIndividualParams(srv, dir)

	result, err := runIndividualSetup(testCtx(), p, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	revoked := fake.Revoked()
	if len(revoked) != 1 || revoked[0] != "old-secret-id" {
		t.Errorf("revoked = %v, want exactly [old-secret-id] (AC-33)", revoked)
	}

	rec, found, rerr := readMintRecord(dir, "infisical", testWorkspaceProject)
	if rerr != nil || !found {
		t.Fatalf("readMintRecord after success: found=%v err=%v", found, rerr)
	}
	if rec.SecretID != result.SecretID {
		t.Errorf("persisted SecretID = %q, want %q (the newly minted id)", rec.SecretID, result.SecretID)
	}
	if rec.SecretID == "old-secret-id" {
		t.Errorf("record was not superseded")
	}
}

func TestRunIndividualSetup_SupersessionRevokeFailureIsWarningOnly(t *testing.T) {
	fake := newIndividualFakeServer()
	fake.failRevokeFor["old-secret-id"] = true
	srv := fake.Start()
	defer srv.Close()

	dir := t.TempDir()
	if err := writeMintRecord(dir, "infisical", testWorkspaceProject, mintRecord{SecretID: "old-secret-id"}); err != nil {
		t.Fatalf("seeding prior record: %v", err)
	}

	runner := &fakeSecretsSetRunner{}
	p := baseIndividualParams(srv, dir)

	result, err := runIndividualSetup(testCtx(), p, runner)
	if err != nil {
		t.Fatalf("a failed best-effort revocation must not change the outcome, got error: %v", err)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "old-secret-id") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings %v do not name the un-revoked secret id", result.Warnings)
	}
}

func TestRunIndividualSetup_NoPriorRecordSkipsRevokeAndWarnsTTL(t *testing.T) {
	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	runner := &fakeSecretsSetRunner{}
	p := baseIndividualParams(srv, t.TempDir()) // fresh RecordDir: no prior record

	result, err := runIndividualSetup(testCtx(), p, runner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := len(fake.Revoked()); n != 0 {
		t.Errorf("revoked %d secrets, want 0 -- AC-35b: no prior id is recoverable", n)
	}
	found := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "TTL") || strings.Contains(w, "live") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings %v do not state the TTL-lapse condition (AC-35b)", result.Warnings)
	}
}

// TestRunIndividualSetup_HostileSecretIDInPriorRecordDoesNotBreakRevoke
// seeds a prior R20 record whose secret id carries URL-structuring
// characters and asserts the revoke call still reaches a well-formed,
// correctly escaped path (RevokeClientSecret already percent-escapes;
// this exercises that escaping through this file's supersession call
// site end to end).
func TestRunIndividualSetup_HostileSecretIDInPriorRecordDoesNotBreakRevoke(t *testing.T) {
	hostileID := "id/with?hostile#chars"
	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	dir := t.TempDir()
	if err := writeMintRecord(dir, "infisical", testWorkspaceProject, mintRecord{SecretID: hostileID}); err != nil {
		t.Fatalf("seeding prior record: %v", err)
	}

	runner := &fakeSecretsSetRunner{}
	p := baseIndividualParams(srv, dir)

	if _, err := runIndividualSetup(testCtx(), p, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	revoked := fake.Revoked()
	if len(revoked) != 1 || revoked[0] != hostileID {
		t.Errorf("revoked = %v, want exactly [%q] (percent-decoded round-trip)", revoked, hostileID)
	}
}

// TestRunIndividualSetup_CanaryNeverLeaks plants a canary client secret
// (via the mint response) and asserts it never appears in the error
// returned from a store failure whose stderr echoes it back -- the
// scrub-before-error discipline (R17/AC-27) must catch it even when
// the CLI itself misbehaves.
func TestRunIndividualSetup_CanaryNeverLeaks(t *testing.T) {
	fake := newIndividualFakeServer()
	srv := fake.Start()
	defer srv.Close()

	runner := &canaryLeakingRunner{server: fake, exitCode: 1}
	p := baseIndividualParams(srv, t.TempDir())

	result, err := runIndividualSetup(testCtx(), p, runner)
	if err == nil {
		t.Fatal("expected a storage-write error")
	}
	canary := fake.LastSecretValue()
	if canary == "" {
		t.Fatal("test setup bug: no secret was minted")
	}
	if strings.Contains(err.Error(), canary) {
		t.Errorf("canary client_secret leaked through the returned error: %v", err)
	}
	for _, w := range result.Warnings {
		if strings.Contains(w, canary) {
			t.Errorf("canary client_secret leaked through a warning: %q", w)
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", result), canary) {
		t.Errorf("canary client_secret leaked through the result struct")
	}
}

// canaryLeakingRunner simulates a misbehaving `infisical` CLI that
// echoes the just-set secret value on stderr when it fails -- the
// worst case R17's scrub-before-error discipline must still contain.
type canaryLeakingRunner struct {
	server   *individualFakeServer
	exitCode int
}

func (c *canaryLeakingRunner) Run(_ context.Context, _ []string, _ []byte) ([]byte, []byte, int, error) {
	leak := []byte("error: write rejected, offending value was " + c.server.LastSecretValue())
	return nil, leak, c.exitCode, nil
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsArgPair(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

// asExitCodeError is a tiny type-assertion wrapper kept local to this
// file to avoid repeating the two-step cast at many call sites. Every
// error this package returns for a terminal outcome is constructed as
// a bare &ExitCodeError{} (never further wrapped), so a plain type
// assertion is sufficient -- no errors.As needed.
func asExitCodeError(err error, target **ExitCodeError) bool {
	e, ok := err.(*ExitCodeError)
	if !ok {
		return false
	}
	*target = e
	return true
}
