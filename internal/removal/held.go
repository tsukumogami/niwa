package removal

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// maxConfirmTips bounds how many tips the ancestry confirmation tries for one
// commit the pre-filter found reachable. Past it the commit is left unheld,
// which keeps.
const maxConfirmTips = 64

// heldOutcome is what the held check found in one repository: evidence for
// the errors bucket (a proof remote that couldn't be read, a scratch store
// that couldn't be used) and for the refs bucket (remotes that aren't proof
// remotes, commits and tags no proof remote holds). Both are empty when
// every ref is held.
type heldOutcome struct {
	errs, keeps []string
}

// proofAnswer is one proof remote that answered ls-remote.
type proofAnswer struct {
	pr   proofRemote
	refs []lsRef
	tips map[string]bool
}

// checkHeld decides which of a repository's refs a proof remote holds.
//
// A ref outside refs/tags/, or a worktree's HEAD, is held when its commit is
// itself a tip a proof remote lists, which reads no object; otherwise when,
// in a fresh scratch store holding the proof remotes' history, it is an
// ancestor of such a tip; otherwise when merge-tree of the proof remote's
// default branch and the commit, in the same store, is clean and yields the
// default branch's own tree (merged by content). A local tag is held only
// when a proof remote lists the same name under refs/tags/ at the same
// object. The store is removed on every path out.
//
// Remotes that aren't proof remotes and proof remotes that couldn't be read
// keep the unit only when some ref is left unheld.
func (e *Env) checkHeld(ctx context.Context, t repoTarget, o callOpts, refs []ref, ps proofSet) heldOutcome {
	var out heldOutcome
	if len(refs) == 0 {
		return out
	}
	if len(ps.remotes) == 0 {
		if len(ps.notes) == 0 {
			out.errs = []string{"clone URL unreachable: no clone URL for this repository"}
			return out
		}
		out.keeps = append(out.keeps, ps.notes...)
		return out
	}
	st, err := e.newStore()
	if err != nil {
		out.errs = []string{"scratch store unusable: " + err.Error()}
		return out
	}
	defer st.remove()

	var answers []proofAnswer
	var unreachable []string
	for _, pr := range ps.remotes {
		res, storeErr := e.remoteRefs(ctx, st, pr.url)
		switch {
		case storeErr != nil:
			out.errs = []string{"scratch store unusable: " + storeErr.Error()}
			return out
		case res.err != nil:
			unreachable = append(unreachable, unreachableText(pr, res.err))
		default:
			answers = append(answers, proofAnswer{pr: pr, refs: res.refs, tips: res.tips})
		}
	}

	var unheldTags []string
	var pending []ref
	for _, rf := range refs {
		if name, ok := strings.CutPrefix(rf.Name, "refs/tags/"); ok {
			if !tagHeld(answers, rf) {
				unheldTags = append(unheldTags, "tag "+name)
			}
			continue
		}
		if !liveTip(answers, rf.OID) {
			pending = append(pending, rf)
		}
	}
	var storeErrs []string
	if len(pending) > 0 && len(answers) > 0 {
		held, errs := e.heldInStore(ctx, st, t, o, answers, commitsOf(pending))
		storeErrs = errs
		var still []ref
		for _, rf := range pending {
			if !held[rf.OID] {
				still = append(still, rf)
			}
		}
		pending = still
	}
	if len(pending) == 0 && len(unheldTags) == 0 {
		return out
	}

	out.errs = append(append(out.errs, unreachable...), storeErrs...)
	out.keeps = append(out.keeps, ps.notes...)
	if len(answers) == 0 {
		// Nothing answered, so nothing can be said about which commits a
		// proof remote holds; the unreachable remotes are the evidence.
		return out
	}
	viaPrefix := false
	for _, a := range answers {
		viaPrefix = viaPrefix || a.pr.viaPrefix
	}
	if len(pending) > 0 {
		labels := make([]string, len(pending))
		for i, rf := range pending {
			labels[i] = refLabel(rf.Name)
		}
		what := "commits the clone URL does not show: "
		if viaPrefix {
			what = "commits no proof remote shows: "
		}
		out.keeps = append(out.keeps, what+nameSome(labels))
	}
	if len(unheldTags) > 0 {
		out.keeps = append(out.keeps, "local tags no proof remote lists at the same object: "+nameSome(unheldTags))
	}
	return out
}

// unreachableText words a proof remote that couldn't be read. A clone URL
// keeps the wording reap has always used.
func unreachableText(pr proofRemote, err error) string {
	if pr.viaPrefix {
		return "proof remote unreachable: " + err.Error()
	}
	return "clone URL unreachable: " + err.Error()
}

// liveTip reports whether id is an id some proof remote lists.
func liveTip(answers []proofAnswer, id string) bool {
	for _, a := range answers {
		if a.tips[id] {
			return true
		}
	}
	return false
}

// tagHeld reports whether some proof remote lists the tag's own name under
// refs/tags/ at the same object: the commit for a lightweight tag, the tag
// object for an annotated one.
func tagHeld(answers []proofAnswer, rf ref) bool {
	for _, a := range answers {
		for _, r := range a.refs {
			if r.Name == rf.Name && r.OID == rf.OID {
				return true
			}
		}
	}
	return false
}

// commitsOf is the distinct ids refs name, in a stable order.
func commitsOf(refs []ref) []string {
	seen := map[string]bool{}
	var out []string
	for _, rf := range refs {
		if !seen[rf.OID] {
			seen[rf.OID] = true
			out = append(out, rf.OID)
		}
	}
	sort.Strings(out)
	return out
}

// remoteRefs is a proof remote's ls-remote answer, made once per URL per
// pass against the scratch store. Before a call it checks the store, its
// config byte for byte included; a store that fails comes back as storeErr
// and isn't remembered as the URL's answer.
func (e *Env) remoteRefs(ctx context.Context, st *store, url string) (lsRemoteResult, error) {
	e.mu.Lock()
	res, ok := e.lsRemote[url]
	e.mu.Unlock()
	if ok {
		return res, nil
	}
	dir, err := st.useNetwork()
	if err != nil {
		return lsRemoteResult{}, err
	}
	return e.cachedLsRemote(ctx, url, func() ([]lsRef, error) {
		return e.lsRemoteRefs(ctx, st.dirs(), dir, url)
	}), nil
}

// heldInStore runs the checks that read objects, all of them in the scratch
// store: it fetches every proof remote's tips into the store, holds each
// pending commit that is an ancestor of one, then brings the rest in from
// the unit as a pack and holds each one merge-tree finds merged by content.
// It returns the commits it found held and why any step couldn't run, which
// keeps.
func (e *Env) heldInStore(ctx context.Context, st *store, t repoTarget, o callOpts, answers []proofAnswer, pending []string) (map[string]bool, []string) {
	held := map[string]bool{}
	var errs []string
	for _, a := range answers {
		err := st.fetch(ctx, a.pr.url, wantsOf(a.refs))
		switch {
		case errors.Is(err, errStoreCap):
			// The fetch was killed; nothing the store holds is trusted to
			// be complete, so nothing is held through it.
			return map[string]bool{}, append(errs, "scratch store unusable: "+err.Error())
		case err != nil:
			errs = append(errs, unreachableText(a.pr, fmt.Errorf("fetching its history: %v", err)))
		}
	}

	ids := append([]string(nil), pending...)
	for _, a := range answers {
		for _, r := range a.refs {
			ids = append(ids, r.OID)
		}
	}
	types, err := st.objectTypes(ctx, ids)
	if err != nil {
		return held, append(errs, "scratch store unusable: "+err.Error())
	}
	tips := orderedCommitTips(answers, types)

	var rest []string
	for _, c := range pending {
		if types[c] != "commit" {
			rest = append(rest, c)
			continue
		}
		ok, err := st.isAncestorOfSome(ctx, c, tips)
		if err != nil {
			errs = append(errs, "scratch store unusable: "+err.Error())
		}
		if ok {
			held[c] = true
		} else {
			rest = append(rest, c)
		}
	}
	if len(rest) == 0 {
		return held, errs
	}

	var defaults []string
	for _, a := range answers {
		for _, r := range a.refs {
			if r.Name == "HEAD" && types[r.OID] == "commit" {
				defaults = append(defaults, r.OID)
			}
		}
	}
	if len(defaults) == 0 {
		// No default branch is known, so nothing can be merged by content.
		return held, errs
	}
	if err := e.packInto(ctx, st, t, o, rest, defaults); err != nil {
		return held, append(errs, "scratch store unusable: "+err.Error())
	}
	for _, c := range rest {
		for _, d := range defaults {
			merged, err := st.mergedByContent(ctx, d, c)
			if err != nil {
				errs = append(errs, "scratch store unusable: "+err.Error())
				break
			}
			if merged {
				held[c] = true
				break
			}
		}
	}
	return held, errs
}

// wantsOf is the ids a fetch asks a proof remote for: every ref it listed,
// never a peeled "^{}" line, which isn't a ref the remote advertises.
func wantsOf(refs []lsRef) []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range refs {
		if strings.HasSuffix(r.Name, "^{}") || seen[r.OID] {
			continue
		}
		seen[r.OID] = true
		out = append(out, r.OID)
	}
	return out
}

// orderedCommitTips is every id the proof remotes list that names a commit
// in the store: each remote's HEAD first, then its branches, then the rest,
// so the confirmation usually ends at the first try.
func orderedCommitTips(answers []proofAnswer, types map[string]string) []string {
	rank := func(name string) int {
		switch {
		case name == "HEAD":
			return 0
		case strings.HasPrefix(name, "refs/heads/"):
			return 1
		}
		return 2
	}
	type tip struct {
		id   string
		rank int
	}
	var all []tip
	seen := map[string]bool{}
	for _, a := range answers {
		for _, r := range a.refs {
			if types[r.OID] != "commit" || seen[r.OID] {
				continue
			}
			seen[r.OID] = true
			all = append(all, tip{r.OID, rank(r.Name)})
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].rank < all[j].rank })
	out := make([]string, len(all))
	for i, t := range all {
		out[i] = t.id
	}
	return out
}

// storeCall builds a git call against the store, from the store's empty
// run directory, after checking both.
func (st *store) storeCall(ctx context.Context, network bool, o callOpts, args ...string) (*exec.Cmd, error) {
	var dir string
	var err error
	if network {
		dir, err = st.useNetwork()
	} else {
		dir, err = st.use()
	}
	if err != nil {
		return nil, err
	}
	o.dir = dir
	return st.e.gitCommand(ctx, st.dirs(), o, args...)
}

// fetch brings the history of wants from url into the store, under the
// network timeout and the store's size cap. The store has no refs, so the
// fetch offers the remote nothing and the remote sends each want's whole
// history, every object of it hashed from its content as git indexes it.
func (st *store) fetch(ctx context.Context, url string, wants []string) error {
	if len(wants) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, networkTimeout)
	defer cancel()
	ctx, stop := st.capped(ctx)
	cmd, err := st.storeCall(ctx, true, callOpts{config: st.e.networkConfig()},
		"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--no-auto-maintenance",
		"--no-recurse-submodules", "--stdin", "--", url)
	if err != nil {
		stop()
		return err
	}
	cmd.Stdin = strings.NewReader(strings.Join(wants, "\n") + "\n")
	_, err = run(cmd)
	if stop() {
		return capError()
	}
	return err
}

// errStoreCap is a call killed because the store grew past storeCap.
var errStoreCap = errors.New("the scratch store grew past its cap")

func capError() error {
	return fmt.Errorf("%w of %d bytes", errStoreCap, storeCap)
}

// objectTypes reads the type of each id in the store with one cat-file
// call: "commit", "tag", "tree", "blob", or "missing".
func (st *store) objectTypes(ctx context.Context, ids []string) (map[string]string, error) {
	types := map[string]string{}
	if len(ids) == 0 {
		return types, nil
	}
	cmd, err := st.storeCall(ctx, false, callOpts{}, "cat-file", "--batch-check=%(objectname) %(objecttype)")
	if err != nil {
		return nil, err
	}
	cmd.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	out, err := run(cmd)
	if err != nil {
		return nil, fmt.Errorf("reading object types: %v", err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(out), "\n"), "\n") {
		id, kind, ok := strings.Cut(line, " ")
		if !ok || !isObjectID(id) {
			return nil, fmt.Errorf("reading object types: a line does not parse: %q", line)
		}
		types[id] = kind
	}
	return types, nil
}

// isAncestorOfSome reports whether commit c is an ancestor of one of tips.
// A rev-list pre-filter asks whether any tip reaches it at all, and
// merge-base --is-ancestor then confirms it against one tip.
func (st *store) isAncestorOfSome(ctx context.Context, c string, tips []string) (bool, error) {
	if len(tips) == 0 {
		return false, nil
	}
	cmd, err := st.storeCall(ctx, false, callOpts{}, "rev-list", "--max-count=1", "--stdin")
	if err != nil {
		return false, err
	}
	var in strings.Builder
	in.WriteString(c + "\n")
	for _, tip := range tips {
		in.WriteString("^" + tip + "\n")
	}
	cmd.Stdin = strings.NewReader(in.String())
	out, err := run(cmd)
	if err != nil {
		return false, fmt.Errorf("walking the proof remote's history: %v", err)
	}
	if len(bytes.TrimSpace(out)) > 0 {
		// Some commit reachable from c is reachable from no tip, c itself.
		return false, nil
	}
	for i, tip := range tips {
		if i == maxConfirmTips {
			break
		}
		code, err := st.exitOf(ctx, "merge-base", "--is-ancestor", c, tip)
		if err != nil {
			return false, err
		}
		if code == 0 {
			// The answer rests on the commits from tip down to c; each is
			// re-hashed before it counts (verifyObjects).
			chain, err := st.ancestryPath(ctx, c, tip)
			if err != nil {
				return false, err
			}
			if err := st.verifyObjects(ctx, append(chain, c)); err != nil {
				return false, err
			}
			return true, nil
		}
	}
	return false, nil
}

// ancestryPath lists the commits on a path from tip down to c, c excluded:
// every commit that descends from c and that tip reaches.
func (st *store) ancestryPath(ctx context.Context, c, tip string) ([]string, error) {
	cmd, err := st.storeCall(ctx, false, callOpts{}, "rev-list", "--ancestry-path", c+".."+tip)
	if err != nil {
		return nil, err
	}
	out, err := run(cmd)
	if err != nil {
		return nil, fmt.Errorf("walking the proof remote's history: %v", err)
	}
	var ids []string
	for _, id := range strings.Fields(string(out)) {
		if !isObjectID(id) {
			return nil, fmt.Errorf("walking the proof remote's history: a line does not parse: %q", id)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// verifyObjects reads each object's raw content from the store and hashes
// it, refusing one whose content doesn't hash to its name. git checks the
// hash of a commit it is handed by name, but a walk reads each parent by
// the id its child names without checking it, so an object planted in the
// store under a real commit's id would otherwise be believed. The store is
// checked before each use against the same person-level tampering; the
// objects a decision rests on are checked here the same way.
func (st *store) verifyObjects(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	cmd, err := st.storeCall(ctx, false, callOpts{}, "cat-file", "--batch")
	if err != nil {
		return err
	}
	cmd.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	out, err := run(cmd)
	if err != nil {
		return fmt.Errorf("reading objects to verify: %v", err)
	}
	for _, id := range ids {
		header, rest, ok := bytes.Cut(out, []byte("\n"))
		if !ok {
			return errors.New("reading objects to verify: the listing ended early")
		}
		fields := strings.Fields(string(header))
		if len(fields) != 3 || fields[0] != id {
			return fmt.Errorf("reading objects to verify: %s is missing or unreadable", id)
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 || size+1 > len(rest) || rest[size] != '\n' {
			return fmt.Errorf("reading objects to verify: %s does not parse", id)
		}
		if got := objectHash(len(id), fields[1], rest[:size]); got != id {
			return fmt.Errorf("an object in the scratch store does not hash to its id: %s", id)
		}
		out = rest[size+1:]
	}
	return nil
}

// objectHash is git's id for an object of kind with body: SHA-1 or SHA-256
// of "<kind> <size>\0<body>", picked by the id's length.
func objectHash(idLen int, kind string, body []byte) string {
	header := fmt.Sprintf("%s %d\x00", kind, len(body))
	if idLen == 64 {
		h := sha256.New()
		h.Write([]byte(header))
		h.Write(body)
		return hex.EncodeToString(h.Sum(nil))
	}
	h := sha1.New()
	h.Write([]byte(header))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// exitOf runs a store call whose answer is its exit status: 0 yes, 1 no.
// Anything else is an error.
func (st *store) exitOf(ctx context.Context, args ...string) (int, error) {
	cmd, err := st.storeCall(ctx, false, callOpts{}, args...)
	if err != nil {
		return 0, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return 1, nil
	}
	msg := lastLine(stderr.String())
	if msg == "" {
		msg = err.Error()
	}
	return 0, fmt.Errorf("%s: %s", args[0], msg)
}

// packInto brings commits and their history from the unit into the store:
// pack-objects in the unit, through gitCommand, piped into index-pack in the
// store, which hashes every object from its content and refuses one whose id
// the store already holds with other content. A default tip the unit holds
// as a commit is passed as a negative, so its history, already fetched, isn't
// packed again; if the unit's object under that id isn't the real one, the
// pack misses objects merge-tree needs, which fails and keeps.
func (e *Env) packInto(ctx context.Context, st *store, t repoTarget, o callOpts, commits, defaults []string) error {
	var in strings.Builder
	for _, c := range commits {
		in.WriteString(c + "\n")
	}
	for _, d := range defaults {
		if e.unitHasCommit(ctx, t, o, d) {
			in.WriteString("^" + d + "\n")
		}
	}
	ctx, stop := st.capped(ctx)
	pack, err := e.gitCommand(ctx, t.Dirs, o, "pack-objects", "--revs", "--stdout", "-q")
	if err != nil {
		stop()
		return err
	}
	index, err := st.storeCall(ctx, false, callOpts{}, "index-pack", "--stdin")
	if err != nil {
		stop()
		return err
	}
	pack.Stdin = strings.NewReader(in.String())
	r, w, err := os.Pipe()
	if err != nil {
		stop()
		return err
	}
	var packErr, indexErr bytes.Buffer
	pack.Stdout, pack.Stderr = w, &packErr
	index.Stdin, index.Stdout, index.Stderr = r, nil, &indexErr
	if err := index.Start(); err != nil {
		r.Close()
		w.Close()
		stop()
		return err
	}
	r.Close()
	startErr := pack.Start()
	w.Close()
	var pErr error
	if startErr == nil {
		pErr = pack.Wait()
	}
	iErr := index.Wait()
	exceeded := stop()
	switch {
	case exceeded:
		return capError()
	case startErr != nil:
		return startErr
	case pErr != nil:
		return fmt.Errorf("packing the unit's commits: %s", firstNonEmpty(lastLine(packErr.String()), pErr.Error()))
	case iErr != nil:
		return fmt.Errorf("indexing the unit's commits: %s", firstNonEmpty(lastLine(indexErr.String()), iErr.Error()))
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// unitHasCommit reports whether the unit's repository holds id as a commit.
// It reads one object header and changes nothing.
func (e *Env) unitHasCommit(ctx context.Context, t repoTarget, o callOpts, id string) bool {
	cmd, err := e.gitCommand(ctx, t.Dirs, o, "cat-file", "-t", id)
	if err != nil {
		return false
	}
	out, err := run(cmd)
	return err == nil && strings.TrimSpace(string(out)) == "commit"
}

// mergedByContent reports whether merge-tree --write-tree of the default
// branch's tip d and commit c, in the store, exits 0 and yields d's own tree:
// what a squash merge of c leaves. The trees it writes land in the store.
func (st *store) mergedByContent(ctx context.Context, d, c string) (bool, error) {
	// merge-tree finds its merge bases by walking both histories, so the
	// commits from each base up to d and to c are re-hashed first, as the
	// ancestry check's are. Histories with no base aren't merged anyway.
	cmd, err := st.storeCall(ctx, false, callOpts{}, "merge-base", "--all", d, c)
	if err != nil {
		return false, err
	}
	out, err := run(cmd)
	if err != nil {
		return false, nil
	}
	verify := []string{d, c}
	for _, base := range strings.Fields(string(out)) {
		if !isObjectID(base) {
			return false, fmt.Errorf("finding the merge base: a line does not parse: %q", base)
		}
		verify = append(verify, base)
		for _, end := range []string{d, c} {
			chain, err := st.ancestryPath(ctx, base, end)
			if err != nil {
				return false, err
			}
			verify = append(verify, chain...)
		}
	}
	if err := st.verifyObjects(ctx, verify); err != nil {
		return false, err
	}
	cmd, err = st.storeCall(ctx, false, callOpts{}, "merge-tree", "--write-tree", d, c)
	if err != nil {
		return false, err
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		// A conflict exits 1, and unrelated histories or a missing object
		// fail too; none of them is merged.
		return false, nil
	}
	tree, _, _ := strings.Cut(strings.TrimSpace(stdout.String()), "\n")
	if !isObjectID(tree) {
		return false, nil
	}
	cmd, err = st.storeCall(ctx, false, callOpts{}, "rev-parse", "--verify", "--end-of-options", d+"^{tree}")
	if err != nil {
		return false, err
	}
	out, err = run(cmd)
	if err != nil {
		return false, fmt.Errorf("reading the default branch's tree: %v", err)
	}
	return strings.TrimSpace(string(out)) == tree, nil
}
