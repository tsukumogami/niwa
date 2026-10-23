package authority

import "regexp"

// MaxSessionIDLen is the longest calling-session id niwa accepts.
const MaxSessionIDLen = 128

// sessionIDPattern is the shape niwa accepts for a calling session's id, the
// same one it already accepts for an agent's session ids elsewhere.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

// dispatchIDPattern is the shape a dispatch id has: a lowercase version-4
// UUID, which is what dispatch mints.
var dispatchIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// dispatchIDLen is the length of every valid dispatch id.
const dispatchIDLen = 36

// ValidSessionID reports whether id has the shape of a calling session's id:
// 8 to 128 letters, digits, '_' or '-', so any agent's id fits, not only a
// UUID. It is looser than workspace.ValidSessionID, which accepts only the
// lowercase UUID a Claude Code session id is, because that one guards a
// session id used as a path component. The two check different things and
// aren't interchangeable.
func ValidSessionID(id string) bool {
	return sessionIDPattern.MatchString(id)
}

// ValidDispatchID reports whether id has the shape of a dispatch id.
func ValidDispatchID(id string) bool {
	return dispatchIDPattern.MatchString(id)
}
