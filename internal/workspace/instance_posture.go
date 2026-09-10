package workspace

import "github.com/tsukumogami/niwa/internal/config"

// Canonical permission postures a workspace can declare under
// [claude.settings] permissions. These are niwa's own vocabulary, not Claude
// Code permission modes.
const (
	postureBypass = "bypass"
	postureAsk    = "ask"
)

// instancePermissionsPosture returns the permission posture the instance-root
// settings document resolves from: "bypass", "ask", or "" when the key is
// absent or holds anything else.
//
// It reads the same input the instance-root settings materializer reads,
// MergeInstanceOverrides(cfg), so the recorded posture follows the same
// precedence: workspace overlay, then workspace, then personal overlay, then
// [instance.claude.settings]. Per-repo overrides never reach it.
//
// It deliberately does not validate. RootSettingsMaterializer reads the same
// map and rejects an unrecognized value before the pipeline saves state, so an
// invalid value never reaches InstanceState. Returning a canonical literal
// rather than the revealed string also means a secret-backed value's
// plaintext is never retained past this call.
func instancePermissionsPosture(cfg *config.WorkspaceConfig) string {
	perm, ok := MergeInstanceOverrides(cfg).Claude.Settings["permissions"]
	if !ok {
		return ""
	}
	switch maybeSecretString(perm) {
	case postureBypass:
		return postureBypass
	case postureAsk:
		return postureAsk
	default:
		return ""
	}
}
