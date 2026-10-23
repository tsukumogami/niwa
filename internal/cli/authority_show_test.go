package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

const (
	showDispatchA = "11111111-1111-4111-8111-111111111111"
	showDispatchB = "22222222-2222-4222-8222-222222222222"
	showSessionA  = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	showSessionB  = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

// showWorkspace is a workspace root built from files: a workspace.toml, and
// instances with a .niwa/instance.json and a cloned-repo directory each.
type showWorkspace struct {
	root string
}

func newShowWorkspace(t *testing.T, authorityBody string) *showWorkspace {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeShowFile(t, filepath.Join(root, ".niwa", "workspace.toml"), "[workspace]\nname = \"ws\"\n"+authorityBody, 0o644)
	return &showWorkspace{root: root}
}

func writeShowFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func (w *showWorkspace) instance(t *testing.T, name string) string {
	t.Helper()
	inst := filepath.Join(w.root, name)
	writeShowFile(t, filepath.Join(inst, ".niwa", "instance.json"), "{}", 0o644)
	if err := os.MkdirAll(filepath.Join(inst, "tools", "app", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	return inst
}

func (w *showWorkspace) mapping(t *testing.T, sessionID, instancePath string) {
	t.Helper()
	if err := workspace.WriteSessionMapping(w.root, workspace.SessionMapping{SessionID: sessionID, InstancePath: instancePath}); err != nil {
		t.Fatal(err)
	}
}

func showRecord(dispatchID string, level authority.Level, source authority.Layer, grants ...authority.Authority) authority.Record {
	rec := authority.Record{
		Schema:               authority.SchemaV1,
		Present:              true,
		DispatchID:           dispatchID,
		ParentDispatchSource: authority.ParentSourceNone,
		Level:                level,
		LevelSource:          source,
		LevelCeiling:         level,
		LevelCeilingSource:   authority.LayerWorkspace,
		GrantCeiling:         []authority.Authority{authority.MergePR, authority.CloseIssue, authority.CutRelease},
		GrantCeilingSource:   authority.LayerWorkspace,
		Roles:                []string{},
		Grant:                []authority.GrantEntry{},
		UnknownGrant:         []authority.RawGrantEntry{},
	}
	for _, g := range grants {
		rec.Grant = append(rec.Grant, authority.GrantEntry{Authority: g, Sources: []authority.Source{authority.SourceDispatch}})
	}
	return rec
}

func writeShowRecord(t *testing.T, inst string, rec authority.Record) {
	t.Helper()
	if err := workspace.WriteAuthorityRecord(inst, rec); err != nil {
		t.Fatal(err)
	}
}

func writeRawRecord(t *testing.T, inst, body string) {
	t.Helper()
	writeShowFile(t, workspace.AuthorityRecordPath(inst), body, 0o600)
}

type authorityRun struct {
	stdout, stderr string
	code           int
}

// runAuthorityCmd runs `niwa authority <args>` from cwd on a freshly built
// command, so no flag state carries over between runs.
func runAuthorityCmd(t *testing.T, cwd string, args ...string) authorityRun {
	t.Helper()
	t.Chdir(cwd)
	var cmd *cobra.Command
	switch args[0] {
	case "show":
		cmd = newAuthorityShowCmd()
	case "decide":
		cmd = newAuthorityDecideCmd()
	default:
		t.Fatalf("no authority command %q", args[0])
	}
	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(args[1:])
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	r := authorityRun{stdout: out.String(), stderr: errb.String(), code: exitCode(t, err)}
	if err != nil {
		r.stderr += err.Error() + "\n"
	}
	return r
}

func (r authorityRun) mustSucceed(t *testing.T) authorityRun {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout:\n%s\nstderr:\n%s", r.code, r.stdout, r.stderr)
	}
	return r
}

func (r authorityRun) mustFail(t *testing.T, code int) authorityRun {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, code, r.stdout, r.stderr)
	}
	if r.stdout != "" {
		t.Fatalf("a failure printed to stdout:\n%s", r.stdout)
	}
	return r
}

func dispatchIDOf(t *testing.T, doc string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, doc)
	}
	id, _ := m["dispatch_id"].(string)
	return id
}

func hasLine(text, line string) bool {
	for _, l := range strings.Split(text, "\n") {
		if l == line {
			return true
		}
	}
	return false
}

// twoInstances builds instances a and b with a record each, and a mapping
// from session A to instance a.
func twoInstances(t *testing.T) (w *showWorkspace, a, b string) {
	t.Helper()
	w = newShowWorkspace(t, "")
	a, b = w.instance(t, "a"), w.instance(t, "b")
	writeShowRecord(t, a, showRecord(showDispatchA, authority.Supervised, authority.LayerFlag, authority.MergePR))
	writeShowRecord(t, b, showRecord(showDispatchB, authority.Strict, authority.LayerFlag))
	w.mapping(t, showSessionA, a)
	return w, a, b
}

func TestSelectDispatch_Table(t *testing.T) {
	w, a, b := twoInstances(t)
	outsideInst := filepath.Join(w.root, "stray", "deep")
	if err := os.MkdirAll(outsideInst, 0o755); err != nil {
		t.Fatal(err)
	}
	// A marker a cloned repository planted inside instance b.
	planted := filepath.Join(b, "tools", "app")
	writeShowFile(t, filepath.Join(planted, ".niwa", "instance.json"), "{}", 0o644)
	nested := filepath.Join(a, "tools", "nested")
	writeShowFile(t, filepath.Join(nested, ".niwa", "instance.json"), "{}", 0o644)
	w.mapping(t, "cccccccc-cccc-4ccc-8ccc-cccccccccccc", "/elsewhere/a")
	w.mapping(t, "dddddddd-dddd-4ddd-8ddd-dddddddddddd", nested)
	t.Setenv("CLAUDE_CODE_SESSION_ID", showSessionA)

	cases := []struct {
		name         string
		sel          dispatchSelectors
		cwd          string
		wantInstance string
		wantCode     int
	}{
		{"--session a from b", dispatchSelectors{Session: showSessionA, SessionSet: true}, b, a, 0},
		{"--instance a from b", dispatchSelectors{Instance: "a", InstanceSet: true}, b, a, 0},
		{"cwd b with the variable naming a", dispatchSelectors{}, b, b, 0},
		{"cwd a repo of b", dispatchSelectors{}, filepath.Join(b, "tools"), b, 0},
		{"cwd a planted marker in b", dispatchSelectors{}, filepath.Join(planted, "pkg"), b, 0},
		{"-C a from b", dispatchSelectors{Dir: a, DirSet: true}, b, a, 0},
		{"workspace root", dispatchSelectors{}, w.root, "", 0},
		{"a directory in no instance", dispatchSelectors{}, outsideInst, "", 0},
		{"-C a directory in no instance", dispatchSelectors{Dir: outsideInst, DirSet: true}, b, "", 0},
		{"--session with no mapping", dispatchSelectors{Session: showSessionB, SessionSet: true}, b, "", 0},
		{"--session and --instance", dispatchSelectors{Session: showSessionA, SessionSet: true, Instance: "a", InstanceSet: true}, b, "", 2},
		{"--session and -C", dispatchSelectors{Session: showSessionA, SessionSet: true, Dir: a, DirSet: true}, b, "", 2},
		{"--instance and -C", dispatchSelectors{Instance: "a", InstanceSet: true, Dir: a, DirSet: true}, b, "", 2},
		{"all three", dispatchSelectors{Session: showSessionA, SessionSet: true, Instance: "a", InstanceSet: true, Dir: a, DirSet: true}, b, "", 2},
		{"-C empty", dispatchSelectors{Dir: "", DirSet: true}, b, "", 1},
		{"-C relative", dispatchSelectors{Dir: "tools", DirSet: true}, b, "", 1},
		{"-C missing", dispatchSelectors{Dir: filepath.Join(w.root, "missing"), DirSet: true}, b, "", 1},
		{"--instance missing", dispatchSelectors{Instance: "zzz", InstanceSet: true}, b, "", 1},
		{"--instance escaping", dispatchSelectors{Instance: "../b", InstanceSet: true}, b, "", 1},
		{"--session mapping outside the workspace", dispatchSelectors{Session: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", SessionSet: true}, b, "", 1},
		{"--session mapping naming a nested instance", dispatchSelectors{Session: "dddddddd-dddd-4ddd-8ddd-dddddddddddd", SessionSet: true}, b, "", 1},
		{"--session not a session id", dispatchSelectors{Session: "../x", SessionSet: true}, b, "", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := selectDispatch(c.sel, c.cwd)
			if code := exitCode(t, err); code != c.wantCode {
				t.Fatalf("exit %d, want %d: %v", code, c.wantCode, err)
			}
			if got.Instance != c.wantInstance {
				t.Fatalf("instance %q, want %q", got.Instance, c.wantInstance)
			}
		})
	}
}

// The session id variable is never read: naming a session with a record, it
// changes nothing for a lookup that finds no instance.
func TestShow_SessionVariableIsNeverRead(t *testing.T) {
	w, a, _ := twoInstances(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", showSessionA)
	stray := filepath.Join(w.root, "stray")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, run := range map[string]func() authorityRun{
		"workspace root":           func() authorityRun { return runAuthorityCmd(t, w.root, "show") },
		"directory in no instance": func() authorityRun { return runAuthorityCmd(t, stray, "show") },
		"-C in no instance":        func() authorityRun { return runAuthorityCmd(t, a, "show", "-C", stray) },
	} {
		r := run().mustSucceed(t)
		if !hasLine(r.stdout, "dispatch record: none") {
			t.Errorf("%s: no 'dispatch record: none' line:\n%s", name, r.stdout)
		}
	}
}

func TestShowDecide_SelectorPairsAreUsageErrors(t *testing.T) {
	_, a, b := twoInstances(t)
	pairs := [][]string{
		{"--session", showSessionA, "--instance", "a"},
		{"--session", showSessionA, "-C", a},
		{"--instance", "a", "-C", a},
	}
	for _, pair := range pairs {
		runAuthorityCmd(t, b, append([]string{"show"}, pair...)...).mustFail(t, 2)
		runAuthorityCmd(t, b, append([]string{"show", "--json"}, pair...)...).mustFail(t, 2)
		runAuthorityCmd(t, b, append([]string{"decide", "merge-own-pr"}, pair...)...).mustFail(t, 2)
	}
}

// A bad -C never falls back to the working directory, which here is a
// dispatched instance with a record.
func TestShowDecide_BadAnchorNeverFallsBack(t *testing.T) {
	_, a, _ := twoInstances(t)
	for _, dir := range []string{"", "tools", filepath.Join(a, "missing")} {
		for _, args := range [][]string{{"show", "-C", dir}, {"show", "--json", "-C", dir}, {"decide", "merge-own-pr", "-C", dir}} {
			r := runAuthorityCmd(t, a, args...).mustFail(t, 1)
			if strings.Contains(r.stderr, showDispatchA) {
				t.Errorf("%v: stderr mentions the working directory's record:\n%s", args, r.stderr)
			}
		}
	}
}

func TestShow_AnchorMatchesRunningFromTheDirectory(t *testing.T) {
	_, a, b := twoInstances(t)
	repo := filepath.Join(a, "tools", "app", "pkg")
	for _, extra := range [][]string{nil, {"--json"}} {
		fromDir := runAuthorityCmd(t, repo, append([]string{"show"}, extra...)...).mustSucceed(t)
		anchored := runAuthorityCmd(t, b, append([]string{"show", "-C", repo}, extra...)...).mustSucceed(t)
		if fromDir.stdout != anchored.stdout {
			t.Errorf("show %v -C differs from running there:\n%s\n---\n%s", extra, anchored.stdout, fromDir.stdout)
		}
	}
}

func TestShow_InstanceFlag(t *testing.T) {
	w, _, b := twoInstances(t)
	r := runAuthorityCmd(t, w.root, "show", "--json", "--instance", "b").mustSucceed(t)
	if dispatchIDOf(t, r.stdout) != showDispatchB {
		t.Errorf("--instance b printed\n%s", r.stdout)
	}
	runAuthorityCmd(t, w.root, "show", "--instance", "nope").mustFail(t, 1)
	// A directory beside the workspace root that looks like an instance with
	// a record: an unchecked join of "../escape" would read it.
	escape := filepath.Join(filepath.Dir(w.root), "escape")
	writeShowFile(t, filepath.Join(escape, ".niwa", "instance.json"), "{}", 0o644)
	writeShowRecord(t, escape, showRecord(showDispatchA, authority.Autonomous, authority.LayerFlag))
	t.Cleanup(func() { os.RemoveAll(escape) })
	for _, name := range []string{"../escape", "b/../../escape", "", ".."} {
		r := runAuthorityCmd(t, b, "show", "--json", "--instance", name).mustFail(t, 1)
		if !strings.Contains(r.stderr, "invalid --instance") {
			t.Errorf("--instance %q: %s", name, r.stderr)
		}
	}
}

func TestShow_SessionFlag(t *testing.T) {
	w, a, b := twoInstances(t)
	r := runAuthorityCmd(t, b, "show", "--json", "--session", showSessionA).mustSucceed(t)
	if dispatchIDOf(t, r.stdout) != showDispatchA {
		t.Errorf("--session A printed\n%s", r.stdout)
	}
	r = runAuthorityCmd(t, b, "show", "--session", showSessionB).mustSucceed(t)
	if !hasLine(r.stdout, "dispatch record: none") {
		t.Errorf("--session with no mapping printed\n%s", r.stdout)
	}
	// A mapping naming a directory outside the workspace, and one naming an
	// instance nested inside another, are refused.
	outside := t.TempDir()
	writeShowFile(t, filepath.Join(outside, ".niwa", "instance.json"), "{}", 0o644)
	writeShowRecord(t, outside, showRecord(showDispatchB, authority.Autonomous, authority.LayerFlag))
	w.mapping(t, showSessionB, outside)
	r = runAuthorityCmd(t, a, "show", "--json", "--session", showSessionB).mustFail(t, 1)
	if strings.Contains(r.stderr, outside) {
		t.Errorf("the refusal echoes the mapping's path: %s", r.stderr)
	}
	nested := filepath.Join(a, "tools", "inner")
	writeShowFile(t, filepath.Join(nested, ".niwa", "instance.json"), "{}", 0o644)
	writeShowRecord(t, nested, showRecord(showDispatchB, authority.Autonomous, authority.LayerFlag))
	w.mapping(t, showSessionB, nested)
	runAuthorityCmd(t, a, "decide", "merge-own-pr", "--session", showSessionB).mustFail(t, 1)
}

func TestShow_PlantedMarkerResolvesTheRealInstance(t *testing.T) {
	_, _, b := twoInstances(t)
	planted := filepath.Join(b, "tools", "app")
	writeShowFile(t, filepath.Join(planted, ".niwa", "instance.json"), "{}", 0o644)
	writeShowRecord(t, planted, showRecord(showDispatchA, authority.Autonomous, authority.LayerFlag, authority.MergePR))
	r := runAuthorityCmd(t, filepath.Join(planted, "pkg"), "show", "--json").mustSucceed(t)
	if dispatchIDOf(t, r.stdout) != showDispatchB {
		t.Errorf("show from a planted marker printed\n%s", r.stdout)
	}
}

func TestShow_NoRecordDocument(t *testing.T) {
	for _, c := range []struct {
		body, level, source string
	}{
		{"", "autonomous", "built-in"},
		{"\n[authority]\nlevel = \"supervised\"\n", "supervised", "workspace"},
	} {
		w := newShowWorkspace(t, c.body)
		inst := w.instance(t, "plain")
		r := runAuthorityCmd(t, inst, "show", "--json").mustSucceed(t)
		var got map[string]any
		if err := json.Unmarshal([]byte(r.stdout), &got); err != nil {
			t.Fatalf("not JSON: %v\n%s", err, r.stdout)
		}
		want := map[string]any{
			"schema": "niwa.authority.record/v1", "record": false, "grant": []any{},
			"level": c.level, "level_source": c.source,
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("no-record document = %v, want %v", got, want)
		}
		// The no-record level is what a no-flag dispatch resolves to.
		p, _, err := loadAuthorityPolicy(w.root)
		if err != nil {
			t.Fatal(err)
		}
		report := authority.BuildPolicyReport(p, nil)
		if report.NoFlagLevel.String() != c.level || string(report.NoFlagLevelSource) != c.source {
			t.Errorf("no-flag dispatch resolves to %s from %s", report.NoFlagLevel, report.NoFlagLevelSource)
		}
		r = runAuthorityCmd(t, inst, "show").mustSucceed(t)
		if !hasLine(r.stdout, "dispatch record: none") || !hasLine(r.stdout, "level: "+c.level+" from "+c.source+" (no dispatch record)") {
			t.Errorf("plain no-record output:\n%s", r.stdout)
		}
	}
}

const unknownGrantRecord = `{
  "schema": "niwa.authority.record/v1",
  "record": true,
  "dispatch_id": "` + showDispatchA + `",
  "parent_session_id": "",
  "parent_dispatch_id": "",
  "parent_dispatch_source": "none",
  "level": "supervised",
  "level_source": "workspace",
  "level_ceiling": "supervised",
  "level_ceiling_source": "workspace",
  "grant_ceiling": ["merge-pr"],
  "grant_ceiling_source": "workspace",
  "roles": [],
  "grant": [
    {"authority": "merge-pr", "sources": ["dispatch"]},
    {"authority": "teardown", "sources": ["dispatch"]}
  ],
  "unknown_grant": [],
  "supervisor": "someone"
}`

func TestShow_UnknownGrant(t *testing.T) {
	w := newShowWorkspace(t, "")
	inst := w.instance(t, "a")
	writeRawRecord(t, inst, unknownGrantRecord)
	r := runAuthorityCmd(t, inst, "show", "--json").mustSucceed(t)
	var doc struct {
		Grant        []map[string]any `json:"grant"`
		UnknownGrant []map[string]any `json:"unknown_grant"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Grant) != 1 || doc.Grant[0]["authority"] != "merge-pr" {
		t.Errorf("grant = %v", doc.Grant)
	}
	wantUnknown := []map[string]any{{"authority": "teardown", "sources": []any{"dispatch"}}}
	if !reflect.DeepEqual(doc.UnknownGrant, wantUnknown) {
		t.Errorf("unknown_grant = %v", doc.UnknownGrant)
	}
	r = runAuthorityCmd(t, inst, "show").mustSucceed(t)
	if !hasLine(r.stdout, "grant: merge-pr (dispatch)") || !hasLine(r.stdout, "not recognised by this niwa, so not held: teardown (dispatch)") {
		t.Errorf("plain output:\n%s", r.stdout)
	}
}

const newerVersionRecord = `{"schema": "niwa.authority.record/v2", "dispatch_id": "` + showDispatchA + `", "grant": [{"authority": "merge-pr", "sources": ["dispatch"]}]}`

func TestShowDecideList_NewerVersionRecord(t *testing.T) {
	w := newShowWorkspace(t, "")
	inst := w.instance(t, "a")
	writeRawRecord(t, inst, newerVersionRecord)
	r := runAuthorityCmd(t, inst, "show").mustSucceed(t)
	if !hasLine(r.stdout, "dispatch record: newer version niwa.authority.record/v2; grant none") {
		t.Errorf("plain output:\n%s", r.stdout)
	}
	js := runAuthorityCmd(t, inst, "show", "--json").mustSucceed(t)
	var doc map[string]any
	if err := json.Unmarshal([]byte(js.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]any{
		"record": true, "record_version": "niwa.authority.record/v2", "dispatch_id": showDispatchA,
		"level": "strict", "level_source": "newer-version", "level_ceiling": "strict",
		"level_ceiling_source": "newer-version", "grant": []any{},
	} {
		if !reflect.DeepEqual(doc[k], v) {
			t.Errorf("%s = %v, want %v", k, doc[k], v)
		}
	}
	d := runAuthorityCmd(t, inst, "decide", "merge-own-pr").mustSucceed(t)
	for _, line := range []string{"level=strict", "decider=person", "source=newer-version", "record=present"} {
		if !hasLine(d.stdout, line) {
			t.Errorf("decide lacks %q:\n%s", line, d.stdout)
		}
	}
	listed := listJSONFor(t, w.root)
	if !jsonEqual(t, listed["a"], js.stdout) {
		t.Errorf("list authority = %s, want %s", listed["a"], js.stdout)
	}
}

// failureFixtures are records that exist and can't be used. Each must make
// show and decide exit 1 with nothing on stdout and stderr naming a failure
// class only.
func failureFixtures(t *testing.T) map[string]func(inst string) {
	return map[string]func(inst string){
		"malformed": func(inst string) { writeRawRecord(t, inst, `{"schema": "niwa.authority.record/v1", SECRET`) },
		"schema matching no version": func(inst string) {
			writeRawRecord(t, inst, strings.Replace(unknownGrantRecord, "record/v1", "record/latest", 1))
		},
		"v2 without a UUID": func(inst string) {
			writeRawRecord(t, inst, `{"schema": "niwa.authority.record/v2", "dispatch_id": "SECRET"}`)
		},
		"unknown level": func(inst string) {
			writeRawRecord(t, inst, strings.Replace(unknownGrantRecord, `"level": "supervised"`, `"level": "SECRET"`, 1))
		},
		"oversized": func(inst string) {
			writeRawRecord(t, inst, unknownGrantRecord+strings.Repeat(" ", authority.MaxRecordBytes)+"SECRET")
		},
		"fifo": func(inst string) {
			if err := syscall.Mkfifo(workspace.AuthorityRecordPath(inst), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(inst string) {
			target := filepath.Join(inst, "SECRET.json")
			writeShowFile(t, target, unknownGrantRecord, 0o600)
			if err := os.Symlink(target, workspace.AuthorityRecordPath(inst)); err != nil {
				t.Fatal(err)
			}
		},
		"unreadable": func(inst string) {
			if os.Geteuid() == 0 {
				t.Skip("root reads a mode-0 file")
			}
			writeShowFile(t, workspace.AuthorityRecordPath(inst), unknownGrantRecord, 0o000)
		},
	}
}

func TestShowDecide_UnusableRecordsFail(t *testing.T) {
	for name, plant := range failureFixtures(t) {
		t.Run(name, func(t *testing.T) {
			w := newShowWorkspace(t, "")
			inst := w.instance(t, "a")
			plant(inst)
			for _, args := range [][]string{{"show"}, {"show", "--json"}, {"decide", "merge-own-pr"}, {"show", "--json", "-C", inst}} {
				r := runAuthorityCmd(t, inst, args...).mustFail(t, 1)
				if strings.Contains(r.stderr, inst) || strings.Contains(r.stderr, "SECRET") || !strings.Contains(r.stderr, "authority record") {
					t.Errorf("%v stderr = %q", args, r.stderr)
				}
			}
		})
	}
}

func TestShow_WatchdogOnAStalledRead(t *testing.T) {
	w := newShowWorkspace(t, "")
	inst := w.instance(t, "a")
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	prev := authorityReadRecord
	authorityReadRecord = func(string) (authority.Record, bool, error) {
		<-release
		return authority.Record{}, false, nil
	}
	t.Cleanup(func() { authorityReadRecord = prev })
	start := time.Now()
	r := runAuthorityCmd(t, inst, "show", "--json").mustFail(t, 1)
	elapsed := time.Since(start)
	if elapsed < authorityWatchdog || elapsed > authorityWatchdog+time.Second {
		t.Errorf("gave up after %s, want about %s", elapsed, authorityWatchdog)
	}
	if !strings.Contains(r.stderr, "gave up after") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

// treeState lists every path under root with its mode, size and mtime.
func treeState(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		out[path] = fmt.Sprintf("%v %d %d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// Reading an existing record found from the working directory or -C loads no
// config and writes nothing: the workspace config and the machine config are
// both unreadable garbage here, and the tree is unchanged after.
func TestShow_ExistingRecordReadsNoConfigAndWritesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-0 file")
	}
	w, a, b := twoInstances(t)
	writeShowFile(t, filepath.Join(w.root, ".niwa", "workspace.toml"), "not toml [[[", 0o000)
	t.Cleanup(func() { os.Chmod(filepath.Join(w.root, ".niwa", "workspace.toml"), 0o644) })
	path, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	writeShowFile(t, path, "not toml [[[", 0o000)
	t.Cleanup(func() { os.Chmod(path, 0o644) })
	before := treeState(t, w.root)
	runAuthorityCmd(t, a, "show", "--json").mustSucceed(t)
	runAuthorityCmd(t, b, "show", "-C", filepath.Join(a, "tools")).mustSucceed(t)
	runAuthorityCmd(t, a, "decide", "merge-own-pr").mustSucceed(t)
	if after := treeState(t, w.root); !reflect.DeepEqual(before, after) {
		t.Errorf("the workspace changed:\nbefore %v\nafter  %v", before, after)
	}
}

// R11: a config typo added after a dispatch doesn't hide its record, and
// fails the no-record answer naming the bad key.
func TestShowDecide_BadConfigAfterTheDispatch(t *testing.T) {
	w, a, _ := twoInstances(t)
	writeShowFile(t, filepath.Join(w.root, ".niwa", "workspace.toml"), "[workspace]\nname = \"ws\"\n\n[authority]\nlevel = \"Strict\"\n", 0o644)
	path, err := config.GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	writeHostConfig(t, path, "[global]\nauthority_level = \"loose\"\n")
	r := runAuthorityCmd(t, a, "show", "--json").mustSucceed(t)
	if dispatchIDOf(t, r.stdout) != showDispatchA {
		t.Errorf("show printed\n%s", r.stdout)
	}
	runAuthorityCmd(t, a, "decide", "merge-own-pr").mustSucceed(t)
	for _, args := range [][]string{{"show", "--json"}, {"decide", "merge-own-pr"}} {
		r := runAuthorityCmd(t, w.root, args...).mustFail(t, 1)
		if !strings.Contains(r.stderr, "level") {
			t.Errorf("%v does not name the bad key: %s", args, r.stderr)
		}
	}
}

func decideLines(point, level, decider, exclusion, source, record string) string {
	return fmt.Sprintf("point=%s\nlevel=%s\ndecider=%s\nauthor_exclusion=%s\nsource=%s\nrecord=%s\n", point, level, decider, exclusion, source, record)
}

func TestDecide_ExactLines(t *testing.T) {
	w := newShowWorkspace(t, "")
	cases := []struct {
		level   authority.Level
		source  authority.Layer
		grant   []authority.Authority
		decider string
		excl    string
	}{
		{authority.Autonomous, authority.LayerFlag, nil, "session", "off"},
		{authority.Supervised, authority.LayerMachine, nil, "dispatcher", "on"},
		{authority.Strict, authority.LayerWorkspace, nil, "person", "on"},
		{authority.Autonomous, authority.LayerBuiltIn, nil, "session", "off"},
		// R4: holding merge-pr doesn't override the level.
		{authority.Strict, authority.LayerFlag, []authority.Authority{authority.MergePR}, "person", "on"},
	}
	for i, c := range cases {
		inst := w.instance(t, fmt.Sprintf("i%d", i))
		writeShowRecord(t, inst, showRecord(showDispatchA, c.level, c.source, c.grant...))
		for _, point := range []string{"merge-own-pr", "merge-pr"} {
			r := runAuthorityCmd(t, inst, "decide", point).mustSucceed(t)
			want := decideLines(point, c.level.String(), c.decider, c.excl, string(c.source), "present")
			if r.stdout != want {
				t.Errorf("decide %s at %s from %s =\n%s\nwant\n%s", point, c.level, c.source, r.stdout, want)
			}
		}
	}
	strictWS := newShowWorkspace(t, "\n[authority]\nlevel = \"strict\"\n")
	inst := strictWS.instance(t, "plain")
	r := runAuthorityCmd(t, inst, "decide", "merge-own-pr").mustSucceed(t)
	if want := decideLines("merge-own-pr", "strict", "person", "on", "workspace (no dispatch record)", "none"); r.stdout != want {
		t.Errorf("no-record decide =\n%s\nwant\n%s", r.stdout, want)
	}
	r = runAuthorityCmd(t, inst, "decide", "merge-own-pr", "--json").mustSucceed(t)
	var doc map[string]string
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["decider"] != "person" || doc["record"] != "none" || doc["source"] != "workspace (no dispatch record)" {
		t.Errorf("decide --json = %v", doc)
	}
}

func TestDecide_BadPointIsCheckedFirst(t *testing.T) {
	_, a, _ := twoInstances(t)
	for _, args := range [][]string{
		{"decide", "Merge_Now"},
		{"decide", "2merge", "-C", ""},
		{"decide", "-merge", "-C", "relative"},
		{"decide", strings.Repeat("a", 65), "--session", showSessionA, "--instance", "a"},
		{"decide"},
	} {
		runAuthorityCmd(t, a, args...).mustFail(t, 2)
	}
}

// listJSONFor runs `niwa list --json` from dir and returns each instance's
// authority object by name, absent for an instance without the key.
func listJSONFor(t *testing.T, dir string) map[string]json.RawMessage {
	t.Helper()
	t.Chdir(dir)
	saved := listJSON
	listJSON = true
	defer func() { listJSON = saved }()
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	if err := runList(cmd, nil); err != nil {
		t.Fatalf("niwa list --json: %v", err)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("list output: %v\n%s", err, out.String())
	}
	got := map[string]json.RawMessage{}
	for _, row := range rows {
		var name string
		json.Unmarshal(row["name"], &name)
		if a, ok := row["authority"]; ok {
			got[name] = a
		} else {
			got[name] = nil
		}
	}
	return got
}

func jsonEqual(t *testing.T, a json.RawMessage, b string) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

func TestList_AuthorityObject(t *testing.T) {
	w, a, b := twoInstances(t)
	plain := w.instance(t, "plain")
	bad := w.instance(t, "bad")
	writeRawRecord(t, bad, `{"schema": "niwa.authority.record/v1", "dispatch_id": "SECRET"}`)
	listed := listJSONFor(t, w.root)
	for name, inst := range map[string]string{"a": a, "b": b} {
		show := runAuthorityCmd(t, inst, "show", "--json").mustSucceed(t)
		if !jsonEqual(t, listed[name], show.stdout) {
			t.Errorf("list authority for %s = %s, want %s", name, listed[name], show.stdout)
		}
	}
	if v, ok := listed["plain"]; !ok || v != nil {
		t.Errorf("an instance with no record carries authority %s (present %v)", v, ok)
	}
	_ = plain
	var errObj map[string]string
	if err := json.Unmarshal(listed["bad"], &errObj); err != nil {
		t.Fatalf("bad record's authority: %v", err)
	}
	if len(errObj) != 1 || errObj["error"] != "authority record is malformed" {
		t.Errorf("bad record's authority = %v", errObj)
	}

	// Editing only the record changes what both commands report.
	data, err := os.ReadFile(workspace.AuthorityRecordPath(a))
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), `"level": "supervised"`, `"level": "strict"`, 1)
	writeRawRecord(t, a, edited)
	show := runAuthorityCmd(t, a, "show", "--json").mustSucceed(t)
	if !strings.Contains(show.stdout, `"level": "strict"`) {
		t.Errorf("show after the edit:\n%s", show.stdout)
	}
	if listed := listJSONFor(t, w.root); !jsonEqual(t, listed["a"], show.stdout) {
		t.Errorf("list after the edit = %s", listed["a"])
	}
}

func TestAuthorityHelpListsShowAndDecide(t *testing.T) {
	usage := authorityCmd.UsageString()
	for _, sub := range []string{"policy", "show", "decide"} {
		if !strings.Contains(usage, sub) {
			t.Errorf("niwa authority --help does not list %s:\n%s", sub, usage)
		}
	}
	for _, cmd := range []*cobra.Command{newAuthorityShowCmd(), newAuthorityDecideCmd()} {
		help := cmd.UsageString()
		for _, flag := range []string{"-C,", "--session", "--instance", "--json"} {
			if !strings.Contains(help, flag) {
				t.Errorf("%s --help does not list %s:\n%s", cmd.Name(), flag, help)
			}
		}
	}
}

// Exactly one session-mapping read for a --session lookup, and none for any
// other selection.
func TestShow_SessionMappingReads(t *testing.T) {
	w, a, b := twoInstances(t)
	reads := 0
	prev := authorityReadSessionMapping
	authorityReadSessionMapping = func(root, id string) (workspace.SessionMapping, error) {
		reads++
		return prev(root, id)
	}
	t.Cleanup(func() { authorityReadSessionMapping = prev })
	for _, c := range []struct {
		cwd  string
		args []string
		want int
	}{
		{b, []string{"show", "--session", showSessionA}, 1},
		{b, []string{"decide", "merge-own-pr", "--session", showSessionA}, 1},
		{b, []string{"show", "--session", showSessionB}, 1},
		{b, []string{"show"}, 0},
		{b, []string{"show", "-C", a}, 0},
		{b, []string{"show", "--instance", "a"}, 0},
		{w.root, []string{"show"}, 0},
		{b, []string{"decide", "merge-own-pr"}, 0},
	} {
		reads = 0
		runAuthorityCmd(t, c.cwd, c.args...).mustSucceed(t)
		if reads != c.want {
			t.Errorf("%v read %d session mappings, want %d", c.args, reads, c.want)
		}
	}
}

type failingTransport struct{ t *testing.T }

func (f failingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("a network call to %s", r.URL)
	return nil, errors.New("no network in this test")
}

// show, decide and list make no network call and start no process.
func TestShowDecideList_NoNetworkOrProcess(t *testing.T) {
	w, a, _ := twoInstances(t)
	w.instance(t, "plain")
	prevTransport := http.DefaultTransport
	http.DefaultTransport = failingTransport{t}
	t.Cleanup(func() { http.DefaultTransport = prevTransport })
	prevExec := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		t.Errorf("started a process: %s %v", name, args)
		return prevExec("false")
	}
	t.Cleanup(func() { execCommand = prevExec })
	t.Setenv("PATH", t.TempDir())

	runAuthorityCmd(t, a, "show", "--json").mustSucceed(t)
	runAuthorityCmd(t, a, "decide", "merge-own-pr").mustSucceed(t)
	runAuthorityCmd(t, filepath.Join(w.root, "plain"), "show").mustSucceed(t)
	runAuthorityCmd(t, w.root, "show", "--session", showSessionA).mustSucceed(t)
	listJSONFor(t, w.root)
}
