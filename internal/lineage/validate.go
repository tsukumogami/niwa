package lineage

import (
	"regexp"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/authority"
)

// The field patterns every known member of a read entry is checked against.
// A writer that respects them produces an entry of at most 4 KiB, well under
// the store's 8 KiB read cap.
var (
	// instancePattern is an instance's directory name: a workspace name
	// (config.NamePattern) joined to its suffix by '+', bounded by what a
	// directory name can hold.
	instancePattern = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,255}$`)
	// nameValuePattern is an agent, origin, source or trust value. A value
	// of this shape that this niwa doesn't know is accepted and kept, so a
	// later niwa can add one.
	nameValuePattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	// principalPattern is a principal's name when it has one.
	principalPattern = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,128}$`)
)

// ValidPrincipal reports whether name is a principal name a person may
// declare: 1 to 128 letters, digits, '.', '_', '-', '@' or '+'.
func ValidPrincipal(name string) bool {
	return principalPattern.MatchString(name)
}

// validNameValue reports whether s is a name value.
func validNameValue(s string) bool {
	return nameValuePattern.MatchString(s)
}

// validWrittenAt reports whether s is an RFC 3339 time in UTC.
func validWrittenAt(s string) bool {
	if !strings.HasSuffix(s, "Z") {
		return false
	}
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}

// validateEntry re-checks every known member of a v1 entry against its
// shape. It returns an *InvalidEntryError naming the first member that
// fails, never the value it holds.
func validateEntry(e Entry) error {
	switch {
	case !authority.ValidDispatchID(e.DispatchID):
		return invalid("dispatch_id is not a dispatch id")
	case !instancePattern.MatchString(e.Instance):
		return invalid("instance is not an instance name")
	case !validNameValue(e.Agent):
		return invalid("agent is not a name value")
	case !validNameValue(e.Origin):
		return invalid("origin is not a name value")
	case !validWrittenAt(e.WrittenAt):
		return invalid("written_at is not an RFC 3339 UTC time")
	case e.SessionID != "" && !authority.ValidSessionID(e.SessionID):
		return invalid("session_id is not a session id")
	case e.ParentSession.ID != "" && !authority.ValidSessionID(e.ParentSession.ID):
		return invalid("parent_session.id is not a session id")
	case !validNameValue(e.ParentSession.Source):
		return invalid("parent_session.source is not a name value")
	case e.ParentSession.MappedSessionID != "" && !authority.ValidSessionID(e.ParentSession.MappedSessionID):
		return invalid("parent_session.mapped_session_id is not a session id")
	case e.ParentDispatch.ID != "" && !authority.ValidDispatchID(e.ParentDispatch.ID):
		return invalid("parent_dispatch.id is not a dispatch id")
	case !validNameValue(e.ParentDispatch.Source):
		return invalid("parent_dispatch.source is not a name value")
	case e.Principal.Name != "" && !ValidPrincipal(e.Principal.Name):
		return invalid("principal.name is not a principal name")
	case !validNameValue(e.Principal.Source):
		return invalid("principal.source is not a name value")
	case !validNameValue(e.Principal.Trust):
		return invalid("principal.trust is not a name value")
	}
	return nil
}
