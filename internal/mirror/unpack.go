package mirror

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// recoverPack salvages what can still be read from a damaged pack of an old
// store, one git verify-pack refused, into the new store's objects
// directory at dstObjects. A pack truncated halfway still holds every object
// written before the cut, and a borrower ref whose objects all sit there
// should read to the root after the restore (PRD R13); copying only packs
// that verify would throw that readable half away.
//
// It works in a temporary repository, .tmp/<repo-id>-<8 hex>, a name
// clearLeftovers removes under the lock should a crash leave it, and
// removes it on every path. The pack is read whole (it's a regular file,
// opened without following a link) and fed to git unpack-objects -r, which
// keeps unpacking past the first corruption; its exit status is ignored, as
// a damaged pack makes it fail by design. Each loose object it wrote is then
// kept only when git can read it in the temporary repository and its content
// hashes to its name, and is copied into the new store as a loose object
// unless the new store already holds it.
//
// It does nothing when the file system lacks the pack's size in free space:
// unpacking would only fill the disk the restore needs. Like the rest of
// salvage, any failure skips the pack and never fails the restore.
func recoverPack(ctx context.Context, mdir, repoID, pack, dstObjects string) {
	f, err := openRegular(pack)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return
	}
	if free, err := freeSpace(mdir); err == nil && free < uint64(info.Size()) {
		return
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return
	}

	suffix, err := randHex()
	if err != nil {
		return
	}
	tmp := filepath.Join(mdir, ".tmp", repoID+"-"+suffix)
	defer os.RemoveAll(tmp)
	if _, err := gitExec(ctx, nil, nil, "", "init", "--bare", "--quiet", "--template=", tmp); err != nil {
		return
	}
	_, _ = gitExec(ctx, nil, nil, string(data), "--git-dir="+tmp, "unpack-objects", "-r", "-q")

	ids := looseIDs(filepath.Join(tmp, "objects"))
	if len(ids) == 0 {
		return
	}
	readable := present(ctx, tmp, ids)
	held := present(ctx, filepath.Dir(dstObjects), ids)
	for _, id := range ids {
		if !readable[id] || held[id] {
			continue
		}
		src := filepath.Join(tmp, "objects", id[:2], id[2:])
		if !looseObjectIntact(src, id) {
			continue
		}
		dst := filepath.Join(dstObjects, id[:2], id[2:])
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if err := mkdirPrivate(filepath.Dir(dst)); err != nil {
			continue
		}
		_ = copyRegular(src, dst)
	}
}

// looseIDs lists the ids of the loose objects under objects/<2 hex>/, each a
// regular file by Lstat with a loose object's name.
func looseIDs(objects string) []string {
	dirs, err := os.ReadDir(objects)
	if err != nil {
		return nil
	}
	var ids []string
	for _, d := range dirs {
		if !looseDirPattern.MatchString(d.Name()) || !d.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(objects, d.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if looseNamePattern.MatchString(f.Name()) && f.Type().IsRegular() {
				ids = append(ids, d.Name()+f.Name())
			}
		}
	}
	return ids
}

// present returns which of ids the repository at gitDir can read, by one
// git cat-file --batch-check. A failure says none.
func present(ctx context.Context, gitDir string, ids []string) map[string]bool {
	out, err := gitOutput(ctx, nil, strings.Join(ids, "\n")+"\n",
		"--git-dir="+gitDir, "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil
	}
	found := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		// A missing object prints "<id> missing"; a present one its type.
		if id, kind, ok := strings.Cut(line, " "); ok && kind != "missing" && commitIDPattern.MatchString(id) {
			found[id] = true
		}
	}
	return found
}
