package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

// setDispatchWorkspaceDefaultAgent rewrites the workspace config at root so its
// [workspace] table carries default_agent. It is the middle rung of the
// resolution -- what a workspace states for everyone who works in it.
func setDispatchWorkspaceDefaultAgent(t *testing.T, root, value string) {
	t.Helper()
	body := "[workspace]\nname = \"test-ws\"\ndefault_agent = \"" + value + "\"\n"
	path := filepath.Join(root, config.ConfigDir, config.ConfigFile)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// recordPreflightedBinary replaces the binary preflight with one that records
// the name it was asked for. The name comes from the resolved agent's own
// launch spec, so it is the cleanest observable answer to "which agent did this
// dispatch decide to launch". Must be called AFTER installDispatchFakes so its
// restore wins.
func recordPreflightedBinary(t *testing.T, into *[]string) {
	t.Helper()
	prev := lookAgentBinary
	lookAgentBinary = func(name string) (string, error) {
		*into = append(*into, name)
		return "/usr/bin/" + name, nil
	}
	t.Cleanup(func() { lookAgentBinary = prev })
}

// binaryFor is the binary the given agent's declaration says niwa launches.
// Reading it from the declaration rather than typing "codex" here keeps these
// tests pinned to the table instead of to a name this file chose.
func binaryFor(t *testing.T, ag agent.Agent) string {
	t.Helper()
	spec, ok := dispatchLaunchSpec(ag)
	if !ok {
		t.Skipf("no launch spec declared for %q, so there is nothing to select", ag)
	}
	return spec.Binary
}

// TestDispatch_LaunchAgentFlagSelectsTheLaunchedAgent is the flag's reason to
// exist. Before it, the only ways to pick the launched agent were an
// environment variable and a config file; the flag's slot in resolveSessionAgent
// existed but was fed "" from dispatch's one call site.
func TestDispatch_LaunchAgentFlagSelectsTheLaunchedAgent(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	var preflighted []string
	recordPreflightedBinary(t, &preflighted)
	dispatchDetach = true
	t.Setenv("NIWA_AGENT", "")
	dispatchLaunchAgent = "codex"

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	want := binaryFor(t, agent.AgentCodex)
	if len(preflighted) == 0 || preflighted[0] != want {
		t.Fatalf("--%s codex preflighted %v, want the codex binary %q", launchAgentFlagName, preflighted, want)
	}
	if f.provisionCalled != 1 {
		t.Fatalf("provision called %d times, want 1", f.provisionCalled)
	}
}

// TestDispatch_LaunchAgentFlagOutranksEveryConfiguredSource pins the top of the
// precedence ladder. Every rung below the flag says codex; the flag says claude
// and wins.
func TestDispatch_LaunchAgentFlagOutranksEveryConfiguredSource(t *testing.T) {
	root := setupDispatchWorkspace(t)
	setDispatchWorkspaceDefaultAgent(t, root, "codex")
	chdir(t, root)
	setHostConfig(t, "[global]\ndefault_agent = \"codex\"\n")
	installDispatchFakes(t, root)
	var preflighted []string
	recordPreflightedBinary(t, &preflighted)
	dispatchDetach = true
	t.Setenv("NIWA_AGENT", "codex")
	dispatchLaunchAgent = "claude"

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	want := binaryFor(t, agent.AgentClaude)
	if len(preflighted) == 0 || preflighted[0] != want {
		t.Fatalf("--%s claude preflighted %v with every other source set to codex, want %q", launchAgentFlagName, preflighted, want)
	}
}

// TestDispatch_HostDefaultAgentSelectsTheLaunchedAgent covers the rung
// `niwa config set default-agent` writes: the developer's own machine-wide
// preference, honored when no more specific source states one.
func TestDispatch_HostDefaultAgentSelectsTheLaunchedAgent(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "[global]\ndefault_agent = \"codex\"\n")
	installDispatchFakes(t, root)
	var preflighted []string
	recordPreflightedBinary(t, &preflighted)
	dispatchDetach = true
	t.Setenv("NIWA_AGENT", "")

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	want := binaryFor(t, agent.AgentCodex)
	if len(preflighted) == 0 || preflighted[0] != want {
		t.Fatalf("host default_agent = codex preflighted %v, want %q", preflighted, want)
	}
}

// TestDispatch_WorkspaceDefaultOutranksHostDefault holds the ordering choice
// that matters most: a workspace that states an agent keeps launching it, and a
// personal machine-wide setting does not quietly override it.
func TestDispatch_WorkspaceDefaultOutranksHostDefault(t *testing.T) {
	root := setupDispatchWorkspace(t)
	setDispatchWorkspaceDefaultAgent(t, root, "claude")
	chdir(t, root)
	setHostConfig(t, "[global]\ndefault_agent = \"codex\"\n")
	installDispatchFakes(t, root)
	var preflighted []string
	recordPreflightedBinary(t, &preflighted)
	dispatchDetach = true
	t.Setenv("NIWA_AGENT", "")

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	want := binaryFor(t, agent.AgentClaude)
	if len(preflighted) == 0 || preflighted[0] != want {
		t.Fatalf("workspace default_agent = claude with host default_agent = codex preflighted %v, want %q", preflighted, want)
	}
}

// TestDispatch_UnknownLaunchAgentRefusesBeforeAnythingExists keeps the flag on
// the same closed set as every other source, and keeps the refusal ahead of
// provisioning so a typo leaves no instance behind.
func TestDispatch_UnknownLaunchAgentRefusesBeforeAnythingExists(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	dispatchDetach = true
	t.Setenv("NIWA_AGENT", "")
	dispatchLaunchAgent = "gemini"

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatalf("--%s gemini succeeded, want a refusal", launchAgentFlagName)
	}
	for _, want := range agent.All() {
		if !strings.Contains(err.Error(), string(want)) {
			t.Errorf("refusal %q does not name the accepted value %q", err, want)
		}
	}
	if f.provisionCalled != 0 {
		t.Fatalf("provision called %d times on an unknown agent, want 0", f.provisionCalled)
	}
}

// TestDispatch_UnknownHostDefaultAgentRefuses holds the host rung to the same
// validation as the rest. A machine-wide setting that names an agent niwa does
// not know is a mistake to report, not a value to skip past.
func TestDispatch_UnknownHostDefaultAgentRefuses(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "[global]\ndefault_agent = \"gemini\"\n")
	f := installDispatchFakes(t, root)
	dispatchDetach = true
	t.Setenv("NIWA_AGENT", "")

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("an unknown host default_agent dispatched successfully, want a refusal")
	}
	if !strings.Contains(err.Error(), "gemini") {
		t.Errorf("refusal %q does not name the rejected value", err)
	}
	if f.provisionCalled != 0 {
		t.Fatalf("provision called %d times on an unknown host default, want 0", f.provisionCalled)
	}
}

// TestDispatchHelpNamesNoSingleAgent is the guard for the failure the docs audit
// found: dispatch's own help said it launches one specific agent, named that
// agent's binary, and described that agent's attach behavior as universal. A
// reader who has just read the guide runs --help to confirm and takes the
// binary's word over the guide's, concluding the feature is not in their build.
//
// The package's AST scan does not catch this. It rejects a string literal that
// IS an agent name; these were agent names sitting inside a much longer literal,
// which is a different shape and reads exactly as authoritative.
//
// Both the agent names and the binary names come from the closed set and the
// launch declarations, so a third agent extends this guard rather than slipping
// past it.
func TestDispatchHelpNamesNoSingleAgent(t *testing.T) {
	var banned []string
	for _, ag := range agent.All() {
		banned = append(banned, string(ag))
		if spec, ok := dispatchLaunchSpec(ag); ok && spec.Binary != "" {
			banned = append(banned, spec.Binary)
		}
	}

	for _, field := range []struct{ name, text string }{
		{"Short", dispatchCmd.Short},
		{"Long", dispatchCmd.Long},
	} {
		lowered := strings.ToLower(field.text)
		for _, name := range banned {
			if strings.Contains(lowered, strings.ToLower(name)) {
				t.Errorf("dispatch's %s names %q; it describes whichever agent the resolution picks, so it must name none of them:\n%s",
					field.name, name, field.text)
			}
		}
	}

	// And it does say how the agent is picked, since --help is where the reader
	// who is about to run an unfamiliar command goes looking.
	if !strings.Contains(dispatchCmd.Long, launchAgentFlagName) {
		t.Errorf("dispatch's Long never mentions --%s, so nothing in the command's own help says the agent is selectable", launchAgentFlagName)
	}
	if !strings.Contains(dispatchCmd.Long, "NIWA_AGENT") {
		t.Error("dispatch's Long never mentions NIWA_AGENT, a rung of the resolution it describes")
	}
}

// TestLaunchAgentFlagIsRegisteredAndDistinctFromAgent guards the collision this
// flag was named around. --agent forwards a subagent type INTO the launched
// agent; --launch-agent picks which agent is launched. They are different
// settings, so they stay different flags bound to different variables.
func TestLaunchAgentFlagIsRegisteredAndDistinctFromAgent(t *testing.T) {
	launch := dispatchCmd.Flags().Lookup(launchAgentFlagName)
	if launch == nil {
		t.Fatalf("niwa dispatch has no --%s flag", launchAgentFlagName)
	}
	subagent := dispatchCmd.Flags().Lookup("agent")
	if subagent == nil {
		t.Fatal("niwa dispatch lost its --agent subagent-type passthrough")
	}
	if launch.Usage == subagent.Usage {
		t.Fatal("--agent and --launch-agent carry the same help text; the two settings must read as different things")
	}
	// The accepted values come from the closed set, so a third agent updates
	// the help line instead of leaving it quietly wrong.
	for _, ag := range agent.All() {
		if !strings.Contains(launch.Usage, string(ag)) {
			t.Errorf("--%s help does not name the accepted value %q: %q", launchAgentFlagName, ag, launch.Usage)
		}
	}
	// Every rung of the precedence has to be reachable from the command a
	// developer is already running, not only from a guide they have to know to
	// go looking for. So the help names all four sources.
	for _, rung := range []string{"NIWA_AGENT", "[workspace].default_agent", "[global].default_agent", "niwa config set default-agent"} {
		if !strings.Contains(launch.Usage, rung) {
			t.Errorf("--%s help does not name the %s rung of the precedence: %q", launchAgentFlagName, rung, launch.Usage)
		}
	}
}
