package infisical

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
)

// ErrIdentityNotFound is returned by ReadIdentity when the Infisical
// API responds 404 for the given identity ID -- the identity exists
// at the org level (or doesn't exist at all) but has no Universal
// Auth method attached. The onboard wizard's detection funnel treats
// this as the team-setup routing signal.
var ErrIdentityNotFound = errors.New("infisical: identity not found (no Universal Auth attached)")

// ErrUnauthorized is returned by any management call that receives a
// 401 or 403 from the Infisical API. This is the authoritative
// "wrong org / bad session" signal: unlike the proactive
// `infisical login status` detection in session.go (whose output
// shape is not guaranteed stable), a 401/403 on an actual privileged
// call is always trustworthy.
var ErrUnauthorized = errors.New("infisical: unauthorized (wrong organization, expired session, or insufficient scope)")

// managementBasePath is the path prefix shared by every Universal
// Auth identity management endpoint. Combined with apiURL (which
// already carries the "/api" suffix -- see defaultAPIURL) the same
// way universalAuthPath does in auth.go.
const managementBasePath = "/v1/auth/universal-auth/identities/"

// environmentSecretsPath is the R9 read-hop endpoint. Confirmed
// current (non-deprecated) per NOTE-onboard-rest-verification.md;
// the v3 "raw" equivalent now lives under the API docs' deprecated/
// section and must not be targeted.
const environmentSecretsPath = "/v4/secrets"

// identityUniversalAuthResponse models the Universal-Auth-specific
// identity retrieve endpoint's response shape. The API nests the
// fields the wizard cares about under "identityUniversalAuth"; the
// clientId field is camelCase in the wire payload (not client_id).
type identityUniversalAuthResponse struct {
	IdentityUniversalAuth struct {
		ClientID string `json:"clientId"`
	} `json:"identityUniversalAuth"`
}

// ReadIdentity fetches the Universal Auth client_id attached to the
// given identity, authenticating with the operator's own session
// bearer. This doubles as the Universal-Auth-attach landing check
// (Decision 4 / Assumption A): a 200 response means Universal Auth is
// attached, a 404 means it isn't.
//
// bearer is registered on the context's redactor before use; the
// response body is scrubbed by that redactor before any error is
// constructed.
//
// Returns ErrIdentityNotFound on a 404 and ErrUnauthorized on a
// 401/403 so callers (the onboard detection funnel) can branch on
// the topology signal without string-matching error text.
func ReadIdentity(ctx context.Context, apiURL string, bearer secret.Value, identityID string) (clientID string, err error) {
	registerOnRedactor(ctx, bearer)

	if identityID == "" {
		return "", secret.Errorf("infisical: ReadIdentity requires a non-empty identityID")
	}

	reqURL := apiURL + managementBasePath + url.PathEscape(identityID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", secret.Errorf("infisical: ReadIdentity: creating request: %w", err)
	}
	setBearerHeader(req, bearer)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", secret.Errorf("infisical: ReadIdentity: HTTP GET failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", secret.Errorf("infisical: ReadIdentity: reading response body: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return "", secret.Errorf("infisical: ReadIdentity(%s): %w", identityID, ErrIdentityNotFound)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", secret.Errorf("infisical: ReadIdentity(%s): %w", identityID, ErrUnauthorized)
	}
	if resp.StatusCode != http.StatusOK {
		scrubbed := scrubCtx(ctx, string(respBody))
		return "", secret.Errorf(
			"infisical: ReadIdentity(%s): unexpected HTTP %d: %s",
			identityID, resp.StatusCode, scrubbed,
		)
	}

	var parsed identityUniversalAuthResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", secret.Errorf("infisical: ReadIdentity(%s): parsing response JSON: %w", identityID, err)
	}
	if parsed.IdentityUniversalAuth.ClientID == "" {
		return "", secret.Errorf("infisical: ReadIdentity(%s): response missing identityUniversalAuth.clientId", identityID)
	}

	return parsed.IdentityUniversalAuth.ClientID, nil
}

// mintClientSecretResponse models the client-secret creation
// endpoint's response. clientSecretData.id is the non-secret
// identifier the wizard captures for R20 revocation bookkeeping --
// it is NOT called "secret_id" in the wire payload despite the
// carried assumption's naming.
type mintClientSecretResponse struct {
	ClientSecret     string `json:"clientSecret"`
	ClientSecretData struct {
		ID string `json:"id"`
	} `json:"clientSecretData"`
}

// MintClientSecret mints a fresh Universal Auth client secret for the
// given identity, authenticating with the operator's own session
// bearer. Never creates an identity -- callers must have already
// confirmed one exists via ReadIdentity.
//
// The minted clientSecret is registered on the context's redactor the
// instant it is parsed from the response, before any further
// processing (including the non-error return path) -- so that any
// later log, error, or re-wrap of this response body is already
// protected. secretID is returned as a plain string: it identifies
// the mint record for R20 revocation, not the credential material
// itself.
//
// Deviation from the design's carried function surface: the design
// sketch also returns a clientID secret.Value. Per
// NOTE-onboard-rest-verification.md the verified mint response
// carries only clientSecret and clientSecretData.id -- no clientId
// field -- and identityID (the org-level identity resource id this
// function's URL path targets) is a different identifier from the
// Universal Auth clientId ReadIdentity returns, so synthesizing a
// "clientID" return value here from identityID would be fabricated
// data, not a real API value. Callers thread the clientID they
// already obtained from ReadIdentity forward themselves.
func MintClientSecret(ctx context.Context, apiURL string, bearer secret.Value, identityID string) (clientSecret secret.Value, secretID string, err error) {
	registerOnRedactor(ctx, bearer)

	if identityID == "" {
		return secret.Value{}, "", secret.Errorf("infisical: MintClientSecret requires a non-empty identityID")
	}

	reqURL := apiURL + managementBasePath + url.PathEscape(identityID) + "/client-secrets"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return secret.Value{}, "", secret.Errorf("infisical: MintClientSecret: creating request: %w", err)
	}
	setBearerHeader(req, bearer)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return secret.Value{}, "", secret.Errorf("infisical: MintClientSecret: HTTP POST failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return secret.Value{}, "", secret.Errorf("infisical: MintClientSecret: reading response body: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return secret.Value{}, "", secret.Errorf("infisical: MintClientSecret(%s): %w", identityID, ErrUnauthorized)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		scrubbed := scrubCtx(ctx, string(respBody))
		return secret.Value{}, "", secret.Errorf(
			"infisical: MintClientSecret(%s): rejected with HTTP %d: %s",
			identityID, resp.StatusCode, scrubbed,
		)
	}

	var parsed mintClientSecretResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return secret.Value{}, "", secret.Errorf("infisical: MintClientSecret(%s): parsing response JSON: %w", identityID, err)
	}

	// Register the minted secret on the redactor the instant it is
	// parsed -- before constructing the secret.Value, before any
	// other processing.
	if r := secret.RedactorFrom(ctx); r != nil {
		r.Register([]byte(parsed.ClientSecret))
	}

	if parsed.ClientSecret == "" || parsed.ClientSecretData.ID == "" {
		return secret.Value{}, "", secret.Errorf(
			"infisical: MintClientSecret(%s): response missing clientSecret or clientSecretData.id", identityID,
		)
	}

	origin := secret.Origin{ProviderName: Kind, Key: "client_secret"}
	clientSecretValue := secret.New([]byte(parsed.ClientSecret), origin)

	return clientSecretValue, parsed.ClientSecretData.ID, nil
}

// RevokeClientSecret revokes a previously minted client secret,
// authenticating with the operator's own session bearer.
//
// Per NOTE-onboard-rest-verification.md this is a POST .../revoke
// action endpoint, NOT a DELETE verb (the design's carried assumption
// was wrong on this point). No request body is sent.
//
// secretID is validated (rejecting a value that would break out of
// the URL path) and percent-escaped before embedding, so a hostile
// value carrying "/" or other path-structuring characters cannot
// redirect the request to an unintended path.
//
// Callers treat revocation as best-effort (R20): a failure here is
// reported as a warning and never changes the wizard's exit code.
func RevokeClientSecret(ctx context.Context, apiURL string, bearer secret.Value, identityID, secretID string) error {
	registerOnRedactor(ctx, bearer)

	if identityID == "" || secretID == "" {
		return secret.Errorf("infisical: RevokeClientSecret requires non-empty identityID and secretID")
	}

	reqURL := apiURL + managementBasePath + url.PathEscape(identityID) + "/client-secrets/" + url.PathEscape(secretID) + "/revoke"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return secret.Errorf("infisical: RevokeClientSecret: creating request: %w", err)
	}
	setBearerHeader(req, bearer)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return secret.Errorf("infisical: RevokeClientSecret: HTTP POST failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return secret.Errorf("infisical: RevokeClientSecret: reading response body: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return secret.Errorf("infisical: RevokeClientSecret(%s, %s): %w", identityID, secretID, ErrUnauthorized)
	}
	if resp.StatusCode != http.StatusOK {
		scrubbed := scrubCtx(ctx, string(respBody))
		return secret.Errorf(
			"infisical: RevokeClientSecret(%s, %s): unexpected HTTP %d: %s",
			identityID, secretID, resp.StatusCode, scrubbed,
		)
	}

	return nil
}

// ReadEnvironmentSecrets performs the R9 read-hop: a REST read of the
// target environment carrying the minted pair's short-lived access
// token in the Authorization header, never `infisical export
// --token` (which would put the token on argv, violating R17/AC-28).
// Its only purpose is to prove the minted pair actually resolves --
// callers discard the response contents and treat success/failure as
// the verification signal.
//
// The response body is scrubbed by the context's redactor before any
// error is constructed, since a failure payload could conceivably
// echo request parameters back.
func ReadEnvironmentSecrets(ctx context.Context, apiURL string, accessToken secret.Value, projectID, env, path string) error {
	registerOnRedactor(ctx, accessToken)

	if projectID == "" || env == "" {
		return secret.Errorf("infisical: ReadEnvironmentSecrets requires non-empty projectID and env")
	}
	if path == "" {
		path = "/"
	}

	q := url.Values{}
	q.Set("projectId", projectID)
	q.Set("environment", env)
	q.Set("secretPath", path)

	reqURL := apiURL + environmentSecretsPath + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return secret.Errorf("infisical: ReadEnvironmentSecrets: creating request: %w", err)
	}
	setBearerHeader(req, accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return secret.Errorf("infisical: ReadEnvironmentSecrets: HTTP GET failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return secret.Errorf("infisical: ReadEnvironmentSecrets: reading response body: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return secret.Errorf("infisical: ReadEnvironmentSecrets(project=%s env=%s): %w", projectID, env, ErrUnauthorized)
	}
	if resp.StatusCode != http.StatusOK {
		scrubbed := scrubCtx(ctx, string(respBody))
		return secret.Errorf(
			"infisical: ReadEnvironmentSecrets(project=%s env=%s): unexpected HTTP %d: %s",
			projectID, env, resp.StatusCode, scrubbed,
		)
	}

	return nil
}

// registerOnRedactor registers v's plaintext on ctx's redactor, if
// one is attached. Every management function calls this on every
// secret.Value parameter it receives, before that value is placed on
// the wire -- defense in depth alongside whatever registration the
// caller already performed when the Value was first constructed.
func registerOnRedactor(ctx context.Context, v secret.Value) {
	if r := secret.RedactorFrom(ctx); r != nil {
		r.RegisterValue(v)
	}
}

// setBearerHeader sets the Authorization header to "Bearer <token>"
// using the raw plaintext of v. This is the only place management.go
// reveals a secret.Value's bytes, and it does so solely to place them
// in an HTTP header -- never on argv, never in a log, never in an
// error string.
func setBearerHeader(req *http.Request, v secret.Value) {
	req.Header.Set("Authorization", "Bearer "+string(reveal.UnsafeReveal(v)))
}

// scrubCtx scrubs s through ctx's redactor, if attached. A thin named
// wrapper so every non-2xx-response call site above reads uniformly;
// Redactor.Scrub already handles the no-redactor-attached and
// no-fragments-registered cases as a no-op.
func scrubCtx(ctx context.Context, s string) string {
	if r := secret.RedactorFrom(ctx); r != nil {
		return r.Scrub(s)
	}
	return s
}
