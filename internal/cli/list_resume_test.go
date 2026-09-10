package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
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

// The two declarations below are invented: they belong to no agent niwa ships,
// and every expectation in this file is written against them.
//
// The previous version of this helper built its expectation by reading the
// production launch table, which meant the assertion and the code under test
// were quoting the same source at each other. A table that lost the grant would
// have taken the expectation down with it and the suite would have stayed green
// -- the exact way an earlier change in this area kept passing while the
// production binary name was mutated.
var (
	listGrantSpec = agentplan.LaunchSpec{
		Binary:           "invented-granting-agent",
		ResumeArgs:       []string{"reopen"},
		WorkdirGrantArgs: []string{"--vouch", "dir=%q"},
		Records:          agentplan.SessionRecords{Handle: agentplan.HandleSessionID},
	}
	listPlainSpec = agentplan.LaunchSpec{
		Binary:     "invented-plain-agent",
		ResumeArgs: []string{"latch"},
		Records:    agentplan.SessionRecords{Handle: agentplan.HandleRecordDir},
	}
)

// useInventedSpecs points the list path at the two declarations above, keyed by
// the agent a mapping recorded -- so a test can still prove the command follows
// the RECORDED agent while asserting against values this file owns.
func useInventedSpecs(t *testing.T) {
	t.Helper()
	prev := dispatchLaunchSpec
	dispatchLaunchSpec = func(ag agent.Agent) (agentplan.LaunchSpec, bool) {
		switch ag {
		case agent.AgentCodex:
			return listGrantSpec, true
		case agent.AgentClaude:
			return listPlainSpec, true
		}
		return agentplan.LaunchSpec{}, false
	}
	t.Cleanup(func() { dispatchLaunchSpec = prev })
}

// wantGrantedResume is the literal command a granting declaration produces for a
// session in instanceDir: the resume verb, the grant with the directory quoted
// into it, then the handle. Written out here rather than derived.
func wantGrantedResume(instanceDir, handle string) string {
	return "invented-granting-agent reopen --vouch " +
		shellToken(`dir="`+instanceDir+`"`) + " " + handle
}

// TestList_DispatchedInstanceCarriesItsResumeCommand is the defect: a
// dispatched session's handle is printed once and then exists only in the
// scrollback of the terminal that started it. For an agent that will not hand
// over a session mid-turn, that terminal never attached, so resuming later is
// the only way the session is ever used -- and niwa list, the command a
// developer already runs to see what is here, said nothing about it.
func TestList_DispatchedInstanceCarriesItsResumeCommand(t *testing.T) {
	useInventedSpecs(t)
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
	want := wantGrantedResume(instance, resumeSessionID)
	if !strings.Contains(stdout, want) {
		t.Fatalf("a dispatched instance listed with no way to reach its session; want %q in:\n%s", want, stdout)
	}
}

// TestList_ResumeCommandComesFromTheRecordedAgent holds the part that would
// otherwise rot: the command is built from the agent the mapping recorded, not
// from a name list.go decided on. Two instances, two agents, two different
// binaries and verbs.
func TestList_ResumeCommandComesFromTheRecordedAgent(t *testing.T) {
	useInventedSpecs(t)
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
		// The granting declaration's command, carrying the grant for the
		// instance the codex-recorded mapping names...
		wantGrantedResume(codexInstance, resumeSessionID),
		// ...and the plain one's, which carries no grant because its
		// declaration asks for none. Same list, two declarations, keyed by
		// what the mapping recorded.
		"invented-plain-agent latch " + claudeHandle,
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
	useInventedSpecs(t)
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
	if !strings.Contains(stdout, "invented-plain-agent latch "+handle) {
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
	useInventedSpecs(t)
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
	if !strings.Contains(stdout, wantGrantedResume(codexInstance, resumeSessionID)) {
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
// command is human output only; --json consumers iterate the documented keys.
// The only key added to the documented record shape is the optional
// session_name, present only when a dispatch recorded a name, and this test's
// mapping records none (TestList_JSONShapeCarriesNoSessionNameWhenNoneRecorded
// checks that).
func TestList_JSONShapeIsUnchanged(t *testing.T) {
	useInventedSpecs(t)
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

// The tests below cover the session name niwa list shows. Every expected value
// is a literal, never one computed through the join or the pattern.

const (
	// thirdResumeSessionID sorts before both ids above.
	thirdResumeSessionID = "01d00000-0000-7000-8000-00000000cafe"
	// namedInstanceName is dispatch-shaped, so a join that derived a name from
	// the directory would produce a well-formed one and show it.
	namedInstanceName = "test-ws+review-4e33acfa"
)

// runListJSON drives the real list command with --json and returns the raw
// bytes and the decoded array.
func runListJSON(t *testing.T) ([]byte, []map[string]any) {
	t.Helper()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	prevJSON := listJSON
	listJSON = true
	t.Cleanup(func() { listJSON = prevJSON })
	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList --json: %v", err)
	}
	var recs []map[string]any
	if err := json.Unmarshal(out.Bytes(), &recs); err != nil {
		t.Fatalf("decoding list JSON: %v\n%s", err, out.String())
	}
	return out.Bytes(), recs
}

// jsonRecord returns the decoded record named name.
func jsonRecord(t *testing.T, recs []map[string]any, name string) map[string]any {
	t.Helper()
	for _, r := range recs {
		if r["name"] == name {
			return r
		}
	}
	t.Fatalf("no JSON record named %q in %v", name, recs)
	return nil
}

// assertConsecutiveLines asserts that want appear as adjacent lines of out, in
// order.
func assertConsecutiveLines(t *testing.T, out string, want ...string) {
	t.Helper()
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if line != want[0] {
			continue
		}
		for j, w := range want[1:] {
			if i+1+j >= len(lines) || lines[i+1+j] != w {
				t.Fatalf("line %d after %q is not %q:\n%s", j+1, want[0], w, out)
			}
		}
		return
	}
	t.Fatalf("no line %q in:\n%s", want[0], out)
}

// namedMapping is a dispatch-written mapping for the default agent carrying
// sessionName. A zero created is stamped to now by WriteSessionMapping.
func namedMapping(sessionID, instancePath, handle, sessionName string, created time.Time) workspace.SessionMapping {
	return workspace.SessionMapping{
		SessionID:    sessionID,
		InstanceName: filepath.Base(instancePath),
		InstancePath: instancePath,
		Agent:        string(agent.AgentClaude),
		Handle:       handle,
		Ephemeral:    true,
		Origin:       "dispatch",
		SessionName:  sessionName,
		Created:      created,
	}
}

func writeMapping(t *testing.T, root string, m workspace.SessionMapping) {
	t.Helper()
	if err := workspace.WriteSessionMapping(root, m); err != nil {
		t.Fatal(err)
	}
}

// TestList_SessionNameFromNewestMapping pins which mapping supplies the name
// when several point at one instance: the one created last, chosen before its
// name is checked, whichever order the store lists them in. The resume command
// keeps its own selection, the last mapping in session-id order.
func TestList_SessionNameFromNewestMapping(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	// otherResumeSessionID sorts last and carries handle 01f00000.
	const wantResume = "invented-plain-agent latch 01f00000"

	for _, tc := range []struct {
		name string
		// first is resumeSessionID's mapping, which the store lists first;
		// last is otherResumeSessionID's.
		firstCreated, lastCreated time.Time
		firstName, lastName       string
		want                      string
	}{
		{"newer_sorts_first", t1, t0, "newer-bbbbbbbb", "older-aaaaaaaa", "newer-bbbbbbbb"},
		{"newer_sorts_last", t0, t1, "older-aaaaaaaa", "newer-bbbbbbbb", "newer-bbbbbbbb"},
		{"newer_invalid_no_fallback", t1, t0, "review", "older-aaaaaaaa", ""},
		{"newer_empty_no_fallback", t0, t1, "older-aaaaaaaa", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useInventedSpecs(t)
			root := setupDispatchWorkspace(t)
			instance := seedInstance(t, root, namedInstanceName, 1)
			writeMapping(t, root, namedMapping(resumeSessionID, instance, "01e00000", tc.firstName, tc.firstCreated))
			writeMapping(t, root, namedMapping(otherResumeSessionID, instance, "01f00000", tc.lastName, tc.lastCreated))

			records, err := workspace.EnumerateInstanceRecords(root)
			if err != nil {
				t.Fatalf("enumerate: %v", err)
			}
			for _, r := range records {
				if r.SessionName != "" {
					t.Fatalf("EnumerateInstanceRecords filled SessionName %q; the list command owns it", r.SessionName)
				}
			}
			resume := annotateFromSessionMappings(records, root, t.TempDir(), time.Now())

			if len(records) != 1 {
				t.Fatalf("got %d records, want 1", len(records))
			}
			if records[0].SessionName != tc.want {
				t.Errorf("SessionName = %q, want %q", records[0].SessionName, tc.want)
			}
			if resume[instance] != wantResume {
				t.Errorf("resume = %q, want %q (its selection must not change)", resume[instance], wantResume)
			}
		})
	}
}

func TestList_SessionNameLineSitsBetweenNameAndResume(t *testing.T) {
	useInventedSpecs(t)
	t.Setenv("HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	instance := seedInstance(t, root, namedInstanceName, 1)
	chdir(t, root)
	writeMapping(t, root, namedMapping(resumeSessionID, instance, "01e00000", "review-4e33acfa", time.Time{}))

	assertConsecutiveLines(t, runListHuman(t),
		"test-ws+review-4e33acfa",
		"  session name: review-4e33acfa",
		"  resume: invented-plain-agent latch 01e00000",
	)
}

// TestList_SessionNameAfterKeepAliveMarker covers the name line under a
// kept-alive instance, and the JSON key order: session_name comes after
// keep_alive, so the existing keys keep their order.
func TestList_SessionNameAfterKeepAliveMarker(t *testing.T) {
	useInventedSpecs(t)
	t.Setenv("HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	instance := seedInstance(t, root, namedInstanceName, 1)
	chdir(t, root)
	m := namedMapping(resumeSessionID, instance, "01e00000", "review-4e33acfa", time.Time{})
	m.KeepAlive = true
	writeMapping(t, root, m)
	writeJobEntry(t, defaultJobsDir(), resumeSessionID)

	assertConsecutiveLines(t, runListHuman(t),
		"test-ws+review-4e33acfa (keep-alive)",
		"  session name: review-4e33acfa",
		"  resume: invented-plain-agent latch 01e00000",
	)

	raw, _ := runListJSON(t)
	ka := bytes.Index(raw, []byte(`"keep_alive":true`))
	sn := bytes.Index(raw, []byte(`"session_name":"review-4e33acfa"`))
	if ka < 0 || sn < 0 || ka > sn {
		t.Errorf("want keep_alive:true before session_name in:\n%s", raw)
	}
}

func TestList_JSONCarriesSessionName(t *testing.T) {
	useInventedSpecs(t)
	t.Setenv("HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	instance := seedInstance(t, root, namedInstanceName, 1)
	seedInstance(t, root, "test-ws-2", 2)
	chdir(t, root)
	writeMapping(t, root, namedMapping(resumeSessionID, instance, "01e00000", "review-4e33acfa", time.Time{}))

	_, recs := runListJSON(t)
	if got := jsonRecord(t, recs, namedInstanceName)["session_name"]; got != "review-4e33acfa" {
		t.Errorf("session_name = %#v, want %q", got, "review-4e33acfa")
	}
	if got, ok := jsonRecord(t, recs, "test-ws-2")["session_name"]; ok {
		t.Errorf("an unmapped instance carries session_name %#v", got)
	}
}

// TestList_ShowsNameForwardedByDispatch runs a named dispatch through the real
// runDispatch and the real launch declaration, then lists the instance: the
// name list shows is the one the agent was handed.
func TestList_ShowsNameForwardedByDispatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root, f := namedDispatchEnv(t, constByteReader(0xab))
	pass := recordPassthrough(f)
	dispatchDetach = true
	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	forwarded, found := forwardedDisplayName(*pass, claudeLaunchSpec().Flags.DisplayName)
	if !found || forwarded != "review-abababab" {
		t.Fatalf("forwarded %q (flag present: %v), want %q; passthrough %q", forwarded, found, "review-abababab", *pass)
	}
	// The fake provisioner makes the directory but no instance state, which
	// is what list enumerates by.
	name := filepath.Base(f.instancePath)
	seedInstance(t, root, name, 1)

	stdout := runListHuman(t)
	lines := strings.Split(stdout, "\n")
	found = false
	for i, line := range lines {
		if line != name {
			continue
		}
		found = true
		if i+2 >= len(lines) || lines[i+1] != "  session name: "+forwarded || !strings.HasPrefix(lines[i+2], "  resume: ") {
			t.Errorf("want %q then a resume line after %q:\n%s", "  session name: "+forwarded, name, stdout)
		}
	}
	if !found {
		t.Fatalf("no line %q in:\n%s", name, stdout)
	}

	_, recs := runListJSON(t)
	if got := jsonRecord(t, recs, name)["session_name"]; got != forwarded {
		t.Errorf("session_name = %#v, want %q", got, forwarded)
	}
}

// TestList_NoSessionNameWhenNoneRecorded covers mappings that recorded no name:
// an unnamed dispatch, an agent with no display-name flag, and a file written
// before the key existed. Every instance name is dispatch-shaped, so a name
// derived from the directory would show up here.
func TestList_NoSessionNameWhenNoneRecorded(t *testing.T) {
	useInventedSpecs(t)
	t.Setenv("HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	unnamed := seedInstance(t, root, namedInstanceName, 1)
	codex := seedInstance(t, root, "test-ws+codex_job-1a2b3c4d", 2)
	legacy := seedInstance(t, root, "test-ws+raw-5e6f7a8b", 3)
	chdir(t, root)

	writeMapping(t, root, namedMapping(resumeSessionID, unnamed, "01e00000", "", time.Time{}))
	cm := namedMapping(otherResumeSessionID, codex, otherResumeSessionID, "", time.Time{})
	cm.Agent = string(agent.AgentCodex)
	writeMapping(t, root, cm)

	legacyPath, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{
  "session_id": "` + thirdResumeSessionID + `",
  "instance_name": "test-ws+raw-5e6f7a8b",
  "instance_path": ` + string(legacyPath) + `,
  "created": "2026-01-02T03:04:05Z",
  "ephemeral": true,
  "handle": "01d00000",
  "origin": "dispatch"
}`
	if err := os.WriteFile(filepath.Join(root, ".niwa", "sessions", thirdResumeSessionID+".json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout := runListHuman(t)
	if strings.Contains(stdout, "session name:") {
		t.Errorf("an instance with no recorded name shows one:\n%s", stdout)
	}
	// The three mappings are all read: each instance lists its resume line.
	if n := strings.Count(stdout, "  resume: "); n != 3 {
		t.Errorf("%d resume lines, want 3 (one mapping was not read):\n%s", n, stdout)
	}

	rawJSON, recs := runListJSON(t)
	if bytes.Contains(rawJSON, []byte("session_name")) {
		t.Errorf("--json carries session_name:\n%s", rawJSON)
	}
	for _, r := range recs {
		if _, ok := r["session_name"]; ok {
			t.Errorf("record %v carries a session_name key", r)
		}
	}
}

// TestList_InvalidSessionNameIsAbsent: a recorded name that does not have the
// forwarded shape is not shown, because the mapping file is writable by any
// same-user process and the value reaches a terminal. The valid row is the
// control that proves the join fills the field at all.
func TestList_InvalidSessionNameIsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		shown       bool
	}{
		{"valid control", "review-4e33acfa", true},
		{"ansi escape", "review-4e33acfa\x1b[31m", false},
		{"no suffix", "review", false},
		{"seven hex", "review-4e33acf", false},
		{"nine hex", "review-4e33acfa0", false},
		{"uppercase hex", "review-4E33ACFA", false},
		{"non-hex", "review-4e33acfg", false},
		{"trailing newline", "review-4e33acfa\n", false},
		{"empty slug", "-4e33acfa", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			useInventedSpecs(t)
			t.Setenv("HOME", t.TempDir())
			root := setupDispatchWorkspace(t)
			instance := seedInstance(t, root, namedInstanceName, 1)
			chdir(t, root)
			writeMapping(t, root, namedMapping(resumeSessionID, instance, "01e00000", tc.value, time.Time{}))

			stdout := runListHuman(t)
			_, recs := runListJSON(t)
			got, hasKey := jsonRecord(t, recs, namedInstanceName)["session_name"]

			if tc.shown {
				if !strings.Contains(stdout, "  session name: "+tc.value+"\n") {
					t.Errorf("a valid recorded name is not shown:\n%s", stdout)
				}
				if got != tc.value {
					t.Errorf("session_name = %#v, want %q", got, tc.value)
				}
				return
			}
			if strings.Contains(stdout, "session name:") {
				t.Errorf("recorded %q is shown:\n%q", tc.value, stdout)
			}
			if strings.Contains(stdout, "\x1b") {
				t.Errorf("an escape byte reached the text output:\n%q", stdout)
			}
			if hasKey {
				t.Errorf("recorded %q reached --json as session_name %#v", tc.value, got)
			}
		})
	}
}

// TestList_JSONShapeCarriesNoSessionNameWhenNoneRecorded is the sibling of
// TestList_JSONShapeIsUnchanged: its fixture records no name, so the record
// lists without a session_name key.
func TestList_JSONShapeCarriesNoSessionNameWhenNoneRecorded(t *testing.T) {
	useInventedSpecs(t)
	t.Setenv("HOME", t.TempDir())
	root := setupDispatchWorkspace(t)
	instance := seedInstance(t, root, "test-ws+task-aaaa1111", 1)
	chdir(t, root)
	writeMapping(t, root, workspace.SessionMapping{
		SessionID:    resumeSessionID,
		InstanceName: "test-ws+task-aaaa1111",
		InstancePath: instance,
		Agent:        string(agent.AgentCodex),
		Handle:       resumeSessionID,
		Ephemeral:    true,
		Origin:       "dispatch",
	})

	_, recs := runListJSON(t)
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if got, ok := recs[0]["session_name"]; ok {
		t.Errorf("a mapping with no recorded name listed session_name %#v", got)
	}
}

func TestListCmd_HelpDescribesSessionName(t *testing.T) {
	for _, want := range []string{"session name:", "session_name"} {
		if !strings.Contains(listCmd.Long, want) {
			t.Errorf("list help does not mention %q", want)
		}
	}
	if usage := listCmd.Flags().Lookup("json").Usage; !strings.Contains(usage, "session_name") {
		t.Errorf("--json usage does not list session_name: %q", usage)
	}
}
