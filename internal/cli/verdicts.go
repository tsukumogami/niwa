package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/removal"
)

// verdictsSchemaV1 names the document `niwa reap --verdict-only --json`
// prints.
const verdictsSchemaV1 = "niwa.verdicts/v1"

// Limits for the unit-sourced strings the verdict printers carry. A name
// gets the 80 characters the kept line gives it; a path and evidence get
// 400.
const (
	verdictNameMax     = 80
	verdictPathMax     = 400
	verdictEvidenceMax = 400
)

// unitVerdict is one unit a verdict-only pass judged: its name, whether the
// pass reached it, and the rule's whole verdict.
type unitVerdict struct {
	Name string
	// Reached is false for a unit the pass's deadline left unjudged, one cut
	// short mid-evaluation, and one whose lock another command held.
	Reached bool
	Verdict removal.Verdict
}

// verdictLabel is what a unit's verdict prints as.
func (u unitVerdict) verdictLabel() string {
	if !u.Reached {
		return "not reached"
	}
	return u.Verdict.Kind.String()
}

// printVerdictsText prints each unit's verdict: its name and verdict, every
// keeping check in rule order with its paths and evidence, and the class of
// every path, directories whose entries share a class collapsed into one
// line. Every unit-sourced string passes removal.ScrubEvidence first; no file
// contents are read or printed.
func printVerdictsText(w io.Writer, vs []unitVerdict) {
	for _, u := range vs {
		fmt.Fprintf(w, "%s: %s\n", removal.ScrubEvidence(u.Name, verdictNameMax), u.verdictLabel())
		if !u.Reached {
			continue
		}
		for _, c := range u.Verdict.Checks {
			fmt.Fprintf(w, "  %s check: %s\n", c.Name, removal.ScrubEvidence(c.Evidence, verdictEvidenceMax))
			for _, p := range c.Paths {
				fmt.Fprintf(w, "    %s\n", removal.ScrubEvidence(p, verdictPathMax))
			}
		}
		paths := collapsePaths(u.Verdict.Paths)
		if len(paths) == 0 {
			continue
		}
		fmt.Fprintf(w, "  paths:\n")
		for _, pc := range paths {
			notice := ""
			if pc.Notice != "" {
				notice = " (" + string(pc.Notice) + ")"
			}
			fmt.Fprintf(w, "    %s %s%s\n", removal.ScrubEvidence(pc.Path, verdictPathMax), pc.Class, notice)
		}
	}
}

// verdictsDoc is the niwa.verdicts/v1 document.
type verdictsDoc struct {
	Schema string            `json:"schema"`
	Units  []verdictUnitJSON `json:"units"`
}

// verdictUnitJSON is one unit of a niwa.verdicts/v1 document. Over is
// always null in this release.
type verdictUnitJSON struct {
	Instance string             `json:"instance"`
	Verdict  string             `json:"verdict"`
	Reached  bool               `json:"reached"`
	Checks   []verdictCheckJSON `json:"checks"`
	Paths    []verdictPathJSON  `json:"paths"`
	Over     *struct{}          `json:"over"`
}

type verdictCheckJSON struct {
	Check    string   `json:"check"`
	Paths    []string `json:"paths"`
	Evidence string   `json:"evidence"`
}

type verdictPathJSON struct {
	Path   string `json:"path"`
	Class  string `json:"class"`
	Notice string `json:"notice"`
}

// printVerdictsJSON prints the units as a niwa.verdicts/v1 document, with
// the fields, collapsed paths and scrubbing the text form has. A unit the
// pass didn't reach carries verdict "keep", since nothing removes it, and
// reached false.
func printVerdictsJSON(w io.Writer, vs []unitVerdict) error {
	doc := verdictsDoc{Schema: verdictsSchemaV1, Units: []verdictUnitJSON{}}
	for _, u := range vs {
		out := verdictUnitJSON{
			Instance: removal.ScrubEvidence(u.Name, verdictNameMax),
			Verdict:  removal.Keep.String(),
			Reached:  u.Reached,
			Checks:   []verdictCheckJSON{},
			Paths:    []verdictPathJSON{},
		}
		if u.Reached {
			out.Verdict = u.Verdict.Kind.String()
			for _, c := range u.Verdict.Checks {
				jc := verdictCheckJSON{
					Check:    string(c.Name),
					Paths:    []string{},
					Evidence: removal.ScrubEvidence(c.Evidence, verdictEvidenceMax),
				}
				for _, p := range c.Paths {
					jc.Paths = append(jc.Paths, removal.ScrubEvidence(p, verdictPathMax))
				}
				out.Checks = append(out.Checks, jc)
			}
			for _, pc := range collapsePaths(u.Verdict.Paths) {
				out.Paths = append(out.Paths, verdictPathJSON{
					Path:   removal.ScrubEvidence(pc.Path, verdictPathMax),
					Class:  string(pc.Class),
					Notice: string(pc.Notice),
				})
			}
		}
		doc.Units = append(doc.Units, out)
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// pathNode is one directory or file in the tree collapsePaths builds from a
// verdict's flat path list.
type pathNode struct {
	// entry is the rule's own entry at this path, nil for a directory the
	// rule named only things under.
	entry    *removal.PathClass
	children map[string]*pathNode
	// judged caches uniform's answer.
	judged *nodeUniformity
}

// nodeUniformity is whether everything under a node shares one class and
// notice, and whether a repository's work tree is at or under it.
type nodeUniformity struct {
	ok     bool
	class  removal.Class
	notice removal.Notice
	repo   bool
}

// collapsePaths turns a verdict's flat, one-entry-per-path list into the
// lines a person reads. A directory whose entries all share one class and
// notice prints as one "<dir>/" line. A repository's work tree whose files
// are all tracked and whose git directory is pushed prints as one tracked
// line. Nothing collapses above a repository's work tree, and the instance
// root and the job directory ("job:") never collapse. The rule's own
// directory entries print as they are.
func collapsePaths(paths []removal.PathClass) []removal.PathClass {
	roots := map[string]*pathNode{}
	var rootOrder []string
	var out []removal.PathClass
	for i := range paths {
		pc := paths[i]
		root, rel := "", pc.Path
		if strings.HasPrefix(rel, "job:") {
			root, rel = "job:", strings.TrimPrefix(rel, "job:")
		}
		rel = strings.TrimSuffix(rel, "/")
		if rel == "" || rel == "." {
			// An entry on a root itself prints as it is.
			out = append(out, pc)
			continue
		}
		n, ok := roots[root]
		if !ok {
			n = &pathNode{}
			roots[root] = n
			rootOrder = append(rootOrder, root)
		}
		for _, seg := range strings.Split(rel, "/") {
			if n.children == nil {
				n.children = map[string]*pathNode{}
			}
			child, ok := n.children[seg]
			if !ok {
				child = &pathNode{}
				n.children[seg] = child
			}
			n = child
		}
		n.entry = &pc
	}
	for _, root := range rootOrder {
		out = append(out, emitPaths(roots[root], root)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// emitPaths returns the lines for n's children, prefix being n's own path
// with its trailing slash.
func emitPaths(n *pathNode, prefix string) []removal.PathClass {
	var out []removal.PathClass
	for _, name := range sortedChildren(n) {
		child := n.children[name]
		if len(child.children) == 0 && child.entry != nil {
			out = append(out, *child.entry)
			continue
		}
		if u := uniform(child); u.ok {
			out = append(out, removal.PathClass{Path: prefix + name + "/", Class: u.class, Notice: u.notice})
			continue
		}
		if child.entry != nil {
			out = append(out, *child.entry)
		}
		out = append(out, emitPaths(child, prefix+name+"/")...)
	}
	return out
}

// uniform says whether everything under n shares one class and notice. A
// repository's work tree (a node holding a ".git/" entry) counts as tracked
// when everything else in it is tracked and its git directory is pushed, and
// marks every directory above it as one that doesn't collapse.
func uniform(n *pathNode) nodeUniformity {
	if n.judged != nil {
		return *n.judged
	}
	var u nodeUniformity
	switch {
	case len(n.children) == 0 && n.entry != nil:
		u = nodeUniformity{ok: true, class: n.entry.Class, notice: n.entry.Notice}
	default:
		git := n.children[".git"]
		repoRoot := git != nil && git.entry != nil && len(git.children) == 0 && strings.HasSuffix(git.entry.Path, "/")
		first, same, containsRepo := true, true, false
		for name, child := range n.children {
			if repoRoot && name == ".git" {
				continue
			}
			cu := uniform(child)
			if cu.repo {
				containsRepo = true
			}
			if !cu.ok {
				same = false
				continue
			}
			if first {
				u.class, u.notice, first = cu.class, cu.notice, false
			} else if cu.class != u.class || cu.notice != u.notice {
				same = false
			}
		}
		// A node with an entry of its own and paths under it prints both.
		if n.entry != nil {
			same = false
		}
		if repoRoot {
			u.ok = same && !first && !containsRepo && u.class == removal.ClassTracked && u.notice == "" && git.entry.Class == removal.ClassPushed
			u.repo = true
		} else {
			u.ok = same && !first && !containsRepo
			u.repo = containsRepo
		}
	}
	n.judged = &u
	return u
}

func sortedChildren(n *pathNode) []string {
	names := make([]string, 0, len(n.children))
	for name := range n.children {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
