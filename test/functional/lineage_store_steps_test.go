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
	ctx.Step(`^the dispatch's lineage entry records parent session "([^"]*)" and parent dispatch "([^"]*)"$`, theDispatchsLineageEntryRecordsSources)
	registerLineageCompletionSteps(ctx)
	registerLineageTreeSteps(ctx)
}

// theDispatchsLineageEntryRecordsSources reads the last dispatch's entry from
// the store and checks both links' sources. The parent session link carries
// the session the dispatching shell exported, and a parent dispatch link
// other than none names the first dispatch.
func theDispatchsLineageEntryRecordsSources(ctx context.Context, sessionSource, dispatchSource string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	id, err := lastDispatchID(s)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(s.workspaceRoot, ".niwa-lineage", id+".json"))
	if err != nil {
		return err
	}
	type link struct {
		ID     string `json:"id"`
		Source string `json:"source"`
	}
	var e struct {
		ParentSession  link `json:"parent_session"`
		ParentDispatch link `json:"parent_dispatch"`
	}
	if err := json.Unmarshal(data, &e); err != nil {
		return fmt.Errorf("entry is not JSON (%v):\n%s", err, data)
	}
	if e.ParentSession.Source != sessionSource || e.ParentSession.ID != s.envOverrides["CLAUDE_CODE_SESSION_ID"] {
		return fmt.Errorf("parent session = %+v, want source %s for %q\n%s", e.ParentSession, sessionSource, s.envOverrides["CLAUDE_CODE_SESSION_ID"], data)
	}
	if e.ParentDispatch.Source != dispatchSource {
		return fmt.Errorf("parent dispatch = %+v, want source %s\n%s", e.ParentDispatch, dispatchSource, data)
	}
	if dispatchSource == "none" {
		if e.ParentDispatch.ID != "" {
			return fmt.Errorf("parent dispatch none carries id %q", e.ParentDispatch.ID)
		}
		return nil
	}
	first, err := os.ReadFile(filepath.Join(s.authority.firstInstance, ".niwa", "authority.json"))
	if err != nil {
		return fmt.Errorf("reading the first dispatch's record: %w", err)
	}
	var rec struct {
		DispatchID string `json:"dispatch_id"`
	}
	if err := json.Unmarshal(first, &rec); err != nil || rec.DispatchID == "" || rec.DispatchID != e.ParentDispatch.ID {
		return fmt.Errorf("parent dispatch id %q, want the first dispatch's %q (%v)", e.ParentDispatch.ID, rec.DispatchID, err)
	}
	return nil
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
