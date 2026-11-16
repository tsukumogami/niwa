//go:build unix

package workspace

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestInstanceLockPath(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	inst := filepath.Join(ws, "inst")
	for _, d := range []string{inst, filepath.Join(ws, "sub"), filepath.Join(ws, "inst-2")} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(t.TempDir(), "wslink")
	if err := os.Symlink(ws, link); err != nil {
		t.Fatal(err)
	}

	got := InstanceLockPath(home, inst)
	if filepath.Dir(got) != filepath.Join(home, ".niwa", "locks") {
		t.Fatalf("lock %q is not directly in ~/.niwa/locks", got)
	}

	for _, variant := range []string{inst + "/", ws + "/./inst", ws + "/sub/../inst", filepath.Join(link, "inst")} {
		if v := InstanceLockPath(home, variant); v != got {
			t.Errorf("InstanceLockPath(%q) = %q, want %q", variant, v, got)
		}
	}
	if other := InstanceLockPath(home, filepath.Join(ws, "inst-2")); other == got {
		t.Error("inst-2 shares inst's lock")
	}

	viaLink := InstanceLockPath(home, filepath.Join(link, "inst"))
	if err := os.Remove(inst); err != nil {
		t.Fatal(err)
	}
	if after := InstanceLockPath(home, inst); after != got {
		t.Errorf("after removal InstanceLockPath = %q, want %q", after, got)
	}
	if after := InstanceLockPath(home, filepath.Join(link, "inst")); after != viaLink {
		t.Errorf("after removal, through the symlinked root = %q, want %q", after, viaLink)
	}

	name := filepath.Base(got)
	if !regexp.MustCompile(`^i-[0-9a-f]{32}\.lock$`).MatchString(name) {
		t.Errorf("lock name %q is not derived from a hash alone", name)
	}
	// A component made only of hex digits (a temp directory's "001") can turn
	// up inside any hash by chance; every other component must not appear.
	for _, comp := range strings.Split(inst, string(os.PathSeparator)) {
		if comp != "" && strings.Trim(comp, "0123456789abcdef") != "" && strings.Contains(name, comp) {
			t.Errorf("lock name %q carries path component %q", name, comp)
		}
	}
	if strings.HasPrefix(got, inst+string(os.PathSeparator)) || strings.HasPrefix(got, filepath.Join(ws, ".niwa")+string(os.PathSeparator)) {
		t.Errorf("lock %q lives under the instance or the workspace's .niwa", got)
	}
}
