package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/watch"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// readPostureDoc parses the JSON document at path. A non-empty root is
// replaced by a placeholder first, so documents from workspaces in different
// temp directories compare equal when only their location differs. It and
// postureDefaultMode mirror readNormalizedDoc and defaultModeOf in
// internal/workspace/posture_documents_test.go; test helpers can't cross
// packages, so a fix to one belongs in the other.
func readPostureDoc(t *testing.T, path, root string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	text := string(data)
	if root != "" {
		text = strings.ReplaceAll(text, root, "{ROOT}")
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	return doc
}

// postureDefaultMode returns the document's permissions.defaultMode and
// whether it is present.
func postureDefaultMode(doc map[string]any) (string, bool) {
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

// repairWorkspaceTOML is S4 from the expected-value table in
// docs/prds/PRD-inert-defaultmode-key.md: the workspace declares bypass and
// repo-a overrides it with ask. The [[sources]] entry lists its repos
// explicitly, so the root apply's cascade never calls the GitHub API.
const repairWorkspaceTOML = `[workspace]
name = "repair-ws"

[[sources]]
org = "testorg"
repos = ["repo-a", "repo-b"]

[groups.default]
repos = ["repo-a", "repo-b"]

[claude.settings]
permissions = "bypass"

[repos.repo-a.claude.settings]
permissions = "ask"
`

// repairWorkspace is a workspace root with one created instance.
type repairWorkspace struct {
	root     string
	instance string
}

// newRepairWorkspace builds a multi-instance workspace (a workspace root plus
// one instance created through a real Applier.Create) and runs one root apply
// so every generated document exists.
func newRepairWorkspace(t *testing.T) repairWorkspace {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	root := canonicalTempDir(t)
	configDir := filepath.Join(root, config.ConfigDir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, config.ConfigFile)
	if err := os.WriteFile(configPath, []byte(repairWorkspaceTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, repo := range []string{"repo-a", "repo-b"} {
		if err := os.MkdirAll(filepath.Join(root, "repair-ws", "default", repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	applier := workspace.NewApplier(&stubRepoLister{})
	applier.Reporter = workspace.NewReporterWithTTY(io.Discard, false)
	applier.Cloner = &workspace.Cloner{}
	instance, err := applier.Create(context.Background(), loaded.Config, configDir, root, "repair-ws")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The fake checkouts have no remote to pull from.
	savedNoPull := applyNoPull
	t.Cleanup(func() { applyNoPull = savedNoPull })
	applyNoPull = true

	if err := runApplyAtRoot(t, root, false); err != nil {
		t.Fatalf("root apply: %v", err)
	}
	return repairWorkspace{root: root, instance: instance}
}

// TestRootApplyRepairsRetiredPermissionModes seeds the four generated
// documents with what an earlier niwa (or a hand edit) left behind, runs apply
// from the workspace root, and checks that each document is rewritten to its
// expected-value cell with every other key matching a fresh materialization.
func TestRootApplyRepairsRetiredPermissionModes(t *testing.T) {
	docs := []struct {
		name   string
		rel    func(w repairWorkspace) string
		seeded string
		// want is the S4 cell: "" means no permissions.defaultMode.
		want string
	}{
		{
			name:   "workspace root",
			rel:    func(w repairWorkspace) string { return filepath.Join(w.root, ".claude", "settings.json") },
			seeded: "bypassPermissions",
		},
		{
			name:   "instance root",
			rel:    func(w repairWorkspace) string { return filepath.Join(w.instance, ".claude", "settings.json") },
			seeded: "bypassPermissions",
		},
		{
			name: "repo A",
			rel: func(w repairWorkspace) string {
				return filepath.Join(w.instance, "default", "repo-a", ".claude", "settings.local.json")
			},
			seeded: "askPermissions",
			want:   "default",
		},
		{
			name: "repo B",
			rel: func(w repairWorkspace) string {
				return filepath.Join(w.instance, "default", "repo-b", ".claude", "settings.local.json")
			},
			seeded: "plan",
		},
	}

	repaired := newRepairWorkspace(t)
	for _, d := range docs {
		path := d.rel(repaired)
		doc := readPostureDoc(t, path, "")
		perms, _ := doc["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
		}
		perms["defaultMode"] = d.seeded
		doc["permissions"] = perms
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := runApplyAtRoot(t, repaired.root, false); err != nil {
		t.Fatalf("repair apply: %v", err)
	}

	fresh := newRepairWorkspace(t)

	for _, d := range docs {
		t.Run(d.name, func(t *testing.T) {
			got := readPostureDoc(t, d.rel(repaired), repaired.root)
			mode, present := postureDefaultMode(got)
			if mode == d.seeded {
				t.Errorf("seeded permissions.defaultMode %q survived the apply", d.seeded)
			}
			if mode != d.want || present != (d.want != "") {
				t.Errorf("permissions.defaultMode = %q (present %v), want %q", mode, present, d.want)
			}
			want := readPostureDoc(t, d.rel(fresh), fresh.root)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("repaired document differs from a fresh materialization:\ngot:  %v\nwant: %v", got, want)
			}
		})
	}
}

// TestReviewSettingsOverMaterializedPostures applies niwa watch's review
// settings, in both postures, on top of an instance materialized under S1
// (bypass), S2 (ask), and S3 (undeclared), and checks that verification
// passes and the instance root ends with the expected permission mode.
func TestReviewSettingsOverMaterializedPostures(t *testing.T) {
	scenarios := []struct {
		name string
		decl string
		// hardDenyMode is the instance-root cell the hard-deny posture leaves
		// in place: "" means no permissions.defaultMode.
		hardDenyMode string
	}{
		{name: "S1", decl: postureS1},
		{name: "S2", decl: postureS2, hardDenyMode: "default"},
		{name: "S3", decl: postureS3},
	}
	for _, sc := range scenarios {
		// operatorApproval is watch's review posture flag, not niwa's ask
		// posture (that's S2).
		for _, operatorApproval := range []bool{true, false} {
			posture := "hard-deny"
			if operatorApproval {
				posture = "operator-approval"
			}
			t.Run(sc.name+"/"+posture, func(t *testing.T) {
				root := setupDeclaredDispatchWorkspace(t, sc.decl)
				instance, err := createDeclaredInstance(context.Background(), root, "test-ws")
				if err != nil {
					t.Fatalf("Create: %v", err)
				}
				if err := watch.ApplyReviewSettings(instance, true, operatorApproval); err != nil {
					t.Fatalf("ApplyReviewSettings: %v", err)
				}
				merged := readPostureDoc(t, filepath.Join(instance, ".claude", "settings.json"), "")
				if err := watch.VerifyReviewSettings(merged, true, operatorApproval); err != nil {
					t.Fatalf("VerifyReviewSettings: %v", err)
				}
				want := sc.hardDenyMode
				if operatorApproval {
					want = "default"
				}
				mode, present := postureDefaultMode(merged)
				if mode != want || present != (want != "") {
					t.Errorf("instance-root permissions.defaultMode = %q (present %v), want %q", mode, present, want)
				}
			})
		}
	}
}
