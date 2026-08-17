package workspace

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// codexOwnerProbeBytes bounds the ownership read of a composed file. The
// ownership test looks at the first line only, so the whole document is never
// needed; a bound also keeps a repository from making niwa read an arbitrarily
// large file at one of its own names.
const codexOwnerProbeBytes = 4096

// codexOwnership is the verdict of the ownership test for one path niwa writes
// into a repository working tree.
type codexOwnership int

const (
	// codexPathAbsent: nothing is there, so niwa may write.
	codexPathAbsent codexOwnership = iota
	// codexPathNiwas: what is there is niwa's own prior materialization, so
	// niwa may refresh it in place.
	codexPathNiwas
	// codexPathForeign: something niwa did not materialize occupies the name.
	// niwa writes nothing there, modifies nothing, and deletes nothing.
	codexPathForeign
)

// CodexConflict records one path niwa refused to write because something niwa
// did not materialize occupies it.
//
// It is both the warning the apply reports and the exemption the managed-file
// cleanup consults: a conflicted path is not deleted by record, even though the
// apply that found the conflict produced nothing at it.
type CodexConflict struct {
	// Repo is the repository the path belongs to, so the report names it.
	Repo string
	// Path is the occupied path, exactly as the writers would have written it.
	Path string
	// WholeRepo reports that this conflict degrades the whole repository rather
	// than one file: it is the `.codex` case, which suppresses the composed
	// override as well as the payload delivery.
	WholeRepo bool
}

func (c CodexConflict) String() string {
	if c.WholeRepo {
		return fmt.Sprintf("repo %q: %s is occupied by something niwa did not write, so this repository gets no Codex payload delivery and no composed %s; nothing at that path was modified or removed, and Codex falls back to discovering the repository's own content",
			c.Repo, c.Path, CodexOverrideFileName)
	}
	return fmt.Sprintf("repo %q: %s is occupied by something niwa did not write, so no composed override was written there; nothing at that path was modified or removed, and the payload delivery and git-exclude coverage still apply",
		c.Repo, c.Path)
}

// CodexRepoVerdict is one repository's conflict verdict for one apply: which of
// the two names niwa writes into a working tree are occupied by content niwa
// did not materialize.
//
// The verdict is the single ownership authority for the repository. The writers
// consult it before writing and the managed-file cleanup consults it before
// deleting, so a path cannot be foreign to one and niwa's to the other.
type CodexRepoVerdict struct {
	// Repo is the repository name, for reporting.
	Repo string
	// RepoDir is the repository's working tree.
	RepoDir string
	// Payload is the `.codex` conflict, or nil when that name is free.
	Payload *CodexConflict
	// Override is the `AGENTS.override.md` conflict, or nil when that name is
	// free.
	Override *CodexConflict
}

// SuppressesPayload reports whether the payload delivery must be skipped.
func (v CodexRepoVerdict) SuppressesPayload() bool { return v.Payload != nil }

// SuppressesOverride reports whether the composed override must be skipped.
//
// This is where the coupling lives, and it runs one way. A `.codex` conflict
// suppresses the override too: the override's byte budget is declared in the
// payload `config.toml` that only the refused delivery would have put in reach,
// so an override written anyway would run the composed chain under Codex's
// 32768-byte default and silently truncate the innermost layer -- a silent
// failure arriving through the rule written to prevent silent failures. An
// override conflict alone suppresses only the override: the payload delivery,
// the git-exclude patterns, and the trust entry still materialize, and the
// repository's own committed override carries the context slot.
func (v CodexRepoVerdict) SuppressesOverride() bool { return v.Payload != nil || v.Override != nil }

// Conflicts returns the repository's conflicts, payload first.
func (v CodexRepoVerdict) Conflicts() []CodexConflict {
	var out []CodexConflict
	if v.Payload != nil {
		out = append(out, *v.Payload)
	}
	if v.Override != nil {
		out = append(out, *v.Override)
	}
	return out
}

// CodexConflictSet is one apply's conflict verdicts, collected across every
// repository the run touched.
//
// The apply builds it in a single detection pass before any Codex write, hands
// it to the writers as their gate, and hands it to the managed-file cleanup as
// the exemption input. Nothing consults the filesystem twice for the same
// answer, so a file that appears mid-apply cannot make the writers and the
// cleanup disagree about who owns a path.
//
// Later pipeline stages read the verdicts through Verdicts and
// PayloadConflicted -- the seam through which the trust step learns which
// repositories must not be vouched for.
type CodexConflictSet struct {
	verdicts []CodexRepoVerdict
	byRepo   map[string]CodexRepoVerdict
	byDir    map[string]bool
	paths    map[string]bool
}

// Record adds one repository's verdict to the set. A verdict with no conflicts
// is recorded too: the set is the run's whole picture, not only its bad news.
func (s *CodexConflictSet) Record(v CodexRepoVerdict) {
	if s.byRepo == nil {
		s.byRepo = map[string]CodexRepoVerdict{}
		s.byDir = map[string]bool{}
		s.paths = map[string]bool{}
	}
	s.verdicts = append(s.verdicts, v)
	s.byRepo[v.Repo] = v
	if v.Payload != nil {
		s.byDir[filepath.Clean(v.RepoDir)] = true
	}
	for _, c := range v.Conflicts() {
		s.paths[filepath.Clean(c.Path)] = true
	}
}

// Conflicted reports whether path is one this apply declared foreign.
//
// This is the cleanup's question. Reconciliation deletes any recorded path the
// current apply did not produce, and a conflicted path is exactly a path the
// apply did not produce -- so without this consultation the cleanup would
// delete the repository's own file, which is the opposite of what the conflict
// rule promises. The record entry still goes, because the state must stop
// claiming niwa owns the path; only the deletion is exempted.
//
// The nil receiver answers false, so a caller with no detection pass behaves
// exactly as the cleanup did before conflicts existed.
func (s *CodexConflictSet) Conflicted(path string) bool {
	if s == nil {
		return false
	}
	return s.paths[filepath.Clean(path)]
}

// SuppressesPayload reports whether the named repository's payload delivery is
// suppressed by a conflict.
func (s *CodexConflictSet) SuppressesPayload(repo string) bool {
	if s == nil {
		return false
	}
	return s.byRepo[repo].SuppressesPayload()
}

// SuppressesOverride reports whether the named repository's composed override
// is suppressed, by its own conflict or by the coupled `.codex` one.
func (s *CodexConflictSet) SuppressesOverride(repo string) bool {
	if s == nil {
		return false
	}
	return s.byRepo[repo].SuppressesOverride()
}

// PayloadConflicted reports whether the repository whose working tree is
// repoDir has a `.codex` conflict. It keys on the directory rather than the
// name because the stages downstream of materialization -- the trust step
// above all -- carry repository roots, not names.
func (s *CodexConflictSet) PayloadConflicted(repoDir string) bool {
	if s == nil {
		return false
	}
	return s.byDir[filepath.Clean(repoDir)]
}

// Verdicts returns every repository's verdict in detection order.
func (s *CodexConflictSet) Verdicts() []CodexRepoVerdict {
	if s == nil {
		return nil
	}
	return s.verdicts
}

// Warnings renders one report line per conflict, in detection order. A quiet
// skip is the silent minority-case failure the design rules out, so every
// refusal names its repository and its path in the apply output.
func (s *CodexConflictSet) Warnings() []string {
	if s == nil {
		return nil
	}
	var out []string
	for _, v := range s.verdicts {
		for _, c := range v.Conflicts() {
			out = append(out, c.String())
		}
	}
	return out
}

// DetectCodexConflicts inspects the two names niwa writes into one repository
// working tree -- `.codex` and `AGENTS.override.md` -- and returns the
// repository's verdict for this apply.
//
// Ownership is recognized two ways, and the distinction is what the whole rule
// rests on. The `.codex` delivery is recognized by its shape and target: a
// symlink is niwa's (a link whose target moved is niwa's own, stale, and the
// writer retargets it), and a real directory is niwa's when the payload config
// inside it carries the generation marker. A composed file is niwa's exactly
// when it is untracked and carries the generation marker on its first line.
// Anything the repository tracks is a conflict whatever its content, and an
// untracked file without the marker is foreign.
//
// The composed-file test is a content test, not a managed-file-record test, and
// that is deliberate: the standalone `niwa worktree apply` path persists no
// managed-file records, so a record-based check would leave that path unable to
// recognize the override it wrote on its own previous run, and it would refuse
// its own refresh.
//
// See DESIGN-dual-agent-workspace.md Decision 7.
func DetectCodexConflicts(repo, repoDir string) (CodexRepoVerdict, error) {
	verdict := CodexRepoVerdict{Repo: repo, RepoDir: repoDir}

	linkPath := filepath.Join(repoDir, CodexPayloadDirName)
	payloadOwner, err := codexPayloadPathOwnership(linkPath)
	if err != nil {
		return verdict, err
	}
	if payloadOwner == codexPathForeign {
		verdict.Payload = &CodexConflict{Repo: repo, Path: linkPath, WholeRepo: true}
	}

	overridePath := filepath.Join(repoDir, CodexOverrideFileName)
	overrideOwner, err := codexComposedPathOwnership(overridePath)
	if err != nil {
		return verdict, err
	}
	if overrideOwner == codexPathForeign {
		verdict.Override = &CodexConflict{Repo: repo, Path: overridePath}
	}

	return verdict, nil
}

// codexPayloadPathOwnership classifies what occupies the `.codex` name. The
// delivery's target decides nothing here beyond shape: whether a symlink points
// at this instance's payload or a stale one is the writer's repair question,
// not an ownership question, since only niwa plants a symlink at this name.
func codexPayloadPathOwnership(linkPath string) (codexOwnership, error) {
	info, err := os.Lstat(linkPath)
	switch {
	case os.IsNotExist(err):
		return codexPathAbsent, nil
	case err != nil:
		return codexPathForeign, fmt.Errorf("inspecting %s: %w", linkPath, err)
	}

	// Tracked content is a conflict whatever its shape. The git-exclude
	// patterns cannot help here -- they act only on untracked paths -- so this
	// test is the only thing standing between a committed `.codex` and a niwa
	// write over it.
	if codexPathTracked(filepath.Dir(linkPath), filepath.Base(linkPath)) {
		return codexPathForeign, nil
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		// A symlink at this name is a shape only niwa writes here, and an
		// untracked one is not committed content: a stale target is repaired
		// rather than refused, which is what heals a moved instance.
		return codexPathNiwas, nil
	case info.IsDir():
		// The copy fallback's shape. It is niwa's only when the payload config
		// inside carries the generation marker; a repository's own `.codex/`
		// directory -- a real Codex convention -- does not.
		if codexPayloadCopyIsNiwas(linkPath) {
			return codexPathNiwas, nil
		}
		return codexPathForeign, nil
	default:
		// A regular file (or anything else) is not a shape niwa ever writes.
		return codexPathForeign, nil
	}
}

// codexComposedPathOwnership classifies what occupies a composed file's name
// (`AGENTS.override.md`). It is niwa's exactly when the path is an untracked
// regular file whose first line is the generation marker.
func codexComposedPathOwnership(path string) (codexOwnership, error) {
	info, err := os.Lstat(path)
	switch {
	case os.IsNotExist(err):
		return codexPathAbsent, nil
	case err != nil:
		return codexPathForeign, fmt.Errorf("inspecting %s: %w", path, err)
	}

	if codexPathTracked(filepath.Dir(path), filepath.Base(path)) {
		return codexPathForeign, nil
	}
	if !info.Mode().IsRegular() {
		// A symlink at niwa's own name is foreign, and refusing it is what
		// keeps the write from landing on whatever it points at: the writer
		// truncates its target through the link.
		return codexPathForeign, nil
	}

	data, err := readCodexOwnerProbe(path)
	if err != nil {
		// Unreadable, or swapped for something that is not a regular file
		// between the Lstat and the open. Either way niwa cannot claim it.
		return codexPathForeign, nil
	}
	if HasCodexGenerationMarker(data) {
		return codexPathNiwas, nil
	}
	return codexPathForeign, nil
}

// readCodexOwnerProbe reads the leading bytes of path for the ownership test.
//
// It is a separate read from the composer's inline read, and its rules differ
// on purpose. The inline read refuses outright where a symlink cannot be
// refused at the open, because it feeds repository-controlled bytes into every
// session's instruction context. This read only decides whether niwa recognizes
// its own file, and refusing everywhere O_NOFOLLOW is unavailable would leave
// niwa unable to refresh its own override on those platforms. So the flag is
// used where the platform has it, and the type check on the open descriptor
// carries the rest.
func readCodexOwnerProbe(path string) ([]byte, error) {
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

	buf := make([]byte, codexOwnerProbeBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return buf[:n], nil
}

// codexPathTracked reports whether git tracks anything at name inside dir. It
// is a package variable so tests can exercise the tracked branch without a git
// repository.
var codexPathTracked = gitTracksPath

// gitTracksPath reports whether the working tree at dir has anything tracked at
// name -- the file itself, or, when name is a directory, any file inside it.
//
// A tree that is not a git repository, or a git that cannot be run at all,
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
