package workspace

import (
	"strings"
	"testing"
)

// sessionStartCommandAt extracts the first hook command string from the
// SessionStart entry at index i. It tolerates both concrete slice types the
// builder uses: installed-hook entries carry []map[string]string, while the
// ephemeral session entry carries []map[string]any.
func sessionStartCommandAt(t *testing.T, entries []map[string]any, i int) string {
	t.Helper()
	if i >= len(entries) {
		t.Fatalf("SessionStart entry index %d out of range (len %d)", i, len(entries))
	}
	switch hs := entries[i]["hooks"].(type) {
	case []map[string]string:
		if len(hs) > 0 {
			return hs[0]["command"]
		}
	case []map[string]any:
		if len(hs) > 0 {
			if c, ok := hs[0]["command"].(string); ok {
				return c
			}
		}
	default:
		t.Fatalf("unexpected hooks type %T in SessionStart entry %d", entries[i]["hooks"], i)
	}
	return ""
}

func sessionStartEntries(t *testing.T, doc map[string]any) []map[string]any {
	t.Helper()
	hooks, ok := doc["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("doc[\"hooks\"] missing or wrong type: %T", doc["hooks"])
	}
	entries, ok := hooks[sessionStartEvent].([]map[string]any)
	if !ok {
		t.Fatalf("hooks[%q] missing or wrong type: %T", sessionStartEvent, hooks[sessionStartEvent])
	}
	return entries
}

// TestBuildSettingsDoc_SessionStartMergesInstalledAndEphemeral is the R10
// regression guard: when an installed hook is registered on the session_start
// event AND the ephemeral session integration is active, the materialized
// SessionStart block must contain BOTH entries. An earlier version overwrote the
// installed entry with the ephemeral one, silently dropping dot-niwa's
// compaction re-injection hook in ephemeral-session mode. Installed entries come
// first (stable order); the ephemeral entry is appended.
func TestBuildSettingsDoc_SessionStartMergesInstalledAndEphemeral(t *testing.T) {
	const installedPath = "/inst/.claude/hooks/session_start/work-summary-compact.sh"
	const ephemeralCmd = "/usr/bin/niwa instance from-hook"

	doc, err := buildSettingsDoc(BuildSettingsConfig{
		InstalledHooks: map[string][]InstalledHookEntry{
			"session_start": {{Paths: []string{installedPath}}},
		},
		SessionHooks:     &SessionHooks{Command: ephemeralCmd, TimeoutSeconds: 300},
		UseAbsolutePaths: true,
	})
	if err != nil {
		t.Fatalf("buildSettingsDoc: %v", err)
	}

	entries := sessionStartEntries(t, doc)
	if len(entries) != 2 {
		t.Fatalf("SessionStart must contain both the installed and ephemeral entries, got %d: %v", len(entries), entries)
	}
	if got := sessionStartCommandAt(t, entries, 0); got != installedPath {
		t.Errorf("first SessionStart entry (installed) command = %q, want %q", got, installedPath)
	}
	if got := sessionStartCommandAt(t, entries, 1); got != ephemeralCmd {
		t.Errorf("second SessionStart entry (ephemeral) command = %q, want %q", got, ephemeralCmd)
	}
}

// TestBuildSettingsDoc_SessionStartEphemeralOnly pins the no-installed-hook case:
// with only the ephemeral integration active, the SessionStart block carries a
// single entry (the niwa instance from-hook command).
func TestBuildSettingsDoc_SessionStartEphemeralOnly(t *testing.T) {
	const ephemeralCmd = "/usr/bin/niwa instance from-hook"

	doc, err := buildSettingsDoc(BuildSettingsConfig{
		SessionHooks:     &SessionHooks{Command: ephemeralCmd, TimeoutSeconds: 300},
		UseAbsolutePaths: true,
	})
	if err != nil {
		t.Fatalf("buildSettingsDoc: %v", err)
	}

	entries := sessionStartEntries(t, doc)
	if len(entries) != 1 {
		t.Fatalf("SessionStart must contain exactly one (ephemeral) entry, got %d: %v", len(entries), entries)
	}
	if got := sessionStartCommandAt(t, entries, 0); !strings.Contains(got, "niwa instance from-hook") {
		t.Errorf("ephemeral SessionStart command = %q, want it to contain %q", got, "niwa instance from-hook")
	}
}
