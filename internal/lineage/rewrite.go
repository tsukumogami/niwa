package lineage

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/tsukumogami/niwa/internal/authority"
)

// checkDuplicateMembers refuses a document in which any object, at any depth,
// names a member twice. encoding/json keeps the last of two such members
// without saying so, which would let an edited entry show one value to one
// reader and another to the next. b is already known to be valid JSON.
func checkDuplicateMembers(b []byte) error {
	type frame struct {
		keys      map[string]bool // nil for an array
		expectKey bool
	}
	var stack []*frame
	valueDone := func() {
		if n := len(stack); n > 0 && stack[n-1].keys != nil {
			stack[n-1].expectKey = true
		}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return invalid("not a JSON object")
		}
		if n := len(stack); n > 0 && stack[n-1].keys != nil && stack[n-1].expectKey {
			if key, ok := tok.(string); ok {
				if stack[n-1].keys[key] {
					return invalid("a member is named twice")
				}
				stack[n-1].keys[key] = true
				stack[n-1].expectKey = false
				continue
			}
		}
		switch tok {
		case json.Delim('{'):
			stack = append(stack, &frame{keys: map[string]bool{}, expectKey: true})
		case json.Delim('['):
			stack = append(stack, &frame{})
		case json.Delim('}'), json.Delim(']'):
			stack = stack[:len(stack)-1]
			valueDone()
		default:
			valueDone()
		}
	}
}

// ErrSessionIDRecorded refuses a completion of an entry that already has a
// session_id member.
var ErrSessionIDRecorded = errors.New("the lineage entry already records a session id")

// ErrLaterVersion refuses a rewrite of an entry of a later version, whose
// members this niwa doesn't know.
var ErrLaterVersion = errors.New("the lineage entry is of a later version")

// member is one top-level member as stored: its decoded key, and its key and
// value bytes exactly as they appear in the document.
type member struct {
	key    string
	rawKey []byte
	value  json.RawMessage
}

// orderedMembers splits a JSON object into its top-level members in order,
// each key and value byte for byte as stored.
func orderedMembers(b []byte) ([]member, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, invalid("not a JSON object")
	}
	var out []member
	prev := dec.InputOffset()
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, invalid("not a JSON object")
		}
		key, ok := tok.(string)
		if !ok {
			return nil, invalid("not a JSON object")
		}
		// Between the end of the previous value and the end of this key lie
		// whitespace, the separating comma and the key itself.
		rawKey := bytes.TrimSpace(b[prev:dec.InputOffset()])
		rawKey = bytes.TrimSpace(bytes.TrimPrefix(rawKey, []byte(",")))
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, invalid("not a JSON object")
		}
		out = append(out, member{key: key, rawKey: rawKey, value: v})
		prev = dec.InputOffset()
	}
	return out, nil
}

// SetSessionID returns raw with a session_id member holding id inserted
// right after written_at, and every other member, unknown ones included,
// written back byte for byte in its stored order. It never re-marshals an
// Entry, so a member a later niwa added survives. It refuses a document
// Parse refuses (a duplicate member included), one of a later version, one
// that already has a session_id member, and an id that isn't a session id.
func SetSessionID(raw []byte, id string) ([]byte, error) {
	if !authority.ValidSessionID(id) {
		return nil, errors.New("not a session id")
	}
	parsed, err := Parse(raw)
	if err != nil {
		return nil, err
	}
	if parsed.LaterVersion {
		return nil, ErrLaterVersion
	}
	members, err := orderedMembers(raw)
	if err != nil {
		return nil, err
	}
	for _, m := range members {
		if m.key == "session_id" {
			return nil, ErrSessionIDRecorded
		}
	}
	idJSON, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, m := range members {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(m.rawKey)
		buf.WriteByte(':')
		buf.Write(m.value)
		if m.key == "written_at" {
			buf.WriteString(`,"session_id":`)
			buf.Write(idJSON)
		}
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}
