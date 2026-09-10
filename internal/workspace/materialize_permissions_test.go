package workspace

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret"
)

func TestClaudeDefaultMode(t *testing.T) {
	tests := []struct {
		posture   string
		wantMode  string
		wantWrite bool
		wantErr   bool
	}{
		{posture: "bypass", wantMode: "", wantWrite: false},
		{posture: "ask", wantMode: "default", wantWrite: true},
		{posture: "bypassPermissions", wantErr: true},
		{posture: "askPermissions", wantErr: true},
		{posture: "auto", wantErr: true},
		{posture: "acceptEdits", wantErr: true},
		{posture: "", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.posture, func(t *testing.T) {
			mode, write, err := claudeDefaultMode(tc.posture)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("claudeDefaultMode(%q) returned no error", tc.posture)
				}
				if tc.posture != "" && strings.Contains(err.Error(), tc.posture) {
					t.Errorf("error %q contains its input %q", err.Error(), tc.posture)
				}
				return
			}
			if err != nil {
				t.Fatalf("claudeDefaultMode(%q): %v", tc.posture, err)
			}
			if mode != tc.wantMode || write != tc.wantWrite {
				t.Errorf("claudeDefaultMode(%q) = (%q, %v), want (%q, %v)", tc.posture, mode, write, tc.wantMode, tc.wantWrite)
			}
		})
	}
}

// secretSetting builds a resolved, vault-backed settings value whose
// plaintext is the given string.
func secretSetting(plaintext, provider, key string) config.MaybeSecret {
	return config.MaybeSecret{
		Secret: secret.New([]byte(plaintext), secret.Origin{ProviderName: provider, Key: key}),
	}
}

func TestBuildSettingsDoc_InvalidPermissionsPlainIsQuoted(t *testing.T) {
	settings := config.SettingsConfig{"permissions": plainSetting("bypassPermissions")}
	_, err := buildSettingsDoc(BuildSettingsConfig{Settings: settings})
	if err == nil {
		t.Fatal("expected an error for an invalid permissions value, got nil")
	}
	msg := err.Error()
	for _, want := range []string{`"bypassPermissions"`, `"bypass"`, `"ask"`} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %s", msg, want)
		}
	}
}

// TestBuildSettingsDoc_InvalidSettingSecretIsNotLeaked covers all three keys
// buildSettingsDoc validates. A vault-backed value that fails validation must
// be described by its key and origin, never by its resolved plaintext.
func TestBuildSettingsDoc_InvalidSettingSecretIsNotLeaked(t *testing.T) {
	const plaintext = "sekret-plaintext-7f3a9c"
	tests := []struct {
		key      string
		wantText []string
	}{
		{key: "permissions", wantText: []string{`"bypass"`, `"ask"`}},
		{key: config.RemoteControlAtStartupKey, wantText: []string{`"true"`, `"false"`}},
		{key: config.KeepAliveOnDispatchKey, wantText: []string{`"true"`, `"false"`}},
	}
	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			settings := config.SettingsConfig{tc.key: secretSetting(plaintext, "team-vault", "claude/"+tc.key)}
			_, err := buildSettingsDoc(BuildSettingsConfig{Settings: settings})
			if err == nil {
				t.Fatal("expected an error for an invalid secret-backed value, got nil")
			}
			msg := err.Error()
			if strings.Contains(msg, plaintext) {
				t.Fatalf("error leaks the resolved plaintext: %q", msg)
			}
			for _, want := range append([]string{tc.key, "team-vault", "claude/" + tc.key}, tc.wantText...) {
				if !strings.Contains(msg, want) {
					t.Errorf("error %q does not contain %q", msg, want)
				}
			}
		})
	}
}

// TestBuildSettingsDoc_BooleanKeysPlainIsQuoted pins that the plain-value
// form of the two boolean keys still quotes the rejected value.
func TestBuildSettingsDoc_BooleanKeysPlainIsQuoted(t *testing.T) {
	for _, key := range []string{config.RemoteControlAtStartupKey, config.KeepAliveOnDispatchKey} {
		t.Run(key, func(t *testing.T) {
			settings := config.SettingsConfig{key: plainSetting("maybe")}
			_, err := buildSettingsDoc(BuildSettingsConfig{Settings: settings})
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if msg := err.Error(); !strings.Contains(msg, `"maybe"`) || !strings.Contains(msg, key) {
				t.Errorf("error %q does not quote the value and name the key", msg)
			}
		})
	}
}
