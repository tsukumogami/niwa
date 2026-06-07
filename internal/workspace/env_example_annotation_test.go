package workspace

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// TestParseDotEnvExampleInlineAnnotationValueForms verifies that the
// "# niwa: warn|fail" marker is extracted for unquoted, single-quoted, and
// double-quoted values.
func TestParseDotEnvExampleInlineAnnotationValueForms(t *testing.T) {
	cases := []struct {
		name string
		line string
		key  string
		want config.Action
	}{
		{"unquoted", `API_KEY=somevalue # niwa: fail`, "API_KEY", config.ActionFail},
		{"single-quoted", `API_KEY='some value' # niwa: warn`, "API_KEY", config.ActionWarn},
		{"double-quoted", `API_KEY="some value" # niwa: fail`, "API_KEY", config.ActionFail},
		{"double-quoted-warn", `API_KEY="x" # niwa: warn`, "API_KEY", config.ActionWarn},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeEnvExample(t, tc.line+"\n")
			_, annotations, warnings, err := parseDotEnvExample(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(warnings) != 0 {
				t.Errorf("unexpected warnings: %v", warnings)
			}
			got, ok := annotations[tc.key]
			if !ok {
				t.Fatalf("no annotation for %s; annotations=%v", tc.key, annotations)
			}
			if got != tc.want {
				t.Errorf("annotation = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestParseDotEnvExampleSpoofedMarkerInQuotedValue verifies a "# niwa:"
// sequence INSIDE a quoted value is not treated as a marker.
func TestParseDotEnvExampleSpoofedMarkerInQuotedValue(t *testing.T) {
	cases := []struct {
		name string
		line string
		key  string
	}{
		{"double-quoted", `TOKEN="value # niwa: fail"`, "TOKEN"},
		{"single-quoted", `TOKEN='value # niwa: fail'`, "TOKEN"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeEnvExample(t, tc.line+"\n")
			_, annotations, warnings, err := parseDotEnvExample(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, ok := annotations[tc.key]; ok {
				t.Errorf("spoofed marker inside quoted value was treated as an annotation: %v", annotations)
			}
			if len(warnings) != 0 {
				t.Errorf("unexpected warnings: %v", warnings)
			}
		})
	}
}

// TestParseDotEnvExampleSpoofedMarkerThenRealMarker verifies that a quoted
// value containing a spoof, followed by a real trailing marker, honors only the
// real one.
func TestParseDotEnvExampleSpoofedMarkerThenRealMarker(t *testing.T) {
	path := writeEnvExample(t, `TOKEN="value # niwa: fail" # niwa: warn`+"\n")
	_, annotations, warnings, err := parseDotEnvExample(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	got, ok := annotations["TOKEN"]
	if !ok {
		t.Fatalf("no annotation for TOKEN; annotations=%v", annotations)
	}
	if got != config.ActionWarn {
		t.Errorf("annotation = %v, want warn (the real trailing marker)", got)
	}
}

// TestParseDotEnvExampleUnknownMarker verifies an unknown marker payload warns
// naming the key only (never echoing the payload) and is ignored.
func TestParseDotEnvExampleUnknownMarker(t *testing.T) {
	const payload = "explode"
	path := writeEnvExample(t, `DANGER=value # niwa: `+payload+"\n")
	_, annotations, warnings, err := parseDotEnvExample(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := annotations["DANGER"]; ok {
		t.Errorf("unknown marker should be ignored, got annotation %v", annotations)
	}
	if len(warnings) != 1 {
		t.Fatalf("want exactly 1 warning, got %d: %v", len(warnings), warnings)
	}
	w := warnings[0]
	if !strings.Contains(w, "DANGER") {
		t.Errorf("warning %q does not name the key DANGER", w)
	}
	if strings.Contains(w, payload) {
		t.Errorf("warning %q echoes the marker payload %q", w, payload)
	}
}

// TestParseDotEnvExampleNoAnnotation verifies a line with no marker produces no
// annotation and no warning. A plain trailing comment is also not a marker.
func TestParseDotEnvExampleNoAnnotation(t *testing.T) {
	cases := []string{
		`PLAIN=value`,
		`COMMENTED=value # just a comment`,
		`QUOTED="value"`,
	}
	for _, line := range cases {
		t.Run(line, func(t *testing.T) {
			path := writeEnvExample(t, line+"\n")
			_, annotations, warnings, err := parseDotEnvExample(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(annotations) != 0 {
				t.Errorf("expected no annotations, got %v", annotations)
			}
			if len(warnings) != 0 {
				t.Errorf("expected no warnings, got %v", warnings)
			}
		})
	}
}

// TestParseDotEnvExampleAnnotationDoesNotCorruptValue verifies extracting the
// marker leaves the parsed value intact (the marker is stripped from unquoted
// values as an inline comment, and quoted values are unaffected).
func TestParseDotEnvExampleAnnotationDoesNotCorruptValue(t *testing.T) {
	path := writeEnvExample(t, `K="literal value" # niwa: fail`+"\n")
	vars, annotations, _, err := parseDotEnvExample(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vars["K"] != "literal value" {
		t.Errorf("value = %q, want %q", vars["K"], "literal value")
	}
	if annotations["K"] != config.ActionFail {
		t.Errorf("annotation = %v, want fail", annotations["K"])
	}
}
