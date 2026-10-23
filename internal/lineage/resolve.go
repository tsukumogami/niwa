package lineage

import (
	"regexp"

	"github.com/tsukumogami/niwa/internal/authority"
)

// Inputs is what dispatch reads at step (2e) for the links and the
// principal. Resolve applies the source rules to it and does no I/O, so
// every row of the rules is a table test.
type Inputs struct {
	SessionVar          string // raw value of the launch spec's variable
	ParentFromSet       bool
	CwdInInstance       bool
	NewestMappedSession string // "" when the instance has no mapping, or the instance came from --parent-from
	ParentDispatchID    string
	ParentDispatchFrom  string // authority.ParentSourceCwd | Anchor | None
	RootMappedDispatch  string // cwd in no instance and no --parent-from: the dispatch id of the mapping keyed by SessionVar (a lowercase UUID), or ""
	ParentEntry         *Entry // nil when absent, unreadable or a later version
	DeclaredPrincipal   string // already validated; "" when not declared
}

// lowercaseUUID is the key shape the session mapping store accepts. A
// session variable of any other shape never names a mapping, so the root
// fallback skips it.
var lowercaseUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Resolve applies the parent-session and parent-dispatch source rules, each
// in row order, first match wins. No outcome is a refusal: a handed, mapped,
// unverified or absent link is recorded as such. The principal is left
// absent until it is resolved here too.
func Resolve(in Inputs) (parentSession, parentDispatch Link, p Principal) {
	return resolveParentSession(in), resolveParentDispatch(in), Principal{Source: SourceNone, Trust: TrustObserved}
}

func resolveParentSession(in Inputs) Link {
	id := in.SessionVar
	switch {
	case !authority.ValidSessionID(id):
		return Link{Source: SourceNone}
	case in.ParentFromSet, !in.CwdInInstance, in.NewestMappedSession == "":
		return Link{ID: id, Source: SourceHanded}
	case in.NewestMappedSession == id:
		return Link{ID: id, Source: SourceObserved}
	default:
		return Link{ID: id, Source: SourceUnverified, MappedSessionID: in.NewestMappedSession}
	}
}

func resolveParentDispatch(in Inputs) Link {
	switch {
	case in.ParentDispatchID != "" && in.ParentDispatchFrom == authority.ParentSourceCwd:
		return Link{ID: in.ParentDispatchID, Source: SourceObserved}
	case in.ParentDispatchID != "" && in.ParentDispatchFrom == authority.ParentSourceAnchor:
		return Link{ID: in.ParentDispatchID, Source: SourceHanded}
	case !in.CwdInInstance && !in.ParentFromSet && lowercaseUUID.MatchString(in.SessionVar) &&
		authority.ValidDispatchID(in.RootMappedDispatch):
		return Link{ID: in.RootMappedDispatch, Source: SourceMapped}
	default:
		return Link{Source: SourceNone}
	}
}
