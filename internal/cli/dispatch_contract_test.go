package cli

import (
	"fmt"
	"os"
	"path/filepath"
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
//
// Which means only half of it runs today. Both agents are declared implemented
// for dispatch-launch, so every subtest takes the first branch and returns, and
// everything below it -- the reason quoted verbatim, the named alternative, the
// refusal landing before anything is provisioned -- is written and waiting
// rather than covered. It becomes live the day some agent's row is anything but
// implemented, which is the arrangement this is built for; until then, read a
// pass here as evidence about the dispatching half only.
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
			t.Setenv("NIWA_DISPATCH_HARNESS", string(ag))

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
		// So the resume assertion below reaches resume at all; whether an agent
		// hands over a running session is a different property with its own
		// test.
		ResumeDuringTurn: true,
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
	dispatchAttach = func(spec agentplan.LaunchSpec, _, _ string) error {
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
	wantAgent, err := resolveSessionAgent("", nil, "", nil)
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

// TestDispatchSharedHalfRunsForEveryAgent is the property behind "resume is one
// verb". Two resume implementations chosen by an if-statement at the call site
// is the failure mode, however tidy each half looks, and it is not something a
// per-agent test catches: each half would pass its own.
//
// So this runs one dispatch per launchable agent through the same code and
// asserts the parts that must not vary do not: the mapping is written and
// records that agent, the handle capture returned reaches resume unchanged, and
// resume runs against that agent's own binary and verb. What varies -- the
// binary, the verb, the shape of the handle -- comes from the declaration, and
// what does not vary is everything else.
func TestDispatchSharedHalfRunsForEveryAgent(t *testing.T) {
	launchable := agentplan.LaunchableAgents()
	if len(launchable) < 2 {
		t.Skipf("only %d launchable agent(s); the shared half cannot be shown to be shared", len(launchable))
	}

	for _, ag := range launchable {
		t.Run(string(ag), func(t *testing.T) {
			spec, ok := agentplan.For(ag).LaunchSpec()
			if !ok {
				t.Fatalf("no launch spec for %s", ag)
			}

			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, "")
			installDispatchFakes(t, root)
			dispatchDetach = false
			t.Setenv("NIWA_DISPATCH_HARNESS", string(ag))

			var resumeSpec agentplan.LaunchSpec
			var resumeHandle, resumeDir string
			prev := dispatchAttach
			dispatchAttach = func(s agentplan.LaunchSpec, handle, workdir string) error {
				resumeSpec, resumeHandle, resumeDir = s, handle, workdir
				return nil
			}
			t.Cleanup(func() { dispatchAttach = prev })

			// Force the two properties that decide whether the resume step runs
			// at all, for every agent, so the resume half runs and can be
			// compared across them. Both are real differences between agents
			// and both have their own tests -- whether an agent hands over a
			// running session, and whether its runner executes the turn in the
			// caller's terminal, which makes the run itself the way in and
			// leaves nothing to step into afterwards. Neither is this test's
			// subject, and letting either decide which agents reach the
			// assertions below would leave the shared half unchecked for
			// exactly the agent whose declaration differs. Everything else
			// about the spec is the agent's own, so the assertions still read
			// the real row.
			resumable := spec
			resumable.ResumeDuringTurn = true
			resumable.Runner = agentplan.RunnerSelfBackgrounding
			prevSpec := dispatchLaunchSpec
			dispatchLaunchSpec = func(agent.Agent) (agentplan.LaunchSpec, bool) { return resumable, true }
			t.Cleanup(func() { dispatchLaunchSpec = prevSpec })

			if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v", err)
			}

			// The shared half: one mapping, in one store, recording the agent.
			m, err := workspace.ReadSessionMapping(root, dispatchTestSessionID)
			if err != nil {
				t.Fatalf("reading the mapping: %v", err)
			}
			if m.Agent != string(ag) {
				t.Errorf("mapping records agent %q, want %q", m.Agent, ag)
			}
			// The handle rides the mapping too. It is not derivable from the
			// session id -- for one agent the two are different strings -- so a
			// later reader that has only the id cannot name a command that
			// reaches the session.
			if m.Handle != dispatchTestShortID {
				t.Errorf("mapping records handle %q, want the captured %q", m.Handle, dispatchTestShortID)
			}
			if !m.Ephemeral || m.Origin != "dispatch" {
				t.Errorf("mapping provenance differs by agent: ephemeral=%v origin=%q", m.Ephemeral, m.Origin)
			}

			// The handle capture returned reaches resume unchanged, and resume
			// runs in the instance the worker was launched in.
			if resumeHandle != dispatchTestShortID {
				t.Errorf("resume handle = %q, want the captured %q", resumeHandle, dispatchTestShortID)
			}
			if resumeDir == "" {
				t.Error("resume was not told which directory the session ran in")
			}

			// And the agent-specific half is the agent's own.
			if resumeSpec.Binary != spec.Binary {
				t.Errorf("resume ran %q, want %q", resumeSpec.Binary, spec.Binary)
			}
			if strings.Join(resumeSpec.ResumeArgs, " ") != strings.Join(spec.ResumeArgs, " ") {
				t.Errorf("resume args = %v, want %v", resumeSpec.ResumeArgs, spec.ResumeArgs)
			}
		})
	}
}

// TestDispatchWarnsWhenTheWorkerStartsUnoriented asserts the two runtime
// notices that make declared gaps observable at the moment they bite, and
// asserts each fires for exactly the agents the table says it is true of.
//
// A gap documented in a guide and invisible at runtime is a developer reading a
// plausible answer from an under-equipped worker and never learning why, or
// believing they armed a flag that was silently dropped. Both notices are
// triggered by a declaration rather than by a name, so neither can drift out of
// step with the table.
//
// The row this one follows changed with row 2's correction. It was row 2, when
// a root-launched worker received nothing at all; it is row 18 now, which is
// what says niwa writes no project layer at an instance root -- the skills, MCP
// servers and posture that ride it are what a root-launched worker still does
// not get. Orientation does reach it, and the notice no longer claims
// otherwise. Deriving the expectation from the table rather than restating it
// is what let this test point at the new row without inventing a new rule.
func TestDispatchWarnsWhenTheWorkerStartsUnoriented(t *testing.T) {
	for _, ag := range agentplan.LaunchableAgents() {
		t.Run(string(ag), func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, "")
			installDispatchFakes(t, root)
			dispatchDetach = true
			t.Setenv("NIWA_DISPATCH_HARNESS", string(ag))

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}

			// The gate is the payload layout's scope, not a declaration row
			// and not the agent's name. No row can express "delivered in a
			// repository, not at the root" -- the schema scopes rows by who
			// receives a capability and never by where from -- and a name
			// check here is the hardcoded branch the contract exists to
			// prevent. So the test asks the same predicate the code asks, and
			// a change to either side that leaves them disagreeing fails here.
			repoScoped := agentplan.ConfigDocRepoScoped(ag)
			warned := strings.Contains(stderr, "starts at the instance root")
			if repoScoped != warned {
				t.Errorf("(%s): config document repo-scoped=%v, warned=%v; the notice must follow the layout scope\n%s",
					ag, repoScoped, warned, stderr)
			}
			if warned {
				// The text has to name what is actually missing. Claiming
				// skills after row 18 delivers them would send a developer
				// looking for a gap that closed.
				for _, want := range []string{"MCP servers", "session environment", "approval and sandbox posture"} {
					if !strings.Contains(stderr, want) {
						t.Errorf("(%s): the notice does not name %q\n%s", ag, want, stderr)
					}
				}
				if strings.Contains(stderr, "skills written there, but not") == false {
					t.Errorf("(%s): the notice does not say skills DO reach the root\n%s", ag, stderr)
				}
			}
		})
	}
}

// TestDispatchWarnsWhenKeepAliveIsUndeliverable asserts a requested --keep-alive
// says something for an agent it cannot be delivered to. A flag that is
// silently ignored for one agent leaves a developer believing they armed
// something they did not, which is worse than a flag that is refused.
func TestDispatchWarnsWhenKeepAliveIsUndeliverable(t *testing.T) {
	for _, ag := range agentplan.LaunchableAgents() {
		decl, err := agentplan.Lookup(agentplan.DispatchKeepAlive, ag)
		if err != nil {
			t.Fatalf("Lookup(dispatch-keep-alive, %s): %v", ag, err)
		}
		if decl.State == agentplan.StateImplemented {
			continue
		}

		t.Run(string(ag), func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, "")
			installDispatchFakes(t, root)
			dispatchDetach = true
			asked := true
			dispatchKeepAlive = &asked
			t.Setenv("NIWA_DISPATCH_HARNESS", string(ag))

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if !strings.Contains(stderr, "--keep-alive does not apply") {
				t.Errorf("(%s): --keep-alive was requested and undeliverable, and nothing said so:\n%s", ag, stderr)
			}
			if !strings.Contains(stderr, decl.Reason) {
				t.Errorf("(%s): the notice does not carry the declared reason %q:\n%s", ag, decl.Reason, stderr)
			}
		})
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
	dispatchAttach = func(spec agentplan.LaunchSpec, handle, _ string) error {
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

// TestDispatchDoesNotResumeAnAgentThatRefusesMidTurn asserts the attach step is
// skipped, not attempted and recovered from, for an agent whose declaration
// says it will not hand over a session whose turn is still running.
//
// The distinction is the whole point. Attempting it and warning on the failure
// leaves the developer reading the agent's own words for the situation --
// against codex-cli 0.147.0, "thread-store conflict: thread <id> already has an
// active writer" -- at the end of a dispatch that in fact succeeded. The worker
// is running, the mapping is durable, the session is resumable in a minute. A
// dispatch that ends in a store-conflict error reads as a broken dispatch.
//
// It runs against the declaration rather than against an agent name: whichever
// agents declare false get this behavior, and an agent that later declares true
// gets the attach without this test changing.
func TestDispatchDoesNotResumeAnAgentThatRefusesMidTurn(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	dispatchDetach = false

	attached := false
	prevAttach := dispatchAttach
	dispatchAttach = func(agentplan.LaunchSpec, string, string) error {
		attached = true
		return nil
	}
	t.Cleanup(func() { dispatchAttach = prevAttach })

	// The workspace's own agent declares true, so substitute a spec that
	// declares false and change nothing else. Reading the flag from the spec
	// the dispatch resolved is what makes this a contract test rather than a
	// test of one agent's row.
	base, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for the default agent")
	}
	refusing := base
	refusing.ResumeDuringTurn = false
	prevSpec := dispatchLaunchSpec
	dispatchLaunchSpec = func(agent.Agent) (agentplan.LaunchSpec, bool) { return refusing, true }
	t.Cleanup(func() { dispatchLaunchSpec = prevSpec })

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if attached {
		t.Error("dispatch resumed a session its agent will not hand over mid-turn; the developer gets a store-conflict error at the end of a dispatch that worked")
	}
	if !strings.Contains(stderr, "still running") {
		t.Errorf("stderr = %q, want it to say the worker is still running; a terminal that silently stays put is indistinguishable from a failed attach", stderr)
	}

	// And the same dispatch with the flag set does attach, so the skip is
	// keyed on the declaration rather than on anything else about this path.
	attached = false
	allowing := base
	allowing.ResumeDuringTurn = true
	dispatchLaunchSpec = func(agent.Agent) (agentplan.LaunchSpec, bool) { return allowing, true }
	if _, _, err := runDispatchCmd(t, "do another thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !attached {
		t.Error("dispatch skipped the attach for an agent that declares it hands over a running session")
	}
}

// grantingSpec is a declaration for an agent niwa does not ship, carrying a
// workdir grant shaped like nothing in the real table. Every expectation below
// is written against it, so a test here cannot be satisfied by code that read
// the production table instead of the spec it was handed -- and cannot go green
// with the production table wrong.
func grantingSpec() agentplan.LaunchSpec {
	return agentplan.LaunchSpec{
		Binary:           "granting-agent",
		LeadingArgs:      []string{"start"},
		ResumeArgs:       []string{"reopen"},
		HintVerbs:        []string{"tail", "reopen"},
		WorkdirGrantArgs: []string{"--vouch", "dir=%q"},
		ResumeDuringTurn: true,
	}
}

// wantGrantFor is the grant argument pair a granting declaration produces for a
// directory: written out here, never asked of the code under test.
func wantGrantFor(dir string) []string { return []string{"--vouch", `dir="` + dir + `"`} }

// TestDispatchHintBlockCarriesTheGrant is the first of the three per-surface
// checks. Each one exists so removing the grant from ONE surface fails a test
// naming that surface -- a single check covering "some surface carries it"
// would stay green while two of the three regressed.
func TestDispatchHintBlockCarriesTheGrant(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	dispatchDetach = true

	spec := grantingSpec()
	prevSpec := dispatchLaunchSpec
	dispatchLaunchSpec = func(agent.Agent) (agentplan.LaunchSpec, bool) { return spec, true }
	t.Cleanup(func() { dispatchLaunchSpec = prevSpec })

	prevLook := lookAgentBinary
	lookAgentBinary = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	t.Cleanup(func() { lookAgentBinary = prevLook })

	stdout, _, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	instance := dispatchedInstancePath(t, stdout)
	wantResume := "granting-agent reopen --vouch " + shellToken(`dir="`+instance+`"`) + " " + dispatchTestShortID
	if !strings.Contains(stdout, wantResume) {
		t.Errorf("the printed hint block does not carry the grant.\nwant %q in:\n%s", wantResume, stdout)
	}
	// The verb that starts no session is printed exactly as it always was.
	if wantPlain := "granting-agent tail " + dispatchTestShortID; !strings.Contains(stdout, wantPlain) {
		t.Errorf("the non-resume hint changed shape.\nwant %q in:\n%s", wantPlain, stdout)
	}
}

// TestDispatchAttachCarriesTheGrant runs the REAL dispatchAttach body rather
// than the package variable tests usually replace, against a stub binary that
// records the argv it was given. Asserting through the seam would pass on a
// constructor unit test that never reaches the call site this is about.
func TestDispatchAttachCarriesTheGrant(t *testing.T) {
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	stub := filepath.Join(dir, "granting-agent")
	script := "#!/bin/sh\n: > " + argvFile + "\nfor a in \"$@\"; do printf '%s\\0' \"$a\" >> " + argvFile + "; done\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	prevLook := lookAgentBinary
	lookAgentBinary = func(name string) (string, error) { return filepath.Join(dir, name), nil }
	t.Cleanup(func() { lookAgentBinary = prevLook })

	workdir := t.TempDir()
	if err := dispatchAttach(grantingSpec(), dispatchTestShortID, workdir); err != nil {
		t.Fatalf("dispatchAttach: %v", err)
	}

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	want := append(append([]string{"reopen"}, wantGrantFor(workdir)...), dispatchTestShortID)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("the attach exec ran with argv\n  %#v\nwant\n  %#v", got, want)
	}
}

// dispatchedInstancePath recovers the instance directory dispatch printed, so a
// test can assert the grant names that directory rather than guessing at it.
func dispatchedInstancePath(t *testing.T, stdout string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if _, after, found := strings.Cut(line, "instance: "); found {
			return strings.TrimSpace(after)
		}
	}
	t.Fatalf("dispatch printed no instance path:\n%s", stdout)
	return ""
}

// TestDispatchAttachFailureFallbackCarriesTheGrant covers the third printed
// surface: the line dispatch prints when the attach it tried could not run.
//
// It gets its own test because it is the one a reader skims past. Reverting it
// to the old hand-joined form left the whole package suite green -- nothing
// else mentions it -- and a developer who lands there is by definition already
// having a bad time, so handing them a command that comes up read-only is the
// worst moment to do it.
func TestDispatchAttachFailureFallbackCarriesTheGrant(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	dispatchDetach = false

	spec := grantingSpec()
	prevSpec := dispatchLaunchSpec
	dispatchLaunchSpec = func(agent.Agent) (agentplan.LaunchSpec, bool) { return spec, true }
	t.Cleanup(func() { dispatchLaunchSpec = prevSpec })

	prevLook := lookAgentBinary
	lookAgentBinary = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	t.Cleanup(func() { lookAgentBinary = prevLook })

	prevAttach := dispatchAttach
	dispatchAttach = func(agentplan.LaunchSpec, string, string) error {
		return fmt.Errorf("the terminal could not be handed over")
	}
	t.Cleanup(func() { dispatchAttach = prevAttach })

	stdout, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	instance := dispatchedInstancePath(t, stdout)
	want := "granting-agent reopen --vouch " + shellToken(`dir="`+instance+`"`) + " " + dispatchTestShortID
	if !strings.Contains(stderr, want) {
		t.Errorf("the attach-failure fallback does not carry the grant.\nwant %q in:\n%s", want, stderr)
	}
}

// TestSessionResumeCommandFailsClosed pins the cases niwa list must answer with
// nothing rather than with a command that fails at the binary.
//
// Two of these guards used to live in sessionResumeCommand's own preamble and
// now live in the constructor it delegates to. The behaviour is the same; this
// is what makes that delegation something a later refactor cannot quietly
// undo.
func TestSessionResumeCommandFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mapping workspace.SessionMapping
		spec    agentplan.LaunchSpec
		ok      bool
	}{
		{"an agent that does not parse", workspace.SessionMapping{Agent: "not-an-agent", Handle: "h1", InstancePath: "/i"}, grantingSpec(), true},
		{"a spec that does not resolve", workspace.SessionMapping{Agent: string(agent.AgentCodex), Handle: "h1", InstancePath: "/i"}, agentplan.LaunchSpec{}, false},
		{"an empty binary", workspace.SessionMapping{Agent: string(agent.AgentCodex), Handle: "h1", InstancePath: "/i"}, agentplan.LaunchSpec{ResumeArgs: []string{"reopen"}}, true},
		{"a declaration naming no way back in", workspace.SessionMapping{Agent: string(agent.AgentCodex), Handle: "h1", InstancePath: "/i"}, agentplan.LaunchSpec{Binary: "granting-agent"}, true},
		{"no usable handle", workspace.SessionMapping{Agent: string(agent.AgentCodex), InstancePath: "/i"}, agentplan.LaunchSpec{Binary: "b", ResumeArgs: []string{"reopen"}, Records: agentplan.SessionRecords{Handle: agentplan.HandleRecordDir}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec, ok := tc.spec, tc.ok
			prev := dispatchLaunchSpec
			dispatchLaunchSpec = func(agent.Agent) (agentplan.LaunchSpec, bool) { return spec, ok }
			t.Cleanup(func() { dispatchLaunchSpec = prev })

			if got := sessionResumeCommand(tc.mapping); got != "" {
				t.Errorf("sessionResumeCommand = %q, want \"\"", got)
			}
		})
	}
}
