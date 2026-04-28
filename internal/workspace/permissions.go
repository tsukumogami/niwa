package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// settingsPermissionsDoc reads only the permissions key from settings.json.
// The env key is intentionally absent — it may carry secret material and is not
// needed here. Do not widen this struct without auditing callers for secret exposure.
type settingsPermissionsDoc struct {
	Permissions struct {
		DefaultMode string `json:"defaultMode"`
	} `json:"permissions"`
}

// WorkerPermissionMode reads the coordinator's materialized permission mode from
// <instanceRoot>/.claude/settings.json. The instance root receives settings.json
// (not settings.local.json) because InstallWorkspaceRootSettings writes there;
// per-repo dirs get settings.local.json instead. Returns "bypassPermissions" if
// the file exists and permissions.defaultMode equals that value. Returns
// "acceptEdits" in all other cases: file absent, unreadable, malformed JSON, key
// missing, or any value other than "bypassPermissions". Never returns an empty string.
func WorkerPermissionMode(instanceRoot string) string {
	path := filepath.Join(instanceRoot, ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return "acceptEdits"
	}
	var doc settingsPermissionsDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return "acceptEdits"
	}
	if doc.Permissions.DefaultMode == "bypassPermissions" {
		return "bypassPermissions"
	}
	return "acceptEdits"
}
