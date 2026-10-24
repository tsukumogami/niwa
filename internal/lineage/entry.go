// Package lineage defines the dispatch lineage entry: the durable record of
// who dispatched a session, how niwa came by each parent link, and the person
// the session acts for. An entry is written once per kept dispatch into the
// workspace root's lineage store and outlives the instance it describes.
//
// This package owns the entry's format only. Where entries live, and how they
// are written and read safely, is internal/workspace's lineage store.
package lineage

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/tsukumogami/niwa/internal/authority"
)

// SchemaEntryV1 is the schema every entry this niwa writes carries.
const SchemaEntryV1 = "niwa.lineage.entry/v1"

// OriginDispatch is the origin this niwa writes: the entry came from
// `niwa dispatch`.
const OriginDispatch = "dispatch"

// The link sources. A link's source says how niwa came by its id; only
// observed means niwa saw it for itself.
const (
	SourceObserved   = "observed"
	SourceHanded     = "handed"
	SourceMapped     = "mapped"
	SourceUnverified = "unverified"
	SourceNone       = "none"
)

// The principal sources, beside SourceNone.
const (
	PrincipalInherited = "inherited"
	PrincipalDeclared  = "declared"
)

// The principal trust values.
const (
	TrustObserved = "observed"
	TrustHanded   = "handed"
)

// Link is a parent link: the parent's id, empty when there is none, and how
// niwa came by it.
type Link struct {
	ID              string `json:"id"`
	Source          string `json:"source"`
	MappedSessionID string `json:"mapped_session_id,omitempty"` // parent session only, when unverified
}

// Principal is the person a dispatched session acts for.
type Principal struct {
	Name   string `json:"name"`
	Source string `json:"source"` // inherited | declared | none
	Trust  string `json:"trust"`  // observed | handed
}

// Entry is one dispatch's lineage entry. Its fields are declared in the order
// Marshal writes them.
type Entry struct {
	Schema         string    `json:"schema"`
	DispatchID     string    `json:"dispatch_id"`
	Instance       string    `json:"instance"`
	Agent          string    `json:"agent"`
	Origin         string    `json:"origin"`
	WrittenAt      string    `json:"written_at"`
	SessionID      string    `json:"session_id,omitempty"`
	ParentSession  Link      `json:"parent_session"`
	ParentDispatch Link      `json:"parent_dispatch"`
	Principal      Principal `json:"principal"`
}

// knownMembers are the top-level members a v1 entry defines.
var knownMembers = map[string]bool{
	"schema":          true,
	"dispatch_id":     true,
	"instance":        true,
	"agent":           true,
	"origin":          true,
	"written_at":      true,
	"session_id":      true,
	"parent_session":  true,
	"parent_dispatch": true,
	"principal":       true,
}

// Marshal encodes e with its known members in a fixed order, under
// SchemaEntryV1 whatever e.Schema holds. It writes no unknown members: an
// entry is only ever created from an Entry, and a later rewrite keeps the
// stored bytes rather than re-marshalling.
func Marshal(e Entry) ([]byte, error) {
	e.Schema = SchemaEntryV1
	return json.Marshal(e)
}

// Parsed is one parsed entry document.
type Parsed struct {
	// Entry holds the known members. For a later version it holds only
	// Schema and DispatchID.
	Entry Entry
	// Unknown holds the top-level members a v1 entry carried that this niwa
	// doesn't know, as stored. It is nil for a later version.
	Unknown map[string]json.RawMessage
	// LaterVersion is true for a niwa.lineage.entry/vN document with N > 1,
	// of which nothing but the schema and dispatch id is read.
	LaterVersion bool
}

// InvalidEntryError is a document that isn't a usable entry. Its message
// names what failed, never a value read from the document.
type InvalidEntryError struct {
	Reason string
}

func (e *InvalidEntryError) Error() string { return "invalid lineage entry: " + e.Reason }

func invalid(format string, args ...any) error {
	return &InvalidEntryError{Reason: fmt.Sprintf(format, args...)}
}

// entrySchemaPattern matches any version of the entry schema.
var entrySchemaPattern = regexp.MustCompile(`^niwa\.lineage\.entry/v([1-9][0-9]{0,8})$`)

// Parse reads an entry document. A v1 document parses to its known members
// plus the members it didn't know; a niwa.lineage.entry/vN document with
// N > 1 parses to a later-version result carrying only its dispatch id and
// schema; any other schema, a document that names a member twice at any
// depth, and a v1 document whose known members have the wrong types or fail
// their shapes (validate.go) are an *InvalidEntryError.
func Parse(b []byte) (Parsed, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(b, &members); err != nil {
		return Parsed{}, invalid("not a JSON object")
	}
	if members == nil {
		return Parsed{}, invalid("not a JSON object")
	}
	if err := checkDuplicateMembers(b); err != nil {
		return Parsed{}, err
	}
	rawSchema, ok := members["schema"]
	if !ok {
		return Parsed{}, invalid("no schema")
	}
	var schema string
	if err := json.Unmarshal(rawSchema, &schema); err != nil {
		return Parsed{}, invalid("schema is not a string")
	}
	if schema != SchemaEntryV1 {
		m := entrySchemaPattern.FindStringSubmatch(schema)
		if m == nil {
			return Parsed{}, invalid("not a lineage entry schema")
		}
		if n, err := strconv.Atoi(m[1]); err != nil || n <= 1 {
			return Parsed{}, invalid("not a lineage entry schema")
		}
		var id string
		if raw, ok := members["dispatch_id"]; ok {
			if err := json.Unmarshal(raw, &id); err != nil || !authority.ValidDispatchID(id) {
				id = ""
			}
		}
		return Parsed{Entry: Entry{Schema: schema, DispatchID: id}, LaterVersion: true}, nil
	}

	var e Entry
	if err := json.Unmarshal(b, &e); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) && typeErr.Field != "" {
			return Parsed{}, invalid("%s has the wrong type", typeErr.Field)
		}
		return Parsed{}, invalid("a member has the wrong type")
	}
	if err := validateEntry(e); err != nil {
		return Parsed{}, err
	}
	var unknown map[string]json.RawMessage
	for k, v := range members {
		if knownMembers[k] {
			continue
		}
		if unknown == nil {
			unknown = map[string]json.RawMessage{}
		}
		unknown[k] = v
	}
	return Parsed{Entry: e, Unknown: unknown}, nil
}
