package mcp

import (
	"encoding/json"
	"testing"
)

// TestErrResultCodeBody_StructuredShape verifies Issue 9: errResultCodeBody
// emits a JSON object whose top-level error_code field is set from the code
// argument and whose other fields come from the body map.
func TestErrResultCodeBody_StructuredShape(t *testing.T) {
	res := errResultCodeBody("MISSING_SKILLS", map[string]any{
		"missing":   []string{"shirabe:rpd"},
		"available": []string{"shirabe:plan", "shirabe:design"},
	})
	if !res.IsError {
		t.Fatal("IsError = false, want true")
	}
	if len(res.Content) != 1 {
		t.Fatalf("Content len = %d, want 1", len(res.Content))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &got); err != nil {
		t.Fatalf("not valid JSON: %v\nbody: %s", err, res.Content[0].Text)
	}
	if got["error_code"] != "MISSING_SKILLS" {
		t.Errorf("error_code = %v, want MISSING_SKILLS", got["error_code"])
	}
	missing, _ := got["missing"].([]any)
	if len(missing) != 1 || missing[0] != "shirabe:rpd" {
		t.Errorf("missing = %v, want [shirabe:rpd]", got["missing"])
	}
}

// TestErrResultCodeBody_OverridesErrorCode verifies that an "error_code" key
// inside body is overwritten by the code argument (canonical wins).
func TestErrResultCodeBody_OverridesErrorCode(t *testing.T) {
	res := errResultCodeBody("CANONICAL", map[string]any{
		"error_code": "ATTACKER_OVERRIDE",
		"detail":     "ok",
	})
	code := errorCode(&res)
	if code != "CANONICAL" {
		t.Errorf("error_code = %q, want CANONICAL (body must not override)", code)
	}
}

// TestErrResultCodeBody_NilBody verifies the helper accepts a nil body and
// emits a minimal object with just error_code.
func TestErrResultCodeBody_NilBody(t *testing.T) {
	res := errResultCodeBody("UNKNOWN_ROLE", nil)
	code := errorCode(&res)
	if code != "UNKNOWN_ROLE" {
		t.Errorf("error_code = %q, want UNKNOWN_ROLE", code)
	}
	var got map[string]any
	_ = json.Unmarshal([]byte(res.Content[0].Text), &got)
	if len(got) != 1 {
		t.Errorf("body keys = %d, want 1 (just error_code); got: %s", len(got), res.Content[0].Text)
	}
}

// TestErrorCodeFromText_BothShapes verifies the parser recognizes both the
// legacy two-line shape and the new structured JSON shape.
func TestErrorCodeFromText_BothShapes(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"legacy two-line", "error_code: SESSION_REQUIRED\ndetail: missing session", "SESSION_REQUIRED"},
		{"structured JSON minimal", `{"error_code":"MISSING_SKILLS"}`, "MISSING_SKILLS"},
		{"structured JSON with body", `{"error_code":"MISSING_SKILLS","missing":["x"]}`, "MISSING_SKILLS"},
		{"plain text no code", "something else", ""},
		{"JSON without error_code", `{"foo":"bar"}`, ""},
		{"prose starting with brace but invalid JSON", "{not actually json error_code: CODE\n", "CODE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := errorCodeFromText(c.text); got != c.want {
				t.Errorf("errorCodeFromText(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}
