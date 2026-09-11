package workspace

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// readNormalizedDoc reads the JSON document at path with every occurrence of
// root replaced by a placeholder, so documents materialized under different
// temp directories compare equal when only their location differs. It and
// defaultModeOf are mirrored by readPostureDoc and postureDefaultMode in
// internal/cli/permissions_posture_test.go; test helpers can't cross
// packages, so a fix to one belongs in the other.
func readNormalizedDoc(t *testing.T, path, root string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(strings.ReplaceAll(string(data), root, "{ROOT}")), &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return doc
}

// defaultModeOf returns the document's permissions.defaultMode and whether it
// is present.
func defaultModeOf(doc map[string]any) (string, bool) {
	perms, ok := doc["permissions"].(map[string]any)
	if !ok {
		return "", false
	}
	mode, ok := perms["defaultMode"]
	if !ok {
		return "", false
	}
	s, _ := mode.(string)
	return s, true
}

// The declarations named S1-S9 in the expected-value table of
// docs/prds/PRD-inert-defaultmode-key.md, applied to newPostureFixture's
// workspace. Repo "app" plays the table's repo X.
var postureScenarios = []struct {
	name      string
	workspace string
	personal  string
	// posture is the instance's effective posture; instanceMode is the
	// instance-root document's permissions.defaultMode cell ("" = none).
	posture      string
	instanceMode string
}{
	{
		name:      "S1 workspace bypass",
		workspace: "\n[claude.settings]\npermissions = \"bypass\"\n",
		posture:   "bypass",
	},
	{
		name:         "S2 workspace ask",
		workspace:    "\n[claude.settings]\npermissions = \"ask\"\n",
		posture:      "ask",
		instanceMode: "default",
	},
	{
		name: "S3 undeclared",
	},
	{
		name: "S4 workspace bypass, repo ask",
		workspace: "\n[claude.settings]\npermissions = \"bypass\"\n" +
			"\n[repos.app.claude.settings]\npermissions = \"ask\"\n",
		posture: "bypass",
	},
	{
		name: "S5 workspace bypass, instance ask",
		workspace: "\n[claude.settings]\npermissions = \"bypass\"\n" +
			"\n[instance.claude.settings]\npermissions = \"ask\"\n",
		posture:      "ask",
		instanceMode: "default",
	},
	{
		name: "S6 workspace ask, instance bypass",
		workspace: "\n[claude.settings]\npermissions = \"ask\"\n" +
			"\n[instance.claude.settings]\npermissions = \"bypass\"\n",
		posture: "bypass",
	},
	{
		name: "S7 workspace ask, repo bypass",
		workspace: "\n[claude.settings]\npermissions = \"ask\"\n" +
			"\n[repos.app.claude.settings]\npermissions = \"bypass\"\n",
		posture:      "ask",
		instanceMode: "default",
	},
	{
		name:     "S8 personal overlay bypass",
		personal: "[workspaces.posture-ws.claude.settings]\npermissions = \"bypass\"\n",
		posture:  "bypass",
	},
	{
		name:         "S9 workspace bypass, personal overlay ask",
		workspace:    "\n[claude.settings]\npermissions = \"bypass\"\n",
		personal:     "[workspaces.posture-ws.claude.settings]\npermissions = \"ask\"\n",
		posture:      "ask",
		instanceMode: "default",
	},
}

// TestRecordedPostureMatchesInstanceRootDocument pins that the posture a real
// Create saves to instance state is the posture the instance-root settings
// document was written from. The two share an input, not a code path, so this
// is what keeps dispatch's reading of the state honest.
func TestRecordedPostureMatchesInstanceRootDocument(t *testing.T) {
	for _, tc := range postureScenarios {
		t.Run(tc.name, func(t *testing.T) {
			f := newPostureFixture(t, tc.personal)
			f.create(t, f.writeConfig(t, tc.workspace))

			recorded := f.recordedPosture(t)
			if recorded != tc.posture {
				t.Errorf("ClaudePermissions = %q, want %q", recorded, tc.posture)
			}

			doc := readNormalizedDoc(t, filepath.Join(f.instanceRoot, ".claude", "settings.json"), f.instanceRoot)
			mode, present := defaultModeOf(doc)
			if mode != tc.instanceMode || present != (tc.instanceMode != "") {
				t.Errorf("instance-root permissions.defaultMode = %q (present %v), want %q", mode, present, tc.instanceMode)
			}

			// The recorded posture, run through the same mapping, must
			// produce the document's cell.
			var fromRecorded string
			if recorded != "" {
				m, write, err := claudeDefaultMode(recorded)
				if err != nil {
					t.Fatalf("recorded posture %q is not a valid posture: %v", recorded, err)
				}
				if write {
					fromRecorded = m
				}
			}
			if fromRecorded != mode {
				t.Errorf("recorded posture %q maps to defaultMode %q, but the document has %q", recorded, fromRecorded, mode)
			}
		})
	}
}

// assertAskIsUndeclaredPlusDefault checks that the ask document equals the
// undeclared document plus permissions.defaultMode "default", every other key
// identical. The undeclared document must carry real content, so two empty
// documents can't pass. It strips defaultMode (and an emptied permissions
// block) from ask in place, so callers shouldn't inspect ask afterward.
func assertAskIsUndeclaredPlusDefault(t *testing.T, ask, undeclared map[string]any) {
	t.Helper()
	if len(undeclared) == 0 {
		t.Fatal("undeclared document is empty; the fixture must configure hooks and plugins")
	}
	if _, hasHooks := undeclared["hooks"]; !hasHooks {
		if _, hasPlugins := undeclared["enabledPlugins"]; !hasPlugins {
			t.Fatalf("undeclared document has neither hooks nor enabledPlugins: %v", undeclared)
		}
	}
	if mode, present := defaultModeOf(undeclared); present {
		t.Errorf("undeclared document has permissions.defaultMode %q, want none", mode)
	}
	mode, present := defaultModeOf(ask)
	if !present || mode != "default" {
		t.Fatalf("ask document permissions.defaultMode = %q (present %v), want \"default\"", mode, present)
	}
	perms := ask["permissions"].(map[string]any)
	delete(perms, "defaultMode")
	if len(perms) == 0 {
		delete(ask, "permissions")
	}
	if !reflect.DeepEqual(ask, undeclared) {
		t.Errorf("ask document minus defaultMode differs from the undeclared document:\nask:        %v\nundeclared: %v", ask, undeclared)
	}
}

// TestAskDocumentIsUndeclaredPlusDefault covers the four generated documents:
// under S2 (workspace ask) each equals its S3 (undeclared) counterpart plus
// permissions.defaultMode "default".
func TestAskDocumentIsUndeclaredPlusDefault(t *testing.T) {
	const askDecl = "\n[claude.settings]\npermissions = \"ask\"\n"
	// Hooks come from the convention directory; the plugin is declared.
	const pluginsDecl = "\n[claude]\nplugins = [\"shirabe@shirabe\"]\n"

	createDocs := func(t *testing.T, decl string) (instanceDoc, repoDoc map[string]any) {
		t.Helper()
		f := newPostureFixture(t, "")
		if err := os.MkdirAll(filepath.Join(f.niwaDir, "hooks", "pre_tool_use"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.niwaDir, "hooks", "pre_tool_use", "lint.sh"), []byte("#!/bin/sh\necho lint\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		f.create(t, f.writeConfig(t, pluginsDecl+decl))
		root := filepath.Dir(f.instanceRoot)
		instanceDoc = readNormalizedDoc(t, filepath.Join(f.instanceRoot, ".claude", "settings.json"), root)
		repoDoc = readNormalizedDoc(t, filepath.Join(f.instanceRoot, "default", "app", ".claude", "settings.local.json"), root)
		return instanceDoc, repoDoc
	}

	t.Run("instance root and repo", func(t *testing.T) {
		askInstance, askRepo := createDocs(t, askDecl)
		undInstance, undRepo := createDocs(t, "")
		t.Run("instance root", func(t *testing.T) { assertAskIsUndeclaredPlusDefault(t, askInstance, undInstance) })
		t.Run("repo", func(t *testing.T) { assertAskIsUndeclaredPlusDefault(t, askRepo, undRepo) })
	})

	t.Run("workspace root", func(t *testing.T) {
		rootDoc := func(settings config.SettingsConfig) map[string]any {
			plugins := []string{"shirabe@shirabe"}
			cfg := &config.WorkspaceConfig{
				Workspace: config.WorkspaceMeta{Name: "ws"},
				Claude: config.ClaudeConfig{
					Settings: settings,
					Plugins:  &plugins,
					// Track "main" keeps the build off network release resolution.
					Marketplaces: config.MarketplaceConfigs{{Source: "tsukumogami/shirabe", Track: "main"}},
				},
			}
			// Ephemeral mode adds the SessionStart hook.
			_, root := materializeRoot(t, cfg, RootMaterializeOptions{EphemeralSessionMode: true})
			return readNormalizedDoc(t, filepath.Join(root, rootClaudeDir, rootSettingsFile), root)
		}
		ask := rootDoc(config.SettingsConfig{"permissions": plainSetting("ask")})
		undeclared := rootDoc(nil)
		assertAskIsUndeclaredPlusDefault(t, ask, undeclared)
	})

	t.Run("worktree", func(t *testing.T) {
		worktreeDoc := func(settings config.SettingsConfig) map[string]any {
			cfg, configDir, instanceRoot, worktreePath := applyToWorktreeFixture(t)
			writeMarketplaceTree(t, filepath.Join(instanceRoot, ".niwa", "marketplaces", "tools"))
			plugins := []string{"shirabe@tools"}
			cfg.Claude.Plugins = &plugins
			cfg.Claude.Marketplaces = config.MarketplaceConfigs{{Source: "acme/tools"}}
			cfg.Claude.Settings = settings
			if _, err := ApplyToWorktree(cfg, configDir, instanceRoot, worktreePath, "apps", "app", "ship-the-thing", "branch-xyz", WorktreeApplyOptions{}); err != nil {
				t.Fatalf("ApplyToWorktree: %v", err)
			}
			return readNormalizedDoc(t, filepath.Join(worktreePath, ".claude", "settings.local.json"), filepath.Dir(configDir))
		}
		ask := worktreeDoc(config.SettingsConfig{"permissions": plainSetting("ask")})
		undeclared := worktreeDoc(nil)
		assertAskIsUndeclaredPlusDefault(t, ask, undeclared)
	})
}

// TestInvalidPostureFailsAtEveryLevel declares a Claude Code mode name where a
// niwa posture belongs, at each level a posture can be declared, and checks
// that Create fails naming the accepted values and that no settings document
// on disk carries the rejected value.
func TestInvalidPostureFailsAtEveryLevel(t *testing.T) {
	levels := []struct {
		name      string
		workspace func(v string) string
		personal  func(v string) string
	}{
		{
			name:      "workspace",
			workspace: func(v string) string { return "\n[claude.settings]\npermissions = \"" + v + "\"\n" },
		},
		{
			name:      "instance",
			workspace: func(v string) string { return "\n[instance.claude.settings]\npermissions = \"" + v + "\"\n" },
		},
		{
			name:      "repo",
			workspace: func(v string) string { return "\n[repos.app.claude.settings]\npermissions = \"" + v + "\"\n" },
		},
		{
			name:     "personal overlay",
			personal: func(v string) string { return "[workspaces.posture-ws.claude.settings]\npermissions = \"" + v + "\"\n" },
		},
	}
	for _, level := range levels {
		for _, value := range []string{"bypassPermissions", "auto", "acceptEdits"} {
			t.Run(level.name+"/"+value, func(t *testing.T) {
				var workspaceTOML, personalTOML string
				if level.workspace != nil {
					workspaceTOML = level.workspace(value)
				}
				if level.personal != nil {
					personalTOML = level.personal(value)
				}
				f := newPostureFixture(t, personalTOML)
				cfg := f.writeConfig(t, workspaceTOML)
				root := filepath.Dir(f.instanceRoot)

				_, err := f.applier.Create(context.Background(), cfg, f.niwaDir, root, cfg.Workspace.Name)
				if err == nil {
					t.Fatalf("Create accepted permissions = %q", value)
				}
				for _, want := range []string{`"bypass"`, `"ask"`} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not name %s", err.Error(), want)
					}
				}

				walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if d.IsDir() || (d.Name() != "settings.json" && d.Name() != "settings.local.json") {
						return nil
					}
					data, err := os.ReadFile(path)
					if err != nil {
						return err
					}
					if strings.Contains(string(data), `"`+value+`"`) {
						t.Errorf("%s carries the rejected value %q:\n%s", path, value, data)
					}
					return nil
				})
				if walkErr != nil {
					t.Fatalf("walking %s: %v", root, walkErr)
				}
			})
		}
	}
}
