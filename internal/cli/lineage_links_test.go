package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestDispatchLineage_LinkSources dispatches once per row of the parent
// session and parent dispatch rules and checks the entry records the source
// that row gives. Every row dispatches: no source refuses.
func TestDispatchLineage_LinkSources(t *testing.T) {
	const (
		sess     = "c0c0c0c0-1111-4111-8111-c0c0c0c0c0c0"
		other    = "d0d0d0d0-2222-4222-8222-d0d0d0d0d0d0"
		nonUUID  = "codex_Thread-0001"
		mappedID = "0f5c2a1e-9b7d-4e3f-8a6b-1c2d3e4f5a6b"
	)
	type mapping struct {
		session, dispatchID string
		parentInstance      bool // the mapping names the parent instance
		age                 time.Duration
	}
	cases := []struct {
		name        string
		sessionVar  string
		fromInside  bool
		anchor      bool
		record      bool
		mappings    []mapping
		wantSession lineage.Link
		wantParent  lineage.Link
	}{
		{name: "variable unset", fromInside: true, record: true, mappings: []mapping{{session: sess, parentInstance: true}},
			wantSession: lineage.Link{Source: "none"}, wantParent: lineage.Link{ID: parentDispatchID, Source: "observed"}},
		{name: "variable malformed", sessionVar: "has space", fromInside: true, record: true, mappings: []mapping{{session: sess, parentInstance: true}},
			wantSession: lineage.Link{Source: "none"}, wantParent: lineage.Link{ID: parentDispatchID, Source: "observed"}},
		{name: "--parent-from from the root, the variable naming a mapped session", sessionVar: sess, anchor: true, record: true,
			mappings:    []mapping{{session: sess, dispatchID: mappedID, parentInstance: true}},
			wantSession: lineage.Link{ID: sess, Source: "handed"}, wantParent: lineage.Link{ID: parentDispatchID, Source: "handed"}},
		{name: "--parent-from inside, a different newest mapping", sessionVar: sess, fromInside: true, anchor: true, record: true,
			mappings:    []mapping{{session: other, parentInstance: true}},
			wantSession: lineage.Link{ID: sess, Source: "handed"}, wantParent: lineage.Link{ID: parentDispatchID, Source: "handed"}},
		{name: "root, no mapping for the variable", sessionVar: sess, record: true,
			wantSession: lineage.Link{ID: sess, Source: "handed"}, wantParent: lineage.Link{Source: "none"}},
		{name: "root, the variable's mapping carries a dispatch id", sessionVar: sess, record: true,
			mappings:    []mapping{{session: sess, dispatchID: mappedID, parentInstance: true}},
			wantSession: lineage.Link{ID: sess, Source: "handed"}, wantParent: lineage.Link{ID: mappedID, Source: "mapped"}},
		{name: "root, the variable's mapping carries no dispatch id", sessionVar: sess,
			mappings:    []mapping{{session: sess, parentInstance: true}},
			wantSession: lineage.Link{ID: sess, Source: "handed"}, wantParent: lineage.Link{Source: "none"}},
		{name: "root, a well-formed non-UUID variable", sessionVar: nonUUID,
			wantSession: lineage.Link{ID: nonUUID, Source: "handed"}, wantParent: lineage.Link{Source: "none"}},
		{name: "inside an instance with no mapping", sessionVar: sess, fromInside: true, record: true,
			mappings:    []mapping{{session: other}},
			wantSession: lineage.Link{ID: sess, Source: "handed"}, wantParent: lineage.Link{ID: parentDispatchID, Source: "observed"}},
		{name: "inside, the newest mapping matches", sessionVar: sess, fromInside: true, record: true,
			mappings:    []mapping{{session: other, parentInstance: true, age: time.Hour}, {session: sess, parentInstance: true}},
			wantSession: lineage.Link{ID: sess, Source: "observed"}, wantParent: lineage.Link{ID: parentDispatchID, Source: "observed"}},
		{name: "inside, the newest mapping differs", sessionVar: sess, fromInside: true, record: true,
			mappings:    []mapping{{session: sess, parentInstance: true, age: time.Hour}, {session: other, parentInstance: true}},
			wantSession: lineage.Link{ID: sess, Source: "unverified", MappedSessionID: other}, wantParent: lineage.Link{ID: parentDispatchID, Source: "observed"}},
		{name: "inside an instance with no record", sessionVar: sess, fromInside: true,
			mappings:    []mapping{{session: sess, dispatchID: mappedID, parentInstance: true}},
			wantSession: lineage.Link{ID: sess, Source: "observed"}, wantParent: lineage.Link{Source: "none"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, f := authorityWorkspace(t, "", "")
			body := ""
			if tc.record {
				body = parentRecord(t)
			}
			parent := installedInstance(t, root, "test-ws-parent", body)
			now := time.Now().UTC()
			for _, m := range tc.mappings {
				path := filepath.Join(root, "test-ws-elsewhere")
				if m.parentInstance {
					path = parent
				}
				if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
					SessionID: m.session, InstanceName: filepath.Base(path), InstancePath: path,
					DispatchID: m.dispatchID, Created: now.Add(-m.age), Origin: "dispatch",
				}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.fromInside {
				chdir(t, parent)
			}
			if tc.anchor {
				dispatchParentFrom, dispatchParentFromSet = parent, true
			}
			t.Setenv("CLAUDE_CODE_SESSION_ID", tc.sessionVar)
			if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch refused: %v\n%s", err, stderr)
			}
			rec := readRecord(t, f)
			stored, found, err := workspace.ReadLineageEntry(root, rec.DispatchID)
			if err != nil || !found {
				t.Fatalf("entry for %s: found %v, err %v", rec.DispatchID, found, err)
			}
			e := stored.Parsed.Entry
			if e.ParentSession != tc.wantSession {
				t.Errorf("parent session = %+v, want %+v", e.ParentSession, tc.wantSession)
			}
			if e.ParentDispatch != tc.wantParent {
				t.Errorf("parent dispatch = %+v, want %+v", e.ParentDispatch, tc.wantParent)
			}
			if rec.ParentSessionID != tc.wantSession.ID {
				t.Errorf("record parent session = %q, want the link's id %q", rec.ParentSessionID, tc.wantSession.ID)
			}
		})
	}
}
