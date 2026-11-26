package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// reviewSettingsRel is the one file ApplyReviewSettings writes in a review
// instance, as the removal rule names it.
const reviewSettingsRel = ".claude/settings.json"

// newRerecordInstance gives a review instance under a fresh workspace root:
// its .niwa/instance.json, and, when provisioned is set, a .claude/settings.json
// recorded as a managed file with its hash, the way provisioning leaves one.
func newRerecordInstance(t *testing.T, provisioned bool) (root, inst string) {
	t.Helper()
	root, _ = setupWatchCharEnv(t)
	stubProcessListing(t)
	inst = filepath.Join(root, "test-ws-review")
	if err := writeMinimalInstanceState(inst); err != nil {
		t.Fatal(err)
	}
	if !provisioned {
		return root, inst
	}
	settings := filepath.Join(inst, filepath.FromSlash(reviewSettingsRel))
	writeTestFile(t, settings, "{\n  \"permissions\": {\n    \"allow\": [\"Read\"]\n  }\n}\n")
	hash, err := workspace.HashFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	state, err := workspace.LoadState(inst)
	if err != nil {
		t.Fatal(err)
	}
	state.ManagedFiles = append(state.ManagedFiles, workspace.ManagedFile{
		Path:        settings,
		ContentHash: hash,
		Generated:   time.Now(),
	})
	if err := workspace.SaveState(inst, state); err != nil {
		t.Fatal(err)
	}
	return root, inst
}

// rerecordVerdict asks the removal rule about inst as the failed stage's
// rollback does, with the session gone, and returns whether it kept the
// instance and what it printed.
func rerecordVerdict(t *testing.T, root, inst string) (bool, string) {
	t.Helper()
	var out bytes.Buffer
	kept := keptByRule(&out, root, rollbackUnit{InstancePath: inst, Liveness: removal.LivenessGone})
	return kept, out.String()
}

// appendByte changes the review settings file by one byte after niwa's write.
func appendByte(t *testing.T, inst string) {
	t.Helper()
	settings := filepath.Join(inst, filepath.FromSlash(reviewSettingsRel))
	f, err := os.OpenFile(settings, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("\n")); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// addForeignKey adds a top-level key to the review settings file after
// niwa's write. Unlike a whitespace change, a re-apply's merge keeps it, so
// the file stays different from anything niwa wrote.
func addForeignKey(t *testing.T, inst string) {
	t.Helper()
	settings := filepath.Join(inst, filepath.FromSlash(reviewSettingsRel))
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(b), "{", "{\"foreign\": true,", 1)
	if err := os.WriteFile(settings, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestReviewSettingsRerecord_NiwasOwnWriteDoesNotKeep: the review settings
// file ApplyReviewSettings writes, with nothing else touching it afterwards,
// is niwa's own file, so the removal rule doesn't keep the instance on it.
// That holds whether provisioning left no settings file or a recorded one
// that the review settings rewrote.
func TestReviewSettingsRerecord_NiwasOwnWriteDoesNotKeep(t *testing.T) {
	for _, tc := range []struct {
		name        string
		provisioned bool
	}{
		{"no settings before", false},
		{"provisioned settings rewritten", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, inst := newRerecordInstance(t, tc.provisioned)
			if err := watch.ApplyReviewSettings(inst, true, false); err != nil {
				t.Fatalf("ApplyReviewSettings: %v", err)
			}
			kept, out := rerecordVerdict(t, root, inst)
			if kept {
				t.Errorf("the rule kept an instance whose settings only niwa wrote:\n%s", out)
			}
			if strings.Contains(out, reviewSettingsRel) {
				t.Errorf("the rule named %s:\n%s", reviewSettingsRel, out)
			}
		})
	}
}

// TestReviewSettingsRerecord_ALaterChangeStillKeeps: a byte changed in the
// review settings file after niwa's write makes it something else's file, so
// the rule keeps the instance and names it. A later re-apply that merges over
// a change (as a continuation's would) doesn't make the file niwa's again.
func TestReviewSettingsRerecord_ALaterChangeStillKeeps(t *testing.T) {
	for _, tc := range []struct {
		name        string
		provisioned bool
		reapply     bool
	}{
		{"changed after the write", false, false},
		{"provisioned, changed after the write", true, false},
		{"changed, then re-applied", false, true},
		{"provisioned, changed, then re-applied", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, inst := newRerecordInstance(t, tc.provisioned)
			if err := watch.ApplyReviewSettings(inst, true, false); err != nil {
				t.Fatalf("ApplyReviewSettings: %v", err)
			}
			if !tc.reapply {
				appendByte(t, inst)
			} else {
				addForeignKey(t, inst)
				if err := watch.ApplyReviewSettings(inst, true, false); err != nil {
					t.Fatalf("re-applying the review settings: %v", err)
				}
			}
			kept, out := rerecordVerdict(t, root, inst)
			if !kept {
				t.Fatalf("the rule passed an instance whose settings changed after niwa's write")
			}
			if !strings.Contains(out, "outside check") || !strings.Contains(out, reviewSettingsRel) {
				t.Errorf("kept line doesn't name %s under the outside check:\n%s", reviewSettingsRel, out)
			}
		})
	}
}
