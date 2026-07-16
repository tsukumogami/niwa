package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// seedKeepAliveFixture builds a workspace with three ephemeral instances whose
// mappings cover the keep-alive report matrix:
//
//   - ka-live: keep-alive recorded, session live  -> reported
//   - ka-dead: keep-alive recorded, session dead  -> not reported
//   - plain:   no keep-alive, session live        -> not reported
//
// It returns the workspace root and the jobs dir carrying the live entries.
func seedKeepAliveFixture(t *testing.T) (root, jobsDir string) {
	t.Helper()
	return seedKeepAliveFixtureAt(t, t.TempDir(), t.TempDir())
}

// seedKeepAliveFixtureAt seeds the keep-alive matrix into the given workspace
// root and jobs dir (so a caller can place the jobs dir where defaultJobsDir
// resolves).
func seedKeepAliveFixtureAt(t *testing.T, root, jobsDir string) (string, string) {
	t.Helper()

	kaLive := seedInstance(t, root, "test-ws+ka_live-aaaa1111", 1)
	kaDead := seedInstance(t, root, "test-ws+ka_dead-bbbb2222", 2)
	plain := seedInstance(t, root, "test-ws+plain-cccc3333", 3)

	write := func(sessionID, path string, keepAlive bool) {
		t.Helper()
		m := workspace.SessionMapping{
			SessionID:    sessionID,
			InstanceName: strings.TrimPrefix(path, root+"/"),
			InstancePath: path,
			Ephemeral:    true,
			Origin:       "dispatch",
			KeepAlive:    keepAlive,
		}
		if err := workspace.WriteSessionMapping(root, m); err != nil {
			t.Fatal(err)
		}
	}
	write(reapLiveSessionID, kaLive, true)
	write(reapDeadSessionID, kaDead, true)
	write(reapNonEphSessionID, plain, false)

	// Live job entries for ka-live and plain; ka-dead's session has none.
	writeJobEntry(t, jobsDir, reapLiveSessionID)
	writeJobEntry(t, jobsDir, reapNonEphSessionID)
	return root, jobsDir
}

// TestAnnotateKeepAlive asserts the report is exactly the opted-in AND live
// set: a dead kept-alive session and a live non-opted one both stay false.
func TestAnnotateKeepAlive(t *testing.T) {
	root, jobsDir := seedKeepAliveFixture(t)

	records, err := workspace.EnumerateInstanceRecords(root)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	annotateKeepAlive(records, root, jobsDir, time.Now())

	got := map[string]bool{}
	for _, r := range records {
		got[r.Name] = r.KeepAlive
	}
	want := map[string]bool{
		"test-ws+ka_live-aaaa1111": true,
		"test-ws+ka_dead-bbbb2222": false,
		"test-ws+plain-cccc3333":   false,
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("record %s KeepAlive = %v, want %v", name, got[name], w)
		}
	}
}

// TestAnnotateKeepAlive_JSONShape pins the wire shape: keep_alive appears only
// on the kept-alive record (omitempty), so non-participating consumers see the
// exact pre-keep-alive JSON.
func TestAnnotateKeepAlive_JSONShape(t *testing.T) {
	root, jobsDir := seedKeepAliveFixture(t)

	records, err := workspace.EnumerateInstanceRecords(root)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	annotateKeepAlive(records, root, jobsDir, time.Now())

	data, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, rec := range raw {
		_, hasKA := rec["keep_alive"]
		if rec["name"] == "test-ws+ka_live-aaaa1111" {
			if !hasKA || rec["keep_alive"] != true {
				t.Errorf("kept-alive record must carry keep_alive:true, got %v", rec)
			}
			continue
		}
		if hasKA {
			t.Errorf("non-kept-alive record %v must omit the keep_alive key", rec["name"])
		}
	}
}

// TestAnnotateKeepAlive_NoStore leaves every record untouched when the
// workspace has no session mapping store (the common case).
func TestAnnotateKeepAlive_NoStore(t *testing.T) {
	root := t.TempDir()
	seedInstance(t, root, "test-ws", 1)
	records, err := workspace.EnumerateInstanceRecords(root)
	if err != nil {
		t.Fatalf("enumerate: %v", err)
	}
	annotateKeepAlive(records, root, t.TempDir(), time.Now())
	for _, r := range records {
		if r.KeepAlive {
			t.Errorf("record %s KeepAlive = true with no mapping store", r.Name)
		}
	}
}

// TestRunList_KeepAliveMarker drives the real list command over the fixture
// (HOME pointed at a sandbox so defaultJobsDir resolves to the fixture jobs
// dir) and asserts the human output marks exactly the kept-alive instance.
func TestRunList_KeepAliveMarker(t *testing.T) {
	// defaultJobsDir() is $HOME/.claude/jobs; point HOME at a sandbox and seed
	// the fixture's job entries there. setupDispatchWorkspace gives a root that
	// ClassifyCwd resolves (list requires being inside a workspace).
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := setupDispatchWorkspace(t)
	seedKeepAliveFixtureAt(t, root, filepath.Join(home, ".claude", "jobs"))
	chdir(t, root)

	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	prevJSON := listJSON
	listJSON = false
	t.Cleanup(func() { listJSON = prevJSON })
	if err := runList(cmd, nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	stdout := out.String()
	if !strings.Contains(stdout, "test-ws+ka_live-aaaa1111 (keep-alive)") {
		t.Errorf("expected the kept-alive marker on the live opted-in instance, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "test-ws+ka_dead-bbbb2222 (keep-alive)") ||
		strings.Contains(stdout, "test-ws+plain-cccc3333 (keep-alive)") {
		t.Errorf("only the live opted-in instance may carry the marker, got:\n%s", stdout)
	}
}
