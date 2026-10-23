package authority

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// SchemaV1 is the schema string of the record format this niwa writes.
const SchemaV1 = "niwa.authority.record/v1"

// Values of a record's parent_dispatch_source.
const (
	ParentSourceAnchor = "anchor" // --parent-from found the parent's record
	ParentSourceCwd    = "cwd"    // the working directory did
	ParentSourceNone   = "none"   // no parent record was found
)

// schemaPattern matches every version of the record format. Only v1 is read
// in full; any later version is read as holding nothing.
var schemaPattern = regexp.MustCompile(`^niwa\.authority\.record/v([1-9][0-9]*)$`)

// Record is one dispatch's authority: what it was granted and under what
// policy. Dispatch writes it into the instance before the worker starts;
// every reader reads it through ParseRecord.
type Record struct {
	Schema               string // SchemaV1
	Present              bool   // encoded as "record"; true for every written record
	DispatchID           string
	ParentSessionID      string
	ParentDispatchID     string
	ParentDispatchSource string // ParentSourceAnchor, ParentSourceCwd or ParentSourceNone
	// ParentSessionSource and Principal are advisory: the lineage entry's
	// parent_session.source and principal, written from the same resolution.
	// No gate reads them, so a value this build can't read is unrecorded ("",
	// nil), never a malformed record.
	ParentSessionSource string
	Principal           *PrincipalDoc
	Level               Level
	LevelSource         Layer
	LevelCeiling        Level
	LevelCeilingSource  Layer
	GrantCeiling        []Authority
	GrantCeilingSource  Layer
	Roles               []string // name order
	Grant               []GrantEntry
	// UnknownGrant is read-side: stored entries this build can't hold, in
	// authority name order. A record this build writes always has none.
	UnknownGrant []RawGrantEntry
	// NewerVersion is read-side: the stored schema when it is a later version
	// than v1. Such a record is read as holding nothing and is never written.
	NewerVersion string
}

// PrincipalDoc is the person a dispatch acts for, as the lineage entry's
// principal records it. It is declared here, not taken from internal/lineage,
// because that package imports this one.
type PrincipalDoc struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Trust  string `json:"trust"`
}

type grantDoc struct {
	Authority string   `json:"authority"`
	Sources   []string `json:"sources"`
}

// recordDoc fixes the document's key order: encoding/json writes struct
// fields in declaration order.
type recordDoc struct {
	Schema               string        `json:"schema"`
	Record               bool          `json:"record"`
	RecordVersion        string        `json:"record_version,omitempty"`
	DispatchID           string        `json:"dispatch_id"`
	ParentSessionID      string        `json:"parent_session_id"`
	ParentDispatchID     string        `json:"parent_dispatch_id"`
	ParentDispatchSource string        `json:"parent_dispatch_source"`
	ParentSessionSource  string        `json:"parent_session_source,omitempty"`
	Principal            *PrincipalDoc `json:"principal,omitempty"`
	Level                string        `json:"level"`
	LevelSource          string        `json:"level_source"`
	LevelCeiling         string        `json:"level_ceiling"`
	LevelCeilingSource   string        `json:"level_ceiling_source"`
	GrantCeiling         []string      `json:"grant_ceiling"`
	GrantCeilingSource   string        `json:"grant_ceiling_source"`
	Roles                []string      `json:"roles"`
	Grant                []grantDoc    `json:"grant"`
	UnknownGrant         []grantDoc    `json:"unknown_grant"`
}

// MarshalJSON writes the one document the record file stores and `niwa
// authority show --json` prints, indented, with "record": true. A record
// read from a later schema version is written as a complete v1 document with
// record_version naming the stored schema right after "record"; a v1
// record's document has no record_version key. The no-record answer is not a
// Record and is built by its caller.
func (r Record) MarshalJSON() ([]byte, error) {
	doc := recordDoc{
		Schema:               SchemaV1,
		Record:               true,
		RecordVersion:        r.NewerVersion,
		DispatchID:           r.DispatchID,
		ParentSessionID:      r.ParentSessionID,
		ParentDispatchID:     r.ParentDispatchID,
		ParentDispatchSource: r.ParentDispatchSource,
		ParentSessionSource:  r.ParentSessionSource,
		Principal:            r.Principal,
		Level:                r.Level.String(),
		LevelSource:          string(r.LevelSource),
		LevelCeiling:         r.LevelCeiling.String(),
		LevelCeilingSource:   string(r.LevelCeilingSource),
		GrantCeiling:         []string{},
		GrantCeilingSource:   string(r.GrantCeilingSource),
		Roles:                []string{},
		Grant:                []grantDoc{},
		UnknownGrant:         []grantDoc{},
	}
	for _, a := range r.GrantCeiling {
		doc.GrantCeiling = append(doc.GrantCeiling, string(a))
	}
	doc.Roles = append(doc.Roles, r.Roles...)
	for _, g := range r.Grant {
		d := grantDoc{Authority: string(g.Authority), Sources: []string{}}
		for _, s := range g.Sources {
			d.Sources = append(d.Sources, string(s))
		}
		doc.Grant = append(doc.Grant, d)
	}
	for _, g := range r.UnknownGrant {
		doc.UnknownGrant = append(doc.UnknownGrant, grantDoc{Authority: g.Authority, Sources: append([]string{}, g.Sources...)})
	}
	return json.MarshalIndent(doc, "", "  ")
}

// MalformedRecordError is a record that can't be read: niwa fails closed on
// it rather than reading it as no record.
type MalformedRecordError struct {
	Reason string
}

func (e *MalformedRecordError) Error() string {
	return "malformed authority record: " + e.Reason
}

func malformed(format string, args ...any) error {
	return &MalformedRecordError{Reason: fmt.Sprintf(format, args...)}
}

// v1Fields are the fields every v1 record carries, each required.
var v1Fields = []string{
	"record", "dispatch_id", "parent_session_id", "parent_dispatch_id",
	"parent_dispatch_source", "level", "level_source", "level_ceiling",
	"level_ceiling_source", "grant_ceiling", "grant_ceiling_source", "roles",
	"grant", "unknown_grant",
}

// v1Optional holds the fields added within v1 after its first release, each
// with the JSON default a reader uses when a record an older niwa wrote
// lacks it. A field added later goes here, not in the required list, so
// records written before it keep reading.
var v1Optional = map[string]string{
	"parent_session_source": `""`,
	"principal":             `null`,
}

// The shapes of the advisory fields, the lineage entry's patterns repeated
// here because internal/lineage imports this package; change them together.
var (
	nameValuePattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	principalPattern = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,128}$`)
)

// lenientNameValue reads an advisory name value: anything but a string of
// the name-value shape reads as "" (unrecorded).
func lenientNameValue(raw json.RawMessage) string {
	s, err := asString(raw)
	if err != nil || !nameValuePattern.MatchString(s) {
		return ""
	}
	return s
}

// lenientPrincipal reads the advisory principal: anything but an object with
// a name (empty or of the principal shape) and a source and trust of the
// name-value shape reads as nil (unrecorded). Members it doesn't know are
// ignored.
func lenientPrincipal(raw json.RawMessage) *PrincipalDoc {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	name, err := asString(m["name"])
	if err != nil || (name != "" && !principalPattern.MatchString(name)) {
		return nil
	}
	source, trust := lenientNameValue(m["source"]), lenientNameValue(m["trust"])
	if source == "" || trust == "" {
		return nil
	}
	return &PrincipalDoc{Name: name, Source: source, Trust: trust}
}

// ParseRecord reads a stored record.
//
// v1 is additive: an unknown field is ignored; a grant entry whose authority
// this build doesn't know, or whose sources are all of unknown kinds, is kept
// out of Grant (so it is never held) and listed in UnknownGrant as stored; an
// entry with at least one known source stays in Grant with its known sources
// only; an unknown name in grant_ceiling is dropped and listed nowhere; an
// unknown parent_dispatch_source reads as "none" with an empty
// ParentDispatchID; an unknown grant_ceiling_source is kept as stored, never
// read as none. The advisory parent_session_source and principal are read
// leniently: missing, of the wrong type or outside their shapes, each reads
// as unrecorded and the rest of the record reads as usual.
//
// A later schema version (niwa.authority.record/v2 and up) is not malformed:
// only schema and the version-independent dispatch_id, which must be a UUID,
// are read, and the record holds nothing: Level and LevelCeiling Strict,
// every source newer-version, empty parents and lists.
//
// Anything else wrong fails with *MalformedRecordError: not a JSON object, a
// schema matching no version, a missing or wrongly typed v1 field, an
// unknown level, a name or id failing its pattern, a known authority out of
// R3 order, control characters in any field, or a v1 source of none or
// newer-version where neither belongs.
func ParseRecord(b []byte) (Record, error) {
	if len(b) > MaxRecordBytes {
		return Record{}, malformed("larger than %d bytes", MaxRecordBytes)
	}
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Record{}, malformed("not a JSON object")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &m); err != nil {
		return Record{}, malformed("not valid JSON")
	}
	schema, err := field(m, "schema", asString)
	if err != nil {
		return Record{}, err
	}
	match := schemaPattern.FindStringSubmatch(schema)
	if match == nil {
		return Record{}, malformed("schema %s is not a niwa.authority.record version", quote(schema))
	}
	dispatchID, err := field(m, "dispatch_id", asString)
	if err != nil {
		return Record{}, err
	}
	if !ValidDispatchID(dispatchID) {
		return Record{}, malformed("dispatch_id is not a UUID")
	}
	if match[1] != "1" {
		return newerVersionRecord(schema, dispatchID), nil
	}
	return parseV1(m, dispatchID)
}

func newerVersionRecord(schema, dispatchID string) Record {
	return Record{
		Schema:               SchemaV1,
		Present:              true,
		NewerVersion:         schema,
		DispatchID:           dispatchID,
		ParentDispatchSource: ParentSourceNone,
		Level:                Strict,
		LevelSource:          LayerNewerVersion,
		LevelCeiling:         Strict,
		LevelCeilingSource:   LayerNewerVersion,
		GrantCeiling:         []Authority{},
		GrantCeilingSource:   LayerNewerVersion,
		Roles:                []string{},
		Grant:                []GrantEntry{},
		UnknownGrant:         []RawGrantEntry{},
	}
}

func parseV1(m map[string]json.RawMessage, dispatchID string) (Record, error) {
	for key, def := range v1Optional {
		if _, ok := m[key]; !ok {
			m[key] = json.RawMessage(def)
		}
	}
	for _, key := range v1Fields {
		if _, ok := m[key]; !ok {
			return Record{}, malformed("missing field %s", key)
		}
	}
	r := Record{Schema: SchemaV1, DispatchID: dispatchID}

	present, err := field(m, "record", asBool)
	if err != nil {
		return Record{}, err
	}
	if !present {
		return Record{}, malformed("record is false in a stored record")
	}
	r.Present = true

	if r.ParentSessionID, err = field(m, "parent_session_id", asString); err != nil {
		return Record{}, err
	}
	if r.ParentSessionID != "" && !ValidSessionID(r.ParentSessionID) {
		return Record{}, malformed("parent_session_id does not match the session id pattern")
	}
	if r.ParentDispatchID, err = field(m, "parent_dispatch_id", asString); err != nil {
		return Record{}, err
	}
	if r.ParentDispatchID != "" && !ValidDispatchID(r.ParentDispatchID) {
		return Record{}, malformed("parent_dispatch_id is neither a UUID nor empty")
	}
	if r.ParentDispatchSource, err = field(m, "parent_dispatch_source", asString); err != nil {
		return Record{}, err
	}
	switch r.ParentDispatchSource {
	case ParentSourceAnchor, ParentSourceCwd, ParentSourceNone:
	default:
		// A later value costs only the parent link.
		r.ParentDispatchSource, r.ParentDispatchID = ParentSourceNone, ""
	}
	r.ParentSessionSource = lenientNameValue(m["parent_session_source"])
	r.Principal = lenientPrincipal(m["principal"])

	if r.Level, err = levelField(m, "level"); err != nil {
		return Record{}, err
	}
	if r.LevelCeiling, err = levelField(m, "level_ceiling"); err != nil {
		return Record{}, err
	}
	if r.LevelSource, err = sourceField(m, "level_source", false); err != nil {
		return Record{}, err
	}
	if r.LevelCeilingSource, err = sourceField(m, "level_ceiling_source", false); err != nil {
		return Record{}, err
	}
	if r.GrantCeilingSource, err = sourceField(m, "grant_ceiling_source", true); err != nil {
		return Record{}, err
	}

	ceiling, err := field(m, "grant_ceiling", asStrings)
	if err != nil {
		return Record{}, err
	}
	r.GrantCeiling = []Authority{}
	seen := map[Authority]bool{}
	for _, name := range ceiling {
		if !ValidName(name) {
			return Record{}, malformed("grant_ceiling entry %s does not match the name pattern", quote(name))
		}
		if a := Authority(name); a.Known() && !seen[a] {
			seen[a] = true
			r.GrantCeiling = append(r.GrantCeiling, a)
		}
	}
	sortAuthorities(r.GrantCeiling)

	roles, err := field(m, "roles", asStrings)
	if err != nil {
		return Record{}, err
	}
	for _, role := range roles {
		if !ValidName(role) {
			return Record{}, malformed("role %s does not match the name pattern", quote(role))
		}
	}
	r.Roles = dedupSorted(roles)

	grant, err := grantField(m, "grant")
	if err != nil {
		return Record{}, err
	}
	stored, err := grantField(m, "unknown_grant")
	if err != nil {
		return Record{}, err
	}
	r.Grant = []GrantEntry{}
	r.UnknownGrant = append([]RawGrantEntry{}, stored...)
	lastRank := -1
	for _, g := range grant {
		a := Authority(g.Authority)
		if !a.Known() {
			r.UnknownGrant = append(r.UnknownGrant, g)
			continue
		}
		if a.rank() <= lastRank {
			return Record{}, malformed("grant lists %s out of order", a)
		}
		lastRank = a.rank()
		known := knownSources(g.Sources)
		if len(known) == 0 {
			r.UnknownGrant = append(r.UnknownGrant, g)
			continue
		}
		r.Grant = append(r.Grant, GrantEntry{Authority: a, Sources: known})
	}
	sort.SliceStable(r.UnknownGrant, func(i, j int) bool {
		return r.UnknownGrant[i].Authority < r.UnknownGrant[j].Authority
	})
	return r, nil
}

// knownSources keeps the sources this build knows, dispatch first and then
// roles by name.
func knownSources(sources []string) []Source {
	var dispatch bool
	var roles []string
	for _, s := range sources {
		switch {
		case s == string(SourceDispatch):
			dispatch = true
		case strings.HasPrefix(s, "role:"):
			roles = append(roles, strings.TrimPrefix(s, "role:"))
		}
	}
	var out []Source
	if dispatch {
		out = append(out, SourceDispatch)
	}
	for _, role := range dedupSorted(roles) {
		out = append(out, RoleSource(role))
	}
	return out
}

// validSource reports whether s is "dispatch" or "<kind>:<name>" with both
// halves matching the name pattern.
func validSource(s string) bool {
	if s == string(SourceDispatch) {
		return true
	}
	kind, name, ok := strings.Cut(s, ":")
	return ok && ValidName(kind) && ValidName(name)
}

var errWrongType = errors.New("wrong type")

// field decodes m[key] with conv, failing malformed when it is missing or of
// the wrong type, or when any string in it holds a control character.
func field[T any](m map[string]json.RawMessage, key string, conv func(json.RawMessage) (T, error)) (T, error) {
	var zero T
	raw, ok := m[key]
	if !ok {
		return zero, malformed("missing field %s", key)
	}
	v, err := conv(raw)
	if err != nil {
		if errors.Is(err, errControl) {
			return zero, malformed("field %s contains a control character", key)
		}
		return zero, malformed("field %s has the wrong type", key)
	}
	return v, nil
}

var errControl = errors.New("control character")

func asString(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", errWrongType
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", errWrongType
	}
	for _, c := range s {
		if unicode.IsControl(c) {
			return "", errControl
		}
	}
	return s, nil
}

func asBool(raw json.RawMessage) (bool, error) {
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, errWrongType
}

func asArray(raw json.RawMessage) ([]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, errWrongType
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, errWrongType
	}
	return items, nil
}

func asStrings(raw json.RawMessage) ([]string, error) {
	items, err := asArray(raw)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		s, err := asString(item)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func levelField(m map[string]json.RawMessage, key string) (Level, error) {
	s, err := field(m, key, asString)
	if err != nil {
		return 0, err
	}
	l, ok := levelByName(s)
	if !ok {
		return 0, malformed("%s %s is not a known level", key, quote(s))
	}
	return l, nil
}

// sourceField reads a level or ceiling source. A name this build doesn't
// know is kept as stored when it matches the name pattern, so a later source
// value reads as declared. none is valid only for the grant ceiling, and
// newer-version never appears in a stored v1 record.
func sourceField(m map[string]json.RawMessage, key string, allowNone bool) (Layer, error) {
	s, err := field(m, key, asString)
	if err != nil {
		return "", err
	}
	if !ValidName(s) {
		return "", malformed("%s %s does not match the name pattern", key, quote(s))
	}
	switch Layer(s) {
	case LayerNewerVersion:
		return "", malformed("%s is %s in a v1 record", key, s)
	case LayerNone:
		if !allowNone {
			return "", malformed("%s is none, which only a grant ceiling may carry", key)
		}
	}
	return Layer(s), nil
}

// grantField reads a list of grant entries, checking each name against its
// pattern but not against this build's vocabulary.
func grantField(m map[string]json.RawMessage, key string) ([]RawGrantEntry, error) {
	items, err := field(m, key, asArray)
	if err != nil {
		return nil, err
	}
	out := []RawGrantEntry{}
	for _, item := range items {
		item = bytes.TrimSpace(item)
		if len(item) == 0 || item[0] != '{' {
			return nil, malformed("%s entry is not an object", key)
		}
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(item, &entry); err != nil {
			return nil, malformed("%s entry is not an object", key)
		}
		name, err := field(entry, "authority", asString)
		if err != nil {
			return nil, malformed("%s entry: %s", key, err.(*MalformedRecordError).Reason)
		}
		if !ValidName(name) {
			return nil, malformed("%s authority %s does not match the name pattern", key, quote(name))
		}
		sources, err := field(entry, "sources", asStrings)
		if err != nil {
			return nil, malformed("%s entry: %s", key, err.(*MalformedRecordError).Reason)
		}
		if len(sources) == 0 {
			return nil, malformed("%s entry %s has no sources", key, name)
		}
		for _, s := range sources {
			if !validSource(s) {
				return nil, malformed("%s source %s does not match the source pattern", key, quote(s))
			}
		}
		out = append(out, RawGrantEntry{Authority: name, Sources: sources})
	}
	return out, nil
}
