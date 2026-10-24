package authority

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CheckSchemaV1 is the schema string of the document `niwa authority check`
// prints. Fields added later are additive and don't change it.
const CheckSchemaV1 = "niwa.authority.check/v1"

// CheckState is a session's binding state: whether it is the session its
// instance's dispatch launched.
type CheckState string

const (
	// StateBound is the dispatch's own session, with a usable record.
	StateBound CheckState = "bound"
	// StateRecordUnusable is the dispatch's own session whose record is
	// missing, malformed or names another dispatch. A gate gives it no more
	// than an instance its exemption list doesn't name.
	StateRecordUnusable CheckState = "record-unusable"
	// StateNotDispatched is every other session.
	StateNotDispatched CheckState = "not-dispatched"
)

// Binding is the evidence a bound session was bound through.
type Binding string

// BindingMapping is the session mapping dispatch writes once it has captured
// the worker's session id.
const BindingMapping Binding = "mapping"

// The reason codes for a state other than bound. They are machine-stable: a
// reader may match on them.
const (
	ReasonNoMapping           = "no-mapping"
	ReasonHookMapping         = "hook-mapping"
	ReasonOtherInstance       = "other-instance"
	ReasonRecordMissing       = "record-missing"
	ReasonRecordMalformed     = "record-malformed"
	ReasonRecordOtherDispatch = "record-other-dispatch"
)

// CheckParent is the dispatcher of a bound session's dispatch. Instance is
// empty when the dispatcher's lineage entry no longer exists.
type CheckParent struct {
	DispatchID string `json:"dispatch_id"`
	Instance   string `json:"instance,omitempty"`
}

// CheckPoint is one decision point's answer for a bound session, decided from
// its record's level the way `niwa authority decide` decides it.
type CheckPoint struct {
	Point           string `json:"point"`
	Decider         string `json:"decider"`
	AuthorExclusion string `json:"author_exclusion"`
}

// CheckDoc is the document `niwa authority check` prints. Its fields are
// declared in the order the document writes them; the omitempty ones are
// present only for the states that carry them.
type CheckDoc struct {
	Schema             string       `json:"schema"`
	State              CheckState   `json:"state"`
	Reason             string       `json:"reason,omitempty"`
	Binding            Binding      `json:"binding,omitempty"`
	DispatchID         string       `json:"dispatch_id,omitempty"`
	Parent             *CheckParent `json:"parent,omitempty"`
	Grant              []grantDoc   `json:"grant"`
	Level              string       `json:"level,omitempty"`
	LevelSource        string       `json:"level_source,omitempty"`
	GrantCeilingSource string       `json:"grant_ceiling_source,omitempty"`
	Points             []CheckPoint `json:"points,omitempty"`

	// grant is the bound grant, kept for the plain text.
	grant []GrantEntry
}

// CheckNotDispatched is the answer for a session that isn't its instance's
// dispatched session.
func CheckNotDispatched(reason string) CheckDoc {
	return CheckDoc{Schema: CheckSchemaV1, State: StateNotDispatched, Reason: reason, Grant: []grantDoc{}}
}

// CheckRecordUnusable is the answer for the dispatched session of
// dispatchID whose record can't be used.
func CheckRecordUnusable(dispatchID, reason string) CheckDoc {
	return CheckDoc{Schema: CheckSchemaV1, State: StateRecordUnusable, Reason: reason, DispatchID: dispatchID, Grant: []grantDoc{}}
}

// CheckBound is the answer for a session bound through binding to rec's
// dispatch, with one entry per point in the order given. A record stored in
// a later schema version reads as it always does: no grant, level strict.
func CheckBound(binding Binding, rec Record, parent *CheckParent, points []string) CheckDoc {
	d := CheckDoc{
		Schema:             CheckSchemaV1,
		State:              StateBound,
		Binding:            binding,
		DispatchID:         rec.DispatchID,
		Parent:             parent,
		Grant:              []grantDoc{},
		Level:              rec.Level.String(),
		LevelSource:        string(rec.LevelSource),
		GrantCeilingSource: string(rec.GrantCeilingSource),
		Points:             []CheckPoint{},
		grant:              rec.Grant,
	}
	for _, g := range rec.Grant {
		doc := grantDoc{Authority: string(g.Authority), Sources: []string{}}
		for _, s := range g.Sources {
			doc.Sources = append(doc.Sources, string(s))
		}
		d.Grant = append(d.Grant, doc)
	}
	for _, p := range points {
		a := Decide(p, rec.Level, rec.LevelSource, false, NoDeclarations)
		exclusion := "off"
		if a.AuthorExclusion {
			exclusion = "on"
		}
		d.Points = append(d.Points, CheckPoint{Point: a.Point, Decider: a.Decider.String(), AuthorExclusion: exclusion})
	}
	return d
}

// MarshalJSON writes the document indented, as `niwa authority show --json`
// writes the record. A bound answer always carries its points list, empty
// when no point was asked about.
func (d CheckDoc) MarshalJSON() ([]byte, error) {
	type doc CheckDoc
	if d.State != StateBound {
		return json.MarshalIndent(doc(d), "", "  ")
	}
	// omitempty would drop an empty points list; a bound answer keeps it.
	type boundDoc struct {
		doc
		Points []CheckPoint `json:"points"`
	}
	return json.MarshalIndent(boundDoc{doc: doc(d), Points: d.Points}, "", "  ")
}

// Text is what plain `niwa authority check` prints: the session's state, its
// grant, and for a bound session its level and each point's answer. Every
// value in it is from a closed vocabulary or passed ParseRecord, so nothing
// here can paint the terminal.
func (d CheckDoc) Text() string {
	var b strings.Builder
	switch d.State {
	case StateBound:
		fmt.Fprintf(&b, "session: bound to dispatch %s (%s)\n", d.DispatchID, d.Binding)
		fmt.Fprintf(&b, "grant: %s\n", GrantListText(d.grant))
		fmt.Fprintf(&b, "level: %s from %s\n", d.Level, d.LevelSource)
		for _, p := range d.Points {
			fmt.Fprintf(&b, "point %s: decider %s, author exclusion %s\n", p.Point, p.Decider, p.AuthorExclusion)
		}
	case StateRecordUnusable:
		fmt.Fprintf(&b, "session: dispatched, record unusable (%s)\n", d.Reason)
		b.WriteString("grant: none\n")
	default:
		fmt.Fprintf(&b, "session: not dispatched (%s)\n", d.Reason)
		b.WriteString("grant: none\n")
	}
	return b.String()
}
