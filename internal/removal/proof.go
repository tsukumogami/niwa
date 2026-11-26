package removal

import (
	"fmt"

	"github.com/tsukumogami/niwa/internal/remoteurl"
)

// proofPolicy is what decides which of a repository's remotes are proof
// remotes: remotes whose refs count as evidence that a commit exists
// somewhere else. Everything in it comes from the workspace configuration at
// the root, never from the unit.
type proofPolicy struct {
	// cloneURLs are every clone URL the configuration gives a repository
	// it names; cloneURLOf gives the one for a repository by name, which
	// also covers a discovering source.
	cloneURLs  []string
	cloneURLOf func(string) (string, bool)
	// prefixes are the [session.removal] proof_remotes entries.
	prefixes []string
}

// proofRemote is one URL niwa contacts for a repository: always the clone
// URL as the configuration gives it or a URL rebuilt from a prefix, never a
// remote's string as read.
type proofRemote struct {
	url string
	// viaPrefix is a remote matched through a proof_remotes prefix rather
	// than a clone URL; it changes only how evidence words it.
	viaPrefix bool
}

// proofSet is a repository's proof remotes and why its other remotes aren't.
type proofSet struct {
	remotes []proofRemote
	// notes name each remote that isn't a proof remote and why: a URL that
	// doesn't parse, or one nothing in the configuration vouches for. They
	// keep the unit only when some ref is left unheld.
	notes []string
}

// selectProofs decides which of a repository's remotes are proof remotes.
// cloneURL is the clone URL the configuration gives the repository itself,
// empty for one it doesn't name (a nested, bare, submodule-store or outside
// repository).
//
// A remote is a proof remote when its URL, normalized, equals the
// repository's own clone URL; for a repository the configuration doesn't
// name, when it equals any clone URL the configuration gives; or when it
// sits under a proof_remotes prefix, on the whole authority and whole path
// segments. A workspace clone whose remotes all differ from its clone URL
// (an origin re-pointed elsewhere) never has its clone URL contacted. A URL
// that doesn't parse, the clone URL's included, vouches for nothing.
func selectProofs(remotes []remoteEntry, cloneURL string, pp proofPolicy, allowFile bool) proofSet {
	var ps proofSet
	seen := map[string]bool{}
	add := func(pr proofRemote) {
		if !seen[pr.url] {
			seen[pr.url] = true
			ps.remotes = append(ps.remotes, pr)
		}
	}

	var own *remoteurl.URL
	if cloneURL != "" {
		u, err := remoteurl.Parse(cloneURL, allowFile)
		if err != nil {
			ps.notes = append(ps.notes, fmt.Sprintf("remote URL not allowed: %v (the clone URL)", err))
		} else {
			own = &u
		}
	}
	var prefixes []remoteurl.URL
	for _, raw := range pp.prefixes {
		if p, err := remoteurl.ParsePrefix(raw); err == nil {
			prefixes = append(prefixes, p)
		}
	}

	for _, r := range remotes {
		// The name is the agent's: it goes into evidence with control
		// characters turned to spaces.
		r.Name = StripControls(r.Name)
		u, err := remoteurl.Parse(r.URL, allowFile)
		if err != nil {
			ps.notes = append(ps.notes, fmt.Sprintf("remote URL not allowed: %v (remote %s)", err, r.Name))
			continue
		}
		if cloneURL != "" {
			if own != nil && own.Key() == u.Key() {
				add(proofRemote{url: cloneURL})
				continue
			}
		} else if c, ok := matchConfigured(u, pp, allowFile); ok {
			add(proofRemote{url: c})
			continue
		}
		if p, ok := matchPrefix(u, prefixes); ok {
			add(proofRemote{url: u.Rebuild(p), viaPrefix: true})
			continue
		}
		ps.notes = append(ps.notes, fmt.Sprintf("remote not recognized: %s ([session.removal] proof_remotes would widen it)", r.Name))
	}
	return ps
}

// matchConfigured returns the configured clone URL a remote of a repository
// the configuration doesn't name equals: any clone URL the configuration
// gives, or the one it gives the repository named by the remote's last path
// segment, which is how a discovering source's URL is found.
func matchConfigured(u remoteurl.URL, pp proofPolicy, allowFile bool) (string, bool) {
	candidates := append([]string(nil), pp.cloneURLs...)
	if pp.cloneURLOf != nil {
		if c, ok := pp.cloneURLOf(u.LastSegment()); ok && c != "" {
			candidates = append(candidates, c)
		}
	}
	for _, c := range candidates {
		cu, err := remoteurl.Parse(c, allowFile)
		if err == nil && cu.Key() == u.Key() {
			return c, true
		}
	}
	return "", false
}

// matchPrefix returns the first prefix u sits under.
func matchPrefix(u remoteurl.URL, prefixes []remoteurl.URL) (remoteurl.URL, bool) {
	for _, p := range prefixes {
		if u.UnderPrefix(p) {
			return p, true
		}
	}
	return remoteurl.URL{}, false
}
