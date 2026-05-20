package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// teamMarkers builds a representative MarkerSet for typed-error
// construction in tests. Mirrors TeamConfigMarkerSet so the rendered
// Error() strings match what production would emit.
func teamMarkers() config.MarkerSet {
	return config.MarkerSet{
		Rank1Dir:  config.ConfigDir,
		Rank1File: config.ConfigFile,
		Rank2Path: config.ConfigFile,
	}
}

// PRD R10 substring asserted by the 401/403 arm.
const r10Substring = "verify GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo scope"

// PRD R11 substrings asserted by the 404 arm.
var r11Substrings = []string{
	"verify the slug is correct (org/repo)",
	"if the repo is private, set GH_TOKEN",
	"if the repo is brand new and has no commits yet",
}

// concatDetailSuggestion joins Detail and Suggestion for substring
// assertions. The arms intentionally split the wording across both
// fields so the formatter can render them on separate lines, but the
// PRD substrings can appear in either.
func concatDetailSuggestion(c *workspace.InitConflictError) string {
	if c == nil {
		return ""
	}
	return c.Detail + " " + c.Suggestion
}

func TestClassifyMaterializeError_Ambiguous(t *testing.T) {
	markers := teamMarkers()
	ambig := &config.AmbiguousMarkersError{
		Found:   markers,
		Markers: markers,
	}

	conflict, rest := classifyMaterializeError(ambig, false)
	if rest != nil {
		t.Fatalf("expected nil rest, got %v", rest)
	}
	if conflict == nil {
		t.Fatal("expected non-nil InitConflictError")
	}
	if conflict.Detail != ambig.Error() {
		t.Errorf("Detail mismatch:\n got: %q\nwant: %q", conflict.Detail, ambig.Error())
	}
	if conflict.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", conflict.ExitCode)
	}
}

func TestClassifyMaterializeError_NoMarker_WithoutBootstrap(t *testing.T) {
	markers := teamMarkers()
	noMarker := &config.NoMarkerError{Markers: markers}

	conflict, rest := classifyMaterializeError(noMarker, false)
	if rest != nil {
		t.Fatalf("expected nil rest, got %v", rest)
	}
	if conflict == nil {
		t.Fatal("expected non-nil InitConflictError")
	}
	if conflict.ExitCode != 4 {
		t.Errorf("ExitCode = %d, want 4", conflict.ExitCode)
	}
	combined := concatDetailSuggestion(conflict)
	if !strings.Contains(combined, "--bootstrap") {
		t.Errorf("Detail+Suggestion missing --bootstrap retry hint:\n%s", combined)
	}
	// The Detail should still surface the underlying NoMarker text.
	if conflict.Detail != noMarker.Error() {
		t.Errorf("Detail mismatch:\n got: %q\nwant: %q", conflict.Detail, noMarker.Error())
	}
}

func TestClassifyMaterializeError_NoMarker_WithBootstrap(t *testing.T) {
	markers := teamMarkers()
	noMarker := &config.NoMarkerError{Markers: markers}

	conflict, rest := classifyMaterializeError(noMarker, true)
	if conflict != nil {
		t.Fatalf("expected nil conflict for hasBootstrap=true, got %v", conflict)
	}
	if rest != nil {
		t.Fatalf("expected nil rest for hasBootstrap=true, got %v", rest)
	}
}

func TestClassifyMaterializeError_Unauthorized(t *testing.T) {
	for _, status := range []int{401, 403} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			statusErr := &github.StatusError{
				StatusCode: status,
				Message:    fmt.Sprintf("github: FetchTarball returned %d (verify GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo scope)", status),
			}

			conflict, rest := classifyMaterializeError(statusErr, false)
			if rest != nil {
				t.Fatalf("expected nil rest, got %v", rest)
			}
			if conflict == nil {
				t.Fatal("expected non-nil InitConflictError")
			}
			if conflict.ExitCode != 1 {
				t.Errorf("ExitCode = %d, want 1", conflict.ExitCode)
			}
			combined := concatDetailSuggestion(conflict)
			if !strings.Contains(combined, r10Substring) {
				t.Errorf("Detail+Suggestion missing R10 substring %q:\n%s", r10Substring, combined)
			}
		})
	}
}

func TestClassifyMaterializeError_NotFound(t *testing.T) {
	statusErr := &github.StatusError{
		StatusCode: 404,
		Message:    "github: FetchTarball returned 404",
	}

	conflict, rest := classifyMaterializeError(statusErr, false)
	if rest != nil {
		t.Fatalf("expected nil rest, got %v", rest)
	}
	if conflict == nil {
		t.Fatal("expected non-nil InitConflictError")
	}
	if conflict.ExitCode != 1 {
		t.Errorf("ExitCode = %d, want 1", conflict.ExitCode)
	}
	combined := concatDetailSuggestion(conflict)
	for _, want := range r11Substrings {
		if !strings.Contains(combined, want) {
			t.Errorf("Detail+Suggestion missing R11 substring %q:\n%s", want, combined)
		}
	}
}

func TestClassifyMaterializeError_FallThrough(t *testing.T) {
	generic := errors.New("disk is on fire")

	conflict, rest := classifyMaterializeError(generic, false)
	if conflict != nil {
		t.Fatalf("expected nil conflict for generic error, got %v", conflict)
	}
	if !errors.Is(rest, generic) {
		t.Errorf("expected fall-through to return original error, got %v", rest)
	}
}

func TestClassifyMaterializeError_NilError(t *testing.T) {
	conflict, rest := classifyMaterializeError(nil, false)
	if conflict != nil {
		t.Errorf("expected nil conflict on nil input, got %v", conflict)
	}
	if rest != nil {
		t.Errorf("expected nil rest on nil input, got %v", rest)
	}
}

// wrap is a tiny helper that produces an error chain wrapping a typed
// inner error. The classifier walks the chain via errors.As, so the
// outer wrap should not change which arm fires.
func wrap(outerMsg string, inner error) error {
	return fmt.Errorf("%s: %w", outerMsg, inner)
}

// TestClassifyMaterializeError_Precedence exercises chains satisfying
// multiple arms simultaneously. The classifier's sequential errors.As
// calls define a strict order; this table asserts the order matches
// PRD N2.
func TestClassifyMaterializeError_Precedence(t *testing.T) {
	markers := teamMarkers()

	ambig := func() *config.AmbiguousMarkersError {
		return &config.AmbiguousMarkersError{Found: markers, Markers: markers}
	}
	noMarker := func() *config.NoMarkerError {
		return &config.NoMarkerError{Markers: markers}
	}
	status := func(code int) *github.StatusError {
		return &github.StatusError{StatusCode: code, Message: fmt.Sprintf("github: FetchTarball returned %d", code)}
	}

	cases := []struct {
		name         string
		err          error
		hasBootstrap bool
		// Exactly one of these expectations should be set.
		wantArm string // one of: "ambiguous", "no-marker", "401", "404", "fall-through", "nil"
	}{
		{
			name:    "ambiguous_wrapping_404_picks_ambiguous",
			err:     wrap("outer", &wrappedError{outer: ambig(), inner: status(404)}),
			wantArm: "ambiguous",
		},
		{
			name:    "no-marker_wrapping_401_picks_no-marker",
			err:     &wrappedError{outer: noMarker(), inner: status(401)},
			wantArm: "no-marker",
		},
		{
			name:    "no-marker_wrapping_ambiguous_picks_ambiguous",
			err:     &wrappedError{outer: noMarker(), inner: ambig()},
			wantArm: "ambiguous",
		},
		{
			name:    "401_wrapping_no-marker_picks_no-marker",
			err:     &wrappedError{outer: status(401), inner: noMarker()},
			wantArm: "no-marker",
		},
		{
			name:    "404_wrapping_401_picks_401",
			err:     &wrappedError{outer: status(404), inner: status(401)},
			wantArm: "401",
		},
		{
			name:    "404_wrapping_generic_picks_404",
			err:     wrap("outer", status(404)),
			wantArm: "404",
		},
		{
			name:    "bare_401_picks_401",
			err:     status(401),
			wantArm: "401",
		},
		{
			name:    "bare_404_picks_404",
			err:     status(404),
			wantArm: "404",
		},
		{
			name:    "generic_wrap_with_no_typed_inner_falls_through",
			err:     wrap("outer", errors.New("plain")),
			wantArm: "fall-through",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conflict, rest := classifyMaterializeError(tc.err, tc.hasBootstrap)
			switch tc.wantArm {
			case "ambiguous":
				if conflict == nil || rest != nil {
					t.Fatalf("expected ambiguous arm, got conflict=%v rest=%v", conflict, rest)
				}
				// Detail must equal a *config.AmbiguousMarkersError.Error()
				// rendering (the precedence test wraps the same template
				// so the substring check is sufficient).
				if !strings.Contains(conflict.Detail, "ambiguous niwa config") {
					t.Errorf("Detail does not look like AmbiguousMarkersError text: %q", conflict.Detail)
				}
			case "no-marker":
				if conflict == nil || rest != nil {
					t.Fatalf("expected no-marker arm, got conflict=%v rest=%v", conflict, rest)
				}
				if conflict.ExitCode != 4 {
					t.Errorf("expected ExitCode=4 for no-marker without bootstrap, got %d", conflict.ExitCode)
				}
				if !strings.Contains(concatDetailSuggestion(conflict), "--bootstrap") {
					t.Errorf("no-marker arm missing --bootstrap hint:\n%s", concatDetailSuggestion(conflict))
				}
			case "401":
				if conflict == nil || rest != nil {
					t.Fatalf("expected 401 arm, got conflict=%v rest=%v", conflict, rest)
				}
				if !strings.Contains(concatDetailSuggestion(conflict), r10Substring) {
					t.Errorf("401 arm missing R10 substring:\n%s", concatDetailSuggestion(conflict))
				}
			case "404":
				if conflict == nil || rest != nil {
					t.Fatalf("expected 404 arm, got conflict=%v rest=%v", conflict, rest)
				}
				combined := concatDetailSuggestion(conflict)
				for _, want := range r11Substrings {
					if !strings.Contains(combined, want) {
						t.Errorf("404 arm missing R11 substring %q:\n%s", want, combined)
					}
				}
			case "fall-through":
				if conflict != nil {
					t.Fatalf("expected fall-through (nil conflict), got %v", conflict)
				}
				if rest == nil {
					t.Fatal("expected rest to carry original error")
				}
			default:
				t.Fatalf("unknown wantArm %q", tc.wantArm)
			}
		})
	}
}

// wrappedError lets tests construct an error whose chain visits two
// typed errors in a specific outer/inner order, so errors.As can reach
// both. Using fmt.Errorf("%w", ...) only supports one wrap target per
// call; this helper threads a second error into the chain.
type wrappedError struct {
	outer error
	inner error
}

func (w *wrappedError) Error() string {
	if w.outer == nil {
		return w.inner.Error()
	}
	if w.inner == nil {
		return w.outer.Error()
	}
	return w.outer.Error() + ": " + w.inner.Error()
}

// Unwrap returns the outer error first (so errors.As reaches it before
// the inner), and the inner second. errors.As walks the slice in order,
// which lets us assert "outer beats inner" precedence semantics.
func (w *wrappedError) Unwrap() []error {
	out := make([]error, 0, 2)
	if w.outer != nil {
		out = append(out, w.outer)
	}
	if w.inner != nil {
		out = append(out, w.inner)
	}
	return out
}
