package functional

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteInstanceStateAtVersion writes a JSON state file to
// <dir>/.niwa/instance.json with the given schema version and body.
// The body is a JSON object literal (without the schema_version key —
// this helper merges it in).
//
// Used by Gherkin steps that need to plant a v2-shaped state file and
// assert v3 lazy upgrade. The directory must already exist; this helper
// creates the .niwa/ subdirectory and writes the file.
func WriteInstanceStateAtVersion(dir string, version int, body string) error {
	stateDir := filepath.Join(dir, ".niwa")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("WriteInstanceStateAtVersion: mkdir %s: %w", stateDir, err)
	}

	// Inject schema_version into the body. The body is expected to be
	// JSON-shaped without the version (e.g., `{"instance_name":"foo"}`)
	// and gets prefixed with `"schema_version":N,`. This avoids needing
	// a real JSON parser here — Gherkin steps pass already-formatted
	// fragments.
	merged := fmt.Sprintf(`{"schema_version":%d,%s`, version, body[1:])
	path := filepath.Join(stateDir, "instance.json")
	if err := os.WriteFile(path, []byte(merged), 0o644); err != nil {
		return fmt.Errorf("WriteInstanceStateAtVersion: write %s: %w", path, err)
	}
	return nil
}
