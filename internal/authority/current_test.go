package authority

import (
	"reflect"
	"testing"
)

// currentTestRecord was dispatched with --grant merge-pr and --role
// coordinator while the role named merge-pr and close-issue.
func currentTestRecord() Record {
	return Record{
		DispatchID: "3c803591-4c01-486f-a927-f96ec399d6a7",
		Roles:      []string{"coordinator"},
		Grant: []GrantEntry{
			{Authority: MergePR, Sources: []Source{SourceDispatch, RoleSource("coordinator")}},
			{Authority: CloseIssue, Sources: []Source{RoleSource("coordinator")}},
		},
	}
}

func currentTestPolicy(ceiling []Authority, coordinator ...Authority) Policy {
	p := Policy{BaseCeiling: &ceiling, Roles: map[string][]Authority{}}
	if coordinator != nil {
		p.Roles["coordinator"] = coordinator
	}
	return p
}

func entry(a Authority, sources ...Source) GrantEntry {
	return GrantEntry{Authority: a, Sources: sources}
}

func TestCurrentGrant(t *testing.T) {
	all := []Authority{MergePR, CloseIssue, CutRelease}
	coord := RoleSource("coordinator")
	for _, tc := range []struct {
		name   string
		policy Policy
		cap    *DispatcherCap
		want   []GrantEntry
	}{
		{"unchanged", currentTestPolicy(all, MergePR, CloseIssue), nil,
			[]GrantEntry{entry(MergePR, SourceDispatch, coord), entry(CloseIssue, coord)}},
		{"a widened role adds to the grant", currentTestPolicy(all, MergePR, CloseIssue, CutRelease), nil,
			[]GrantEntry{entry(MergePR, SourceDispatch, coord), entry(CloseIssue, coord), entry(CutRelease, coord)}},
		{"a narrowed role takes away", currentTestPolicy(all, MergePR), nil,
			[]GrantEntry{entry(MergePR, SourceDispatch, coord)}},
		{"a removed role grants nothing, and --grant stays", currentTestPolicy(all), nil,
			[]GrantEntry{entry(MergePR, SourceDispatch)}},
		{"a narrowed ceiling drops --grant and role alike", currentTestPolicy([]Authority{CloseIssue}, MergePR, CloseIssue, CutRelease), nil,
			[]GrantEntry{entry(CloseIssue, coord)}},
		{"no ceiling declared holds nothing", Policy{Roles: map[string][]Authority{"coordinator": all}}, nil,
			[]GrantEntry{}},
		{"a dispatcher narrower than the role caps it", currentTestPolicy(all, MergePR, CloseIssue, CutRelease),
			&DispatcherCap{Held: []Authority{MergePR, CutRelease}},
			[]GrantEntry{entry(MergePR, SourceDispatch, coord), entry(CutRelease, coord)}},
		{"a dispatcher holding nothing caps --grant too", currentTestPolicy(all, MergePR, CloseIssue),
			&DispatcherCap{Held: []Authority{}},
			[]GrantEntry{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CurrentGrant(tc.policy, currentTestRecord(), tc.cap)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CurrentGrant =\n%v\nwant\n%v", got, tc.want)
			}
		})
	}
}

func TestCurrentGrantLaterVersionHoldsNothing(t *testing.T) {
	rec := currentTestRecord()
	rec.NewerVersion = "niwa.authority.record/v2"
	all := []Authority{MergePR, CloseIssue, CutRelease}
	if got := CurrentGrant(currentTestPolicy(all, all...), rec, nil); len(got) != 0 {
		t.Errorf("a later-version record holds %v, want nothing", got)
	}
}

func TestCurrentGrantIgnoresRoleSourcesTheRecordDoesNotName(t *testing.T) {
	// A grant entry claiming a role the record's roles list lacks grants
	// nothing through it: only the roles dispatch resolved are re-read.
	rec := Record{Roles: []string{}, Grant: []GrantEntry{entry(CutRelease, RoleSource("releaser"))}}
	all := []Authority{MergePR, CloseIssue, CutRelease}
	p := currentTestPolicy(all)
	p.Roles["releaser"] = []Authority{CutRelease}
	if got := CurrentGrant(p, rec, nil); len(got) != 0 {
		t.Errorf("held %v through a role the record doesn't name", got)
	}
}
