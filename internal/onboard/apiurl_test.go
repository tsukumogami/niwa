package onboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
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
// call downstream must never fire. Each case points CheckAPIURL at the
// REST double's OWN url (not an unrelated hostname), then composes the
// wizard's intended sequence -- call Detect's ReadIdentity only if the
// gate returned nil -- against that same double. This makes the
// request-counter assertion a real regression check: if CheckAPIURL
// ever let one of these cases through with a nil error, the composed
// ReadIdentity call would actually reach the double and the counter
// would go non-zero, catching the regression rather than passing
// vacuously.
func TestAPIURLGate_BlocksDownstreamCallOnReject(t *testing.T) {
	bearer := secret.New([]byte("probe-bearer"), secret.Origin{})

	cases := []struct {
		name        string
		newServer   func(http.Handler) *httptest.Server
		accept      bool
		interactive bool
		confirm     ConfirmFunc
	}{
		{
			name:      "non-https hard reject",
			newServer: httptest.NewServer, // plain http:// -- what rule 1 rejects
			accept:    true,               // even with the override, rule 1 has no override
		},
		{
			name:        "non-default https declined",
			newServer:   httptest.NewTLSServer, // https://127.0.0.1:port -- well-formed, non-default
			accept:      false,
			interactive: true,
			confirm:     func(prompt string, defaultYes bool) (bool, error) { return false, nil },
		},
		{
			name:        "non-default https non-interactive no accept",
			newServer:   httptest.NewTLSServer,
			accept:      false,
			interactive: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var requests int32
			srv := c.newServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&requests, 1)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			// ReadIdentity dials through http.DefaultClient, which
			// doesn't trust httptest.NewTLSServer's self-signed cert by
			// default -- without this, a regression's composed call
			// below would fail at the TLS handshake before ever
			// reaching the double's handler, and the request counter
			// would never move even when it should. Swap in the
			// server's own trusting client for the duration of this
			// subtest so the composed call can actually complete.
			prevClient := *http.DefaultClient
			*http.DefaultClient = *srv.Client()
			defer func() { *http.DefaultClient = prevClient }()

			apiURL := srv.URL

			err := CheckAPIURL(apiURL, c.accept, c.interactive, c.confirm)

			// The wizard's own sequencing: the detection call is only
			// ever reached if the gate returned nil. This must run
			// BEFORE any t.Fatal on err -- t.Fatal calls
			// runtime.Goexit, which would make this unreachable
			// regardless of outcome and silently defeat the whole
			// point of composing a real call here. err is expected to
			// be non-nil below, so in the passing case this is
			// correctly skipped; a future CheckAPIURL regression that
			// returns nil here would cause this to actually hit the
			// double, and the assertions below (both non-fatal, so
			// both always run) would catch it.
			if err == nil {
				_, _ = infisical.ReadIdentity(context.Background(), apiURL, bearer, "probe-identity")
			}

			if err == nil {
				t.Errorf("%s: expected the gate to reject", c.name)
			}
			if atomic.LoadInt32(&requests) != 0 {
				t.Errorf("%s: gate rejected but %d request(s) reached the downstream double", c.name, requests)
			}
		})
	}
}
