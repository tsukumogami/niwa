package authority

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const testDispatchID = "3c803591-4c01-486f-a927-f96ec399d6a7"
const testParentID = "7f1d2c3b-9a8e-4d6c-b5a4-0e1f2a3b4c5d"

// goldenV1 is the document a written record stores and `show --json` prints.
const goldenV1 = `{
  "schema": "niwa.authority.record/v1",
  "record": true,
  "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7",
  "parent_session_id": "session_abc-123",
  "parent_dispatch_id": "7f1d2c3b-9a8e-4d6c-b5a4-0e1f2a3b4c5d",
  "parent_dispatch_source": "cwd",
  "level": "strict",
  "level_source": "flag",
  "level_ceiling": "supervised",
  "level_ceiling_source": "workspace",
  "grant_ceiling": [
    "merge-pr",
    "close-issue"
  ],
  "grant_ceiling_source": "machine",
  "roles": [
    "coordinator"
  ],
  "grant": [
    {
      "authority": "merge-pr",
      "sources": [
        "role:coordinator"
      ]
    },
    {
      "authority": "close-issue",
      "sources": [
        "dispatch",
        "role:coordinator"
      ]
    }
  ],
  "unknown_grant": []
}`

// goldenNewer is what a v1 reader prints for a record a later schema wrote.
const goldenNewer = `{
  "schema": "niwa.authority.record/v1",
  "record": true,
  "record_version": "niwa.authority.record/v2",
  "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7",
  "parent_session_id": "",
  "parent_dispatch_id": "",
  "parent_dispatch_source": "none",
  "level": "strict",
  "level_source": "newer-version",
  "level_ceiling": "strict",
  "level_ceiling_source": "newer-version",
  "grant_ceiling": [],
  "grant_ceiling_source": "newer-version",
  "roles": [],
  "grant": [],
  "unknown_grant": []
}`

func goldenRecord() Record {
	return Record{
		Schema:               SchemaV1,
		Present:              true,
		DispatchID:           testDispatchID,
		ParentSessionID:      "session_abc-123",
		ParentDispatchID:     testParentID,
		ParentDispatchSource: ParentSourceCwd,
		Level:                Strict,
		LevelSource:          LayerFlag,
		LevelCeiling:         Supervised,
		LevelCeilingSource:   LayerWorkspace,
		GrantCeiling:         []Authority{MergePR, CloseIssue},
		GrantCeilingSource:   LayerMachine,
		Roles:                []string{"coordinator"},
		Grant: []GrantEntry{
			{Authority: MergePR, Sources: []Source{"role:coordinator"}},
			{Authority: CloseIssue, Sources: []Source{"dispatch", "role:coordinator"}},
		},
		UnknownGrant: []RawGrantEntry{},
	}
}

func TestRecordMarshalJSON_Golden(t *testing.T) {
	b, err := goldenRecord().MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != goldenV1 {
		t.Errorf("document =\n%s\nwant\n%s", b, goldenV1)
	}
	got, err := ParseRecord(b)
	if err != nil {
		t.Fatalf("ParseRecord of the golden document: %v", err)
	}
	if !reflect.DeepEqual(got, goldenRecord()) {
		t.Errorf("round trip =\n%+v\nwant\n%+v", got, goldenRecord())
	}
	if strings.Contains(string(b), "record_version") {
		t.Error("a v1 record's document carries record_version")
	}
}

// doc builds a v1 record document from the golden one with edits applied:
// a key set to nil is removed.
func doc(t *testing.T, edits map[string]any) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(goldenV1), &m); err != nil {
		t.Fatal(err)
	}
	for k, v := range edits {
		if v == nil {
			delete(m, k)
			continue
		}
		m[k] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func grantJSON(entries ...[]string) []any {
	out := []any{}
	for _, e := range entries {
		sources := []any{}
		for _, s := range e[1:] {
			sources = append(sources, s)
		}
		out = append(out, map[string]any{"authority": e[0], "sources": sources})
	}
	return out
}

func mustParse(t *testing.T, b []byte) Record {
	t.Helper()
	r, err := ParseRecord(b)
	if err != nil {
		t.Fatalf("ParseRecord: %v\n%s", err, b)
	}
	return r
}

func TestParseRecord_Malformed(t *testing.T) {
	cases := map[string][]byte{
		"truncated":                    []byte(goldenV1[:len(goldenV1)/2]),
		"not JSON":                     []byte("schema = v1"),
		"array":                        []byte(`[1,2]`),
		"null":                         []byte(`null`),
		"string":                       []byte(`"niwa.authority.record/v1"`),
		"schema v0":                    doc(t, map[string]any{"schema": "niwa.authority.record/v0"}),
		"schema latest":                doc(t, map[string]any{"schema": "niwa.authority.record/latest"}),
		"schema other":                 doc(t, map[string]any{"schema": "other/v1"}),
		"schema missing":               doc(t, map[string]any{"schema": nil}),
		"v2 without dispatch_id":       []byte(`{"schema":"niwa.authority.record/v2"}`),
		"v2 with bad dispatch_id":      []byte(`{"schema":"niwa.authority.record/v2","dispatch_id":"x"}`),
		"missing level":                doc(t, map[string]any{"level": nil}),
		"missing grant":                doc(t, map[string]any{"grant": nil}),
		"missing parent source":        doc(t, map[string]any{"parent_dispatch_source": nil}),
		"missing unknown_grant":        doc(t, map[string]any{"unknown_grant": nil}),
		"missing record":               doc(t, map[string]any{"record": nil}),
		"level wrong type":             doc(t, map[string]any{"level": 2}),
		"roles wrong type":             doc(t, map[string]any{"roles": "coordinator"}),
		"grant ceiling null":           doc(t, map[string]any{"grant_ceiling": json.RawMessage("null")}),
		"record wrong type":            doc(t, map[string]any{"record": "true"}),
		"unknown level":                doc(t, map[string]any{"level": "unbounded"}),
		"unknown level ceiling":        doc(t, map[string]any{"level_ceiling": "Strict"}),
		"empty dispatch_id":            doc(t, map[string]any{"dispatch_id": ""}),
		"dispatch_id not a UUID":       doc(t, map[string]any{"dispatch_id": "abc"}),
		"parent dispatch id not UUID":  doc(t, map[string]any{"parent_dispatch_id": "abc"}),
		"parent session id bad":        doc(t, map[string]any{"parent_session_id": "bad id!"}),
		"role bad name":                doc(t, map[string]any{"roles": []any{"Coordinator"}}),
		"authority bad name":           doc(t, map[string]any{"grant": grantJSON([]string{"Merge-PR", "dispatch"})}),
		"source bad name":              doc(t, map[string]any{"grant": grantJSON([]string{"merge-pr", "Dispatch"})}),
		"source bad kind":              doc(t, map[string]any{"grant": grantJSON([]string{"merge-pr", "role:"})}),
		"grant ceiling bad name":       doc(t, map[string]any{"grant_ceiling": []any{"merge pr"}}),
		"known grant out of R3 order":  doc(t, map[string]any{"grant": grantJSON([]string{"close-issue", "dispatch"}, []string{"merge-pr", "dispatch"})}),
		"known grant repeated":         doc(t, map[string]any{"grant": grantJSON([]string{"merge-pr", "dispatch"}, []string{"merge-pr", "role:x"})}),
		"control in session id":        doc(t, map[string]any{"parent_session_id": "abcdefgh\u0007"}),
		"control in role":              doc(t, map[string]any{"roles": []any{"coord\u001binator"}}),
		"control in source":            doc(t, map[string]any{"grant": grantJSON([]string{"merge-pr", "dispatch\n"})}),
		"control in parent source":     doc(t, map[string]any{"parent_dispatch_source": "cwd\u0000"}),
		"none as level source":         doc(t, map[string]any{"level_source": "none"}),
		"none as level ceiling source": doc(t, map[string]any{"level_ceiling_source": "none"}),
		"newer-version level source":   doc(t, map[string]any{"level_source": "newer-version"}),
		"newer-version ceiling source": doc(t, map[string]any{"level_ceiling_source": "newer-version"}),
		"newer-version grant source":   doc(t, map[string]any{"grant_ceiling_source": "newer-version"}),
		"grant entry without sources":  doc(t, map[string]any{"grant": []any{map[string]any{"authority": "merge-pr", "sources": []any{}}}}),
		"grant entry not an object":    doc(t, map[string]any{"grant": []any{"merge-pr"}}),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRecord(b)
			var me *MalformedRecordError
			if !errors.As(err, &me) {
				t.Errorf("err = %v, want *MalformedRecordError", err)
			}
		})
	}
}

func TestParseRecord_Sources(t *testing.T) {
	r := mustParse(t, doc(t, map[string]any{"grant_ceiling_source": "none"}))
	if r.GrantCeilingSource != LayerNone {
		t.Errorf("grant ceiling source %s", r.GrantCeilingSource)
	}
	r = mustParse(t, doc(t, map[string]any{"grant_ceiling_source": "delegated"}))
	if r.GrantCeilingSource != "delegated" {
		t.Errorf("unknown grant ceiling source read as %q, want it kept as stored", r.GrantCeilingSource)
	}
}

func TestParseRecord_UnknownParentDispatchSource(t *testing.T) {
	r := mustParse(t, doc(t, map[string]any{"parent_dispatch_source": "inherited"}))
	if r.ParentDispatchSource != ParentSourceNone || r.ParentDispatchID != "" {
		t.Errorf("parent %q from %q, want empty from none", r.ParentDispatchID, r.ParentDispatchSource)
	}
}

func TestParseRecord_NewerVersion(t *testing.T) {
	for _, schema := range []string{"niwa.authority.record/v2", "niwa.authority.record/v10"} {
		t.Run(schema, func(t *testing.T) {
			b := []byte(`{"schema":"` + schema + `","dispatch_id":"` + testDispatchID + `",` +
				`"grant":[{"authority":"merge-pr","sources":["dispatch"]}],"level":"unbounded"}`)
			r := mustParse(t, b)
			want := Record{
				Schema: SchemaV1, Present: true, NewerVersion: schema, DispatchID: testDispatchID,
				ParentDispatchSource: ParentSourceNone,
				Level:                Strict, LevelSource: LayerNewerVersion,
				LevelCeiling: Strict, LevelCeilingSource: LayerNewerVersion,
				GrantCeiling: []Authority{}, GrantCeilingSource: LayerNewerVersion,
				Roles: []string{}, Grant: []GrantEntry{}, UnknownGrant: []RawGrantEntry{},
			}
			if !reflect.DeepEqual(r, want) {
				t.Errorf("record =\n%+v\nwant\n%+v", r, want)
			}
		})
	}
	r := mustParse(t, []byte(`{"schema":"niwa.authority.record/v2","dispatch_id":"`+testDispatchID+`"}`))
	b, err := r.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != goldenNewer {
		t.Errorf("document =\n%s\nwant\n%s", b, goldenNewer)
	}
}

func TestParseRecord_Additive(t *testing.T) {
	t.Run("unknown top-level field", func(t *testing.T) {
		r := mustParse(t, doc(t, map[string]any{"supervisor": "someone"}))
		if !reflect.DeepEqual(r, goldenRecord()) {
			t.Errorf("record = %+v", r)
		}
	})
	t.Run("unknown authority", func(t *testing.T) {
		r := mustParse(t, doc(t, map[string]any{"grant": grantJSON(
			[]string{"merge-pr", "dispatch"}, []string{"teardown", "dispatch"})}))
		if !reflect.DeepEqual(r.Grant, []GrantEntry{{Authority: MergePR, Sources: []Source{"dispatch"}}}) {
			t.Errorf("grant = %+v", r.Grant)
		}
		if !reflect.DeepEqual(r.UnknownGrant, []RawGrantEntry{{Authority: "teardown", Sources: []string{"dispatch"}}}) {
			t.Errorf("unknown grant = %+v", r.UnknownGrant)
		}
	})
	t.Run("unknown source kind only", func(t *testing.T) {
		r := mustParse(t, doc(t, map[string]any{"grant": grantJSON([]string{"merge-pr", "delegated:x"})}))
		if len(r.Grant) != 0 {
			t.Errorf("grant = %+v, want merge-pr not held", r.Grant)
		}
		if !reflect.DeepEqual(r.UnknownGrant, []RawGrantEntry{{Authority: "merge-pr", Sources: []string{"delegated:x"}}}) {
			t.Errorf("unknown grant = %+v", r.UnknownGrant)
		}
	})
	t.Run("one known source keeps the entry", func(t *testing.T) {
		r := mustParse(t, doc(t, map[string]any{"grant": grantJSON([]string{"merge-pr", "dispatch", "delegated:x"})}))
		if !reflect.DeepEqual(r.Grant, []GrantEntry{{Authority: MergePR, Sources: []Source{"dispatch"}}}) {
			t.Errorf("grant = %+v", r.Grant)
		}
		if len(r.UnknownGrant) != 0 {
			t.Errorf("unknown grant = %+v", r.UnknownGrant)
		}
	})
	t.Run("unknown name in grant ceiling", func(t *testing.T) {
		r := mustParse(t, doc(t, map[string]any{"grant_ceiling": []any{"merge-pr", "teardown"}}))
		if !reflect.DeepEqual(r.GrantCeiling, []Authority{MergePR}) {
			t.Errorf("grant ceiling = %v", r.GrantCeiling)
		}
		for _, g := range r.UnknownGrant {
			if g.Authority == "teardown" {
				t.Error("a ceiling name was listed in unknown_grant")
			}
		}
	})
	t.Run("unknown grant in name order", func(t *testing.T) {
		r := mustParse(t, doc(t, map[string]any{
			"grant": grantJSON([]string{"merge-pr", "dispatch"}, []string{"zap", "dispatch"}, []string{"approve", "role:x"}),
		}))
		var got []string
		for _, g := range r.UnknownGrant {
			got = append(got, g.Authority)
		}
		if !reflect.DeepEqual(got, []string{"approve", "zap"}) {
			t.Errorf("unknown grant order = %v", got)
		}
	})
	t.Run("field added within v1 defaults when absent", func(t *testing.T) {
		// No field has been added within v1 yet, so the test stands one in:
		// unknown_grant registered as a later optional field with its
		// default, and a record that lacks it.
		saved := v1Optional
		v1Optional = map[string]string{"unknown_grant": "[]"}
		defer func() { v1Optional = saved }()
		r := mustParse(t, doc(t, map[string]any{"unknown_grant": nil}))
		if r.UnknownGrant == nil || len(r.UnknownGrant) != 0 {
			t.Errorf("unknown grant = %v, want its default", r.UnknownGrant)
		}
	})
}
