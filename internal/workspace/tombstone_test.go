package workspace

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/claudetrust"
	"github.com/tsukumogami/niwa/internal/hostdir"
)

var tombstonePattern = regexp.MustCompile(`^\.niwa-tombstone-doomed-[0-9a-f]{8}$`)

func TestTombstoneInstance_RenamesInTheSameDirectory(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "doomed")
	if err := os.WriteFile(filepath.Join(inst, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	tomb, err := TombstoneInstance(root, "doomed")
	if err != nil {
		t.Fatalf("TombstoneInstance: %v", err)
	}
	if filepath.Dir(tomb) != root || !tombstonePattern.MatchString(filepath.Base(tomb)) {
		t.Fatalf("tombstone = %s, want <root>/.niwa-tombstone-doomed-<8 hex>", tomb)
	}
	// A path opened under the old name gets "no such file".
	if _, err := os.Open(filepath.Join(inst, "file.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("opening under the old name: %v, want no such file", err)
	}
	if b, err := os.ReadFile(filepath.Join(tomb, "file.txt")); err != nil || string(b) != "data" {
		t.Fatalf("tombstone content = %q (%v)", b, err)
	}

	back, err := UntombstoneInstance(root, tomb)
	if err != nil {
		t.Fatalf("UntombstoneInstance: %v", err)
	}
	if back != inst {
		t.Fatalf("renamed back to %s, want %s", back, inst)
	}
	if b, err := os.ReadFile(filepath.Join(inst, "file.txt")); err != nil || string(b) != "data" {
		t.Fatalf("instance content after the rename back = %q (%v)", b, err)
	}
	if _, err := os.Lstat(tomb); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tombstone still there after the rename back: %v", err)
	}
}

func TestTombstoneInstance_RefusesASymlinkAndBadNames(t *testing.T) {
	root := destroySetupWorkspace(t)
	elsewhere := destroySetupInstance(t, t.TempDir(), "elsewhere")
	if err := os.Symlink(elsewhere, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := TombstoneInstance(root, "linked"); err == nil {
		t.Fatal("TombstoneInstance renamed a symlink")
	}
	if _, err := os.Lstat(filepath.Join(root, "linked")); err != nil {
		t.Fatalf("the link moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, StateDir, StateFile)); err != nil {
		t.Fatalf("the link's target changed: %v", err)
	}
	for _, name := range []string{"", ".", "..", "a/b", ".niwa", ".niwa-launch", ".niwa-tombstone-x-0011aabb", "missing"} {
		if _, err := TombstoneInstance(root, name); err == nil {
			t.Errorf("TombstoneInstance(%q) succeeded", name)
		}
	}
}

func TestTombstoneInstance_NeverReplacesAnEntryAtItsName(t *testing.T) {
	root := destroySetupWorkspace(t)
	destroySetupInstance(t, root, "doomed")
	taken := filepath.Join(root, TombstoneName("doomed", "00000000"))
	if err := os.Mkdir(taken, 0o755); err != nil {
		t.Fatal(err)
	}
	picks := []string{"00000000", "11111111"}
	orig := randomTombstoneSuffix
	randomTombstoneSuffix = func() (string, error) {
		p := picks[0]
		picks = picks[1:]
		return p, nil
	}
	t.Cleanup(func() { randomTombstoneSuffix = orig })

	tomb, err := TombstoneInstance(root, "doomed")
	if err != nil {
		t.Fatalf("TombstoneInstance: %v", err)
	}
	if filepath.Base(tomb) != TombstoneName("doomed", "11111111") {
		t.Fatalf("tombstone = %s, want the second pick", tomb)
	}
	if entries, _ := os.ReadDir(taken); len(entries) != 0 {
		t.Fatal("the rename wrote into the entry already at the first pick")
	}
}

func TestUntombstoneInstance_KeepsWhenTheNameIsTaken(t *testing.T) {
	root := destroySetupWorkspace(t)
	destroySetupInstance(t, root, "doomed")
	tomb, err := TombstoneInstance(root, "doomed")
	if err != nil {
		t.Fatal(err)
	}
	destroySetupInstance(t, root, "doomed")

	if _, err := UntombstoneInstance(root, tomb); !errors.Is(err, ErrInstanceNameTaken) {
		t.Fatalf("UntombstoneInstance over a taken name = %v, want ErrInstanceNameTaken", err)
	}
	if _, err := os.Stat(filepath.Join(tomb, StateDir, StateFile)); err != nil {
		t.Fatalf("the tombstone moved: %v", err)
	}
}

func TestUntombstoneInstance_RefusesALinkOrAPathElsewhere(t *testing.T) {
	root := destroySetupWorkspace(t)
	elsewhere := destroySetupInstance(t, t.TempDir(), "doomed")
	link := filepath.Join(root, TombstoneName("doomed", "0011aabb"))
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	if _, err := UntombstoneInstance(root, link); err == nil {
		t.Fatal("UntombstoneInstance renamed a symlinked tombstone")
	}
	if _, err := os.Lstat(filepath.Join(root, "doomed")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("something was renamed to the instance name: %v", err)
	}

	nested := filepath.Join(root, "sub")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(nested, TombstoneName("doomed", "0011aabb"))
	if err := os.Mkdir(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := UntombstoneInstance(root, deep); err == nil {
		t.Fatal("UntombstoneInstance renamed a tombstone that isn't directly under the root")
	}
}

func TestParseTombstoneName(t *testing.T) {
	cases := map[string]string{
		".niwa-tombstone-ws-0011aabb":      "ws",
		".niwa-tombstone-ws+x-1-0011aabb":  "ws+x-1",
		".niwa-tombstone-ws-0011AABB":      "",
		".niwa-tombstone-ws-0011aab":       "",
		".niwa-tombstone--0011aabb":        "",
		".niwa-tombstone-.niwa-0011aabb":   "",
		".niwa-tombstone-a/b-0011aabb":     "",
		"ws-0011aabb":                      "",
		".niwa-tombstone-ws_0011aabb":      "",
		".niwa-tombstone-ws-0011aabbcc":    "",
		".niwa-tombstone-.-0011aabb":       "",
		".niwa-tombstone-ws-0011aabb-tail": "",
	}
	for in, want := range cases {
		got, ok := ParseTombstoneName(in)
		if want == "" && ok {
			t.Errorf("ParseTombstoneName(%q) = %q, want no instance", in, got)
		}
		if want != "" && (!ok || got != want) {
			t.Errorf("ParseTombstoneName(%q) = %q, %v, want %q", in, got, ok, want)
		}
	}
}

func TestEnumerateInstances_SkipsNiwaNamesAndTombstones(t *testing.T) {
	root := destroySetupWorkspace(t)
	kept := destroySetupInstance(t, root, "kept")
	destroySetupInstance(t, root, "doomed")
	tomb, err := TombstoneInstance(root, "doomed")
	if err != nil {
		t.Fatal(err)
	}
	destroySetupInstance(t, root, ".niwa-other")

	got, err := EnumerateInstances(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != kept {
		t.Fatalf("EnumerateInstances = %v, want only %s", got, kept)
	}

	// hostdir.InstanceDirs is unchanged: it still lists the tombstone, so
	// the plugin store's sweep sees its links.
	dirs, err := hostdir.InstanceDirs(t.TempDir(), root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range dirs {
		if d == tomb {
			found = true
		}
	}
	if !found {
		t.Fatalf("hostdir.InstanceDirs = %v, want it to list %s", dirs, tomb)
	}
}

func TestTombstoneLockPath_UsesTheWorkspaceKeyDigits(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	got := TombstoneLockPath(home, root)
	key := strings.TrimPrefix(hostdir.WorkspaceKey(root), "w-")
	if len(key) != 32 {
		t.Fatalf("workspace key digits = %q, want 32", key)
	}
	if want := filepath.Join(home, ".niwa", "locks", "t-"+key+".lock"); got != want {
		t.Fatalf("TombstoneLockPath = %s, want %s", got, want)
	}
}

func TestDestroyInstance_RefusesASymlinkedInstance(t *testing.T) {
	root := destroySetupWorkspace(t)
	target := destroySetupInstance(t, t.TempDir(), "target")
	link := filepath.Join(root, "linked")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	err := DestroyInstance(root, link)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("DestroyInstance through a symlink = %v, want a symlink refusal", err)
	}
	if _, err := os.Stat(filepath.Join(target, StateDir, StateFile)); err != nil {
		t.Fatalf("the link's target was touched: %v", err)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("the link was removed: %v", err)
	}
}

func TestDestroyInstance_RefusesAPathNotDirectlyUnderTheRoot(t *testing.T) {
	root := destroySetupWorkspace(t)
	nested := destroySetupInstance(t, filepath.Join(root, "group"), "deep")
	if err := DestroyInstance(root, nested); err == nil || !strings.Contains(err.Error(), "not directly under") {
		t.Fatalf("DestroyInstance of a nested instance = %v, want a refusal", err)
	}
	if _, err := os.Stat(nested); err != nil {
		t.Fatalf("the nested instance was removed: %v", err)
	}

	other := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, other, "alpha")
	if err := DestroyInstance(root, inst); err == nil {
		t.Fatal("DestroyInstance of another workspace's instance succeeded")
	}
	if _, err := os.Stat(inst); err != nil {
		t.Fatalf("the other workspace's instance was removed: %v", err)
	}

	if err := DestroyInstance("", inst); err == nil {
		t.Fatal("DestroyInstance with no workspace root succeeded")
	}
}

// A root reached through a symlink resolves to the same directory, so the
// instance under it is directly under the resolved root.
func TestDestroyInstance_AcceptsARootReachedThroughALink(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "alpha")
	linkedRoot := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, linkedRoot); err != nil {
		t.Fatal(err)
	}
	if err := DestroyInstance(linkedRoot, inst); err != nil {
		t.Fatalf("DestroyInstance under a linked root: %v", err)
	}
	assertGone(t, inst)
}

// A destroy prepared on the instance's own path and removing its tombstone
// prunes the plugin records and withdraws the trust grant recorded for the
// original path, and removes the tombstone.
func TestDestroyPlan_RemovesATombstoneWithTheOriginalPathsRecords(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "doomed")
	sibling := destroySetupInstance(t, root, "doomed-sibling")
	key := trustKey(t, inst)

	base := registryBaseDir(t)
	seedRegistry(t, base, inst, filepath.Join(inst, "repo"), sibling)

	cfgPath := filepath.Join(t.TempDir(), ".claude.json")
	writeClaudeConfig(t, cfgPath, map[string]any{
		"projects": map[string]any{
			key:            map[string]any{"hasTrustDialogAccepted": true},
			"/elsewhere/p": map[string]any{"hasTrustDialogAccepted": true},
		},
	})
	recordClaudeTrust(t, inst, &claudetrust.Grant{
		ConfigFile: cfgPath, Key: key, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceWorkspace,
	})

	var buf bytes.Buffer
	plan, err := PrepareDestroy(root, inst, WithPluginRecordBaseDir(base), WithDestroyReporter(NewReporterWithTTY(&buf, false)))
	if err != nil {
		t.Fatalf("PrepareDestroy: %v", err)
	}
	tomb, err := TombstoneInstance(root, "doomed")
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Remove(tomb); err != nil {
		t.Fatalf("Remove(tombstone): %v", err)
	}
	assertGone(t, tomb)
	assertGone(t, inst)

	if got := loadRegistryProjectPaths(t, base); len(got) != 1 || got[0] != sibling {
		t.Fatalf("remaining plugin records = %v, want only the sibling's", got)
	}
	projects := projectsOf(t, readClaudeConfig(t, cfgPath))
	if _, ok := projects[key]; ok {
		t.Errorf("trust entry %s for the original path remains: %v", key, projects)
	}
	if _, ok := projects["/elsewhere/p"]; !ok {
		t.Errorf("an unrelated trust entry was removed: %v", projects)
	}
	if strings.Contains(buf.String(), "left in place") {
		t.Errorf("a trust entry was left in place: %s", buf.String())
	}
}

// Remove refuses a directory other than the one the plan checked, and a
// tombstone that is a symlink, and deletes nothing.
func TestDestroyPlan_RefusesAnotherDirectory(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "doomed")
	plan, err := PrepareDestroy(root, inst)
	if err != nil {
		t.Fatal(err)
	}
	impostor := destroySetupInstance(t, root, TombstoneName("doomed", "0011aabb"))
	if err := plan.Remove(impostor); err == nil {
		t.Fatal("Remove accepted a directory the plan didn't check")
	}
	if _, err := os.Stat(impostor); err != nil {
		t.Fatalf("the other directory was removed: %v", err)
	}
	if _, err := os.Stat(inst); err != nil {
		t.Fatalf("the instance was removed: %v", err)
	}

	target := destroySetupInstance(t, t.TempDir(), "target")
	link := filepath.Join(root, TombstoneName("doomed", "2233ccdd"))
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := plan.Remove(link); err == nil {
		t.Fatal("Remove accepted a symlinked tombstone")
	}
	if _, err := os.Stat(filepath.Join(target, StateDir, StateFile)); err != nil {
		t.Fatalf("the link's target was touched: %v", err)
	}
}
