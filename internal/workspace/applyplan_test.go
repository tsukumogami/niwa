package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// newPlanEntry builds an entry with the fields these tests care about, so each
// case reads as the one thing it is exercising rather than a wall of zero
// values. Tests may construct plan types; production code in this package may
// not (applyplan_wiring_test.go).
func newPlanEntry(op agentplan.Op, path string, content string) agentplan.Entry {
	return agentplan.Entry{
		Op:      op,
		Path:    path,
		Content: []byte(content),
		Mode:    0o644,
	}
}

func TestApplyPlanWriteFileCreatesParentsAndHonorsMode(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b", "settings.json")
	secret := filepath.Join(dir, "env.local")
	script := filepath.Join(dir, "hook.sh")

	secretEntry := newPlanEntry(agentplan.OpWriteFile, secret, "TOKEN=x\n")
	secretEntry.Mode = 0o600
	scriptEntry := newPlanEntry(agentplan.OpWriteFile, script, "#!/bin/sh\n")
	scriptEntry.Mode = 0o755

	plan := &agentplan.Plan{Entries: []agentplan.Entry{
		newPlanEntry(agentplan.OpWriteFile, nested, "{}\n"),
		secretEntry,
		scriptEntry,
	}}

	written, excludes, err := applyPlan(plan)
	if err != nil {
		t.Fatalf("applyPlan: %v", err)
	}
	if len(excludes) != 0 {
		t.Errorf("excludes = %v, want none", excludes)
	}
	wantWritten := []string{nested, secret, script}
	if strings.Join(written, "\n") != strings.Join(wantWritten, "\n") {
		t.Errorf("written = %v, want %v", written, wantWritten)
	}

	if got := readFileString(t, nested); got != "{}\n" {
		t.Errorf("nested content = %q", got)
	}

	for _, tc := range []struct {
		path string
		mode os.FileMode
	}{
		{nested, 0o644},
		{secret, 0o600},
		{script, 0o755},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tc.path, err)
		}
		if info.Mode().Perm() != tc.mode {
			t.Errorf("%s mode = %o, want %o", tc.path, info.Mode().Perm(), tc.mode)
		}
	}
}

func TestApplyPlanAppendLineIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	rules := filepath.Join(dir, "rules", "workspace-imports.md")

	first := &agentplan.Plan{Entries: []agentplan.Entry{
		newPlanEntry(agentplan.OpAppendLine, rules, "@/abs/one.md"),
	}}
	second := &agentplan.Plan{Entries: []agentplan.Entry{
		newPlanEntry(agentplan.OpAppendLine, rules, "@/abs/one.md"),
		newPlanEntry(agentplan.OpAppendLine, rules, "@/abs/two.md"),
	}}

	if _, _, err := applyPlan(first); err != nil {
		t.Fatalf("first applyPlan: %v", err)
	}
	if got := readFileString(t, rules); got != "@/abs/one.md\n" {
		t.Fatalf("after first apply = %q", got)
	}

	written, _, err := applyPlan(second)
	if err != nil {
		t.Fatalf("second applyPlan: %v", err)
	}
	if got := readFileString(t, rules); got != "@/abs/one.md\n@/abs/two.md\n" {
		t.Errorf("after second apply = %q", got)
	}
	// The already-present line still counts as written: the file is the
	// plan's whether or not this apply changed its bytes.
	if len(written) != 1 || written[0] != rules {
		t.Errorf("written = %v, want [%s]", written, rules)
	}
}

func TestApplyPlanAppendLineTerminatesUnterminatedFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "imports.md")
	if err := os.WriteFile(target, []byte("@/abs/existing.md"), 0o644); err != nil {
		t.Fatalf("seeding target: %v", err)
	}

	plan := &agentplan.Plan{Entries: []agentplan.Entry{
		newPlanEntry(agentplan.OpAppendLine, target, "@/abs/added.md"),
	}}
	if _, _, err := applyPlan(plan); err != nil {
		t.Fatalf("applyPlan: %v", err)
	}

	want := "@/abs/existing.md\n@/abs/added.md\n"
	if got := readFileString(t, target); got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestApplyPlanReplaceSection(t *testing.T) {
	const marker = "## Worktree Context"
	section := marker + "\n\nfresh body\n"

	cases := []struct {
		name   string
		seed   string
		seeded bool
		want   string
	}{
		{
			name:   "marker present replaces from the marker on",
			seed:   "user prose\n\n" + marker + "\n\nstale body\n",
			seeded: true,
			want:   "user prose\n\n" + section,
		},
		{
			name:   "marker missing appends the section",
			seed:   "user prose\n",
			seeded: true,
			want:   "user prose\n\n" + section,
		},
		{
			name: "file missing becomes the section",
			want: section,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "CONTEXT.md")
			if tc.seeded {
				if err := os.WriteFile(target, []byte(tc.seed), 0o644); err != nil {
					t.Fatalf("seeding target: %v", err)
				}
			}

			entry := newPlanEntry(agentplan.OpReplaceSection, target, section)
			entry.Marker = marker
			if _, _, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{entry}}); err != nil {
				t.Fatalf("applyPlan: %v", err)
			}
			if got := readFileString(t, target); got != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApplyPlanReplaceSectionIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CONTEXT.md")
	const marker = "## Worktree Context"
	section := marker + "\n\nbody\n"

	entry := newPlanEntry(agentplan.OpReplaceSection, target, section)
	entry.Marker = marker
	plan := &agentplan.Plan{Entries: []agentplan.Entry{entry}}

	if _, _, err := applyPlan(plan); err != nil {
		t.Fatalf("first applyPlan: %v", err)
	}
	after := readFileString(t, target)
	if _, _, err := applyPlan(plan); err != nil {
		t.Fatalf("second applyPlan: %v", err)
	}
	if got := readFileString(t, target); got != after {
		t.Errorf("second apply changed the file: %q then %q", after, got)
	}
}

func TestApplyPlanIfSourceExists(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.md")
	present := filepath.Join(dir, "present.md")
	absent := filepath.Join(dir, "absent.md")
	if err := os.WriteFile(source, []byte("x"), 0o644); err != nil {
		t.Fatalf("seeding source: %v", err)
	}

	withSource := newPlanEntry(agentplan.OpWriteFile, present, "delivered\n")
	withSource.Pre = agentplan.IfSourceExists
	withSource.Source = source

	withoutSource := newPlanEntry(agentplan.OpWriteFile, absent, "delivered\n")
	withoutSource.Pre = agentplan.IfSourceExists
	withoutSource.Source = filepath.Join(dir, "no-such-source.md")
	withoutSource.ExcludeAs = "absent.md"

	written, excludes, err := applyPlan(&agentplan.Plan{
		Entries: []agentplan.Entry{withSource, withoutSource},
	})
	if err != nil {
		t.Fatalf("applyPlan: %v", err)
	}
	if len(written) != 1 || written[0] != present {
		t.Errorf("written = %v, want [%s]", written, present)
	}
	// A skipped entry contributes neither a path nor its exclude pattern.
	if len(excludes) != 0 {
		t.Errorf("excludes = %v, want none", excludes)
	}
	if _, err := os.Stat(absent); !os.IsNotExist(err) {
		t.Errorf("gated entry wrote %s (stat err %v)", absent, err)
	}
}

// treeMarker is the owner line the tree-delivery tests use. Its only job is to
// be the same string on the way in and on the way out.
const treeMarker = "Generated by niwa (test)"

// newTreeEntry builds a tree-delivery entry for source at path.
func newTreeEntry(path, source string) agentplan.Entry {
	e := newPlanEntry(agentplan.OpDeliverTree, path, "")
	e.Source = source
	e.Mode = 0o755
	e.Owner = treeMarker
	return e
}

// newSourceTree writes a small tree with a nested file, standing in for a
// plugin tree.
func newSourceTree(t *testing.T, root string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "skills", "review"), 0o755); err != nil {
		t.Fatalf("building source tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "review", "SKILL.md"), []byte("body\n"), 0o644); err != nil {
		t.Fatalf("writing source file: %v", err)
	}
	return root
}

func TestApplyPlanDeliverTreeLinksAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	source := newSourceTree(t, filepath.Join(dir, "plugin"))
	target := filepath.Join(dir, "repo", ".codex", "skills", "plugin")

	entry := newTreeEntry(target, source)
	entry.ExcludeAs = ".codex/skills/plugin"

	written, excludes, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{entry}})
	if err != nil {
		t.Fatalf("applyPlan: %v", err)
	}
	if len(written) != 1 || written[0] != target {
		t.Errorf("written = %v, want [%s]", written, target)
	}
	if len(excludes) != 1 || excludes[0] != ".codex/skills/plugin" {
		t.Errorf("excludes = %v, want the delivered tree's pattern", excludes)
	}

	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("lstat %s: %v", target, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink; the link is the preferred delivery", target)
	}
	// The whole tree is reachable through the link, which is what makes the
	// skill inside it resolve under its plugin's namespace.
	if _, err := os.Stat(filepath.Join(target, "skills", "review", "SKILL.md")); err != nil {
		t.Errorf("delivered tree is missing its nested file: %v", err)
	}

	if _, _, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{entry}}); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	again, err := os.Readlink(target)
	if err != nil || again != source {
		t.Errorf("re-applying changed the link to %q (err %v), want %q", again, err, source)
	}
}

func TestApplyPlanDeliverTreeRetargetsAStaleLink(t *testing.T) {
	dir := t.TempDir()
	source := newSourceTree(t, filepath.Join(dir, "plugin"))
	stale := newSourceTree(t, filepath.Join(dir, "old-instance-plugin"))
	target := filepath.Join(dir, "repo", ".codex", "skills", "plugin")

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("preparing target directory: %v", err)
	}
	if err := os.Symlink(stale, target); err != nil {
		t.Fatalf("planting stale link: %v", err)
	}

	if _, _, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{newTreeEntry(target, source)}}); err != nil {
		t.Fatalf("applyPlan: %v", err)
	}
	got, err := os.Readlink(target)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if got != source {
		t.Errorf("link target = %q, want %q; a link whose source moved has to heal", got, source)
	}
}

func TestApplyPlanDeliverTreeCopiesWhenLinkingIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	source := newSourceTree(t, filepath.Join(dir, "plugin"))
	target := filepath.Join(dir, "repo", ".codex", "skills", "plugin")

	prefersCopy(t)

	if _, _, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{newTreeEntry(target, source)}}); err != nil {
		t.Fatalf("applyPlan: %v", err)
	}

	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("lstat %s: %v", target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s is a symlink; the fallback delivers a real copy", target)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", target)
	}
	body, err := os.ReadFile(filepath.Join(target, "skills", "review", "SKILL.md"))
	if err != nil || string(body) != "body\n" {
		t.Errorf("copied file = %q (err %v), want the source's content", body, err)
	}
	// The sentinel is what lets the next apply recognize the copy as niwa's.
	marker, err := os.ReadFile(filepath.Join(target, agentplan.TreeMarkerFileName()))
	if err != nil || strings.TrimSpace(string(marker)) != treeMarker {
		t.Errorf("delivery sentinel = %q (err %v), want the entry's owner line", marker, err)
	}
}

func TestApplyPlanDeliverTreeRefreshesItsOwnCopy(t *testing.T) {
	dir := t.TempDir()
	source := newSourceTree(t, filepath.Join(dir, "plugin"))
	target := filepath.Join(dir, "repo", ".codex", "skills", "plugin")

	prefersCopy(t)

	entry := newTreeEntry(target, source)
	if _, _, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{entry}}); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	// The source drops a file and gains another. A refresh that only overlaid
	// the new content would leave the dropped one delivering forever.
	if err := os.Remove(filepath.Join(source, "skills", "review", "SKILL.md")); err != nil {
		t.Fatalf("removing source file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(source, "skills", "review", "OTHER.md"), []byte("other\n"), 0o644); err != nil {
		t.Fatalf("adding source file: %v", err)
	}

	if _, _, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{entry}}); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "skills", "review", "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("dropped file survived the refresh (stat err %v)", err)
	}
	if _, err := os.Stat(filepath.Join(target, "skills", "review", "OTHER.md")); err != nil {
		t.Errorf("added file did not reach the copy: %v", err)
	}
}

func TestApplyPlanDeliverTreeRefusesAForeignTarget(t *testing.T) {
	dir := t.TempDir()
	source := newSourceTree(t, filepath.Join(dir, "plugin"))
	target := filepath.Join(dir, "repo", ".codex", "skills", "plugin")

	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("planting foreign directory: %v", err)
	}
	kept := filepath.Join(target, "theirs.md")
	if err := os.WriteFile(kept, []byte("not niwa's\n"), 0o644); err != nil {
		t.Fatalf("writing foreign file: %v", err)
	}

	_, _, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{newTreeEntry(target, source)}})
	if !errors.Is(err, errForeignDeliveryTarget) {
		t.Fatalf("err = %v, want errForeignDeliveryTarget", err)
	}
	if _, statErr := os.Stat(kept); statErr != nil {
		t.Errorf("the refusal removed content it did not write: %v", statErr)
	}
}

func TestApplyPlanDeliverTreeSkipsAnAbsentSource(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "repo", ".codex", "skills", "plugin")

	entry := newTreeEntry(target, filepath.Join(dir, "never-installed"))
	entry.Pre = agentplan.IfSourceExists

	written, _, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{entry}})
	if err != nil {
		t.Fatalf("applyPlan: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("written = %v, want none", written)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Errorf("a gated delivery touched %s (stat err %v)", target, statErr)
	}
}

// prefersCopy forces the copy fallback for one test, so the branch that only
// runs on a platform without directory symlinks is exercised everywhere.
func prefersCopy(t *testing.T) {
	t.Helper()
	orig := treeDeliveryPrefersCopy
	treeDeliveryPrefersCopy = func() bool { return true }
	t.Cleanup(func() { treeDeliveryPrefersCopy = orig })
}

func TestApplyPlanRejectsMalformedEntries(t *testing.T) {
	dir := t.TempDir()

	noMode := newPlanEntry(agentplan.OpWriteFile, filepath.Join(dir, "a.md"), "x")
	noMode.Mode = 0

	noMarker := newPlanEntry(agentplan.OpReplaceSection, filepath.Join(dir, "b.md"), "x")

	unknownPre := newPlanEntry(agentplan.OpWriteFile, filepath.Join(dir, "c.md"), "x")
	unknownPre.Pre = agentplan.Precondition(200)

	noSource := newTreeEntry(filepath.Join(dir, "d"), "")

	noOwner := newTreeEntry(filepath.Join(dir, "e"), dir)
	noOwner.Owner = ""

	cases := []struct {
		name  string
		entry agentplan.Entry
		want  error
	}{
		{"relative path", newPlanEntry(agentplan.OpWriteFile, "relative.md", "x"), errMalformedPlanEntry},
		{"zero mode", noMode, errMalformedPlanEntry},
		{"section replace with no marker", noMarker, errMalformedPlanEntry},
		{"tree delivery with no source", noSource, errMalformedPlanEntry},
		{"tree delivery with no owner line", noOwner, errMalformedPlanEntry},
		{"unknown precondition", unknownPre, errUnknownPrecondition},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{tc.entry}})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestApplyPlanStopsAtTheFirstFailure(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.md")
	last := filepath.Join(dir, "last.md")

	bad := newPlanEntry(agentplan.OpWriteFile, filepath.Join(dir, "bad.md"), "x")
	bad.Mode = 0

	_, _, err := applyPlan(&agentplan.Plan{Entries: []agentplan.Entry{
		newPlanEntry(agentplan.OpWriteFile, first, "written\n"),
		bad,
		newPlanEntry(agentplan.OpWriteFile, last, "never\n"),
	}})
	if err == nil {
		t.Fatal("applyPlan succeeded, want an error")
	}
	if _, statErr := os.Stat(first); statErr != nil {
		t.Errorf("entry before the failure did not land: %v", statErr)
	}
	if _, statErr := os.Stat(last); !os.IsNotExist(statErr) {
		t.Errorf("entry after the failure landed (stat err %v)", statErr)
	}
}

func TestApplyPlanNilPlanIsANoOp(t *testing.T) {
	written, excludes, err := applyPlan(nil)
	if err != nil || written != nil || excludes != nil {
		t.Fatalf("applyPlan(nil) = %v, %v, %v", written, excludes, err)
	}
}

func TestPlanRunRecordsManagedEntriesAndExcludes(t *testing.T) {
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "group", "repo")
	inRepo := filepath.Join(repoDir, "settings.local.json")
	outsideRepo := filepath.Join(dir, "CONTEXT.md")
	unmanaged := filepath.Join(repoDir, "developer-owned.md")

	managed := newPlanEntry(agentplan.OpWriteFile, inRepo, "{}\n")
	managed.Managed = true
	managed.ExcludeAs = "settings.local.json"
	managed.Sources = []agentplan.SourceEntry{{
		Kind:         "plaintext",
		SourceID:     "config/settings.json",
		VersionToken: "abc",
	}}

	elsewhere := newPlanEntry(agentplan.OpWriteFile, outsideRepo, "context\n")
	elsewhere.Managed = true
	elsewhere.ExcludeAs = "CONTEXT.md"

	foreignTree := newPlanEntry(agentplan.OpWriteFile, unmanaged, "left alone\n")

	skipped := newPlanEntry(agentplan.OpWriteFile, filepath.Join(repoDir, "skipped.json"), "{}\n")
	skipped.Managed = true
	skipped.ExcludeAs = "skipped.json"
	skipped.Pre = agentplan.IfSourceExists
	skipped.Source = filepath.Join(dir, "no-such-source")

	plans := &planRun{}
	if _, _, err := plans.apply(&agentplan.Plan{
		Entries:  []agentplan.Entry{managed, elsewhere, foreignTree, skipped},
		Warnings: []string{"a declaration went unhonored"},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	entries := plans.managedEntries()
	if len(entries) != 2 {
		t.Fatalf("managedEntries = %d entries, want 2 (%v)", len(entries), entries)
	}
	if entries[0].Path != inRepo || entries[1].Path != outsideRepo {
		t.Errorf("managedEntries paths = %s, %s", entries[0].Path, entries[1].Path)
	}
	if len(entries[0].Sources) != 1 || entries[0].Sources[0].SourceID != "config/settings.json" {
		t.Errorf("managed entry lost its sources: %v", entries[0].Sources)
	}

	got := plans.excludesUnder(repoDir)
	if len(got) != 1 || got[0] != "settings.local.json" {
		t.Errorf("excludesUnder(%s) = %v, want [settings.local.json]", repoDir, got)
	}
	if got := plans.excludesUnder(filepath.Join(dir, "group", "other-repo")); len(got) != 0 {
		t.Errorf("excludesUnder(other repo) = %v, want none", got)
	}

	warnings := plans.warnings()
	if len(warnings) != 1 || warnings[0] != "a declaration went unhonored" {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestPlanRunRecordsAPathOnceByItsLastWriter(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "imports.md")

	first := newPlanEntry(agentplan.OpWriteFile, target, "@/abs/one.md\n")
	first.Managed = true
	first.Sources = []agentplan.SourceEntry{{Kind: "plaintext", SourceID: "one"}}

	second := newPlanEntry(agentplan.OpAppendLine, target, "@/abs/two.md")
	second.Managed = true
	second.Sources = []agentplan.SourceEntry{{Kind: "plaintext", SourceID: "two"}}

	plans := &planRun{}
	if _, _, err := plans.apply(&agentplan.Plan{Entries: []agentplan.Entry{first, second}}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	entries := plans.managedEntries()
	if len(entries) != 1 {
		t.Fatalf("managedEntries = %d, want 1", len(entries))
	}
	if entries[0].Sources[0].SourceID != "two" {
		t.Errorf("recorded sources = %v, want the last writer's", entries[0].Sources)
	}
}

func TestPlanRunEmptyRunRecordsNothing(t *testing.T) {
	plans := &planRun{}
	if got := plans.managedEntries(); len(got) != 0 {
		t.Errorf("managedEntries = %v, want none", got)
	}
	if got := plans.excludesUnder("/anywhere"); len(got) != 0 {
		t.Errorf("excludesUnder = %v, want none", got)
	}
	if got := plans.warnings(); len(got) != 0 {
		t.Errorf("warnings = %v, want none", got)
	}
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}
