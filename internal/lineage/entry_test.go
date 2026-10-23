package lineage

import (
	"errors"
	"strings"
	"testing"
)

const testDispatchID = "0b9a3c1e-5f2d-4a7b-8c6e-1d2f3a4b5c6d"

func sampleEntry() Entry {
	return Entry{
		DispatchID:     testDispatchID,
		Instance:       "ws+task-0b9a3c1e",
		Agent:          "claude",
		Origin:         OriginDispatch,
		WrittenAt:      "2026-10-23T12:00:00Z",
		ParentSession:  Link{Source: SourceNone},
		ParentDispatch: Link{Source: SourceNone},
		Principal:      Principal{Source: SourceNone, Trust: TrustObserved},
	}
}

func TestMarshal_FixedOrder(t *testing.T) {
	b, err := Marshal(sampleEntry())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema":"niwa.lineage.entry/v1","dispatch_id":"0b9a3c1e-5f2d-4a7b-8c6e-1d2f3a4b5c6d",` +
		`"instance":"ws+task-0b9a3c1e","agent":"claude","origin":"dispatch","written_at":"2026-10-23T12:00:00Z",` +
		`"parent_session":{"id":"","source":"none"},"parent_dispatch":{"id":"","source":"none"},` +
		`"principal":{"name":"","source":"none","trust":"observed"}}`
	if string(b) != want {
		t.Errorf("Marshal =\n%s\nwant\n%s", b, want)
	}
}

func TestMarshal_SetsSchema(t *testing.T) {
	e := sampleEntry()
	e.Schema = "something else"
	b, err := Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), `{"schema":"niwa.lineage.entry/v1",`) {
		t.Errorf("Marshal = %s", b)
	}
}

func TestParse_RoundTrip(t *testing.T) {
	e := sampleEntry()
	e.SessionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	e.ParentSession = Link{ID: "caller-session", Source: SourceUnverified, MappedSessionID: "other-session"}
	b, err := Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	e.Schema = SchemaEntryV1
	if p.Entry != e || p.LaterVersion || p.Unknown != nil {
		t.Errorf("Parse = %+v, want %+v", p, e)
	}
}

func TestParse_KeepsUnknownMembers(t *testing.T) {
	b, _ := Marshal(sampleEntry())
	doc := strings.TrimSuffix(string(b), "}") + `,"future_field":{"a":[1,2]},"note":"x"}`
	p, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Unknown) != 2 || string(p.Unknown["future_field"]) != `{"a":[1,2]}` || string(p.Unknown["note"]) != `"x"` {
		t.Errorf("unknown = %v", p.Unknown)
	}
	if p.Entry.DispatchID != testDispatchID {
		t.Errorf("dispatch id = %q", p.Entry.DispatchID)
	}
}

func TestParse_LaterVersion(t *testing.T) {
	doc := `{"schema":"niwa.lineage.entry/v2","dispatch_id":"` + testDispatchID + `","instance":7,"whatever":true}`
	p, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := Entry{Schema: "niwa.lineage.entry/v2", DispatchID: testDispatchID}
	if !p.LaterVersion || p.Entry != want || p.Unknown != nil {
		t.Errorf("Parse = %+v", p)
	}
}

func TestParse_RefusesOtherSchemas(t *testing.T) {
	for name, doc := range map[string]string{
		"no schema":       `{"dispatch_id":"` + testDispatchID + `"}`,
		"schema a number": `{"schema":1}`,
		"another schema":  `{"schema":"niwa.authority.record/v1"}`,
		"v0":              `{"schema":"niwa.lineage.entry/v0"}`,
		"v01":             `{"schema":"niwa.lineage.entry/v01"}`,
		"suffix":          `{"schema":"niwa.lineage.entry/v1x"}`,
		"not an object":   `["schema"]`,
		"null":            `null`,
		"not json":        `{`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(doc))
			var ie *InvalidEntryError
			if !errors.As(err, &ie) {
				t.Errorf("Parse(%s) err = %v, want *InvalidEntryError", doc, err)
			}
		})
	}
}

func TestParse_RefusesBadV1(t *testing.T) {
	for name, doc := range map[string]string{
		"bad dispatch id":  `{"schema":"niwa.lineage.entry/v1","dispatch_id":"../../etc/passwd"}`,
		"wrong type":       `{"schema":"niwa.lineage.entry/v1","dispatch_id":"` + testDispatchID + `","instance":7}`,
		"wrong link type":  `{"schema":"niwa.lineage.entry/v1","dispatch_id":"` + testDispatchID + `","parent_session":"x"}`,
		"missing dispatch": `{"schema":"niwa.lineage.entry/v1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(doc))
			var ie *InvalidEntryError
			if !errors.As(err, &ie) {
				t.Errorf("err = %v, want *InvalidEntryError", err)
			}
			if err != nil && strings.Contains(err.Error(), "passwd") {
				t.Errorf("error echoes a stored value: %v", err)
			}
		})
	}
}
