package lineage

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/authority"
)

const (
	testSession      = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testOtherSession = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	testNonUUID      = "codex_Session-01"
	testParentID     = "3c803591-4c01-486f-a927-f96ec399d6a7"
	testMappedID     = "0f5c2a1e-9b7d-4e3f-8a6b-1c2d3e4f5a6b"
)

func TestResolveParentSession(t *testing.T) {
	cases := []struct {
		name string
		in   Inputs
		want Link
	}{
		{"variable unset", Inputs{CwdInInstance: true, NewestMappedSession: testSession}, Link{Source: SourceNone}},
		{"variable malformed", Inputs{SessionVar: "not a session id", CwdInInstance: true, NewestMappedSession: "not a session id"}, Link{Source: SourceNone}},
		{"variable too short", Inputs{SessionVar: "abc"}, Link{Source: SourceNone}},
		{"--parent-from given", Inputs{SessionVar: testSession, ParentFromSet: true, CwdInInstance: true, NewestMappedSession: testOtherSession}, Link{ID: testSession, Source: SourceHanded}},
		{"--parent-from from the root", Inputs{SessionVar: testSession, ParentFromSet: true}, Link{ID: testSession, Source: SourceHanded}},
		{"no instance", Inputs{SessionVar: testSession}, Link{ID: testSession, Source: SourceHanded}},
		{"no instance, non-UUID id", Inputs{SessionVar: testNonUUID}, Link{ID: testNonUUID, Source: SourceHanded}},
		{"instance with no mapping", Inputs{SessionVar: testSession, CwdInInstance: true}, Link{ID: testSession, Source: SourceHanded}},
		{"newest mapping matches", Inputs{SessionVar: testSession, CwdInInstance: true, NewestMappedSession: testSession}, Link{ID: testSession, Source: SourceObserved}},
		{"newest mapping differs", Inputs{SessionVar: testSession, CwdInInstance: true, NewestMappedSession: testOtherSession},
			Link{ID: testSession, Source: SourceUnverified, MappedSessionID: testOtherSession}},
		{"newest mapping differs, non-UUID id", Inputs{SessionVar: testNonUUID, CwdInInstance: true, NewestMappedSession: testSession},
			Link{ID: testNonUUID, Source: SourceUnverified, MappedSessionID: testSession}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, _ := Resolve(tc.in)
			if got != tc.want {
				t.Errorf("parent session = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestResolveParentDispatch(t *testing.T) {
	cases := []struct {
		name string
		in   Inputs
		want Link
	}{
		{"the working directory's instance record", Inputs{SessionVar: testSession, CwdInInstance: true, ParentDispatchID: testParentID, ParentDispatchFrom: authority.ParentSourceCwd},
			Link{ID: testParentID, Source: SourceObserved}},
		{"--parent-from", Inputs{SessionVar: testSession, ParentFromSet: true, ParentDispatchID: testParentID, ParentDispatchFrom: authority.ParentSourceAnchor},
			Link{ID: testParentID, Source: SourceHanded}},
		{"--parent-from while the variable names a mapped session",
			Inputs{SessionVar: testSession, ParentFromSet: true, ParentDispatchID: testParentID, ParentDispatchFrom: authority.ParentSourceAnchor, RootMappedDispatch: testMappedID},
			Link{ID: testParentID, Source: SourceHanded}},
		{"--parent-from naming an instance with no record", Inputs{SessionVar: testSession, ParentFromSet: true, ParentDispatchFrom: authority.ParentSourceNone, RootMappedDispatch: testMappedID},
			Link{Source: SourceNone}},
		{"root dispatch, mapping with a dispatch id", Inputs{SessionVar: testSession, ParentDispatchFrom: authority.ParentSourceNone, RootMappedDispatch: testMappedID},
			Link{ID: testMappedID, Source: SourceMapped}},
		{"root dispatch, non-UUID variable", Inputs{SessionVar: testNonUUID, ParentDispatchFrom: authority.ParentSourceNone, RootMappedDispatch: testMappedID},
			Link{Source: SourceNone}},
		{"root dispatch, uppercase UUID variable", Inputs{SessionVar: "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA", RootMappedDispatch: testMappedID},
			Link{Source: SourceNone}},
		{"root dispatch, mapping with no dispatch id", Inputs{SessionVar: testSession, ParentDispatchFrom: authority.ParentSourceNone},
			Link{Source: SourceNone}},
		{"root dispatch, mapping with a malformed dispatch id", Inputs{SessionVar: testSession, RootMappedDispatch: "../../etc"},
			Link{Source: SourceNone}},
		{"root dispatch, variable unset", Inputs{RootMappedDispatch: testMappedID}, Link{Source: SourceNone}},
		{"instance with no record", Inputs{SessionVar: testSession, CwdInInstance: true, ParentDispatchFrom: authority.ParentSourceNone, RootMappedDispatch: testMappedID},
			Link{Source: SourceNone}},
		{"nothing", Inputs{}, Link{Source: SourceNone}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got, _ := Resolve(tc.in)
			if got != tc.want {
				t.Errorf("parent dispatch = %+v, want %+v", got, tc.want)
			}
		})
	}
}
