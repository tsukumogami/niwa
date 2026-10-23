package functional

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"
)

// The completion scenarios remember one entry's bytes and check that nothing
// a later command does (a reap, another dispatch, an apply) changes them.

func registerLineageCompletionSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the dispatch's lineage entry records session "([^"]*)"$`, theDispatchsLineageEntryRecordsSession)
	ctx.Step(`^I remember the dispatch's lineage entry$`, iRememberTheDispatchsLineageEntry)
	ctx.Step(`^the remembered lineage entry is unchanged$`, theRememberedLineageEntryIsUnchanged)
}

// rememberedEntryKey keys the remembered entry in the scenario's context.
type rememberedEntryKey struct{}

type rememberedEntry struct {
	path string
	data []byte
}

func lastLineageEntryPath(s *testState) (string, error) {
	id, err := lastDispatchID(s)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.workspaceRoot, ".niwa-lineage", id+".json"), nil
}

func theDispatchsLineageEntryRecordsSession(ctx context.Context, session string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, err := lastLineageEntryPath(s)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var e struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(data, &e); err != nil || e.SessionID != session {
		return fmt.Errorf("session_id = %q, want %q (%v):\n%s", e.SessionID, session, err, data)
	}
	return nil
}

func iRememberTheDispatchsLineageEntry(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path, err := lastLineageEntryPath(s)
	if err != nil {
		return ctx, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, rememberedEntryKey{}, rememberedEntry{path: path, data: data}), nil
}

func theRememberedLineageEntryIsUnchanged(ctx context.Context) error {
	r, ok := ctx.Value(rememberedEntryKey{}).(rememberedEntry)
	if !ok {
		return fmt.Errorf("no lineage entry was remembered")
	}
	data, err := os.ReadFile(r.path)
	if err != nil {
		return fmt.Errorf("the remembered entry is gone: %w", err)
	}
	if !bytes.Equal(data, r.data) {
		return fmt.Errorf("the entry changed:\nbefore %s\nafter  %s", r.data, data)
	}
	return nil
}
