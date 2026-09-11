package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// This file pins what a session mapping written before the session-name change
// looks like to every reader: niwa list (text and JSON), niwa status (summary
// and detail), niwa reap (live and dead), and the re-entry hints. The mapping is
// checked in as raw bytes under testdata/legacy_session/mapping.json and copied
// onto disk with one substitution, its single {{INSTANCE_PATH}} token, and is
// never built through workspace.WriteSessionMapping, so a field added to the
// struct later cannot change the fixture.
//
// Regenerate the goldens after a deliberate behavior change with:
//
//	NIWA_UPDATE_CHARACTERIZATION=1 go test ./internal/cli/ -run LegacySessionCharacterization
//
// and read the diff before committing it. The fixture itself is never
// rewritten.
const legacySessionGoldenEnv = "NIWA_UPDATE_CHARACTERIZATION"

const (
	legacySessionID    = "6a7b8c9d-0e1f-4a2b-8c3d-4e5f6a7b8c9d"
	legacyInstanceName = "test-ws+review-4e33acfa"
	legacyHandle       = "6a7b8c9d"

	// legacyInstancePathToken is the one placeholder in the fixture. The
	// mapping must carry the real absolute instance path, because list and
	// reap join mappings to instances by that path, so it is substituted at
	// load time.
	legacyInstancePathToken = "{{INSTANCE_PATH}}"
)

// legacySessionTestdata returns the absolute testdata directory. It is
// resolved before any subtest changes directory into its workspace.
func legacySessionTestdata(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "legacy_session"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// compareLegacyGolden checks got against testdata/legacy_session/<name>, or
// rewrites the golden when NIWA_UPDATE_CHARACTERIZATION is set.
func compareLegacyGolden(t *testing.T, dir, name, got string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if os.Getenv(legacySessionGoldenEnv) != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("rewrote %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v\n\nset %s=1 to create it", path, err, legacySessionGoldenEnv)
	}
	if string(want) != got {
		t.Errorf("%s changed\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
}

// legacyWorkspace is one workspace holding the legacy mapping and its instance.
type legacyWorkspace struct {
	root, inst, home, mappingPath string
}

// writeLegacyMappingFixture copies the raw fixture bytes into the workspace's
// session store, substituting the single instance-path token. No Go struct is
// involved.
func writeLegacyMappingFixture(t *testing.T, dir, root, inst string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("fixture is not JSON: %v", err)
	}
	if _, ok := raw["session_name"]; ok {
		t.Fatal("the legacy fixture must not carry a session_name key")
	}
	if n := bytes.Count(data, []byte(legacyInstancePathToken)); n != 1 {
		t.Fatalf("fixture carries %d %s tokens, want 1", n, legacyInstancePathToken)
	}
	quoted, err := json.Marshal(inst)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(legacyInstancePathToken), quoted[1:len(quoted)-1], 1)

	sessions := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessions, legacySessionID+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// buildLegacySessionWorkspace creates a workspace with the legacy instance and
// its mapping, points HOME at a temp dir, and changes into the workspace root.
func buildLegacySessionWorkspace(t *testing.T, dir string) legacyWorkspace {
	t.Helper()
	home := canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))

	root := setupDispatchWorkspace(t)
	inst := filepath.Join(root, legacyInstanceName)
	cfg := "test-ws"
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := workspace.SaveState(inst, &workspace.InstanceState{
		SchemaVersion: workspace.SchemaVersion,
		ConfigName:    &cfg,
		InstanceName:  legacyInstanceName,
		Root:          inst,
		Created:       ts,
		LastApplied:   ts,
		Repos:         map[string]workspace.RepoState{},
	}); err != nil {
		t.Fatal(err)
	}
	mp := writeLegacyMappingFixture(t, dir, root, inst)
	chdir(t, root)
	return legacyWorkspace{root: root, inst: inst, home: home, mappingPath: mp}
}

var legacyRelativeTime = regexp.MustCompile(`applied (just now|[0-9]+[mhd] ago)`)

// normalizeLegacyOutput replaces temp paths with fixed tokens, longest first,
// and the summary view's wall-clock relative time with <RELATIVE>.
func normalizeLegacyOutput(s string, ws legacyWorkspace) string {
	s = strings.ReplaceAll(s, ws.inst, "<INSTANCE>")
	s = strings.ReplaceAll(s, ws.root, "<ROOT>")
	s = strings.ReplaceAll(s, ws.home, "<HOME>")
	return legacyRelativeTime.ReplaceAllString(s, "applied <RELATIVE>")
}

func newLegacyCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	return cmd, &stdout, &stderr
}

// legacySection renders one titled section of a multi-part golden.
func legacySection(title, body string) string {
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return "== " + title + " ==\n" + body
}

func assertNoSessionNameText(t *testing.T, got string) {
	t.Helper()
	if strings.Contains(got, "session name:") {
		t.Errorf("legacy output carries a session name line:\n%s", got)
	}
}

func TestLegacySessionCharacterization(t *testing.T) {
	dir := legacySessionTestdata(t)

	prevListJSON, prevVerbose := listJSON, statusVerbose
	t.Cleanup(func() { listJSON, statusVerbose = prevListJSON, prevVerbose })
	statusVerbose = false

	t.Run("list_text", func(t *testing.T) {
		ws := buildLegacySessionWorkspace(t, dir)
		listJSON = false
		cmd, stdout, _ := newLegacyCmd()
		if err := runList(cmd, nil); err != nil {
			t.Fatalf("runList: %v", err)
		}
		got := normalizeLegacyOutput(stdout.String(), ws)
		if !strings.Contains(got, "\n  resume: ") {
			t.Errorf("list output has no resume line:\n%s", got)
		}
		assertNoSessionNameText(t, got)
		compareLegacyGolden(t, dir, "list.txt", got)
	})

	t.Run("list_json", func(t *testing.T) {
		ws := buildLegacySessionWorkspace(t, dir)
		listJSON = true
		t.Cleanup(func() { listJSON = false })
		cmd, stdout, _ := newLegacyCmd()
		if err := runList(cmd, nil); err != nil {
			t.Fatalf("runList: %v", err)
		}
		var decoded []map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
			t.Fatalf("list --json is not a JSON array: %v", err)
		}
		for _, rec := range decoded {
			if _, ok := rec["session_name"]; ok {
				t.Errorf("list --json record carries session_name: %v", rec)
			}
		}
		got := normalizeLegacyOutput(stdout.String(), ws)
		if strings.Contains(got, "session_name") {
			t.Errorf("list --json carries session_name:\n%s", got)
		}
		compareLegacyGolden(t, dir, "list.json", got)
	})

	t.Run("status", func(t *testing.T) {
		ws := buildLegacySessionWorkspace(t, dir)
		cmd, stdout, stderr := newLegacyCmd()
		if err := showSummaryView(cmd, ws.root); err != nil {
			t.Fatalf("showSummaryView: %v", err)
		}
		summary := stdout.String()
		stdout.Reset()
		if err := showDetailView(cmd, ws.inst); err != nil {
			t.Fatalf("showDetailView: %v", err)
		}
		detail := stdout.String()
		if stderr.Len() != 0 {
			t.Errorf("status wrote to stderr: %q", stderr.String())
		}
		got := normalizeLegacyOutput(legacySection("summary", summary)+legacySection("detail", detail), ws)
		assertNoSessionNameText(t, got)
		compareLegacyGolden(t, dir, "status.txt", got)
	})

	t.Run("reap", func(t *testing.T) {
		var b strings.Builder

		// Live: the session's job entry is present, so the instance is kept
		// and nothing is reported. The empty live sections in reap.txt are the
		// intended baseline, not a capture that failed: the not-destroyed and
		// mapping-kept assertions below carry this case.
		{
			ws := buildLegacySessionWorkspace(t, dir)
			writeJobEntry(t, filepath.Join(ws.home, ".claude", "jobs"), legacySessionID)
			destroyed := stubDestroyAll(t)
			stubRemoveTrust(t)
			cmd, stdout, _ := newLegacyCmd()
			var runErr error
			stderr := captureOSStderr(t, func() { runErr = runReap(cmd, nil) })
			if runErr != nil {
				t.Fatalf("runReap: %v", runErr)
			}
			if len(*destroyed) != 0 {
				t.Errorf("a live session's instance was destroyed: %q", *destroyed)
			}
			if _, err := os.Stat(ws.mappingPath); err != nil {
				t.Errorf("a live session's mapping is gone: %v", err)
			}
			b.WriteString(normalizeLegacyOutput(
				legacySection("live: runReap stdout", stdout.String())+
					legacySection("live: spared report (stderr)", stderr), ws))
		}

		// Dead: no job entry, so the instance is reclaimed, its trust entry
		// removed, and its mapping deleted.
		{
			ws := buildLegacySessionWorkspace(t, dir)
			destroyed := stubDestroyAll(t)
			untrusted := stubRemoveTrust(t)
			var n int
			var reapErr error
			stderr := captureOSStderr(t, func() {
				n, reapErr = reapWorkspace(ws.root, filepath.Join(ws.home, ".claude", "jobs"), time.Now())
			})
			if reapErr != nil {
				t.Fatalf("reapWorkspace: %v", reapErr)
			}
			if n != 1 {
				t.Errorf("reapWorkspace = %d, want 1", n)
			}
			if !reflect.DeepEqual(*destroyed, []string{ws.inst}) {
				t.Errorf("destroyed = %q, want [%q]", *destroyed, ws.inst)
			}
			if !reflect.DeepEqual(*untrusted, []string{ws.inst}) {
				t.Errorf("untrusted = %q, want [%q]", *untrusted, ws.inst)
			}
			if _, err := os.Stat(ws.mappingPath); !os.IsNotExist(err) {
				t.Errorf("a dead session's mapping survived the reap (stat err %v)", err)
			}
			b.WriteString(normalizeLegacyOutput(
				legacySection("dead: reapWorkspace count", "1")+
					legacySection("dead: destroyed", strings.Join(*destroyed, "\n"))+
					legacySection("dead: stderr", stderr), ws))
		}

		got := b.String()
		assertNoSessionNameText(t, got)
		compareLegacyGolden(t, dir, "reap.txt", got)
	})

	t.Run("hints", func(t *testing.T) {
		ws := buildLegacySessionWorkspace(t, dir)
		lines := reentryHints(claudeLaunchSpec(), legacyHandle, ws.inst)
		if len(lines) != 3 {
			t.Fatalf("reentryHints = %q, want 3 lines", lines)
		}
		got := normalizeLegacyOutput(strings.Join(lines, "\n")+"\n", ws)
		assertNoSessionNameText(t, got)
		compareLegacyGolden(t, dir, "hints.txt", got)
	})
}

// TestLegacySessionFixtureMatchesConstants cross-checks the constants this
// file's assertions use against the checked-in fixture, so the two cannot
// drift apart unnoticed.
func TestLegacySessionFixtureMatchesConstants(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(legacySessionTestdata(t), "mapping.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m workspace.SessionMapping
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	if m.SessionID != legacySessionID {
		t.Errorf("fixture session_id = %q, legacySessionID = %q", m.SessionID, legacySessionID)
	}
	if m.InstanceName != legacyInstanceName {
		t.Errorf("fixture instance_name = %q, legacyInstanceName = %q", m.InstanceName, legacyInstanceName)
	}
	if m.Handle != legacyHandle {
		t.Errorf("fixture handle = %q, legacyHandle = %q", m.Handle, legacyHandle)
	}
	if m.InstancePath != legacyInstancePathToken {
		t.Errorf("fixture instance_path = %q, want the %s token", m.InstancePath, legacyInstancePathToken)
	}
}
