package removal

import (
	"sort"
	"strings"
)

// Class is what the rule counts a path as. Every path in a unit gets exactly
// one, and only a unique path keeps the unit.
type Class string

const (
	// ClassTracked is a file a repository tracks, unmodified.
	ClassTracked Class = "tracked"
	// ClassPushed is a repository's git directory whose every ref the clone
	// URL holds.
	ClassPushed Class = "pushed"
	// ClassBuildTree is a build tree: a git-ignored directory carrying a
	// CACHEDIR.TAG, or one the workspace declares in build_trees.
	ClassBuildTree Class = "build tree"
	// ClassGenerated is a file niwa or Claude Code writes again on
	// provisioning, or an empty directory.
	ClassGenerated Class = "generated"
	// ClassDisposable is a path a disposable pattern matches, a git-ignored
	// path, or job scratch in a unit with no handoff brief.
	ClassDisposable Class = "disposable"
	// ClassUnique is anything else. It keeps the unit.
	ClassUnique Class = "unique"
)

// Notice marks a disposable path that is disposable for a reason a person
// may want to see apart from a pattern.
type Notice string

const (
	// NoticeGitIgnored marks a path disposable because it is git-ignored.
	NoticeGitIgnored Notice = "git-ignored"
	// NoticeJobScratch marks job scratch disposable because the unit has no
	// handoff brief.
	NoticeJobScratch Notice = "job scratch"
)

// jobPrefix starts the path of anything in the unit's job directory, so it
// can't be mistaken for a path in the instance.
const jobPrefix = "job:"

// PathClass is one path and its class. Path is relative to the instance, or
// starts with "job:" and is relative to the unit's job directory; a
// directory ends in "/" and its class covers everything under it.
type PathClass struct {
	Path   string
	Class  Class
	Notice Notice
}

// classes collects the classes an evaluation gives.
type classes struct {
	paths []PathClass
}

func (c *classes) add(p string, class Class, notice Notice) {
	c.paths = append(c.paths, PathClass{Path: p, Class: class, Notice: notice})
}

// sorted returns the paths in path order, each once. A path given two
// classes keeps the stricter one, unique above everything else, so an
// evaluation that judged one path twice never reports it as less than it
// found.
func (c *classes) sorted() []PathClass {
	byPath := map[string]PathClass{}
	for _, pc := range c.paths {
		prev, ok := byPath[pc.Path]
		if !ok || classRank(pc.Class) > classRank(prev.Class) {
			byPath[pc.Path] = pc
		}
	}
	out := make([]PathClass, 0, len(byPath))
	for _, pc := range byPath {
		out = append(out, pc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func classRank(c Class) int {
	switch c {
	case ClassUnique:
		return 2
	case "":
		return 0
	default:
		return 1
	}
}

// dirPath gives a directory's path its trailing slash.
func dirPath(rel string) string {
	if strings.HasSuffix(rel, "/") {
		return rel
	}
	return rel + "/"
}
