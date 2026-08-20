package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/workspace"
)

const (
	resumeSessionID      = "01e00000-0000-7000-8000-00000000beef"
	otherResumeSessionID = "01f00000-0000-7000-8000-00000000f00d"
)

// runListHuman drives the real list command in its human-output mode from the
// workspace root.
func runListHuman(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	prevJSON := listJSON
	listJSON = false
	t.Cleanup(func() { listJSON = prevJSON })
	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	return out.String()
}

// wantResumeFor builds the command an agent's own declaration says reaches a
// session. Reading it from the launch spec rather than typing "codex resume"
// keeps this test pinned to the declaration, which is where the binary and the
// verb are decided.
func wantResumeFor(t *testing.T, ag agent.Agent, handle string) string {
	t.Helper()
	spec, ok := dispatchLaunchSpec(ag)
	if !ok {
		t.Skipf("no launch spec declared for %q", ag)
	}
	return strings.Join(append(append([]string{spec.Binary}, spec.ResumeArgs...), handle), " ")
}

// TestList_DispatchedInstanceCarriesItsResumeCommand is the defect: a
// dispatched session's handle is printed once and then exists only in the
// scrollback of the terminal that started it. For an agent that will not hand
// over a session mid-turn, that terminal never attached, so resuming later is
// the only way the session is ever used -- and niwa list, the command a
// developer already runs to see what is here, said nothing about it.
func TestList_DispatchedInstanceCarriesItsResumeCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	instance := seedInstance(t, root, "test-ws+task-aaaa1111", 1)
	chdir(t, root)

	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    resumeSessionID,
		InstanceName: "test-ws+task-aaaa1111",
		InstancePath: instance,
		Agent:        string(agent.AgentCodex),
		Handle:       resumeSessionID,
		Ephemeral:    true,
		Origin:       "dispatch",
	}); err != nil {
		t.Fatal(err)
	}

	stdout := runListHuman(t)
	want := wantResumeFor(t, agent.AgentCodex, resumeSessionID)
	if !strings.Contains(stdout, want) {
		t.Fatalf("a dispatched instance listed with no way to reach its session; want %q in:\n%s", want, stdout)
	}
}

// TestList_ResumeCommandComesFromTheRecordedAgent holds the part that would
// otherwise rot: the command is built from the agent the mapping recorded, not
// from a name list.go decided on. Two instances, two agents, two different
// binaries and verbs.
func TestList_ResumeCommandComesFromTheRecordedAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	codexInstance := seedInstance(t, root, "test-ws+codex-aaaa1111", 1)
	claudeInstance := seedInstance(t, root, "test-ws+claude-bbbb2222", 2)
	chdir(t, root)

	const claudeHandle = "01f00000"
	for _, m := range []workspace.SessionMapping{
		{
			SessionID: resumeSessionID, InstanceName: "test-ws+codex-aaaa1111",
			InstancePath: codexInstance, Agent: string(agent.AgentCodex),
			Handle: resumeSessionID, Ephemeral: true, Origin: "dispatch",
		},
		{
			SessionID: otherResumeSessionID, InstanceName: "test-ws+claude-bbbb2222",
			InstancePath: claudeInstance, Agent: string(agent.AgentClaude),
			Handle: claudeHandle, Ephemeral: true, Origin: "dispatch",
		},
	} {
		if err := workspace.WriteSessionMapping(root, m); err != nil {
			t.Fatal(err)
		}
	}

	stdout := runListHuman(t)
	for _, want := range []string{
		wantResumeFor(t, agent.AgentCodex, resumeSessionID),
		wantResumeFor(t, agent.AgentClaude, claudeHandle),
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("want %q in list output:\n%s", want, stdout)
		}
	}
}

// TestList_ResumeCommandUsesTheHandleNotTheSessionID pins the difference the
// mapping now records. One agent's management verbs reject the full session id
// and take the record directory's name instead, so a command built from the id
// would be a command that fails at the binary.
func TestList_ResumeCommandUsesTheHandleNotTheSessionID(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	instance := seedInstance(t, root, "test-ws+task-aaaa1111", 1)
	chdir(t, root)

	const handle = "01e00000"
	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    resumeSessionID,
		InstanceName: "test-ws+task-aaaa1111",
		InstancePath: instance,
		Agent:        string(agent.AgentClaude),
		Handle:       handle,
		Ephemeral:    true,
		Origin:       "dispatch",
	}); err != nil {
		t.Fatal(err)
	}

	stdout := runListHuman(t)
	if !strings.Contains(stdout, wantResumeFor(t, agent.AgentClaude, handle)) {
		t.Fatalf("the resume command must be built from the recorded handle, got:\n%s", stdout)
	}
	if strings.Contains(stdout, resumeSessionID) {
		t.Fatalf("the resume command used the session id, which this agent's verbs reject:\n%s", stdout)
	}
}

// TestList_LegacyMappingOffersNothingItCannotBack covers a mapping written
// before the handle was recorded. Where the declaration says the session id is
// the handle it still works; where it does not, niwa has no handle and must
// print nothing rather than a command that fails.
func TestList_LegacyMappingOffersNothingItCannotBack(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	codexInstance := seedInstance(t, root, "test-ws+codex-aaaa1111", 1)
	claudeInstance := seedInstance(t, root, "test-ws+claude-bbbb2222", 2)
	chdir(t, root)

	for _, m := range []workspace.SessionMapping{
		{
			SessionID: resumeSessionID, InstanceName: "test-ws+codex-aaaa1111",
			InstancePath: codexInstance, Agent: string(agent.AgentCodex),
			Ephemeral: true, Origin: "dispatch",
		},
		{
			SessionID: otherResumeSessionID, InstanceName: "test-ws+claude-bbbb2222",
			InstancePath: claudeInstance, Agent: string(agent.AgentClaude),
			Ephemeral: true, Origin: "dispatch",
		},
	} {
		if err := workspace.WriteSessionMapping(root, m); err != nil {
			t.Fatal(err)
		}
	}

	stdout := runListHuman(t)
	if !strings.Contains(stdout, wantResumeFor(t, agent.AgentCodex, resumeSessionID)) {
		t.Errorf("a legacy mapping for an agent whose handle IS the session id still has a reachable session, got:\n%s", stdout)
	}
	if strings.Contains(stdout, otherResumeSessionID) {
		t.Errorf("a legacy mapping with no handle must not be offered a command built from the session id, got:\n%s", stdout)
	}
}

// TestList_UnmappedInstanceStaysAPlainLine keeps the new line bound to
// dispatched sessions: an ordinary instance lists exactly as it did.
func TestList_UnmappedInstanceStaysAPlainLine(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	seedInstance(t, root, "test-ws-1", 1)
	chdir(t, root)

	stdout := runListHuman(t)
	if strings.Contains(stdout, "resume:") {
		t.Fatalf("an instance with no session mapping was offered a resume command:\n%s", stdout)
	}
}

// TestList_JSONShapeIsUnchanged holds the machine-readable contract. The resume
// command is human output; --json consumers iterate the documented keys and
// must not have to cope with a new one.
func TestList_JSONShapeIsUnchanged(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	instance := seedInstance(t, root, "test-ws+task-aaaa1111", 1)
	chdir(t, root)
	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    resumeSessionID,
		InstanceName: "test-ws+task-aaaa1111",
		InstancePath: instance,
		Agent:        string(agent.AgentCodex),
		Handle:       resumeSessionID,
		Ephemeral:    true,
		Origin:       "dispatch",
	}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	prevJSON := listJSON
	listJSON = true
	t.Cleanup(func() { listJSON = prevJSON })
	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if strings.Contains(out.String(), "resume") {
		t.Fatalf("--json grew a resume key: %s", out.String())
	}
}
