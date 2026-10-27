package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// recordFixtureDir holds the released-record fixtures and the pinned show and
// decide output. It is made absolute once, at load, because runAuthorityCmd
// changes the working directory.
var recordFixtureDir, _ = filepath.Abs(filepath.Join("testdata", "authority-records"))

// checkMapping writes the session mapping a dispatch (origin "dispatch") or
// a hook (origin "") would write for sessionID.
func checkMapping(t *testing.T, root, sessionID, instancePath, origin, dispatchID string) {
	t.Helper()
	m := workspace.SessionMapping{SessionID: sessionID, InstancePath: instancePath, Origin: origin, DispatchID: dispatchID, Ephemeral: true}
	if err := workspace.WriteSessionMapping(root, m); err != nil {
		t.Fatal(err)
	}
}

// checkPolicy is the authority config the check fixtures' workspaces
// declare: every authority in the ceiling, and the coordinator role the
// released records name. check resolves a bound session's grant against it.
const checkPolicy = "[authority]\nceiling = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n\n[authority.roles.coordinator]\nauthorities = [\"merge-pr\", \"close-issue\"]\n"

// boundWorkspace is a workspace with one instance whose record grants
// merge-pr at autonomous to showDispatchA, and showSessionA mapped to it by
// dispatch.
func boundWorkspace(t *testing.T) (*showWorkspace, string) {
	t.Helper()
	w := newShowWorkspace(t, checkPolicy)
	inst := w.instance(t, "ws-1")
	writeShowRecord(t, inst, showRecord(showDispatchA, authority.Autonomous, authority.LayerWorkspace, authority.MergePR))
	checkMapping(t, w.root, showSessionA, inst, "dispatch", showDispatchA)
	return w, inst
}

const goldenCheckBound = `{
  "schema": "niwa.authority.check/v1",
  "state": "bound",
  "binding": "mapping",
  "dispatch_id": "11111111-1111-4111-8111-111111111111",
  "grant": [
    {
      "authority": "merge-pr",
      "sources": [
        "dispatch"
      ]
    }
  ],
  "level": "autonomous",
  "level_source": "workspace",
  "grant_ceiling_source": "workspace",
  "points": [
    {
      "point": "merge-pr",
      "decider": "session",
      "author_exclusion": "off"
    }
  ]
}
`

func TestCheckBoundThroughTheDispatchMapping(t *testing.T) {
	_, inst := boundWorkspace(t)
	r := runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionA, "-C", inst, "--point", "merge-pr").mustSucceed(t)
	if r.stdout != goldenCheckBound {
		t.Errorf("stdout =\n%s\nwant\n%s", r.stdout, goldenCheckBound)
	}
}

func TestCheckBoundWithNoPointsKeepsAnEmptyPointsList(t *testing.T) {
	_, inst := boundWorkspace(t)
	r := runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionA, "-C", inst).mustSucceed(t)
	if !strings.Contains(r.stdout, `"points": []`) {
		t.Errorf("a bound answer with no --point has no empty points list:\n%s", r.stdout)
	}
}

func TestCheckPointsFollowTheLevel(t *testing.T) {
	for _, tc := range []struct {
		level              authority.Level
		decider, exclusion string
	}{
		{authority.Autonomous, "session", "off"},
		{authority.Supervised, "dispatcher", "on"},
		{authority.Strict, "person", "on"},
	} {
		t.Run(tc.level.String(), func(t *testing.T) {
			w := newShowWorkspace(t, "")
			inst := w.instance(t, "ws-1")
			writeShowRecord(t, inst, showRecord(showDispatchA, tc.level, authority.LayerWorkspace, authority.MergePR))
			checkMapping(t, w.root, showSessionA, inst, "dispatch", showDispatchA)
			r := runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionA, "-C", inst, "--point", "merge-pr", "--point", "close-issue").mustSucceed(t)
			for _, point := range []string{"merge-pr", "close-issue"} {
				want := `"point": "` + point + `",
      "decider": "` + tc.decider + `",
      "author_exclusion": "` + tc.exclusion + `"`
				if !strings.Contains(r.stdout, want) {
					t.Errorf("no %s answer %s/%s:\n%s", point, tc.decider, tc.exclusion, r.stdout)
				}
			}
		})
	}
}

func TestCheckNotDispatched(t *testing.T) {
	const notDispatched = `{
  "schema": "niwa.authority.check/v1",
  "state": "not-dispatched",
  "reason": "%s",
  "grant": []
}
`
	for _, tc := range []struct {
		name, reason string
		setup        func(t *testing.T, w *showWorkspace, inst string)
	}{
		{"another session", authority.ReasonNoMapping, func(t *testing.T, w *showWorkspace, inst string) {}},
		{"a hook-written mapping", authority.ReasonHookMapping, func(t *testing.T, w *showWorkspace, inst string) {
			checkMapping(t, w.root, showSessionB, inst, "", showDispatchA)
		}},
		{"a mapping with another origin", authority.ReasonHookMapping, func(t *testing.T, w *showWorkspace, inst string) {
			checkMapping(t, w.root, showSessionB, inst, "developer", showDispatchA)
		}},
		{"a dispatch mapping naming another instance", authority.ReasonOtherInstance, func(t *testing.T, w *showWorkspace, inst string) {
			checkMapping(t, w.root, showSessionB, w.instance(t, "ws-2"), "dispatch", showDispatchA)
		}},
		{"a dispatch mapping naming a relative path", authority.ReasonOtherInstance, func(t *testing.T, w *showWorkspace, inst string) {
			checkMapping(t, w.root, showSessionB, "ws-1", "dispatch", showDispatchA)
		}},
		{"a dispatch mapping naming a missing path", authority.ReasonOtherInstance, func(t *testing.T, w *showWorkspace, inst string) {
			checkMapping(t, w.root, showSessionB, filepath.Join(w.root, "gone"), "dispatch", showDispatchA)
		}},
		{"a dispatch mapping naming a repo in the instance", authority.ReasonOtherInstance, func(t *testing.T, w *showWorkspace, inst string) {
			checkMapping(t, w.root, showSessionB, filepath.Join(inst, "tools", "app"), "dispatch", showDispatchA)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, inst := boundWorkspace(t)
			tc.setup(t, w, inst)
			r := runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionB, "-C", inst, "--point", "merge-pr").mustSucceed(t)
			if want := strings.Replace(notDispatched, "%s", tc.reason, 1); r.stdout != want {
				t.Errorf("stdout =\n%s\nwant\n%s", r.stdout, want)
			}
		})
	}
}

func TestCheckRecordUnusable(t *testing.T) {
	const unusable = `{
  "schema": "niwa.authority.check/v1",
  "state": "record-unusable",
  "reason": "%s",
  "dispatch_id": "11111111-1111-4111-8111-111111111111",
  "grant": []
}
`
	for _, tc := range []struct {
		name, reason string
		setup        func(t *testing.T, inst string)
	}{
		{"record deleted", authority.ReasonRecordMissing, func(t *testing.T, inst string) {
			if err := os.Remove(workspace.AuthorityRecordPath(inst)); err != nil {
				t.Fatal(err)
			}
		}},
		{"record not JSON", authority.ReasonRecordMalformed, func(t *testing.T, inst string) {
			writeRawRecord(t, inst, "{not json")
		}},
		{"record a symlink", authority.ReasonRecordMalformed, func(t *testing.T, inst string) {
			path := workspace.AuthorityRecordPath(inst)
			target := filepath.Join(inst, "elsewhere.json")
			if err := os.Rename(path, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"record for another dispatch", authority.ReasonRecordOtherDispatch, func(t *testing.T, inst string) {
			writeShowRecord(t, inst, showRecord(showDispatchB, authority.Autonomous, authority.LayerWorkspace, authority.MergePR))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, inst := boundWorkspace(t)
			tc.setup(t, inst)
			r := runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionA, "-C", inst, "--point", "merge-pr").mustSucceed(t)
			if want := strings.Replace(unusable, "%s", tc.reason, 1); r.stdout != want {
				t.Errorf("stdout =\n%s\nwant\n%s", r.stdout, want)
			}
		})
	}
}

func TestCheckLaterRecordVersionBindsHoldingNothing(t *testing.T) {
	_, inst := boundWorkspace(t)
	writeRawRecord(t, inst, `{"schema": "niwa.authority.record/v2", "dispatch_id": "`+showDispatchA+`"}`)
	r := runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionA, "-C", inst, "--point", "merge-pr").mustSucceed(t)
	for _, want := range []string{`"state": "bound"`, `"grant": []`, `"level": "strict"`, `"decider": "person"`} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("no %s:\n%s", want, r.stdout)
		}
	}
}

func TestCheckFromARepoOrAWorktreeAnswersAsFromTheRoot(t *testing.T) {
	_, inst := boundWorkspace(t)
	worktree := filepath.Join(inst, ".niwa", "worktrees", "app-1234")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{showSessionA, showSessionB} {
		want := runAuthorityCmd(t, inst, "check", "--json", "--session", sid, "-C", inst, "--point", "merge-pr").mustSucceed(t).stdout
		for _, dir := range []string{filepath.Join(inst, "tools", "app", "pkg"), worktree} {
			got := runAuthorityCmd(t, dir, "check", "--json", "--session", sid, "-C", dir, "--point", "merge-pr").mustSucceed(t).stdout
			if got != want {
				t.Errorf("-C %s for %s printed\n%s\nfrom the root\n%s", dir, sid, got, want)
			}
		}
	}
}

func TestCheckRefusesABadAnchorOrSession(t *testing.T) {
	w, inst := boundWorkspace(t)
	outside := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"relative -C", []string{"--session", showSessionA, "-C", "ws-1"}, 1},
		{"empty -C", []string{"--session", showSessionA, "-C", ""}, 1},
		{"missing -C directory", []string{"--session", showSessionA, "-C", filepath.Join(w.root, "nope")}, 1},
		{"-C in no instance", []string{"--session", showSessionA, "-C", outside}, 1},
		{"-C the workspace root", []string{"--session", showSessionA, "-C", w.root}, 1},
		{"uppercase session id", []string{"--session", strings.ToUpper(showSessionA), "-C", inst}, 1},
		{"session id not a UUID", []string{"--session", "../../etc", "-C", inst}, 1},
		{"no --session", []string{"-C", inst}, 2},
		{"no -C", []string{"--session", showSessionA}, 2},
		{"bad point", []string{"--session", showSessionA, "-C", inst, "--point", "Merge PR"}, 2},
		{"positional argument", []string{"merge-pr", "--session", showSessionA, "-C", inst}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runAuthorityCmd(t, inst, append([]string{"check", "--json"}, tc.args...)...).mustFail(t, tc.code)
		})
	}
}

func TestCheckRefusesAnUnreadableMapping(t *testing.T) {
	w, inst := boundWorkspace(t)
	writeShowFile(t, filepath.Join(w.root, ".niwa", "sessions", showSessionB+".json"), "{broken", 0o600)
	runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionB, "-C", inst).mustFail(t, 1)
}

func TestCheckPlainText(t *testing.T) {
	_, inst := boundWorkspace(t)
	r := runAuthorityCmd(t, inst, "check", "--session", showSessionA, "-C", inst, "--point", "merge-pr").mustSucceed(t)
	want := "session: bound to dispatch " + showDispatchA + " (mapping)\n" +
		"grant: merge-pr (dispatch)\n" +
		"level: autonomous from workspace\n" +
		"point merge-pr: decider session, author exclusion off\n"
	if r.stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", r.stdout, want)
	}

	r = runAuthorityCmd(t, inst, "check", "--session", showSessionB, "-C", inst).mustSucceed(t)
	if want := "session: not dispatched (no-mapping)\ngrant: none\n"; r.stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", r.stdout, want)
	}

	writeShowRecord(t, inst, showRecord(showDispatchA, authority.Autonomous, authority.LayerWorkspace))
	r = runAuthorityCmd(t, inst, "check", "--session", showSessionA, "-C", inst).mustSucceed(t)
	if !hasLine(r.stdout, "grant: none") {
		t.Errorf("a bound session with no grant has no \"grant: none\" line:\n%s", r.stdout)
	}

	if err := os.Remove(workspace.AuthorityRecordPath(inst)); err != nil {
		t.Fatal(err)
	}
	r = runAuthorityCmd(t, inst, "check", "--session", showSessionA, "-C", inst).mustSucceed(t)
	if want := "session: dispatched, record unusable (record-missing)\ngrant: none\n"; r.stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", r.stdout, want)
	}
}

func TestCheckNamesTheParentDispatch(t *testing.T) {
	w := newShowWorkspace(t, "")
	inst := w.instance(t, "ws-1")
	rec := showRecord(showDispatchA, authority.Autonomous, authority.LayerWorkspace, authority.MergePR)
	rec.ParentDispatchID, rec.ParentDispatchSource = showDispatchB, authority.ParentSourceCwd
	writeShowRecord(t, inst, rec)
	checkMapping(t, w.root, showSessionA, inst, "dispatch", showDispatchA)

	r := runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionA, "-C", inst).mustSucceed(t)
	if want := "\"parent\": {\n    \"dispatch_id\": \"" + showDispatchB + "\"\n  },"; !strings.Contains(r.stdout, want) {
		t.Errorf("no parent without an instance, want %s:\n%s", want, r.stdout)
	}

	entry := lineage.Entry{
		DispatchID:     showDispatchB,
		Instance:       "ws-coordinator",
		Agent:          "claude",
		Origin:         lineage.OriginDispatch,
		WrittenAt:      "2026-10-23T10:00:00Z",
		ParentSession:  lineage.Link{Source: lineage.SourceNone},
		ParentDispatch: lineage.Link{Source: lineage.SourceNone},
		Principal:      lineage.Principal{Source: lineage.SourceNone, Trust: lineage.TrustObserved},
	}
	if err := workspace.CreateLineageEntry(w.root, entry); err != nil {
		t.Fatal(err)
	}
	r = runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionA, "-C", inst).mustSucceed(t)
	if want := "\"parent\": {\n    \"dispatch_id\": \"" + showDispatchB + "\",\n    \"instance\": \"ws-coordinator\"\n  },"; !strings.Contains(r.stdout, want) {
		t.Errorf("no parent with its instance, want %s:\n%s", want, r.stdout)
	}
}

// TestCheckReadsReleasedRecords reads the records niwa 0.36.0 and 0.37.0
// wrote (the same v1 layout, before the advisory fields) through check, with
// the grant each recorded.
func TestCheckReadsReleasedRecords(t *testing.T) {
	for _, tc := range []struct {
		version, dispatchID, grantText string
	}{
		{"v0.36.0", "36363636-0360-4036-8036-363636363636", "grant: merge-pr (dispatch)"},
		{"v0.37.0", "37373737-0370-4037-8037-373737373737", "grant: merge-pr (role coordinator), close-issue (dispatch, role coordinator)"},
		{"v0.38.0", "38383838-0380-4038-8038-383838383838", "grant: merge-pr (role coordinator), close-issue (dispatch, role coordinator)"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			w := newShowWorkspace(t, checkPolicy)
			inst := w.instance(t, "ws-1")
			body, err := os.ReadFile(filepath.Join(recordFixtureDir, tc.version+".json"))
			if err != nil {
				t.Fatal(err)
			}
			writeRawRecord(t, inst, string(body))
			checkMapping(t, w.root, showSessionA, inst, "dispatch", tc.dispatchID)
			r := runAuthorityCmd(t, inst, "check", "--session", showSessionA, "-C", inst).mustSucceed(t)
			if !hasLine(r.stdout, "session: bound to dispatch "+tc.dispatchID+" (mapping)") || !hasLine(r.stdout, tc.grantText) {
				t.Errorf("the %s record read through check as\n%s\nwant %q", tc.version, r.stdout, tc.grantText)
			}
		})
	}
}

// TestShowAndDecideOutputIsPinned holds show, show --json and decide to the
// bytes they printed before check existed, for the released records and a
// later-version one.
func TestShowAndDecideOutputIsPinned(t *testing.T) {
	update := os.Getenv("NIWA_UPDATE_AUTHORITY_GOLDEN") == "1"
	records := map[string]string{
		"v0.36.0": "",
		"v0.37.0": "",
		"v2":      `{"schema": "niwa.authority.record/v2", "dispatch_id": "` + showDispatchA + `"}`,
	}
	for name, raw := range records {
		t.Run(name, func(t *testing.T) {
			w := newShowWorkspace(t, "")
			inst := w.instance(t, "ws-1")
			if raw == "" {
				body, err := os.ReadFile(filepath.Join(recordFixtureDir, name+".json"))
				if err != nil {
					t.Fatal(err)
				}
				raw = string(body)
			}
			writeRawRecord(t, inst, raw)
			for _, run := range []struct {
				golden string
				args   []string
			}{
				{"show.txt", []string{"show"}},
				{"show.json", []string{"show", "--json"}},
				{"decide.txt", []string{"decide", "merge-pr"}},
			} {
				got := runAuthorityCmd(t, inst, run.args...).mustSucceed(t).stdout
				path := filepath.Join(recordFixtureDir, "golden", name+"."+run.golden)
				if update {
					writeShowFile(t, path, got, 0o644)
					continue
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("%v (set NIWA_UPDATE_AUTHORITY_GOLDEN=1 to write it)", err)
				}
				if got != string(want) {
					t.Errorf("%v printed\n%s\nwant\n%s", run.args, got, want)
				}
			}
		})
	}
}

func TestCheckWritesNothing(t *testing.T) {
	w, inst := boundWorkspace(t)
	before := treeState(t, w.root)
	runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionA, "-C", inst, "--point", "merge-pr").mustSucceed(t)
	runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionB, "-C", inst).mustSucceed(t)
	if after := treeState(t, w.root); !reflect.DeepEqual(after, before) {
		t.Errorf("check changed the workspace:\nbefore %v\nafter  %v", before, after)
	}
}

func TestCheckGivesUpUnderTheWatchdog(t *testing.T) {
	_, inst := boundWorkspace(t)
	// Ordered against the stalled goroutine as show's watchdog test is.
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	prev := authorityReadRecord
	authorityReadRecord = func(string) (authority.Record, bool, error) {
		entered <- struct{}{}
		<-release
		return authority.Record{}, false, errors.New("released")
	}
	t.Cleanup(func() {
		<-entered
		close(release)
		authorityReadRecord = prev
	})
	r := runAuthorityCmd(t, inst, "check", "--json", "--session", showSessionA, "-C", inst).mustFail(t, 1)
	if !strings.Contains(r.stderr, "gave up after") {
		t.Errorf("stderr = %q", r.stderr)
	}
}
