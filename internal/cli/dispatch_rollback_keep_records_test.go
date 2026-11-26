package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// A failed dispatch whose instance the removal rule keeps removes nothing:
// the instance stays, and so do its lineage entry and the copy of its
// authority record at the root, which still describe what the kept instance
// was granted. The destroy is never called.
func TestFailedDispatchRule_AKeptInstanceKeepsItsLineageEntryAndAuthorityCopy(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	const partial = "half of the worker's output\n"
	provisionThen(t, func(_, instance string) {
		writeTestFile(t, filepath.Join(instance, "partial.txt"), partial)
	})
	dispatchLaunch = func(context.Context, launchRequest) error {
		f.launchCalled++
		return errors.New("launch boom")
	}

	stdout, stderr, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("dispatch succeeded, want the launch error")
	}
	if f.launchCalled != 1 {
		t.Fatalf("launched %d times, want 1: the lineage entry is written before the launch", f.launchCalled)
	}
	if f.destroyCalled != 0 {
		t.Errorf("destroy called %d times for an instance the rule keeps", f.destroyCalled)
	}
	assertFileUnchanged(t, filepath.Join(f.instancePath, "partial.txt"), partial)
	if out := stdout + stderr; !strings.Contains(out, "outside check") || !strings.Contains(out, "partial.txt") {
		t.Errorf("the kept line doesn't name the outside check and partial.txt:\n%s", out)
	}

	rec := readRecord(t, f)
	if files := storeFiles(t, root); len(files) != 1 || files[0] != rec.DispatchID+".json" {
		t.Errorf("lineage store holds %v, want the kept dispatch's entry", files)
	}
	if _, err := os.Stat(workspace.AuthorityCopyPath(root, rec.DispatchID)); err != nil {
		t.Errorf("the authority copy is gone for a kept instance: %v", err)
	}
}
