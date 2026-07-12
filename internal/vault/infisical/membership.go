package infisical

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"

	"github.com/tsukumogami/niwa/internal/secret"
)

// projectMembershipBasePath is the path prefix for the project-level
// identity-membership read: the environment-grant landing check's
// REST surface, confirmed in NOTE-onboard-rest-verification.md
// (Assumption A). Combined with apiURL the same way managementBasePath
// does in management.go.
const projectMembershipBasePath = "/v1/projects/"

// identityMembershipResponse models the project-identity-membership
// endpoint's response shape: an `identityMembership` object carrying a
// `roles` array. Only the roles array's presence is consulted here --
// per the verification note, this endpoint confirms project-level
// role assignment but does not readably expose which environments a
// custom role's permission conditions scope access to, so a caller
// wanting that finer-grained detail must fall back to trusting the
// operator's claim.
type identityMembershipResponse struct {
	IdentityMembership struct {
		Roles []struct {
			Role string `json:"role"`
		} `json:"roles"`
	} `json:"identityMembership"`
}

// ReadProjectMembership performs the environment-grant landing check
// (Decision 4 / Assumption A): a project-level read of the given
// identity's membership, authenticating with the operator's own
// session bearer -- never a niwa-custodied admin token. granted
// reports whether the identity has at least one role assigned on the
// project; a 404 (no membership at all) is the "not yet granted"
// signal the team-phase landing check probes for, not an error.
//
// This confirms project-level role assignment only. It does not
// verify the finer-grained environment-scoping nuance inside a custom
// role (which environments its permission conditions actually cover)
// -- that detail is not readable from this endpoint, so callers fall
// back to trusting the operator's claim for that sub-question, per
// the verification note's documented fallback.
func ReadProjectMembership(ctx context.Context, apiURL string, bearer secret.Value, projectID, identityID string) (granted bool, err error) {
	registerOnRedactor(ctx, bearer)

	if projectID == "" || identityID == "" {
		return false, secret.Errorf("infisical: ReadProjectMembership requires non-empty projectID and identityID")
	}

	reqURL := apiURL + projectMembershipBasePath + url.PathEscape(projectID) + "/memberships/identities/" + url.PathEscape(identityID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return false, secret.Errorf("infisical: ReadProjectMembership: creating request: %w", err)
	}
	setBearerHeader(req, bearer)

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return false, secret.Errorf("infisical: ReadProjectMembership: HTTP GET failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, secret.Errorf("infisical: ReadProjectMembership: reading response body: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return false, secret.Errorf("infisical: ReadProjectMembership(%s, %s): %w", projectID, identityID, ErrUnauthorized)
	}
	if resp.StatusCode != http.StatusOK {
		scrubbed := scrubCtx(ctx, string(respBody))
		return false, secret.Errorf(
			"infisical: ReadProjectMembership(%s, %s): unexpected HTTP %d: %s",
			projectID, identityID, resp.StatusCode, scrubbed,
		)
	}

	var parsed identityMembershipResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return false, secret.Errorf("infisical: ReadProjectMembership(%s, %s): parsing response JSON: %w", projectID, identityID, err)
	}

	return len(parsed.IdentityMembership.Roles) > 0, nil
}
