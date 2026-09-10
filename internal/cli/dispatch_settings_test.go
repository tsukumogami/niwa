package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
)

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
