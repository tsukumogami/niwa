package lineage

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/authority"
)

func parentEntry(id, name, trust string) *Entry {
	src := PrincipalInherited
	if name == "" {
		src = SourceNone
	}
	return &Entry{DispatchID: id, Principal: Principal{Name: name, Source: src, Trust: trust}}
}

func TestResolvePrincipal(t *testing.T) {
	// The inputs of a dispatch from inside the parent's instance, whose
	// newest mapping is the calling session: both links observed.
	inside := func() Inputs {
		return Inputs{
			SessionVar: testSession, CwdInInstance: true, NewestMappedSession: testSession,
			ParentDispatchID: testParentID, ParentDispatchFrom: authority.ParentSourceCwd,
		}
	}
	cases := []struct {
		name string
		in   func() Inputs
		want Principal
	}{
		{"nothing declared, no parent", func() Inputs { return Inputs{} },
			Principal{Source: SourceNone, Trust: TrustObserved}},
		{"declared, no parent", func() Inputs { return Inputs{DeclaredPrincipal: "dana@example.com"} },
			Principal{Name: "dana@example.com", Source: PrincipalDeclared, Trust: TrustObserved}},
		{"inherited through observed links", func() Inputs {
			in := inside()
			in.ParentEntry = parentEntry(testParentID, "dana", TrustObserved)
			in.DeclaredPrincipal = "someone-else"
			return in
		}, Principal{Name: "dana", Source: PrincipalInherited, Trust: TrustObserved}},
		{"inherited, the variable unset", func() Inputs {
			in := inside()
			in.SessionVar = ""
			in.ParentEntry = parentEntry(testParentID, "dana", TrustObserved)
			return in
		}, Principal{Name: "dana", Source: PrincipalInherited, Trust: TrustObserved}},
		{"inherited, the instance has no mapping (session link handed)", func() Inputs {
			in := inside()
			in.NewestMappedSession = ""
			in.ParentEntry = parentEntry(testParentID, "dana", TrustObserved)
			return in
		}, Principal{Name: "dana", Source: PrincipalInherited, Trust: TrustObserved}},
		{"inherited past an unverified session link", func() Inputs {
			in := inside()
			in.NewestMappedSession = testOtherSession
			in.ParentEntry = parentEntry(testParentID, "dana", TrustObserved)
			return in
		}, Principal{Name: "dana", Source: PrincipalInherited, Trust: TrustHanded}},
		{"inherited through --parent-from", func() Inputs {
			in := inside()
			in.ParentFromSet, in.NewestMappedSession = true, ""
			in.ParentDispatchFrom = authority.ParentSourceAnchor
			in.ParentEntry = parentEntry(testParentID, "dana", TrustObserved)
			return in
		}, Principal{Name: "dana", Source: PrincipalInherited, Trust: TrustHanded}},
		{"inherited through a mapped link", func() Inputs {
			return Inputs{
				SessionVar: testSession, RootMappedDispatch: testMappedID,
				ParentDispatchFrom: authority.ParentSourceNone,
				ParentEntry:        parentEntry(testMappedID, "dana", TrustObserved),
			}
		}, Principal{Name: "dana", Source: PrincipalInherited, Trust: TrustHanded}},
		{"grandchild of a handed principal", func() Inputs {
			in := inside()
			in.ParentEntry = parentEntry(testParentID, "dana", TrustHanded)
			return in
		}, Principal{Name: "dana", Source: PrincipalInherited, Trust: TrustHanded}},
		{"a readable parent with an empty principal, principal declared", func() Inputs {
			in := inside()
			in.ParentEntry = parentEntry(testParentID, "", TrustObserved)
			in.DeclaredPrincipal = "dana"
			return in
		}, Principal{Source: PrincipalInherited, Trust: TrustObserved}},
		{"parent entry unusable, declared", func() Inputs {
			in := inside()
			in.DeclaredPrincipal = "dana"
			return in
		}, Principal{Name: "dana", Source: PrincipalDeclared, Trust: TrustHanded}},
		{"parent entry unusable, nothing declared", func() Inputs { return inside() },
			Principal{Source: SourceNone, Trust: TrustHanded}},
		{"mapped parent entry unusable", func() Inputs {
			return Inputs{SessionVar: testSession, RootMappedDispatch: testMappedID, ParentDispatchFrom: authority.ParentSourceNone}
		}, Principal{Source: SourceNone, Trust: TrustHanded}},
		{"an entry for another dispatch is not the parent's", func() Inputs {
			in := inside()
			in.ParentEntry = parentEntry(testMappedID, "mallory", TrustObserved)
			in.DeclaredPrincipal = "dana"
			return in
		}, Principal{Name: "dana", Source: PrincipalDeclared, Trust: TrustHanded}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, got := Resolve(tc.in())
			if got != tc.want {
				t.Errorf("principal = %+v, want %+v", got, tc.want)
			}
		})
	}
}
