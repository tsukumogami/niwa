package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// This file is the workspace half of the probe seam. A producer that composes a
// document into a tree niwa does not own has to know two things about that tree
// first: whether the name it would write is free, and what the tree already
// commits at the slot the document takes. Both answers need a filesystem and one
// of them needs git, so the producer declares the question here and this file
// answers it -- which is what keeps internal/agentplan free of reads it would
// have to be trusted about and this package free of any idea which agent it is
// answering for.
//
// Nothing below names an agent, a filename, or a capability. It is handed a
// spec, it looks, and it hands back what it found.

// ownerProbeBytes bounds the ownership read. The test looks at the first line
// only, so the whole document is never needed; a bound also keeps a repository
// from making niwa read an arbitrarily large file at one of its own names.
const ownerProbeBytes = 4096

// probeContextTree answers one directory's spec. A zero spec is answered with a
// zero probe and no filesystem access at all, which is the whole cost for an
// agent whose documents are not under an ownership rule.
//
// It fails only on an unexpected error inspecting the owned path. Everything
// else degrades in the safe direction: a path niwa cannot read or cannot
// classify is foreign, and a committed file it cannot read as a regular file is
// left out of the composition with a refusal to report.
func probeContextTree(spec agentplan.ContextProbeSpec) (agentplan.ContextProbe, error) {
	probe := agentplan.ContextProbe{Dir: spec.Dir, OwnedPath: spec.OwnedPath}
	if spec.OwnedPath == "" {
		return probe, nil
	}

	owned, err := probeOwnership(spec.OwnedPath, spec.OwnerMarker)
	if err != nil {
		return probe, err
	}
	switch owned {
	case pathForeign:
		probe.Foreign = true
	case pathOurs:
		probe.Owned = true
	}

	if spec.InlinePath == "" {
		return probe, nil
	}

	data, err := readRegularFileNoFollow(spec.InlinePath)
	switch {
	case err == nil:
		probe.Inlined, probe.HasInlined = data, true
	case os.IsNotExist(err):
		// No committed file: the ordinary case, not a refusal.
	default:
		probe.InlineRefusal = fmt.Sprintf("%s was not read into the composed context document (niwa reads it only as a regular file, never through a symlink): %v", spec.InlinePath, err)
	}

	return probe, nil
}

// ownership is the verdict for one path niwa would write into a tree it does
// not own.
type ownership int

const (
	// pathAbsent: nothing is there, so niwa may write.
	pathAbsent ownership = iota
	// pathOurs: what is there is niwa's own prior write, so it may be refreshed
	// in place.
	pathOurs
	// pathForeign: something niwa did not write occupies the name. niwa writes
	// nothing there, modifies nothing, and deletes nothing.
	pathForeign
)

// probeOwnership classifies what occupies path. It is niwa's exactly when the
// path is an untracked regular file whose first line is marker.
//
// Tracked content is foreign whatever it holds, and that test is not redundant
// with the marker one: git-exclude coverage acts only on untracked paths, so
// this is the only thing standing between a committed file at one of niwa's
// names and a write over it. A symlink is foreign too -- writing through one
// would truncate whatever it points at.
func probeOwnership(path, marker string) (ownership, error) {
	info, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		return pathAbsent, nil
	case err != nil:
		return pathForeign, fmt.Errorf("inspecting %s: %w", path, err)
	}

	if pathTrackedByGit(filepath.Dir(path), filepath.Base(path)) {
		return pathForeign, nil
	}
	if !info.Mode().IsRegular() {
		return pathForeign, nil
	}

	data, err := readOwnerProbe(path)
	if err != nil {
		// Unreadable, or swapped for something that is not a regular file
		// between the Lstat and the open. Either way niwa cannot claim it.
		return pathForeign, nil
	}
	if firstLine(data) == marker {
		return pathOurs, nil
	}
	return pathForeign, nil
}

// firstLine returns data's first line with any carriage return trimmed.
func firstLine(data []byte) string {
	line, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimRight(line, "\r")
}

// readOwnerProbe reads the leading bytes of path for the ownership test.
//
// Its rules differ from readRegularFileNoFollow's on purpose. That read feeds
// tree-controlled bytes into every session's instruction context, so it refuses
// outright where a symlink cannot be refused at the open. This one only decides
// whether niwa recognizes its own file, and refusing everywhere O_NOFOLLOW is
// unavailable would leave niwa unable to refresh its own document on those
// platforms. So the flag is used where the platform has it, and the type check
// on the open descriptor carries the rest.
func readOwnerProbe(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|nofollowOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stating %s after open: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (mode %s)", path, info.Mode().Type())
	}

	buf := make([]byte, ownerProbeBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return buf[:n], nil
}

// readRegularFileNoFollow reads path only if it is a regular file that is not
// reached through a symlink at its final component, and refuses otherwise.
//
// The refusal is the open, not a check before it. Git reproduces committed
// symlinks verbatim, so a repository can commit its context file as a symlink to
// any absolute path -- the developer's own agent credentials being the obvious
// target -- and without this rule niwa would read the target and write it into
// the instruction context of every session in that repository. A stat-then-open
// would leave a window between the type check and the read in which the path can
// be swapped; O_NOFOLLOW makes the refusal and the read one syscall with nothing
// in between. niwa's claim that it never reads the developer's credentials is
// true only because of this rule.
//
// O_NONBLOCK rides along so a non-regular file cannot stall the apply: opening a
// FIFO for reading otherwise blocks until a writer appears. The type check on
// the already-open descriptor then refuses anything that is not a regular file,
// which is where FIFOs, directories, and devices land.
//
// The guarantee is narrow, deliberately: O_NOFOLLOW refuses a symlink at the
// final path component only. Any future read through a longer tree-controlled
// path needs full no-symlink path resolution (an openat2-style no-symlinks
// mode), not this.
func readRegularFileNoFollow(path string) ([]byte, error) {
	if !nofollowSupported {
		return nil, fmt.Errorf("refusing to read %s: this platform cannot refuse a symlink at the open, and no weaker check is accepted here", path)
	}

	f, err := os.OpenFile(path, os.O_RDONLY|nofollowOpenFlags, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stating %s after open: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (mode %s)", path, info.Mode().Type())
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return data, nil
}

// pathTrackedByGit reports whether git tracks anything at name inside dir. It is
// a package variable so tests can exercise the tracked branch without a git
// repository.
var pathTrackedByGit = gitTracksPath

// gitTracksPath reports whether the working tree at dir has anything tracked at
// name. A tree that is not a git repository, or a git that cannot be run at all,
// answers false: no tracked content can exist without a repository, and the
// marker test that follows is what decides those cases.
func gitTracksPath(dir, name string) bool {
	cmd := exec.Command("git", "-C", dir, "ls-files", "-z", "--", name)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimRight(string(out), "\x00") != ""
}
