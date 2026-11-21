package pluginstore

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// maxCompareCalls bounds the compare calls one reachability check makes.
const maxCompareCalls = 20

// RefTips lists a source's refs and the commit each points at, the answer of
// `git ls-remote` of the source: "HEAD", "refs/heads/<branch>" and
// "refs/tags/<tag>", annotated tags peeled. The caller runs git; this package
// never does.
type RefTips func(ctx context.Context, owner, repo string) (map[string]string, error)

// Behind reports whether commit is identical to or behind the ref tip base,
// by the compare endpoint (APIClient.CompareCommits with base and head).
type Behind func(ctx context.Context, owner, repo, base, commit string) (bool, error)

// NewReachable builds the check Ensure and Repair run before fetching a
// commit taken from a link. The commit passes when it equals a tag or branch
// tip the source lists, or when the compare endpoint reports it identical to
// or behind one of them, trying the default branch and then stable tags,
// newest first, at most 20 calls. A commit only a fork holds is never behind
// a ref of the source, so it is refused.
func NewReachable(tips RefTips, behind Behind) Reachable {
	return func(ctx context.Context, owner, repo, commit string) (bool, error) {
		refs, err := tips(ctx, owner, repo)
		if err != nil {
			return false, err
		}
		commit = strings.ToLower(commit)
		for _, tip := range refs {
			if strings.ToLower(tip) == commit {
				return true, nil
			}
		}
		if behind == nil {
			return false, nil
		}
		var lastErr error
		for i, base := range compareOrder(refs) {
			if i == maxCompareCalls {
				break
			}
			ok, err := behind(ctx, owner, repo, base, commit)
			if err != nil {
				lastErr = err
				continue
			}
			if ok {
				return true, nil
			}
		}
		return false, lastErr
	}
}

// compareOrder is the tips compared against, in order: the default branch,
// then stable tags newest first, each commit once.
func compareOrder(refs map[string]string) []string {
	var order []string
	seen := map[string]bool{}
	add := func(c string) {
		if c != "" && !seen[c] {
			seen[c] = true
			order = append(order, c)
		}
	}
	add(refs["HEAD"])

	type tag struct {
		v      [3]int
		commit string
	}
	var tags []tag
	for name, c := range refs {
		t, ok := strings.CutPrefix(name, "refs/tags/")
		if !ok {
			continue
		}
		if v, ok := parseStable(t); ok {
			tags = append(tags, tag{v, c})
		}
	}
	sort.Slice(tags, func(i, j int) bool {
		a, b := tags[i].v, tags[j].v
		for k := 0; k < 3; k++ {
			if a[k] != b[k] {
				return a[k] > b[k]
			}
		}
		return tags[i].commit < tags[j].commit
	})
	for _, t := range tags {
		add(t.commit)
	}
	return order
}

// parseStable parses a "vX.Y.Z" or "X.Y.Z" tag with no prerelease suffix.
func parseStable(tag string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || strings.ContainsAny(p, "+-") {
			return v, false
		}
		v[i] = n
	}
	return v, true
}
