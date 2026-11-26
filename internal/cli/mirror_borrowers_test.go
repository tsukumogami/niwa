package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tsukumogami/niwa/internal/hostdir"
	"github.com/tsukumogami/niwa/internal/mirror"
)

// borrowEverywhere adds, beside the instance "inst", one instance moved
// under kept/ and one half-created instance with no state file, each with a
// clone of app borrowing from the mirror, and returns those two clones.
func (h hookMirror) borrowEverywhere(t *testing.T) (kept, half string) {
	t.Helper()
	kept = filepath.Join(hostdir.WorkspaceDir(h.home, h.root), hostdir.KeptDirName, "old", "tools", "app")
	half = filepath.Join(h.root, "half", "tools", "app")
	line := mirror.StablePath(h.home, h.root, "app") + "/objects\n"
	for _, clone := range []string{kept, half} {
		info := filepath.Join(clone, ".git", "objects", "info")
		if err := os.MkdirAll(info, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(info, "alternates"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return kept, half
}

// deleteMirror removes the stable link and the store it names.
func (h hookMirror) deleteMirror(t *testing.T) {
	t.Helper()
	stable := mirror.StablePath(h.home, h.root, "app")
	target, err := os.Readlink(stable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stable); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(filepath.Dir(stable), target)); err != nil {
		t.Fatal(err)
	}
}

// requireProbed fails unless the last restore line among lines lists every
// clone in want as probed.
func requireProbed(t *testing.T, lines []map[string]any, want ...string) {
	t.Helper()
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i]["event"] != "restore" {
			continue
		}
		raw, _ := lines[i]["probed"].([]any)
		var probed []string
		for _, v := range raw {
			probed = append(probed, fmt.Sprint(v))
		}
		for _, clone := range want {
			if !slices.Contains(probed, clone) {
				t.Fatalf("the restore line probed %v, want it to list %s", probed, clone)
			}
		}
		return
	}
	t.Fatalf("no restore line among %v", lines)
}

func TestCheckRecordedMirrors_ProbesKeptAndHalfCreatedClones(t *testing.T) {
	h := setupHookMirror(t, true)
	kept, half := h.borrowEverywhere(t)
	h.deleteMirror(t)
	before := len(h.logLines(t))

	res := checkRecordedMirrors(context.Background(), h.root)
	if len(res) != 1 || res[0].Outcome != mirror.OutcomeRestored {
		t.Fatalf("results = %+v, want app restored", res)
	}
	requireProbed(t, h.logLines(t)[before:], h.clone, kept, half)
}

func TestCheckWorktreeMirror_ProbesEveryCloneOnDisk(t *testing.T) {
	h := setupHookMirror(t, true)
	kept, half := h.borrowEverywhere(t)
	h.deleteMirror(t)
	before := len(h.logLines(t))

	var stderr bytes.Buffer
	checkWorktreeMirror(context.Background(), filepath.Join(h.root, "inst"), "app", &stderr)
	requireProbed(t, h.logLines(t)[before:], h.clone, kept, half)
}
