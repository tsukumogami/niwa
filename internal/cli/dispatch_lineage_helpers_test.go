package cli

import (
	"encoding/json"
	"testing"
)

// withoutLineageSettings returns a launch's pass-through with the lineage
// contributor's "env" key taken out of its settings document, and the
// document dropped altogether when nothing else was in it. Every Claude
// dispatch now carries that key, so the tests that predate it compare what
// they always compared -- their own contributor's key, and nothing else --
// while the lineage tests check the key itself. It fails the test when the
// document isn't JSON.
func withoutLineageSettings(t *testing.T, pass []string) []string {
	t.Helper()
	out, err := stripLineageSettings(pass)
	if err != nil {
		t.Fatalf("launch settings document is not JSON: %v", err)
	}
	return out
}

// withoutLineageSettingsQuiet is withoutLineageSettings for a capture that has
// no *testing.T in scope. A document that isn't JSON is returned unchanged, so
// the caller's own assertion reports it.
func withoutLineageSettingsQuiet(pass []string) []string {
	out, err := stripLineageSettings(pass)
	if err != nil {
		return pass
	}
	return out
}

func stripLineageSettings(pass []string) ([]string, error) {
	out := make([]string, 0, len(pass))
	for i := 0; i < len(pass); i++ {
		if pass[i] != "--settings" || i+1 >= len(pass) {
			out = append(out, pass[i])
			continue
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal([]byte(pass[i+1]), &doc); err != nil {
			return nil, err
		}
		delete(doc, "env")
		i++
		if len(doc) == 0 {
			continue
		}
		rendered, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		out = append(out, "--settings", string(rendered))
	}
	return out, nil
}
