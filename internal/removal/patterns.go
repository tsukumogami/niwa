package removal

import (
	"path"
	"strings"
)

// ShippedDisposable is the list of disposable patterns niwa ships: editor
// swap and backup files, macOS folder metadata and Python's caches. A path
// one of them matches holds nothing anyone would miss.
var ShippedDisposable = []string{
	"*.swp",
	"*.swo",
	"*~",
	".#*",
	".DS_Store",
	"__pycache__/",
	".pytest_cache/",
}

// matchAny reports whether any of patterns matches rel.
func matchAny(patterns []string, rel string, isDir bool) bool {
	for _, p := range patterns {
		if matchPattern(p, rel, isDir) {
			return true
		}
	}
	return false
}

// matchPattern reports whether pattern matches rel, a slash-separated path
// relative to the repository or instance root the pattern applies to, and
// isDir says whether rel is a directory.
//
// The syntax is path.Match's plus "**", which matches any number of whole
// directories. A pattern with no slash matches a base name anywhere; one
// with a slash matches the path from the root. A trailing slash makes the
// pattern match only a directory, and a matched directory covers everything
// under it, so a pattern also matches a path when it matches one of the
// path's parent directories.
func matchPattern(pattern, rel string, isDir bool) bool {
	dirOnly := strings.HasSuffix(pattern, "/")
	body := strings.TrimSuffix(pattern, "/")
	rel = strings.Trim(rel, "/")
	if body == "" || rel == "" {
		return false
	}
	psegs := strings.Split(body, "/")
	segs := strings.Split(rel, "/")
	anchored := len(psegs) > 1
	for n := 1; n <= len(segs); n++ {
		// segs[:n] is rel itself when n == len(segs), and otherwise one of
		// its parent directories.
		candIsDir := n < len(segs) || isDir
		if dirOnly && !candIsDir {
			continue
		}
		cand := segs[:n]
		if anchored {
			if matchSegments(psegs, cand) {
				return true
			}
			continue
		}
		if ok, err := path.Match(body, cand[n-1]); err == nil && ok {
			return true
		}
	}
	return false
}

// matchSegments matches pattern segments against path segments, "**"
// standing for zero or more whole segments.
func matchSegments(p, s []string) bool {
	if len(p) == 0 {
		return len(s) == 0
	}
	if p[0] == "**" {
		for i := 0; i <= len(s); i++ {
			if matchSegments(p[1:], s[i:]) {
				return true
			}
		}
		return false
	}
	if len(s) == 0 {
		return false
	}
	ok, err := path.Match(p[0], s[0])
	return err == nil && ok && matchSegments(p[1:], s[1:])
}
