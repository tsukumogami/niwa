package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeTestJSON(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestMirrorMCPApprovalsToLocal(t *testing.T) {
	t.Run("creates local file when absent", func(t *testing.T) {
		root := t.TempDir()
		writeTestJSON(t, filepath.Join(root, ".claude", "settings.json"), `{"enabledMcpjsonServers":["datadog"]}`)

		if err := mirrorMCPApprovalsToLocal(root); err != nil {
			t.Fatal(err)
		}
		got := readTestJSON(t, filepath.Join(root, ".claude", "settings.local.json"))
		if !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"datadog"}) {
			t.Fatalf("enabledMcpjsonServers = %v", got["enabledMcpjsonServers"])
		}
	})

	t.Run("merges and preserves other keys and existing names", func(t *testing.T) {
		root := t.TempDir()
		writeTestJSON(t, filepath.Join(root, ".claude", "settings.json"), `{"enabledMcpjsonServers":["datadog","b"]}`)
		writeTestJSON(t, filepath.Join(root, ".claude", "settings.local.json"),
			`{"enabledPlugins":{"x@y":true},"enabledMcpjsonServers":["manual"]}`)

		if err := mirrorMCPApprovalsToLocal(root); err != nil {
			t.Fatal(err)
		}
		got := readTestJSON(t, filepath.Join(root, ".claude", "settings.local.json"))
		if !reflect.DeepEqual(got["enabledMcpjsonServers"], []any{"b", "datadog", "manual"}) {
			t.Fatalf("enabledMcpjsonServers = %v", got["enabledMcpjsonServers"])
		}
		if !reflect.DeepEqual(got["enabledPlugins"], map[string]any{"x@y": true}) {
			t.Fatalf("enabledPlugins = %v", got["enabledPlugins"])
		}
	})

	t.Run("no approvals writes nothing", func(t *testing.T) {
		root := t.TempDir()
		writeTestJSON(t, filepath.Join(root, ".claude", "settings.json"), `{}`)

		if err := mirrorMCPApprovalsToLocal(root); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, ".claude", "settings.local.json")); !os.IsNotExist(err) {
			t.Fatalf("settings.local.json should not exist, stat err = %v", err)
		}
	})

	t.Run("unparseable local file is left alone", func(t *testing.T) {
		root := t.TempDir()
		writeTestJSON(t, filepath.Join(root, ".claude", "settings.json"), `{"enabledMcpjsonServers":["datadog"]}`)
		local := filepath.Join(root, ".claude", "settings.local.json")
		writeTestJSON(t, local, `{not json`)

		if err := mirrorMCPApprovalsToLocal(root); err == nil {
			t.Fatal("expected an error for an unparseable settings.local.json")
		}
		data, _ := os.ReadFile(local)
		if string(data) != `{not json` {
			t.Fatalf("settings.local.json was modified: %q", data)
		}
	})
}
