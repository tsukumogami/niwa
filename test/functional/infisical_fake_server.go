package functional

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// infisicalFakeServer is the httptest counterpart of the Infisical
// management REST surface internal/vault/infisical consumes:
// read-identity, mint-client-secret, universal-auth login (the R9
// verification hop, same endpoint auth.go's authenticateHTTP already
// targets), revoke, the environment secrets-read the R9 read hop
// targets, and the project-identity-membership read the team-phase
// environment-grant landing check (membership.go) targets.
// Structurally modeled on tarballFakeServer.
//
// Wire it into the niwa subprocess under test by setting
// NIWA_INFISICAL_API_URL to the server's URL -- the same mechanism
// already proven for NIWA_GITHUB_API_URL.
//
// Per-resource state is independently seedable present / absent /
// malformed, and fault-mode injection lets a scenario force a
// specific failure shape (wrong-org auth, mint rejection, plan-gate,
// login-exchange failure, read-hop failure, revocation failure)
// without needing a bespoke server for each case. Every request is
// recorded so scenarios can assert things like "the team path made
// zero identity/org/project management calls" (AC-10) or "a revoke
// fired for the just-minted id" (AC-34).
type infisicalFakeServer struct {
	srv *httptest.Server

	// prevDefaultClient saves http.DefaultClient as it was before this
	// server overrode it (see newInfisicalFakeServer's comment on why
	// that override exists), restored by Close.
	prevDefaultClient *http.Client

	mu sync.Mutex

	// identities holds per-identity-id resource state, keyed by
	// identityID. A missing entry behaves as "absent" (404).
	identities map[string]identityFixture

	// mintResponses holds the mint-client-secret response to return
	// for a given identityID's next mint call. A missing entry behaves
	// as "absent" (mint rejected).
	mintResponses map[string]mintFixture

	// environmentSecrets holds the tri-state fixture for the R9 read
	// hop, keyed by "projectID/env/path".
	environmentSecrets map[string]environmentFixture

	// memberships holds the tri-state fixture for the team-phase
	// environment-grant landing check (the project-identity-membership
	// read), keyed by "projectID/identityID".
	memberships map[string]membershipFixture

	// loginClientSecrets maps a clientSecret value to the accessToken
	// the universal-auth login exchange should return for it.
	loginClientSecrets map[string]string

	// faults holds one status-code override per fault mode. A zero
	// value means "no fault injected."
	faults map[faultMode]int

	requests []infisicalFakeRequest
}

// identityFixture models one identity's read-identity response state.
type identityFixture struct {
	present   bool
	malformed bool
	clientID  string
}

// mintFixture models one identity's mint-client-secret response
// state.
type mintFixture struct {
	present      bool
	malformed    bool
	clientSecret string
	secretID     string
}

// environmentFixture models the R9 read-hop response state for one
// (project, env, path) tuple.
type environmentFixture struct {
	present   bool
	malformed bool
}

// membershipFixture models one (project, identity) pair's
// project-identity-membership response state -- the team-phase
// environment-grant landing check.
type membershipFixture struct {
	present   bool
	malformed bool
	granted   bool
}

// faultMode names the fault-injection dimensions AC-lists for issue
// 2's fake server: wrong-org auth failure, mint rejection, plan-gate,
// login-exchange failure, read-hop failure, and revocation failure.
type faultMode int

const (
	faultWrongOrg faultMode = iota
	faultMintRejection
	faultPlanGate
	faultLoginExchangeFailure
	faultReadHopFailure
	faultRevocationFailure
)

type infisicalFakeRequest struct {
	Method string
	Path   string
	Header http.Header
}

func newInfisicalFakeServer() *infisicalFakeServer {
	s := &infisicalFakeServer{
		identities:         map[string]identityFixture{},
		mintResponses:      map[string]mintFixture{},
		environmentSecrets: map[string]environmentFixture{},
		memberships:        map[string]membershipFixture{},
		loginClientSecrets: map[string]string{},
		faults:             map[faultMode]int{},
	}
	// TLS, not plain HTTP: CheckAPIURL (internal/onboard/apiurl.go)
	// unconditionally hard-rejects a non-https api_url in every mode,
	// with no override -- a deliberate, no-escape-hatch rule (Decision
	// 4's supply-chain guard). A plain-HTTP double would make every
	// scenario that drives the real onboard binary against it fail
	// that gate before ever reaching a mint/verify call. httptest's
	// self-signed cert isn't in any trust store by default, so:
	//
	//   - In-process callers (this package's own *_test.go unit tests,
	//     which invoke internal/vault/infisical functions directly via
	//     http.DefaultClient) need http.DefaultClient swapped for
	//     s.srv.Client() -- done here, restored by Close -- since
	//     those functions have no client-injection seam of their own.
	//   - The niwa subprocess under test (a separate process spawned
	//     by the Gherkin steps) picks up trust via the SSL_CERT_FILE
	//     env var, which Go's crypto/x509 honors as the process's sole
	//     root pool on Linux; CertPEM() below hands the step definition
	//     the bytes to write to a file and wire in via iSetEnv.
	s.srv = httptest.NewTLSServer(http.HandlerFunc(s.handle))
	s.prevDefaultClient = http.DefaultClient
	http.DefaultClient = s.srv.Client()
	return s
}

// URL returns the base URL to set NIWA_INFISICAL_API_URL to.
func (s *infisicalFakeServer) URL() string { return s.srv.URL }

// CertPEM returns the server's self-signed certificate, PEM-encoded,
// for a scenario to write to a file and point SSL_CERT_FILE at so the
// niwa subprocess under test trusts this server (see the TLS comment
// on newInfisicalFakeServer above).
func (s *infisicalFakeServer) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.srv.Certificate().Raw})
}

// Close shuts down the underlying httptest.Server. Safe to call
// multiple times.
func (s *infisicalFakeServer) Close() {
	http.DefaultClient = s.prevDefaultClient
	s.srv.Close()
}

// SetIdentityPresent seeds a present, well-formed identity with the
// given Universal-Auth clientID.
func (s *infisicalFakeServer) SetIdentityPresent(identityID, clientID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identities[identityID] = identityFixture{present: true, clientID: clientID}
}

// SetIdentityAbsent seeds a 404 response for the given identity --
// no Universal Auth attached (or the identity doesn't exist).
func (s *infisicalFakeServer) SetIdentityAbsent(identityID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.identities, identityID)
}

// SetIdentityMalformed seeds a 200 response with a body missing the
// expected identityUniversalAuth.clientId field.
func (s *infisicalFakeServer) SetIdentityMalformed(identityID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identities[identityID] = identityFixture{present: true, malformed: true}
}

// SetMintPresent seeds a successful mint-client-secret response for
// the given identity.
func (s *infisicalFakeServer) SetMintPresent(identityID, clientSecret, secretID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mintResponses[identityID] = mintFixture{present: true, clientSecret: clientSecret, secretID: secretID}
}

// SetMintAbsent removes any seeded mint response, so the next mint
// call for identityID falls through to the default 500.
func (s *infisicalFakeServer) SetMintAbsent(identityID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.mintResponses, identityID)
}

// SetMintMalformed seeds a 200 mint response missing the expected
// clientSecret/clientSecretData.id fields.
func (s *infisicalFakeServer) SetMintMalformed(identityID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mintResponses[identityID] = mintFixture{present: true, malformed: true}
}

// SetLoginExchange seeds the universal-auth login response returned
// when the minted clientSecret is exchanged for an access token
// (the first hop of the R9 two-hop proof).
func (s *infisicalFakeServer) SetLoginExchange(clientSecret, accessToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loginClientSecrets[clientSecret] = accessToken
}

// SetEnvironmentSecretsPresent seeds a successful /v4/secrets
// response for the given (project, env, path) tuple -- the R9 read
// hop's success is also the environment-grant proxy: present means
// the grant resolves.
func (s *infisicalFakeServer) SetEnvironmentSecretsPresent(projectID, env, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.environmentSecrets[envKey(projectID, env, path)] = environmentFixture{present: true}
}

// SetEnvironmentSecretsAbsent removes any seeded response, so the
// read hop 404s -- no grant.
func (s *infisicalFakeServer) SetEnvironmentSecretsAbsent(projectID, env, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.environmentSecrets, envKey(projectID, env, path))
}

// SetEnvironmentSecretsMalformed seeds a 200 response with an
// unparseable body.
func (s *infisicalFakeServer) SetEnvironmentSecretsMalformed(projectID, env, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.environmentSecrets[envKey(projectID, env, path)] = environmentFixture{present: true, malformed: true}
}

// SetMembershipGranted seeds a present membership response for the
// given (projectID, identityID) pair with at least one role assigned
// -- the team-phase environment-grant landing check's "granted"
// outcome.
func (s *infisicalFakeServer) SetMembershipGranted(projectID, identityID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memberships[membershipKey(projectID, identityID)] = membershipFixture{present: true, granted: true}
}

// SetMembershipAbsent removes any seeded membership, so the next read
// 404s -- no grant.
func (s *infisicalFakeServer) SetMembershipAbsent(projectID, identityID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.memberships, membershipKey(projectID, identityID))
}

// SetMembershipMalformed seeds a 200 response body missing the
// expected identityMembership.roles field.
func (s *infisicalFakeServer) SetMembershipMalformed(projectID, identityID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memberships[membershipKey(projectID, identityID)] = membershipFixture{present: true, malformed: true}
}

// SetFault forces the given fault mode to respond with status for
// every subsequent matching request. Pass status 0 to clear a
// previously set fault.
func (s *infisicalFakeServer) SetFault(mode faultMode, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status == 0 {
		delete(s.faults, mode)
		return
	}
	s.faults[mode] = status
}

// Requests returns a snapshot of the request log.
func (s *infisicalFakeServer) Requests() []infisicalFakeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]infisicalFakeRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// CountRequests returns the number of recorded requests whose path
// contains pathSubstring. Used for AC-10 ("zero management calls on
// the team path") and similar assertions.
func (s *infisicalFakeServer) CountRequests(pathSubstring string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, r := range s.requests {
		if strings.Contains(r.Path, pathSubstring) {
			count++
		}
	}
	return count
}

// ResetLog clears the request log without affecting configured
// responses.
func (s *infisicalFakeServer) ResetLog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

func (s *infisicalFakeServer) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, infisicalFakeRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Header: r.Header.Clone(),
	})
	s.mu.Unlock()

	// Every modeled path shape has a distinct, unambiguous tail, so
	// routing is a sequence of suffix checks from most to least
	// specific (revoke's tail is a superset of mint's, which is a
	// superset of read-identity's, so order matters):
	//
	//   .../universal-auth/login                                (login)
	//   .../identities/{id}/client-secrets/{secretId}/revoke     (revoke)
	//   .../identities/{id}/client-secrets                       (mint)
	//   .../projects/{id}/memberships/identities/{id}            (membership)
	//   .../identities/{id}                                      (read-identity)
	//   .../v4/secrets                                           (env read)
	segments := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")

	switch {
	case r.Method == http.MethodPost && matchesSuffix(segments, "v1", "auth", "universal-auth", "login"):
		s.handleLogin(w, r)
	case r.Method == http.MethodPost && matchesSuffix(segments, "client-secrets", "*", "revoke"):
		s.handleRevoke(w, r, segments)
	case r.Method == http.MethodPost && matchesSuffix(segments, "identities", "*", "client-secrets"):
		s.handleMint(w, r, segments)
	case r.Method == http.MethodGet && matchesSuffix(segments, "projects", "*", "memberships", "identities", "*"):
		s.handleReadMembership(w, r, segments)
	case r.Method == http.MethodGet && matchesSuffix(segments, "identities", "*"):
		s.handleReadIdentity(w, r, segments)
	case r.Method == http.MethodGet && matchesSuffix(segments, "v4", "secrets"):
		s.handleEnvironmentSecrets(w, r)
	default:
		http.NotFound(w, r)
	}
}

// matchesSuffix reports whether segments ends with want, where the
// wildcard "*" in want matches any single segment (used for path
// variables like an identity or secret id).
func matchesSuffix(segments []string, want ...string) bool {
	if len(segments) < len(want) {
		return false
	}
	tail := segments[len(segments)-len(want):]
	for i, w := range want {
		if w != "*" && tail[i] != w {
			return false
		}
	}
	return true
}

func (s *infisicalFakeServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if status, ok := s.faults[faultLoginExchangeFailure]; ok {
		s.mu.Unlock()
		w.WriteHeader(status)
		return
	}
	if status, ok := s.faults[faultWrongOrg]; ok {
		s.mu.Unlock()
		w.WriteHeader(status)
		return
	}
	s.mu.Unlock()

	var body struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	accessToken, ok := s.loginClientSecrets[body.ClientSecret]
	s.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"accessToken": accessToken})
}

func (s *infisicalFakeServer) handleReadIdentity(w http.ResponseWriter, r *http.Request, segments []string) {
	identityID := segments[len(segments)-1]

	s.mu.Lock()
	if status, ok := s.faults[faultWrongOrg]; ok {
		s.mu.Unlock()
		w.WriteHeader(status)
		return
	}
	fixture, ok := s.identities[identityID]
	s.mu.Unlock()

	if !ok || !fixture.present {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if fixture.malformed {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"identityUniversalAuth": {}}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"identityUniversalAuth": map[string]string{"clientId": fixture.clientID},
	})
}

func (s *infisicalFakeServer) handleReadMembership(w http.ResponseWriter, r *http.Request, segments []string) {
	// Routing already confirmed the shape
	// .../projects/{projectID}/memberships/identities/{identityID}.
	identityID := segments[len(segments)-1]
	projectID := segments[len(segments)-4]

	s.mu.Lock()
	if status, ok := s.faults[faultWrongOrg]; ok {
		s.mu.Unlock()
		w.WriteHeader(status)
		return
	}
	fixture, ok := s.memberships[membershipKey(projectID, identityID)]
	s.mu.Unlock()

	if !ok || !fixture.present {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if fixture.malformed {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"identityMembership": {}}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	roles := []map[string]string{}
	if fixture.granted {
		roles = append(roles, map[string]string{"role": "member"})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"identityMembership": map[string]any{"roles": roles},
	})
}

func (s *infisicalFakeServer) handleMint(w http.ResponseWriter, r *http.Request, segments []string) {
	// Routing already confirmed the shape .../identities/{id}/client-secrets.
	identityID := segments[len(segments)-2]

	s.mu.Lock()
	if status, ok := s.faults[faultWrongOrg]; ok {
		s.mu.Unlock()
		w.WriteHeader(status)
		return
	}
	if status, ok := s.faults[faultPlanGate]; ok {
		s.mu.Unlock()
		w.WriteHeader(status)
		return
	}
	if status, ok := s.faults[faultMintRejection]; ok {
		s.mu.Unlock()
		w.WriteHeader(status)
		return
	}
	fixture, ok := s.mintResponses[identityID]
	s.mu.Unlock()

	if !ok || !fixture.present {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if fixture.malformed {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"clientSecret":     fixture.clientSecret,
		"clientSecretData": map[string]string{"id": fixture.secretID},
	})
}

func (s *infisicalFakeServer) handleRevoke(w http.ResponseWriter, r *http.Request, segments []string) {
	s.mu.Lock()
	status, faulted := s.faults[faultRevocationFailure]
	s.mu.Unlock()
	if faulted {
		w.WriteHeader(status)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *infisicalFakeServer) handleEnvironmentSecrets(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if status, ok := s.faults[faultReadHopFailure]; ok {
		s.mu.Unlock()
		w.WriteHeader(status)
		return
	}
	s.mu.Unlock()

	q := r.URL.Query()
	key := envKey(q.Get("projectId"), q.Get("environment"), q.Get("secretPath"))

	s.mu.Lock()
	fixture, ok := s.environmentSecrets[key]
	s.mu.Unlock()

	if !ok || !fixture.present {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if fixture.malformed {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{not valid json`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"secrets": []}`))
}

func envKey(projectID, env, path string) string {
	if path == "" {
		path = "/"
	}
	return projectID + "/" + env + "/" + path
}

func membershipKey(projectID, identityID string) string {
	return projectID + "/" + identityID
}
