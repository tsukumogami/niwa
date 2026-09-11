package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// readSingleMappingJSON decodes the single session mapping a dispatch wrote as a
// plain JSON object, so a test can tell an absent key from a false one. It
// finds the file by listing the store rather than by session id, because the
// id a dispatch records depends on the agent.
func readSingleMappingJSON(t *testing.T, root string) map[string]any {
	t.Helper()
	files := sessionMappingFiles(t, root)
	if len(files) != 1 {
		t.Fatalf("want exactly one session mapping, got %v", files)
	}
	data, err := os.ReadFile(filepath.Join(root, workspace.StateDir, "sessions", files[0]))
	if err != nil {
		t.Fatalf("reading mapping: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decoding mapping: %v\n%s", err, data)
	}
	return raw
}

// TestDispatch_Inbound_RecordedOnMapping: a dispatch where the behavior takes
// effect records accepts_session_messages: true on its mapping, and one where
// it does not writes no key at all. The record follows the same boolean as the
// audit line, so the two always agree.
func TestDispatch_Inbound_RecordedOnMapping(t *testing.T) {
	modes := []inboundMode{
		{"machine setting on", hostInboundOn, nil, true},
		{"flag on", "", inboundFlag(true), true},
		{"flag over machine setting off", hostInboundOff, inboundFlag(true), true},
		{"nothing set", "", nil, false},
		{"machine setting off", hostInboundOff, nil, false},
		{"=false over machine setting", hostInboundOn, inboundFlag(false), false},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			root, _, pass := setupInboundMode(t, mode)

			_, stderr, err := runDispatchCmd(t, "do a thing")
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			checkLaunchedKey(t, mode, *pass)

			v, ok := readSingleMappingJSON(t, root)["accepts_session_messages"]
			if mode.wantKey && (!ok || v != true) {
				t.Errorf("mapping accepts_session_messages = %v (present %v), want true", v, ok)
			}
			if !mode.wantKey && ok {
				t.Errorf("mapping carries accepts_session_messages = %v; a dispatch without the behavior writes no key", v)
			}
			if printed := strings.Contains(stderr, auditMarker); printed != mode.wantKey {
				t.Errorf("audit line printed = %v but recorded = %v; they must agree. stderr:\n%s", printed, mode.wantKey, stderr)
			}
		})
	}
}

// TestDispatch_Inbound_CodexNotRecordedOnMapping: a Codex dispatch cannot
// receive the behavior, so even with the flag its mapping writes no key.
func TestDispatch_Inbound_CodexNotRecordedOnMapping(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostInboundOn)
	f := installDispatchFakes(t, root)
	var pass []string
	captureLaunchPassthrough(f, &pass)
	dispatchDetach = true
	t.Setenv("NIWA_DISPATCH_HARNESS", string(agent.AgentCodex))
	dispatchAcceptSessionMessages = inboundFlag(true)

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if strings.Contains(stderr, auditMarker) {
		t.Errorf("a Codex dispatch printed the audit line; stderr:\n%s", stderr)
	}
	raw := readSingleMappingJSON(t, root)
	if raw["agent"] != string(agent.AgentCodex) {
		t.Fatalf("mapping agent = %v, want %q; the test would not be exercising Codex", raw["agent"], agent.AgentCodex)
	}
	if v, ok := raw["accepts_session_messages"]; ok {
		t.Errorf("a Codex mapping carries accepts_session_messages = %v; the behavior never took effect", v)
	}
}
