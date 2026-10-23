package lineage

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// withMember returns the sample entry's document with one top-level member
// replaced (or added) by raw JSON.
func withMember(t *testing.T, key, raw string) []byte {
	t.Helper()
	b, err := Marshal(sampleEntry())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	m[key] = json.RawMessage(raw)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParse_RevalidatesKnownFields(t *testing.T) {
	cases := map[string][2]string{
		"instance empty":            {"instance", `""`},
		"instance with a slash":     {"instance", `"../x"`},
		"instance too long":         {"instance", `"` + strings.Repeat("a", 256) + `"`},
		"agent upper case":          {"agent", `"Claude"`},
		"agent empty":               {"agent", `""`},
		"origin with a space":       {"origin", `"dis patch"`},
		"origin too long":           {"origin", `"` + strings.Repeat("a", 33) + `"`},
		"written_at not a time":     {"written_at", `"yesterday"`},
		"written_at not UTC":        {"written_at", `"2026-10-23T12:00:00+02:00"`},
		"session_id bad":            {"session_id", `"has space"`},
		"parent session id bad":     {"parent_session", `{"id":"x","source":"handed"}`},
		"parent session source bad": {"parent_session", `{"id":"","source":"Observed"}`},
		"mapped session id bad":     {"parent_session", `{"id":"c0c0c0c0-1111-4111-8111-c0c0c0c0c0c0","source":"unverified","mapped_session_id":"no"}`},
		"parent dispatch traversal": {"parent_dispatch", `{"id":"../../etc/passwd","source":"observed"}`},
		"parent dispatch no source": {"parent_dispatch", `{"id":""}`},
		"principal name bad":        {"principal", `{"name":"dana smith","source":"declared","trust":"observed"}`},
		"principal name too long":   {"principal", `{"name":"` + strings.Repeat("a", 129) + `","source":"declared","trust":"observed"}`},
		"principal source bad":      {"principal", `{"name":"","source":"","trust":"observed"}`},
		"principal trust bad":       {"principal", `{"name":"","source":"none","trust":"TRUSTED"}`},
		"principal null":            {"principal", `null`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(withMember(t, c[0], c[1]))
			var ie *InvalidEntryError
			if !errors.As(err, &ie) {
				t.Fatalf("err = %v, want *InvalidEntryError", err)
			}
			if strings.Contains(err.Error(), "passwd") {
				t.Errorf("error echoes a stored value: %v", err)
			}
		})
	}
}

func TestParse_AcceptsUnknownNameValues(t *testing.T) {
	doc := withMember(t, "origin", `"schedule"`)
	var m map[string]json.RawMessage
	_ = json.Unmarshal(doc, &m)
	m["parent_session"] = json.RawMessage(`{"id":"c0c0c0c0-1111-4111-8111-c0c0c0c0c0c0","source":"attested"}`)
	m["parent_dispatch"] = json.RawMessage(`{"id":"7f1d2c3b-9a8e-4d6c-b5a4-0e1f2a3b4c5d","source":"brokered"}`)
	m["principal"] = json.RawMessage(`{"name":"dana+ci@example.com","source":"sso","trust":"verified"}`)
	doc, _ = json.Marshal(m)
	p, err := Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	if p.Entry.Origin != "schedule" || p.Entry.ParentSession.Source != "attested" ||
		p.Entry.ParentDispatch.Source != "brokered" || p.Entry.Principal.Trust != "verified" {
		t.Errorf("entry = %+v", p.Entry)
	}
}

func TestParse_RefusesDuplicateMembers(t *testing.T) {
	good, err := Marshal(sampleEntry())
	if err != nil {
		t.Fatal(err)
	}
	s := string(good)
	for name, doc := range map[string]string{
		"top level": strings.Replace(s, `"agent":"claude"`, `"agent":"claude","agent":"codex"`, 1),
		"nested":    strings.Replace(s, `"parent_dispatch":{"id":""`, `"parent_dispatch":{"id":"","id":"7f1d2c3b-9a8e-4d6c-b5a4-0e1f2a3b4c5d"`, 1),
		"unknown":   strings.Replace(s, `}`+"", `,"x":[{"a":1,"a":2}]}`, 1),
		"later":     `{"schema":"niwa.lineage.entry/v2","dispatch_id":"` + testDispatchID + `","schema":"niwa.lineage.entry/v2"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if doc == s {
				t.Fatal("fixture did not change the document")
			}
			_, err := Parse([]byte(doc))
			var ie *InvalidEntryError
			if !errors.As(err, &ie) {
				t.Errorf("Parse(%s) err = %v, want *InvalidEntryError", doc, err)
			}
			if _, err := SetSessionID([]byte(doc), "c0c0c0c0-1111-4111-8111-c0c0c0c0c0c0"); err == nil {
				t.Error("SetSessionID accepted a duplicate member")
			}
		})
	}
	// The same key in sibling objects is not a duplicate.
	if _, err := Parse(good); err != nil {
		t.Errorf("sibling objects with the same keys: %v", err)
	}
}

func TestMarshal_MaxLengthEntryFitsIn4KiB(t *testing.T) {
	sid := strings.Repeat("s", 128)
	name := strings.Repeat("n", 32)
	e := Entry{
		DispatchID:     testDispatchID,
		Instance:       strings.Repeat("i", 255),
		Agent:          name,
		Origin:         name,
		WrittenAt:      "2026-10-23T12:00:00Z",
		SessionID:      sid,
		ParentSession:  Link{ID: sid, Source: name, MappedSessionID: sid},
		ParentDispatch: Link{ID: testDispatchID, Source: name},
		Principal:      Principal{Name: strings.Repeat("p", 128), Source: name, Trust: name},
	}
	b, err := Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(b); err != nil {
		t.Fatalf("a maximal entry doesn't read back: %v", err)
	}
	if len(b)+1 > 4<<10 {
		t.Errorf("maximal entry is %d bytes, want at most 4 KiB", len(b)+1)
	}
}

func TestSetSessionID_PreservesMembers(t *testing.T) {
	const sid = "c0c0c0c0-1111-4111-8111-c0c0c0c0c0c0"
	good, err := Marshal(sampleEntry())
	if err != nil {
		t.Fatal(err)
	}
	// An unknown member a later niwa wrote, with spacing and a '<' a
	// re-marshal would change. Whitespace between members is not kept.
	stored := strings.TrimSuffix(string(good), "}") + `,"later_field":{"b": 2,  "a": "é<"}}` + "\n"
	out, err := SetSessionID([]byte(stored), sid)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(strings.TrimSuffix(stored, "\n"), `"written_at":"2026-10-23T12:00:00Z"`,
		`"written_at":"2026-10-23T12:00:00Z","session_id":"`+sid+`"`, 1) + "\n"
	if string(out) != want {
		t.Errorf("rewrite =\n%s\nwant\n%s", out, want)
	}
	p, err := Parse(out)
	if err != nil || p.Entry.SessionID != sid || string(p.Unknown["later_field"]) != `{"b": 2,  "a": "é<"}` {
		t.Errorf("parsed %+v, unknown %s, err %v", p.Entry, p.Unknown["later_field"], err)
	}
}

func TestSetSessionID_Refusals(t *testing.T) {
	const sid = "c0c0c0c0-1111-4111-8111-c0c0c0c0c0c0"
	withID := sampleEntry()
	withID.SessionID = sid
	recorded, _ := Marshal(withID)
	good, _ := Marshal(sampleEntry())
	if _, err := SetSessionID(recorded, sid); !errors.Is(err, ErrSessionIDRecorded) {
		t.Errorf("already recorded: err = %v", err)
	}
	emptyMember := strings.Replace(string(good), `"written_at"`, `"session_id":"","written_at"`, 1)
	if _, err := SetSessionID([]byte(emptyMember), sid); !errors.Is(err, ErrSessionIDRecorded) {
		t.Errorf("empty session_id member: err = %v", err)
	}
	later := `{"schema":"niwa.lineage.entry/v2","dispatch_id":"` + testDispatchID + `"}`
	if _, err := SetSessionID([]byte(later), sid); !errors.Is(err, ErrLaterVersion) {
		t.Errorf("later version: err = %v", err)
	}
	if _, err := SetSessionID(good, "has space"); err == nil {
		t.Error("accepted a bad session id")
	}
	if _, err := SetSessionID([]byte(`{`), sid); err == nil {
		t.Error("accepted a malformed document")
	}
}
