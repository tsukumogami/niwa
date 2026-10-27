package authority

// CurrentGrant is what rec's dispatch holds under the policy as it stands
// now, rather than the list dispatch froze into the record:
//
//   - every role the record names grants what the role declares now, so a
//     widened role adds to it and a narrowed or removed one takes away;
//   - every authority granted with --grant (source dispatch) stays;
//   - each of them is kept only inside the current grant ceiling and, when
//     cap is not nil, inside what the dispatcher holds now.
//
// A nil cap is a dispatch with no dispatcher, which only the ceiling limits.
// Entries are in R3 order with dispatch first and then roles by name, as
// Resolve writes them. A record in a later schema version holds nothing, and
// stored entries this build can't hold grant nothing.
func CurrentGrant(p Policy, rec Record, cap *DispatcherCap) []GrantEntry {
	out := []GrantEntry{}
	if rec.NewerVersion != "" {
		return out
	}
	allowed := map[Authority]bool{}
	for _, a := range EffectivePolicy(p).GrantCeiling {
		allowed[a] = true
	}
	if cap != nil {
		held := map[Authority]bool{}
		for _, a := range cap.Held {
			held[a] = true
		}
		for a := range allowed {
			if !held[a] {
				delete(allowed, a)
			}
		}
	}

	sources := map[Authority][]Source{}
	add := func(a Authority, s Source) {
		for _, have := range sources[a] {
			if have == s {
				return
			}
		}
		sources[a] = append(sources[a], s)
	}
	for _, g := range rec.Grant {
		for _, s := range g.Sources {
			if s == SourceDispatch {
				add(g.Authority, s)
			}
		}
	}
	for _, role := range dedupSorted(rec.Roles) {
		for _, a := range p.Roles[role] {
			add(a, RoleSource(role))
		}
	}

	for _, a := range knownAuthorities {
		if len(sources[a]) > 0 && allowed[a] {
			out = append(out, GrantEntry{Authority: a, Sources: sources[a]})
		}
	}
	return out
}
