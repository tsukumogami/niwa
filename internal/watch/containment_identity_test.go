package watch

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fullReviewDoc builds a settings document holding everything a review of the
// instance inst needs in the given mode, minus the hook named skip (if any), plus
// any extra PreToolUse entries.
func fullReviewDoc(inst string, sandbox, ask bool, skip string, extra ...any) map[string]any {
	doc := map[string]any{}
	if sandbox {
		doc["sandbox"] = noEgressSandboxStanza()
		if ask {
			doc["permissions"] = map[string]any{"defaultMode": "default"}
		}
	}
	var pre []any
	for _, h := range reviewHooks(inst, sandbox, ask) {
		if h.name != skip {
			pre = append(pre, h.build())
		}
	}
	pre = append(pre, extra...)
	doc["hooks"] = map[string]any{"PreToolUse": pre}
	return doc
}

// writeSettingsDoc writes doc as inst/.claude/settings.json.
func writeSettingsDoc(t *testing.T, inst string, doc map[string]any) {
	t.Helper()
	dir := filepath.Join(inst, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(dir, "settings.json"), doc)
}

// impostorCase is one entry that shares a review hook's matcher but is not that hook.
type impostorCase struct {
	name    string
	hook    string // the review hook it tries to stand in for
	matcher string
	command string
}

// impostorCases lists, for a mode, an "exit 0" impostor for every hook the mode
// needs, plus filesystem-guard entries built for another root or the other posture.
func impostorCases(inst string, sandbox, ask bool) []impostorCase {
	var cases []impostorCase
	for _, h := range reviewHooks(inst, sandbox, ask) {
		cases = append(cases, impostorCase{h.name + " exit 0", h.name, h.matcher, "exit 0"})
	}
	if sandbox {
		cases = append(cases,
			impostorCase{"filesystem-guard other root", "filesystem-guard", fsGuardMatcher, fsGuardCommand(inst+"-other", ask)},
			impostorCase{"filesystem-guard other posture", "filesystem-guard", fsGuardMatcher, fsGuardCommand(inst, !ask)},
		)
	}
	return cases
}

// TestReviewHooks_ImpostorCannotStandIn: for every review hook in every mode where it
// applies, an entry with niwa's matcher and a different command fails verification
// with an error naming the hook, and apply keeps it and adds niwa's hook beside it.
func TestReviewHooks_ImpostorCannotStandIn(t *testing.T) {
	for _, mode := range reviewModes {
		probe := t.TempDir()
		for _, ic := range impostorCases(probe, mode.sandbox, mode.ask) {
			t.Run(mode.name+"/"+ic.name, func(t *testing.T) {
				inst := t.TempDir()
				// Rebuild the case against this subtest's instance.
				command := strings.ReplaceAll(ic.command, probe, inst)
				impostor := commandHook(ic.matcher, command)
				doc := fullReviewDoc(inst, mode.sandbox, mode.ask, ic.hook, impostor)

				err := VerifyReviewSettings(doc, inst, mode.sandbox, mode.ask)
				if err == nil {
					t.Fatal("an impostor must not satisfy verification")
				}
				if !strings.Contains(err.Error(), ic.hook) {
					t.Errorf("error %q does not name the %s hook", err, ic.hook)
				}

				writeSettingsDoc(t, inst, doc)
				if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
					t.Fatalf("ApplyReviewSettings with an impostor present: %v", err)
				}
				got := readSettings(t, inst)
				if !hasPreToolUseHook(got, ic.matcher, command) {
					t.Error("apply must keep the impostor entry")
				}
				for _, h := range reviewHooks(inst, mode.sandbox, mode.ask) {
					if n := countPreToolUseHook(got, h.matcher, h.command); n != 1 {
						t.Errorf("%s: %d entries after apply, want 1", h.name, n)
					}
				}
				if err := VerifyReviewSettings(got, inst, mode.sandbox, mode.ask); err != nil {
					t.Errorf("settings must verify once niwa's hook is beside the impostor: %v", err)
				}
			})
		}
	}
}

// TestReviewHooks_ExtraHandlerKeysCannotStandIn: niwa's matcher and exact command
// with an extra handler key, niwa's command under another matcher, and niwa's entry
// under another hook event each fail verification, for every hook in every mode.
func TestReviewHooks_ExtraHandlerKeysCannotStandIn(t *testing.T) {
	extras := map[string]any{"async": true, "timeout": 0.001, "once": true, "if": "Bash(never)"}
	for _, mode := range reviewModes {
		inst := t.TempDir()
		for _, h := range reviewHooks(inst, mode.sandbox, mode.ask) {
			for key, val := range extras {
				variant := map[string]any{
					"matcher": h.matcher,
					"hooks": []any{map[string]any{
						"type":    "command",
						"command": h.command,
						key:       val,
					}},
				}
				doc := fullReviewDoc(inst, mode.sandbox, mode.ask, h.name, variant)
				if err := VerifyReviewSettings(doc, inst, mode.sandbox, mode.ask); err == nil {
					t.Errorf("%s/%s: an entry carrying %q must not satisfy verification", mode.name, h.name, key)
				}
			}

			other := commandHook(h.matcher+"|Read", h.command)
			if h.matcher == autoAllowMatcher {
				other = commandHook("Bash|Read", h.command)
			}
			doc := fullReviewDoc(inst, mode.sandbox, mode.ask, h.name, other)
			if err := VerifyReviewSettings(doc, inst, mode.sandbox, mode.ask); err == nil {
				t.Errorf("%s/%s: niwa's command under matcher %q must not satisfy verification", mode.name, h.name, other["matcher"])
			}

			doc = fullReviewDoc(inst, mode.sandbox, mode.ask, h.name)
			doc["hooks"].(map[string]any)["PostToolUse"] = []any{h.build()}
			if err := VerifyReviewSettings(doc, inst, mode.sandbox, mode.ask); err == nil {
				t.Errorf("%s/%s: niwa's entry under PostToolUse must not satisfy verification", mode.name, h.name)
			}
		}
	}
}

// TestApplyReviewSettings_ReapplyIsByteStable: re-applying to an unchanged instance
// leaves settings.json byte-identical, with every niwa hook present exactly once.
func TestApplyReviewSettings_ReapplyIsByteStable(t *testing.T) {
	for _, mode := range reviewModes {
		t.Run(mode.name, func(t *testing.T) {
			inst := t.TempDir()
			// Pre-existing content of the kinds a provisioned instance carries.
			writeSettingsDoc(t, inst, map[string]any{
				"permissions": map[string]any{"deny": []any{"Bash(rm:*)"}},
				"hooks": map[string]any{
					"PreToolUse":  []any{commandHook("Bash", "exit 0")},
					"PostToolUse": []any{commandHook("Edit", "true")},
				},
				"env": map[string]any{"A": "1", "B": 2.5},
			})
			path := filepath.Join(inst, ".claude", "settings.json")
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("first apply: %v", err)
			}
			first, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("second apply: %v", err)
			}
			second, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, second) {
				t.Errorf("re-apply changed settings.json:\nfirst:\n%s\nsecond:\n%s", first, second)
			}
			got := readSettings(t, inst)
			for _, h := range reviewHooks(inst, mode.sandbox, mode.ask) {
				if n := countPreToolUseHook(got, h.matcher, h.command); n != 1 {
					t.Errorf("%s: %d entries, want 1", h.name, n)
				}
			}
		})
	}
}

// TestReviewSettings_RefuseDisableAllHooks: disableAllHooks set to anything but
// false fails verification, and apply refuses it from either instance settings file
// without removing it.
func TestReviewSettings_RefuseDisableAllHooks(t *testing.T) {
	values := map[string]any{"true": true, "string": "false", "number": 0.0, "null": nil, "object": map[string]any{}}
	for _, mode := range reviewModes {
		inst := t.TempDir()
		for name, v := range values {
			doc := fullReviewDoc(inst, mode.sandbox, mode.ask, "")
			doc["disableAllHooks"] = v
			err := VerifyReviewSettings(doc, inst, mode.sandbox, mode.ask)
			if err == nil || !strings.Contains(err.Error(), "disableAllHooks") {
				t.Errorf("%s: disableAllHooks=%s: error = %v, want one naming disableAllHooks", mode.name, name, err)
			}
		}
		passing := fullReviewDoc(inst, mode.sandbox, mode.ask, "")
		if err := VerifyReviewSettings(passing, inst, mode.sandbox, mode.ask); err != nil {
			t.Errorf("%s: absent disableAllHooks must pass: %v", mode.name, err)
		}
		passing["disableAllHooks"] = false
		if err := VerifyReviewSettings(passing, inst, mode.sandbox, mode.ask); err != nil {
			t.Errorf("%s: disableAllHooks=false must pass: %v", mode.name, err)
		}

		for _, file := range []string{"settings.json", "settings.local.json"} {
			t.Run(mode.name+"/"+file, func(t *testing.T) {
				inst := t.TempDir()
				dir := filepath.Join(inst, ".claude")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, file)
				body := []byte(`{"disableAllHooks": true}`)
				if err := os.WriteFile(path, body, 0o644); err != nil {
					t.Fatal(err)
				}
				err := ApplyReviewSettings(inst, mode.sandbox, mode.ask)
				if err == nil || !strings.Contains(err.Error(), "disableAllHooks") {
					t.Fatalf("apply: error = %v, want one naming disableAllHooks", err)
				}
				after, rerr := os.ReadFile(path)
				if rerr != nil || !bytes.Equal(after, body) {
					t.Errorf("apply must leave %s as it was, got %q (err %v)", file, after, rerr)
				}
			})
		}

		t.Run(mode.name+"/false passes", func(t *testing.T) {
			inst := t.TempDir()
			dir := filepath.Join(inst, ".claude")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"settings.json", "settings.local.json"} {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(`{"disableAllHooks": false}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Errorf("disableAllHooks=false must not refuse: %v", err)
			}
		})
	}
}
