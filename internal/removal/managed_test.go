package removal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
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

// swapForLink replaces the directory dir with a link to target.
func swapForLink(t *testing.T, dir, target string) {
	t.Helper()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	symlink(t, target, dir)
}

func TestEvaluateKeepsAManagedFileWhoseDirectoryBecomesALinkAfterTheCheck(t *testing.T) {
	f := classFixture(t)
	const body = "managed body"
	const rel = "docs/sub/notes.md"
	writeFile(t, filepath.Join(f.instance, "docs", "sub", "notes.md"), body)
	withManaged(t, f, body, filepath.Join(f.instance, "docs", "sub", "notes.md"))
	u := f.unit()
	s := &evalState{niwa: openNiwaDir(u.InstancePath)}
	defer s.niwa.close()

	// ResolveRecorded accepts the entry while sub is a real directory.
	if m := s.managedFiles(u.InstancePath); len(m.failures) != 0 || m.files[rel].rel != rel {
		t.Fatalf("managed read %+v; want the entry accepted", m)
	}
	// Then sub becomes a link to a directory holding a file with the hash
	// niwa recorded. A read by name would follow it and find the hash.
	elsewhere := t.TempDir()
	writeFile(t, filepath.Join(elsewhere, "notes.md"), body)
	swapForLink(t, filepath.Join(f.instance, "docs", "sub"), elsewhere)

	if s.generated(u.InstancePath)(rel) {
		t.Fatalf("%s classed generated through a link swapped in after the check", rel)
	}
	keeps := checkOutside(context.Background(), u, classPolicy(t, f), s)
	var named bool
	for _, c := range keeps {
		if len(c.Paths) == 1 && c.Paths[0] == rel && strings.HasPrefix(c.Evidence, "unreadable: managed file ") {
			named = true
			if !strings.Contains(c.Evidence, "docs/sub") {
				t.Errorf("evidence %q; want the swapped directory named", c.Evidence)
			}
		}
	}
	if !named {
		t.Fatalf("outside keeps %+v; want %s named as an unreadable managed file", keeps, rel)
	}
	v := Verdict{Paths: s.classes.sorted()}
	wantClass(t, v, rel, ClassUnique, "")
	// The listed CLAUDE.md, read the same way, still counts as generated.
	wantClass(t, v, "CLAUDE.md", ClassGenerated, "")
}

func TestReadManagedFileUnderNiwaGoesThroughTheNiwaDescriptor(t *testing.T) {
	u, _ := bareUnit(t)
	writeFile(t, filepath.Join(u.InstancePath, ".niwa", "extra", "notes.md"), "body")
	s := &evalState{niwa: openNiwaDir(u.InstancePath)}
	defer s.niwa.close()
	mf := managedFile{base: u.InstancePath, rel: ".niwa/extra/notes.md"}
	if b, err := s.readManagedFile(mf); err != nil || string(b) != "body" {
		t.Fatalf("read %q, %v; want the file's body", b, err)
	}
	elsewhere := t.TempDir()
	writeFile(t, filepath.Join(elsewhere, "notes.md"), "body")
	swapForLink(t, filepath.Join(u.InstancePath, ".niwa", "extra"), elsewhere)
	if b, err := s.readManagedFile(mf); err == nil {
		t.Fatalf("read %q through a link under .niwa; want it refused", b)
	}
	// A link at the file itself is refused too.
	symlink(t, filepath.Join(elsewhere, "notes.md"), filepath.Join(u.InstancePath, "linked.md"))
	if b, err := s.readManagedFile(managedFile{base: u.InstancePath, rel: "linked.md"}); err == nil {
		t.Fatalf("read %q through a link at the file; want it refused", b)
	}
}

// onAnotherDevice makes statDevice report a different device for each of
// paths, matched by device and inode, until the test ends. ResolveRecorded
// reads devices with Lstat, not through statDevice, so it still accepts the
// path: this is a mount appearing on the way after that check.
func onAnotherDevice(t *testing.T, paths ...string) {
	t.Helper()
	type id struct{ dev, ino uint64 }
	moved := map[id]bool{}
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		st := info.Sys().(*syscall.Stat_t)
		moved[id{uint64(st.Dev), uint64(st.Ino)}] = true
	}
	prev := statDevice
	statDevice = func(st *unix.Stat_t) uint64 {
		if moved[id{uint64(st.Dev), uint64(st.Ino)}] {
			return uint64(st.Dev) + 1
		}
		return prev(st)
	}
	t.Cleanup(func() { statDevice = prev })
}

func TestEvaluateKeepsAManagedFileOnAnotherDevice(t *testing.T) {
	const body = "managed body"
	const rel = "docs/sub/notes.md"
	for _, tc := range []struct {
		name, moved, shown string
	}{
		{"a directory on the way", "docs/sub", "docs/sub is on another device"},
		{"the file itself", rel, rel + " is on another device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := classFixture(t)
			file := filepath.Join(f.instance, "docs", "sub", "notes.md")
			writeFile(t, file, body)
			withManaged(t, f, body, file)
			onAnotherDevice(t, filepath.Join(f.instance, filepath.FromSlash(tc.moved)))

			v := Evaluate(context.Background(), f.unit(), classPolicy(t, f))
			if v.Kind != Keep {
				t.Fatalf("verdict %s %+v; want keep", v.Kind, v.Checks)
			}
			c, ok := managedKeep(v, rel)
			if !ok {
				t.Fatalf("checks %+v; want %s named under the outside check", v.Checks, rel)
			}
			if !strings.Contains(c.Evidence, tc.shown) {
				t.Errorf("evidence %q; want %q", c.Evidence, tc.shown)
			}
			wantClass(t, v, rel, ClassUnique, "")
			// The listed CLAUDE.md, on the instance's device, still counts
			// as generated.
			wantClass(t, v, "CLAUDE.md", ClassGenerated, "")
		})
	}
}

func TestReadManagedFileRefusesAnotherDeviceUnderNiwa(t *testing.T) {
	for _, tc := range []struct {
		name, moved, want string
	}{
		{".niwa itself", ".niwa", ".niwa is on another device"},
		{"a directory under .niwa", ".niwa/extra", ".niwa/extra is on another device"},
		{"the file under .niwa", ".niwa/extra/notes.md", ".niwa/extra/notes.md is on another device"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, _ := bareUnit(t)
			writeFile(t, filepath.Join(u.InstancePath, ".niwa", "extra", "notes.md"), "body")
			s := &evalState{niwa: openNiwaDir(u.InstancePath)}
			defer s.niwa.close()
			mf := managedFile{base: u.InstancePath, rel: ".niwa/extra/notes.md"}
			if b, err := s.readManagedFile(mf); err != nil || string(b) != "body" {
				t.Fatalf("read %q, %v; want the file's body while everything is on one device", b, err)
			}
			onAnotherDevice(t, filepath.Join(u.InstancePath, filepath.FromSlash(tc.moved)))
			b, err := s.readManagedFile(mf)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("read %q, %v; want %q", b, err, tc.want)
			}
		})
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
