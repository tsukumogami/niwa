package pluginstore

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/tsukumogami/niwa/internal/buildinfo"
	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/hostdir"
	"github.com/tsukumogami/niwa/internal/testfault"
)

// DefaultLockWait bounds how long Ensure waits for another command placing the
// same release. A download and extraction finishes well inside it, so a
// holder past it is stuck rather than slow.
const DefaultLockWait = 2 * time.Minute

// markSchema is the held.json schema version.
const markSchema = 1

// Condition names why the store can't be used, for the fallback notice. ""
// means the store worked.
type Condition string

// The conditions a caller falls back on, as printed.
const (
	CondNone        Condition = ""
	CondUnreadable  Condition = "unreadable"
	CondUnwritable  Condition = "unwritable"
	CondUncreatable Condition = "can't be created"
	CondNotPlaced   Condition = "release not placed"
)

// ErrRefused is returned by Ensure for a release taken from a link that needs
// fetching when its source isn't declared or its commit is not shown to
// belong to the source's own history. Nothing was fetched and the link was
// left as it is.
var ErrRefused = errors.New("release refused")

// Reachable reports whether commit is a tag or branch tip of owner/repo, or
// an ancestor of one by the compare endpoint. A commit taken from a link is
// never fetched unless it passes.
type Reachable func(ctx context.Context, owner, repo, commit string) (bool, error)

// Store is one workspace's plugin store.
type Store struct {
	Home, WorkspaceRoot string
	// LockWait bounds the wait for a release's lock; zero means
	// DefaultLockWait.
	LockWait time.Duration
	// Fetch streams the tarball at the commit. The caller wraps its GitHub
	// client; the store never sees a token.
	Fetch func(ctx context.Context, owner, repo, commit string) (io.ReadCloser, error)
}

// Seal is what a release's tree measured once it was made read-only.
type Seal struct {
	Entries    int64 `json:"entries"`
	Bytes      int64 `json:"bytes"`
	MaxCtimeNS int64 `json:"max_ctime_ns"`
}

// Mark is held.json, a release's completion mark and seal.
type Mark struct {
	Schema     int    `json:"schema"`
	Source     string `json:"source"`
	Ref        string `json:"ref"`
	Tag        string `json:"tag"`
	Commit     string `json:"commit"`
	CommitTime string `json:"commit_time"`
	PlacedAt   string `json:"placed_at"`
	Niwa       string `json:"niwa"`
	Seal       Seal   `json:"seal"`
}

// pluginsDir is this store's plugins directory.
func (s Store) pluginsDir() string { return Dir(s.Home, s.WorkspaceRoot) }

// lockWait is LockWait or its default.
func (s Store) lockWait() time.Duration {
	if s.LockWait > 0 {
		return s.LockWait
	}
	return DefaultLockWait
}

// ReadMark reads r's held.json. It never reads the tree.
func (s Store) ReadMark(r Release) (Mark, error) {
	var m Mark
	data, err := os.ReadFile(filepath.Join(releaseDir(s.pluginsDir(), r), markFileName))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("parsing %s of %s: %w", markFileName, r.ID(), err)
	}
	return m, nil
}

// Held reports whether r is complete and marked: held.json parses and names
// this source and commit, and tree/.claude-plugin/marketplace.json exists.
// It makes a few stats and one short read and never reads the tree. It does
// not read the seal either, so a tree changed after it was placed still
// counts as held; Verify is the check that walks the tree against the seal.
// A mark that can't be read for a reason other than its absence is the
// unreadable condition.
func (s Store) Held(r Release) (bool, Condition) {
	m, err := s.ReadMark(r)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, CondNone
		}
		var perr *fs.PathError
		if errors.As(err, &perr) {
			return false, CondUnreadable
		}
		return false, CondNone
	}
	if m.Schema != markSchema || m.Source != r.Source() || m.Commit != r.Commit {
		return false, CondNone
	}
	manifest := filepath.Join(releaseDir(s.pluginsDir(), r), treeDirName, ".claude-plugin", "marketplace.json")
	info, err := os.Lstat(manifest)
	if err != nil || !info.Mode().IsRegular() {
		return false, CondNone
	}
	return true, CondNone
}

// Ensure places r if it is missing, or held but failing Verify, and points
// link at its tree, both under r's lock. A held release that fails Verify was
// changed after it was placed: it is moved into .trash/ and placed again at
// its commit exactly as a missing one is, and restored is true. link must sit
// in a real .niwa/marketplaces directory of the instance the command runs
// for; an empty link places without linking.
//
// When r was resolved from a declared ref just now, reachable is nil. When r
// came from a link, the caller passes declared and reachable, and Ensure
// fetches r only if its source is in declared and reachable passes;
// otherwise it fetches nothing, leaves link as it is and returns ErrRefused.
//
// A non-empty Condition means the store can't be used and the caller falls
// back to a per-instance copy. An error with no Condition is a failure that
// isn't the store's (the source couldn't be fetched, say, or the link
// couldn't be written); nothing partly written is marked held either way.
func (s Store) Ensure(ctx context.Context, r Release, link string, declared map[string]bool, reachable Reachable) (restored bool, cond Condition, err error) {
	if !r.Valid() {
		return false, CondNone, fmt.Errorf("release %q is not a name the store can hold", r.ID())
	}
	if cond, err := s.prepare(); cond != CondNone {
		return false, cond, err
	}

	unlock, err := filelock.WaitFileLock(ctx, LockPath(s.Home, s.WorkspaceRoot, r), fmt.Sprintf("niwa pid %d", os.Getpid()), s.lockWait())
	if err != nil {
		return false, CondNotPlaced, fmt.Errorf("locking release %s: %w", r.Label(), err)
	}
	defer unlock()

	held, cond := s.Held(r)
	if cond != CondNone {
		return false, cond, fmt.Errorf("reading the mark of release %s", r.Label())
	}
	intact := held
	if held {
		// A walk that fails, or a mark gone since Held, is not intact either.
		intact, _ = s.Verify(r)
	}
	if !intact {
		if reachable != nil {
			if !declared[r.Source()] {
				return false, CondNone, fmt.Errorf("%w: %s is not a marketplace source this workspace declares", ErrRefused, r.Source())
			}
			ok, rerr := reachable(ctx, r.Owner, r.Repo, r.Commit)
			if rerr != nil || !ok {
				detail := "it is not in the source's own history"
				if rerr != nil {
					detail = "its history could not be checked: " + rerr.Error()
				}
				return false, CondNone, fmt.Errorf("%w: commit %s of %s: %s", ErrRefused, r.Commit[:12], r.Source(), detail)
			}
		}
		if err := probeWritable(s.pluginsDir()); err != nil {
			return false, CondUnwritable, err
		}
		cond, err := s.place(ctx, r)
		if err != nil {
			return false, cond, err
		}
		restored = true
	}

	if link != "" {
		if err := placeLink(link, TreePath(s.Home, s.WorkspaceRoot, r)); err != nil {
			return restored, CondNone, err
		}
	}
	return restored, CondNone, nil
}

// prepare makes the levels down to plugins/ exist, creating a missing one with
// mode 0700 and never changing the mode of an existing one, and reports the
// condition when it can't.
func (s Store) prepare() (Condition, error) {
	niwa := filepath.Join(s.Home, ".niwa")
	if err := os.MkdirAll(niwa, 0o700); err != nil {
		return CondUncreatable, fmt.Errorf("creating %s: %w", niwa, err)
	}
	pdir := s.pluginsDir()
	_, statErr := os.Lstat(pdir)
	for _, dir := range []string{filepath.Dir(pdir), pdir} {
		if err := hostdir.CreatePrivateDir(dir); err != nil {
			if os.IsNotExist(statErr) {
				return CondUncreatable, err
			}
			return CondUnreadable, err
		}
	}
	f, err := os.Open(pdir)
	if err != nil {
		return CondUnreadable, fmt.Errorf("reading %s: %w", pdir, err)
	}
	defer f.Close()
	if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return CondUnreadable, fmt.Errorf("reading %s: %w", pdir, err)
	}
	return CondNone, nil
}

// probeWritable checks that a file can be made in dir itself, whatever the
// modes of the levels below it.
func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".probe-*")
	if err != nil {
		return fmt.Errorf("writing in %s: %w", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// place downloads r at its commit into a staging directory, extracts and
// checks it, makes the tree read-only, seals and marks it, and renames it into
// place. The caller holds r's lock. Any failure before the final rename
// removes only the staging directory, so a release is either absent or
// complete, sealed and marked.
func (s Store) place(ctx context.Context, r Release) (Condition, error) {
	pdir := s.pluginsDir()
	for _, dir := range []string{filepath.Join(pdir, tmpDirName), filepath.Join(pdir, trashDirName)} {
		if err := hostdir.CreatePrivateDir(dir); err != nil {
			return CondNotPlaced, err
		}
	}
	clearStaging(filepath.Join(pdir, tmpDirName), r)
	if err := s.trashRelease(r); err != nil {
		return CondNotPlaced, err
	}

	suffix, err := randHex()
	if err != nil {
		return CondNotPlaced, err
	}
	staging := filepath.Join(pdir, tmpDirName, r.hash()+"-"+suffix)
	if err := os.Mkdir(staging, 0o700); err != nil {
		return CondNotPlaced, fmt.Errorf("creating %s: %w", staging, err)
	}
	placed := false
	defer func() {
		if !placed {
			removeTree(staging)
		}
	}()

	if s.Fetch == nil {
		return CondNone, fmt.Errorf("no fetcher to download %s", r.Label())
	}
	archive := filepath.Join(staging, "archive.tar.gz")
	if err := s.download(ctx, r, archive); err != nil {
		return CondNone, err
	}

	commitTime, err := checkArchive(archive, r)
	if err != nil {
		return CondNotPlaced, err
	}
	tree := filepath.Join(staging, treeDirName)
	if err := extract(archive, tree); err != nil {
		return CondNotPlaced, fmt.Errorf("extracting %s: %w", r.Label(), err)
	}
	if err := os.Remove(archive); err != nil {
		return CondNotPlaced, fmt.Errorf("removing %s: %w", archive, err)
	}
	if info, err := os.Lstat(filepath.Join(tree, ".claude-plugin", "marketplace.json")); err != nil || !info.Mode().IsRegular() {
		return CondNotPlaced, fmt.Errorf("%s carries no .claude-plugin/marketplace.json", r.Label())
	}
	if err := makeReadOnly(tree); err != nil {
		return CondNotPlaced, fmt.Errorf("making %s read-only: %w", r.Label(), err)
	}
	seal, err := TakeSeal(tree)
	if err != nil {
		return CondNotPlaced, fmt.Errorf("sealing %s: %w", r.Label(), err)
	}
	mark := Mark{
		Schema:     markSchema,
		Source:     r.Source(),
		Ref:        SafeRef(r.Ref),
		Tag:        SafeRef(r.Tag),
		Commit:     r.Commit,
		CommitTime: commitTime.UTC().Format(time.RFC3339),
		PlacedAt:   time.Now().UTC().Format(time.RFC3339),
		Niwa:       SafeRef(buildinfo.Version()),
		Seal:       seal,
	}
	if err := writeMark(staging, mark); err != nil {
		return CondNotPlaced, err
	}

	repoDir := filepath.Dir(releaseDir(pdir, r))
	for _, dir := range []string{filepath.Join(pdir, Forge), filepath.Dir(repoDir), repoDir} {
		if err := hostdir.CreatePrivateDir(dir); err != nil {
			return CondNotPlaced, err
		}
	}
	if err := os.Rename(staging, releaseDir(pdir, r)); err != nil {
		return CondNotPlaced, fmt.Errorf("placing %s: %w", r.Label(), err)
	}
	placed = true
	_ = syncDir(repoDir)
	return CondNone, nil
}

// download streams r's tarball at its commit into path.
//
// The plugin-store-download fault label sits before the fetch, under r's lock
// and with the staging directory made, so a test can hold one command here
// while others queue on the lock, or kill it with nothing yet placed.
func (s Store) download(ctx context.Context, r Release, path string) error {
	if err := testfault.Maybe("plugin-store-download"); err != nil {
		return fmt.Errorf("fetching %s: %w", r.Label(), err)
	}
	body, err := s.Fetch(ctx, r.Owner, r.Repo, r.Commit)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", r.Label(), err)
	}
	defer body.Close()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	n, err := io.Copy(f, io.LimitReader(body, github.MaxDecompressedBytes+1))
	closeErr := f.Close()
	if err != nil {
		return fmt.Errorf("fetching %s: %w", r.Label(), err)
	}
	if closeErr != nil {
		return fmt.Errorf("writing %s: %w", path, closeErr)
	}
	if n > github.MaxDecompressedBytes {
		return fmt.Errorf("fetching %s: the archive is larger than %d bytes", r.Label(), github.MaxDecompressedBytes)
	}
	return nil
}

// checkArchive reads the archive's headers, never its file contents, and
// checks that its single top-level directory names r's commit, GitHub's
// <owner>-<repo>-<abbreviated commit>. It returns the first entry's
// modification time, which git archive sets to the commit time on every
// entry, so a refetch of the same commit reproduces it.
func checkArchive(path string, r Release) (time.Time, error) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading the archive of %s: %w", r.Label(), err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return time.Time{}, fmt.Errorf("reading the archive of %s: no entries", r.Label())
		}
		if hdr.Typeflag != tar.TypeDir && hdr.Typeflag != tar.TypeReg {
			continue
		}
		wrapper := strings.SplitN(strings.TrimSuffix(hdr.Name, "/"), "/", 2)[0]
		i := strings.LastIndex(wrapper, "-")
		abbrev := strings.ToLower(wrapper[i+1:])
		if i < 0 || len(abbrev) < 7 || !commitPattern.MatchString(abbrev+strings.Repeat("0", 40-len(abbrev))) || !strings.HasPrefix(r.Commit, abbrev) {
			return time.Time{}, fmt.Errorf("the archive of %s is wrapped in %q, which does not name commit %s", r.Label(), cleanName(wrapper), r.Commit[:12])
		}
		return hdr.ModTime, nil
	}
}

// cleanName cuts a name the remote chose to printable ASCII and 100 bytes.
func cleanName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s) && b.Len() < 100; i++ {
		if c := s[i]; c >= 0x20 && c < 0x7f {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// extract unpacks the archive into dest with the shared extractor and its
// path, symlink and size defences.
func extract(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	return github.ExtractSubpath(f, "", dest)
}

// makeReadOnly strips every write bit from the tree: directories 0555, files
// 0444, or 0555 where the archive marked them executable. Files go first and
// each directory after everything inside it.
func makeReadOnly(tree string) error {
	var dirs []string
	err := filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			dirs = append(dirs, path)
		case info.Mode().IsRegular():
			perm := os.FileMode(0o444)
			if info.Mode().Perm()&0o111 != 0 {
				perm = 0o555
			}
			if err := os.Chmod(path, perm); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i], 0o555); err != nil {
			return err
		}
	}
	return nil
}

// TakeSeal measures the tree with Lstat: its entry count, total size and the
// newest inode change time among its entries.
func TakeSeal(tree string) (Seal, error) {
	m, err := measure(tree)
	return m.seal, err
}

// measurement is what one Lstat walk of a tree found: the seal's figures,
// and whether any directory or regular file carries a write bit.
type measurement struct {
	seal     Seal
	writable bool
}

// measure walks tree with Lstat and never follows a link. The tree root
// counts as one entry and only regular files add to the size. A symlink is
// passed over for the write-bit check, since Lstat reports every link as
// 0777.
func measure(tree string) (measurement, error) {
	var m measurement
	err := filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		m.seal.Entries++
		mode := info.Mode()
		if mode.IsRegular() {
			m.seal.Bytes += info.Size()
		}
		if (mode.IsRegular() || mode.IsDir()) && mode.Perm()&0o222 != 0 {
			m.writable = true
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			if ns := ctimeNS(st); ns > m.seal.MaxCtimeNS {
				m.seal.MaxCtimeNS = ns
			}
		}
		return nil
	})
	return m, err
}

// Verify walks r's tree with Lstat and compares it with the seal in its
// held.json. It reports false when the entry count or the total size of the
// regular files differs, when any directory or regular file carries a write
// bit, or when any entry's inode change time is newer than the sealed one,
// so an edit, an added or removed entry, or a chmod (even one put back) each
// fail it. It reads metadata only, never file contents. The error says why
// when the mark can't be read or the walk fails inside the tree (an entry
// gone part way, an unreadable directory); the release is not intact then
// either.
//
// The walk does not catch a tree rewritten together with its held.json, or a
// change that keeps the entry count, the size and the newest ctime.
func (s Store) Verify(r Release) (bool, error) {
	mark, err := s.ReadMark(r)
	if err != nil {
		return false, fmt.Errorf("reading the mark of release %s: %w", r.Label(), err)
	}
	m, err := measure(TreePath(s.Home, s.WorkspaceRoot, r))
	if err != nil {
		return false, fmt.Errorf("walking release %s: %w", r.Label(), err)
	}
	return m.seal.Entries == mark.Seal.Entries &&
		m.seal.Bytes == mark.Seal.Bytes &&
		m.seal.MaxCtimeNS <= mark.Seal.MaxCtimeNS &&
		!m.writable, nil
}

// writeMark writes held.json into the staging directory: a temporary name,
// synced, made 0444 and renamed, and the directory synced.
func writeMark(staging string, m Mark) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(staging, "."+markFileName+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	_, werr := f.Write(append(data, '\n'))
	serr := f.Sync()
	cerr := f.Close()
	if err := errors.Join(werr, serr, cerr); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Chmod(tmp, 0o444); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(staging, markFileName)); err != nil {
		return err
	}
	return syncDir(staging)
}

// clearStaging removes the staging entries a killed placement of r left, by
// exact name.
func clearStaging(tmpDir string, r Release) {
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return
	}
	pattern := stagingPattern(r)
	for _, e := range entries {
		if pattern.MatchString(e.Name()) {
			removeTree(filepath.Join(tmpDir, e.Name()))
		}
	}
}

// trashRelease moves a release directory that is about to be placed again
// into .trash/ and removes it there, so a reader sees the whole release or
// nothing: one without a valid mark, or a marked one whose tree failed
// Verify. Only a real directory found by Lstat is moved. It runs before the
// download, so a placement that fails after it leaves nothing at the
// release path and no held.json.
func (s Store) trashRelease(r Release) error {
	dir := releaseDir(s.pluginsDir(), r)
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", dir, err)
	}
	suffix, err := randHex()
	if err != nil {
		return err
	}
	trash := filepath.Join(s.pluginsDir(), trashDirName, r.hash()+"-"+suffix)
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory; leaving it as it is", dir)
	}
	if err := os.Rename(dir, trash); err != nil {
		return fmt.Errorf("moving %s aside: %w", dir, err)
	}
	removeTree(trash)
	return nil
}

// removeTree gives the directories under path their write bits back and
// removes it. It walks with Lstat and never follows a link.
func removeTree(path string) {
	_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(path)
}

// syncDir flushes a directory's entries, so a rename in it survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
