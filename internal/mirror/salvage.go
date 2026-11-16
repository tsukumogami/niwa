package mirror

import (
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
// that is still good: a pack with its .idx when git verify-pack passes, and
// a loose object whose content hashes to its name. The fresh clone holds
// what the remote serves; salvage keeps what only the old store held, such
// as commits the remote has since deleted.
//
// Only regular files are read, each found by Lstat: no link is followed, and
// nothing but objects is copied, so a config, hook, alternates file or ref
// planted in an old store never reaches the new one. An object already in
// the new store is left as the clone wrote it. A file that can't be copied
// is skipped, never fatal: the clone alone is a usable mirror.
func salvage(ctx context.Context, mdir string, olds []string, dst string) {
	for _, name := range olds {
		src := filepath.Join(mdir, ".store", name, "objects")
		if fi, err := os.Lstat(src); err != nil || !fi.IsDir() {
			continue
		}
		salvagePacks(ctx, filepath.Join(src, "pack"), filepath.Join(dst, "objects", "pack"))
		salvageLoose(src, filepath.Join(dst, "objects"))
	}
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
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
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
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				continue
			}
			_ = copyRegular(src, dst)
		}
	}
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
// the opened file so a link swapped in after an Lstat isn't followed.
func openRegular(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
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
