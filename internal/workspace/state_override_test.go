package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestConfigNameOverride_RoundTrip verifies a non-empty override survives
// SaveState / LoadState exactly.
func TestConfigNameOverride_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configName := "upstream"
	state := &InstanceState{
		SchemaVersion:      SchemaVersion,
		ConfigName:         &configName,
		InstanceName:       "upstream",
		Root:               dir,
		Created:            time.Now(),
		LastApplied:        time.Now(),
		ConfigNameOverride: "my-name",
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded.ConfigNameOverride != "my-name" {
		t.Fatalf("ConfigNameOverride: got %q, want %q", loaded.ConfigNameOverride, "my-name")
	}
}

// TestConfigNameOverride_OmitemptyAbsentFromZeroValue verifies the JSON
// output omits the field when the override is empty (zero value).
func TestConfigNameOverride_OmitemptyAbsentFromZeroValue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configName := "upstream"
	state := &InstanceState{
		SchemaVersion: SchemaVersion,
		ConfigName:    &configName,
		InstanceName:  "upstream",
		Root:          dir,
		Created:       time.Now(),
		LastApplied:   time.Now(),
		// ConfigNameOverride intentionally left as zero value.
	}
	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, StateDir, StateFile))
	if err != nil {
		t.Fatalf("read state file: %v", err)
	}
	if strings.Contains(string(raw), "config_name_override") {
		t.Fatalf("state JSON contains config_name_override key when override is empty:\n%s", raw)
	}
}

// TestConfigNameOverride_OldStateFileLoadsClean verifies state files written
// before this field existed (no config_name_override key) load cleanly with
// the field decoded as the empty string.
func TestConfigNameOverride_OldStateFileLoadsClean(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	niwaDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// Hand-craft a v3 state file without the new field. Use only fields
	// the existing decoder accepts; the new field must default to "".
	configName := "upstream"
	old := map[string]any{
		"schema_version": SchemaVersion,
		"config_name":    &configName,
		"instance_name":  "upstream",
		"root":           dir,
		"created":        time.Now().Format(time.RFC3339Nano),
		"last_applied":   time.Now().Format(time.RFC3339Nano),
		"managed_files":  []any{},
		"repos":          map[string]any{},
	}
	body, err := json.Marshal(old)
	if err != nil {
		t.Fatalf("marshal old state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, StateFile), body, 0o644); err != nil {
		t.Fatalf("write old state: %v", err)
	}

	loaded, err := LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState old file: %v", err)
	}
	if loaded.ConfigNameOverride != "" {
		t.Fatalf("ConfigNameOverride from old state: got %q, want empty", loaded.ConfigNameOverride)
	}
}
