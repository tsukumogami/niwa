package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// setHostConfig points XDG_CONFIG_HOME at a temp dir and, when body != "",
// writes niwa/config.toml there. An empty body leaves the host config absent
// (the "preference unset" case).
func setHostConfig(t *testing.T, body string) {
	t.Helper()
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	if body != "" {
		dir := filepath.Join(cfgHome, "niwa")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// provisionWithInstanceSettings overrides the provision seam so the instance dir
// also carries a .claude/settings.json with the given body (empty => no file).
// Must be called AFTER installDispatchFakes so its restore wins.
func provisionWithInstanceSettings(t *testing.T, f *dispatchFakes, settingsBody string) {
	t.Helper()
	provisionInstanceFunc = func(_ context.Context, root, _, namePrefix, sep string) (provisionResult, error) {
		f.provisionCalled++
		name := "test-ws" + sep + namePrefix
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(dir, ".niwa"), 0o755); err != nil {
			return provisionResult{}, err
		}
		if settingsBody != "" {
			claudeDir := filepath.Join(dir, ".claude")
			if err := os.MkdirAll(claudeDir, 0o755); err != nil {
				return provisionResult{}, err
			}
			if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(settingsBody), 0o644); err != nil {
				return provisionResult{}, err
			}
		}
		f.instancePath = dir
		return provisionResult{Name: name, Path: dir}, nil
	}
}

// captureLaunchPassthrough overrides the launch seam to record the passthrough argv.
func captureLaunchPassthrough(f *dispatchFakes, got *[]string) {
	dispatchLaunch = func(_ context.Context, _, _ string, passthrough []string, _ []string) error {
		f.launchCalled++
		*got = passthrough
		return nil
	}
}

const hostRConDispatch = "[global]\nremote_control_on_dispatch = true\n"

func hasRemoteControlSettings(passthrough []string) bool {
	for i := 0; i+1 < len(passthrough); i++ {
		if passthrough[i] == "--settings" && passthrough[i+1] == remoteControlSettingsJSON {
			return true
		}
	}
	return false
}

func TestDispatch_RemoteControl_HostOn_Injects(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "") // ensure not forced to API-key auth
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch)
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "") // downstream unset
	var pass []string
	captureLaunchPassthrough(f, &pass)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasRemoteControlSettings(pass) {
		t.Fatalf("expected --settings %s in passthrough, got %v", remoteControlSettingsJSON, pass)
	}
}

func TestDispatch_RemoteControl_DownstreamOff_Wins(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch)
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, `{"remoteControlAtStartup": false}`)
	var pass []string
	captureLaunchPassthrough(f, &pass)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasRemoteControlSettings(pass) {
		t.Fatalf("downstream off must win; --settings should be absent, got %v", pass)
	}
}

func TestDispatch_RemoteControl_HostUnset_NoChange(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "") // no host config at all
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")
	var pass []string
	captureLaunchPassthrough(f, &pass)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// AC4: with the preference unset, the passthrough must be byte-for-byte the
	// baseline buildDispatchPassthrough produces -- not merely "--settings absent".
	// dispatchName is "" here, so the baseline carries no flags at all.
	if want := buildDispatchPassthrough("", ""); !slices.Equal(pass, want) {
		t.Fatalf("preference unset must not alter argv; got %v, want %v", pass, want)
	}
}

func TestDispatch_RemoteControl_APIKey_WarnsAndSkips(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-xxx")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch)
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")
	var pass []string
	captureLaunchPassthrough(f, &pass)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hasRemoteControlSettings(pass) {
		t.Fatalf("API-key auth precludes remote-control; --settings should be absent, got %v", pass)
	}
	if !strings.Contains(stderr, apiKeyForcedWarning) {
		t.Fatalf("expected the exact eligibility warning on stderr, got %q", stderr)
	}
}

// A malformed instance settings.json is unreadable, so the resolver treats the
// downstream value as unset and the host default-fill still injects -- the dispatch
// must never fail on it.
func TestDispatch_RemoteControl_MalformedInstanceSettings_DegradesToInject(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch)
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, `{ this is not json`)
	var pass []string
	captureLaunchPassthrough(f, &pass)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("a malformed instance settings.json must not fail dispatch: %v", err)
	}
	if !hasRemoteControlSettings(pass) {
		t.Fatalf("unreadable downstream value should be treated as unset -> inject; got %v", pass)
	}
}

// An invalid host-config value makes LoadGlobalConfig fail; dispatch must degrade
// to no injection (today's behavior) rather than fail.
func TestDispatch_RemoteControl_InvalidHostConfig_NoInjectNoFail(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "[global]\nremote_control_on_dispatch = \"notabool\"\n")
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")
	var pass []string
	captureLaunchPassthrough(f, &pass)

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("an invalid host config must not fail dispatch: %v", err)
	}
	if slices.Contains(pass, "--settings") {
		t.Fatalf("invalid host config should degrade to no injection; got %v", pass)
	}
}
