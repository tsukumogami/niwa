package mirror

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A clone whose alternates line names a .store/ directory directly, rather
// than the stable path, still borrows from that store after the stable link
// moves on. A restore must not remove the store out from under it.
func TestRestore_KeepsAStoreACloneNamesDirectly(t *testing.T) {
	f := built(t)
	old := f.linkTarget(t)

	direct := filepath.Join(f.root, "half", "tools", "app")
	info := filepath.Join(direct, ".git", "objects", "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		t.Fatal(err)
	}
	line := filepath.Join(f.mdir(), old, "objects") + "\n"
	if err := os.WriteFile(filepath.Join(info, "alternates"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := f.applyOpts()
	opts.Instances = append(append([]string{}, opts.Instances...), direct)

	damageHEAD(t, f)
	res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored {
		t.Fatalf("restore: %+v", res)
	}
	if now := f.linkTarget(t); now == old {
		t.Fatalf("the stable link still names %s after the restore", old)
	}

	if _, err := os.Lstat(filepath.Join(f.mdir(), old, "objects")); err != nil {
		t.Fatalf("the restore removed %s (%v): a clone whose alternates names a .store/ directory directly keeps that store on disk through a restore", old, err)
	}
}
