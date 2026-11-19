package mirror

import (
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// oldStores returns the names of the store directories of repoID under
// .store/: real directories only, by Lstat, so a link named like a store is
// neither read from nor removed.
func oldStores(mdir, repoID string) []string {
	entries, err := os.ReadDir(filepath.Join(mdir, ".store"))
	if err != nil {
		return nil
	}
	re := storeNamePattern(repoID)
	var names []string
	for _, e := range entries {
		if !re.MatchString(e.Name()) {
			continue
		}
		fi, err := os.Lstat(filepath.Join(mdir, ".store", e.Name()))
		if err != nil || !fi.IsDir() || !ownedByUser(fi) {
			continue
		}
		names = append(names, e.Name())
	}
	return names
}

// salvage copies into the new store at dst every object of the old stores
// at srcs (each a store directory's path) that is still good: a pack with its
// .idx when git verify-pack passes, and a loose object whose content hashes
// to its name. The fresh clone holds what the remote serves; salvage keeps
// what only the old store held, such as commits the remote has since deleted.
//
// Only regular files are read, each found by Lstat: no link is followed, and
// nothing but objects is copied, so a config, hook, alternates file or ref
// planted in an old store never reaches the new one. An object already in
// the new store is left as the clone wrote it. A file that can't be copied
// is skipped, never fatal: the clone alone is a usable mirror. A directory
// it makes in the new store is 0700 (mkdirPrivate).
func salvage(ctx context.Context, srcs []string, dst string) {
	for _, store := range srcs {
		src := filepath.Join(store, "objects")
		if fi, err := os.Lstat(src); err != nil || !fi.IsDir() {
			continue
		}
		salvagePacks(ctx, filepath.Join(src, "pack"), filepath.Join(dst, "objects", "pack"))
		salvageLoose(src, filepath.Join(dst, "objects"))
	}
}

// keptRefPrefix names the refs a restore pins salvaged commits under, so a
// commit only an old store's ref named stays reachable in the new store.
const keptRefPrefix = "refs/niwa/kept/"

// commitIDPattern is a full-length SHA-1 in hex, the only form a pin's name
// takes.
var commitIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// pinKept pins in the new store at dst, as refs/niwa/kept/<sha>, each commit
// that a ref of an old store at srcs names, that dst holds once salvage has
// run, and that no ref of dst reaches. An old store git can't open (its HEAD
// gone, say) gives no pins, and a commit whose history can't be walked isn't
// pinned: a pin keeps a commit the remote dropped, but the store is usable
// without one, so the restore goes on either way. A ref naming anything but a
// commit is left out. No git runs in an old store whose HEAD, config or
// packed-refs isn't a regular file or whose objects/ isn't a real directory
// (storeSafeForGit): git would follow a link there.
func pinKept(ctx context.Context, srcs []string, dst string) {
	seen := map[string]bool{}
	var named []string
	for _, src := range srcs {
		if !storeSafeForGit(src) {
			continue
		}
		out, err := gitOutput(ctx, nil, "", "--git-dir="+src, "for-each-ref", "--format=%(objectname)")
		if err != nil {
			continue
		}
		for _, id := range strings.Fields(out) {
			if commitIDPattern.MatchString(id) && !seen[id] {
				seen[id] = true
				named = append(named, id)
			}
		}
	}
	if len(named) == 0 {
		return
	}

	held := heldCommits(ctx, dst, named)
	if len(held) == 0 {
		return
	}

	// rev-list prints each commit reachable from held and from no ref of dst,
	// so a held commit it prints is one nothing in dst reaches. One commit
	// whose history salvage couldn't bring fails the whole walk, so on a
	// failure each commit is walked alone, and only the ones that can't be
	// walked go unpinned.
	unreached := map[string]bool{}
	if out, err := unreachedFrom(ctx, dst, held); err == nil {
		maps.Copy(unreached, out)
	} else {
		for _, id := range held {
			if out, err := unreachedFrom(ctx, dst, []string{id}); err == nil {
				maps.Copy(unreached, out)
			}
		}
	}
	var pins []string
	for _, id := range held {
		if unreached[id] {
			pins = append(pins, id)
		}
	}
	_ = writePins(ctx, dst, pins)
}

// heldCommits returns those of ids, each a full-length commit id, that the
// store at dst holds as commits, in the order given.
func heldCommits(ctx context.Context, dst string, ids []string) []string {
	want := map[string]bool{}
	var valid []string
	for _, id := range ids {
		if commitIDPattern.MatchString(id) && !want[id] {
			want[id] = true
			valid = append(valid, id)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	out, err := gitOutput(ctx, nil, strings.Join(valid, "\n")+"\n",
		"--git-dir="+dst, "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil
	}
	isCommit := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if id, kind, ok := strings.Cut(line, " "); ok && kind == "commit" && want[id] {
			isCommit[id] = true
		}
	}
	var held []string
	for _, id := range valid {
		if isCommit[id] {
			held = append(held, id)
		}
	}
	return held
}

// writePins pins each of ids in the store at dst as refs/niwa/kept/<sha>, in
// one update-ref --stdin, so a commit nothing else names stays reachable.
// Each id must be a full-length commit id the store holds.
func writePins(ctx context.Context, dst string, ids []string) error {
	var cmds strings.Builder
	for _, id := range ids {
		if commitIDPattern.MatchString(id) {
			cmds.WriteString("update " + keptRefPrefix + id + " " + id + "\n")
		}
	}
	if cmds.Len() == 0 {
		return nil
	}
	_, err := gitOutput(ctx, nil, cmds.String(), "--git-dir="+dst, "update-ref", "--stdin")
	return err
}

// unreachedFrom returns the commits of ids that no ref of the store at dst
// reaches.
func unreachedFrom(ctx context.Context, dst string, ids []string) (map[string]bool, error) {
	out, err := gitOutput(ctx, nil, strings.Join(ids, "\n")+"\n",
		"--git-dir="+dst, "rev-list", "--stdin", "--not", "--all")
	if err != nil {
		return nil, err
	}
	printed := map[string]bool{}
	for _, id := range strings.Fields(out) {
		printed[id] = true
	}
	unreached := map[string]bool{}
	for _, id := range ids {
		if printed[id] {
			unreached[id] = true
		}
	}
	return unreached, nil
}

// salvagePacks copies each pack in srcDir whose .idx is beside it and passes
// git verify-pack. The .idx is copied before the .pack and both keep their
// names, so git never sees a pack without its index.
func salvagePacks(ctx context.Context, srcDir, dstDir string) {
	if fi, err := os.Lstat(srcDir); err != nil || !fi.IsDir() {
		return
	}
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !validPackName(e.Name()) || !e.Type().IsRegular() {
			continue
		}
		idxName := strings.TrimSuffix(e.Name(), ".pack") + ".idx"
		pack, idx := filepath.Join(srcDir, e.Name()), filepath.Join(srcDir, idxName)
		if fi, err := os.Lstat(idx); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dstDir, e.Name())); err == nil {
			continue
		}
		if verifyPack(ctx, idx) != nil {
			continue
		}
		if err := mkdirPrivate(dstDir); err != nil {
			return
		}
		dstIdx, dstPack := filepath.Join(dstDir, idxName), filepath.Join(dstDir, e.Name())
		if copyRegular(idx, dstIdx) != nil {
			continue
		}
		if copyRegular(pack, dstPack) != nil {
			_ = os.Remove(dstIdx)
		}
	}
}

// salvageLoose copies each loose object under srcObjects/<2 hex>/ whose
// zlib content hashes to its name.
func salvageLoose(srcObjects, dstObjects string) {
	dirs, err := os.ReadDir(srcObjects)
	if err != nil {
		return
	}
	for _, d := range dirs {
		if !looseDirPattern.MatchString(d.Name()) || !d.IsDir() {
			continue
		}
		srcDir := filepath.Join(srcObjects, d.Name())
		files, err := os.ReadDir(srcDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !looseNamePattern.MatchString(f.Name()) || !f.Type().IsRegular() {
				continue
			}
			src := filepath.Join(srcDir, f.Name())
			dst := filepath.Join(dstObjects, d.Name(), f.Name())
			if _, err := os.Lstat(dst); err == nil {
				continue
			}
			if !looseObjectIntact(src, d.Name()+f.Name()) {
				continue
			}
			if err := mkdirPrivate(filepath.Dir(dst)); err != nil {
				continue
			}
			_ = copyRegular(src, dst)
		}
	}
}

// mkdirPrivate makes dir, whose parent exists, with mode 0700 set explicitly
// rather than left to the umask. A directory already there is left as it is;
// anything else there is an error.
func mkdirPrivate(dir string) error {
	err := os.Mkdir(dir, 0o700)
	if err == nil {
		return os.Chmod(dir, 0o700)
	}
	if !os.IsExist(err) {
		return err
	}
	if fi, lerr := os.Lstat(dir); lerr != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}

// storeSafeForGit reports whether git may run in the store directory: its HEAD
// and config are regular files by Lstat, its packed-refs is absent or a
// regular file, and its objects/ is a real directory. git would follow a
// link at any of them to whatever it names.
func storeSafeForGit(store string) bool {
	for _, name := range []string{"HEAD", "config"} {
		if fi, err := os.Lstat(filepath.Join(store, name)); err != nil || !fi.Mode().IsRegular() {
			return false
		}
	}
	if fi, err := os.Lstat(filepath.Join(store, "packed-refs")); err == nil && !fi.Mode().IsRegular() ||
		err != nil && !os.IsNotExist(err) {
		return false
	}
	fi, err := os.Lstat(filepath.Join(store, "objects"))
	return err == nil && fi.IsDir()
}

// looseObjectIntact reports whether the loose object at path inflates to
// content whose SHA-1 is id.
func looseObjectIntact(path, id string) bool {
	f, err := openRegular(path)
	if err != nil {
		return false
	}
	defer f.Close()
	zr, err := zlib.NewReader(f)
	if err != nil {
		return false
	}
	defer zr.Close()
	h := sha1.New()
	if _, err := io.Copy(h, zr); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == id
}

// openRegular opens path for reading only if it is a regular file, checking
// the opened file so a link swapped in after an Lstat isn't followed. It
// opens without blocking, so a fifo at path is refused rather than waited
// on.
func openRegular(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	return f, nil
}

// copyRegular copies the regular file src to a new file dst, read-only like
// the objects git writes. dst must not exist; a partial copy is removed.
func copyRegular(src, dst string) error {
	in, err := openRegular(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}
