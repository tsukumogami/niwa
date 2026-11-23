package removal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// niwaGenerated are the entries niwa itself writes directly under an
// instance's .niwa directory: its state and records, the plugin and
// marketplace trees it links there, and its locks and spill files. Anything
// else found there is unique, like any other loose file.
var niwaGenerated = map[string]bool{
	"instance.json":        true,
	"authority.json":       true,
	"capture-pending.json": true,
	"dispatch-pending":     true,
	"dispatch-retain":      true,
	"dispatch-prompts":     true,
	"attach.lock":          true,
	"sessions":             true,
	"plugin":               true,
	"marketplaces":         true,
}

// dispatchCapture reports whether name is one of the files a detached
// dispatch captures its agent's standard output or error in,
// dispatch-<program>.out and dispatch-<program>.err.
func dispatchCapture(name string) bool {
	stem, ok := strings.CutPrefix(name, "dispatch-")
	if !ok {
		return false
	}
	program, stream, ok := strings.Cut(stem, ".")
	return ok && program != "" && !strings.ContainsAny(program, "./") && (stream == "out" || stream == "err")
}

// rootGeneratedDirs are the plugin trees niwa delivers at the instance root
// for an agent that reads skills from a directory.
var rootGeneratedDirs = map[string]bool{
	".codex/skills": true,
}

// maxIgnoreBytes bounds the instance-root .gitignore niwa writes.
const maxIgnoreBytes = 4096

// patternsOnly reports whether the file at p, the .gitignore niwa keeps at
// the instance root, holds nothing but ignore patterns: small, and every
// line empty or a single word. A file with prose in it is something someone
// wrote, and keeps.
func patternsOnly(p string) bool {
	b, err := readRegular(p, maxIgnoreBytes)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.ContainsAny(strings.TrimSpace(line), " \t") {
			return false
		}
	}
	return true
}

// jobGenerated are the files Claude Code writes at the top of a job
// directory and writes again for a new job.
var jobGenerated = map[string]bool{
	"state.json":     true,
	"timeline.jsonl": true,
}

// maxStateBytes bounds the read of instance.json.
const maxStateBytes = 8 << 20

// maxManagedBytes bounds the read of a managed file when it is hashed.
const maxManagedBytes = 16 << 20

// maxEmptyDepth bounds how deep the empty-directory test looks.
const maxEmptyDepth = 64

// generated returns the test for a file niwa wrote and still holds what
// niwa wrote: a path, relative to the instance, that instance.json lists in
// managed_files and whose content still has the hash recorded there. A file
// edited since, or one the state can't be read for, is not generated.
func (s *evalState) generated(instance string) func(rel string) bool {
	managed := readManaged(instance)
	return func(rel string) bool {
		want, ok := managed[filepath.ToSlash(rel)]
		if !ok {
			return false
		}
		b, err := readRegular(filepath.Join(instance, filepath.FromSlash(rel)), maxManagedBytes)
		if err != nil {
			return false
		}
		sum := sha256.Sum256(b)
		return want == "sha256:"+hex.EncodeToString(sum[:])
	}
}

// readManaged reads managed_files from the instance's state: each path,
// relative to the instance, with its recorded hash. A state that can't be
// read gives none.
func readManaged(instance string) map[string]string {
	out := map[string]string{}
	niwaDir := filepath.Join(instance, ".niwa")
	if info, err := os.Lstat(niwaDir); err != nil || !info.IsDir() {
		return out
	}
	b, err := readRegular(filepath.Join(niwaDir, "instance.json"), maxStateBytes)
	if err != nil {
		return out
	}
	var st struct {
		ManagedFiles []struct {
			Path string `json:"path"`
			Hash string `json:"hash"`
		} `json:"managed_files"`
	}
	if json.Unmarshal(b, &st) != nil {
		return out
	}
	bases := []string{instance}
	if r, err := filepath.EvalSymlinks(instance); err == nil && r != instance {
		bases = append(bases, r)
	}
	for _, mf := range st.ManagedFiles {
		if !filepath.IsAbs(mf.Path) || mf.Hash == "" {
			continue
		}
		for _, base := range bases {
			rel, err := filepath.Rel(base, filepath.Clean(mf.Path))
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			out[filepath.ToSlash(rel)] = mf.Hash
			break
		}
	}
	return out
}

// checkOutside is the outside check: every path outside the repositories
// the walk found, in the instance and in the unit's job directory, gets one
// class, and a unique one keeps the unit. Paths inside a judged clone are
// the repository check's; a .git the rule doesn't judge yet keeps there.
func checkOutside(ctx context.Context, u Unit, p Policy, s *evalState) []Check {
	if s.walk == nil {
		f := walkInstance(ctx, u.InstancePath, p.CloneURL)
		s.walk = &f
	}
	skip := map[string]bool{}
	for _, c := range s.walk.clones {
		skip[c.WorkTree] = true
	}
	for _, rel := range s.walk.unjudged {
		skip[filepath.Join(u.InstancePath, filepath.Dir(rel))] = true
	}
	pats := append(append([]string(nil), ShippedDisposable...), p.Disposable...)
	gen := s.generated(u.InstancePath)

	var unique []string
	err := filepath.WalkDir(u.InstancePath, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if path == u.InstancePath {
			return err
		}
		rel, relErr := filepath.Rel(u.InstancePath, path)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(rel)
		if err != nil {
			// The repository check's walk read the same entry and keeps
			// the unit with the error; here it is only classed.
			s.classes.add(slash, ClassUnique, "")
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		isDir := d.IsDir()
		if isDir && skip[path] {
			return filepath.SkipDir
		}
		if d.Name() == ".git" {
			// Classed and kept by the repository check.
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}
		top, inNiwa := strings.CutPrefix(slash, ".niwa/")
		switch {
		case slash == ".niwa" && isDir:
			return nil
		case inNiwa:
			name, _, _ := strings.Cut(top, "/")
			class := ClassUnique
			if niwaGenerated[name] || dispatchCapture(name) {
				class = ClassGenerated
			}
			if class == ClassUnique {
				unique = append(unique, slash)
			}
			if isDir {
				s.classes.add(dirPath(slash), class, "")
				return filepath.SkipDir
			}
			s.classes.add(slash, class, "")
			return nil
		}
		if rootGeneratedDirs[slash] && isDir {
			s.classes.add(dirPath(slash), ClassGenerated, "")
			return filepath.SkipDir
		}
		if slash == ".gitignore" && !isDir && patternsOnly(path) {
			s.classes.add(slash, ClassGenerated, "")
			return nil
		}
		if isDir {
			if emptyTree(path, 0) {
				s.classes.add(dirPath(slash), ClassGenerated, "")
				return filepath.SkipDir
			}
			if matchAny(pats, slash, true) {
				s.classes.add(dirPath(slash), ClassDisposable, "")
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case matchAny(pats, slash, false):
			s.classes.add(slash, ClassDisposable, "")
		case gen(slash):
			s.classes.add(slash, ClassGenerated, "")
		default:
			s.classes.add(slash, ClassUnique, "")
			unique = append(unique, slash)
		}
		return nil
	})
	var keeps []Check
	if err != nil {
		keeps = append(keeps, Check{Evidence: fmt.Sprintf("the walk outside the repositories stopped: %v", err)})
	}
	if len(unique) > 0 {
		keeps = append(keeps, Check{Paths: unique, Evidence: "files outside every repository: " + nameSome(unique)})
	}
	return append(keeps, checkJobDir(ctx, u, p, pats, s)...)
}

// checkJobDir classes everything in the unit's job directory: Claude Code's
// own state files and empty directories are generated, a pattern makes a
// path disposable, and any other file is job scratch, disposable with the
// job scratch notice unless the workspace counts job scratch as unique. A
// job directory that is a link, or isn't a directory, keeps the unit.
func checkJobDir(ctx context.Context, u Unit, p Policy, pats []string, s *evalState) []Check {
	if u.JobDir == "" {
		return nil
	}
	info, err := os.Lstat(u.JobDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return []Check{{Paths: []string{jobPrefix}, Evidence: "the job directory could not be read: " + errText(err)}}
	case !info.IsDir():
		s.classes.add(jobPrefix, ClassUnique, "")
		return []Check{{Paths: []string{jobPrefix}, Evidence: "the job directory is not a directory"}}
	}
	var unique, unreadable []string
	walkErr := filepath.WalkDir(u.JobDir, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if path == u.JobDir {
			return err
		}
		rel, relErr := filepath.Rel(u.JobDir, path)
		if relErr != nil {
			return relErr
		}
		slash := filepath.ToSlash(rel)
		name := jobPrefix + slash
		if err != nil {
			s.classes.add(name, ClassUnique, "")
			unreadable = append(unreadable, fmt.Sprintf("%s: %s", name, errText(err)))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		isDir := d.IsDir()
		if d.Name() == ".git" {
			s.classes.add(name, ClassUnique, "")
			unique = append(unique, name)
			if isDir {
				return filepath.SkipDir
			}
			return nil
		}
		if isDir {
			switch {
			case emptyTree(path, 0):
				s.classes.add(dirPath(name), ClassGenerated, "")
				return filepath.SkipDir
			case matchAny(pats, slash, true):
				s.classes.add(dirPath(name), ClassDisposable, "")
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case !strings.Contains(slash, "/") && jobGenerated[slash]:
			s.classes.add(name, ClassGenerated, "")
		case matchAny(pats, slash, false):
			s.classes.add(name, ClassDisposable, "")
		case p.JobScratchUnique:
			s.classes.add(name, ClassUnique, "")
			unique = append(unique, name)
		default:
			s.classes.add(name, ClassDisposable, NoticeJobScratch)
		}
		return nil
	})
	var keeps []Check
	if walkErr != nil {
		keeps = append(keeps, Check{Paths: []string{jobPrefix}, Evidence: fmt.Sprintf("the job directory walk stopped: %v", walkErr)})
	}
	for _, msg := range unreadable {
		name, _, _ := strings.Cut(msg, ": ")
		keeps = append(keeps, Check{Paths: []string{name}, Evidence: "unreadable: " + msg})
	}
	if len(unique) > 0 {
		keeps = append(keeps, Check{Paths: unique, Evidence: "job scratch counted as unique: " + nameSome(unique)})
	}
	return keeps
}

// emptyTree reports whether dir is a real directory with no entries, or
// holding only directories that are themselves empty trees.
func emptyTree(dir string, depth int) bool {
	if depth > maxEmptyDepth {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() || e.Type()&fs.ModeSymlink != 0 {
			return false
		}
		if !emptyTree(filepath.Join(dir, e.Name()), depth+1) {
			return false
		}
	}
	return true
}
