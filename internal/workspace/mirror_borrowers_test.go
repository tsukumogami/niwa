package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/hostdir"
	"github.com/tsukumogami/niwa/internal/mirror"
)

// The pipeline's check passes every clone on disk: an instance moved under
// kept/ and a half-created one with no state file both borrow, and the
// restore's probe reads them beside the instance being applied.
func TestEnsureMirrors_ProbesKeptAndHalfCreatedClones(t *testing.T) {
	f := newMirrorFixture(t)
	line := mirror.StablePath(f.home, f.root, "app") + "/objects\n"
	kept := filepath.Join(hostdir.WorkspaceDir(f.home, f.root), hostdir.KeptDirName, "old", "tools", "app")
	half := filepath.Join(f.root, "half", "tools", "app")
	// A repo the config doesn't name borrows too.
	unnamed := filepath.Join(f.root, "half", "other", "app")
	for _, clone := range []string{kept, half, unnamed} {
		info := filepath.Join(clone, ".git", "objects", "info")
		if err := os.MkdirAll(info, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(info, "alternates"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	var stderr bytes.Buffer
	f.applier(&stderr).ensureMirrors(context.Background(), &config.WorkspaceConfig{}, f.classified, f.instance, "apply")

	lines := f.logLines(t)
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i]["event"] != "restore" {
			continue
		}
		raw, _ := lines[i]["probed"].([]any)
		var probed []string
		for _, v := range raw {
			probed = append(probed, fmt.Sprint(v))
		}
		for _, clone := range []string{filepath.Join(f.instance, "tools", "app"), kept, half, unnamed} {
			if !slices.Contains(probed, clone) {
				t.Fatalf("the restore line probed %v, want it to list %s", probed, clone)
			}
		}
		return
	}
	t.Fatalf("no restore line in %v", lines)
}
