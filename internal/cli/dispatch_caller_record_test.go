package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// callerCapFor runs callerCap the way resolveDispatchAuthority does, from dir
// for caller, with a ceiling holding every authority, as capFor does for
// dispatcherCap.
func callerCapFor(t *testing.T, root, dir string, caller callerSession) *authority.DispatcherCap {
	t.Helper()
	parent, _, cwdInstance, cwdInInstance, err := dispatchParent(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	all := authority.Authorities()
	grants := grantResolver{root: root, policy: authority.Policy{BaseCeiling: &all}}
	return callerCap(grants, caller, parent, cwdInstance, cwdInInstance)
}

// TestCallerCap: a stripped caller holds none wherever it dispatches from,
// and outside every instance the record's session and the variable's each
// cap the child, so the child gets only what both dispatchers hold.
func TestCallerCap(t *testing.T) {
	resetParentFrom(t)
	w := newShowWorkspace(t, "")
	narrow := w.instance(t, "ws-narrow")
	writeShowRecord(t, narrow, showRecord(showDispatchA, authority.Autonomous, authority.LayerWorkspace, authority.MergePR))
	checkMapping(t, w.root, showSessionA, narrow, workspace.MappingOriginDispatch, showDispatchA)
	wide := w.instance(t, "ws-wide")
	writeShowRecord(t, wide, showRecord(showDispatchB, authority.Autonomous, authority.LayerWorkspace, authority.MergePR, authority.CloseIssue))
	checkMapping(t, w.root, showSessionB, wide, workspace.MappingOriginDispatch, showDispatchB)
	const unmapped = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"

	stripped := callerSession{Marker: "CLAUDECODE", varName: "CLAUDE_CODE_SESSION_ID"}
	mergeOnly := []authority.Authority{authority.MergePR}
	for _, tc := range []struct {
		name, dir, anchor string
		caller            callerSession
		wantStripped      bool
		wantHeld          []authority.Authority // nil with wantNil for no cap
		wantNil           bool
		wantID            string // the dispatch the refusal names: the record's session's, when it narrows
	}{
		{"stripped, inside a granted instance", wide, "", stripped, true, nil, false, ""},
		{"stripped, at the root with --parent-from a granted instance", w.root, wide, stripped, true, nil, false, ""},
		{"stripped, at the root", w.root, "", stripped, true, nil, false, ""},
		{"variable narrow, record wide", w.root, "", callerSession{Var: showSessionA, RecordID: showSessionB}, false, mergeOnly, false, showDispatchB},
		{"variable wide, record narrow", w.root, "", callerSession{Var: showSessionB, RecordID: showSessionA}, false, mergeOnly, false, showDispatchA},
		{"variable unmapped, record narrow", w.root, "", callerSession{Var: unmapped, RecordID: showSessionA}, false, mergeOnly, false, showDispatchA},
		// A record naming a session no mapping knows is a session niwa
		// didn't dispatch: no dispatcher to cap by, and not stripped.
		{"record valid but unmapped", w.root, "", callerSession{RecordID: unmapped, Marker: "CLAUDE_JOB_DIR"}, false, nil, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.anchor != "" {
				dispatchParentFrom, dispatchParentFromSet = tc.anchor, true
				t.Cleanup(func() { dispatchParentFrom, dispatchParentFromSet = "", false })
			}
			got := callerCapFor(t, w.root, tc.dir, tc.caller)
			if tc.wantNil {
				if got != nil {
					t.Errorf("cap = %+v, want none", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("cap = nil, want one holding %v", tc.wantHeld)
			}
			if got.DispatchID != tc.wantID {
				t.Errorf("cap names dispatch %q, want %q", got.DispatchID, tc.wantID)
			}
			if (got.Stripped != "") != tc.wantStripped || len(got.Held) != len(tc.wantHeld) ||
				(len(tc.wantHeld) > 0 && !reflect.DeepEqual(got.Held, tc.wantHeld)) {
				t.Errorf("cap = %+v, want held %v stripped %v", got, tc.wantHeld, tc.wantStripped)
			}
		})
	}
}

// TestDispatchCallerSession_StrippedRefusalSaysWhy: the refusal for a
// stripped caller names the variable that marked the agent session, not
// only the never-wider rule.
func TestDispatchCallerSession_StrippedRefusalSaysWhy(t *testing.T) {
	resetParentFrom(t)
	authorityWorkspace(t, r8aExample, "")
	agentSessionEnv(t, "")
	dispatchRoles = []string{"coordinator"}
	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatalf("a stripped caller dispatched --role coordinator\n%s", stderr)
	}
	if !strings.Contains(err.Error(), "CLAUDECODE is set") || !strings.Contains(err.Error(), "no calling session resolves") {
		t.Errorf("refusal = %v, want it to say the agent session marker is set and no session resolves", err)
	}
}

// TestDispatchCallerSession_StrippedRecordNamesNoSession: a stripped
// caller's record names no parent session.
func TestDispatchCallerSession_StrippedRecordNamesNoSession(t *testing.T) {
	resetParentFrom(t)
	_, f := authorityWorkspace(t, r8aExample, "")
	agentSessionEnv(t, "")
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	if rec := readRecord(t, f); rec.ParentSessionID != "" || rec.ParentSessionSource != "stripped" {
		t.Errorf("parent session = %q (%s), want none recorded as stripped", rec.ParentSessionID, rec.ParentSessionSource)
	}
}

// envOf is a getenv reading only the given values.
func envOf(vals map[string]string) func(string) string {
	return func(name string) string { return vals[name] }
}

func TestReadCallerSession(t *testing.T) {
	spec := claudeLaunchSpec()
	l := spec.Lineage
	recordDir := callerRecordDir(t, callerSessionID, "/somewhere")
	const varID = "a2a2a2a2-2222-4222-8222-a2a2a2a2a2a2"
	cases := []struct {
		name         string
		env          map[string]string
		wantID       string
		wantStripped bool
	}{
		{"plain terminal", map[string]string{}, "", false},
		{"variable set, no markers", map[string]string{l.ParentSessionEnv: varID}, varID, false},
		{"variable set with markers", map[string]string{l.ParentSessionEnv: varID, l.SessionMarkerEnv[0]: "1"}, varID, false},
		{"variable unset, record names the session", map[string]string{l.SessionRecordEnv: recordDir, l.SessionMarkerEnv[0]: "1"}, callerSessionID, false},
		{"variable wins over the record", map[string]string{l.ParentSessionEnv: varID, l.SessionRecordEnv: recordDir}, varID, false},
		{"marker only", map[string]string{l.SessionMarkerEnv[1]: "cli"}, "", true},
		{"record directory with no record", map[string]string{l.SessionRecordEnv: t.TempDir()}, "", true},
		{"malformed variable, marker set", map[string]string{l.ParentSessionEnv: "not a session", l.SessionMarkerEnv[0]: "1"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := readCallerSession(spec, envOf(tc.env))
			if c.ID() != tc.wantID || c.Stripped() != tc.wantStripped {
				t.Errorf("caller = %+v: ID %q stripped %v, want ID %q stripped %v", c, c.ID(), c.Stripped(), tc.wantID, tc.wantStripped)
			}
		})
	}
}

func TestReadCallerSession_StrippedReasonNamesTheVariables(t *testing.T) {
	spec := claudeLaunchSpec()
	c := readCallerSession(spec, envOf(map[string]string{spec.Lineage.SessionMarkerEnv[0]: "1"}))
	reason := c.strippedReason()
	if !strings.Contains(reason, spec.Lineage.SessionMarkerEnv[0]) || !strings.Contains(reason, spec.Lineage.ParentSessionEnv) {
		t.Errorf("reason = %q, want it to name the marker and the session variable", reason)
	}
}

// TestDispatchCallerSession_UnmappedVariableStillCappedByRecord: replacing
// the session variable with an id no mapping names doesn't escape the
// dispatcher the session's own record leads to.
func TestDispatchCallerSession_UnmappedVariableStillCappedByRecord(t *testing.T) {
	resetParentFrom(t)
	root, _ := authorityWorkspace(t, r8aExample, "")
	inst := grantlessDispatchedInstance(t, root)
	agentSessionEnv(t, callerRecordDir(t, callerSessionID, inst))
	t.Setenv("CLAUDE_CODE_SESSION_ID", "b3b3b3b3-3333-4333-8333-b3b3b3b3b3b3")
	dispatchRoles = []string{"coordinator"}
	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatalf("a grantless session with an unmapped variable dispatched --role coordinator; want the never-wider refusal\n%s", stderr)
	}
	if !strings.Contains(err.Error(), "wider than the dispatcher's grant") || !strings.Contains(err.Error(), parentDispatchID) {
		t.Errorf("refusal = %v, want the never-wider refusal naming dispatch %s", err, parentDispatchID)
	}
}

// TestReadCallerRecordID_OnlyASafeRecordIsRead: the record directory comes
// from the environment, so a record reached any unsafe way names no session.
func TestReadCallerRecordID_OnlyASafeRecordIsRead(t *testing.T) {
	r := claudeLaunchSpec().Records
	good := callerRecordDir(t, callerSessionID, "/somewhere")
	if got := readCallerRecordID(good, r); got != callerSessionID {
		t.Fatalf("a plain record read %q, want %q", got, callerSessionID)
	}

	t.Run("relative directory", func(t *testing.T) {
		t.Chdir(filepath.Dir(good))
		if got := readCallerRecordID(filepath.Base(good), r); got != "" {
			t.Errorf("read %q through a relative directory", got)
		}
	})
	t.Run("directory is a symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "job")
		if err := os.Symlink(good, link); err != nil {
			t.Fatal(err)
		}
		if got := readCallerRecordID(link, r); got != "" {
			t.Errorf("read %q through a symlinked directory", got)
		}
	})
	t.Run("record is a symlink", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink(filepath.Join(good, r.FileName), filepath.Join(dir, r.FileName)); err != nil {
			t.Fatal(err)
		}
		if got := readCallerRecordID(dir, r); got != "" {
			t.Errorf("read %q through a symlinked record", got)
		}
	})
	t.Run("record is a directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, r.FileName), 0o700); err != nil {
			t.Fatal(err)
		}
		if got := readCallerRecordID(dir, r); got != "" {
			t.Errorf("read %q from a directory", got)
		}
	})
	t.Run("record too large", func(t *testing.T) {
		dir := t.TempDir()
		big := `{"pad":"` + strings.Repeat("x", maxCallerRecordBytes) + `"}`
		if err := os.WriteFile(filepath.Join(dir, r.FileName), []byte(big), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := readCallerRecordID(dir, r); got != "" {
			t.Errorf("read %q from an oversized record", got)
		}
	})
	t.Run("record owned by someone else", func(t *testing.T) {
		prev := callerRecordOwnerUID
		callerRecordOwnerUID = func() int { return os.Getuid() + 1 }
		t.Cleanup(func() { callerRecordOwnerUID = prev })
		if got := readCallerRecordID(good, r); got != "" {
			t.Errorf("read %q from a record another user owns", got)
		}
	})
	t.Run("record not JSON", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, r.FileName), []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := readCallerRecordID(dir, r); got != "" {
			t.Errorf("read %q from a malformed record", got)
		}
	})
}
