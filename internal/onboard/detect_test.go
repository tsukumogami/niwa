package onboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
)

func testBearer() secret.Value {
	return secret.New([]byte("op-bearer-token"), secret.Origin{})
}

func TestDetect_ConfigEmptyRoutesTeamWithNoNetworkCall(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	result, err := Detect(context.Background(), srv.URL, testBearer(), "ident-1", true, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Phase != PhaseTeam {
		t.Errorf("Phase = %v, want PhaseTeam", result.Phase)
	}
	if requests != 0 {
		t.Errorf("teamVaultEmpty must short-circuit with zero network calls, got %d", requests)
	}
}

func TestDetect_IdentityNotFoundRoutesTeam(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	result, err := Detect(context.Background(), srv.URL, testBearer(), "ident-1", false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Phase != PhaseTeam {
		t.Errorf("Phase = %v, want PhaseTeam", result.Phase)
	}
}

func TestDetect_IdentityFoundNoPersonalCredRoutesIndividualSameLogin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"identityUniversalAuth": map[string]string{"clientId": "client-abc"},
		})
	}))
	defer srv.Close()

	result, err := Detect(context.Background(), srv.URL, testBearer(), "ident-1", false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Phase != PhaseIndividual {
		t.Errorf("Phase = %v, want PhaseIndividual", result.Phase)
	}
	if result.Topology != TopologySameLogin {
		t.Errorf("Topology = %v, want TopologySameLogin (the call just succeeded with the current session)", result.Topology)
	}
	if result.ClientID != "client-abc" {
		t.Errorf("ClientID = %q, want client-abc", result.ClientID)
	}
}

func TestDetect_IdentityFoundPersonalCredResolvesRoutesVerifyOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"identityUniversalAuth": map[string]string{"clientId": "client-abc"},
		})
	}))
	defer srv.Close()

	result, err := Detect(context.Background(), srv.URL, testBearer(), "ident-1", false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Phase != PhaseVerifyOnly {
		t.Errorf("Phase = %v, want PhaseVerifyOnly", result.Phase)
	}
}

func TestDetect_UnauthorizedRoutesIndividualSplitLogin(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))

		result, err := Detect(context.Background(), srv.URL, testBearer(), "ident-1", false, false)
		if err != nil {
			t.Fatalf("status %d: unexpected error: %v", status, err)
		}
		if result.Phase != PhaseIndividual {
			t.Errorf("status %d: Phase = %v, want PhaseIndividual", status, result.Phase)
		}
		if result.Topology != TopologySplitLogin {
			t.Errorf("status %d: Topology = %v, want TopologySplitLogin", status, result.Topology)
		}
		srv.Close()
	}
}

func TestDetect_AmbiguousFailureAssumesSplitLoginPrior(t *testing.T) {
	// Neither a clean 404 nor a clean 401/403 -- a malformed body is
	// the only failure shape ReadIdentity can't classify into either
	// sentinel, exercising Assumption C's "no distinguishable signal"
	// fallback.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"identityUniversalAuth": {}}`))
	}))
	defer srv.Close()

	result, err := Detect(context.Background(), srv.URL, testBearer(), "ident-1", false, false)
	if err != nil {
		t.Fatalf("unexpected hard error on an ambiguous failure shape: %v", err)
	}
	if result.Phase != PhaseIndividual || result.Topology != TopologySplitLogin {
		t.Errorf("got Phase=%v Topology=%v, want the split-login prior (PhaseIndividual/TopologySplitLogin)", result.Phase, result.Topology)
	}
}

func TestDetect_TransportFailureAssumesSplitLoginPrior(t *testing.T) {
	// A genuine transport failure (server unreachable) is a different
	// unclassifiable shape than the malformed-body case above -- both
	// must fall back to the same split-login prior per Assumption C's
	// "treats any failure as split-login" language, not just the
	// malformed-JSON case.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unreachableURL := srv.URL
	srv.Close() // now genuinely unreachable -- connection refused

	result, err := Detect(context.Background(), unreachableURL, testBearer(), "ident-1", false, false)
	if err != nil {
		t.Fatalf("unexpected hard error on a transport failure: %v", err)
	}
	if result.Phase != PhaseIndividual || result.Topology != TopologySplitLogin {
		t.Errorf("got Phase=%v Topology=%v, want the split-login prior (PhaseIndividual/TopologySplitLogin)", result.Phase, result.Topology)
	}
}

func TestConfirmSetup_AcceptsInferred(t *testing.T) {
	confirm := func(prompt string, defaultYes bool) (bool, error) { return true, nil }
	got, err := ConfirmSetup(PhaseTeam, confirm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != PhaseTeam {
		t.Errorf("got %v, want PhaseTeam accepted", got)
	}
}

func TestConfirmSetup_OverridesToOther(t *testing.T) {
	confirm := func(prompt string, defaultYes bool) (bool, error) { return false, nil }
	got, err := ConfirmSetup(PhaseTeam, confirm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != PhaseIndividual {
		t.Errorf("got %v, want override to PhaseIndividual", got)
	}
}

func TestConfirmSetup_RejectsVerifyOnly(t *testing.T) {
	confirm := func(prompt string, defaultYes bool) (bool, error) { return true, nil }
	if _, err := ConfirmSetup(PhaseVerifyOnly, confirm); err == nil {
		t.Fatal("expected an error: PhaseVerifyOnly is not a confirmable setup choice")
	}
}

func TestConfirmTopology_AcceptsInferred(t *testing.T) {
	confirm := func(prompt string, defaultYes bool) (bool, error) { return true, nil }
	got, err := ConfirmTopology(TopologySplitLogin, confirm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != TopologySplitLogin {
		t.Errorf("got %v, want TopologySplitLogin accepted", got)
	}
}

func TestConfirmTopology_OverridesToOther(t *testing.T) {
	confirm := func(prompt string, defaultYes bool) (bool, error) { return false, nil }
	got, err := ConfirmTopology(TopologySameLogin, confirm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != TopologySplitLogin {
		t.Errorf("got %v, want override to TopologySplitLogin", got)
	}
}

func TestConfirmTopology_PromptNamesSplitLoginExplicitly(t *testing.T) {
	var seen string
	confirm := func(prompt string, defaultYes bool) (bool, error) {
		seen = prompt
		return true, nil
	}
	if _, err := ConfirmTopology(TopologySplitLogin, confirm); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(seen, "split-login") || !strings.Contains(seen, "doesn't yet reach the team vault's org") {
		t.Fatalf("prompt %q does not name split-login and explain why, per Decision 3's stated prompt text", seen)
	}
}
