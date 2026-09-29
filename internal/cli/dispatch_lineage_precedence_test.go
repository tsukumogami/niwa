package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/otelprecedence"
)

// The precedence tests check the value a worker ends up with, not the two
// copies niwa sends. Which copy wins is Claude Code's choice, so each test
// computes the worker's value under every order it could apply
// (otelprecedence.Modes) and requires the same facts from all of them.

// seededUserAttributes is what the tests put in the developer's user settings.
const seededUserAttributes = "team.id=platform,host.name=h1"

// recordFullLaunch replaces the launch seam with one that keeps the whole
// launch: its pass-through and its environment.
func recordFullLaunch(f *dispatchFakes, got *otelprecedence.Launch) {
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		f.launchCalled++
		got.Argv = append([]string(nil), req.Passthrough...)
		got.Env = req.Env
		if got.Env == nil {
			got.Env = os.Environ()
		}
		return nil
	}
}

// holdsLineage reports what the value is missing: a seeded user entry, the
// seeded value of a conflicting key, or any of niwa's attributes.
func holdsLineage(value string) error {
	entries := otelprecedence.Entries(value)
	var missing []string
	for k, v := range otelprecedence.Entries(seededUserAttributes) {
		if entries[k] != v {
			missing = append(missing, fmt.Sprintf("%s=%s (have %q)", k, v, entries[k]))
		}
	}
	for _, name := range lineageAttributeNames {
		if entries[name] == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing %s", strings.Join(missing, ", "))
	}
	return nil
}

// dispatchForPrecedence runs one dispatch that sets every lineage attribute,
// with the seeded user settings and, when shell is not empty, the dispatching
// shell exporting shell. It returns the recorded launch with the user
// settings layer filled in from the seeded file.
func dispatchForPrecedence(t *testing.T, shell string) otelprecedence.Launch {
	t.Helper()
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	var got otelprecedence.Launch
	recordFullLaunch(f, &got)

	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	settingsPath := filepath.Join(configDir, "settings.json")
	doc := fmt.Sprintf(`{"env":{"OTEL_RESOURCE_ATTRIBUTES":%q}}`, seededUserAttributes)
	if err := os.WriteFile(settingsPath, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	if shell != "" {
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", shell)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "caller-session")
	brief := filepath.Join(root, "brief.md")
	if err := os.WriteFile(brief, []byte("the brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dispatchName = "precedence"
	dispatchSkill = "shirabe:work-on"
	dispatchBrief = brief
	dispatchDetach = true

	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	got.UserSettings, got.UserSettingsSet = otelprecedence.UserSettingsValue(settingsPath)
	if !got.UserSettingsSet {
		t.Fatal("the seeded user settings did not parse")
	}
	return got
}

func TestDispatch_Lineage_Precedence_EveryModeHoldsBothLayers(t *testing.T) {
	launch := dispatchForPrecedence(t, "")
	for _, mode := range otelprecedence.Modes {
		value, _ := launch.Effective(mode)
		if err := holdsLineage(value); err != nil {
			t.Errorf("%s: %v in %q", mode, err, value)
		}
	}
}

func TestDispatch_Lineage_Precedence_ShellConflictKeepsSettings(t *testing.T) {
	launch := dispatchForPrecedence(t, "team.id=other")
	for _, mode := range otelprecedence.Modes {
		value, _ := launch.Effective(mode)
		if err := holdsLineage(value); err != nil {
			t.Errorf("%s: %v in %q", mode, err, value)
		}
	}
}

// TestDispatch_Lineage_Precedence_MissingCopyFails shows the check can fail:
// a launch that drops either copy leaves at least one order with a worker
// value missing something.
func TestDispatch_Lineage_Precedence_MissingCopyFails(t *testing.T) {
	launch := dispatchForPrecedence(t, "team.id=other")

	noSettingsCopy := launch
	noSettingsCopy.Argv = withoutLineageSettings(t, launch.Argv)

	noEnvCopy := launch
	noEnvCopy.Env = nil
	for _, kv := range launch.Env {
		if strings.HasPrefix(kv, otelprecedence.Variable+"=") {
			kv = otelprecedence.Variable + "=team.id=other"
		}
		noEnvCopy.Env = append(noEnvCopy.Env, kv)
	}

	for name, l := range map[string]otelprecedence.Launch{
		"without the settings copy":    noSettingsCopy,
		"without the environment copy": noEnvCopy,
	} {
		failed := false
		for _, mode := range otelprecedence.Modes {
			value, _ := l.Effective(mode)
			if holdsLineage(value) != nil {
				failed = true
			}
		}
		if !failed {
			t.Errorf("a launch %s passed every order; the check can't see a lost copy", name)
		}
	}
}
