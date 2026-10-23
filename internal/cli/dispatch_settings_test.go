package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
)

// envValue returns name's value in a KEY=VALUE list and whether it is there,
// failing on a name listed twice.
func envValue(t *testing.T, env []string, name string) (string, bool) {
	t.Helper()
	var value string
	found := 0
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			value = v
			found++
		}
	}
	if found > 1 {
		t.Fatalf("%s appears %d times in the worker environment", name, found)
	}
	return value, found == 1
}

// TestLaunchEnv_MarshalsInOrder: the settings env value is an ordered map of
// strings, and a repeated name replaces its value in place.
func TestLaunchEnv_MarshalsInOrder(t *testing.T) {
	var e launchEnv
	e.Set("OTEL_RESOURCE_ATTRIBUTES", "a=b")
	e.Set("PASS_MARK", "one")
	e.Set(config.ReapKeyEnv, "key-1")
	e.Set("PASS_MARK", "two")
	got, ok := renderLaunchSettings(map[string]any{"env": e})
	if !ok {
		t.Fatal("no document")
	}
	const want = `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"a=b","PASS_MARK":"two","NIWA_REAP_KEY":"key-1"}}`
	if got != want {
		t.Errorf("document = %s, want %s", got, want)
	}
}

// TestApplyLaunchEnv_ReapKeyWithoutLineage: a launch with no lineage value
// still carries NIWA_REAP_KEY in both its settings env and its process
// environment, and an inherited value is replaced, not duplicated.
func TestApplyLaunchEnv_ReapKeyWithoutLineage(t *testing.T) {
	settings := map[string]any{}
	base := []string{"PATH=/usr/bin", "NIWA_REAP_KEY=inherited-key"}
	env := applyLaunchEnv(claudeLaunchSpec(), settings, base, launchEnvShape{ReapKey: "dispatch-id-1"})
	if v, ok := envValue(t, env, config.ReapKeyEnv); !ok || v != "dispatch-id-1" {
		t.Errorf("process NIWA_REAP_KEY = %q (%v), want dispatch-id-1", v, ok)
	}
	doc, ok := renderLaunchSettings(settings)
	if !ok || doc != `{"env":{"NIWA_REAP_KEY":"dispatch-id-1"}}` {
		t.Errorf("settings = %s (%v)", doc, ok)
	}
	if _, ok := envValue(t, env, "OTEL_RESOURCE_ATTRIBUTES"); ok {
		t.Error("a launch with no lineage set OTEL_RESOURCE_ATTRIBUTES")
	}
}

// TestApplyLaunchEnv_InvalidKeyRemovesInherited: with no valid key to set, an
// inherited NIWA_REAP_KEY is removed and none is written to the settings.
func TestApplyLaunchEnv_InvalidKeyRemovesInherited(t *testing.T) {
	for _, key := range []string{"", "bad key", "x;rm -rf", strings.Repeat("a", 129)} {
		settings := map[string]any{}
		env := applyLaunchEnv(claudeLaunchSpec(), settings, []string{"NIWA_REAP_KEY=inherited-key", "HOME=/h"}, launchEnvShape{ReapKey: key})
		if _, ok := envValue(t, env, config.ReapKeyEnv); ok {
			t.Errorf("key %q: an inherited NIWA_REAP_KEY survived", key)
		}
		if _, ok := settings["env"]; ok {
			t.Errorf("key %q: a settings env was written: %v", key, settings)
		}
		if v, _ := envValue(t, env, "HOME"); v != "/h" {
			t.Errorf("key %q: an unrelated variable was lost", key)
		}
	}
}

// TestApplyLaunchEnv_PassthroughAndUnset: a declared pass-through name that is
// set reaches both halves next to lineage, one that isn't set reaches
// neither, and an unset name leaves the process environment.
func TestApplyLaunchEnv_PassthroughAndUnset(t *testing.T) {
	settings := map[string]any{}
	base := []string{
		"PASS_MARK=carried",
		"DROP_MARK=from-the-shell",
		"OTEL_RESOURCE_ATTRIBUTES=old=1",
		"KEEP_MARK=kept",
	}
	env := applyLaunchEnv(claudeLaunchSpec(), settings, base, launchEnvShape{
		Lineage:     "niwa.dispatch.id=x",
		Passthrough: []string{"PASS_MARK", "ABSENT_MARK"},
		Unset:       []string{"DROP_MARK"},
		ReapKey:     "dispatch-id-2",
	})
	if v, ok := envValue(t, env, "PASS_MARK"); !ok || v != "carried" {
		t.Errorf("process PASS_MARK = %q (%v)", v, ok)
	}
	if _, ok := envValue(t, env, "DROP_MARK"); ok {
		t.Error("an unset name stayed in the process environment")
	}
	if _, ok := envValue(t, env, "ABSENT_MARK"); ok {
		t.Error("a pass-through name that was never set appeared")
	}
	if v, _ := envValue(t, env, "OTEL_RESOURCE_ATTRIBUTES"); v != "niwa.dispatch.id=x" {
		t.Errorf("process lineage = %q", v)
	}
	if v, _ := envValue(t, env, "KEEP_MARK"); v != "kept" {
		t.Error("an undeclared variable was lost")
	}
	doc, _ := renderLaunchSettings(settings)
	const want = `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"niwa.dispatch.id=x","PASS_MARK":"carried","NIWA_REAP_KEY":"dispatch-id-2"}}`
	if doc != want {
		t.Errorf("settings = %s, want %s", doc, want)
	}
}

// TestApplyLaunchEnv_NoSettingsFlag: an agent with no settings flag gets the
// shaped process environment and no settings entry at all.
func TestApplyLaunchEnv_NoSettingsFlag(t *testing.T) {
	spec := claudeLaunchSpec()
	spec.Flags.Settings = ""
	settings := map[string]any{}
	env := applyLaunchEnv(spec, settings, []string{"PASS_MARK=carried", "DROP_MARK=x"}, launchEnvShape{
		Passthrough: []string{"PASS_MARK"},
		Unset:       []string{"DROP_MARK"},
		ReapKey:     "dispatch-id-3",
	})
	if len(settings) != 0 {
		t.Errorf("settings = %v, want none", settings)
	}
	if v, _ := envValue(t, env, config.ReapKeyEnv); v != "dispatch-id-3" {
		t.Errorf("process NIWA_REAP_KEY = %q", v)
	}
	if _, ok := envValue(t, env, "DROP_MARK"); ok {
		t.Error("an unset name stayed in the process environment")
	}
}

// TestRenderLaunchSettings_RemoteControlAloneIsByteIdentical pins the
// regression guard for routing remote control through the shared map: with its
// key alone, the rendered document is exactly the one niwa sent before.
func TestRenderLaunchSettings_RemoteControlAloneIsByteIdentical(t *testing.T) {
	got, ok := renderLaunchSettings(map[string]any{config.RemoteControlAtStartupKey: true})
	if !ok {
		t.Fatal("renderLaunchSettings reported no document for a map with one key")
	}
	if got != remoteControlSettingsJSON {
		t.Errorf("renderLaunchSettings = %q, want %q byte for byte", got, remoteControlSettingsJSON)
	}
}

// TestRenderLaunchSettings_SortsKeys checks that contributors share one
// document in sorted key order. Go randomizes map iteration, so with five keys
// an encoder that followed iteration order instead of sorting would fail this
// on almost every run.
func TestRenderLaunchSettings_SortsKeys(t *testing.T) {
	const want = `{"aKey":"accept","bKey":true,"cKey":1,"dKey":false,"eKey":"x"}`
	got, ok := renderLaunchSettings(map[string]any{
		"eKey": "x", "cKey": 1, "aKey": "accept", "dKey": false, "bKey": true,
	})
	if !ok {
		t.Fatal("renderLaunchSettings reported no document for a map with five keys")
	}
	if got != want {
		t.Errorf("renderLaunchSettings = %q, want keys in sorted order: %q", got, want)
	}
}

// TestRenderLaunchSettings_EmptyMapRendersNothing checks that no contributor
// means no document, so step (9c) appends no settings flag.
func TestRenderLaunchSettings_EmptyMapRendersNothing(t *testing.T) {
	for name, m := range map[string]map[string]any{
		"nil":   nil,
		"empty": {},
	} {
		got, ok := renderLaunchSettings(m)
		if ok || got != "" {
			t.Errorf("%s map: renderLaunchSettings = (%q, %v), want (\"\", false)", name, got, ok)
		}
	}
}

// TestRenderLaunchSettings_UnencodableValuePanics pins that a contributor
// passing a value encoding/json can't encode fails loudly. Returning no
// document instead would leave that contributor's own record saying its key
// was sent when it wasn't.
func TestRenderLaunchSettings_UnencodableValuePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("renderLaunchSettings did not panic on an unencodable value")
		}
	}()
	renderLaunchSettings(map[string]any{"badKey": func() {}})
}

// TestBuildLaunchArgs_PromptCannotReplaceTheSettingsDocument is the reason
// Claude's launch spec declares a prompt separator. Claude Code reads a prompt
// that begins with a dash as a flag, and a repeated --settings is last-wins, so
// without the separator this prompt would silently replace niwa's document. With
// it, niwa's document is the only settings flag the agent parses, and the prompt
// arrives as the prompt.
func TestBuildLaunchArgs_PromptCannotReplaceTheSettingsDocument(t *testing.T) {
	const prompt = `--settings={"remoteControlAtStartup":false}`
	doc, ok := renderLaunchSettings(map[string]any{config.RemoteControlAtStartupKey: true})
	if !ok {
		t.Fatal("no document rendered for remote control")
	}

	for _, mode := range []struct {
		name string
		mode agentplan.LaunchMode
	}{
		{"detached", agentplan.LaunchDetached},
		{"backgrounded", agentplan.LaunchBackgrounded},
	} {
		t.Run(mode.name, func(t *testing.T) {
			got := buildLaunchArgs(claudeLaunchSpec(), mode.mode, "/inst", prompt, []string{"--name", "w", "--settings", doc})

			sep := slices.Index(got, "--")
			if sep < 0 {
				t.Fatalf("no -- separator in %#v; Claude's launch spec in internal/agentplan must set PromptSeparator", got)
			}
			if after := got[sep+1:]; !slices.Equal(after, []string{prompt}) {
				t.Errorf("elements after -- = %#v, want only the prompt", after)
			}

			var settingsAt []int
			for i, a := range got[:sep] {
				if a == prompt {
					t.Errorf("prompt text appears before the separator at %d: %#v", i, got)
				}
				if a == "--settings" || strings.HasPrefix(a, "--settings=") {
					settingsAt = append(settingsAt, i)
				}
			}
			if len(settingsAt) != 1 {
				t.Fatalf("want exactly one --settings before the separator, found %d in %#v", len(settingsAt), got)
			}
			if i := settingsAt[0]; got[i] != "--settings" || i+1 >= sep || got[i+1] != doc {
				t.Errorf("the settings flag before the separator is not niwa's: %#v", got)
			}
		})
	}
}
