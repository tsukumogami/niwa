package agentplan

import (
	"path/filepath"
	"testing"
)

// These pin the settings document as data: the bytes the three writers this
// producer replaces agreed on, and the per-scope decisions -- file name and
// managed-record membership -- that each of them used to make for itself.

func TestSettingsPlanMarshalsTheDocumentTheWayTheWritersDid(t *testing.T) {
	plan, err := SettingsPlan(SettingsInputs{
		Scope: SettingsInRepo,
		Dir:   filepath.FromSlash("/ws/public/app"),
		Doc:   map[string]any{"permissions": map[string]any{"defaultMode": "acceptEdits"}},
	})
	if err != nil {
		t.Fatalf("SettingsPlan: %v", err)
	}
	if len(plan.Entries) != 1 {
		t.Fatalf("plan has %d entries, want 1", len(plan.Entries))
	}

	want := "{\n  \"permissions\": {\n    \"defaultMode\": \"acceptEdits\"\n  }\n}\n"
	if got := string(plan.Entries[0].Content); got != want {
		t.Errorf("entry content = %q, want %q (two-space indent, trailing newline)", got, want)
	}
}

func TestSettingsPlanPlacesEachScopesDocument(t *testing.T) {
	dir := filepath.FromSlash("/ws")
	cases := []struct {
		scope   SettingsScope
		want    string
		managed bool
	}{
		{SettingsAtWorkspaceRoot, filepath.Join(dir, ".claude", "settings.json"), false},
		{SettingsAtInstanceRoot, filepath.Join(dir, ".claude", "settings.json"), true},
		{SettingsInRepo, filepath.Join(dir, ".claude", "settings.local.json"), true},
	}
	for _, tc := range cases {
		plan, err := SettingsPlan(SettingsInputs{Scope: tc.scope, Dir: dir, Doc: map[string]any{}})
		if err != nil {
			t.Fatalf("SettingsPlan(scope %d): %v", tc.scope, err)
		}
		e := plan.Entries[0]
		if e.Path != tc.want {
			t.Errorf("scope %d path = %q, want %q", tc.scope, e.Path, tc.want)
		}
		if e.Managed != tc.managed {
			t.Errorf("scope %d managed = %v, want %v", tc.scope, e.Managed, tc.managed)
		}
		if e.Op != OpWriteFile || e.Mode != settingsFileMode {
			t.Errorf("scope %d entry = {op %d, mode %v}, want a 0o600 whole-file write", tc.scope, e.Op, e.Mode)
		}
		if e.Capability != ApprovalPosture {
			t.Errorf("scope %d capability = %s, want %s", tc.scope, e.Capability, ApprovalPosture)
		}
	}
}

func TestSettingsPlanRejectsAScopeItCannotPlace(t *testing.T) {
	if _, err := SettingsPlan(SettingsInputs{Dir: filepath.FromSlash("/ws")}); err == nil {
		t.Fatal("SettingsPlan accepted the zero scope; an unset scope must not resolve to a document")
	}
}
