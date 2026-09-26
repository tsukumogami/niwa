package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// registryRelPath mirrors the location pluginrecord locates relative to $HOME.
// The functional sandbox points HOME at s.homeDir, so seeding and asserting
// against <homeDir>/.claude/plugins/installed_plugins.json targets the same
// test registry the niwa binary mutates — never the developer's real one.
const pluginRegistryRelPath = ".claude/plugins/installed_plugins.json"

// registryDoc is the on-disk shape the test seeds and reads back. It mirrors
// the subset of Claude Code's installed_plugins.json that these scenarios
// assert on: a top-level "plugins" map of key -> list of records. Unmodelled
// fields are irrelevant to the assertions, so the test models only what it
// checks.
type registryDoc struct {
	Plugins map[string][]registryRecord `json:"plugins"`
}

type registryRecord struct {
	Scope       string `json:"scope,omitempty"`
	ProjectPath string `json:"projectPath,omitempty"`
	InstallPath string `json:"installPath,omitempty"`
}

// registerPluginRecordSteps installs the plugin-record lifecycle steps. It is
// called from suite_test.go's scenario initializer.
func registerPluginRecordSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the plugin registry has records:$`, thePluginRegistryHasRecords)
	ctx.Step(`^the plugin registry has (\d+) record for plugin "([^"]*)"$`, thePluginRegistryHasNRecordsForPlugin)
	ctx.Step(`^the plugin registry has (\d+) records for plugin "([^"]*)"$`, thePluginRegistryHasNRecordsForPlugin)
	ctx.Step(`^the plugin registry has a record with projectPath under instance "([^"]*)"$`, thePluginRegistryHasRecordUnderInstance)
	ctx.Step(`^the plugin registry has no record with projectPath under instance "([^"]*)"$`, thePluginRegistryHasNoRecordUnderInstance)
	ctx.Step(`^the plugin registry has a record with projectPath equal to HOME$`, thePluginRegistryHasRecordEqualToHome)
	ctx.Step(`^a plugin registry backup exists$`, aPluginRegistryBackupExists)
}

// registryPath returns the absolute path of the sandboxed registry.
func registryPath(s *testState) string {
	return filepath.Join(s.homeDir, filepath.FromSlash(pluginRegistryRelPath))
}

// expandRegistryPlaceholder resolves the placeholders the feature file uses in
// projectPath/installPath cells:
//
//	{home}                    -> the sandboxed $HOME
//	{instance:<ws>/<inst>}    -> <workspaceRoot>/<ws>/<inst> (an instance root)
//	{abs}/<rel>               -> <tmpDir>/<rel>, an absolute path guaranteed absent
//
// An absolute path with no placeholder is used verbatim.
func expandRegistryPlaceholder(s *testState, raw string) string {
	switch {
	case raw == "{home}":
		return s.homeDir
	case strings.HasPrefix(raw, "{instance:"):
		end := strings.IndexByte(raw, '}')
		spec := raw[len("{instance:"):end]
		rest := raw[end+1:]
		return filepath.Join(s.workspaceRoot, filepath.FromSlash(spec), filepath.FromSlash(rest))
	case strings.HasPrefix(raw, "{abs}"):
		rest := strings.TrimPrefix(raw, "{abs}")
		return filepath.Join(s.tmpDir, filepath.FromSlash(rest))
	default:
		return raw
	}
}

// thePluginRegistryHasRecords seeds <homeDir>/.claude/plugins/installed_plugins.json
// from a Gherkin data table. Columns: plugin, projectPath, installPath (scope
// optional). Rows sharing a plugin key are grouped into that key's record list.
func thePluginRegistryHasRecords(ctx context.Context, table *godog.Table) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if len(table.Rows) < 2 {
		return ctx, fmt.Errorf("plugin registry table needs a header row and at least one data row")
	}

	header := table.Rows[0].Cells
	colIdx := map[string]int{}
	for i, c := range header {
		colIdx[c.Value] = i
	}
	pluginCol, ok := colIdx["plugin"]
	if !ok {
		return ctx, fmt.Errorf("plugin registry table needs a 'plugin' column")
	}

	doc := registryDoc{Plugins: map[string][]registryRecord{}}
	for _, row := range table.Rows[1:] {
		cells := row.Cells
		plugin := cells[pluginCol].Value
		rec := registryRecord{}
		if i, ok := colIdx["scope"]; ok {
			rec.Scope = cells[i].Value
		} else {
			rec.Scope = "user"
		}
		if i, ok := colIdx["projectPath"]; ok && cells[i].Value != "" {
			rec.ProjectPath = expandRegistryPlaceholder(s, cells[i].Value)
		}
		if i, ok := colIdx["installPath"]; ok && cells[i].Value != "" {
			rec.InstallPath = expandRegistryPlaceholder(s, cells[i].Value)
		}
		doc.Plugins[plugin] = append(doc.Plugins[plugin], rec)
	}

	path := registryPath(s)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ctx, fmt.Errorf("creating registry dir: %w", err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ctx, fmt.Errorf("marshaling seed registry: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return ctx, fmt.Errorf("writing seed registry: %w", err)
	}
	return ctx, nil
}

// loadRegistry reads back the (possibly mutated) sandbox registry.
func loadRegistry(s *testState) (registryDoc, error) {
	var doc registryDoc
	data, err := os.ReadFile(registryPath(s))
	if err != nil {
		return doc, fmt.Errorf("reading registry at %s: %w", registryPath(s), err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("parsing registry: %w", err)
	}
	if doc.Plugins == nil {
		doc.Plugins = map[string][]registryRecord{}
	}
	return doc, nil
}

func thePluginRegistryHasNRecordsForPlugin(ctx context.Context, want int, plugin string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	doc, err := loadRegistry(s)
	if err != nil {
		return err
	}
	got := len(doc.Plugins[plugin])
	if got != want {
		return fmt.Errorf("plugin %q has %d record(s), want %d\nregistry: %+v", plugin, got, want, doc.Plugins)
	}
	return nil
}

func thePluginRegistryHasRecordUnderInstance(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	root := filepath.Join(s.workspaceRoot, instance)
	doc, err := loadRegistry(s)
	if err != nil {
		return err
	}
	if !anyRecordUnder(doc, root) {
		return fmt.Errorf("expected a record with projectPath under %s, none found\nregistry: %+v", root, doc.Plugins)
	}
	return nil
}

func thePluginRegistryHasNoRecordUnderInstance(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	root := filepath.Join(s.workspaceRoot, instance)
	doc, err := loadRegistry(s)
	if err != nil {
		return err
	}
	if anyRecordUnder(doc, root) {
		return fmt.Errorf("expected no record with projectPath under %s, but found one\nregistry: %+v", root, doc.Plugins)
	}
	return nil
}

// anyRecordUnder reports whether any record's projectPath lies within root,
// using cleaned-path containment (mirrors the instance-owned predicate so the
// assertion matches the production semantics).
func anyRecordUnder(doc registryDoc, root string) bool {
	cleanRoot := filepath.Clean(root)
	for _, recs := range doc.Plugins {
		for _, rec := range recs {
			if rec.ProjectPath == "" {
				continue
			}
			rel, err := filepath.Rel(cleanRoot, filepath.Clean(rec.ProjectPath))
			if err != nil {
				continue
			}
			if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
				continue
			}
			return true
		}
	}
	return false
}

func thePluginRegistryHasRecordEqualToHome(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	doc, err := loadRegistry(s)
	if err != nil {
		return err
	}
	want := filepath.Clean(s.homeDir)
	for _, recs := range doc.Plugins {
		for _, rec := range recs {
			if filepath.Clean(rec.ProjectPath) == want {
				return nil
			}
		}
	}
	return fmt.Errorf("expected a record with projectPath == HOME (%s), none found\nregistry: %+v", want, doc.Plugins)
}

// aPluginRegistryBackupExists asserts a timestamped backup sibling
// (installed_plugins.json.niwa-bak.<RFC3339>) exists next to the registry,
// proving Prune snapshotted before its first mutation (R11).
func aPluginRegistryBackupExists(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir := filepath.Dir(registryPath(s))
	prefix := filepath.Base(registryPath(s)) + ".niwa-bak."
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("listing registry dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			return nil
		}
	}
	return fmt.Errorf("expected a backup file with prefix %q in %s, none found", prefix, dir)
}
