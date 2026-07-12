package onboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
)

// teamTestServer is a small httptest double covering exactly the
// three endpoints the team runner may call: read-identity, project
// membership, and (indirectly, via a request counter only -- the
// runner never calls these) mint/revoke client-secrets. Kept local to
// this test file rather than reusing test/functional's
// infisicalFakeServer, whose types are unexported to that package.
type teamTestServer struct {
	srv *httptest.Server

	identityPresent bool
	clientID        string
	unauthorized    bool

	membershipPresent bool
	granted           bool
	membershipUnauth  bool

	mintRevokeCalls int32
	authHeaders     []string
}

func newTeamTestServer() *teamTestServer {
	s := &teamTestServer{}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

func (s *teamTestServer) Close()      { s.srv.Close() }
func (s *teamTestServer) URL() string { return s.srv.URL }

func (s *teamTestServer) handle(w http.ResponseWriter, r *http.Request) {
	s.authHeaders = append(s.authHeaders, r.Header.Get("Authorization"))

	switch {
	case strings.Contains(r.URL.Path, "client-secrets"):
		atomic.AddInt32(&s.mintRevokeCalls, 1)
		w.WriteHeader(http.StatusOK)
	case strings.Contains(r.URL.Path, "/memberships/identities/"):
		if s.membershipUnauth {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !s.membershipPresent {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		roles := []map[string]string{}
		if s.granted {
			roles = append(roles, map[string]string{"role": "member"})
		}
		json.NewEncoder(w).Encode(map[string]any{
			"identityMembership": map[string]any{"roles": roles},
		})
	case strings.Contains(r.URL.Path, "/identities/"):
		if s.unauthorized {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !s.identityPresent {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"identityUniversalAuth": map[string]string{"clientId": s.clientID},
		})
	default:
		http.NotFound(w, r)
	}
}

func teamTestBearer() secret.Value {
	return secret.New([]byte("operator-bearer-value"), secret.Origin{})
}

// withCreateFolder swaps the createFolder seam for the duration of fn,
// restoring the original afterward. Tests must not run in parallel
// with each other (they don't; t.Parallel is never called here).
func withCreateFolder(t *testing.T, fn func(ctx context.Context, projectID, env, path string) error, body func()) {
	t.Helper()
	orig := createFolder
	createFolder = fn
	defer func() { createFolder = orig }()
	body()
}

func baseTeamOptions(apiURL string) TeamOptions {
	return TeamOptions{
		APIURL:          apiURL,
		Bearer:          teamTestBearer(),
		ProjectID:       "proj-1",
		IdentityID:      "ident-1",
		IdentityName:    "ci-bot",
		AuthMethod:      "Universal Auth",
		EnvironmentSlug: "dev",
		SecretPath:      "/team",
		In:              strings.NewReader(""),
		Out:             &strings.Builder{},
	}
}

// TestRunTeam_HappyPath_AC8_AC10_AC12 drives the full team setup with
// everything already landed on the world-state probes (folder create
// succeeds, identity exposes a client_id, membership is granted) --
// proving the folder-create CLI delegation fires (AC-8), zero
// mint/revoke management calls are ever made on the team path (AC-10),
// and every REST call carries the operator's own bearer, nothing else
// (AC-12).
func TestRunTeam_HappyPath_AC8_AC10_AC12(t *testing.T) {
	srv := newTeamTestServer()
	defer srv.Close()
	srv.identityPresent = true
	srv.clientID = "client-abc"
	srv.membershipPresent = true
	srv.granted = true

	var folderCalls int32
	var capturedArgs []string
	withCreateFolder(t, func(ctx context.Context, projectID, env, path string) error {
		atomic.AddInt32(&folderCalls, 1)
		capturedArgs = []string{projectID, env, path}
		return nil
	}, func() {
		result, err := RunTeam(context.Background(), baseTeamOptions(srv.URL()))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ClientID != "client-abc" {
			t.Errorf("ClientID = %q, want client-abc", result.ClientID)
		}
	})

	if folderCalls == 0 {
		t.Error("AC-8: createFolder was never invoked")
	}
	wantArgs := []string{"proj-1", "dev", "/team"}
	if len(capturedArgs) != 3 || capturedArgs[0] != wantArgs[0] || capturedArgs[1] != wantArgs[1] || capturedArgs[2] != wantArgs[2] {
		t.Errorf("createFolder args = %v, want %v", capturedArgs, wantArgs)
	}

	if got := atomic.LoadInt32(&srv.mintRevokeCalls); got != 0 {
		t.Errorf("AC-10: mint/revoke calls = %d, want 0", got)
	}
	for _, h := range srv.authHeaders {
		if h != "Bearer operator-bearer-value" {
			t.Errorf("AC-12: Authorization header = %q, want the operator's own bearer only", h)
		}
	}
	if len(srv.authHeaders) == 0 {
		t.Error("expected at least one recorded request")
	}
}

// TestRunTeam_IdentityNotYetCreated_AC9_AC9b proves the guided
// identity/UA instruction names the required tokens (identity name,
// auth method) and that a failed landing check re-surfaces the
// instruction, never advancing, until ReadIdentity succeeds.
func TestRunTeam_IdentityNotYetCreated_AC9_AC9b(t *testing.T) {
	srv := newTeamTestServer()
	defer srv.Close()
	// identityPresent starts false: every landing check fails until
	// the test flips it after the second Pause.

	var out strings.Builder
	pauseCount := 0
	// A reader providing two lines: the operator "acts" between the
	// first and second Pause, and identityPresent flips right before
	// the SECOND read so the first landing check demonstrably fails
	// and re-surfaces (AC-9b) before the second one lands (AC-9).
	in := &flippingReader{
		lines: []string{"\n", "\n"},
		onRead: func(n int) {
			if n == 2 {
				srv.identityPresent = true
				srv.clientID = "client-xyz"
			}
			pauseCount = n
		},
	}

	opts := baseTeamOptions(srv.URL())
	opts.In = in
	opts.Out = &out

	withCreateFolder(t, func(ctx context.Context, projectID, env, path string) error {
		return nil
	}, func() {
		srv.membershipPresent = true
		srv.granted = true
		result, err := RunTeam(context.Background(), opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.ClientID != "client-xyz" {
			t.Errorf("ClientID = %q, want client-xyz", result.ClientID)
		}
	})

	if pauseCount < 2 {
		t.Fatalf("AC-9b: expected the landing check to fail once and re-surface, pauseCount = %d", pauseCount)
	}
	printed := out.String()
	if !strings.Contains(printed, "ci-bot") {
		t.Errorf("AC-9: guided instruction missing identity name token, got: %s", printed)
	}
	if !strings.Contains(printed, "Universal Auth") {
		t.Errorf("AC-9: guided instruction missing auth method token, got: %s", printed)
	}
}

// flippingReader feeds lines one at a time, invoking onRead(index)
// after each line is consumed -- lets a test simulate "the operator
// did the manual step between this Pause and the next."
type flippingReader struct {
	lines  []string
	idx    int
	onRead func(n int)
}

func (f *flippingReader) Read(p []byte) (int, error) {
	if f.idx >= len(f.lines) {
		return 0, errEOFForTest
	}
	line := f.lines[f.idx]
	f.idx++
	n := copy(p, line)
	if f.onRead != nil {
		f.onRead(f.idx)
	}
	return n, nil
}

var errEOFForTest = errors.New("flippingReader: no more lines")

// TestRunTeam_GrantNotYetPresent_AC9 proves the guided grant
// instruction names the environment slug token and blocks until
// ReadProjectMembership reports granted.
func TestRunTeam_GrantNotYetPresent_AC9(t *testing.T) {
	srv := newTeamTestServer()
	defer srv.Close()
	srv.identityPresent = true
	srv.clientID = "client-abc"
	// membershipPresent starts false.

	var out strings.Builder
	in := &flippingReader{
		lines: []string{"\n"},
		onRead: func(n int) {
			srv.membershipPresent = true
			srv.granted = true
		},
	}

	opts := baseTeamOptions(srv.URL())
	opts.In = in
	opts.Out = &out

	withCreateFolder(t, func(ctx context.Context, projectID, env, path string) error {
		return nil
	}, func() {
		if _, err := RunTeam(context.Background(), opts); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	printed := out.String()
	if !strings.Contains(printed, "dev") {
		t.Errorf("AC-9: guided grant instruction missing environment slug token, got: %s", printed)
	}
	if !strings.Contains(printed, "ci-bot") {
		t.Errorf("AC-9: guided grant instruction missing identity name token, got: %s", printed)
	}
}

// TestRunTeam_PlanGatedFolder_AC11 proves a plan-gated folder-create
// degrades to guided instructions for that specific step (never a raw
// provider error) and resumes the remaining steps automatically once
// the operator confirms.
func TestRunTeam_PlanGatedFolder_AC11(t *testing.T) {
	srv := newTeamTestServer()
	defer srv.Close()
	srv.identityPresent = true
	srv.clientID = "client-abc"
	srv.membershipPresent = true
	srv.granted = true

	var out strings.Builder
	gated := true
	in := &flippingReader{
		lines: []string{"\n"},
		onRead: func(n int) {
			gated = false // operator manually created the folder
		},
	}

	opts := baseTeamOptions(srv.URL())
	opts.In = in
	opts.Out = &out

	var calls int32
	withCreateFolder(t, func(ctx context.Context, projectID, env, path string) error {
		atomic.AddInt32(&calls, 1)
		if gated {
			return infisical.ErrPlanGated
		}
		return nil
	}, func() {
		result, err := RunTeam(context.Background(), opts)
		if err != nil {
			t.Fatalf("expected plan-gate to degrade to guided instructions, not error out: %v", err)
		}
		if result.ClientID != "client-abc" {
			t.Errorf("ClientID = %q, want client-abc", result.ClientID)
		}
	})

	if calls < 2 {
		t.Fatalf("AC-11: expected createFolder to be retried after the guided pause, calls = %d", calls)
	}
	if !strings.Contains(out.String(), "plan does not allow") {
		t.Errorf("AC-11: expected a guided plan-gate instruction, got: %s", out.String())
	}
}

// TestRunTeam_PlanGatedFolder_NeverAdvancesWithoutRetry proves a
// non-plan-gated folder failure surfaces as a raw error rather than
// silently degrading -- AC-11 only exempts plan-gated failures.
func TestRunTeam_FolderHardFailureSurfacesAsError(t *testing.T) {
	srv := newTeamTestServer()
	defer srv.Close()

	opts := baseTeamOptions(srv.URL())
	withCreateFolder(t, func(ctx context.Context, projectID, env, path string) error {
		return errors.New("boom: folder create failed")
	}, func() {
		_, err := RunTeam(context.Background(), opts)
		if err == nil {
			t.Fatal("expected a raw error for a non-plan-gated folder failure")
		}
		if errors.Is(err, infisical.ErrPlanGated) {
			t.Error("a generic failure must not be treated as plan-gated")
		}
	})
}

// TestRunTeam_R21_AC35 proves the R21 sweep names the missing
// artifact distinctly, and is reported with an "R21" prefix (never
// R11's wording), for each of the three probes it composes -- identity,
// grant, and folder -- exercised directly against verifyR21 to isolate
// the R21-sweep-specific naming/prefix behavior (RunTeam's own
// ensure* loops would otherwise block forever on a real Pause waiting
// for a probe that this test deliberately never lands).
func TestRunTeam_R21_NamesMissingArtifact_AC35(t *testing.T) {
	cases := []struct {
		name         string
		setup        func(srv *teamTestServer)
		wantContains string
	}{
		{
			name: "missing identity",
			setup: func(srv *teamTestServer) {
				srv.identityPresent = false
			},
			wantContains: "client_id",
		},
		{
			name: "missing grant",
			setup: func(srv *teamTestServer) {
				srv.identityPresent = true
				srv.clientID = "client-abc"
				srv.membershipPresent = false
			},
			wantContains: "grant",
		},
		{
			name: "missing folder",
			setup: func(srv *teamTestServer) {
				srv.identityPresent = true
				srv.clientID = "client-abc"
				srv.membershipPresent = true
				srv.granted = true
			},
			wantContains: "folder",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTeamTestServer()
			defer srv.Close()
			tc.setup(srv)

			folderErr := error(nil)
			if tc.wantContains == "folder" {
				folderErr = errors.New("boom: folder create failed")
			}

			var err error
			withCreateFolder(t, func(ctx context.Context, projectID, env, path string) error {
				return folderErr
			}, func() {
				_, err = verifyR21WithProbe(srv.URL())
			})

			if err == nil {
				t.Fatal("expected R21 verification error")
			}
			var exitErr *ExitCodeError
			if !errors.As(err, &exitErr) {
				t.Fatalf("err = %v, want *ExitCodeError", err)
			}
			if exitErr.Code != ExitVerification {
				t.Errorf("Code = %d, want ExitVerification", exitErr.Code)
			}
			if !strings.HasPrefix(exitErr.Msg, "R21 ") {
				t.Errorf("Msg = %q, want an R21-prefixed message distinct from R11", exitErr.Msg)
			}
			if !strings.Contains(exitErr.Msg, tc.wantContains) {
				t.Errorf("Msg = %q, want it to name the missing %s artifact", exitErr.Msg, tc.wantContains)
			}
		})
	}
}

// verifyR21WithProbe is a thin helper isolating verifyR21 against a
// pre-seeded teamTestServer.
func verifyR21WithProbe(apiURL string) (TeamResult, error) {
	opts := TeamOptions{
		APIURL:          apiURL,
		Bearer:          teamTestBearer(),
		ProjectID:       "proj-1",
		IdentityID:      "ident-1",
		EnvironmentSlug: "dev",
		SecretPath:      "/team",
	}
	return verifyR21(context.Background(), opts)
}

// TestRunTeam_ResumeSkipsGuidedStepsWhenAlreadyLanded proves a run
// where everything is already landed on the world-state probes never
// prompts at all -- the resume/skip behavior Decision 1 requires.
func TestRunTeam_ResumeSkipsGuidedStepsWhenAlreadyLanded(t *testing.T) {
	srv := newTeamTestServer()
	defer srv.Close()
	srv.identityPresent = true
	srv.clientID = "client-abc"
	srv.membershipPresent = true
	srv.granted = true

	opts := baseTeamOptions(srv.URL())
	opts.In = &neverReadReader{t: t}

	withCreateFolder(t, func(ctx context.Context, projectID, env, path string) error {
		return nil
	}, func() {
		if _, err := RunTeam(context.Background(), opts); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

type neverReadReader struct{ t *testing.T }

func (r *neverReadReader) Read(p []byte) (int, error) {
	r.t.Fatal("Pause should never be invoked when every step is already landed")
	return 0, errEOFForTest
}
