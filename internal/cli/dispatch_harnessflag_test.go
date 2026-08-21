package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
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

// TestDispatch_HarnessFlagSelectsTheLaunchedAgent is the flag's reason to
// exist. Before it, the only ways to pick the launched agent were an
// environment variable and a config file; the flag's slot in resolveSessionAgent
// existed but was fed "" from dispatch's one call site.
func TestDispatch_HarnessFlagSelectsTheLaunchedAgent(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	var preflighted []string
	recordPreflightedBinary(t, &preflighted)
	dispatchDetach = true
	t.Setenv("NIWA_DISPATCH_HARNESS", "")
	dispatchHarness = "codex"

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	want := binaryFor(t, agent.AgentCodex)
	if len(preflighted) == 0 || preflighted[0] != want {
		t.Fatalf("--%s codex preflighted %v, want the codex binary %q", harnessFlagName, preflighted, want)
	}
	if f.provisionCalled != 1 {
		t.Fatalf("provision called %d times, want 1", f.provisionCalled)
	}
}

// TestDispatch_HarnessFlagOutranksEveryConfiguredSource pins the top of the
// precedence ladder. Every rung below the flag says codex; the flag says claude
// and wins.
func TestDispatch_HarnessFlagOutranksEveryConfiguredSource(t *testing.T) {
	root := setupDispatchWorkspace(t)
	setDispatchWorkspaceDefaultAgent(t, root, "codex")
	chdir(t, root)
	setHostConfig(t, "[global]\ndefault_dispatch_harness = \"codex\"\n")
	installDispatchFakes(t, root)
	var preflighted []string
	recordPreflightedBinary(t, &preflighted)
	dispatchDetach = true
	t.Setenv("NIWA_DISPATCH_HARNESS", "codex")
	dispatchHarness = "claude"

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	want := binaryFor(t, agent.AgentClaude)
	if len(preflighted) == 0 || preflighted[0] != want {
		t.Fatalf("--%s claude preflighted %v with every other source set to codex, want %q", harnessFlagName, preflighted, want)
	}
}

// TestDispatch_HostDefaultHarnessSelectsTheLaunchedAgent covers the rung
// `niwa config set default-dispatch-harness` writes: the developer's own machine-wide
// preference, honored when no more specific source states one.
func TestDispatch_HostDefaultHarnessSelectsTheLaunchedAgent(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "[global]\ndefault_dispatch_harness = \"codex\"\n")
	installDispatchFakes(t, root)
	var preflighted []string
	recordPreflightedBinary(t, &preflighted)
	dispatchDetach = true
	t.Setenv("NIWA_DISPATCH_HARNESS", "")

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	want := binaryFor(t, agent.AgentCodex)
	if len(preflighted) == 0 || preflighted[0] != want {
		t.Fatalf("host default_dispatch_harness = codex preflighted %v, want %q", preflighted, want)
	}
}

// TestDispatch_WorkspaceDefaultOutranksHostDefault holds the ordering choice
// that matters most: a workspace that states an agent keeps launching it, and a
// personal machine-wide setting does not quietly override it.
func TestDispatch_WorkspaceDefaultOutranksHostDefault(t *testing.T) {
	root := setupDispatchWorkspace(t)
	setDispatchWorkspaceDefaultAgent(t, root, "claude")
	chdir(t, root)
	setHostConfig(t, "[global]\ndefault_dispatch_harness = \"codex\"\n")
	installDispatchFakes(t, root)
	var preflighted []string
	recordPreflightedBinary(t, &preflighted)
	dispatchDetach = true
	t.Setenv("NIWA_DISPATCH_HARNESS", "")

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	want := binaryFor(t, agent.AgentClaude)
	if len(preflighted) == 0 || preflighted[0] != want {
		t.Fatalf("workspace default_agent = claude with host default_dispatch_harness = codex preflighted %v, want %q", preflighted, want)
	}
}

// TestDispatch_UnknownHarnessRefusesBeforeAnythingExists keeps the flag on
// the same closed set as every other source, and keeps the refusal ahead of
// provisioning so a typo leaves no instance behind.
func TestDispatch_UnknownHarnessRefusesBeforeAnythingExists(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	dispatchDetach = true
	t.Setenv("NIWA_DISPATCH_HARNESS", "")
	dispatchHarness = "gemini"

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatalf("--%s gemini succeeded, want a refusal", harnessFlagName)
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

// TestDispatch_UnknownHostDefaultHarnessRefuses holds the host rung to the same
// validation as the rest. A machine-wide setting that names an agent niwa does
// not know is a mistake to report, not a value to skip past.
func TestDispatch_UnknownHostDefaultHarnessRefuses(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "[global]\ndefault_dispatch_harness = \"gemini\"\n")
	f := installDispatchFakes(t, root)
	dispatchDetach = true
	t.Setenv("NIWA_DISPATCH_HARNESS", "")

	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("an unknown host default_dispatch_harness dispatched successfully, want a refusal")
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
	if !strings.Contains(dispatchCmd.Long, harnessFlagName) {
		t.Errorf("dispatch's Long never mentions --%s, so nothing in the command's own help says the agent is selectable", harnessFlagName)
	}
	if !strings.Contains(dispatchCmd.Long, "NIWA_DISPATCH_HARNESS") {
		t.Error("dispatch's Long never mentions NIWA_DISPATCH_HARNESS, a rung of the resolution it describes")
	}
}

// TestHarnessFlagIsRegisteredAndDistinctFromAgent guards the collision this
// flag was named around. --agent forwards a subagent type INTO the launched
// agent; --harness picks which agent is launched. They are different
// settings, so they stay different flags bound to different variables.
func TestHarnessFlagIsRegisteredAndDistinctFromAgent(t *testing.T) {
	launch := dispatchCmd.Flags().Lookup(harnessFlagName)
	if launch == nil {
		t.Fatalf("niwa dispatch has no --%s flag", harnessFlagName)
	}
	subagent := dispatchCmd.Flags().Lookup("agent")
	if subagent == nil {
		t.Fatal("niwa dispatch lost its --agent subagent-type passthrough")
	}
	if launch.Usage == subagent.Usage {
		t.Fatal("--agent and --harness carry the same help text; the two settings must read as different things")
	}
	// The accepted values come from the closed set, so a third agent updates
	// the help line instead of leaving it quietly wrong.
	for _, ag := range agent.All() {
		if !strings.Contains(launch.Usage, string(ag)) {
			t.Errorf("--%s help does not name the accepted value %q: %q", harnessFlagName, ag, launch.Usage)
		}
	}
	// Every rung of the precedence has to be reachable from the command a
	// developer is already running, not only from a guide they have to know to
	// go looking for. So the help names all four sources.
	for _, rung := range []string{"NIWA_DISPATCH_HARNESS", "[workspace].default_agent", "[global].default_dispatch_harness", "niwa config set default-dispatch-harness"} {
		if !strings.Contains(launch.Usage, rung) {
			t.Errorf("--%s help does not name the %s rung of the precedence: %q", harnessFlagName, rung, launch.Usage)
		}
	}
}

// TestLaunchableAgentsHintOffersTheFlagFirst holds the refusal to the surface
// the developer is standing on. The hint predates --harness and still said
// only "Set NIWA_DISPATCH_HARNESS=<agent>", which changes every niwa command in the shell
// and everything dispatched from it -- a wide answer to someone retrying one
// command. The flag is named first, the variable second, and both are there.
//
// The agent names come from the declarations, so a row that flips changes what
// the refusal offers instead of leaving a name behind that no longer launches.
func TestLaunchableAgentsHintOffersTheFlagFirst(t *testing.T) {
	launchable := agentplan.LaunchableAgents()
	if len(launchable) == 0 {
		t.Skip("no launchable agent, so the hint has nothing to point at")
	}

	hint := launchableAgentsHint()
	if !strings.Contains(hint, "--"+harnessFlagName) {
		t.Errorf("the refusal never mentions --%s, the rung that changes only the command being retried: %q", harnessFlagName, hint)
	}
	if !strings.Contains(hint, "NIWA_DISPATCH_HARNESS") {
		t.Errorf("the refusal dropped NIWA_DISPATCH_HARNESS, which is still a way to set the agent: %q", hint)
	}
	if flagAt, envAt := strings.Index(hint, "--"+harnessFlagName), strings.Index(hint, "NIWA_DISPATCH_HARNESS"); flagAt > envAt {
		t.Errorf("the refusal offers the whole shell before the one command: %q", hint)
	}
	for _, ag := range launchable {
		if !strings.Contains(hint, string(ag)) {
			t.Errorf("the refusal does not name the launchable agent %q: %q", ag, hint)
		}
	}
}

// TestDispatch_UnreadableWorkspaceConfigSaysTheRungWasSkipped covers the one
// resolution outcome that is right about its inputs and wrong about the
// developer's: a workspace config that states an agent and does not parse. The
// rung is not overridden and not empty, it is skipped, so the dispatch launches
// whatever the broader rungs say while the file the developer would point at
// says something else.
//
// The dispatch still runs -- this is a notice, not a refusal, and the config
// failure has its own consequences further along -- but the agent question is
// answered before those, so it is answered out loud.
func TestDispatch_UnreadableWorkspaceConfigSaysTheRungWasSkipped(t *testing.T) {
	root := setupDispatchWorkspace(t)
	cfgPath := filepath.Join(root, config.ConfigDir, config.ConfigFile)
	// The agent the developer wrote, and a syntax error below it. TOML decoding
	// is all-or-nothing, so default_agent goes with the file.
	broken := "[workspace]\nname = \"test-ws\"\ndefault_agent = \"codex\"\nthis line is not toml\n"
	if err := os.WriteFile(cfgPath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	var preflighted []string
	recordPreflightedBinary(t, &preflighted)
	dispatchDetach = true
	t.Setenv("NIWA_DISPATCH_HARNESS", "")

	_, stderr, _ := runDispatchCmd(t, "do a thing")

	// The premise: the stated agent really was dropped. Without this the notice
	// could be about a rung that was honored anyway.
	if want := binaryFor(t, agent.AgentClaude); len(preflighted) == 0 || preflighted[0] != want {
		t.Fatalf("preflighted %v with an unreadable workspace config; this test needs the dispatch to have fallen through to %q", preflighted, want)
	}
	if !strings.Contains(stderr, cfgPath) {
		t.Errorf("the dispatch never named the config it could not read; a developer looking for why codex was not launched has no file to open.\nstderr: %s", stderr)
	}
	if !strings.Contains(stderr, "default_agent") {
		t.Errorf("the notice does not say what was dropped, so it reads as a config problem rather than an agent-selection one.\nstderr: %s", stderr)
	}
}

// TestDispatchHarnessSurfaceSpellings pins the two spellings a developer types
// that no other test can catch a change to, because every other test reaches
// them through the same constant or the same os.Getenv call that production
// uses. A rename of either is a break for every script and shell profile that
// sets it, so it has to be a deliberate edit to this test rather than a silent
// one somewhere else.
//
// The third surface, `niwa config set default-dispatch-harness`, is already
// pinned literally by TestConfigDefaultHarnessSubcommandsAreRegistered, and the
// TOML key by the round-trip in internal/config. The workspace rung keeps the
// older default_agent spelling on purpose -- see harnessFlagName's comment.
func TestDispatchHarnessSurfaceSpellings(t *testing.T) {
	t.Run("the flag is --harness", func(t *testing.T) {
		if harnessFlagName != "harness" {
			t.Fatalf("the selection flag is --%s, want --harness", harnessFlagName)
		}
		if dispatchCmd.Flags().Lookup("harness") == nil {
			t.Fatal("niwa dispatch has no --harness flag")
		}
	})

	t.Run("the variable is NIWA_DISPATCH_HARNESS", func(t *testing.T) {
		t.Setenv("NIWA_DISPATCH_HARNESS", "codex")
		got, err := resolveSessionAgent("", nil, "", nil)
		if err != nil {
			t.Fatalf("resolving with NIWA_DISPATCH_HARNESS=codex: %v", err)
		}
		if got != agent.AgentCodex {
			t.Fatalf("NIWA_DISPATCH_HARNESS=codex resolved to %q, want codex", got)
		}
	})

	t.Run("the old NIWA_AGENT spelling is not read", func(t *testing.T) {
		// Renamed rather than aliased, deliberately: carrying a permanent
		// fallback for a variable one release old would preserve the split
		// the rename exists to close. A reader who reintroduces the alias
		// should have to delete this, not discover it by accident.
		t.Setenv("NIWA_DISPATCH_HARNESS", "")
		t.Setenv("NIWA_AGENT", "codex")
		got, err := resolveSessionAgent("", nil, "", nil)
		if err != nil {
			t.Fatalf("resolving with only NIWA_AGENT set: %v", err)
		}
		if got != agent.AgentClaude {
			t.Fatalf("NIWA_AGENT=codex still selected %q; the variable was renamed to NIWA_DISPATCH_HARNESS", got)
		}
	})
}
