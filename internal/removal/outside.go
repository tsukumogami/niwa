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
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
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
	b, err := readCapped(p, maxIgnoreBytes)
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
// edited since, or one the state can't be read for, is not generated. A
// managed file the read refuses is a failure the outside check keeps the
// unit on, named.
func (s *evalState) generated(instance string) func(rel string) bool {
	m := s.managedFiles(instance)
	return func(rel string) bool {
		key := filepath.ToSlash(rel)
		mf, ok := m.files[key]
		if !ok {
			return false
		}
		b, err := s.readManagedFile(mf)
		if err != nil {
			m.refuse(key, err)
			return false
		}
		sum := sha256.Sum256(b)
		return mf.hash == "sha256:"+hex.EncodeToString(sum[:])
	}
}

// instanceOpenFlags opens the instance directory a managed file is read
// under. The instance itself is the caller's, so a link at or above it is
// followed, as ResolveRecorded follows it.
const instanceOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC

// readManagedFile reads the managed file mf for its hash. ResolveRecorded
// checked its path, but a directory on the way can be swapped for a link or
// a mount after that check, so the read doesn't go by name: it starts from a
// descriptor for the instance, or for the .niwa the evaluation opened when
// the file is under .niwa, and opens each directory on the way relative to
// the one before it without following a link. Everything it opens, the .niwa
// included, must be on the instance's device, the one device ResolveRecorded
// allows. The file itself goes through the shared reader.
func (s *evalState) readManagedFile(mf managedFile) ([]byte, error) {
	fd, err := retryEINTR(func() (int, error) { return unix.Open(mf.base, instanceOpenFlags, 0) })
	if err != nil {
		return nil, fmt.Errorf("the instance can't be opened: %v", err)
	}
	defer unix.Close(fd)
	dev, err := fdDevice(fd)
	if err != nil {
		return nil, fmt.Errorf("the instance's device can't be read: %v", err)
	}
	parts := strings.Split(mf.rel, "/")
	if len(parts) > 1 && parts[0] == niwaDirName {
		if err := s.niwa.usable(); err != nil {
			return nil, err
		}
		if err := onDevice(s.niwa.fd, niwaDirName, dev); err != nil {
			return nil, err
		}
		return readUnder(s.niwa.fd, niwaDirName, parts[1:], maxManagedBytes, dev)
	}
	return readUnder(fd, "", parts, maxManagedBytes, dev)
}

// readUnder reads the file parts names under the directory open at dirfd,
// which is named prefix in errors. Each directory on the way is opened
// relative to the one before it with O_DIRECTORY|O_NOFOLLOW, so a link at
// any of them is refused, and each must be on device dev, as must the file.
// The last component goes through the shared reader: not a link, not a
// FIFO, a regular file of the current user's, no larger than max. dirfd
// stays open.
func readUnder(dirfd int, prefix string, parts []string, max int64, dev uint64) ([]byte, error) {
	if len(parts) == 0 {
		return nil, errors.New("the path names no file")
	}
	cur := dirfd
	closeCur := func() {
		if cur != dirfd {
			unix.Close(cur)
		}
	}
	defer closeCur()
	shown := prefix
	for _, part := range parts[:len(parts)-1] {
		shown = path.Join(shown, part)
		next, err := retryEINTR(func() (int, error) { return unix.Openat(cur, part, dirOpenFlags, 0) })
		if err != nil {
			return nil, openError(shown, err)
		}
		closeCur()
		cur = next
		if err := onDevice(cur, shown, dev); err != nil {
			return nil, err
		}
	}
	name := parts[len(parts)-1]
	fd, err := retryEINTR(func() (int, error) { return unix.Openat(cur, name, readOpenFlags, 0) })
	if err != nil {
		return nil, openError(name, err)
	}
	if err := onDevice(fd, path.Join(shown, name), dev); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return readFD(fd, name, max)
}

// statDevice is the device a stat names. A variable so a test can put a
// directory or file on another device without mounting one.
var statDevice = func(st *unix.Stat_t) uint64 { return uint64(st.Dev) }

// fdDevice is the device of the file open at fd.
func fdDevice(fd int) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return 0, err
	}
	return statDevice(&st), nil
}

// onDevice refuses the file open at fd, named shown, unless it is on device
// dev, in the words ResolveRecorded uses.
func onDevice(fd int, shown string, dev uint64) error {
	d, err := fdDevice(fd)
	if err != nil {
		return fmt.Errorf("%s: its device can't be read: %v", shown, err)
	}
	if d != dev {
		return fmt.Errorf("%s is on another device", shown)
	}
	return nil
}

// refuse records that the managed file rel couldn't be read for its hash,
// once, as a failure the outside check names.
func (m *managedRead) refuse(rel string, err error) {
	for _, c := range m.failures {
		if len(c.Paths) == 1 && c.Paths[0] == rel {
			return
		}
	}
	m.failures = append(m.failures, Check{Paths: []string{rel}, Evidence: fmt.Sprintf("unreadable: managed file %q: %s", rel, err.Error())})
}

// instanceStateRecord is the instance's state file under .niwa.
const instanceStateRecord = "instance.json"

// managedFile is one managed_files entry that ResolveRecorded accepted: the
// hash niwa recorded, the instance path it was resolved under, and its path
// relative to that, slash separated.
type managedFile struct {
	hash, base, rel string
}

// managedRead is what one evaluation read of managed_files: each entry
// ResolveRecorded accepted, by its path relative to the instance, and the
// keeping checks for what couldn't be read or resolved.
type managedRead struct {
	files    map[string]managedFile
	failures []Check
}

// managedFiles reads managed_files once per evaluation, so the repository
// check and the outside check share one read and a failure is named once.
func (s *evalState) managedFiles(instance string) *managedRead {
	if s.managed == nil {
		s.managed = s.readManaged(instance)
	}
	return s.managed
}

// readManaged reads managed_files from the instance's state, relative to
// the .niwa the evaluation opened. An absent state, or one that doesn't
// parse, gives no managed files. A state that is present but refused (a
// link, a FIFO, another user's file, one over its cap) is a failure the
// outside check keeps the unit on. instance.json is niwa's own file, so its
// decode stays lenient.
//
// Every entry under the instance goes through ResolveRecorded before the
// rule hashes it. An entry it refuses (a "..", a link on the way, another
// device) is a failure named under the outside check; an entry whose file
// is gone is dropped; an entry outside the instance isn't the rule's.
func (s *evalState) readManaged(instance string) *managedRead {
	m := &managedRead{files: map[string]managedFile{}}
	if s.niwa.absent || s.niwa.usable() != nil {
		// A .niwa the rule can't use keeps through the hold and records
		// checks.
		return m
	}
	present, err := s.niwa.present(instanceStateRecord)
	if !present {
		return m
	}
	var b []byte
	if err == nil {
		b, err = s.niwa.read(instanceStateRecord, maxStateBytes)
	}
	if err != nil {
		rel := niwaDirName + "/" + instanceStateRecord
		m.failures = append(m.failures, Check{Paths: []string{rel}, Evidence: "unreadable: " + rel + ": " + errText(err)})
		return m
	}
	out := m.files
	var st struct {
		ManagedFiles []struct {
			Path string `json:"path"`
			Hash string `json:"hash"`
		} `json:"managed_files"`
	}
	if json.Unmarshal(b, &st) != nil {
		return m
	}
	bases := []string{instance}
	if r, err := filepath.EvalSymlinks(instance); err == nil && r != instance {
		bases = append(bases, r)
	}
	for _, mf := range st.ManagedFiles {
		if !filepath.IsAbs(mf.Path) || mf.Hash == "" {
			continue
		}
		// The instance prefix is stripped as text, without cleaning first,
		// so a "..", "." or empty component reaches ResolveRecorded to be
		// refused rather than being cleaned into another path.
		base, rel, ok := underBase(mf.Path, bases)
		if !ok {
			continue
		}
		_, err := ResolveRecorded(Base{Instance: base}, rel)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// A generated file the agent deleted loses nothing.
			continue
		case err != nil:
			m.failures = append(m.failures, Check{Paths: []string{rel}, Evidence: "unreadable: managed file " + err.Error()})
			continue
		}
		// The hash is read later, from a descriptor for base, not through
		// the path ResolveRecorded checked: see readManagedFile.
		out[rel] = managedFile{hash: mf.Hash, base: base, rel: strings.TrimSuffix(rel, "/")}
	}
	return m
}

// underBase strips the first of bases that prefixes p, as text, and
// returns that base and the rest of p.
func underBase(p string, bases []string) (string, string, bool) {
	for _, base := range bases {
		if rel, ok := strings.CutPrefix(p, strings.TrimSuffix(base, "/")+"/"); ok {
			return base, rel, true
		}
	}
	return "", "", false
}

// checkOutside is the outside check: every path outside the repositories
// the walk found, in the instance and in the unit's job directory, gets one
// class, and a unique one keeps the unit. Every work tree and git
// directory the walk found is the repository check's, and a directory on
// another filesystem is kept by the walk; this check leaves all of them
// alone. It makes no walk of its own for repositories: without the
// repository check's walk it skips nothing, so everything counts here.
func checkOutside(ctx context.Context, u Unit, p Policy, s *evalState) []Check {
	skip := map[string]bool{}
	if s.walk != nil {
		skip = s.walk.skip
	}
	pats := append(append([]string(nil), ShippedDisposable...), p.Disposable...)
	gen := s.generated(u.InstancePath)

	var unique []string
	var unreadable []Check
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
			// An entry this walk can't read keeps the unit here, whatever
			// the repository check's walk saw: it may have turned
			// unreadable between the two.
			s.classes.add(slash, ClassUnique, "")
			unreadable = append(unreadable, Check{Paths: []string{slash}, Evidence: fmt.Sprintf("unreadable: %s: %s", slash, errText(err))})
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
			// Judged by the repository check, or kept by one of the
			// walk's rules.
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
	keeps = append(keeps, unreadable...)
	// A managed-files record the rule couldn't read keeps the unit here,
	// named, and the path it names counts as unique.
	for _, c := range s.managedFiles(u.InstancePath).failures {
		for _, p := range c.Paths {
			s.classes.add(p, ClassUnique, "")
		}
		keeps = append(keeps, c)
	}
	if len(unique) > 0 {
		keeps = append(keeps, Check{Paths: unique, Evidence: "files outside every repository: " + nameSome(unique)})
	}
	return append(keeps, checkJobDir(ctx, u, p, pats, s, skip)...)
}

// checkJobDir classes everything in the unit's job directory: Claude Code's
// own state files and empty directories are generated, a pattern makes a
// path disposable, and any other file is job scratch, disposable with the
// job scratch notice unless the workspace counts job scratch as unique. A
// job directory that is a link, or isn't a directory, keeps the unit. A
// repository in the job directory is the repository check's, and a
// directory on another filesystem the walk's: skip holds both.
func checkJobDir(ctx context.Context, u Unit, p Policy, pats []string, s *evalState, skip map[string]bool) []Check {
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
	// Job scratch is disposable only in a unit with no handoff brief, and
	// only while the workspace configuration says so. Until the records
	// check reads the brief, a brief that is present, or one the rule can't
	// rule out, makes job scratch unique.
	scratchUnique, why := p.JobScratchUnique, ""
	switch {
	case p.ConfigError != nil:
		scratchUnique, why = true, " (configuration unreadable)"
	case briefPossible(s):
		scratchUnique, why = true, " (a handoff brief may be present: "+keepRecord+")"
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
		if isDir && skip[path] {
			return filepath.SkipDir
		}
		if d.Name() == ".git" {
			// Judged by the repository check, or kept by one of the
			// walk's rules.
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
		case scratchUnique:
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
		keeps = append(keeps, Check{Paths: unique, Evidence: "job scratch counted as unique" + why + ": " + nameSome(unique)})
	}
	return keeps
}

// keepRecord is the keep record, which carries a unit's handoff brief.
const keepRecord = ".niwa/keep.json"

// briefPossible reports whether the unit may have a handoff brief: its keep
// record is present, or the rule can't tell, because .niwa or the record
// can't be read or .niwa isn't a real directory of the user's. The keep
// record is looked for relative to the .niwa the evaluation opened, without
// following a link.
func briefPossible(s *evalState) bool {
	if s.niwa.absent {
		return false
	}
	present, _ := s.niwa.present(path.Base(keepRecord))
	return present
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
