// Package pluginstore holds each github plugin marketplace release once per
// workspace on the host, keyed by the commit its track resolved to:
//
//	~/.niwa/w-<workspace key>/plugins/github.com/<owner>/<repo>/<commit>/
//	  tree/       the extracted marketplace, read-only
//	  held.json   the completion mark and seal, read-only
//
// An instance holds only a symlink, <instance>/.niwa/marketplaces/<name>,
// whose absolute target is the release's tree/. That link is the instance's
// only pointer into the store.
//
// The package never runs git, never imports internal/workspace and never
// follows a link it read from an instance; it only parses link targets. The
// only write it makes outside the store is the one link Ensure places for the
// instance the command runs for. Callers pass in the fetch function, the
// declared sources and the reachability check.
package pluginstore

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tsukumogami/niwa/internal/hostdir"
)

// Forge is the one forge segment under plugins/. It leaves room for another
// forge without a migration.
const Forge = "github.com"

// Names niwa uses for its own entries under plugins/. Nothing a source can
// name starts with a dot, so no source can take them.
const (
	tmpDirName   = ".tmp"
	trashDirName = ".trash"
	treeDirName  = "tree"
	markFileName = "held.json"
)

var (
	ownerPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}$`)
	repoPattern   = regexp.MustCompile(`^[a-z0-9._-]{1,100}$`)
	commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// refPattern is what a ref or tag must look like to be kept in held.json.
	refPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,99}$`)
	// cleanTagPattern is the one tag shape a message may name.
	cleanTagPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)
)

// Release names one held marketplace release.
type Release struct {
	Owner, Repo string // lowercased GitHub names
	Commit      string // 40 lowercase hex
	Ref, Tag    string // what was asked for, and the tag when there is one
}

// NewRelease builds a release from a source's owner and repo as configured
// (any case) and a resolved commit, and reports whether the three name a
// release the store can hold. Ref and Tag are kept only when they match the
// strict ref pattern.
func NewRelease(owner, repo, commit, ref, tag string) (Release, bool) {
	r := Release{
		Owner:  strings.ToLower(owner),
		Repo:   strings.ToLower(repo),
		Commit: strings.ToLower(commit),
		Ref:    SafeRef(ref),
		Tag:    SafeRef(tag),
	}
	return r, r.Valid()
}

// Valid reports whether the owner, repo and commit may become path
// components.
func (r Release) Valid() bool {
	return ValidOwner(r.Owner) && ValidRepo(r.Repo) && commitPattern.MatchString(r.Commit)
}

// ValidOwner reports whether owner is a lowercased GitHub owner name.
func ValidOwner(owner string) bool { return ownerPattern.MatchString(owner) }

// ValidRepo reports whether repo is a lowercased GitHub repository name, not
// "." or "..".
func ValidRepo(repo string) bool {
	return repoPattern.MatchString(repo) && repo != "." && repo != ".."
}

// SafeRef returns ref when it matches the strict ref pattern with no "..",
// and "" otherwise. A ref the remote chose is never stored or shown unchecked.
func SafeRef(ref string) string {
	if !refPattern.MatchString(ref) || strings.Contains(ref, "..") {
		return ""
	}
	return ref
}

// Source is "<owner>/<repo>", the key of the declared-sources set.
func (r Release) Source() string { return r.Owner + "/" + r.Repo }

// ID is "github.com/<owner>/<repo>/<commit>", the release's path under
// plugins/ and the input of its lock name.
func (r Release) ID() string { return Forge + "/" + r.Owner + "/" + r.Repo + "/" + r.Commit }

// Label names the release in a message: the source and a clean tag when one
// is recorded, the source and the short commit otherwise.
func (r Release) Label() string {
	if cleanTagPattern.MatchString(r.Tag) {
		return r.Source() + " " + r.Tag
	}
	return r.Source() + " " + r.Commit[:12]
}

// hash is the first 32 hex digits of the sha256 of the release id, the
// prefix of its staging and trash entries.
func (r Release) hash() string {
	sum := sha256.Sum256([]byte(r.ID()))
	return hex.EncodeToString(sum[:])[:32]
}

// Dir is the workspace's plugins directory, ~/.niwa/w-<key>/plugins.
func Dir(home, workspaceRoot string) string {
	return filepath.Join(hostdir.WorkspaceDir(home, workspaceRoot), "plugins")
}

// LockPath is the release's lock in niwa's lock directory:
// ~/.niwa/locks/p-<first 32 hex of sha256(workspace key + "/" + release id)>.lock.
// The scheme is the mirror's, with its own prefix.
func LockPath(home, workspaceRoot string, r Release) string {
	sum := sha256.Sum256([]byte(hostdir.WorkspaceKey(workspaceRoot) + "/" + r.ID()))
	return filepath.Join(home, ".niwa", "locks", "p-"+hex.EncodeToString(sum[:])[:32]+".lock")
}

// releaseDir is the held release's directory.
func releaseDir(pluginsDir string, r Release) string {
	return filepath.Join(pluginsDir, Forge, r.Owner, r.Repo, r.Commit)
}

// TreePath is the absolute path an instance's link names: the release's
// tree/ in this store.
func TreePath(home, workspaceRoot string, r Release) string {
	return filepath.Join(releaseDir(Dir(home, workspaceRoot), r), treeDirName)
}

// stagingPattern matches a staging or trash entry of r and of no other
// release: "<release hash>-<8 hex>".
func stagingPattern(r Release) *regexp.Regexp {
	return regexp.MustCompile(`^` + r.hash() + `-[0-9a-f]{8}$`)
}

// randHex returns 8 random hex digits for staging, trash and link names.
func randHex() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
