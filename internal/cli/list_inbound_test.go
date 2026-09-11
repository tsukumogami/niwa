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
	"github.com/tsukumogami/niwa/internal/workspace"
)

// Session ids and instance names for the accepts_session_messages list
// fixture. The ids are distinct from the reap and keep-alive fixtures' ids.
const (
	inboundListAcceptID = "44444444-4444-4444-4444-444444444444"
	inboundListBothID   = "55555555-5555-5555-5555-555555555555"
	inboundListPlainID  = "66666666-6666-6666-6666-666666666666"
	inboundListLegacyID = "77777777-7777-7777-7777-777777777777"

	inboundAcceptName = "test-ws+accept-aaaa1111"
	inboundBothName   = "test-ws+both-bbbb2222"
	inboundPlainName  = "test-ws+plain-cccc3333"
	inboundLegacyName = "test-ws+legacy-dddd4444"
	inboundBareName   = "test-ws+bare-eeee5555"
)

// inboundListWant is the accepts_session_messages value each fixture instance
// must report.
var inboundListWant = map[string]bool{
	inboundAcceptName: true,
	inboundBothName:   true,
	inboundPlainName:  false,
	inboundLegacyName: false,
	inboundBareName:   false,
}

// seedInboundListFixture builds five instances covering the report matrix:
//
//   - accept: recorded the behavior, session finished (no job entry) -> true
//   - both:   recorded the behavior and keep-alive, session live     -> true, keep-alive
//   - plain:  recorded neither, session live                         -> false
//   - legacy: a mapping written before the field existed             -> false
//   - bare:   no mapping at all                                      -> false
//
// accept has no live job entry on purpose: the grant is reported whether or
// not the session is still running, unlike keep-alive.
func seedInboundListFixture(t *testing.T, root, jobsDir string) {
	t.Helper()
	accept := seedInstance(t, root, inboundAcceptName, 1)
	both := seedInstance(t, root, inboundBothName, 2)
	plain := seedInstance(t, root, inboundPlainName, 3)
	legacy := seedInstance(t, root, inboundLegacyName, 4)
	seedInstance(t, root, inboundBareName, 5)

	write := func(sessionID, name, path string, keepAlive, accepts bool) {
		t.Helper()
		m := workspace.SessionMapping{
			SessionID:              sessionID,
			InstanceName:           name,
			InstancePath:           path,
			Agent:                  string(agent.AgentClaude),
			Handle:                 sessionID,
			Ephemeral:              true,
			Origin:                 "dispatch",
			KeepAlive:              keepAlive,
			AcceptsSessionMessages: accepts,
		}
		if err := workspace.WriteSessionMapping(root, m); err != nil {
			t.Fatal(err)
		}
	}
	write(inboundListAcceptID, inboundAcceptName, accept, false, true)
	write(inboundListBothID, inboundBothName, both, true, true)
	write(inboundListPlainID, inboundPlainName, plain, false, false)
	writeLegacySessionMapping(t, root, inboundListLegacyID, inboundLegacyName, legacy)

	writeJobEntry(t, jobsDir, inboundListBothID)
	writeJobEntry(t, jobsDir, inboundListPlainID)
}

// writeLegacySessionMapping hand-writes a mapping in the shape niwa wrote
// before accepts_session_messages existed.
func writeLegacySessionMapping(t *testing.T, root, sessionID, name, path string) {
	t.Helper()
	dir := filepath.Join(root, workspace.StateDir, "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"session_id":    sessionID,
		"instance_name": name,
		"instance_path": path,
		"ephemeral":     true,
		"origin":        "dispatch",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionID+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// setupInboundListWorkspace gives a workspace root that ClassifyCwd resolves,
// with HOME pointed at a sandbox so defaultJobsDir resolves inside it, and
// returns the root and that jobs dir. The caller chdirs.
func setupInboundListWorkspace(t *testing.T) (root, jobsDir string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return setupDispatchWorkspace(t), filepath.Join(home, ".claude", "jobs")
}

// runListForInbound runs the real list command in human or JSON mode and
// returns its stdout.
func runListForInbound(t *testing.T, asJSON bool) string {
	t.Helper()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	prev := listJSON
	listJSON = asJSON
	defer func() { listJSON = prev }()
	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList (json=%v): %v", asJSON, err)
	}
	return out.String()
}

// decodeListJSON decodes `niwa list --json` output into plain objects, so a
// test can tell an absent key from a false one.
func decodeListJSON(t *testing.T, out string) []map[string]any {
	t.Helper()
	var raw []map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		t.Fatalf("decoding list JSON: %v\n%s", err, out)
	}
	return raw
}

// listLines splits human list output into its instance name lines and its
// resume lines.
func listLines(out string) (names, resumes []string) {
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "":
		case strings.HasPrefix(line, "  resume: "):
			resumes = append(resumes, line)
		default:
			names = append(names, line)
		}
	}
	return names, resumes
}

// TestAnnotateAcceptsSessionMessages asserts the report comes from any mapping
// that recorded the behavior, with no liveness gate: the finished session
// still reports true, and older mappings and unmapped instances report false.
func TestAnnotateAcceptsSessionMessages(t *testing.T) {
	root, jobsDir := t.TempDir(), t.TempDir()
	seedInboundListFixture(t, root, jobsDir)

	records, err := workspace.EnumerateInstanceRecords(root)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	for _, r := range records {
		if r.AcceptsSessionMessages {
			t.Errorf("EnumerateInstanceRecords set AcceptsSessionMessages on %s; the list command fills it in", r.Name)
		}
	}
	annotateFromSessionMappings(records, root, jobsDir, time.Now())

	got := map[string]workspace.InstanceRecord{}
	for _, r := range records {
		got[r.Name] = r
	}
	for name, want := range inboundListWant {
		if got[name].AcceptsSessionMessages != want {
			t.Errorf("record %s AcceptsSessionMessages = %v, want %v", name, got[name].AcceptsSessionMessages, want)
		}
	}
	if got[inboundAcceptName].KeepAlive {
		t.Errorf("record %s reports keep-alive; the behavior must not imply it", inboundAcceptName)
	}
	if !got[inboundBothName].KeepAlive {
		t.Errorf("record %s lost its keep-alive report", inboundBothName)
	}
}

// TestRunList_AcceptsSessionMessages_JSONShape pins the wire shape of the real
// command: accepts_session_messages is on every record, true or false, while
// keep_alive stays omitted when false.
func TestRunList_AcceptsSessionMessages_JSONShape(t *testing.T) {
	root, jobsDir := setupInboundListWorkspace(t)
	seedInboundListFixture(t, root, jobsDir)
	chdir(t, root)

	raw := decodeListJSON(t, runListForInbound(t, true))
	seen := 0
	for _, rec := range raw {
		v, ok := rec["accepts_session_messages"]
		if !ok {
			t.Errorf("record %v lacks the accepts_session_messages key", rec["name"])
			continue
		}
		name, _ := rec["name"].(string)
		want, known := inboundListWant[name]
		if !known {
			if v != false {
				t.Errorf("record %s reports accepts_session_messages %v with no mapping, want false", name, v)
			}
			continue
		}
		seen++
		if v != want {
			t.Errorf("record %s accepts_session_messages = %v, want %v", name, v, want)
		}
		_, hasKA := rec["keep_alive"]
		if hasKA != (name == inboundBothName) {
			t.Errorf("record %s keep_alive present = %v; only the kept-alive record carries it", name, hasKA)
		}
	}
	if seen != len(inboundListWant) {
		t.Fatalf("saw %d fixture records, want %d: %v", seen, len(inboundListWant), raw)
	}
}

// TestRunList_AcceptsSessionMessagesMarker asserts the three forms of the
// human name line: the marker alone, the marker after (keep-alive), and no
// marker for a false record.
func TestRunList_AcceptsSessionMessagesMarker(t *testing.T) {
	root, jobsDir := setupInboundListWorkspace(t)
	seedInboundListFixture(t, root, jobsDir)
	chdir(t, root)

	out := runListForInbound(t, false)
	names, _ := listLines(out)
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, want := range []string{
		"test-ws+accept-aaaa1111 (accepts session messages)",
		"test-ws+both-bbbb2222 (keep-alive) (accepts session messages)",
		"test-ws+plain-cccc3333",
		"test-ws+legacy-dddd4444",
		"test-ws+bare-eeee5555",
	} {
		if !have[want] {
			t.Errorf("missing name line %q in:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "(accepts session messages)"); got != 2 {
		t.Errorf("marker appears %d times, want 2 (only the true records):\n%s", got, out)
	}
}

// TestRunList_AcceptsSessionMessages_ResumeLineUnchanged: for one session, the
// resume line is the same whether or not the behavior took effect; only the
// name line changes.
func TestRunList_AcceptsSessionMessages_ResumeLineUnchanged(t *testing.T) {
	root, _ := setupInboundListWorkspace(t)
	path := seedInstance(t, root, inboundAcceptName, 1)
	chdir(t, root)

	run := func(accepts bool) (names, resumes []string) {
		t.Helper()
		m := workspace.SessionMapping{
			SessionID:              inboundListAcceptID,
			InstanceName:           inboundAcceptName,
			InstancePath:           path,
			Agent:                  string(agent.AgentClaude),
			Handle:                 inboundListAcceptID,
			Ephemeral:              true,
			Origin:                 "dispatch",
			AcceptsSessionMessages: accepts,
		}
		if err := workspace.WriteSessionMapping(root, m); err != nil {
			t.Fatal(err)
		}
		return listLines(runListForInbound(t, false))
	}

	offNames, offResumes := run(false)
	onNames, onResumes := run(true)
	if len(offResumes) != 1 || len(onResumes) != 1 {
		t.Fatalf("want one resume line each way, got off %q and on %q", offResumes, onResumes)
	}
	if offResumes[0] != onResumes[0] {
		t.Errorf("resume line changed with the behavior:\n off: %s\n on:  %s", offResumes[0], onResumes[0])
	}
	if want := []string{inboundAcceptName}; strings.Join(offNames, "\n") != strings.Join(want, "\n") {
		t.Errorf("name lines without the behavior = %q, want %q", offNames, want)
	}
	if want := []string{inboundAcceptName + " (accepts session messages)"}; strings.Join(onNames, "\n") != strings.Join(want, "\n") {
		t.Errorf("name lines with the behavior = %q, want %q", onNames, want)
	}
}

// TestRunList_AcceptsSessionMessages_FalseWithoutARecordingStore covers the
// early return in annotateFromSessionMappings: with no session store, an
// empty one, or one that cannot be read, list still succeeds in both modes
// and every record reports false.
func TestRunList_AcceptsSessionMessages_FalseWithoutARecordingStore(t *testing.T) {
	cases := []struct {
		name  string
		store func(t *testing.T, sessions string)
	}{
		{"no session store", func(t *testing.T, sessions string) {
			if err := os.RemoveAll(sessions); err != nil {
				t.Fatal(err)
			}
		}},
		{"empty session store", func(t *testing.T, sessions string) {
			if err := os.RemoveAll(sessions); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(sessions, 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{"unreadable session store", func(t *testing.T, sessions string) {
			// A regular file where the directory should be fails the read for
			// every user, root included, on Linux and macOS alike.
			if err := os.RemoveAll(sessions); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(sessions, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := setupInboundListWorkspace(t)
			seedInstance(t, root, inboundBareName, 1)
			tc.store(t, filepath.Join(root, workspace.StateDir, "sessions"))
			chdir(t, root)

			if out := runListForInbound(t, false); strings.Contains(out, "(accepts session messages)") {
				t.Errorf("no record may carry the marker:\n%s", out)
			}
			raw := decodeListJSON(t, runListForInbound(t, true))
			if len(raw) == 0 {
				t.Fatal("expected at least the seeded instance's record")
			}
			for _, rec := range raw {
				if v, ok := rec["accepts_session_messages"]; !ok || v != false {
					t.Errorf("record %v accepts_session_messages = %v (present %v), want false", rec["name"], v, ok)
				}
			}
		})
	}
}
