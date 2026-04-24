package config

import "github.com/tsukumogami/niwa/internal/source"

// PopulateMirror parses e.SourceURL into the parsed-mirror fields
// (SourceHost, SourceOwner, SourceRepo, SourceSubpath, SourceRef) and
// returns whether any field changed. PRD R23 commits to lazy upgrade:
// callers invoke this on read; the new fields persist on next save.
//
// When SourceURL is empty (legacy entries that were never registered
// with --from), the mirror fields are left empty and the function
// returns false. When SourceURL is malformed, the mirror fields are
// also left empty and the function returns false (the caller's next
// save propagates the malformed URL unchanged so the user can repair
// it manually).
//
// When the mirror fields are already set and disagree with SourceURL,
// PopulateMirror treats SourceURL as canonical (PRD R22) and
// overwrites the mirror to match. The caller may emit a stderr
// warning naming the inconsistency before saving.
func (e *RegistryEntry) PopulateMirror() bool {
	if e == nil || e.SourceURL == "" {
		return false
	}
	parsed, err := source.Parse(e.SourceURL)
	if err != nil {
		// Malformed slug: leave mirror empty so callers can detect
		// (and the user can repair) without us silently rewriting it.
		return false
	}
	canonical := mirrorFrom(parsed)
	current := mirrorFrom(source.Source{
		Host:    e.SourceHost,
		Owner:   e.SourceOwner,
		Repo:    e.SourceRepo,
		Subpath: e.SourceSubpath,
		Ref:     e.SourceRef,
	})
	if canonical == current {
		return false
	}
	e.SourceHost = parsed.Host
	e.SourceOwner = parsed.Owner
	e.SourceRepo = parsed.Repo
	e.SourceSubpath = parsed.Subpath
	e.SourceRef = parsed.Ref
	return true
}

func mirrorFrom(s source.Source) source.Source {
	// Identical to s; the function exists so we can extend later
	// without affecting callers.
	return s
}
