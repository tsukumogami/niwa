package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/cucumber/godog"
)

// The lineage-store scenarios check the entry a dispatch writes into
// <root>/.niwa-lineage/ and read it back through `niwa lineage show`. The
// dispatch id comes from the dispatch instance's authority record, which is
// written beside the entry with the same id.

func registerLineageStoreSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^I run "niwa lineage show" for the dispatch with "([^"]*)"$`, iRunLineageShowForTheDispatch)
	ctx.Step(`^the lineage show output is the dispatch's entry with every link none$`, theLineageShowOutputIsTheDispatchsEntry)
	ctx.Step(`^the dispatch's lineage entry is private to its owner$`, theDispatchsLineageEntryIsPrivate)
	ctx.Step(`^nothing under the workspace root's \.niwa names the dispatch$`, nothingUnderTheRootsNiwaNamesTheDispatch)
}

// lastDispatchID reads the dispatch id from the last dispatch instance's
// authority record.
func lastDispatchID(s *testState) (string, error) {
	data, err := lastRecord(s)
	if err != nil {
		return "", err
	}
	var rec struct {
		DispatchID string `json:"dispatch_id"`
	}
	if err := json.Unmarshal(data, &rec); err != nil || rec.DispatchID == "" {
		return "", fmt.Errorf("no dispatch id in the authority record (%v):\n%s", err, data)
	}
	return rec.DispatchID, nil
}

func iRunLineageShowForTheDispatch(ctx context.Context, flags string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	id, err := lastDispatchID(s)
	if err != nil {
		return ctx, err
	}
	return ctx, runNiwa(s, s.workspaceRoot, strings.TrimSpace("niwa lineage show "+id+" "+flags))
}

func theLineageShowOutputIsTheDispatchsEntry(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	id, err := lastDispatchID(s)
	if err != nil {
		return err
	}
	var doc struct {
		Schema               string                   `json:"schema"`
		Chain                []map[string]interface{} `json:"chain"`
		Stop                 string                   `json:"stop"`
		Matched              int                      `json:"matched"`
		Matches              []string                 `json:"matches"`
		InstanceNameFallback bool                     `json:"instance_name_fallback"`
	}
	if err := json.Unmarshal([]byte(s.stdout), &doc); err != nil {
		return fmt.Errorf("lineage show --json output is not JSON (%v):\n%s%s", err, s.stdout, s.stderr)
	}
	if doc.Schema != "niwa.lineage.chain/v1" || doc.Stop != "root" || doc.Matched != 1 ||
		!reflect.DeepEqual(doc.Matches, []string{id}) || doc.InstanceNameFallback || len(doc.Chain) != 1 {
		return fmt.Errorf("unexpected chain document:\n%s", s.stdout)
	}
	e := doc.Chain[0]
	none := map[string]interface{}{"id": "", "source": "none"}
	checks := map[string]interface{}{
		"schema":          "niwa.lineage.entry/v1",
		"dispatch_id":     id,
		"instance":        filepath.Base(s.lastDispatchInstancePath),
		"agent":           "claude",
		"origin":          "dispatch",
		"parent_session":  none,
		"parent_dispatch": none,
		"principal":       map[string]interface{}{"name": "", "source": "none", "trust": "observed"},
	}
	for k, want := range checks {
		if !reflect.DeepEqual(e[k], want) {
			return fmt.Errorf("entry %s = %v, want %v\n%s", k, e[k], want, s.stdout)
		}
	}
	if w, _ := e["written_at"].(string); !strings.HasSuffix(w, "Z") {
		return fmt.Errorf("written_at = %v", e["written_at"])
	}
	return nil
}

func theDispatchsLineageEntryIsPrivate(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	id, err := lastDispatchID(s)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.workspaceRoot, ".niwa-lineage")
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, id+".json"): 0o600} {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if got := info.Mode().Perm(); got != want {
			return fmt.Errorf("%s mode = %o, want %o", path, got, want)
		}
	}
	return nil
}

// nothingUnderTheRootsNiwaNamesTheDispatch checks the entry was written to
// the store and not into the workspace configuration directory: no file
// under <root>/.niwa has the dispatch id or "lineage" in its name.
func nothingUnderTheRootsNiwaNamesTheDispatch(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	id, err := lastDispatchID(s)
	if err != nil {
		return err
	}
	return filepath.WalkDir(filepath.Join(s.workspaceRoot, ".niwa"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(d.Name(), id) || strings.Contains(d.Name(), "lineage") {
			return fmt.Errorf("%s names the dispatch", path)
		}
		return nil
	})
}
