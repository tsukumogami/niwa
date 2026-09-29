package functional

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// iRunFromDirectoryUnderWorkspace runs a niwa command with cwd set to
// <workspaceRoot>/<workspace>/<dir>. The directory must already exist; the
// root-scope scenarios point it at a directory under the root that is not an
// instance, which is the cwd apply and destroy must refuse.
func iRunFromDirectoryUnderWorkspace(ctx context.Context, command, dir, workspace string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cwd := filepath.Join(s.workspaceRoot, workspace, filepath.FromSlash(dir))
	if info, err := os.Stat(cwd); err != nil || !info.IsDir() {
		return ctx, fmt.Errorf("directory %s does not exist: %v", cwd, err)
	}
	return ctx, runNiwa(s, cwd, command)
}

// iRemovePathUnderWorkspaceRoot deletes <workspaceRoot>/<workspace>/<relPath>,
// so a later apply has something to converge and the scenario can tell
// whether it did.
func iRemovePathUnderWorkspaceRoot(ctx context.Context, relPath, workspace string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	base := filepath.Join(s.workspaceRoot, workspace)
	path := filepath.Join(base, filepath.FromSlash(relPath))
	if rel, err := filepath.Rel(base, path); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ctx, fmt.Errorf("refusing to remove %s: not strictly under %s", path, base)
	}
	if _, err := os.Stat(path); err != nil {
		return ctx, fmt.Errorf("expected %s to exist before removing it: %w", path, err)
	}
	if err := os.RemoveAll(path); err != nil {
		return ctx, fmt.Errorf("removing %s: %w", path, err)
	}
	return ctx, nil
}
