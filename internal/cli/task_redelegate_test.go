package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadBodyOverrides_Empty(t *testing.T) {
	got, err := loadBodyOverrides("")
	if err != nil {
		t.Fatalf("loadBodyOverrides(\"\"): %v", err)
	}
	if got != nil {
		t.Errorf("empty raw should yield nil; got %v", got)
	}
}

func TestLoadBodyOverrides_InlineJSON(t *testing.T) {
	got, err := loadBodyOverrides(`{"kind":"retry","level":2}`)
	if err != nil {
		t.Fatalf("loadBodyOverrides: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(got), got)
	}
	if string(got["kind"]) != `"retry"` {
		t.Errorf("kind = %s, want \"retry\"", got["kind"])
	}
}

func TestLoadBodyOverrides_FileRef(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "body.json")
	if err := os.WriteFile(path, []byte(`{"kind":"file"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadBodyOverrides("@" + path)
	if err != nil {
		t.Fatalf("loadBodyOverrides @path: %v", err)
	}
	if string(got["kind"]) != `"file"` {
		t.Errorf("kind = %s, want \"file\"", got["kind"])
	}
}

func TestLoadBodyOverrides_FileMissing(t *testing.T) {
	_, err := loadBodyOverrides("@/no/such/file")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "reading file") {
		t.Errorf("error should mention file: %v", err)
	}
}

func TestLoadBodyOverrides_InvalidJSON(t *testing.T) {
	_, err := loadBodyOverrides(`{not valid`)
	if err == nil {
		t.Fatal("expected JSON parse error")
	}
	if !strings.Contains(err.Error(), "parsing JSON") {
		t.Errorf("error should mention parsing: %v", err)
	}
}

// TestTaskRedelegateCmd_Wired verifies the subcommand is registered under
// `niwa task` so `niwa task redelegate` is reachable from the root.
func TestTaskRedelegateCmd_Wired(t *testing.T) {
	found := false
	for _, c := range taskCmd.Commands() {
		if c.Name() == "redelegate" {
			found = true
			break
		}
	}
	if !found {
		var names []string
		for _, c := range taskCmd.Commands() {
			names = append(names, c.Name())
		}
		t.Errorf("redelegate not wired under taskCmd; found: %v", names)
	}
}

// TestTaskRedelegateCmd_FlagSet verifies all documented flags are
// registered with the expected types and defaults.
func TestTaskRedelegateCmd_FlagSet(t *testing.T) {
	cases := []struct {
		flag    string
		typeStr string
	}{
		{"to", "string"},
		{"session-id", "string"},
		{"read-only", "bool"},
		{"mode", "string"},
		{"expires-at", "string"},
		{"body-overrides", "string"},
	}
	for _, c := range cases {
		f := taskRedelegateCmd.Flags().Lookup(c.flag)
		if f == nil {
			t.Errorf("flag --%s not registered", c.flag)
			continue
		}
		if !reflect.DeepEqual(f.Value.Type(), c.typeStr) {
			t.Errorf("flag --%s type = %q, want %q", c.flag, f.Value.Type(), c.typeStr)
		}
	}
	// mode default is "async".
	if got := taskRedelegateCmd.Flags().Lookup("mode").DefValue; got != "async" {
		t.Errorf("--mode default = %q, want async", got)
	}
}
