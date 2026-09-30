package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/otelprecedence"
)

// The lineage fixtures record what a dispatch produces for the three facts
// downstream analysis reads off a session: that a dispatched worker's session
// record carries the dispatch's identity, that a dispatch without --skill
// names no requested skill, and that only a session niwa launched carries the
// workspace name. Each fixture is built from a real dispatch through the
// launch seam and compared with its file under testdata/lineage, with the
// dispatch id -- the one value that changes per run -- replaced by a
// placeholder. Run with -update-lineage-fixtures to rewrite the files.

var updateLineageFixtures = flag.Bool("update-lineage-fixtures", false, "rewrite testdata/lineage from the dispatches the fixture tests run")

const dispatchIDPlaceholder = "<dispatch-id>"

// packageDir is this package's directory, taken before any test changes the
// working directory: the dispatch tests chdir into a temporary workspace.
var packageDir = func() string {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return wd
}()

// lineageFixture is one fixture file.
type lineageFixture struct {
	Sessions []lineageFixtureSession `json:"sessions"`
}

type lineageFixtureSession struct {
	LaunchedByNiwa     bool              `json:"launched_by_niwa"`
	SessionRecord      map[string]string `json:"session_record,omitempty"`
	ResourceAttributes map[string]string `json:"resource_attributes"`
}

// dispatchedFixtureSession runs one dispatch with the given skill and brief
// and returns the worker's session as a fixture entry: the attributes its
// launch carries and the dispatch fields of its session record. It fails the
// test unless the record's dispatch id is the one in the attributes.
func dispatchedFixtureSession(t *testing.T, skill string, withBrief bool) lineageFixtureSession {
	t.Helper()
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	var got lineageLaunch
	captureLineageLaunch(f, &got)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "caller-session")
	dispatchName = "fixture"
	dispatchSkill = skill
	if withBrief {
		brief := filepath.Join(root, "brief.md")
		if err := os.WriteFile(brief, []byte("the brief\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		dispatchBrief = brief
	}
	dispatchDetach = true
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}

	attrs := otelprecedence.Entries(got.settings)
	id := attrs[attrDispatchID]
	if !dispatchIDPattern.MatchString(id) {
		t.Fatalf("%s = %q, want a dispatch id", attrDispatchID, id)
	}
	raw := readSingleMappingJSON(t, root)
	if raw["dispatch_id"] != id {
		t.Fatalf("session record dispatch_id = %v, want the worker's %q", raw["dispatch_id"], id)
	}
	record := map[string]string{"dispatch_id": dispatchIDPlaceholder}
	if p, ok := raw["parent_session_id"].(string); ok {
		record["parent_session_id"] = p
	}
	attrs[attrDispatchID] = dispatchIDPlaceholder
	return lineageFixtureSession{LaunchedByNiwa: true, SessionRecord: record, ResourceAttributes: attrs}
}

// checkLineageFixture compares got with testdata/lineage/<name>.json, or
// rewrites the file under -update-lineage-fixtures.
func checkLineageFixture(t *testing.T, name string, got lineageFixture) {
	t.Helper()
	path := filepath.Join(packageDir, "testdata", "lineage", name+".json")
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(got); err != nil {
		t.Fatal(err)
	}
	rendered := buf.Bytes()
	if *updateLineageFixtures {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, rendered, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v (run with -update-lineage-fixtures to create it)", path, err)
	}
	if !bytes.Equal(want, rendered) {
		t.Errorf("%s differs from what a dispatch produces now:\n--- file\n%s\n--- dispatch\n%s", path, want, rendered)
	}
}

func TestLineageFixture_DispatchedWorkerCarriesDispatchIdentity(t *testing.T) {
	s := dispatchedFixtureSession(t, "shirabe:work-on", true)
	if s.SessionRecord["parent_session_id"] != "caller-session" {
		t.Errorf("session record parent = %q, want the calling session", s.SessionRecord["parent_session_id"])
	}
	checkLineageFixture(t, "dispatched_worker_carries_dispatch_identity", lineageFixture{Sessions: []lineageFixtureSession{s}})
}

func TestLineageFixture_NoSkillMeansNoRequestedSkill(t *testing.T) {
	s := dispatchedFixtureSession(t, "", true)
	if v, ok := s.ResourceAttributes[attrRequestedSkill]; ok {
		t.Errorf("a dispatch without --skill carries %s=%q; it must carry none", attrRequestedSkill, v)
	}
	checkLineageFixture(t, "no_skill_means_no_requested_skill", lineageFixture{Sessions: []lineageFixtureSession{s}})
}

func TestLineageFixture_WorkspaceNameOnlyOnDispatchedSessions(t *testing.T) {
	dispatched := dispatchedFixtureSession(t, "", false)
	if dispatched.ResourceAttributes[attrWorkspace] != "test-ws" {
		t.Errorf("a dispatched session carries %s=%q, want the workspace name", attrWorkspace, dispatched.ResourceAttributes[attrWorkspace])
	}

	// A session niwa doesn't launch gets nothing from niwa: its value is
	// whatever the developer's own layers hold, here none.
	var notLaunched otelprecedence.Launch
	other := lineageFixtureSession{LaunchedByNiwa: false, ResourceAttributes: map[string]string{}}
	for _, mode := range otelprecedence.Modes {
		value, _ := notLaunched.Effective(mode)
		for k, v := range otelprecedence.Entries(value) {
			other.ResourceAttributes[k] = v
		}
	}
	if _, ok := other.ResourceAttributes[attrWorkspace]; ok {
		t.Errorf("a session niwa didn't launch carries %s", attrWorkspace)
	}
	checkLineageFixture(t, "workspace_name_only_on_dispatched_sessions", lineageFixture{Sessions: []lineageFixtureSession{dispatched, other}})
}

// TestLineage_OnlyDispatchNamesTheVariable backs the fixture's second
// session: niwa has no path that sets the variable outside a dispatch it
// launches. A new production file that names it has to be added here, which
// is the moment to decide whether it gives a session niwa doesn't launch any
// attributes.
func TestLineage_OnlyDispatchNamesTheVariable(t *testing.T) {
	allowed := map[string]bool{
		"internal/cli/dispatch.go":          true,
		"internal/cli/dispatch_launcher.go": true,
		"internal/cli/dispatch_lineage.go":  true,
		"internal/cli/dispatch_settings.go": true,
		// The test-only package that works out a worker's value.
		"internal/otelprecedence/otelprecedence.go": true,
	}
	repo := filepath.Join(packageDir, "..", "..")
	var found []string
	err := filepath.WalkDir(repo, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "testdata", "node_modules", "worktrees":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(otelprecedence.Variable)) {
			rel, _ := filepath.Rel(repo, path)
			rel = filepath.ToSlash(rel)
			if !allowed[rel] {
				found = append(found, rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Errorf("production files outside dispatch name %s: %v", otelprecedence.Variable, found)
	}
	// The allowlist names files that exist, so a rename can't leave it
	// vouching for nothing.
	for f := range allowed {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Errorf("allowlisted %s: %v", f, err)
		}
	}
}

// claudeNativeAttributes are the resource attribute names Claude Code sets
// itself, which a fixture may hold beside niwa's. host.name is left out on
// purpose: no fixture may carry a host name.
var claudeNativeAttributes = map[string]bool{
	"service.name":    true,
	"service.version": true,
	"os.type":         true,
	"os.version":      true,
	"host.arch":       true,
}

var sessionIDShape = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// TestLineageFixtures_HoldNothingPrivate checks every fixture file for what
// must never be in one: a cost field, an attribute outside niwa's namespace
// or Claude Code's own names, an absolute path, a host name, or a value
// shaped like a real session id.
func TestLineageFixtures_HoldNothingPrivate(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(packageDir, "testdata", "lineage", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("found %d lineage fixtures, want 3: %v", len(files), files)
	}
	host, _ := os.Hostname()
	sort.Strings(files)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var generic any
		if err := json.Unmarshal(data, &generic); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		walkFixture(generic, func(key, value string) {
			if strings.Contains(strings.ToLower(key), "cost") {
				t.Errorf("%s: cost field %q", file, key)
			}
			if key == "host.name" {
				t.Errorf("%s: host name attribute", file)
			}
			if value == "" {
				return
			}
			if filepath.IsAbs(value) || strings.HasPrefix(value, "~") || regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(value) {
				t.Errorf("%s: %s holds an absolute path", file, key)
			}
			if host != "" && strings.Contains(value, host) {
				t.Errorf("%s: %s holds this host's name", file, key)
			}
			if sessionIDShape.MatchString(value) {
				t.Errorf("%s: %s holds a value shaped like a session id", file, key)
			}
		})
		var fixture lineageFixture
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, s := range fixture.Sessions {
			for k := range s.ResourceAttributes {
				if !strings.HasPrefix(k, "niwa.") && !claudeNativeAttributes[k] {
					t.Errorf("%s: attribute %q is outside niwa's namespace and Claude Code's own names", file, k)
				}
			}
		}
	}
}

// walkFixture calls visit for every key and string value in v, with the key
// the value sits under.
func walkFixture(v any, visit func(key, value string)) {
	var walk func(key string, v any)
	walk = func(key string, v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, child := range x {
				visit(k, "")
				walk(k, child)
			}
		case []any:
			for _, child := range x {
				walk(key, child)
			}
		case string:
			visit(key, x)
		}
	}
	walk("", v)
}
