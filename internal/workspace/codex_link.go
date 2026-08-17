package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// codexCopyMaxLinkHops bounds how deep the copy fallback follows symlinks it
// finds inside the payload. The payload's own shape needs one hop (a
// skills/<plugin> link into an installed tree); the allowance exists for links
// inside a plugin tree, and the bound is what keeps a link cycle from turning a
// fallback copy into an unbounded walk.
const codexCopyMaxLinkHops = 4

// codexLinkPrefersCopy decides, once per process, whether the per-repository
// delivery is a symlink or a real copy of the payload. It is the single decision
// point for the fallback: everything below it copies or links because this said
// so, rather than each write re-deciding.
//
// Directory symlinks need elevated privileges on Windows, so a copy is the
// default there (Decision 1A). Everywhere else the symlink is preferred, because
// it leaves one source of truth: `niwa apply` regenerates one directory and the
// change is live in every repository at once. A symlink write that fails anyway
// falls back to a copy at the call site, which covers a filesystem that rejects
// links for its own reasons.
//
// It is a package variable so tests can exercise the fallback on a platform
// where symlinks work.
var codexLinkPrefersCopy = func() bool { return runtime.GOOS == "windows" }

// CodexLinkResult reports what one repository's payload delivery produced.
type CodexLinkResult struct {
	// Path is the delivered .codex path, or "" when nothing was written.
	//
	// It is deliberately not a managed file: it is a symlink to a directory (or,
	// under the fallback, a directory), neither of which the managed-file
	// pipeline can hash, so this writer reconciles it against the payload itself
	// on every apply -- the same division the payload's skills links use.
	Path string
	// Copied reports that the real-copy fallback produced a directory rather
	// than a symlink.
	Copied bool
	// Foreign reports that the .codex name in this repository is occupied by
	// something niwa did not materialize, so nothing was written, modified, or
	// deleted there.
	//
	// This is the seam issue 7's conflict rule extends: that rule adds the loud
	// per-repository verdict, the coupling to the composed override, and the
	// managed-file cleanup exemption. What this writer owns is the refusal
	// itself -- niwa never writes over a name a repository already occupies.
	Foreign bool
}

// InstallRepoCodexLink delivers the instance's Codex payload into one cloned
// repository, as `<repoDir>/.codex`.
//
// The link is what makes one payload reach every session: Codex walks up from
// the working directory to the nearest project-root marker, finds the
// repository's `.git`, and reads `.codex` right beside it -- so config, skills,
// and context all load through this one entry, from the repository root and from
// any directory below it. The project-layer loader follows symlinks, which is
// why a link works at all (Decision 1A).
//
// niwa recognizes its own delivery by what it points at: a symlink is niwa's
// when it resolves to this instance's payload, and a real directory is niwa's
// when it carries the payload config's generation marker. A delivery that is
// niwa's is repaired in place (retargeted, or re-copied) so a deleted, dangling,
// or stale entry heals on the next apply. Anything else at the name is left
// exactly as it is, and reported through Foreign.
func InstallRepoCodexLink(instanceRoot, repoDir string) (*CodexLinkResult, error) {
	payloadDir, err := filepath.Abs(filepath.Join(instanceRoot, CodexPayloadDirName))
	if err != nil {
		return nil, fmt.Errorf("resolving Codex payload directory: %w", err)
	}
	linkPath := filepath.Join(repoDir, CodexPayloadDirName)

	info, err := os.Lstat(linkPath)
	switch {
	case err != nil && !os.IsNotExist(err):
		return nil, fmt.Errorf("inspecting %s: %w", linkPath, err)

	case err == nil && info.Mode()&os.ModeSymlink != 0:
		if codexLinkTargets(linkPath, payloadDir) {
			return &CodexLinkResult{Path: linkPath}, nil
		}
		// A link niwa planted whose payload moved -- an instance renamed, a
		// workspace relocated -- points at a path that is no longer this
		// instance's. Retarget it: the shape is niwa's own, and a stale target
		// delivers the wrong workspace's skills and budget.
		if rmErr := os.Remove(linkPath); rmErr != nil {
			return nil, fmt.Errorf("replacing stale Codex link %s: %w", linkPath, rmErr)
		}

	case err == nil && info.IsDir():
		if !codexPayloadCopyIsNiwas(linkPath) {
			return &CodexLinkResult{Foreign: true}, nil
		}
		// niwa's own copy from a prior apply under the fallback. Re-deliver it
		// wholesale so a de-configured plugin's skills leave the copy too.
		if rmErr := os.RemoveAll(linkPath); rmErr != nil {
			return nil, fmt.Errorf("refreshing Codex payload copy %s: %w", linkPath, rmErr)
		}

	case err == nil:
		// A regular file (or anything else) at the name is not a shape niwa ever
		// writes, so it is not niwa's.
		return &CodexLinkResult{Foreign: true}, nil
	}

	copied, err := deliverCodexPayload(linkPath, payloadDir)
	if err != nil {
		return nil, err
	}
	return &CodexLinkResult{Path: linkPath, Copied: copied}, nil
}

// codexLinkTargets reports whether the symlink at linkPath resolves to
// payloadDir. A relative target is resolved against the link's own directory,
// and a textual mismatch is re-checked through fully resolved paths so an
// instance reached by a symlinked parent (a linked home directory, an
// automounted volume) is not mistaken for a stale target.
func codexLinkTargets(linkPath, payloadDir string) bool {
	target, err := os.Readlink(linkPath)
	if err != nil {
		return false
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	if filepath.Clean(target) == filepath.Clean(payloadDir) {
		return true
	}

	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	resolvedPayload, err := filepath.EvalSymlinks(payloadDir)
	if err != nil {
		return false
	}
	return resolvedTarget == resolvedPayload
}

// codexPayloadCopyIsNiwas reports whether the directory at dir is a copy of the
// payload niwa made, recognized by the generation marker on the first line of
// the payload config it holds. The marker is content niwa writes, so the test
// works on the standalone worktree path, which keeps no managed-file records.
func codexPayloadCopyIsNiwas(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, codexPayloadConfigName))
	if err != nil {
		return false
	}
	first, _, _ := strings.Cut(string(data), "\n")
	first = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(first), "#"))
	return HasCodexGenerationMarker([]byte(first))
}

// deliverCodexPayload writes the payload delivery at linkPath, which does not
// exist. It reports whether the copy fallback was used.
func deliverCodexPayload(linkPath, payloadDir string) (bool, error) {
	if !codexLinkPrefersCopy() {
		if err := os.Symlink(payloadDir, linkPath); err == nil {
			return false, nil
		}
		// The platform said symlinks were available and the write disagreed.
		// The payload is kilobytes of text, so the copy costs almost nothing and
		// keeps the repository usable rather than failing the apply.
	}

	if err := copyCodexPayload(payloadDir, linkPath, codexCopyMaxLinkHops); err != nil {
		return false, fmt.Errorf("copying the Codex payload into %s: %w", linkPath, err)
	}
	return true, nil
}

// copyCodexPayload copies the payload tree at src to dst as real files and
// directories, following symlinks rather than reproducing them: the fallback
// exists precisely where links are unavailable, so a copy full of links would
// deliver nothing. hops bounds how many symlinked directories deep the copy
// follows, so a link cycle terminates.
func copyCodexPayload(src, dst string, hops int) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dst, err)
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}

	for _, e := range entries {
		srcPath := filepath.Join(src, e.Name())
		dstPath := filepath.Join(dst, e.Name())

		info, err := os.Lstat(srcPath)
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", srcPath, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			if hops == 0 {
				// Deeper than the payload's own shape needs; a cycle is the
				// likeliest cause, and stopping here bounds the copy.
				continue
			}
			resolved, err := filepath.EvalSymlinks(srcPath)
			if err != nil {
				// A dangling link inside the payload is already reported by the
				// payload writer as a missing plugin root; the copy carries the
				// same gap rather than failing the apply over it.
				continue
			}
			resolvedInfo, err := os.Stat(resolved)
			if err != nil {
				continue
			}
			if resolvedInfo.IsDir() {
				if err := copyCodexPayload(resolved, dstPath, hops-1); err != nil {
					return err
				}
				continue
			}
			if err := copyCodexFile(resolved, dstPath, resolvedInfo.Mode().Perm()); err != nil {
				return err
			}
			continue
		}

		if info.IsDir() {
			if err := copyCodexPayload(srcPath, dstPath, hops); err != nil {
				return err
			}
			continue
		}

		if !info.Mode().IsRegular() {
			continue
		}
		if err := copyCodexFile(srcPath, dstPath, info.Mode().Perm()); err != nil {
			return err
		}
	}

	return nil
}

// copyCodexFile copies one regular file, preserving its permission bits.
func copyCodexFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("reading %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("writing %s: %w", dst, err)
	}
	return out.Close()
}
