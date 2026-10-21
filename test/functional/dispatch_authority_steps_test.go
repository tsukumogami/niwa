package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"
)

// The authority scenarios check what a dispatch records about its authority
// level: the session mapping's two fields, the record inside the instance, and
// what `niwa list --json` reads back. The refusal half reuses the generic
// dispatch steps (no instance, no mapping) and the stderr line counter.

func registerDispatchAuthoritySteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the dispatch mapping for session "([^"]*)" records authority level "([^"]*)" from "([^"]*)"$`, theDispatchMappingRecordsAuthority)
	ctx.Step(`^the dispatch instance holds an authority record with level "([^"]*)" from "([^"]*)"$`, theDispatchInstanceHoldsAuthorityRecord)
	ctx.Step(`^the list JSON reports the dispatch instance at authority level "([^"]*)" from "([^"]*)"$`, theListJSONReportsAuthority)
}

// authorityPair reads authority_level and authority_source out of a JSON
// object, failing when either is absent or not a string.
func authorityPair(doc map[string]any, where string) (string, string, error) {
	level, ok1 := doc["authority_level"].(string)
	source, ok2 := doc["authority_source"].(string)
	if !ok1 || !ok2 {
		return "", "", fmt.Errorf("%s does not carry authority_level and authority_source as strings: %v", where, doc)
	}
	return level, source, nil
}

func theDispatchMappingRecordsAuthority(ctx context.Context, sessionID, level, source string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "sessions", sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading mapping %s: %w\nstderr:\n%s", path, err, s.stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing mapping %s: %w", path, err)
	}
	gotLevel, gotSource, err := authorityPair(doc, "mapping "+path)
	if err != nil {
		return err
	}
	if gotLevel != level || gotSource != source {
		return fmt.Errorf("mapping records %s from %s; want %s from %s", gotLevel, gotSource, level, source)
	}
	return nil
}

func theDispatchInstanceHoldsAuthorityRecord(ctx context.Context, level, source string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst := findDispatchInstance(s.workspaceRoot)
	if inst == "" {
		return fmt.Errorf("no dispatch instance under %s", s.workspaceRoot)
	}
	path := filepath.Join(inst, ".niwa", "authority.json")
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("the instance has no authority record: %w", err)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s has mode %v; want 0600", path, info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	gotLevel, gotSource, err := authorityPair(doc, path)
	if err != nil {
		return err
	}
	if gotLevel != level || gotSource != source {
		return fmt.Errorf("%s records %s from %s; want %s from %s", path, gotLevel, gotSource, level, source)
	}
	return nil
}

func theListJSONReportsAuthority(ctx context.Context, level, source string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	rec, err := dispatchListRecord(s)
	if err != nil {
		return err
	}
	gotLevel, gotSource, err := authorityPair(rec, "the dispatch instance's list record")
	if err != nil {
		return err
	}
	if gotLevel != level || gotSource != source {
		return fmt.Errorf("niwa list --json reports %s from %s; want %s from %s", gotLevel, gotSource, level, source)
	}
	return nil
}
