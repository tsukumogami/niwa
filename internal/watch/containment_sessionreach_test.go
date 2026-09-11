package watch

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// reviewModes lists every (sandbox, ask) combination watch launches a review in.
var reviewModes = []struct {
	name         string
	sandbox, ask bool
}{
	{"no-sandbox", false, false},
	{"sandbox-hard-deny", true, false},
	{"sandbox-ask", true, true},
}

// reviewToolNames are the tools review sessions rely on. The session-reach deny
// matcher must not overlap any of them under exact or substring comparison.
var reviewToolNames = []string{
	"Bash", "Read", "Glob", "Grep", "Write", "Edit", "MultiEdit", "NotebookEdit", "WebFetch", "WebSearch",
}

// reviewDocFor builds a hand-made settings document holding everything the given
// mode requires except the session-reach deny hook, plus any extra PreToolUse
// entries.
func reviewDocFor(sandbox, ask bool, extra ...any) map[string]any {
	pre := []any{postGuardHook()}
	doc := map[string]any{}
	if sandbox {
		doc["sandbox"] = noEgressSandboxStanza()
		pre = append(pre, egressDenyHook(), fsGuardHook("/inst", ask))
		if ask {
			doc["permissions"] = map[string]any{"defaultMode": "default"}
			pre = append(pre, autoAllowHook())
		}
	}
	pre = append(pre, extra...)
	doc["hooks"] = map[string]any{"PreToolUse": pre}
	return doc
}

// writeJSON encodes v as JSON into path.
func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("encoding %s: %v", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// commandHook builds a PreToolUse entry with one command hook.
func commandHook(matcher, command string) map[string]any {
	return map[string]any{
		"matcher": matcher,
		"hooks":   []any{map[string]any{"type": "command", "command": command}},
	}
}

// matcherTokens splits a matcher on "|".
func matcherTokens(matcher string) []string {
	return strings.Split(matcher, "|")
}

// TestSessionReachDenyMatcher_Form pins the matcher to a plain list of tool names,
// which Claude Code compares as exact names rather than as a substring or regex, and
// pins the set of names.
func TestSessionReachDenyMatcher_Form(t *testing.T) {
	if !regexp.MustCompile(`^[A-Za-z0-9_]+(\|[A-Za-z0-9_]+)*$`).MatchString(sessionReachDenyMatcher) {
		t.Fatalf("sessionReachDenyMatcher %q must be tool names joined by |, with no regex characters, spaces, or empty alternatives", sessionReachDenyMatcher)
	}
	tokens := matcherTokens(sessionReachDenyMatcher)
	seen := map[string]bool{}
	for _, tok := range tokens {
		if seen[tok] {
			t.Errorf("duplicate token %q in sessionReachDenyMatcher", tok)
		}
		seen[tok] = true
	}
	got := append([]string(nil), tokens...)
	sort.Strings(got)
	want := []string{"ListAgents", "RemoteTrigger", "SendFile", "SendMessage"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sessionReachDenyMatcher tokens = %v, want %v", got, want)
	}
	for _, tok := range tokens {
		for _, tool := range reviewToolNames {
			if strings.Contains(tool, tok) || strings.Contains(tok, tool) {
				t.Errorf("token %q overlaps review tool %q; the deny hook would fire for normal review work", tok, tool)
			}
		}
	}
}

// TestSessionReachDenyHook_Shape pins the entry: exactly a matcher and one command hook.
func TestSessionReachDenyHook_Shape(t *testing.T) {
	h := sessionReachDenyHook()
	if len(h) != 2 {
		t.Fatalf("sessionReachDenyHook must have exactly two keys (matcher, hooks), got %v", h)
	}
	if m, _ := h["matcher"].(string); m != sessionReachDenyMatcher {
		t.Errorf("matcher = %q, want %q", m, sessionReachDenyMatcher)
	}
	inner, ok := h["hooks"].([]any)
	if !ok || len(inner) != 1 {
		t.Fatalf("hooks must hold exactly one entry, got %v", h["hooks"])
	}
	entry, ok := inner[0].(map[string]any)
	if !ok {
		t.Fatalf("hook entry must be a map, got %T", inner[0])
	}
	if typ, _ := entry["type"].(string); typ != "command" {
		t.Errorf("hook type = %q, want command", typ)
	}
	if cmd, _ := entry["command"].(string); cmd == "" {
		t.Error("hook command must be a non-empty string")
	}
	if sessionReachDenyMessage != "niwa watch: review sessions don't reach other sessions" {
		t.Errorf("refusal message changed: %q", sessionReachDenyMessage)
	}
}

// TestSessionReachDenyHook_CommandRefuses runs the command the applied settings carry
// through sh, once per denied tool and once with empty stdin, in every mode. Each run
// must exit 2 with exactly the refusal line on stderr and nothing on stdout.
func TestSessionReachDenyHook_CommandRefuses(t *testing.T) {
	for _, mode := range reviewModes {
		t.Run(mode.name, func(t *testing.T) {
			inst := t.TempDir()
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("ApplyReviewSettings: %v", err)
			}
			got := readSettings(t, inst)
			cmd := preToolUseCommand(t, got, sessionReachDenyMatcher)
			if cmd == "" {
				t.Fatal("applied settings carry no session-reach deny command")
			}

			// The hook that blocks the four tools never fires for a normal review read.
			for _, tok := range matcherTokens(sessionReachDenyMatcher) {
				if tok == "Read" {
					t.Error("Read must not be a token of the session-reach deny matcher")
				}
			}

			payloads := map[string]string{
				"empty stdin": "",
				// Larger than a pipe buffer, so a hook that exits without reading
				// would leave the writer blocked or failing.
				"large payload": `{"hook_event_name":"PreToolUse","tool_name":"SendMessage","tool_input":{"message":"` + strings.Repeat("x", 300*1024) + `"}}`,
			}
			for _, tool := range matcherTokens(sessionReachDenyMatcher) {
				payloads[tool] = `{"hook_event_name":"PreToolUse","tool_name":"` + tool + `","tool_input":{}}`
			}
			for name, payload := range payloads {
				c := exec.Command("sh", "-c", cmd)
				c.Stdin = strings.NewReader(payload)
				var stdout, stderr bytes.Buffer
				c.Stdout, c.Stderr = &stdout, &stderr
				code := 0
				if err := c.Run(); err != nil {
					ee, ok := err.(*exec.ExitError)
					if !ok {
						t.Fatalf("%s: running hook: %v", name, err)
					}
					code = ee.ExitCode()
				}
				if code != 2 {
					t.Errorf("%s: exit code = %d, want 2", name, code)
				}
				if stderr.String() != sessionReachDenyMessage+"\n" {
					t.Errorf("%s: stderr = %q, want %q", name, stderr.String(), sessionReachDenyMessage+"\n")
				}
				if stdout.Len() != 0 {
					t.Errorf("%s: stdout must be empty, got %q", name, stdout.String())
				}
			}
		})
	}
}

// TestApplyReviewSettings_SessionReachDenyOnlyUnderPreToolUse asserts the entry lands
// under PreToolUse and under no other hook event, even when other events exist.
func TestApplyReviewSettings_SessionReachDenyOnlyUnderPreToolUse(t *testing.T) {
	for _, mode := range reviewModes {
		t.Run(mode.name, func(t *testing.T) {
			inst := t.TempDir()
			claudeDir := filepath.Join(inst, ".claude")
			if err := os.MkdirAll(claudeDir, 0o755); err != nil {
				t.Fatal(err)
			}
			existing := `{"hooks": {
			  "PostToolUse": [{"matcher": "Edit", "hooks": []}],
			  "UserPromptSubmit": [{"hooks": []}]
			}}`
			if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(existing), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("ApplyReviewSettings: %v", err)
			}
			got := readSettings(t, inst)
			if n := countPreToolUseMatcher(t, got, sessionReachDenyMatcher); n != 1 {
				t.Errorf("PreToolUse must hold the deny hook exactly once, got %d", n)
			}
			hooks := got["hooks"].(map[string]any)
			for event, raw := range hooks {
				if event == "PreToolUse" {
					continue
				}
				entries, _ := raw.([]any)
				for _, e := range entries {
					m, _ := e.(map[string]any)
					if s, _ := m["matcher"].(string); s == sessionReachDenyMatcher {
						t.Errorf("hook event %q must not hold the session-reach deny entry", event)
					}
				}
			}
		})
	}
}

// withoutSessionReachDeny returns a copy of the PreToolUse entries with every
// session-reach deny entry removed.
func withoutSessionReachDeny(t *testing.T, settings map[string]any) map[string]any {
	t.Helper()
	hooks := settings["hooks"].(map[string]any)
	var kept []any
	for _, e := range hooks["PreToolUse"].([]any) {
		m, _ := e.(map[string]any)
		if s, _ := m["matcher"].(string); s == sessionReachDenyMatcher {
			continue
		}
		kept = append(kept, e)
	}
	hooks["PreToolUse"] = kept
	return settings
}

// TestApplyReviewSettings_SessionReachDenyEveryMode applies and verifies the deny hook
// in every mode, and shows that dropping it fails verification in that same mode.
func TestApplyReviewSettings_SessionReachDenyEveryMode(t *testing.T) {
	for _, mode := range reviewModes {
		t.Run(mode.name, func(t *testing.T) {
			inst := t.TempDir()
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("ApplyReviewSettings: %v", err)
			}
			got := readSettings(t, inst)
			if n := countPreToolUseMatcher(t, got, sessionReachDenyMatcher); n != 1 {
				t.Errorf("deny hook must be present exactly once, got %d", n)
			}
			if err := VerifyReviewSettings(got, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("applied settings must verify: %v", err)
			}
			err := VerifyReviewSettings(withoutSessionReachDeny(t, got), mode.sandbox, mode.ask)
			if err == nil {
				t.Fatal("settings without the deny hook must fail verification")
			}
			if want := `session-reach deny PreToolUse hook (matcher "` + sessionReachDenyMatcher + `") missing`; !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to contain %q", err, want)
			}
		})
	}
}

// TestSessionReachDeny_ImpostorCannotStandIn covers a same-matcher hook that runs a
// different command: apply keeps it and adds niwa's hook beside it, and a document
// holding only the impostor, or niwa's command under a different matcher, fails
// verification in every mode.
func TestSessionReachDeny_ImpostorCannotStandIn(t *testing.T) {
	for _, mode := range reviewModes {
		t.Run(mode.name, func(t *testing.T) {
			inst := t.TempDir()
			claudeDir := filepath.Join(inst, ".claude")
			if err := os.MkdirAll(claudeDir, 0o755); err != nil {
				t.Fatal(err)
			}
			existing := `{"hooks": {"PreToolUse": [
			  {"matcher": "SendMessage|SendFile|RemoteTrigger|ListAgents", "hooks": [{"type": "command", "command": "exit 0"}]}
			]}}`
			if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(existing), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("ApplyReviewSettings with an impostor present: %v", err)
			}
			got := readSettings(t, inst)
			if n := countPreToolUseMatcher(t, got, sessionReachDenyMatcher); n != 2 {
				t.Errorf("impostor and niwa's hook must both be present, got %d entries with the matcher", n)
			}
			if !hasPreToolUseHook(got, sessionReachDenyMatcher, "exit 0") {
				t.Error("the pre-existing impostor entry must be kept")
			}
			if err := VerifyReviewSettings(got, mode.sandbox, mode.ask); err != nil {
				t.Errorf("settings with niwa's hook beside the impostor must verify: %v", err)
			}

			// Sanity: the hand-built document verifies once niwa's hook is present.
			if err := VerifyReviewSettings(reviewDocFor(mode.sandbox, mode.ask, sessionReachDenyHook()), mode.sandbox, mode.ask); err != nil {
				t.Fatalf("hand-built doc with niwa's hook must verify: %v", err)
			}
			impostorOnly := reviewDocFor(mode.sandbox, mode.ask, commandHook(sessionReachDenyMatcher, "exit 0"))
			if err := VerifyReviewSettings(impostorOnly, mode.sandbox, mode.ask); err == nil {
				t.Error("a same-matcher hook with a different command must not satisfy verification")
			}
			wrongMatcher := reviewDocFor(mode.sandbox, mode.ask, commandHook("SendMessage", sessionReachDenyCommand()))
			if err := VerifyReviewSettings(wrongMatcher, mode.sandbox, mode.ask); err == nil {
				t.Error("niwa's command under a different matcher must not satisfy verification")
			}
		})
	}
}

// TestSessionReachDeny_ExtraHandlerKeysCannotStandIn covers an entry with niwa's
// matcher and exact command plus a handler key Claude Code honors (async, once, if,
// timeout), any of which can keep it from blocking. It must not satisfy the verify
// check, and apply must add niwa's plain hook beside it.
func TestSessionReachDeny_ExtraHandlerKeysCannotStandIn(t *testing.T) {
	extras := map[string]any{"async": true, "once": true, "if": "Bash(never)", "timeout": 0.001}
	for key, val := range extras {
		t.Run(key, func(t *testing.T) {
			variant := map[string]any{
				"matcher": sessionReachDenyMatcher,
				"hooks": []any{map[string]any{
					"type":    "command",
					"command": sessionReachDenyCommand(),
					key:       val,
				}},
			}
			for _, mode := range reviewModes {
				if err := VerifyReviewSettings(reviewDocFor(mode.sandbox, mode.ask, variant), mode.sandbox, mode.ask); err == nil {
					t.Errorf("%s: a deny entry carrying %q must not satisfy verification", mode.name, key)
				}
			}

			inst := t.TempDir()
			claudeDir := filepath.Join(inst, ".claude")
			if err := os.MkdirAll(claudeDir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeJSON(t, filepath.Join(claudeDir, "settings.json"), map[string]any{
				"hooks": map[string]any{"PreToolUse": []any{variant}},
			})
			if err := ApplyReviewSettings(inst, false, false); err != nil {
				t.Fatalf("ApplyReviewSettings with a %q variant present: %v", key, err)
			}
			if n := countPreToolUseMatcher(t, readSettings(t, inst), sessionReachDenyMatcher); n != 2 {
				t.Errorf("apply must keep the %q variant and add niwa's plain hook, got %d entries", key, n)
			}
		})
	}
}

// TestApplyReviewSettings_SessionReachDenyDedupe re-applies in every mode and expects a
// single deny entry.
func TestApplyReviewSettings_SessionReachDenyDedupe(t *testing.T) {
	for _, mode := range reviewModes {
		t.Run(mode.name, func(t *testing.T) {
			inst := t.TempDir()
			for i := 0; i < 2; i++ {
				if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
					t.Fatalf("apply %d: %v", i+1, err)
				}
			}
			if n := countPreToolUseMatcher(t, readSettings(t, inst), sessionReachDenyMatcher); n != 1 {
				t.Errorf("re-apply must leave exactly one deny entry, got %d", n)
			}
		})
	}
}

// TestApplyReviewSettings_AddsSessionReachDenyToPreFeatureSettings re-applies to a
// settings file in the shape written before the deny hook existed, as a resumed
// review staged before the upgrade would have, and expects the hook to be added.
func TestApplyReviewSettings_AddsSessionReachDenyToPreFeatureSettings(t *testing.T) {
	for _, mode := range reviewModes {
		t.Run(mode.name, func(t *testing.T) {
			inst := t.TempDir()
			claudeDir := filepath.Join(inst, ".claude")
			if err := os.MkdirAll(claudeDir, 0o755); err != nil {
				t.Fatal(err)
			}
			pre := reviewDocFor(mode.sandbox, mode.ask)
			if hasPreToolUseMatcher(pre, sessionReachDenyMatcher) {
				t.Fatal("fixture must be in the pre-feature shape")
			}
			if err := VerifyReviewSettings(pre, mode.sandbox, mode.ask); err == nil {
				t.Fatal("a pre-feature document must no longer verify")
			}
			writeJSON(t, filepath.Join(claudeDir, "settings.json"), pre)
			if err := ApplyReviewSettings(inst, mode.sandbox, mode.ask); err != nil {
				t.Fatalf("ApplyReviewSettings on pre-feature settings: %v", err)
			}
			got := readSettings(t, inst)
			if n := countPreToolUseMatcher(t, got, sessionReachDenyMatcher); n != 1 {
				t.Errorf("re-apply must add the deny hook exactly once, got %d", n)
			}
			for _, m := range []string{postGuardMatcher, egressDenyMatcher, fsGuardMatcher, autoAllowMatcher} {
				want := 0
				if hasPreToolUseMatcher(pre, m) {
					want = 1
				}
				if n := countPreToolUseMatcher(t, got, m); n != want {
					t.Errorf("matcher %q: got %d entries, want %d", m, n, want)
				}
			}
		})
	}
}

// TestNoEgressSandboxStanza_NetworkAllowsNoUnixSockets pins the stanza's network map
// to an empty domain list and nothing else, so a unix-socket allowance added later
// (which would let sandboxed Bash reach the local cross-session inbox) fails here.
func TestNoEgressSandboxStanza_NetworkAllowsNoUnixSockets(t *testing.T) {
	network, ok := noEgressSandboxStanza()["network"].(map[string]any)
	if !ok {
		t.Fatal("stanza must carry a network map")
	}
	if len(network) != 1 {
		t.Fatalf("network map must hold exactly one key (allowedDomains), got %v", network)
	}
	domains, ok := network["allowedDomains"].([]any)
	if !ok {
		t.Fatalf("allowedDomains must be a list, got %T", network["allowedDomains"])
	}
	if len(domains) != 0 {
		t.Errorf("allowedDomains must be empty, got %v", domains)
	}
}

// TestExistingReviewMatchersUnchanged pins the matchers of the hooks that predate the
// session-reach deny hook.
func TestExistingReviewMatchersUnchanged(t *testing.T) {
	for got, want := range map[string]string{
		egressDenyMatcher: "WebFetch|WebSearch|mcp__",
		fsGuardMatcher:    "Write|Edit|MultiEdit|NotebookEdit",
		postGuardMatcher:  "Bash",
		autoAllowMatcher:  "Bash|Read|Glob|Grep",
	} {
		if got != want {
			t.Errorf("matcher = %q, want %q", got, want)
		}
	}
}
