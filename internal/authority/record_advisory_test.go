package authority

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func advisoryRecord() Record {
	r := goldenRecord()
	r.ParentSessionSource = "observed"
	r.Principal = &PrincipalDoc{Name: "dana@example.com", Source: "inherited", Trust: "observed"}
	return r
}

// The advisory fields sit right after parent_dispatch_source and read back
// as written.
func TestRecordAdvisory_RoundTrip(t *testing.T) {
	b, err := advisoryRecord().MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `"parent_dispatch_source": "cwd",
  "parent_session_source": "observed",
  "principal": {
    "name": "dana@example.com",
    "source": "inherited",
    "trust": "observed"
  },
  "level": `
	if !strings.Contains(string(b), want) {
		t.Errorf("document does not carry the fields after parent_dispatch_source:\n%s", b)
	}
	if got := mustParse(t, b); !reflect.DeepEqual(got, advisoryRecord()) {
		t.Errorf("round trip = %+v", got)
	}
}

// A record written before the fields reads with both unrecorded, and its
// document omits them.
func TestRecordAdvisory_OlderRecordReadsUnrecorded(t *testing.T) {
	r := mustParse(t, doc(t, nil))
	if r.ParentSessionSource != "" || r.Principal != nil {
		t.Errorf("source %q, principal %+v; want unrecorded", r.ParentSessionSource, r.Principal)
	}
	b, err := r.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "parent_session_source") || strings.Contains(string(b), `"principal"`) {
		t.Errorf("unrecorded fields printed:\n%s", b)
	}
}

// A value of the wrong type or outside its shape reads as unrecorded and
// never voids the record or its grant.
func TestRecordAdvisory_LenientRead(t *testing.T) {
	valid := map[string]any{"name": "dana", "source": "declared", "trust": "observed"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{}
		for key, val := range valid {
			m[key] = val
		}
		m[k] = v
		return m
	}
	sources := map[string]any{
		"number":            7,
		"object":            map[string]any{},
		"null":              json.RawMessage("null"),
		"uppercase":         "Observed",
		"too long":          strings.Repeat("a", 33),
		"control character": "obs\u0007erved",
		"empty":             "",
	}
	for name, v := range sources {
		t.Run("source "+name, func(t *testing.T) {
			r := mustParse(t, doc(t, map[string]any{"parent_session_source": v, "principal": valid}))
			if r.ParentSessionSource != "" {
				t.Errorf("source = %q, want unrecorded", r.ParentSessionSource)
			}
			if r.Principal == nil {
				t.Error("a bad source voided the principal")
			}
			if !reflect.DeepEqual(r.Grant, goldenRecord().Grant) {
				t.Errorf("grant = %+v", r.Grant)
			}
		})
	}
	principals := map[string]any{
		"string":            "dana",
		"array":             []any{},
		"name not a string": with("name", 3),
		"name too long":     with("name", strings.Repeat("a", 129)),
		"name with space":   with("name", "dana smith"),
		"missing source":    with("source", nil),
		"bad trust":         with("trust", "Not Valid"),
		"control character": with("name", "da\u001bna"),
	}
	for name, v := range principals {
		t.Run("principal "+name, func(t *testing.T) {
			if m, ok := v.(map[string]any); ok {
				for k, val := range m {
					if val == nil {
						delete(m, k)
					}
				}
			}
			r := mustParse(t, doc(t, map[string]any{"parent_session_source": "handed", "principal": v}))
			if r.Principal != nil {
				t.Errorf("principal = %+v, want unrecorded", r.Principal)
			}
			if r.ParentSessionSource != "handed" {
				t.Errorf("source = %q", r.ParentSessionSource)
			}
			if !reflect.DeepEqual(r.Grant, goldenRecord().Grant) {
				t.Errorf("grant = %+v", r.Grant)
			}
		})
	}
	t.Run("unknown values of the right shape are kept", func(t *testing.T) {
		p := map[string]any{"name": "", "source": "vouched", "trust": "vouched", "extra": 1}
		r := mustParse(t, doc(t, map[string]any{"parent_session_source": "vouched", "principal": p}))
		if r.ParentSessionSource != "vouched" || !reflect.DeepEqual(r.Principal, &PrincipalDoc{Source: "vouched", Trust: "vouched"}) {
			t.Errorf("source %q, principal %+v", r.ParentSessionSource, r.Principal)
		}
	})
}

// The new fields change no answer: the grant and the decision are those of
// the same record without them.
func TestRecordAdvisory_AnswersUnchanged(t *testing.T) {
	plain := goldenRecord()
	with := advisoryRecord()
	if !reflect.DeepEqual(plain.Grant, with.Grant) || plain.Level != with.Level || plain.LevelSource != with.LevelSource {
		t.Fatal("fixture differs beyond the advisory fields")
	}
	b, _ := with.MarshalJSON()
	read := mustParse(t, b)
	for _, point := range []string{"merge", "pr-review", "plan-approval"} {
		a := Decide(point, plain.Level, plain.LevelSource, false, NoDeclarations)
		b := Decide(point, read.Level, read.LevelSource, false, NoDeclarations)
		if a != b {
			t.Errorf("%s: %+v vs %+v", point, a, b)
		}
	}
	if !reflect.DeepEqual(read.Grant, plain.Grant) || !reflect.DeepEqual(read.UnknownGrant, plain.UnknownGrant) {
		t.Errorf("grant %+v / %+v", read.Grant, read.UnknownGrant)
	}
}

func TestRecordAdvisory_Text(t *testing.T) {
	text := advisoryRecord().Text()
	for _, line := range []string{
		"parent session source: observed\n",
		"principal: dana@example.com, source inherited, trust observed\n",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("text lacks %q:\n%s", line, text)
		}
	}
	r := goldenRecord()
	r.Principal = &PrincipalDoc{Source: "none", Trust: "observed"}
	if !strings.Contains(r.Text(), "principal: none, source none, trust observed\n") {
		t.Errorf("nameless principal:\n%s", r.Text())
	}
}
