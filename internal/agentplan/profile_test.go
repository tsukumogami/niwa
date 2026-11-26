package agentplan

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

// coordinatorDrops is the coordinator profile the design settles on: seven
// tools in declared order, bundled skills off, connectors off.
var coordinatorDrops = ProfileDrops{
	Tools: []string{
		"Artifact", "Workflow", "SendFeedback", "ReportFindings",
		"EnterWorktree", "AskUserQuestion", "Skill",
	},
	BundledSkills:     true,
	AccountConnectors: true,
}

func TestProfileSettingsSpellsTheCoordinatorProfileForClaude(t *testing.T) {
	keys, env, err := ProfileSettings(agent.AgentClaude, coordinatorDrops)
	if err != nil {
		t.Fatalf("ProfileSettings(claude): %v", err)
	}
	wantKeys := map[string]any{
		"permissions":          map[string]any{"deny": coordinatorDrops.Tools},
		"disableBundledSkills": true,
	}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("keys = %#v, want %#v", keys, wantKeys)
	}
	wantEnv := map[string]string{"ENABLE_CLAUDEAI_MCP_SERVERS": "false"}
	if !reflect.DeepEqual(env, wantEnv) {
		t.Errorf("env = %#v, want %#v", env, wantEnv)
	}
}

func TestProfileSettingsAddsOnlyWhatTheProfileDrops(t *testing.T) {
	keys, env, err := ProfileSettings(agent.AgentClaude, ProfileDrops{Tools: []string{"Workflow"}})
	if err != nil {
		t.Fatalf("tools only: %v", err)
	}
	if _, ok := keys["disableBundledSkills"]; ok {
		t.Errorf("tools only: keys carry disableBundledSkills: %#v", keys)
	}
	if len(env) != 0 {
		t.Errorf("tools only: env = %#v, want none", env)
	}
	if _, ok := keys["permissions"]; !ok {
		t.Errorf("tools only: keys carry no permissions: %#v", keys)
	}

	keys, env, err = ProfileSettings(agent.AgentClaude, ProfileDrops{AccountConnectors: true})
	if err != nil {
		t.Fatalf("connectors only: %v", err)
	}
	if _, ok := keys["permissions"]; ok {
		t.Errorf("connectors only: keys carry permissions: %#v", keys)
	}
	if len(keys) != 0 {
		t.Errorf("connectors only: keys = %#v, want none", keys)
	}
	if env["ENABLE_CLAUDEAI_MCP_SERVERS"] != "false" {
		t.Errorf("connectors only: env = %#v, want the connectors switch off", env)
	}
}

func TestProfileSettingsDoesNotShareTheCallersToolSlice(t *testing.T) {
	tools := []string{"Workflow", "Artifact"}
	keys, _, err := ProfileSettings(agent.AgentClaude, ProfileDrops{Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	tools[0] = "Changed"
	deny := keys["permissions"].(map[string]any)["deny"].([]string)
	if deny[0] != "Workflow" {
		t.Errorf("deny list follows the caller's slice: %v", deny)
	}
}

func TestProfileSettingsRefusesCodexNamingTheRow(t *testing.T) {
	keys, env, err := ProfileSettings(agent.AgentCodex, coordinatorDrops)
	if err == nil {
		t.Fatalf("ProfileSettings(codex) = %#v, %#v, want an error", keys, env)
	}
	if keys != nil || env != nil {
		t.Errorf("ProfileSettings(codex) returned a spelling with its error: %#v, %#v", keys, env)
	}
	if !strings.Contains(err.Error(), DispatchProfile.String()) {
		t.Errorf("error %q does not name the %s row", err, DispatchProfile)
	}
}

func TestProfileRequiredTools(t *testing.T) {
	if got, want := ProfileRequiredTools(agent.AgentClaude), []string{"ToolSearch", "CronCreate"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ProfileRequiredTools(claude) = %v, want %v", got, want)
	}
	if got := ProfileRequiredTools(agent.AgentCodex); len(got) != 0 {
		t.Errorf("ProfileRequiredTools(codex) = %v, want none", got)
	}
}

func TestAllProfileEnvNames(t *testing.T) {
	if got, want := AllProfileEnvNames(), []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "ENABLE_CLAUDEAI_MCP_SERVERS"}; !reflect.DeepEqual(got, want) {
		t.Errorf("AllProfileEnvNames() = %v, want %v", got, want)
	}
}

// TestSettingsOnlyProfileEnvNames: the compaction window is the one name a
// profile sets in the settings document alone, and it is among the names any
// profile can set, so a launch that strips an inherited profile strips it too.
func TestSettingsOnlyProfileEnvNames(t *testing.T) {
	got := SettingsOnlyProfileEnvNames()
	if want := []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW"}; !reflect.DeepEqual(got, want) {
		t.Errorf("SettingsOnlyProfileEnvNames() = %v, want %v", got, want)
	}
	for _, n := range got {
		if !slices.Contains(AllProfileEnvNames(), n) {
			t.Errorf("%s is settings-only but AllProfileEnvNames() doesn't list it", n)
		}
	}
}

// TestProfileSettingsSpellsTheWindow: a window becomes the decimal string of
// CLAUDE_CODE_AUTO_COMPACT_WINDOW in the env values and adds no settings key;
// a profile with no window sets no such name; Codex still refuses.
func TestProfileSettingsSpellsTheWindow(t *testing.T) {
	keys, env, err := ProfileSettings(agent.AgentClaude, ProfileDrops{AutoCompactWindow: 400000})
	if err != nil {
		t.Fatalf("window only: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("window only: keys = %#v, want none", keys)
	}
	if want := map[string]string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW": "400000"}; !reflect.DeepEqual(env, want) {
		t.Errorf("window only: env = %#v, want %#v", env, want)
	}
	_, env, err = ProfileSettings(agent.AgentClaude, coordinatorDrops)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := env["CLAUDE_CODE_AUTO_COMPACT_WINDOW"]; ok {
		t.Errorf("a profile with no window set one: %#v", env)
	}
	if _, _, err := ProfileSettings(agent.AgentClaude, ProfileDrops{AutoCompactWindow: -1}); err == nil {
		t.Error("a negative window was spelled")
	}
	if _, _, err := ProfileSettings(agent.AgentCodex, ProfileDrops{AutoCompactWindow: 400000}); err == nil {
		t.Error("ProfileSettings(codex) spelled a window")
	}
}

// TestProfileEnvStaysInsideAllProfileEnvNames fails when any agent's spelling
// sets an env name the union doesn't list. The union is what an inherited
// environment is stripped of, so a name outside it would leak from a profiled
// session into every launch below it.
func TestProfileEnvStaysInsideAllProfileEnvNames(t *testing.T) {
	union := AllProfileEnvNames()
	everything := coordinatorDrops
	everything.AutoCompactWindow = 400000
	for _, ag := range agent.All() {
		_, env, err := ProfileSettings(ag, everything)
		if err != nil {
			continue
		}
		for name := range env {
			if !slices.Contains(union, name) {
				t.Errorf("%s's profile sets %s, which AllProfileEnvNames() doesn't list", ag, name)
			}
		}
	}
}

// TestEveryImplementedProfileRowHasASpelling holds the declaration table and
// the spelling table together: an agent whose DispatchProfile row is
// implemented must have a spelling, and one without a spelling must not be
// declared implemented.
func TestEveryImplementedProfileRowHasASpelling(t *testing.T) {
	for _, ag := range agent.All() {
		d, err := Lookup(DispatchProfile, ag)
		if err != nil {
			t.Fatalf("Lookup(%s, %s): %v", DispatchProfile, ag, err)
		}
		_, has := profileSpellings[ag]
		if implemented := d.State == StateImplemented; implemented != has {
			t.Errorf("%s: declared implemented=%v but has spelling=%v", ag, implemented, has)
		}
	}
}

func TestDispatchProfileRowForCodexIsUnsupportedWithAReason(t *testing.T) {
	d, err := Lookup(DispatchProfile, agent.AgentCodex)
	if err != nil {
		t.Fatal(err)
	}
	if d.State != StateUnavailable || d.Kind != ReasonNotBuilt || d.Reason == "" {
		t.Errorf("Lookup(%s, codex) = %+v, want unavailable, not built, with a reason", DispatchProfile, d)
	}
}
