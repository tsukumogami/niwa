package lineage

// IsObserved reports whether a parent_session.source, parent_dispatch.source
// or principal.trust value means niwa saw it for itself. Only observed does:
// mapped, handed, unverified and every value this niwa doesn't know are not
// observed. Every renderer decides with this one rule.
func IsObserved(value string) bool {
	return value == SourceObserved
}
