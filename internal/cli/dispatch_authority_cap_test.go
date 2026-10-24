package cli

import (
	"reflect"
	"testing"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// capFor runs dispatcherCap the way resolveDispatchAuthority does, from dir
// with sessionVar, after dispatchParent found (or didn't find) a parent.
func capFor(t *testing.T, root, dir, sessionVar string) *authority.DispatcherCap {
	t.Helper()
	parent, _, cwdInstance, cwdInInstance, err := dispatchParent(dir, root)
	if err != nil {
		t.Fatal(err)
	}
	return dispatcherCap(root, sessionVar, parent, cwdInstance, cwdInInstance)
}

func resetParentFrom(t *testing.T) {
	t.Helper()
	prev, prevSet := dispatchParentFrom, dispatchParentFromSet
	dispatchParentFrom, dispatchParentFromSet = "", false
	t.Cleanup(func() { dispatchParentFrom, dispatchParentFromSet = prev, prevSet })
}

func TestDispatcherCap(t *testing.T) {
	resetParentFrom(t)
	w := newShowWorkspace(t, "")
	granted := w.instance(t, "ws-granted")
	writeShowRecord(t, granted, showRecord(showDispatchA, authority.Autonomous, authority.LayerWorkspace, authority.MergePR))
	checkMapping(t, w.root, showSessionA, granted, workspace.MappingOriginDispatch, showDispatchA)

	lost := w.instance(t, "ws-lost")
	checkMapping(t, w.root, showSessionB, lost, workspace.MappingOriginDispatch, showDispatchB)

	created := w.instance(t, "ws-created")

	newer := w.instance(t, "ws-newer")
	writeRawRecord(t, newer, `{"schema": "niwa.authority.record/v2", "dispatch_id": "`+showDispatchB+`"}`)

	const handSession = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	checkMapping(t, w.root, handSession, created, "", "")

	held := func(as ...authority.Authority) *authority.DispatcherCap {
		return &authority.DispatcherCap{DispatchID: showDispatchA, Held: as}
	}
	for _, tc := range []struct {
		name, dir, sessionVar string
		want                  *authority.DispatcherCap
	}{
		{"inside a granted instance", granted, "", held(authority.MergePR)},
		{"a repo inside a granted instance, any session variable", granted + "/tools/app", showSessionB, held(authority.MergePR)},
		{"inside a dispatched instance with no record", lost, "", &authority.DispatcherCap{DispatchID: showDispatchB, Missing: true}},
		{"inside an instance niwa create made", created, "", nil},
		{"inside an instance with a later-version record", newer, "", &authority.DispatcherCap{DispatchID: showDispatchB, Held: []authority.Authority{}}},
		{"at the root, as a mapped dispatched session", w.root, showSessionA, held(authority.MergePR)},
		{"at the root, as a dispatched session whose record is gone", w.root, showSessionB, &authority.DispatcherCap{DispatchID: showDispatchB, Missing: true}},
		{"at the root, as a hook-mapped session", w.root, handSession, nil},
		{"at the root, as a session with no mapping", w.root, "dddddddd-dddd-4ddd-8ddd-dddddddddddd", nil},
		{"at the root, with no session variable", w.root, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := capFor(t, w.root, tc.dir, tc.sessionVar)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("cap = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDispatcherCapRootRecordForAnotherDispatch: a root session's mapping
// names a dispatch other than its instance's record does, so the record isn't
// the dispatcher's, and the cap holds nothing.
func TestDispatcherCapRootRecordForAnotherDispatch(t *testing.T) {
	resetParentFrom(t)
	w := newShowWorkspace(t, "")
	inst := w.instance(t, "ws-1")
	writeShowRecord(t, inst, showRecord(showDispatchB, authority.Autonomous, authority.LayerWorkspace, authority.MergePR, authority.CloseIssue))
	checkMapping(t, w.root, showSessionA, inst, workspace.MappingOriginDispatch, showDispatchA)
	got := capFor(t, w.root, w.root, showSessionA)
	want := &authority.DispatcherCap{DispatchID: showDispatchA, Missing: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cap = %+v, want %+v", got, want)
	}
}

// TestDispatcherCapThroughParentFrom: --parent-from names the dispatcher the
// same way the working directory does.
func TestDispatcherCapThroughParentFrom(t *testing.T) {
	resetParentFrom(t)
	w := newShowWorkspace(t, "")
	lost := w.instance(t, "ws-lost")
	checkMapping(t, w.root, showSessionB, lost, workspace.MappingOriginDispatch, showDispatchB)
	dispatchParentFrom, dispatchParentFromSet = lost, true
	got := capFor(t, w.root, w.root, "")
	want := &authority.DispatcherCap{DispatchID: showDispatchB, Missing: true}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("cap = %+v, want %+v", got, want)
	}
}
