package removal

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// heldFixture is a fixture whose remote's HEAD names main, so ls-remote
// lists a default branch, with main checked out in the clone, and a second
// clone, other, that stands for everyone else pushing to the remote.
type heldFixture struct {
	fixture
	remote, other string
}

func newHeldFixture(t *testing.T) heldFixture {
	t.Helper()
	f := newFixture(t)
	h := heldFixture{fixture: f, remote: strings.TrimPrefix(f.remoteURL, "file://")}
	gitRun(t, h.remote, "symbolic-ref", "HEAD", "refs/heads/main")
	gitRun(t, f.clone, "checkout", "-q", "main")
	h.other = filepath.Join(filepath.Dir(filepath.Dir(h.remote)), "other")
	gitRun(t, filepath.Dir(h.other), "clone", "-q", f.remoteURL, h.other)
	return h
}

// commitFile writes name in dir with body and commits it.
func commitFile(t *testing.T, dir, name, body, msg string) string {
	t.Helper()
	writeFile(t, filepath.Join(dir, name), body)
	gitRun(t, dir, "add", name)
	gitRun(t, dir, "commit", "-q", "-m", msg)
	return gitRun(t, dir, "rev-parse", "HEAD")
}

// storesLeft lists the scratch stores left in the pass directory.
func storesLeft(t *testing.T, p Policy) []string {
	t.Helper()
	entries, err := os.ReadDir(p.Env.Scratch.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "store-") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestHeldByAncestry(t *testing.T) {
	h := newHeldFixture(t)
	commitFile(t, h.other, "more.txt", "m\n", "more")
	gitRun(t, h.other, "push", "-q", "origin", "main")
	// The clone's main and origin/main now sit behind the remote's tip.
	p := h.policy(t)
	v := Evaluate(context.Background(), h.unit(), p)
	if v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want an ancestor of a live tip held", v.Kind, v.Checks)
	}
	if left := storesLeft(t, p); len(left) != 0 {
		t.Errorf("scratch stores left after the pass's evaluation: %v", left)
	}
}

func TestHeldByContentAfterASquashMerge(t *testing.T) {
	h := newHeldFixture(t)
	gitRun(t, h.clone, "checkout", "-q", "-b", "feature")
	commitFile(t, h.clone, "feature.txt", "f\n", "feature work")
	// The same change lands on the remote's main as a different commit.
	commitFile(t, h.other, "feature.txt", "f\n", "squash of feature")
	gitRun(t, h.other, "push", "-q", "origin", "main")

	before := snapshot(t, filepath.Join(h.clone, ".git"))
	p := h.policy(t)
	v := Evaluate(context.Background(), h.unit(), p)
	if v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want a branch merged by content held", v.Kind, v.Checks)
	}
	if after := snapshot(t, filepath.Join(h.clone, ".git")); after != before {
		t.Error("the merged-by-content check changed the clone's .git")
	}
	if left := storesLeft(t, p); len(left) != 0 {
		t.Errorf("scratch stores left: %v", left)
	}
}

func TestHeldByContentNeedsACleanMerge(t *testing.T) {
	h := newHeldFixture(t)
	gitRun(t, h.clone, "checkout", "-q", "-b", "feature")
	commitFile(t, h.clone, "README", "mine\n", "feature work")
	commitFile(t, h.other, "README", "theirs\n", "conflicting change")
	gitRun(t, h.other, "push", "-q", "origin", "main")

	v := Evaluate(context.Background(), h.unit(), h.policy(t))
	c := keepWith(t, v, "tools/app: ", "commits the clone URL does not show", "branch feature")
	if c.Name != CheckRepository {
		t.Errorf("kept by %s; want the repository check", c.Name)
	}
}

func TestHeldKeepsACommitWithAChangeNobodyHas(t *testing.T) {
	h := newHeldFixture(t)
	gitRun(t, h.clone, "checkout", "-q", "-b", "feature")
	commitFile(t, h.clone, "feature.txt", "f\n", "feature work")
	p := h.policy(t)
	v := Evaluate(context.Background(), h.unit(), p)
	keepWith(t, v, "commits the clone URL does not show", "branch feature", "HEAD")
	if left := storesLeft(t, p); len(left) != 0 {
		t.Errorf("scratch stores left: %v", left)
	}
}

func TestHeldTagsNeedTheSameNameAtTheSameObject(t *testing.T) {
	h := newHeldFixture(t)
	gitRun(t, h.clone, "tag", "v1")
	v := Evaluate(context.Background(), h.unit(), h.policy(t))
	keepWith(t, v, "local tags no proof remote lists at the same object", "tag v1")

	gitRun(t, h.clone, "push", "-q", "origin", "v1")
	if v := Evaluate(context.Background(), h.unit(), h.policy(t)); v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want a pushed tag held", v.Kind, v.Checks)
	}

	// The remote's v1 becomes an annotated tag, another object, while the
	// clone's stays lightweight: the commit it names is held, the tag isn't.
	gitRun(t, h.other, "fetch", "-q", "origin", "--tags")
	gitRun(t, h.other, "tag", "-f", "-a", "-m", "annotated", "v1", "v1")
	gitRun(t, h.other, "push", "-q", "-f", "origin", "v1")
	v = Evaluate(context.Background(), h.unit(), h.policy(t))
	keepWith(t, v, "tag v1")
	noKeepWith(t, v, "commits the clone URL does not show")
}

func TestHeldNeverReadsTheClonesHistory(t *testing.T) {
	cases := []struct {
		name   string
		forge  func(t *testing.T, h heldFixture, unpushed, tip string)
		fetchd bool
	}{
		{"replace ref", func(t *testing.T, h heldFixture, unpushed, tip string) {
			child := forgedChild(t, h.clone, unpushed)
			gitRun(t, h.clone, "replace", tip, child)
		}, true},
		{"grafts", func(t *testing.T, h heldFixture, unpushed, tip string) {
			writeFile(t, filepath.Join(h.clone, ".git", "info", "grafts"), tip+" "+unpushed+"\n")
		}, true},
		{"forged commit-graph", func(t *testing.T, h heldFixture, unpushed, tip string) {
			forgeCommitGraph(t, h.clone, tip, unpushed)
		}, true},
		{"forged loose object", func(t *testing.T, h heldFixture, unpushed, tip string) {
			writeLooseObject(t, h.clone, tip, forgedCommitBody(t, h.clone, unpushed))
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHeldFixture(t)
			gitRun(t, h.clone, "checkout", "-q", "-b", "feature")
			unpushed := commitFile(t, h.clone, "feature.txt", "f\n", "feature work")
			tip := commitFile(t, h.other, "other.txt", "o\n", "someone else's work")
			gitRun(t, h.other, "push", "-q", "origin", "main")
			if c.fetchd {
				gitRun(t, h.clone, "fetch", "-q", "origin")
			}
			c.forge(t, h, unpushed, tip)
			v := Evaluate(context.Background(), h.unit(), h.policy(t))
			keepWith(t, v, "commits the clone URL does not show", "branch feature")
		})
	}
}

// TestHeldForgeriesWorkInTheClone checks the forgeries above do what they
// claim inside the clone itself, so the test above proves the rule never
// reads them rather than that they were inert.
func TestHeldForgeriesWorkInTheClone(t *testing.T) {
	h := newHeldFixture(t)
	gitRun(t, h.clone, "checkout", "-q", "-b", "feature")
	unpushed := commitFile(t, h.clone, "feature.txt", "f\n", "feature work")
	tip := commitFile(t, h.other, "other.txt", "o\n", "someone else's work")
	gitRun(t, h.other, "push", "-q", "origin", "main")
	gitRun(t, h.clone, "fetch", "-q", "origin")
	forgeCommitGraph(t, h.clone, tip, unpushed)
	cmd := exec.Command("git", "-c", "core.commitGraph=true", "rev-list", tip)
	cmd.Dir = h.clone
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.Output()
	if err != nil || !strings.Contains(string(out), unpushed) {
		t.Fatalf("the forged commit-graph doesn't make the tip descend from the unpushed commit: %v\n%s", err, out)
	}
}

// forgedCommitBody is a commit whose parent is parent, with parent's tree.
func forgedCommitBody(t *testing.T, dir, parent string) []byte {
	t.Helper()
	tree := gitRun(t, dir, "rev-parse", parent+"^{tree}")
	return []byte("tree " + tree + "\nparent " + parent +
		"\nauthor t <t@example.com> 1700000000 +0000\ncommitter t <t@example.com> 1700000000 +0000\n\nforged\n")
}

// forgedChild writes a real commit whose parent is parent and returns its id.
func forgedChild(t *testing.T, dir, parent string) string {
	t.Helper()
	tree := gitRun(t, dir, "rev-parse", parent+"^{tree}")
	return gitRun(t, dir, "commit-tree", tree, "-p", parent, "-m", "forged child")
}

// writeLooseObject writes body as a loose commit object under id, whatever
// id its content hashes to.
func writeLooseObject(t *testing.T, dir, id string, body []byte) {
	t.Helper()
	writeObjectIn(t, filepath.Join(dir, ".git", "objects"), id, body)
}

// writeObjectIn writes body as a loose commit object under id in the object
// directory objects.
func writeObjectIn(t *testing.T, objects, id string, body []byte) {
	t.Helper()
	writeKindIn(t, objects, id, "commit", body)
}

// writeKindIn writes body as a loose object of kind under id in the object
// directory objects.
func writeKindIn(t *testing.T, objects, id, kind string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	z := zlib.NewWriter(&buf)
	fmt.Fprintf(z, "%s %d\x00", kind, len(body))
	z.Write(body)
	z.Close()
	p := filepath.Join(objects, id[:2], id[2:])
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o444); err != nil {
		t.Fatal(err)
	}
}

// forgeCommitGraph writes the clone's commit-graph and rewrites the entry
// for child so its first parent is parent.
func forgeCommitGraph(t *testing.T, dir, child, parent string) {
	t.Helper()
	gitRun(t, dir, "-c", "commitGraph.generationVersion=1", "commit-graph", "write", "--reachable", "--no-progress")
	path := filepath.Join(dir, ".git", "objects", "info", "commit-graph")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data[:4]) != "CGPH" {
		t.Fatalf("commit-graph header %q", data[:4])
	}
	hashLen := 20
	chunks := int(data[6])
	offsets := map[string]uint64{}
	for i := 0; i < chunks; i++ {
		at := 8 + 12*i
		offsets[string(data[at:at+4])] = binary.BigEndian.Uint64(data[at+4 : at+12])
	}
	oidl, cdat := offsets["OIDL"], offsets["CDAT"]
	if oidl == 0 || cdat == 0 {
		t.Fatalf("commit-graph chunks %v", offsets)
	}
	count := int(cdat-oidl) / hashLen
	pos := func(id string) int {
		raw, _ := hex.DecodeString(id)
		for i := 0; i < count; i++ {
			at := int(oidl) + i*hashLen
			if bytes.Equal(data[at:at+hashLen], raw) {
				return i
			}
		}
		t.Fatalf("%s isn't in the commit-graph", id)
		return -1
	}
	entry := int(cdat) + pos(child)*(hashLen+16)
	binary.BigEndian.PutUint32(data[entry+hashLen:], uint32(pos(parent)))
	binary.BigEndian.PutUint32(data[entry+hashLen+4:], 0x70000000)
	// The child's generation goes one past its new parent's, as a graph
	// git wrote would have it, so a reachability walk doesn't cut it off.
	parentEntry := int(cdat) + pos(parent)*(hashLen+16)
	parentGen := binary.BigEndian.Uint64(data[parentEntry+hashLen+8:]) >> 34
	genDate := binary.BigEndian.Uint64(data[entry+hashLen+8:])
	genDate = (parentGen+1)<<34 | genDate&(1<<34-1)
	binary.BigEndian.PutUint64(data[entry+hashLen+8:], genDate)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHeldJudgesAnUnnamedRepositoryByTheCloneURLItsRemoteEquals(t *testing.T) {
	h := newHeldFixture(t)
	other := filepath.Join(h.instance, "extra", "other")
	gitRun(t, h.instance, "clone", "-q", h.remoteURL, other)
	v := Evaluate(context.Background(), h.unit(), h.policy(t))
	if v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want a fully pushed clone at extra/other held", v.Kind, v.Checks)
	}

	commitFile(t, other, "work.txt", "w\n", "work")
	v = Evaluate(context.Background(), h.unit(), h.policy(t))
	keepWith(t, v, "extra/other: ", "commits the clone URL does not show", "branch main")
}

func TestHeldJudgesABareRepositoryByTheCloneURLItsRemoteEquals(t *testing.T) {
	h := newHeldFixture(t)
	bare := filepath.Join(h.instance, "keep.git")
	gitRun(t, h.instance, "clone", "-q", "--bare", h.remoteURL, bare)
	p := h.policy(t)
	p.CloneURLs = []string{h.remoteURL}
	p.CloneURL = func(string) (string, bool) { return "", false }
	// The clone at tools/app is unnamed too now, and judged the same way.
	if v := Evaluate(context.Background(), h.unit(), p); v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want a pushed bare repository held through a configured clone URL", v.Kind, v.Checks)
	}
	commitFile(t, h.clone, "work.txt", "w\n", "work")
	gitRun(t, h.clone, "push", "-q", bare, "main:refs/heads/local-only")
	v := Evaluate(context.Background(), h.unit(), p)
	keepWith(t, v, "keep.git: ", "commits the clone URL does not show", "branch local-only")
}

// TestHeldKeepsABareRepositoryHoldingAnUnpushedStash pins how a repository
// with no work tree is judged: git stash list can't run there, so its stash
// reflog is read instead, and a stash keeps as it does in a work tree.
func TestHeldKeepsABareRepositoryHoldingAnUnpushedStash(t *testing.T) {
	h := newHeldFixture(t)
	bare := filepath.Join(h.instance, "keep.git")
	gitRun(t, h.instance, "clone", "-q", "--bare", h.remoteURL, bare)
	writeFile(t, filepath.Join(h.clone, "README"), "stashed work\n")
	gitRun(t, h.clone, "stash", "push", "-q")
	// A push refuses to create refs/stash, so the stash goes over under
	// another name and is moved there in the bare repository.
	stash := gitRun(t, h.clone, "rev-parse", "refs/stash")
	gitRun(t, h.clone, "push", "-q", bare, "refs/stash:refs/carry/stash")
	gitRun(t, h.instance, "--git-dir="+bare, "update-ref", "refs/stash", stash)
	gitRun(t, h.instance, "--git-dir="+bare, "update-ref", "-d", "refs/carry/stash")
	gitRun(t, h.clone, "stash", "drop", "-q")
	p := h.policy(t)
	p.CloneURLs = []string{h.remoteURL}
	v := Evaluate(context.Background(), h.unit(), p)
	// The ref was moved in with no reflog, so the ref itself names it.
	keepWith(t, v, "keep.git: ", "stashes: refs/stash")
	noKeepWith(t, v, "listing stashes")
	noKeepWith(t, v, "tools/app: ")
}

// TestHeldKeepsABareRepositoryWithAnOlderStashBehindAHeldOne pins that every
// stash in a repository with no work tree keeps, not only the newest: an
// older stash lives only in the refs/stash reflog, so a newest stash that a
// proof remote happens to show must not let it go.
func TestHeldKeepsABareRepositoryWithAnOlderStashBehindAHeldOne(t *testing.T) {
	h := newHeldFixture(t)
	bare := filepath.Join(h.instance, "keep.git")
	gitRun(t, h.instance, "clone", "-q", "--bare", h.remoteURL, bare)
	writeFile(t, filepath.Join(h.clone, "README"), "older stashed work\n")
	gitRun(t, h.clone, "stash", "push", "-q")
	older := gitRun(t, h.clone, "rev-parse", "refs/stash")
	writeFile(t, filepath.Join(h.clone, "README"), "newer stashed work\n")
	gitRun(t, h.clone, "stash", "push", "-q")
	newer := gitRun(t, h.clone, "rev-parse", "refs/stash")
	// The newest stash is pushed, so a proof remote lists it as a tip; the
	// older one is nobody else's.
	gitRun(t, h.clone, "push", "-q", "origin", newer+":refs/heads/stash-pushed")
	gitRun(t, h.clone, "push", "-q", bare, older+":refs/carry/older", newer+":refs/carry/newer")
	gitRun(t, h.instance, "--git-dir="+bare, "update-ref", "--create-reflog", "-m", "older", "refs/stash", older)
	gitRun(t, h.instance, "--git-dir="+bare, "update-ref", "--create-reflog", "-m", "newer", "refs/stash", newer)
	gitRun(t, h.instance, "--git-dir="+bare, "update-ref", "-d", "refs/carry/older")
	gitRun(t, h.instance, "--git-dir="+bare, "update-ref", "-d", "refs/carry/newer")
	gitRun(t, h.clone, "stash", "clear")
	p := h.policy(t)
	p.CloneURLs = []string{h.remoteURL}
	v := Evaluate(context.Background(), h.unit(), p)
	keepWith(t, v, "keep.git: ", "stashes: ", "stash@{1}")
	noKeepWith(t, v, "tools/app: ")
}

// TestHeldKeepsAStashReflogWithNoStashRef pins that a repository with no
// work tree and a logs/refs/stash but no refs/stash keeps.
func TestHeldKeepsAStashReflogWithNoStashRef(t *testing.T) {
	h := newHeldFixture(t)
	bare := filepath.Join(h.instance, "keep.git")
	gitRun(t, h.instance, "clone", "-q", "--bare", h.remoteURL, bare)
	head := gitRun(t, h.clone, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(bare, "logs", "refs", "stash"),
		strings.Repeat("0", len(head))+" "+head+" t <t@example.com> 1700000000 +0000\tstash\n")
	p := h.policy(t)
	p.CloneURLs = []string{h.remoteURL}
	v := Evaluate(context.Background(), h.unit(), p)
	keepWith(t, v, "keep.git: ", "stash reflog")
}

func TestHeldNeverContactsARePointedOrigin(t *testing.T) {
	h := newHeldFixture(t)
	// The agent points origin at a remote it controls, which holds the
	// clone's work; the configured clone URL is never contacted for it.
	agent := filepath.Join(filepath.Dir(h.remote), "agent.git")
	gitRun(t, h.clone, "init", "-q", "--bare", agent)
	gitRun(t, h.clone, "checkout", "-q", "-b", "feature")
	commitFile(t, h.clone, "feature.txt", "f\n", "feature work")
	gitRun(t, h.clone, "push", "-q", agent, "feature")
	gitRun(t, h.clone, "remote", "set-url", "origin", "file://"+agent)

	v := Evaluate(context.Background(), h.unit(), h.policy(t))
	keepWith(t, v, "remote not recognized: origin", "proof_remotes")
	noKeepWith(t, v, "clone URL unreachable")
}

func TestHeldNamesAnAddedRemoteOnlyWhenARefIsLeftUnheld(t *testing.T) {
	h := newHeldFixture(t)
	gitRun(t, h.clone, "remote", "add", "agent", "https://agent.example/repo")
	if v := Evaluate(context.Background(), h.unit(), h.policy(t)); v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want an added remote ignored while every ref is held", v.Kind, v.Checks)
	}
	gitRun(t, h.clone, "checkout", "-q", "-b", "feature")
	commitFile(t, h.clone, "feature.txt", "f\n", "feature work")
	v := Evaluate(context.Background(), h.unit(), h.policy(t))
	keepWith(t, v, "remote not recognized: agent")
	keepWith(t, v, "commits the clone URL does not show", "branch feature")
}

func TestHeldKeepsOnAStoreAlreadyAtItsPath(t *testing.T) {
	h := newHeldFixture(t)
	gitRun(t, h.clone, "checkout", "-q", "-b", "feature")
	commitFile(t, h.clone, "feature.txt", "f\n", "feature work")
	p := h.policy(t)
	// A store planted where the pass makes its first one, holding a forged
	// object under the remote tip's id whose parent is the unpushed commit.
	planted := filepath.Join(p.Env.Scratch.Dir, "store-1")
	gitRun(t, h.clone, "init", "-q", "--bare", planted)
	tip := gitRun(t, h.remote, "rev-parse", "main")
	unpushed := gitRun(t, h.clone, "rev-parse", "feature")
	writeObjectIn(t, filepath.Join(planted, "objects"), tip, forgedCommitBody(t, h.clone, unpushed))
	v := Evaluate(context.Background(), h.unit(), p)
	keepWith(t, v, "scratch store unusable", "file exists")
}

func TestHeldKeepsWhenTheStoreIsTamperedWith(t *testing.T) {
	cases := []struct {
		name   string
		tamper func(t *testing.T, dir, otherURL string)
		want   string
	}{
		{"config rewritten", func(t *testing.T, dir, otherURL string) {
			f, err := os.OpenFile(filepath.Join(dir, "config"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(f, "[url %q]\n\tinsteadOf = %s\n", otherURL, "file://")
			f.Close()
		}, "configuration is not the one niwa wrote"},
		{"commondir", func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "commondir"), "../elsewhere\n")
		}, "the store holds commondir"},
		{"alternates", func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "objects", "info", "alternates"), "/elsewhere/objects\n")
		}, "the store holds objects/info/alternates"},
		{"http-alternates", func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "objects", "info", "http-alternates"), "https://elsewhere/\n")
		}, "the store holds objects/info/http-alternates"},
		{"mode", func(t *testing.T, dir, _ string) {
			if err := os.Chmod(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}, "has mode 755"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHeldFixture(t)
			gitRun(t, h.clone, "checkout", "-q", "-b", "feature")
			commitFile(t, h.clone, "feature.txt", "f\n", "feature work")
			// A remote that holds the unpushed commit, which a rewritten
			// config could point the call at.
			decoy := filepath.Join(filepath.Dir(h.remote), "decoy.git")
			gitRun(t, h.clone, "init", "-q", "--bare", decoy)
			gitRun(t, h.clone, "push", "-q", decoy, "feature:main")
			afterStoreLayout = func(dir string) { c.tamper(t, dir, "file://"+decoy) }
			t.Cleanup(func() { afterStoreLayout = nil })
			p := h.policy(t)
			v := Evaluate(context.Background(), h.unit(), p)
			keepWith(t, v, "scratch store unusable", c.want)
			if left := storesLeft(t, p); len(left) != 0 {
				t.Errorf("scratch stores left: %v", left)
			}
		})
	}
}

// TestHeldRehashesTheObjectsTheAncestryWalkReads plants, in the scratch store
// right after niwa lays it out, a forged object under the id of the proof
// remote's tip whose parent is the unit's unpushed commit, and that commit
// itself. A fetch never overwrites an object the store already holds, so the
// walk reads the forgery; the commit must still keep, because every object
// the walk rests on is re-hashed and the forgery doesn't hash to its name.
func TestHeldRehashesTheObjectsTheAncestryWalkReads(t *testing.T) {
	h := newHeldFixture(t)
	gitRun(t, h.clone, "checkout", "-q", "-b", "feature")
	unpushed := commitFile(t, h.clone, "feature.txt", "f\n", "feature work")
	// git checks the hash of the commits it is handed by name, so the
	// forgery sits one commit below the remote's tip, where only a walk
	// reads it.
	inner := commitFile(t, h.other, "other.txt", "o\n", "someone else's work")
	commitFile(t, h.other, "more.txt", "m\n", "more of it")
	gitRun(t, h.other, "push", "-q", "origin", "main")
	// The unpushed commit's own objects go in genuine, so the forgery's
	// history is complete and the fetch's connectivity check passes.
	type object struct {
		id, kind string
		body     []byte
	}
	var genuine []object
	for _, id := range strings.Fields(gitRun(t, h.clone, "rev-list", "--objects", "--no-object-names", unpushed, "^origin/main")) {
		kind := gitRun(t, h.clone, "cat-file", "-t", id)
		cmd := exec.Command("git", "cat-file", kind, id)
		cmd.Dir = h.clone
		body, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		genuine = append(genuine, object{id, kind, body})
	}
	forged := forgedCommitBody(t, h.clone, unpushed)
	afterStoreLayout = func(dir string) {
		objects := filepath.Join(dir, "objects")
		for _, o := range genuine {
			writeKindIn(t, objects, o.id, o.kind, o.body)
		}
		writeObjectIn(t, objects, inner, forged)
	}
	t.Cleanup(func() { afterStoreLayout = nil })
	v := Evaluate(context.Background(), h.unit(), h.policy(t))
	keepWith(t, v, "commits the clone URL does not show", "branch feature")
	keepWith(t, v, "does not hash to its id")
}

func TestHeldKeepsWhenTheStoreGrowsPastItsCap(t *testing.T) {
	h := newHeldFixture(t)
	commitFile(t, h.other, "more.txt", "m\n", "more")
	gitRun(t, h.other, "push", "-q", "origin", "main")
	old := storeCap
	storeCap = 1
	t.Cleanup(func() { storeCap = old })
	p := h.policy(t)
	v := Evaluate(context.Background(), h.unit(), p)
	keepWith(t, v, "grew past")
	if left := storesLeft(t, p); len(left) != 0 {
		t.Errorf("scratch stores left: %v", left)
	}
}

func TestNetworkCallsCarryTheHelpersAndNoRedirects(t *testing.T) {
	e := &Env{Root: "/ws", Home: "/h", PATH: "/usr/bin", Git: "/usr/bin/git", credHelpers: []string{"osxkeychain", "store --file=/h/.creds"}}
	cmd, err := e.gitCommand(context.Background(), repoDirs{GitDir: "/s/store-1"}, callOpts{dir: "/s/net", config: e.networkConfig()}, "ls-remote", "--", "https://host/r")
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, "\x00")
	reset := strings.Index(args, "credential.helper=\x00")
	first := strings.Index(args, "credential.helper=osxkeychain")
	if reset < 0 || first < reset || !strings.Contains(args, "credential.helper=store --file=/h/.creds") {
		t.Errorf("helpers not added after the reset: %q", cmd.Args)
	}
	for _, want := range []string{"http.followRedirects=false", "http.proxy=", "http.sslVerify=true"} {
		if !strings.Contains(args, want) {
			t.Errorf("network call without %s: %q", want, cmd.Args)
		}
	}
}

func TestGitEnvCarriesTheCAOnlyWhenSet(t *testing.T) {
	e := &Env{Root: "/ws", Home: "/h", PATH: "/usr/bin", Git: "/usr/bin/git"}
	for _, kv := range e.gitEnv() {
		if strings.HasPrefix(kv, "GIT_SSL_CAINFO=") {
			t.Fatalf("CA set with no CA file: %q", kv)
		}
	}
	e.caFile = "/tmp/ca.pem"
	found := false
	for _, kv := range e.gitEnv() {
		found = found || kv == "GIT_SSL_CAINFO=/tmp/ca.pem"
	}
	if !found {
		t.Fatalf("CA file not passed: %q", e.gitEnv())
	}
}
