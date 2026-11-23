package removal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// checkedOut is a fixture whose clone has main checked out: the fixture's
// bare remote has no HEAD branch, so its clone starts with nothing checked
// out.
func checkedOut(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	gitRun(t, f.clone, "checkout", "-q", "main")
	return f
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// classFixture is a clean fixture whose clone ignores target/, .env and
// dist-ignored/, with the ignore file pushed, and whose instance carries the
// leftovers every class covers.
func classFixture(t *testing.T) fixture {
	t.Helper()
	f := checkedOut(t)
	writeFile(t, filepath.Join(f.clone, ".gitignore"), "target/\n.env\nfake-cache/\n")
	gitRun(t, f.clone, "add", ".gitignore")
	gitRun(t, f.clone, "commit", "-q", "-m", "ignore")
	gitRun(t, f.clone, "push", "-q", "origin", "HEAD")

	writeFile(t, filepath.Join(f.clone, "target", "CACHEDIR.TAG"), cacheDirSignature+"\n# made by a build\n")
	writeFile(t, filepath.Join(f.clone, "target", "debug", "app"), "binary")
	writeFile(t, filepath.Join(f.clone, "dist", "bundle.js"), "x")
	writeFile(t, filepath.Join(f.clone, ".env"), "A=1\n")
	writeFile(t, filepath.Join(f.clone, "pkg", "__pycache__", "m.pyc"), "c")
	writeFile(t, filepath.Join(f.instance, ".DS_Store"), "d")
	if err := os.MkdirAll(filepath.Join(f.instance, "empty", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	claude := "# context\n"
	writeFile(t, filepath.Join(f.instance, "CLAUDE.md"), claude)
	sum := sha256.Sum256([]byte(claude))
	state, err := json.Marshal(map[string]any{
		"managed_files": []map[string]string{{
			"path": filepath.Join(f.instance, "CLAUDE.md"),
			"hash": "sha256:" + hex.EncodeToString(sum[:]),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.instance, ".niwa", "instance.json"), string(state))
	writeFile(t, filepath.Join(f.instance, ".niwa", "dispatch-claude.out"), "log")
	writeFile(t, filepath.Join(f.instance, ".gitignore"), "*.local*\n.mcp.json\n")
	if err := os.MkdirAll(filepath.Join(f.instance, ".codex", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(f.instance, ".codex", "skills", "niwa")); err != nil {
		t.Fatal(err)
	}
	return f
}

func classPolicy(t *testing.T, f fixture) Policy {
	p := f.policy(t)
	p.BuildTrees = func(repo string) []string {
		if repo == "app" {
			return []string{"dist/"}
		}
		return nil
	}
	return p
}

func classOf(v Verdict, path string) (PathClass, bool) {
	for _, pc := range v.Paths {
		if pc.Path == path {
			return pc, true
		}
	}
	return PathClass{}, false
}

func wantClass(t *testing.T, v Verdict, path string, class Class, notice Notice) {
	t.Helper()
	pc, ok := classOf(v, path)
	if !ok {
		t.Errorf("no class for %s; paths %+v", path, v.Paths)
		return
	}
	if pc.Class != class || pc.Notice != notice {
		t.Errorf("%s classed %q notice %q, want %q notice %q", path, pc.Class, pc.Notice, class, notice)
	}
}

func TestEvaluateGivesEveryPathOneClass(t *testing.T) {
	f := classFixture(t)
	job := filepath.Join(t.TempDir(), "job")
	writeFile(t, filepath.Join(job, "state.json"), "{}")
	writeFile(t, filepath.Join(job, "timeline.jsonl"), "")
	writeFile(t, filepath.Join(job, "scratch.md"), "notes")
	u := f.unit()
	u.JobDir = job

	v := Evaluate(context.Background(), u, classPolicy(t, f))
	if v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want reap", v.Kind, v.Checks)
	}
	wantClass(t, v, "tools/app/README", ClassTracked, "")
	wantClass(t, v, "tools/app/.gitignore", ClassTracked, "")
	wantClass(t, v, "tools/app/.git/", ClassPushed, "")
	wantClass(t, v, "tools/app/target/", ClassBuildTree, "")
	wantClass(t, v, "tools/app/dist/", ClassBuildTree, "")
	wantClass(t, v, "tools/app/.env", ClassDisposable, NoticeGitIgnored)
	wantClass(t, v, "tools/app/pkg/__pycache__/m.pyc", ClassDisposable, "")
	wantClass(t, v, ".DS_Store", ClassDisposable, "")
	wantClass(t, v, "empty/", ClassGenerated, "")
	wantClass(t, v, "CLAUDE.md", ClassGenerated, "")
	wantClass(t, v, ".niwa/instance.json", ClassGenerated, "")
	wantClass(t, v, ".niwa/dispatch-claude.out", ClassGenerated, "")
	wantClass(t, v, ".gitignore", ClassGenerated, "")
	wantClass(t, v, ".codex/skills/", ClassGenerated, "")
	wantClass(t, v, "job:state.json", ClassGenerated, "")
	wantClass(t, v, "job:timeline.jsonl", ClassGenerated, "")
	wantClass(t, v, "job:scratch.md", ClassDisposable, NoticeJobScratch)
	seen := map[string]bool{}
	for _, pc := range v.Paths {
		if seen[pc.Path] {
			t.Errorf("%s classed twice", pc.Path)
		}
		seen[pc.Path] = true
	}
}

func TestEvaluateKeepsAUniqueFileOutsideEveryRepository(t *testing.T) {
	f := classFixture(t)
	writeFile(t, filepath.Join(f.instance, "results.csv"), "1,2\n")
	v := Evaluate(context.Background(), f.unit(), classPolicy(t, f))
	first, _ := v.First()
	if v.Kind != Keep || first.Name != CheckOutside || len(first.Paths) != 1 || first.Paths[0] != "results.csv" {
		t.Fatalf("verdict %s %+v; want the outside check naming results.csv", v.Kind, v.Checks)
	}
	wantClass(t, v, "results.csv", ClassUnique, "")
}

func TestEvaluateCountsAnEditedManagedFileAsUnique(t *testing.T) {
	f := classFixture(t)
	writeFile(t, filepath.Join(f.instance, "CLAUDE.md"), "# context\nmy notes\n")
	v := Evaluate(context.Background(), f.unit(), classPolicy(t, f))
	if v.Kind != Keep {
		t.Fatalf("verdict %s; want keep", v.Kind)
	}
	wantClass(t, v, "CLAUDE.md", ClassUnique, "")
}

func TestEvaluateCountsARootIgnoreFileWithProseAsUnique(t *testing.T) {
	f := classFixture(t)
	writeFile(t, filepath.Join(f.instance, ".gitignore"), "*.local*\nremember to ship the fix\n")
	v := Evaluate(context.Background(), f.unit(), classPolicy(t, f))
	wantClass(t, v, ".gitignore", ClassUnique, "")
}

func TestEvaluateCountsAnUnknownNiwaFileAsUnique(t *testing.T) {
	f := classFixture(t)
	writeFile(t, filepath.Join(f.instance, ".niwa", "notes.md"), "mine")
	v := Evaluate(context.Background(), f.unit(), classPolicy(t, f))
	if v.Kind != Keep {
		t.Fatalf("verdict %s; want keep", v.Kind)
	}
	wantClass(t, v, ".niwa/notes.md", ClassUnique, "")
}

func TestEvaluateWithIgnoredFilesUniqueKeepsAGitIgnoredFile(t *testing.T) {
	f := classFixture(t)
	p := classPolicy(t, f)
	p.IgnoredUnique = true
	v := Evaluate(context.Background(), f.unit(), p)
	first, _ := v.First()
	if v.Kind != Keep || first.Name != CheckRepository || !strings.Contains(first.Evidence, ".env") {
		t.Fatalf("verdict %s %+v; want the repository check naming .env", v.Kind, v.Checks)
	}
	wantClass(t, v, "tools/app/.env", ClassUnique, "")
	// A build tree and a pattern still win over the setting.
	wantClass(t, v, "tools/app/target/", ClassBuildTree, "")
	wantClass(t, v, "tools/app/pkg/__pycache__/m.pyc", ClassDisposable, "")
}

func TestEvaluateWithJobScratchUniqueKeepsJobScratch(t *testing.T) {
	f := classFixture(t)
	job := filepath.Join(t.TempDir(), "job")
	writeFile(t, filepath.Join(job, "scratch.md"), "notes")
	if err := os.MkdirAll(filepath.Join(job, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := f.unit()
	u.JobDir = job
	p := classPolicy(t, f)
	p.JobScratchUnique = true
	v := Evaluate(context.Background(), u, p)
	first, _ := v.First()
	if v.Kind != Keep || first.Name != CheckOutside || first.Paths[0] != "job:scratch.md" {
		t.Fatalf("verdict %s %+v; want the outside check naming job:scratch.md", v.Kind, v.Checks)
	}
	wantClass(t, v, "job:empty/", ClassGenerated, "")
}

func TestCacheDirTagNeedsTheSignatureAndAnIgnoredDirectory(t *testing.T) {
	f := classFixture(t)
	writeFile(t, filepath.Join(f.clone, "fake-cache", "CACHEDIR.TAG"), "Signature: wrong\n")
	writeFile(t, filepath.Join(f.clone, "unignored", "CACHEDIR.TAG"), cacheDirSignature+"\n")
	v := Evaluate(context.Background(), f.unit(), classPolicy(t, f))
	wantClass(t, v, "tools/app/fake-cache/", ClassDisposable, NoticeGitIgnored)
	wantClass(t, v, "tools/app/unignored/CACHEDIR.TAG", ClassUnique, "")
	if v.Kind != Keep {
		t.Fatalf("verdict %s; want keep for the untracked tag outside any ignored directory", v.Kind)
	}
}

func TestEvaluateClassesAnEmptyDirectoryAsGeneratedWhateverTheSettings(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(filepath.Join(f.instance, "a", "b", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := f.policy(t)
	p.IgnoredUnique, p.JobScratchUnique = true, true
	v := Evaluate(context.Background(), f.unit(), p)
	if v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want reap", v.Kind, v.Checks)
	}
	wantClass(t, v, "a/", ClassGenerated, "")
}

func TestEvaluateKeepsAModifiedTrackedFileApartFromUntrackedOnes(t *testing.T) {
	f := checkedOut(t)
	writeFile(t, filepath.Join(f.clone, "README"), "changed\n")
	writeFile(t, filepath.Join(f.clone, "notes.txt"), "n\n")
	v := Evaluate(context.Background(), f.unit(), f.policy(t))
	var mod, untracked string
	for _, c := range v.Checks {
		switch {
		case strings.Contains(c.Evidence, "modified tracked files:"):
			mod = c.Evidence
		case strings.Contains(c.Evidence, "untracked files:"):
			untracked = c.Evidence
		}
	}
	if !strings.Contains(mod, "README") || strings.Contains(mod, "notes.txt") {
		t.Errorf("modified evidence %q; want README alone", mod)
	}
	if !strings.Contains(untracked, "untracked files: notes.txt") {
		t.Errorf("untracked evidence %q; want notes.txt", untracked)
	}
	wantClass(t, v, "tools/app/README", ClassUnique, "")
}
