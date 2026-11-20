package pluginstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ParseLink reads a link target as a release link, whatever home or key
// prefixes it, by its plugins/github.com/<owner>/<repo>/<commit>/tree tail.
// The target must be absolute and clean, and every name in the tail must pass
// the store's patterns. Nothing is read through the target.
func ParseLink(target string) (Release, bool) {
	if !filepath.IsAbs(target) || filepath.Clean(target) != target {
		return Release{}, false
	}
	parts := strings.Split(target, string(filepath.Separator))
	n := len(parts)
	if n < 7 {
		return Release{}, false
	}
	tail := parts[n-6:]
	if tail[0] != "plugins" || tail[1] != Forge || tail[5] != treeDirName {
		return Release{}, false
	}
	r := Release{Owner: tail[2], Repo: tail[3], Commit: tail[4]}
	if !r.Valid() {
		return Release{}, false
	}
	return r, true
}

// placeLink points link at target: a temporary link beside it renamed over
// it, so a reader sees the old link or the new one and never neither. link's
// directory must be, by Lstat, a real directory: the instance's
// .niwa/marketplaces. An existing entry at link that is not a symlink is
// refused rather than replaced.
func placeLink(link, target string) error {
	dir := filepath.Dir(link)
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a real directory", dir)
	}
	if existing, err := os.Lstat(link); err == nil {
		if existing.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("%s exists and is not a link; leaving it as it is", link)
		}
		if current, err := os.Readlink(link); err == nil && current == target {
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspecting %s: %w", link, err)
	}

	suffix, err := randHex()
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+filepath.Base(link)+".link-"+suffix)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("creating link %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("placing link %s: %w", link, err)
	}
	return nil
}
