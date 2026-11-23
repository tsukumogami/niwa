package removal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// withManaged rewrites the class fixture's instance.json so managed_files
// lists the fixture's CLAUDE.md and each extra path, absolute as niwa
// records them, with the hash of body.
func withManaged(t *testing.T, f fixture, body string, extra ...string) {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	hash := "sha256:" + hex.EncodeToString(sum[:])
	claude := sha256.Sum256([]byte("# context\n"))
	entries := []map[string]string{{
		"path": filepath.Join(f.instance, "CLAUDE.md"),
		"hash": "sha256:" + hex.EncodeToString(claude[:]),
	}}
	for _, p := range extra {
		entries = append(entries, map[string]string{"path": p, "hash": hash})
	}
	state, err := json.Marshal(map[string]any{"managed_files": entries})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.instance, ".niwa", "instance.json"), string(state))
}

// managedKeep returns the outside check's keep naming rel as an unreadable
// managed file.
func managedKeep(v Verdict, rel string) (Check, bool) {
	for _, c := range checksNamed(v, CheckOutside) {
		if len(c.Paths) == 1 && c.Paths[0] == rel && strings.HasPrefix(c.Evidence, "unreadable: managed file ") {
			return c, true
		}
	}
	return Check{}, false
}

func TestEvaluateKeepsAManagedFileWithADotDotComponent(t *testing.T) {
	f := classFixture(t)
	// The textual prefix strip keeps the .. for ResolveRecorded to refuse;
	// cleaning first would have turned it into CLAUDE.md and passed it.
	withManaged(t, f, "# context\n", f.instance+"/sub/../CLAUDE.md")
	v := Evaluate(context.Background(), f.unit(), classPolicy(t, f))
	if v.Kind != Keep {
		t.Fatalf("verdict %s %+v; want keep", v.Kind, v.Checks)
	}
	c, ok := managedKeep(v, "sub/../CLAUDE.md")
	if !ok {
		t.Fatalf("checks %+v; want the .. entry named under the outside check", v.Checks)
	}
	if !strings.Contains(c.Evidence, `"sub/../CLAUDE.md"`) || !strings.Contains(c.Evidence, `".." component`) {
		t.Errorf("evidence %q; want the entry and its .. named", c.Evidence)
	}
	wantClass(t, v, "sub/../CLAUDE.md", ClassUnique, "")
	// The listed CLAUDE.md itself still counts as generated.
	wantClass(t, v, "CLAUDE.md", ClassGenerated, "")
}

func TestEvaluateKeepsAManagedFileThroughASymlinkedDirectory(t *testing.T) {
	f := classFixture(t)
	elsewhere := t.TempDir()
	writeFile(t, filepath.Join(elsewhere, "notes.md"), "managed body")
	symlink(t, elsewhere, filepath.Join(f.instance, "linked"))
	withManaged(t, f, "managed body", filepath.Join(f.instance, "linked", "notes.md"))
	v := Evaluate(context.Background(), f.unit(), classPolicy(t, f))
	if v.Kind != Keep {
		t.Fatalf("verdict %s %+v; want keep", v.Kind, v.Checks)
	}
	c, ok := managedKeep(v, "linked/notes.md")
	if !ok {
		t.Fatalf("checks %+v; want the entry through the link named under the outside check", v.Checks)
	}
	if !strings.Contains(c.Evidence, "linked is a symlink") {
		t.Errorf("evidence %q; want the link named", c.Evidence)
	}
}

func TestEvaluateDropsAMissingManagedFile(t *testing.T) {
	f := classFixture(t)
	withManaged(t, f, "gone", filepath.Join(f.instance, "gone.md"), filepath.Join(f.instance, "gone-dir", "x.md"))
	v := Evaluate(context.Background(), f.unit(), classPolicy(t, f))
	if v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want reap: a managed file the agent deleted loses nothing", v.Kind, v.Checks)
	}
	wantClass(t, v, "CLAUDE.md", ClassGenerated, "")
}

func TestEvaluateSkipsAManagedFileOutsideTheInstance(t *testing.T) {
	f := classFixture(t)
	withManaged(t, f, "x", filepath.Join(t.TempDir(), "elsewhere.md"), "relative/path.md")
	v := Evaluate(context.Background(), f.unit(), classPolicy(t, f))
	if v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want reap: entries outside the instance are not the rule's", v.Kind, v.Checks)
	}
}
