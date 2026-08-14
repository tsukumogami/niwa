package envformat

import (
	"encoding/json"
	"regexp"
	"strings"
)

// RecordPrefix opens every unresolved-key record in the dotenv and shell
// formats. It is a whole-line comment in both, which is what makes a record
// inert: a dotenv reader drops the line before it splits on "=", and a shell
// drops it before it splits into words.
const RecordPrefix = "# niwa: unresolved"

// keyRe is the conservative key-name pattern a record's key must match to be
// written bare. It is the same pattern the .env.example reader enforces
// (internal/workspace/env_example.go), duplicated rather than shared because
// envformat is a leaf package the workspace materializer depends on and not the
// other way round.
//
// Key names are author-supplied: the environment-table decoder stores whatever
// TOML key it read, verbatim, and no validation ever inspects it. A key holding
// a newline, an "=", or a leading "# niwa:" would break a record across two
// physical lines and turn the second into a real assignment, so the writer never
// interpolates a key it has not checked.
var keyRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// tokenRe constrains the level and cause fields. Both come from closed enums in
// this codebase rather than from a configuration file, so this is a guard
// against a future value with a space in it silently shifting the description
// into the cause column, not against an attacker.
var tokenRe = regexp.MustCompile(`^[a-z][a-z-]{0,31}$`)

// Fallback tokens used when a level or cause fails tokenRe. A record with a
// shifted column would be worse than one that admits it does not know.
const (
	levelUnspecified = "unspecified"
	causeUnknown     = "unknown"
)

// ValidKey reports whether key may be written into a record without encoding.
// Callers outside this package use it to revalidate a key recovered from a
// file, which is untrusted input for the same reason the configuration is.
func ValidKey(key string) bool {
	return keyRe.MatchString(key)
}

// Record is the metadata written in place of a key niwa could not supply. It
// holds no value: a key with a record has none by construction.
type Record struct {
	// Level is the key's declared requirement level ("required",
	// "recommended", "optional"), or empty when it appears in no sub-table.
	Level string

	// Cause is why the key has no value, from the resolver's closed set.
	Cause string

	// Description is the author-supplied text from the requirement sub-table.
	// It is free text out of a TOML file: the writer JSON-encodes it and the
	// reader sanitizes it, because it is neither trusted nor guaranteed to be
	// free of line breaks.
	Description string
}

// Item is one element of a generated environment document, in the order the
// caller wants it written. When Record is nil the item is a resolved
// assignment; when Record is non-nil it is an unresolved-key record and Value
// is ignored — an omitted key is never written with an empty value, because a
// downstream consumer cannot tell that apart from a deliberately empty one.
type Item struct {
	Key    string
	Value  string
	Record *Record
}

// recordLine renders one record as a single physical line, without its
// trailing newline. Both the dotenv and shell writers use it: the shape is a
// whole-line comment in each.
//
// Every field that could carry a line break is encoded or replaced, so the
// returned string is guaranteed to contain no "\n", "\r", or any other byte
// that a reader would treat as a line terminator. That guarantee covers the
// WHOLE record, not just the description — the key reaches here from a TOML
// table that constrains nothing.
func recordLine(key string, r Record) string {
	var b strings.Builder
	b.WriteString(RecordPrefix)
	b.WriteByte(' ')
	b.WriteString(encodeKey(key))
	b.WriteByte(' ')
	b.WriteString(token(r.Level, levelUnspecified))
	b.WriteByte(' ')
	b.WriteString(token(r.Cause, causeUnknown))
	b.WriteByte(' ')
	b.WriteString(encodeString(r.Description))
	return b.String()
}

// encodeKey returns key unchanged when it matches the conservative pattern and
// a JSON string literal otherwise. Encoding rather than dropping keeps the
// generated file a complete account of what was omitted; a reader revalidating
// the bare pattern rejects the encoded form, which is the intended outcome for
// a key this odd.
func encodeKey(key string) string {
	if ValidKey(key) {
		return key
	}
	return encodeString(key)
}

// encodeString JSON-encodes s. json.Marshal of a string cannot fail — invalid
// UTF-8 becomes replacement runes — but the error is folded into a safe literal
// rather than ignored, so a future change to that guarantee cannot emit a raw
// value.
func encodeString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// token returns v when it is a plain lowercase word and fallback otherwise.
func token(v, fallback string) string {
	if tokenRe.MatchString(v) {
		return v
	}
	return fallback
}

// ParseRecord recovers a record from one line of a dotenv file. It returns
// ok=false for any line that is not a well-formed record, including a
// well-formed-looking one whose fields fail revalidation.
//
// The input is untrusted: a repository can write its own environment file, and
// the worktree path reads records back out of one. Every field is therefore
// checked again on the way in with the constraints the writer applied on the
// way out, and a record that fails any of them is dropped rather than repaired.
// Dropping is the fail-closed direction here: a dropped record leaves the
// promote path on its hard-error branch instead of its tolerated one.
func ParseRecord(line string) (string, Record, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), RecordPrefix+" ")
	if !ok {
		return "", Record{}, false
	}

	// Exactly four fields, single-space separated, description last: the
	// description is the only one that may contain spaces.
	fields := strings.SplitN(rest, " ", 4)
	if len(fields) != 4 {
		return "", Record{}, false
	}
	key, level, cause, encodedDesc := fields[0], fields[1], fields[2], fields[3]

	if !ValidKey(key) {
		return "", Record{}, false
	}
	if !tokenRe.MatchString(level) || !tokenRe.MatchString(cause) {
		return "", Record{}, false
	}

	var desc string
	if err := json.Unmarshal([]byte(encodedDesc), &desc); err != nil {
		return "", Record{}, false
	}

	if level == levelUnspecified {
		level = ""
	}
	return key, Record{Level: level, Cause: cause, Description: sanitize(desc)}, true
}

// sanitize strips C0 and C1 control characters and the unicode line and
// paragraph separators from a recovered description. The writer's encoding
// makes any byte safe to store, but a recovered description flows on into a
// report that reaches a terminal and, on the hook path, an agent's context
// window, so it is stripped at the boundary rather than trusted downstream.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f, r == 0x2028, r == 0x2029:
			return -1
		default:
			return r
		}
	}, s)
}
