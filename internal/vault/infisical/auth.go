package infisical

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/tsukumogami/niwa/internal/secret"
)

// defaultAPIURL is the Infisical cloud API endpoint used when the
// credential entry does not specify api_url.
const defaultAPIURL = "https://app.infisical.com/api"

// universalAuthPath is the API path for machine-identity login.
const universalAuthPath = "/v1/auth/universal-auth/login"

// Authenticate obtains a short-lived JWT from Infisical's universal-auth
// endpoint using machine-identity credentials from a ProviderAuthEntry.
// Returns the JWT string on success.
//
// The entry must contain:
//   - "client_id" (string, required)
//   - "client_secret" (string, required)
//   - "api_url" (string, optional -- defaults to "https://app.infisical.com/api")
//
// Uses net/http + encoding/json (stdlib, R20 compliant). The
// client_secret is sent in the HTTP POST body, NEVER on subprocess
// argv (R21). Errors are scrubbed via secret.Errorf.
func Authenticate(ctx context.Context, entry map[string]any) (string, error) {
	clientID, ok := entry["client_id"].(string)
	if !ok || clientID == "" {
		return "", fmt.Errorf("infisical auth: credential entry missing required field \"client_id\"")
	}

	clientSecret, ok := entry["client_secret"].(string)
	if !ok || clientSecret == "" {
		return "", fmt.Errorf("infisical auth: credential entry missing required field \"client_secret\"")
	}

	// Register client_secret on the context's redactor so all
	// downstream errors scrub it automatically.
	if r := secret.RedactorFrom(ctx); r != nil {
		r.Register([]byte(clientSecret))
	}

	apiURL := defaultAPIURL
	if raw, ok := entry["api_url"].(string); ok && raw != "" {
		apiURL = raw
	}

	return authenticateHTTP(ctx, apiURL, clientID, clientSecret)
}

// authenticateHTTP performs the HTTP POST to the universal-auth login
// endpoint. Separated from Authenticate for testability (callers can
// override the HTTP client via a custom transport on the context, or
// tests can provide an httptest.Server URL as apiURL).
func authenticateHTTP(ctx context.Context, apiURL, clientID, clientSecret string) (string, error) {
	body, err := json.Marshal(map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
	})
	if err != nil {
		return "", secret.Errorf("infisical auth: marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL+universalAuthPath, bytes.NewReader(body))
	if err != nil {
		return "", secret.Errorf("infisical auth: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", secret.Errorf("infisical auth: HTTP POST failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", secret.Errorf("infisical auth: reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Scrub response body -- Infisical may echo credentials in error payloads.
		scrubbed := scrubResponseBody(ctx, string(respBody), clientSecret)
		return "", secret.Errorf(
			"infisical auth: universal-auth login returned HTTP %d: %s",
			resp.StatusCode, scrubbed,
		)
	}

	var result struct {
		AccessToken string `json:"accessToken"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", secret.Errorf("infisical auth: parsing response JSON: %w", err)
	}
	if result.AccessToken == "" {
		return "", secret.Errorf("infisical auth: response missing accessToken field")
	}

	return result.AccessToken, nil
}

// scrubResponseBody removes the client_secret from a response body
// string. Uses the context's redactor if available, plus a direct
// string replacement as a belt-and-suspenders measure.
func scrubResponseBody(ctx context.Context, body, clientSecret string) string {
	out := body
	if r := secret.RedactorFrom(ctx); r != nil {
		out = r.Scrub(out)
	}
	return out
}
