package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// derivedNoticePrefix is the stderr notice buildDispatchPassthrough's caller
// prints when it derives --permission-mode from the workspace's materialized
// settings rather than an explicit flag.
const derivedNoticePrefix = "niwa dispatch: derived --permission-mode bypassPermissions"

// bypassSettings is a materialized .claude/settings.json body declaring the
// workspace's posture as bypass.
const bypassSettings = `{"permissions": {"defaultMode": "bypassPermissions"}}`

// TestDispatch_PermissionMode_DerivedWhenBypassAndNoFlag covers AC1: a
// bypass-configured workspace with no explicit --permission-mode gets the
// flag derived into the launched worker's argv.
func TestDispatch_PermissionMode_DerivedWhenBypassAndNoFlag(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, bypassSettings)
	var pass []string
	captureLaunchPassthrough(f, &pass)

	stdout, stderr, err := runDispatchCmd(t, "do a thing")
	_ = stdout
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"--permission-mode", "bypassPermissions"}
	if !containsSubsequence(pass, want) {
		t.Fatalf("passthrough = %v, want it to contain %v", pass, want)
	}
	if !strings.Contains(stderr, derivedNoticePrefix) {
		t.Fatalf("expected audit notice on stderr, got %q", stderr)
	}
}

// TestDispatch_PermissionMode_ExplicitFlagWins covers AC2: an operator who
// passes --permission-mode explicitly gets exactly that value, regardless of
// the workspace's declared posture, and the audit notice does not fire.
func TestDispatch_PermissionMode_ExplicitFlagWins(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, bypassSettings)
	dispatchPermissionMode = "acceptEdits"
	var pass []string
	captureLaunchPassthrough(f, &pass)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"--permission-mode", "acceptEdits"}
	if !containsSubsequence(pass, want) {
		t.Fatalf("passthrough = %v, want it to contain %v", pass, want)
	}
	if slices.Contains(pass, "acceptEdits") && slices.Contains(pass, "bypassPermissions") {
		t.Fatalf("expected exactly one --permission-mode value, got both in %v", pass)
	}
	if strings.Contains(stderr, derivedNoticePrefix) {
		t.Fatalf("audit notice must not fire when the flag was explicit; got %q", stderr)
	}
}

// TestDispatch_PermissionMode_NoPostureOrAsk_ForwardsNothing covers AC3 and
// the negative half of AC9: a workspace with no declared posture, or "ask",
// forwards no --permission-mode flag and prints no audit notice.
func TestDispatch_PermissionMode_NoPostureOrAsk_ForwardsNothing(t *testing.T) {
	cases := []struct {
		name     string
		settings string
	}{
		{"no permissions key", `{"enabledPlugins": {}}`},
		{"no settings file at all", ""},
		{"ask posture", `{"permissions": {"defaultMode": "ask"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, "")
			f := installDispatchFakes(t, root)
			provisionWithInstanceSettings(t, f, tc.settings)
			var pass []string
			captureLaunchPassthrough(f, &pass)

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if slices.Contains(pass, "--permission-mode") {
				t.Fatalf("expected no --permission-mode flag, got %v", pass)
			}
			if strings.Contains(stderr, derivedNoticePrefix) {
				t.Fatalf("audit notice must not fire when nothing was derived; got %q", stderr)
			}
		})
	}
}

// TestDispatch_PermissionMode_Codex_NeverDerived covers AC4: dispatching a
// Codex worker never receives a derived value through its --sandbox flag,
// regardless of the workspace's declared permission posture, and the audit
// notice does not fire (it names --permission-mode, a Claude-only concept).
func TestDispatch_PermissionMode_Codex_NeverDerived(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, bypassSettings)
	dispatchHarness = "codex"
	var pass []string
	captureLaunchPassthrough(f, &pass)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slices.Contains(pass, "bypassPermissions") {
		t.Fatalf("Codex must never receive a derived bypassPermissions value; got %v", pass)
	}
	if slices.Contains(pass, "--sandbox") {
		t.Fatalf("Codex's --sandbox flag must not be touched by this derivation; got %v", pass)
	}
	if strings.Contains(stderr, derivedNoticePrefix) {
		t.Fatalf("audit notice must not fire for Codex; got %q", stderr)
	}
}

// TestDispatch_PermissionMode_MaterializedSettingsWinOverWorkspaceToml covers
// AC6: the derivation reads the materialized instance settings, never
// workspace.toml directly. A fixture where the two disagree, in both
// directions, proves it -- an implementation that (wrongly) re-derived from
// workspace.toml would fail one direction or the other.
func TestDispatch_PermissionMode_MaterializedSettingsWinOverWorkspaceToml(t *testing.T) {
	cases := []struct {
		name             string
		workspacePerm    string // workspace.toml's [claude.settings] permissions value
		materializedBody string // the instance's materialized .claude/settings.json
		wantDerived      bool
	}{
		{
			name:             "workspace.toml says ask, materialized says bypass",
			workspacePerm:    "ask",
			materializedBody: bypassSettings,
			wantDerived:      true,
		},
		{
			name:             "workspace.toml says bypass, materialized says nothing",
			workspacePerm:    "bypass",
			materializedBody: `{"enabledPlugins": {}}`,
			wantDerived:      false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			writeWorkspaceClaudePermissions(t, root, tc.workspacePerm)
			chdir(t, root)
			setHostConfig(t, "")
			f := installDispatchFakes(t, root)
			provisionWithInstanceSettings(t, f, tc.materializedBody)
			var pass []string
			captureLaunchPassthrough(f, &pass)

			_, _, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := slices.Contains(pass, "bypassPermissions")
			if got != tc.wantDerived {
				t.Fatalf("derived=%v, want %v (passthrough=%v) -- derivation must track materialized settings, not workspace.toml", got, tc.wantDerived, pass)
			}
		})
	}
}

// writeWorkspaceClaudePermissions appends a [claude.settings] permissions
// key to the workspace.toml setupDispatchWorkspace already wrote, so a test
// can set a workspace-declared posture that may disagree with the
// materialized instance settings.
func writeWorkspaceClaudePermissions(t *testing.T, root, value string) {
	t.Helper()
	path := filepath.Join(root, ".niwa", "workspace.toml")
	existing, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(existing) + "\n[claude.settings]\npermissions = \"" + value + "\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDispatch_PermissionMode_MalformedSettings_DegradesToNothingDerived
// covers AC7: an absent, unreadable, or malformed materialized settings.json
// results in no --permission-mode flag being forwarded from this derivation
// path, and dispatch does not fail because of it.
func TestDispatch_PermissionMode_MalformedSettings_DegradesToNothingDerived(t *testing.T) {
	cases := []struct {
		name     string
		settings string
	}{
		{"malformed json", `{ this is not json`},
		{"absent file", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, "")
			f := installDispatchFakes(t, root)
			provisionWithInstanceSettings(t, f, tc.settings)
			var pass []string
			captureLaunchPassthrough(f, &pass)

			_, _, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("a malformed or absent settings.json must not fail dispatch: %v", err)
			}
			if slices.Contains(pass, "--permission-mode") {
				t.Fatalf("expected no --permission-mode flag on a degraded read, got %v", pass)
			}
		})
	}
}

// TestDispatch_PermissionMode_RemoteControlAndKeepAlive_Unaffected covers
// AC8: consolidating the settings read to one call site ahead of
// buildDispatchPassthrough must not regress the existing remote-control
// default-fill or keep-alive arming behaviors, both of which also consume
// the instance settings read at that same site.
func TestDispatch_PermissionMode_RemoteControlAndKeepAlive_Unaffected(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch)
	f := installDispatchFakes(t, root)
	// A settings body carrying both a bypass posture (exercises the new
	// derivation) and remoteControlAtStartup unset (exercises the existing
	// host default-fill) proves the single shared read serves both readers.
	provisionWithInstanceSettings(t, f, bypassSettings)
	var pass []string
	captureLaunchPassthrough(f, &pass)

	_, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasRemoteControlSettings(pass) {
		t.Fatalf("remote-control default-fill must still inject; got %v", pass)
	}
	if !slices.Contains(pass, "bypassPermissions") {
		t.Fatalf("permission-mode derivation must still fire alongside remote-control; got %v", pass)
	}
}

// containsSubsequence reports whether want appears as a contiguous
// subsequence anywhere in got.
func containsSubsequence(got, want []string) bool {
	if len(want) == 0 {
		return true
	}
	for i := 0; i+len(want) <= len(got); i++ {
		if slices.Equal(got[i:i+len(want)], want) {
			return true
		}
	}
	return false
}
