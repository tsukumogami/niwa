package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/watch"
)

// liveMCPStubDirEnv names the marker directory of the stub MCP server. When it
// is set, TestMain runs the test binary as that server instead of the tests.
const liveMCPStubDirEnv = "NIWA_WATCH_LIVE_MCP_STUB_DIR"

// The stub server's name, its one tool, the tool's name as Claude Code exposes
// it, and the markers it writes: one when it starts, one when the tool is called.
const (
	liveMCPStubServer     = "niwa-live-stub"
	liveMCPStubTool       = "record_call"
	liveMCPStubToolName   = "mcp__" + liveMCPStubServer + "__" + liveMCPStubTool
	liveMCPStubStarted    = "started"
	liveMCPStubCallMarker = "called"
)

// runLiveMCPStub serves the stub MCP server over stdio: newline-delimited
// JSON-RPC 2.0 with initialize, tools/list (one tool, record_call), tools/call
// and ping. It writes the started marker in dir before reading anything and the
// call marker on every tools/call, so the test can tell a server that was loaded
// from a call that reached it. It returns 0 when stdin closes.
func runLiveMCPStub(dir string, in io.Reader, out io.Writer) int {
	if err := os.WriteFile(filepath.Join(dir, liveMCPStubStarted), []byte("started\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "stub MCP server: writing the started marker: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(out)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue // a notification, or not JSON-RPC: nothing to answer
		}
		var result any
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2024-11-05"
			}
			result = map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": liveMCPStubServer, "version": "0.0.0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{
				"name":        liveMCPStubTool,
				"description": "Records that it was called.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"note": map[string]any{"type": "string"}},
				},
			}}}
		case "tools/call":
			f, err := os.OpenFile(filepath.Join(dir, liveMCPStubCallMarker), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
			if err == nil {
				fmt.Fprintf(f, "%s\n", req.Params)
				f.Close()
			}
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "recorded"}}}
		case "ping":
			result = map[string]any{}
		default:
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID,
				"error": map[string]any{"code": -32601, "message": "method not found"}})
			continue
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	return 0
}

// TestLiveMCPStub drives the stub server through the calls the live test relies
// on, so a broken stub shows here rather than as an inconclusive live run.
func TestLiveMCPStub(t *testing.T) {
	dir := t.TempDir()
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"record_call","arguments":{"note":"x"}}}`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if code := runLiveMCPStub(dir, strings.NewReader(in), &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 responses (the notification gets none), got %d:\n%s", len(lines), out.String())
	}
	for i, want := range []string{`"protocolVersion":"2025-06-18"`, `"name":"record_call"`, `"text":"recorded"`} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("response %d = %s, want it to contain %s", i+1, lines[i], want)
		}
	}
	for _, m := range []string{liveMCPStubStarted, liveMCPStubCallMarker} {
		if _, err := os.Stat(filepath.Join(dir, m)); err != nil {
			t.Errorf("marker %s: %v", m, err)
		}
	}
}

// TestReviewSessionRefusesMCPCall_OnHarness is the live check for the MCP deny. It
// stages an unsandboxed review session (the mode with no --strict-mcp-config and no
// egress-deny hook, so the MCP deny is the only thing in the way) in an instance
// whose .mcp.json declares a stub stdio MCP server, approved in the instance's
// settings, and asks the session to call the stub's tool. It passes when the
// server was started (so the tool was reachable), the call never reached it, and
// the session's transcript holds both the attempted call and the hook's refusal.
//
// It is gated like TestResumedSessionDeniesEgress_OnHarness: skipped in CI and
// without NIWA_WATCH_LIVE_TEST=1 and NIWA_WATCH_LIVE_TEST_DISPOSABLE_HOST=1, and
// once opted in every unmet precondition fails it. Run it locally with:
//
//	NIWA_WATCH_LIVE_TEST=1 NIWA_WATCH_LIVE_TEST_DISPOSABLE_HOST=1 \
//	  go test ./internal/cli/ -count=1 -v -run '^TestReviewSessionRefusesMCPCall_OnHarness$' -timeout 20m
func TestReviewSessionRefusesMCPCall_OnHarness(t *testing.T) {
	requireLiveGateHost(t)
	h := newLiveGateHarness(t)
	appendLiveGateReport(t, "claude_version: "+liveGateClaudeVersion(t))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary for the stub MCP server: %v", err)
	}
	inst := t.TempDir()
	if err := writeMinimalInstanceState(inst); err != nil {
		t.Fatalf("writing the instance state: %v", err)
	}
	// Claude Code won't start a session in an untrusted directory. The
	// sandboxed posture check is what seeds trust for a review instance; the
	// review itself runs unsandboxed.
	var postureOut bytes.Buffer
	resolveAskPosture(&postureOut, inst, true)
	t.Logf("live MCP deny: trust seed for %s: %s", filepath.Base(inst), strings.TrimSpace(postureOut.String()))

	// The markers live outside the instance: an MCP server runs outside any
	// sandbox and could write anywhere.
	markers := t.TempDir()
	mcp := map[string]any{"mcpServers": map[string]any{liveMCPStubServer: map[string]any{
		"type":    "stdio",
		"command": exe,
		"args":    []string{},
		"env":     map[string]string{liveMCPStubDirEnv: markers},
	}}}
	settings := map[string]any{"enabledMcpjsonServers": []string{liveMCPStubServer}}
	if err := os.MkdirAll(filepath.Join(inst, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, doc := range map[string]any{
		filepath.Join(inst, ".mcp.json"):                mcp,
		filepath.Join(inst, ".claude", "settings.json"): settings,
	} {
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("encoding %s: %v", path, err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
	if err := watch.ApplyReviewSettings(inst, false, false); err != nil {
		t.Fatalf("applying the review settings: %v", err)
	}
	doc := readReviewSettingsDoc(t, inst)
	if approved, _ := doc["enabledMcpjsonServers"].([]any); len(approved) != 1 || approved[0] != liveMCPStubServer {
		t.Fatalf("the review settings lost the stub server's approval: %v", doc["enabledMcpjsonServers"])
	}
	assertMCPDeny(t, doc)

	reapKey, err := newDispatchID(dispatchRandReader)
	if err != nil {
		t.Fatalf("minting a reap key: %v", err)
	}
	slug := sanitizeInstanceSlug("watch-live-mcp-" + filepath.Base(inst))
	if len(slug) > 40 {
		slug = slug[:40]
	}
	prompt := "Call the MCP tool " + liveMCPStubToolName + ` with note "live". If it is not loaded yet, find it with ToolSearch first. Make the call exactly once, whatever happens, then STOP.`
	if err := dispatchLaunch(ctx, watchReviewLaunch(inst, slug, prompt, false, watchLaunchEnv(h.root, reapKey))); err != nil {
		t.Fatalf("launching the review session: %v", err)
	}
	sid, short := "", ""
	for i := 0; i < 6 && sid == "" && ctx.Err() == nil; i++ {
		sid, short = captureReviewSession(inst)
	}
	if sid == "" {
		t.Fatalf("the review session's ids weren't captured from the jobs directory (instance holds: %s)", liveGateInstFiles(inst))
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = stopSessionFunc(c, short)
		_, _ = removeClaudeSessionFunc(c, short)
	})

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolving the session HOME: %v", err)
	}
	transcript := sessionattach.TranscriptPath(home, inst, sid)
	attempted, refused := false, false
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) && ctx.Err() == nil && !(attempted && refused) {
		if b, err := os.ReadFile(transcript); err == nil {
			attempted, refused = liveMCPTranscriptVerdict(b)
		}
		if !(attempted && refused) {
			time.Sleep(3 * time.Second)
		}
	}

	if _, err := os.Stat(filepath.Join(markers, liveMCPStubStarted)); err != nil {
		t.Fatalf("inconclusive: the stub MCP server was never started (%v), so the session had no MCP tool to call", err)
	}
	if b, err := os.ReadFile(filepath.Join(markers, liveMCPStubCallMarker)); err == nil {
		t.Fatalf("MCP CALL NOT REFUSED: the review session's call reached the stub server (%s)", strings.TrimSpace(string(b)))
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checking the call marker: %v", err)
	}
	if !attempted {
		t.Fatalf("inconclusive: the transcript %s shows no call to %s", transcript, liveMCPStubToolName)
	}
	if !refused {
		t.Fatalf("the transcript %s shows the call to %s but not the refusal %q", transcript, liveMCPStubToolName, watchMCPDenyMessage)
	}
	appendLiveGateReport(t, "mcp deny: call to "+liveMCPStubToolName+" refused")
}

// liveMCPTranscriptVerdict reads a session transcript and reports whether it
// holds a tool_use of the stub's tool and the MCP deny's refusal line.
func liveMCPTranscriptVerdict(transcript []byte) (attempted, refused bool) {
	for _, line := range bytes.Split(transcript, []byte("\n")) {
		var entry struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		var blocks []struct {
			Type    string          `json:"type"`
			Name    string          `json:"name"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(entry.Message.Content, &blocks) != nil {
			continue
		}
		for _, b := range blocks {
			switch b.Type {
			case "tool_use":
				attempted = attempted || b.Name == liveMCPStubToolName
			case "tool_result":
				var text string
				if json.Unmarshal(b.Content, &text) != nil {
					text = string(b.Content)
				}
				refused = refused || strings.Contains(text, watchMCPDenyMessage)
			}
		}
	}
	return attempted, refused
}

// TestLiveMCPTranscriptVerdict pins the transcript reading the live test's verdict
// rests on: a tool_use of the stub's tool, and the refusal in a tool_result whose
// content is a string or a list of text blocks. The refusal anywhere else (the
// prompt, the agent's own prose) doesn't count.
func TestLiveMCPTranscriptVerdict(t *testing.T) {
	use := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"` + liveMCPStubToolName + `","input":{}}]}}`
	strResult := `{"type":"user","message":{"content":[{"type":"tool_result","content":"PreToolUse:` + liveMCPStubToolName + ` hook error: [cmd]: niwa watch: review sessions don't call MCP tools\n","is_error":true}]}}`
	listResult := `{"type":"user","message":{"content":[{"type":"tool_result","content":[{"type":"text","text":"niwa watch: review sessions don't call MCP tools"}],"is_error":true}]}}`
	prose := `{"type":"assistant","message":{"content":[{"type":"text","text":"niwa watch: review sessions don't call MCP tools"}]}}`
	other := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{}}]}}`
	for _, tc := range []struct {
		name                string
		lines               []string
		attempted, refusedW bool
	}{
		{"string result", []string{use, strResult}, true, true},
		{"block result", []string{use, listResult}, true, true},
		{"prose only", []string{use, prose}, true, false},
		{"other tool", []string{other, strResult}, false, true},
		{"empty", nil, false, false},
	} {
		a, r := liveMCPTranscriptVerdict([]byte(strings.Join(tc.lines, "\n")))
		if a != tc.attempted || r != tc.refusedW {
			t.Errorf("%s: attempted, refused = %v, %v; want %v, %v", tc.name, a, r, tc.attempted, tc.refusedW)
		}
	}
}
