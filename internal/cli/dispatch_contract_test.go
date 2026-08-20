package cli

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestDispatchGateFollowsTheDeclaration ranges over every accepted agent and
// asserts the command does what the capability table says about it: an agent
// DispatchLaunch is implemented for dispatches, and one it is not refuses --
// quoting the declaration's own reason, before anything is provisioned.
//
// It is written against the table rather than against a named agent so it keeps
// meaning something as the table changes. When a row flips, this test changes
// which branch it takes for that agent and asserts the other half; it never
// needs editing to keep passing, and it fails immediately if the binary and the
// table disagree about who can be launched.
func TestDispatchGateFollowsTheDeclaration(t *testing.T) {
	for _, ag := range agent.All() {
		t.Run(string(ag), func(t *testing.T) {
			decl, err := agentplan.Lookup(agentplan.DispatchLaunch, ag)
			if err != nil {
				t.Fatalf("Lookup(dispatch-launch, %s): %v", ag, err)
			}

			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, "")
			f := installDispatchFakes(t, root)
			dispatchDetach = true
			t.Setenv("NIWA_AGENT", string(ag))

			_, _, runErr := runDispatchCmd(t, "do a thing")

			if decl.State == agentplan.StateImplemented {
				if runErr != nil {
					t.Fatalf("(%s) is declared implemented but dispatch failed: %v", ag, runErr)
				}
				if f.provisionCalled != 1 {
					t.Fatalf("(%s) is declared implemented but provision was called %d times", ag, f.provisionCalled)
				}
				return
			}

			if runErr == nil {
				t.Fatalf("(%s) is declared unavailable but dispatch succeeded", ag)
			}
			// The refusal carries the declaration's reason verbatim. That is
			// what stops the message a developer hits and the gap list they
			// read from drifting into two different explanations.
			if decl.Reason == "" {
				t.Fatalf("(%s) is declared unavailable with no reason; the contract suite should have caught this", ag)
			}
			if !strings.Contains(runErr.Error(), decl.Reason) {
				t.Errorf("(%s) refusal does not carry the declared reason.\n got: %v\nwant it to contain: %s", ag, runErr, decl.Reason)
			}
			// And it says what to do instead, naming an agent the table says
			// can be launched rather than one this code decided on. A refusal
			// is read by the person who does not know the answer.
			for _, launchable := range agentplan.LaunchableAgents() {
				if !strings.Contains(runErr.Error(), string(launchable)) {
					t.Errorf("(%s) refusal does not name %q, which the table says can be launched.\n got: %v", ag, launchable, runErr)
				}
			}
			// And it refuses before anything exists on disk, which is the
			// ordering the binary preflight depends on too.
			if f.provisionCalled != 0 {
				t.Errorf("(%s) is declared unavailable but provision was called %d times", ag, f.provisionCalled)
			}
		})
	}
}

// TestDispatchUsesTheResolvedAgentsSpec is the test the three below cannot be.
//
// Each of them resolves its expected value from the one agent niwa ships a
// launch description for, so each proves the dispatch reads *a* description. If
// the code hardcoded that agent instead of resolving one, every one of them
// would still pass, and the difference between reading a description and
// reading the resolved agent's description is the whole feature. So this one
// substitutes a description for an agent that does not exist, shaped unlike
// anything niwa ships, and asserts the dispatch follows it -- the same move the
// capture suite makes with its second fixture store.
func TestDispatchUsesTheResolvedAgentsSpec(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	dispatchDetach = false

	invented := agentplan.LaunchSpec{
		Binary:      "invented-agent",
		LeadingArgs: []string{"start"},
		Flags:       agentplan.LaunchFlags{Model: "--pick"},
		ResumeArgs:  []string{"reopen", "--by-id"},
		HintVerbs:   []string{"reopen", "kill"},
	}

	var askedFor []agent.Agent
	prevSpec := dispatchLaunchSpec
	dispatchLaunchSpec = func(ag agent.Agent) (agentplan.LaunchSpec, bool) {
		askedFor = append(askedFor, ag)
		return invented, true
	}
	t.Cleanup(func() { dispatchLaunchSpec = prevSpec })

	var preflighted []string
	prevLook := lookAgentBinary
	lookAgentBinary = func(name string) (string, error) {
		preflighted = append(preflighted, name)
		return "/usr/bin/" + name, nil
	}
	t.Cleanup(func() { lookAgentBinary = prevLook })

	var resumeSpec agentplan.LaunchSpec
	prevAttach := dispatchAttach
	dispatchAttach = func(spec agentplan.LaunchSpec, _ string) error {
		resumeSpec = spec
		return nil
	}
	t.Cleanup(func() { dispatchAttach = prevAttach })

	stdout, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// The description was asked for by the agent the workspace resolved, not by
	// a name this code chose.
	wantAgent, err := resolveSessionAgent("", nil)
	if err != nil {
		t.Fatalf("resolving the session agent: %v", err)
	}
	if len(askedFor) == 0 || askedFor[0] != wantAgent {
		t.Fatalf("the launch description was resolved for %v, want the session's agent %q", askedFor, wantAgent)
	}

	// And every surface downstream followed it rather than the real table.
	if len(preflighted) == 0 || preflighted[0] != invented.Binary {
		t.Errorf("preflight looked up %v, want the substituted binary %q", preflighted, invented.Binary)
	}
	for _, verb := range invented.HintVerbs {
		if want := invented.Binary + " " + verb + " "; !strings.Contains(stdout, want) {
			t.Errorf("output does not offer %q.\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "claude ") {
		t.Errorf("output offers a command from the real table rather than the substituted one.\n%s", stdout)
	}
	if resumeSpec.Binary != invented.Binary || strings.Join(resumeSpec.ResumeArgs, " ") != strings.Join(invented.ResumeArgs, " ") {
		t.Errorf("resume ran %q %v, want the substituted %q %v", resumeSpec.Binary, resumeSpec.ResumeArgs, invented.Binary, invented.ResumeArgs)
	}
}

// TestDispatchPreflightsTheDeclaredBinary asserts the preflight looks up the
// binary the launched agent's declaration names, not a binary this code knows
// about. The ordering it runs in -- before any instance is created -- is pinned
// separately by TestDispatch_ClaudeNotOnPath_Errors; this pins what it asks for.
func TestDispatchPreflightsTheDeclaredBinary(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	dispatchDetach = true

	var asked []string
	prev := lookAgentBinary
	lookAgentBinary = func(name string) (string, error) {
		asked = append(asked, name)
		return "/usr/bin/" + name, nil
	}
	t.Cleanup(func() { lookAgentBinary = prev })

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	_ = f

	spec, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for the default agent")
	}
	if len(asked) == 0 || asked[0] != spec.Binary {
		t.Fatalf("preflight looked up %v, want the declared binary %q first", asked, spec.Binary)
	}
}

// TestDispatchMappingRecordsTheLaunchingAgent asserts the durable mapping says
// which agent's session it describes. Everything downstream -- the reaper's
// choice of liveness rule, and any later step back into the session -- reads it
// there rather than inferring it from the shape of an id.
func TestDispatchMappingRecordsTheLaunchingAgent(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	dispatchDetach = true

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	m, err := workspace.ReadSessionMapping(root, dispatchTestSessionID)
	if err != nil {
		t.Fatalf("reading the mapping: %v", err)
	}
	if m.Agent != string(agent.AgentClaude) {
		t.Errorf("mapping records agent %q, want %q", m.Agent, agent.AgentClaude)
	}
}

// TestDispatchHintsComeFromTheDeclaration asserts the management commands niwa
// prints are the launched agent's own verbs against its own binary. niwa keeps
// no list of them, so it can never offer a developer a command their binary
// does not have.
func TestDispatchHintsComeFromTheDeclaration(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	dispatchDetach = true

	stdout, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	spec, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for the default agent")
	}
	if len(spec.HintVerbs) == 0 {
		t.Fatal("the launch spec names no management verbs")
	}
	for _, verb := range spec.HintVerbs {
		want := spec.Binary + " " + verb + " "
		if !strings.Contains(stdout, want) {
			t.Errorf("output does not offer %q.\n%s", want, stdout)
		}
	}
}

// TestDispatchResumeUsesTheDeclaredVerb asserts the final step-into-the-session
// runs the agent's own resume verb against the handle capture recovered, and
// that everything around it -- the lookup, the handle, the non-fatal outcome --
// is the same code whoever is launched.
func TestDispatchResumeUsesTheDeclaredVerb(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	dispatchDetach = false

	var gotSpec agentplan.LaunchSpec
	var gotHandle string
	prev := dispatchAttach
	dispatchAttach = func(spec agentplan.LaunchSpec, handle string) error {
		gotSpec, gotHandle = spec, handle
		return nil
	}
	t.Cleanup(func() { dispatchAttach = prev })

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	want, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for the default agent")
	}
	if gotSpec.Binary != want.Binary {
		t.Errorf("resume ran against %q, want %q", gotSpec.Binary, want.Binary)
	}
	if strings.Join(gotSpec.ResumeArgs, " ") != strings.Join(want.ResumeArgs, " ") {
		t.Errorf("resume args = %v, want %v", gotSpec.ResumeArgs, want.ResumeArgs)
	}
	if gotHandle == "" {
		t.Error("resume was handed an empty handle")
	}
}
