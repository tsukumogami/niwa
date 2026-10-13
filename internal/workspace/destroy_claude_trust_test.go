package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/claudetrust"
)

// Every test here records a grant naming a config file under t.TempDir, so no
// destroy can reach the developer's own Claude config.

// trustKey is the projects key Claude Code computes for dir.
func trustKey(t *testing.T, dir string) string {
	t.Helper()
	key, err := claudetrust.Key(dir)
	if err != nil {
		t.Fatalf("claudetrust.Key(%s): %v", dir, err)
	}
	return key
}

// writeClaudeConfig writes doc as a Claude config file at path.
func writeClaudeConfig(t *testing.T, path string, doc map[string]any) {
	t.Helper()
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// readClaudeConfig decodes the Claude config file at path.
func readClaudeConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	return doc
}

// projectsOf returns the projects map of a decoded Claude config.
func projectsOf(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	projects, ok := doc["projects"].(map[string]any)
	if !ok {
		t.Fatalf("config has no projects object: %v", doc)
	}
	return projects
}

// recordClaudeTrust stores g as instanceDir's recorded grant.
func recordClaudeTrust(t *testing.T, instanceDir string, g *claudetrust.Grant) {
	t.Helper()
	state, err := LoadState(instanceDir)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	state.ClaudeTrust = g
	if err := SaveState(instanceDir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
}

// mustRead returns the bytes of path.
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return data
}

// assertGone fails unless dir no longer exists.
func assertGone(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("instance directory %s should be removed, got err: %v", dir, err)
	}
}

// TestDestroyInstance_WithdrawsCreatedGrant checks that a grant recorded as
// created takes the whole entry with it and leaves every other project and
// top-level key alone.
func TestDestroyInstance_WithdrawsCreatedGrant(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "alpha")
	key := trustKey(t, inst)

	cfgPath := filepath.Join(t.TempDir(), ".claude.json")
	writeClaudeConfig(t, cfgPath, map[string]any{
		"numStartups": 7,
		"projects": map[string]any{
			key:            map[string]any{"hasTrustDialogAccepted": true},
			"/elsewhere/p": map[string]any{"hasTrustDialogAccepted": true},
		},
	})
	recordClaudeTrust(t, inst, &claudetrust.Grant{
		ConfigFile: cfgPath, Key: key, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceWorkspace,
	})

	var buf bytes.Buffer
	if err := DestroyInstance(inst, WithDestroyReporter(NewReporterWithTTY(&buf, false))); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	assertGone(t, inst)
	if buf.Len() != 0 {
		t.Errorf("unexpected output: %s", buf.String())
	}

	doc := readClaudeConfig(t, cfgPath)
	projects := projectsOf(t, doc)
	if _, ok := projects[key]; ok {
		t.Errorf("entry %s should be removed, projects = %v", key, projects)
	}
	if _, ok := projects["/elsewhere/p"]; !ok {
		t.Errorf("unrelated entry was removed, projects = %v", projects)
	}
	if doc["numStartups"] != float64(7) {
		t.Errorf("numStartups = %v, want 7", doc["numStartups"])
	}
}

// TestDestroyInstance_WithdrawsFlagOnlyGrant checks that a grant recorded as
// flag-only removes just the flag and keeps the keys Claude Code wrote into
// the entry.
func TestDestroyInstance_WithdrawsFlagOnlyGrant(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "alpha")
	key := trustKey(t, inst)

	cfgPath := filepath.Join(t.TempDir(), ".claude.json")
	writeClaudeConfig(t, cfgPath, map[string]any{
		"projects": map[string]any{
			key: map[string]any{
				"hasTrustDialogAccepted": true,
				"allowedTools":           []string{"Bash"},
				"lastCost":               1.5,
			},
		},
	})
	recordClaudeTrust(t, inst, &claudetrust.Grant{
		ConfigFile: cfgPath, Key: key, Mode: claudetrust.ModeFlagOnly, Source: claudetrust.SourceWorkspace,
	})

	if err := DestroyInstance(inst); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	assertGone(t, inst)

	entry, ok := projectsOf(t, readClaudeConfig(t, cfgPath))[key].(map[string]any)
	if !ok {
		t.Fatalf("flag-only withdrawal removed the whole entry for %s", key)
	}
	if _, ok := entry["hasTrustDialogAccepted"]; ok {
		t.Errorf("trust flag still present: %v", entry)
	}
	if _, ok := entry["allowedTools"]; !ok {
		t.Errorf("allowedTools was removed: %v", entry)
	}
	if entry["lastCost"] != 1.5 {
		t.Errorf("lastCost = %v, want 1.5", entry["lastCost"])
	}
}

// TestDestroyWorkspace_WithdrawsEachInstanceGrant checks that the
// workspace-wipe path withdraws every instance's recorded grant and nothing
// else.
func TestDestroyWorkspace_WithdrawsEachInstanceGrant(t *testing.T) {
	root := destroySetupWorkspace(t)
	a := destroySetupInstance(t, root, "alpha")
	b := destroySetupInstance(t, root, "beta")
	keyA, keyB := trustKey(t, a), trustKey(t, b)

	cfgPath := filepath.Join(t.TempDir(), ".claude.json")
	writeClaudeConfig(t, cfgPath, map[string]any{
		"projects": map[string]any{
			keyA:           map[string]any{"hasTrustDialogAccepted": true},
			keyB:           map[string]any{"hasTrustDialogAccepted": true, "allowedTools": []string{}},
			"/elsewhere/p": map[string]any{"hasTrustDialogAccepted": true},
		},
	})
	recordClaudeTrust(t, a, &claudetrust.Grant{
		ConfigFile: cfgPath, Key: keyA, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceWorkspace,
	})
	recordClaudeTrust(t, b, &claudetrust.Grant{
		ConfigFile: cfgPath, Key: keyB, Mode: claudetrust.ModeFlagOnly, Source: claudetrust.SourceReview,
	})

	if err := DestroyWorkspace(root, DestroyWorkspaceOpts{}); err != nil {
		t.Fatalf("DestroyWorkspace: %v", err)
	}
	assertGone(t, root)

	projects := projectsOf(t, readClaudeConfig(t, cfgPath))
	if _, ok := projects[keyA]; ok {
		t.Errorf("created entry for alpha should be removed, projects = %v", projects)
	}
	entryB, ok := projects[keyB].(map[string]any)
	if !ok {
		t.Fatalf("flag-only entry for beta should remain, projects = %v", projects)
	}
	if _, ok := entryB["hasTrustDialogAccepted"]; ok {
		t.Errorf("beta's trust flag still present: %v", entryB)
	}
	if _, ok := projects["/elsewhere/p"]; !ok {
		t.Errorf("unrelated entry was removed, projects = %v", projects)
	}
}

// TestDestroy_RefusesTamperedRecord checks that a record naming another
// path's key, or a file not named .claude.json, changes no file, is reported,
// and doesn't stop the destroy. Both destroy routes are covered.
func TestDestroy_RefusesTamperedRecord(t *testing.T) {
	tests := []struct {
		name string
		// grant builds the record from the keys of the instance being
		// destroyed and its sibling, and the two candidate files.
		grant func(instKey, siblingKey, claudeJSON, otherJSON string) *claudetrust.Grant
	}{
		{
			name: "key of another path",
			grant: func(_, siblingKey, claudeJSON, _ string) *claudetrust.Grant {
				return &claudetrust.Grant{
					ConfigFile: claudeJSON, Key: siblingKey, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceWorkspace,
				}
			},
		},
		{
			name: "config file not named .claude.json",
			grant: func(instKey, _, _, otherJSON string) *claudetrust.Grant {
				return &claudetrust.Grant{
					ConfigFile: otherJSON, Key: instKey, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceWorkspace,
				}
			},
		},
	}
	routes := []struct {
		name    string
		destroy func(root, inst string, r *Reporter) error
	}{
		{"DestroyInstance", func(_, inst string, r *Reporter) error {
			return DestroyInstance(inst, WithDestroyReporter(r))
		}},
		{"DestroyWorkspace", func(root, _ string, r *Reporter) error {
			return DestroyWorkspace(root, DestroyWorkspaceOpts{Reporter: r})
		}},
	}

	for _, tc := range tests {
		for _, route := range routes {
			t.Run(tc.name+"/"+route.name, func(t *testing.T) {
				root := destroySetupWorkspace(t)
				inst := destroySetupInstance(t, root, "alpha")
				sibling := destroySetupInstance(t, root, "beta")
				key, siblingKey := trustKey(t, inst), trustKey(t, sibling)

				cfgDir := t.TempDir()
				claudeJSON := filepath.Join(cfgDir, ".claude.json")
				otherJSON := filepath.Join(cfgDir, "settings.json")
				doc := map[string]any{
					"projects": map[string]any{
						key:        map[string]any{"hasTrustDialogAccepted": true},
						siblingKey: map[string]any{"hasTrustDialogAccepted": true},
					},
				}
				writeClaudeConfig(t, claudeJSON, doc)
				writeClaudeConfig(t, otherJSON, doc)
				beforeClaude, beforeOther := mustRead(t, claudeJSON), mustRead(t, otherJSON)

				recordClaudeTrust(t, inst, tc.grant(key, siblingKey, claudeJSON, otherJSON))

				var buf bytes.Buffer
				if err := route.destroy(root, inst, NewReporterWithTTY(&buf, false)); err != nil {
					t.Fatalf("destroy: %v", err)
				}
				assertGone(t, inst)

				if !bytes.Equal(mustRead(t, claudeJSON), beforeClaude) {
					t.Errorf("%s was modified", claudeJSON)
				}
				if !bytes.Equal(mustRead(t, otherJSON), beforeOther) {
					t.Errorf("%s was modified", otherJSON)
				}
				out := buf.String()
				if !strings.Contains(out, "warning: instance alpha: ") || !strings.Contains(out, "left in place") {
					t.Errorf("expected a left-in-place warning naming instance alpha, got:\n%s", out)
				}
			})
		}
	}
}

// TestDestroyInstance_UnparseableConfigWarns checks that a config file that
// isn't JSON is left byte-identical, the warning names the file and key, and
// the destroy completes.
func TestDestroyInstance_UnparseableConfigWarns(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "alpha")
	key := trustKey(t, inst)

	cfgPath := filepath.Join(t.TempDir(), ".claude.json")
	garbage := []byte("{ this is not valid json")
	if err := os.WriteFile(cfgPath, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	recordClaudeTrust(t, inst, &claudetrust.Grant{
		ConfigFile: cfgPath, Key: key, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceWorkspace,
	})

	var buf bytes.Buffer
	if err := DestroyInstance(inst, WithDestroyReporter(NewReporterWithTTY(&buf, false))); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	assertGone(t, inst)

	if !bytes.Equal(mustRead(t, cfgPath), garbage) {
		t.Errorf("unparseable config was modified")
	}
	out := buf.String()
	if !strings.Contains(out, "warning:") {
		t.Errorf("expected a warning, got:\n%s", out)
	}
	if !strings.Contains(out, cfgPath) {
		t.Errorf("warning should name the file %s, got:\n%s", cfgPath, out)
	}
	if !strings.Contains(out, key) {
		t.Errorf("warning should name the key %s, got:\n%s", key, out)
	}
}

// TestDestroyInstance_NoRecordLeavesEntry checks that an entry at the
// instance's path with no grant recorded for it is the developer's own and
// survives the destroy byte-identical.
func TestDestroyInstance_NoRecordLeavesEntry(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "alpha")
	key := trustKey(t, inst)

	// The entry sits where Claude Code would read it, under a throwaway HOME.
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgPath := filepath.Join(home, ".claude.json")
	writeClaudeConfig(t, cfgPath, map[string]any{
		"projects": map[string]any{key: map[string]any{"hasTrustDialogAccepted": true}},
	})
	before := mustRead(t, cfgPath)

	var buf bytes.Buffer
	if err := DestroyInstance(inst, WithDestroyReporter(NewReporterWithTTY(&buf, false))); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	assertGone(t, inst)

	if !bytes.Equal(mustRead(t, cfgPath), before) {
		t.Errorf("unrecorded entry's config file was modified")
	}
	if buf.Len() != 0 {
		t.Errorf("unexpected output: %s", buf.String())
	}
}

// TestDestroyInstance_MissingConfigIsSilent checks that a grant whose config
// file no longer exists is nothing to withdraw: no warning, no file created.
func TestDestroyInstance_MissingConfigIsSilent(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "alpha")

	cfgPath := filepath.Join(t.TempDir(), ".claude.json")
	recordClaudeTrust(t, inst, &claudetrust.Grant{
		ConfigFile: cfgPath, Key: trustKey(t, inst), Mode: claudetrust.ModeCreated, Source: claudetrust.SourceWorkspace,
	})

	var buf bytes.Buffer
	if err := DestroyInstance(inst, WithDestroyReporter(NewReporterWithTTY(&buf, false))); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	assertGone(t, inst)
	if buf.Len() != 0 {
		t.Errorf("unexpected output: %s", buf.String())
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Errorf("withdrawal created %s: %v", cfgPath, err)
	}
}

// TestDestroyInstance_NilReporterDropsWarning checks that a refused withdrawal
// with no reporter neither panics nor fails the destroy.
func TestDestroyInstance_NilReporterDropsWarning(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "alpha")

	cfgPath := filepath.Join(t.TempDir(), ".claude.json")
	writeClaudeConfig(t, cfgPath, map[string]any{"projects": map[string]any{}})
	recordClaudeTrust(t, inst, &claudetrust.Grant{
		ConfigFile: cfgPath, Key: "/not/this/instance", Mode: claudetrust.ModeCreated, Source: claudetrust.SourceWorkspace,
	})

	if err := DestroyInstance(inst); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	assertGone(t, inst)
}

// TestDestroyInstance_UnreadableStateWarns checks that an instance state that
// can't be parsed is reported: a grant it records is about to become
// unreachable, so the destroy says so rather than staying silent.
func TestDestroyInstance_UnreadableStateWarns(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "alpha")
	if err := os.WriteFile(filepath.Join(inst, StateDir, StateFile), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := DestroyInstance(inst, WithDestroyReporter(NewReporterWithTTY(&buf, false))); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	assertGone(t, inst)

	out := buf.String()
	for _, want := range []string{"warning:", "Claude trust", "parsing instance state"} {
		if !strings.Contains(out, want) {
			t.Errorf("warning lacks %q, got:\n%s", want, out)
		}
	}
}

// TestDestroyInstance_UnresolvableRootWarns checks that when the instance root
// can't be resolved, the warning says so and carries the cause, and the
// recorded entry is left alone.
func TestDestroyInstance_UnresolvableRootWarns(t *testing.T) {
	root := destroySetupWorkspace(t)
	inst := destroySetupInstance(t, root, "alpha")
	key := trustKey(t, inst)

	cfgPath := filepath.Join(t.TempDir(), ".claude.json")
	writeClaudeConfig(t, cfgPath, map[string]any{
		"projects": map[string]any{key: map[string]any{"hasTrustDialogAccepted": true}},
	})
	recordClaudeTrust(t, inst, &claudetrust.Grant{
		ConfigFile: cfgPath, Key: key, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceWorkspace,
	})
	before := mustRead(t, cfgPath)

	prev := claudeTrustKey
	claudeTrustKey = func(string) (string, error) { return "", errors.New("lstat failed") }
	t.Cleanup(func() { claudeTrustKey = prev })

	var buf bytes.Buffer
	if err := DestroyInstance(inst, WithDestroyReporter(NewReporterWithTTY(&buf, false))); err != nil {
		t.Fatalf("DestroyInstance: %v", err)
	}
	assertGone(t, inst)

	if !bytes.Equal(mustRead(t, cfgPath), before) {
		t.Errorf("config file was modified")
	}
	out := buf.String()
	for _, want := range []string{"warning:", "instance root could not be resolved", "lstat failed", cfgPath, key} {
		if !strings.Contains(out, want) {
			t.Errorf("warning lacks %q, got:\n%s", want, out)
		}
	}
}
