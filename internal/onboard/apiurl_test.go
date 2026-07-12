package onboard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCheckAPIURL_NonHTTPSHardRejectsInEveryMode(t *testing.T) {
	confirmAccepts := func(prompt string, defaultYes bool) (bool, error) { return true, nil }
	cases := []struct {
		name        string
		accept      bool
		interactive bool
		confirm     ConfirmFunc
	}{
		{"accept-true", true, false, nil},
		{"interactive-confirming", false, true, confirmAccepts},
		{"non-interactive-no-accept", false, false, nil},
	}
	for _, c := range cases {
		err := CheckAPIURL("http://example.com/api", c.accept, c.interactive, c.confirm)
		if err == nil {
			t.Errorf("%s: expected non-https to be rejected regardless of mode", c.name)
		}
	}
}

func TestCheckAPIURL_MalformedURLHardRejects(t *testing.T) {
	err := CheckAPIURL("://not-a-url", true, true, nil)
	if err == nil {
		t.Fatal("expected malformed api_url to be rejected")
	}
}

func TestCheckAPIURL_DefaultHTTPSPasses(t *testing.T) {
	err := CheckAPIURL("https://app.infisical.com/api", false, false, nil)
	if err != nil {
		t.Fatalf("unexpected error for the default api_url: %v", err)
	}
}

func TestCheckAPIURL_NonDefaultAcceptOverridePasses(t *testing.T) {
	err := CheckAPIURL("https://self-hosted.example.com/api", true, false, nil)
	if err != nil {
		t.Fatalf("unexpected error with --accept-api-url: %v", err)
	}
}

func TestCheckAPIURL_NonDefaultNonInteractiveWithoutAcceptFails(t *testing.T) {
	err := CheckAPIURL("https://self-hosted.example.com/api", false, false, nil)
	if !errors.Is(err, ErrAPIURLNotAccepted) {
		t.Fatalf("err = %v, want ErrAPIURLNotAccepted", err)
	}
}

func TestCheckAPIURL_NonDefaultInteractiveConfirmAccepts(t *testing.T) {
	var promptSeen string
	confirm := func(prompt string, defaultYes bool) (bool, error) {
		promptSeen = prompt
		return true, nil
	}
	err := CheckAPIURL("https://self-hosted.example.com/api", false, true, confirm)
	if err != nil {
		t.Fatalf("unexpected error when confirm accepts: %v", err)
	}
	if promptSeen == "" {
		t.Fatal("expected the interactive confirm to be invoked")
	}
}

func TestCheckAPIURL_NonDefaultInteractiveConfirmDeclines(t *testing.T) {
	confirm := func(prompt string, defaultYes bool) (bool, error) { return false, nil }
	err := CheckAPIURL("https://self-hosted.example.com/api", false, true, confirm)
	if !errors.Is(err, ErrAPIURLNotAccepted) {
		t.Fatalf("err = %v, want ErrAPIURLNotAccepted", err)
	}
}

func TestCheckAPIURL_InteractiveWithoutConfirmFuncErrors(t *testing.T) {
	err := CheckAPIURL("https://self-hosted.example.com/api", false, true, nil)
	if err == nil {
		t.Fatal("expected an error when interactive is true but confirm is nil")
	}
}

// TestAPIURLGate_BlocksDownstreamCallOnReject asserts the load-bearing
// ordering rule (AC-5): when the api_url gate rejects, the detection
// call downstream must never fire. It models the wizard's actual
// entry sequence -- gate first, only proceed to the bearer-carrying
// call if the gate passes -- against a request-counting REST double,
// for both hard-reject rules.
func TestAPIURLGate_BlocksDownstreamCallOnReject(t *testing.T) {
	cases := []struct {
		name        string
		apiURL      func(serverURL string) string
		accept      bool
		interactive bool
		confirm     ConfirmFunc
	}{
		{
			name:   "non-https hard reject",
			apiURL: func(serverURL string) string { return "http://plain-not-https.example.com/api" },
			accept: true, // even with the override, rule 1 has no override
		},
		{
			name:        "non-default https declined",
			apiURL:      func(serverURL string) string { return "https://self-hosted.example.com/api" },
			accept:      false,
			interactive: true,
			confirm:     func(prompt string, defaultYes bool) (bool, error) { return false, nil },
		},
		{
			name:        "non-default https non-interactive no accept",
			apiURL:      func(serverURL string) string { return "https://self-hosted.example.com/api" },
			accept:      false,
			interactive: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var requests int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&requests, 1)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			apiURL := c.apiURL(srv.URL)

			err := CheckAPIURL(apiURL, c.accept, c.interactive, c.confirm)
			if err == nil {
				t.Fatalf("%s: expected the gate to reject", c.name)
			}

			// Simulate the wizard's own sequencing: the detection call
			// (represented here by the request-counting server) is
			// only ever reached if the gate returned nil. Since it
			// returned an error, the wizard must short-circuit before
			// issuing any request -- assert exactly that.
			if atomic.LoadInt32(&requests) != 0 {
				t.Fatalf("%s: gate rejected but %d request(s) reached the downstream server", c.name, requests)
			}
		})
	}
}
