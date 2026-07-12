package functional

import (
	"context"
	"net/http"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
)

// TestInfisicalFakeServer_ReadIdentity verifies the double's
// read-identity endpoint composes with the real client function
// end-to-end, present and absent.
func TestInfisicalFakeServer_ReadIdentity(t *testing.T) {
	srv := newInfisicalFakeServer()
	defer srv.Close()

	srv.SetIdentityPresent("ident-1", "client-abc")

	ctx := secret.WithRedactor(context.Background(), secret.NewRedactor())
	bearer := secret.New([]byte("operator-bearer-value"), secret.Origin{})

	clientID, err := infisical.ReadIdentity(ctx, srv.URL(), bearer, "ident-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clientID != "client-abc" {
		t.Errorf("clientID = %q, want client-abc", clientID)
	}

	_, err = infisical.ReadIdentity(ctx, srv.URL(), bearer, "ident-absent")
	if err == nil {
		t.Fatal("expected error for absent identity")
	}
}

// TestInfisicalFakeServer_MintAndVerifyAndRevoke drives the full
// mint -> login-exchange -> read-hop -> revoke sequence against the
// double, mirroring the individual-setup pipeline's R9 two-hop proof.
func TestInfisicalFakeServer_MintAndVerifyAndRevoke(t *testing.T) {
	srv := newInfisicalFakeServer()
	defer srv.Close()

	srv.SetMintPresent("ident-1", "minted-secret-value", "secret-id-001")
	srv.SetLoginExchange("minted-secret-value", "access-token-xyz")
	srv.SetEnvironmentSecretsPresent("proj-1", "dev", "/")

	ctx := secret.WithRedactor(context.Background(), secret.NewRedactor())
	bearer := secret.New([]byte("operator-bearer-value"), secret.Origin{})

	clientSecret, secretID, err := infisical.MintClientSecret(ctx, srv.URL(), bearer, "ident-1")
	if err != nil {
		t.Fatalf("MintClientSecret: %v", err)
	}
	if secretID != "secret-id-001" {
		t.Errorf("secretID = %q, want secret-id-001", secretID)
	}

	accessToken, err := infisical.Authenticate(ctx, map[string]any{
		"client_id":     "client-abc",
		"client_secret": string(reveal.UnsafeReveal(clientSecret)),
		"api_url":       srv.URL(),
	})
	if err != nil {
		t.Fatalf("Authenticate (login exchange): %v", err)
	}
	if accessToken != "access-token-xyz" {
		t.Errorf("accessToken = %q, want access-token-xyz", accessToken)
	}

	tokenValue := secret.New([]byte(accessToken), secret.Origin{})
	if err := infisical.ReadEnvironmentSecrets(ctx, srv.URL(), tokenValue, "proj-1", "dev", "/"); err != nil {
		t.Fatalf("ReadEnvironmentSecrets: %v", err)
	}

	if err := infisical.RevokeClientSecret(ctx, srv.URL(), bearer, "ident-1", secretID); err != nil {
		t.Fatalf("RevokeClientSecret: %v", err)
	}

	if got := srv.CountRequests("/client-secrets/secret-id-001/revoke"); got != 1 {
		t.Errorf("revoke request count = %d, want 1", got)
	}
}

// TestInfisicalFakeServer_FaultInjection exercises each fault mode
// the design's Test-double architecture names, confirming the server
// returns the injected status for the matching endpoint.
func TestInfisicalFakeServer_FaultInjection(t *testing.T) {
	srv := newInfisicalFakeServer()
	defer srv.Close()

	ctx := secret.WithRedactor(context.Background(), secret.NewRedactor())
	bearer := secret.New([]byte("operator-bearer-value"), secret.Origin{})

	t.Run("wrong-org", func(t *testing.T) {
		srv.SetFault(faultWrongOrg, http.StatusForbidden)
		defer srv.SetFault(faultWrongOrg, 0)
		srv.SetIdentityPresent("ident-wrongorg", "client-abc")
		_, err := infisical.ReadIdentity(ctx, srv.URL(), bearer, "ident-wrongorg")
		if err == nil {
			t.Fatal("expected error under wrong-org fault")
		}
	})

	t.Run("mint-rejection", func(t *testing.T) {
		srv.SetFault(faultMintRejection, http.StatusBadRequest)
		defer srv.SetFault(faultMintRejection, 0)
		srv.SetMintPresent("ident-mintreject", "s", "id")
		_, _, err := infisical.MintClientSecret(ctx, srv.URL(), bearer, "ident-mintreject")
		if err == nil {
			t.Fatal("expected error under mint-rejection fault")
		}
	})

	t.Run("plan-gate", func(t *testing.T) {
		srv.SetFault(faultPlanGate, http.StatusPaymentRequired)
		defer srv.SetFault(faultPlanGate, 0)
		srv.SetMintPresent("ident-plangate", "s", "id")
		_, _, err := infisical.MintClientSecret(ctx, srv.URL(), bearer, "ident-plangate")
		if err == nil {
			t.Fatal("expected error under plan-gate fault")
		}
	})

	t.Run("login-exchange-failure", func(t *testing.T) {
		srv.SetFault(faultLoginExchangeFailure, http.StatusUnauthorized)
		defer srv.SetFault(faultLoginExchangeFailure, 0)
		_, err := infisical.Authenticate(ctx, map[string]any{
			"client_id":     "client-abc",
			"client_secret": "some-secret-value",
			"api_url":       srv.URL(),
		})
		if err == nil {
			t.Fatal("expected error under login-exchange-failure fault")
		}
	})

	t.Run("read-hop-failure", func(t *testing.T) {
		srv.SetFault(faultReadHopFailure, http.StatusForbidden)
		defer srv.SetFault(faultReadHopFailure, 0)
		token := secret.New([]byte("some-token"), secret.Origin{})
		err := infisical.ReadEnvironmentSecrets(ctx, srv.URL(), token, "proj-1", "dev", "/")
		if err == nil {
			t.Fatal("expected error under read-hop-failure fault")
		}
	})

	t.Run("revocation-failure", func(t *testing.T) {
		srv.SetFault(faultRevocationFailure, http.StatusInternalServerError)
		defer srv.SetFault(faultRevocationFailure, 0)
		err := infisical.RevokeClientSecret(ctx, srv.URL(), bearer, "ident-1", "secret-id-001")
		if err == nil {
			t.Fatal("expected error under revocation-failure fault")
		}
	})
}

// TestInfisicalFakeServer_ReadProjectMembership verifies the double's
// project-identity-membership endpoint composes with the real client
// function end-to-end -- granted, not-yet-granted, and absent.
func TestInfisicalFakeServer_ReadProjectMembership(t *testing.T) {
	srv := newInfisicalFakeServer()
	defer srv.Close()

	srv.SetMembershipGranted("proj-1", "ident-1")

	ctx := secret.WithRedactor(context.Background(), secret.NewRedactor())
	bearer := secret.New([]byte("operator-bearer-value"), secret.Origin{})

	granted, err := infisical.ReadProjectMembership(ctx, srv.URL(), bearer, "proj-1", "ident-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !granted {
		t.Error("granted = false, want true")
	}

	granted, err = infisical.ReadProjectMembership(ctx, srv.URL(), bearer, "proj-1", "ident-absent")
	if err != nil {
		t.Fatalf("a 404 must not be an error: %v", err)
	}
	if granted {
		t.Error("granted = true, want false for an absent membership")
	}

	if got := srv.CountRequests("/memberships/identities/ident-1"); got != 1 {
		t.Errorf("CountRequests(/memberships/identities/ident-1) = %d, want 1", got)
	}
}

// TestInfisicalFakeServer_RequestRecorder confirms the recorder
// tracks every request and CountRequests filters correctly -- the
// mechanism AC-10's team-path assertion depends on.
func TestInfisicalFakeServer_RequestRecorder(t *testing.T) {
	srv := newInfisicalFakeServer()
	defer srv.Close()

	srv.SetIdentityPresent("ident-1", "client-abc")

	ctx := secret.WithRedactor(context.Background(), secret.NewRedactor())
	bearer := secret.New([]byte("operator-bearer-value"), secret.Origin{})

	if _, err := infisical.ReadIdentity(ctx, srv.URL(), bearer, "ident-1"); err != nil {
		t.Fatalf("ReadIdentity: %v", err)
	}

	if got := srv.CountRequests("/identities/ident-1"); got != 1 {
		t.Errorf("CountRequests(/identities/ident-1) = %d, want 1", got)
	}
	if got := srv.CountRequests("/client-secrets"); got != 0 {
		t.Errorf("CountRequests(/client-secrets) = %d, want 0 (no mint call made)", got)
	}

	srv.ResetLog()
	if got := len(srv.Requests()); got != 0 {
		t.Errorf("len(Requests()) after ResetLog = %d, want 0", got)
	}
}
