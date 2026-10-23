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

// lowercaseUUID is the key shape the session mapping store accepts, the one
// workspace.ValidSessionID checks before the caller looks a mapping up. A
// session variable of any other shape never names a mapping, so the root
// fallback skips it. It is repeated here, rather than imported, to keep this
// package free of the store; change the two together.
var lowercaseUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Resolve applies the parent-session, parent-dispatch and principal source
// rules, each in row order, first match wins. No outcome is a refusal: a
// handed, mapped, unverified or absent link is recorded as such, and so is a
// principal niwa couldn't vouch for.
func Resolve(in Inputs) (parentSession, parentDispatch Link, p Principal) {
	parentSession, parentDispatch = resolveParentSession(in), resolveParentDispatch(in)
	return parentSession, parentDispatch, resolvePrincipal(in, parentSession, parentDispatch)
}

// resolvePrincipal applies the principal rows. A parent dispatch whose entry
// is readable passes its principal down, an empty one included; its trust
// stays observed only when niwa observed every hop that carried it: the
// parent dispatch link, the parent session link not contradicted by the
// instance's mapping, and the parent's own principal. Otherwise the declared
// principal, or none. When a parent link has an id but its entry couldn't
// be used, the person should have come from that parent, so the trust is
// handed.
func resolvePrincipal(in Inputs, parentSession, parentDispatch Link) Principal {
	hasParent := parentDispatch.ID != ""
	if hasParent && in.ParentEntry != nil && in.ParentEntry.DispatchID == parentDispatch.ID {
		trust := TrustHanded
		if parentDispatch.Source == SourceObserved && parentSession.Source != SourceUnverified &&
			in.ParentEntry.Principal.Trust == TrustObserved {
			trust = TrustObserved
		}
		return Principal{Name: in.ParentEntry.Principal.Name, Source: PrincipalInherited, Trust: trust}
	}
	trust := TrustObserved
	if hasParent {
		trust = TrustHanded
	}
	if in.DeclaredPrincipal != "" {
		return Principal{Name: in.DeclaredPrincipal, Source: PrincipalDeclared, Trust: trust}
	}
	return Principal{Source: SourceNone, Trust: trust}
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
