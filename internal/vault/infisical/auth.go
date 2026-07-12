package infisical

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/tsukumogami/niwa/internal/secret"
)

// defaultAPIURL is the Infisical cloud API endpoint used when the
// credential entry does not specify api_url.
const defaultAPIURL = "https://app.infisical.com/api"

// apiURLEnvOverride is the test/CI override variable, mirroring the
// proven NIWA_GITHUB_API_URL pattern in internal/github/client.go.
const apiURLEnvOverride = "NIWA_INFISICAL_API_URL"

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

	configVal, _ := entry["api_url"].(string)
	apiURL := resolveAPIURL(configVal)

	return authenticateHTTP(ctx, apiURL, clientID, clientSecret)
}

// resolveAPIURL implements the single api_url precedence rule shared
// by Authenticate and every management.go call: an explicit
// config-declared value wins; otherwise the NIWA_INFISICAL_API_URL
// environment override (mirroring the proven NIWA_GITHUB_API_URL
// pattern in internal/github/client.go, intended primarily for tests
// against infisicalFakeServer); otherwise the Infisical cloud default.
//
// Consolidating this into one function (rather than letting
// Authenticate and management.go each read entry["api_url"] /
// os.Getenv independently) is what makes "exactly one precedence
// rule in the package" true rather than aspirational.
func resolveAPIURL(configVal string) string {
	if configVal != "" {
		return configVal
	}
	if envVal := os.Getenv(apiURLEnvOverride); envVal != "" {
		return envVal
	}
	return defaultAPIURL
}

// ValidateAPIURL checks a resolved api_url against the two-rule
// supply-chain guard Decision 4 requires, run at wizard entry right
// after resolveAPIURL and before any bearer-carrying call.
//
// Rule 1 (unconditional hard reject): a non-https scheme -- including
// a malformed URL that fails to parse -- is rejected in every mode,
// before any request is built. There is no override for this rule;
// "warn and proceed" would be silent acceptance in a scripted run.
//
// Rule 2 (flagged, not rejected here): a well-formed https URL that
// differs from the Infisical cloud default is returned with
// nonDefault=true. This function does not itself gate on that flag --
// it hands the decision to the caller, which is the entry-time gate
// (onboard.CheckAPIURL: an interactive confirm, or --accept-api-url /
// exit 2 in a non-TTY run). R14 permits a workspace to declare a
// non-default api_url for a self-hosted instance; this function's job
// is only to make that declaration visible and scheme-checked, never
// to forbid it.
func ValidateAPIURL(apiURL string) (nonDefault bool, err error) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return false, secret.Errorf("infisical: api_url %q is not a valid URL: %w", apiURL, err)
	}
	if u.Scheme != "https" {
		return false, secret.Errorf("infisical: api_url %q must use https (got scheme %q)", apiURL, u.Scheme)
	}
	return apiURL != defaultAPIURL, nil
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
		scrubbed := scrubCtx(ctx, string(respBody))
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
