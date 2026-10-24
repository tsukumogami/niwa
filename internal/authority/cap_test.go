package authority

import (
	"errors"
	"testing"
)

func capTestRecord() Record {
	return Record{Grant: []GrantEntry{
		{Authority: MergePR, Sources: []Source{SourceDispatch}},
		{Authority: CloseIssue, Sources: []Source{SourceDispatch, RoleSource("coordinator")}},
	}}
}

func TestCapByDispatcherNilCapLimitsNothing(t *testing.T) {
	if err := CapByDispatcher(capTestRecord(), nil); err != nil {
		t.Errorf("a nil cap refused: %v", err)
	}
}

func TestCapByDispatcherAllowsWhatTheDispatcherHolds(t *testing.T) {
	cap := &DispatcherCap{DispatchID: "3c803591-4c01-486f-a927-f96ec399d6a7", Held: []Authority{MergePR, CloseIssue, CutRelease}}
	if err := CapByDispatcher(capTestRecord(), cap); err != nil {
		t.Errorf("refused a grant inside the cap: %v", err)
	}
	if err := CapByDispatcher(Record{}, &DispatcherCap{DispatchID: cap.DispatchID}); err != nil {
		t.Errorf("refused an empty grant under an empty cap: %v", err)
	}
}

func TestCapByDispatcherNamesEveryWiderAuthority(t *testing.T) {
	cap := &DispatcherCap{DispatchID: "3c803591-4c01-486f-a927-f96ec399d6a7", Held: []Authority{MergePR}}
	err := CapByDispatcher(capTestRecord(), cap)
	var wider *WiderThanDispatcherError
	if !errors.As(err, &wider) {
		t.Fatalf("err = %v, want *WiderThanDispatcherError", err)
	}
	want := "authority close-issue (requested by --grant, role coordinator) is wider than the dispatcher's grant: the dispatcher holds merge-pr (dispatch 3c803591-4c01-486f-a927-f96ec399d6a7)"
	if err.Error() != want {
		t.Errorf("message =\n%s\nwant\n%s", err.Error(), want)
	}
}

func TestCapByDispatcherHoldingNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		cap  DispatcherCap
		want string
	}{
		{"a record that grants nothing", DispatcherCap{DispatchID: "3c803591-4c01-486f-a927-f96ec399d6a7"},
			"authority merge-pr (requested by --grant) is wider than the dispatcher's grant: the dispatcher holds none (dispatch 3c803591-4c01-486f-a927-f96ec399d6a7)"},
		{"a missing record", DispatcherCap{DispatchID: "3c803591-4c01-486f-a927-f96ec399d6a7", Missing: true},
			"authority merge-pr (requested by --grant) is wider than the dispatcher's grant: the dispatcher's record is missing or can't be used, so it holds none (dispatch 3c803591-4c01-486f-a927-f96ec399d6a7)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := Record{Grant: []GrantEntry{{Authority: MergePR, Sources: []Source{SourceDispatch}}}}
			err := CapByDispatcher(rec, &tc.cap)
			if err == nil || err.Error() != tc.want {
				t.Errorf("err = %v\nwant %s", err, tc.want)
			}
		})
	}
}
