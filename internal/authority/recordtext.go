package authority

import (
	"encoding/json"
	"fmt"
	"strings"
)

// noRecordDoc fixes the key order of the no-record document: the schema and
// the "record": false that marks it, the empty grant, and the level a dispatch
// with no authority flags would resolve to with its plain source.
type noRecordDoc struct {
	Schema      string     `json:"schema"`
	Record      bool       `json:"record"`
	Grant       []grantDoc `json:"grant"`
	Level       string     `json:"level"`
	LevelSource string     `json:"level_source"`
}

// NoRecordJSON is the document `niwa authority show --json` prints when no
// record exists for the selected dispatch. It carries the record schema so a
// reader checks one schema string either way, and "record": false is what
// marks it; level_source is a plain layer name, since only the text output and
// decide's source= line add the no-record suffix.
func NoRecordJSON(level Level, levelSource Layer) ([]byte, error) {
	return json.MarshalIndent(noRecordDoc{
		Schema:      SchemaV1,
		Record:      false,
		Grant:       []grantDoc{},
		Level:       level.String(),
		LevelSource: string(levelSource),
	}, "", "  ")
}

// NoRecordText is what plain `niwa authority show` prints with no record: the
// "dispatch record: none" line, the no-flag level with its source marked, and
// an empty grant.
func NoRecordText(level Level, levelSource Layer) string {
	return fmt.Sprintf("dispatch record: none\nlevel: %s from %s%s\ngrant: none\n", level, levelSource, NoRecordSuffix)
}

// sourceText writes a grant source the way dispatch's resolution line does:
// "dispatch", and "role <name>" for the stored "role:<name>". Any other kind
// keeps its name with the separator spelled the same way.
func sourceText(s string) string {
	return strings.Replace(s, ":", " ", 1)
}

func grantText(authority string, sources []string) string {
	parts := make([]string, len(sources))
	for i, s := range sources {
		parts[i] = sourceText(s)
	}
	return fmt.Sprintf("%s (%s)", authority, strings.Join(parts, ", "))
}

// Text is what plain `niwa authority show` prints for a record that exists.
// Every value in it came through ParseRecord, which refuses control characters
// and names outside their patterns, so nothing here can paint the terminal.
//
// A grant entry this build can't hold is never printed on the grant line: it
// goes on its own "not recognised by this niwa" line, so a reader can't
// mistake it for something held. A record stored in a later schema version
// holds nothing, and says so on its first line.
func (r Record) Text() string {
	var b strings.Builder
	if r.NewerVersion != "" {
		fmt.Fprintf(&b, "dispatch record: newer version %s; grant none\n", r.NewerVersion)
		fmt.Fprintf(&b, "dispatch id: %s\n", r.DispatchID)
		return b.String()
	}
	b.WriteString("dispatch record: present\n")
	fmt.Fprintf(&b, "dispatch id: %s\n", r.DispatchID)
	fmt.Fprintf(&b, "level: %s from %s\n", r.Level, r.LevelSource)
	fmt.Fprintf(&b, "level ceiling: %s from %s\n", r.LevelCeiling, r.LevelCeilingSource)
	// Rendered as `niwa authority policy` renders it, so "none" only ever
	// names the source of a ceiling no layer declares.
	fmt.Fprintf(&b, "grant ceiling: %s from %s\n", listText(r.GrantCeiling), r.GrantCeilingSource)
	roles := "none"
	if len(r.Roles) > 0 {
		roles = strings.Join(r.Roles, ", ")
	}
	fmt.Fprintf(&b, "roles: %s\n", roles)
	grant := "none"
	if len(r.Grant) > 0 {
		parts := make([]string, len(r.Grant))
		for i, g := range r.Grant {
			sources := make([]string, len(g.Sources))
			for j, s := range g.Sources {
				sources[j] = string(s)
			}
			parts[i] = grantText(string(g.Authority), sources)
		}
		grant = strings.Join(parts, ", ")
	}
	fmt.Fprintf(&b, "grant: %s\n", grant)
	if len(r.UnknownGrant) > 0 {
		parts := make([]string, len(r.UnknownGrant))
		for i, g := range r.UnknownGrant {
			parts[i] = grantText(g.Authority, g.Sources)
		}
		fmt.Fprintf(&b, "not recognised by this niwa, so not held: %s\n", strings.Join(parts, ", "))
	}
	parentSession := r.ParentSessionID
	if parentSession == "" {
		parentSession = "none"
	}
	fmt.Fprintf(&b, "parent session id: %s\n", parentSession)
	if r.ParentDispatchID == "" {
		b.WriteString("parent dispatch id: none\n")
	} else {
		fmt.Fprintf(&b, "parent dispatch id: %s (from %s)\n", r.ParentDispatchID, r.ParentDispatchSource)
	}
	return b.String()
}
