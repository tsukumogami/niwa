package mirror

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// LostRef is a ref in a borrower clone that can't be read after a restore:
// it's built on a commit the remote deleted after it was pushed, the mirror
// that kept the commit was lost, and the remote no longer serves it. Every
// name a remote or a person chose (Instance, Ref and each LostFiles path) is
// cut to printable ASCII with any other byte escaped as \xNN, and capped at
// 256 bytes, when the LostRef is built, so anything that prints one is safe.
type LostRef struct {
	// Instance is the borrower clone's path as the caller passed it in
	// Options.Instances.
	Instance string `json:"instance"`
	// Repo is the repo id of the mirror.
	Repo string `json:"repo"`
	// Ref is the full ref name, or HEAD for a detached HEAD.
	Ref string `json:"ref"`
	// MissingCommit is the first commit of the ref's history that neither
	// the borrower nor the mirror holds.
	MissingCommit string `json:"missing_commit"`
	// RemoteTracking is true for a ref under refs/remotes/: a stale copy of
	// a remote branch the user's git fetch --prune clears, not their work.
	RemoteTracking bool `json:"remote_tracking"`
	// OwnCommits, for a local branch only, are the commits read on the way
	// from the tip to MissingCommit: the branch's unpushed work, intact in
	// the borrower. At most ownCommitsMax, the first the walk read.
	OwnCommits []string `json:"own_commits"`
	// LostFiles, for a local branch only, are the paths in the tip's tree
	// whose blob or subtree neither the borrower nor the mirror can read. At
	// most lostFilesMax.
	LostFiles []LostFile `json:"lost_files"`
}

// LostFile is one path of a lost branch's tip tree that can't be read, and
// whether the clone's working tree holds a file at that path.
type LostFile struct {
	Path          string `json:"path"`
	InWorkingTree bool   `json:"in_working_tree"`
}

// nameMax caps every name a LostRef carries, in bytes, after escaping.
const nameMax = 256

// lostFilesMax caps how many lost paths one LostRef lists, and ownCommitsMax
// how many of its own commits. With every name capped at nameMax too, they
// bound the size of a LostRef and so of its unrecoverable log line.
const (
	lostFilesMax  = 100
	ownCommitsMax = 100
)

// parentWalkMax caps how many objects one ref's parent walk reads.
const parentWalkMax = 10000

// The bounds on recovering lost commits by id. They're variables so the
// tests can shorten them.
var (
	// recoverRounds is how many rounds of probe and fetch run. A final
	// probe follows the last fetch.
	recoverRounds = 2
	// recoverMaxIDs caps the distinct ids fetched across every round.
	recoverMaxIDs = 32
	// recoverFetchLimit bounds one fetch.
	recoverFetchLimit = 60 * time.Second
	// recoverTotalLimit bounds every fetch of one recovery together.
	recoverTotalLimit = 120 * time.Second
)

// fetchCommits fetches ids into store; the tests replace it.
var fetchCommits = fetchByID

// probeHook, when a test sets it, runs as recoverBorrowers starts, with the
// store it probes against. It's nil outside the tests.
var probeHook func(store string)

// cleanName cuts s to printable ASCII: each other byte, and the backslash,
// becomes \xNN, and the result stops before the escape or byte that would
// take it past nameMax bytes.
func cleanName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		piece := string(rune(c))
		if c < 0x20 || c >= 0x7f || c == '\\' {
			piece = fmt.Sprintf(`\x%02x`, c)
		}
		if b.Len()+len(piece) > nameMax {
			break
		}
		b.WriteString(piece)
	}
	return b.String()
}

// CleanText is s cut as the mirror log's strings and a LostRef's names are
// (cleanName): printable ASCII, with every other byte and the backslash
// escaped as \xNN, at most 256 bytes. A caller prints a Result's repo id and
// the text of its Err and LogErr through it, since git, the system or a
// remote can put anything in them.
func CleanText(s string) string { return cleanName(s) }

// borrowersOf returns the clones among instances whose alternates name the
// stable path's objects directory, in order and each once.
func borrowersOf(instances []string, stable string) []string {
	var out []string
	for _, clone := range instances {
		if !slices.Contains(out, clone) && hasBorrower([]string{clone}, stable) {
			out = append(out, clone)
		}
	}
	return out
}

// borrowerRef is one ref of a borrower the probe walks.
type borrowerRef struct{ name, id string }

// lostRef is a ref the probe found unreadable, with its names raw.
type lostRef struct {
	clone   string
	ref     borrowerRef
	missing string
	own     []string
}

// recoverBorrowers runs after a restore has put a store in place, under the
// lock the restore's caller holds. It probes each borrower for refs the new
// store can't serve, fetches the missing commits by id from r.URL (the
// config's URL as given), pins each one the store then holds as
// refs/niwa/kept/<sha>, and probes again. It's bounded: recoverRounds rounds
// of probe and fetch, recoverMaxIDs ids across them, recoverFetchLimit a
// fetch and recoverTotalLimit in all, a failed batch retried one id at a
// time inside the same budget. A probe after the last fetch alone decides
// what's lost, never a fetch's exit status.
//
// Nothing in a borrower is written: the probe runs for-each-ref, rev-list,
// cat-file and ls-tree there, all reads. When a fetch ran and the store's
// packs changed, the state file is written again with the new manifest, its
// timestamps kept, so the check covers the new pack.
func recoverBorrowers(ctx context.Context, mdir string, r Repo, borrowers []string) []LostRef {
	if len(borrowers) == 0 {
		return nil
	}
	store := filepath.Join(mdir, r.ID)
	if probeHook != nil {
		probeHook(store)
	}
	budget, cancel := context.WithTimeout(ctx, recoverTotalLimit)
	defer cancel()

	tried := map[string]bool{}
	fetched := false
	var lost []lostRef
	for round := 0; ; round++ {
		lost = probe(ctx, store, borrowers)
		if round == recoverRounds || r.URL == "" {
			break
		}
		var ids []string
		for _, l := range lost {
			if len(tried) >= recoverMaxIDs {
				break
			}
			if commitIDPattern.MatchString(l.missing) && !tried[l.missing] {
				tried[l.missing] = true
				ids = append(ids, l.missing)
			}
		}
		if len(ids) == 0 {
			break
		}
		fetchIDs(budget, store, r.URL, ids)
		fetched = true
		_ = writePins(ctx, store, heldCommits(ctx, store, ids))
	}
	if fetched {
		recordFetched(ctx, mdir, r)
	}

	var out []LostRef
	for _, l := range lost {
		out = append(out, l.report(ctx, store, r.ID))
	}
	return out
}

// fetchIDs fetches ids into store within budget: all at once, then, when
// that fails, one at a time while the budget lasts. Each fetch gets its own
// recoverFetchLimit.
func fetchIDs(budget context.Context, store, url string, ids []string) {
	if fetchOne(budget, store, url, ids) == nil || len(ids) == 1 {
		return
	}
	for _, id := range ids {
		if budget.Err() != nil {
			return
		}
		_ = fetchOne(budget, store, url, []string{id})
	}
}

// fetchOne runs one fetch of ids under recoverFetchLimit.
func fetchOne(budget context.Context, store, url string, ids []string) error {
	ctx, cancel := context.WithTimeout(budget, recoverFetchLimit)
	defer cancel()
	return fetchCommits(ctx, store, url, ids)
}

// recordFetched writes the state file again when the linked store's packs no
// longer match its manifest, keeping every other field. A failure leaves the
// state file as it was: the check ignores a pack the manifest doesn't list.
func recordFetched(ctx context.Context, mdir string, r Repo) {
	rec, err := readState(mdir, r.ID)
	if err != nil {
		return
	}
	if target, err := os.Readlink(filepath.Join(mdir, r.ID)); err != nil || target != rec.Store {
		return
	}
	packs, err := manifest(filepath.Join(mdir, rec.Store))
	if err != nil || slices.Equal(packs, rec.Packs) {
		return
	}
	next := rec
	next.Packs = packs
	stateTmp, err := writeStateTemp(mdir, next)
	if err != nil {
		return
	}
	if err := recordState(mdir, r.ID, stateTmp); err != nil {
		_ = os.Remove(stateTmp)
	}
}

// probe returns the refs of each borrower that can't be read to the root
// through the store.
func probe(ctx context.Context, store string, borrowers []string) []lostRef {
	tips, _ := storeTips(ctx, store)
	var lost []lostRef
	for _, clone := range borrowers {
		lost = append(lost, probeBorrower(ctx, store, clone, tips)...)
	}
	return lost
}

// storeTips lists the object every ref of the store names, the kept pins
// included: the probe's negative tips, so its walk covers only what the
// mirror doesn't hold.
func storeTips(ctx context.Context, store string) ([]string, error) {
	out, err := gitOutput(ctx, nil, "", "--git-dir="+store, "for-each-ref", "--format=%(objectname)")
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// probeBorrower walks every ref of one borrower with one rev-list over
// everything the store's tips don't reach; when that walk fails, it walks
// each distinct ref target alone, and a ref whose walk fails gets a parent
// walk to find the commit it lost.
func probeBorrower(ctx context.Context, store, clone string, tips []string) []lostRef {
	gitDir := filepath.Join(clone, ".git")
	refs := borrowerRefs(ctx, gitDir)
	if len(refs) == 0 {
		return nil
	}
	var ids []string
	for _, ref := range refs {
		if !slices.Contains(ids, ref.id) {
			ids = append(ids, ref.id)
		}
	}
	if walkReads(ctx, gitDir, ids, tips) {
		return nil
	}
	bad := map[string]bool{}
	for _, id := range ids {
		if !walkReads(ctx, gitDir, []string{id}, tips) {
			bad[id] = true
		}
	}
	var lost []lostRef
	walked := map[string]lostRef{}
	for _, ref := range refs {
		if !bad[ref.id] {
			continue
		}
		l, ok := walked[ref.id]
		if !ok {
			l.missing, l.own = parentWalk(ctx, store, gitDir, ref.id)
			walked[ref.id] = l
		}
		// A walk that fails with every commit readable lost a tree or blob
		// of a commit the store holds, which a store that passed its check
		// and fsck doesn't; there's no commit to fetch or name, so it isn't
		// reported.
		if l.missing == "" {
			continue
		}
		lost = append(lost, lostRef{clone: clone, ref: ref, missing: l.missing, own: l.own})
	}
	return lost
}

// borrowerRefs lists a borrower's branches, remote-tracking refs and tags,
// symbolic refs left out, plus HEAD when it's detached. for-each-ref lists a
// ref whose object is missing too.
func borrowerRefs(ctx context.Context, gitDir string) []borrowerRef {
	out, err := gitOutput(ctx, nil, "", "--git-dir="+gitDir, "for-each-ref",
		"--format=%(objectname)%00%(symref)%00%(refname)",
		"refs/heads/", "refs/remotes/", "refs/tags/")
	if err != nil {
		return nil
	}
	var refs []borrowerRef
	for _, line := range strings.Split(out, "\n") {
		fields := strings.SplitN(line, "\x00", 3)
		if len(fields) != 3 || fields[1] != "" || !commitIDPattern.MatchString(fields[0]) {
			continue
		}
		refs = append(refs, borrowerRef{name: fields[2], id: fields[0]})
	}
	if id := detachedHead(gitDir); id != "" {
		refs = append(refs, borrowerRef{name: "HEAD", id: id})
	}
	return refs
}

// detachedHead returns the commit a detached HEAD names, read from the file
// itself, or "" when HEAD is symbolic or can't be read.
func detachedHead(gitDir string) string {
	f, err := openRegular(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if commitIDPattern.MatchString(id) {
		return id
	}
	return ""
}

// walkReads reports whether every object reachable from ids and from none of
// tips can be read in the borrower. Both go on stdin, tips as negatives.
func walkReads(ctx context.Context, gitDir string, ids, tips []string) bool {
	var in strings.Builder
	for _, id := range ids {
		in.WriteString(id + "\n")
	}
	for _, tip := range tips {
		in.WriteString("^" + tip + "\n")
	}
	_, err := gitOutput(ctx, nil, in.String(), "--git-dir="+gitDir, "rev-list", "--objects", "--stdin")
	return err == nil
}

// objectType returns the type of id as gitDir reads it, or "" when it can't
// be read.
func objectType(ctx context.Context, gitDir, id string) string {
	out, err := gitOutput(ctx, nil, "", "--git-dir="+gitDir, "cat-file", "-t", id)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// headerIDs returns the ids on the lines of an object's header that start
// with key and a space, up to the blank line ending the header.
func headerIDs(body, key string) []string {
	var ids []string
	for _, line := range strings.Split(body, "\n") {
		if line == "" {
			break
		}
		if id, ok := strings.CutPrefix(line, key+" "); ok && commitIDPattern.MatchString(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// parentWalk walks a ref's history from tip, breadth first, stopping at any
// commit the store holds, whose history the store holds too. It returns the
// first object neither the borrower nor the store holds, and the commits it
// read on the way: the borrower's own. An annotated tag is followed to its
// object. It returns "" when every object it reached was readable.
func parentWalk(ctx context.Context, store, gitDir, tip string) (missing string, own []string) {
	queue := []string{tip}
	seen := map[string]bool{tip: true}
	for steps := 0; len(queue) > 0 && steps < parentWalkMax; steps++ {
		id := queue[0]
		queue = queue[1:]
		if objectType(ctx, store, id) != "" {
			continue
		}
		var next []string
		switch objectType(ctx, gitDir, id) {
		case "":
			return id, own
		case "tag":
			body, err := gitOutput(ctx, nil, "", "--git-dir="+gitDir, "cat-file", "tag", id)
			if err == nil {
				next = headerIDs(body, "object")
			}
		case "commit":
			own = append(own, id)
			body, err := gitOutput(ctx, nil, "", "--git-dir="+gitDir, "cat-file", "commit", id)
			if err == nil {
				next = headerIDs(body, "parent")
			}
		}
		for _, n := range next {
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return "", own
}

// report builds the LostRef for l, with its names cleaned. A local branch
// gets its own commits, the first ownCommitsMax the walk read, and its lost
// files; any other ref gets neither.
func (l lostRef) report(ctx context.Context, store, repoID string) LostRef {
	out := LostRef{
		Instance:       cleanName(l.clone),
		Repo:           repoID,
		Ref:            cleanName(l.ref.name),
		MissingCommit:  cleanName(l.missing),
		RemoteTracking: strings.HasPrefix(l.ref.name, "refs/remotes/"),
		OwnCommits:     []string{},
		LostFiles:      []LostFile{},
	}
	if strings.HasPrefix(l.ref.name, "refs/heads/") {
		out.OwnCommits = append(out.OwnCommits, l.own[:min(len(l.own), ownCommitsMax)]...)
		out.LostFiles = lostFiles(ctx, store, l.clone, l.ref.id)
	}
	return out
}

// treeEntry is one entry of a tree as ls-tree prints it.
type treeEntry struct{ kind, id, name string }

// lsTree lists one tree's entries, names raw (ls-tree -z quotes none).
func lsTree(ctx context.Context, gitDir, tree string) ([]treeEntry, error) {
	out, err := gitOutput(ctx, nil, "", "--git-dir="+gitDir, "ls-tree", "-z", "--full-tree", tree)
	if err != nil {
		return nil, err
	}
	var entries []treeEntry
	for _, rec := range strings.Split(out, "\x00") {
		meta, name, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 {
			continue
		}
		entries = append(entries, treeEntry{kind: f[1], id: f[2], name: name})
	}
	return entries, nil
}

// readable returns which of ids gitDir can read.
func readable(ctx context.Context, gitDir string, ids []string) map[string]bool {
	got := map[string]bool{}
	if len(ids) == 0 {
		return got
	}
	out, err := gitOutput(ctx, nil, strings.Join(ids, "\n")+"\n",
		"--git-dir="+gitDir, "cat-file", "--batch-check=%(objectname)")
	if err != nil {
		return got
	}
	for _, line := range strings.Split(out, "\n") {
		if commitIDPattern.MatchString(line) {
			got[line] = true
		}
	}
	return got
}

// lostFiles walks the tree of the commit tip in the borrower at clone and
// returns each path whose blob or subtree neither the borrower nor the store
// can read, with whether the working tree holds a file there. A subtree the
// store holds is complete and isn't walked. A tip whose commit can't be read
// has no tree to walk and gives none.
func lostFiles(ctx context.Context, store, clone, tip string) []LostFile {
	gitDir := filepath.Join(clone, ".git")
	files := []LostFile{}
	body, err := gitOutput(ctx, nil, "", "--git-dir="+gitDir, "cat-file", "commit", tip)
	if err != nil {
		return files
	}
	roots := headerIDs(body, "tree")
	if len(roots) != 1 {
		return files
	}
	type dir struct{ id, path string }
	queue := []dir{{id: roots[0]}}
	for steps := 0; len(queue) > 0 && steps < parentWalkMax && len(files) < lostFilesMax; steps++ {
		d := queue[0]
		queue = queue[1:]
		entries, err := lsTree(ctx, gitDir, d.id)
		if err != nil {
			continue
		}
		var ids []string
		for _, e := range entries {
			if e.kind == "blob" || e.kind == "tree" {
				ids = append(ids, e.id)
			}
		}
		inStore := readable(ctx, store, ids)
		anywhere := readable(ctx, gitDir, ids)
		for _, e := range entries {
			if e.kind != "blob" && e.kind != "tree" {
				continue
			}
			path := e.name
			if d.path != "" {
				path = d.path + "/" + e.name
			}
			switch {
			case !anywhere[e.id]:
				if len(files) < lostFilesMax {
					_, statErr := os.Lstat(filepath.Join(clone, filepath.FromSlash(path)))
					files = append(files, LostFile{Path: cleanName(path), InWorkingTree: statErr == nil})
				}
			case e.kind == "tree" && !inStore[e.id]:
				queue = append(queue, dir{id: e.id, path: path})
			}
		}
	}
	return files
}
