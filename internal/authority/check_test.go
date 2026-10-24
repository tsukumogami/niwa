package authority

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func checkTestRecord() Record {
	return Record{
		Schema:             SchemaV1,
		Present:            true,
		DispatchID:         "3c803591-4c01-486f-a927-f96ec399d6a7",
		Level:              Autonomous,
		LevelSource:        LayerWorkspace,
		GrantCeilingSource: LayerWorkspace,
		Grant: []GrantEntry{
			{Authority: MergePR, Sources: []Source{RoleSource("coordinator")}},
			{Authority: CloseIssue, Sources: []Source{SourceDispatch, RoleSource("coordinator")}},
		},
	}
}

// TestCheckDocKeys pins which keys each state's document carries, in order.
func TestCheckDocKeys(t *testing.T) {
	parent := &CheckParent{DispatchID: "9f0c2a11-77b2-4d0e-8a51-0c1d2e3f4a5b", Instance: "ws+coordinator-1a2b3c4d"}
	for _, tc := range []struct {
		name string
		doc  CheckDoc
		keys []string
	}{
		{"bound", CheckBound(BindingMapping, checkTestRecord(), parent, []string{"merge-pr"}),
			[]string{"schema", "state", "binding", "dispatch_id", "parent", "grant", "level", "level_source", "grant_ceiling_source", "points"}},
		{"bound, no parent, no points", CheckBound(BindingMapping, checkTestRecord(), nil, nil),
			[]string{"schema", "state", "binding", "dispatch_id", "grant", "level", "level_source", "grant_ceiling_source", "points"}},
		{"record-unusable", CheckRecordUnusable("3c803591-4c01-486f-a927-f96ec399d6a7", ReasonRecordMissing),
			[]string{"schema", "state", "reason", "dispatch_id", "grant"}},
		{"not-dispatched", CheckNotDispatched(ReasonNoMapping),
			[]string{"schema", "state", "reason", "grant"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := tc.doc.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			dec := json.NewDecoder(bytes.NewReader(b))
			var keys []string
			if _, err := dec.Token(); err != nil {
				t.Fatal(err)
			}
			for dec.More() {
				k, err := dec.Token()
				if err != nil {
					t.Fatal(err)
				}
				keys = append(keys, k.(string))
				var skip json.RawMessage
				if err := dec.Decode(&skip); err != nil {
					t.Fatal(err)
				}
			}
			if !reflect.DeepEqual(keys, tc.keys) {
				t.Errorf("keys = %v\nwant   %v\n%s", keys, tc.keys, b)
			}
		})
	}
}

func TestCheckBoundGrantAndText(t *testing.T) {
	d := CheckBound(BindingMapping, checkTestRecord(), nil, []string{"merge-pr"})
	want := []grantDoc{
		{Authority: "merge-pr", Sources: []string{"role:coordinator"}},
		{Authority: "close-issue", Sources: []string{"dispatch", "role:coordinator"}},
	}
	if !reflect.DeepEqual(d.Grant, want) {
		t.Errorf("grant = %+v, want %+v", d.Grant, want)
	}
	text := "session: bound to dispatch 3c803591-4c01-486f-a927-f96ec399d6a7 (mapping)\n" +
		"grant: merge-pr (role coordinator), close-issue (dispatch, role coordinator)\n" +
		"level: autonomous from workspace\n" +
		"point merge-pr: decider session, author exclusion off\n"
	if got := d.Text(); got != text {
		t.Errorf("text =\n%s\nwant\n%s", got, text)
	}
}

func TestCheckNotBoundHoldsNothing(t *testing.T) {
	for _, d := range []CheckDoc{CheckNotDispatched(ReasonHookMapping), CheckRecordUnusable("3c803591-4c01-486f-a927-f96ec399d6a7", ReasonRecordMalformed)} {
		b, err := d.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if g, ok := m["grant"].([]any); !ok || len(g) != 0 {
			t.Errorf("%s: grant = %v, want []", d.State, m["grant"])
		}
		if m["schema"] != CheckSchemaV1 {
			t.Errorf("%s: schema = %v", d.State, m["schema"])
		}
	}
}
