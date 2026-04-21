package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// TestServer_Initialize_ToolsList verifies the MCP server responds to
// initialize and tools/list with the expected tool names. This is a partial
// validation — niwa_ask and niwa_wait are added in Issue 5.
func TestServer_Initialize_ToolsList(t *testing.T) {
	pr, pw := io.Pipe()
	var outBuf strings.Builder

	srv := New("", "", "coordinator", "test-session-id")

	done := make(chan error, 1)
	go func() {
		done <- srv.Run(pr, &outBuf)
	}()

	enc := json.NewEncoder(pw)

	// Send initialize.
	if err := enc.Encode(request{JSONRPC: "2.0", ID: 1, Method: "initialize"}); err != nil {
		t.Fatalf("encode initialize: %v", err)
	}
	// Send notifications/initialized (no response expected).
	if err := enc.Encode(request{JSONRPC: "2.0", Method: "notifications/initialized"}); err != nil {
		t.Fatalf("encode notifications/initialized: %v", err)
	}
	// Send tools/list.
	if err := enc.Encode(request{JSONRPC: "2.0", ID: 2, Method: "tools/list"}); err != nil {
		t.Fatalf("encode tools/list: %v", err)
	}

	pw.Close()
	if err := <-done; err != nil {
		t.Fatalf("server run: %v", err)
	}

	// Parse all response lines.
	var responses []map[string]any
	sc := bufio.NewScanner(strings.NewReader(outBuf.String()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("unmarshal response line: %v\nline: %s", err, sc.Text())
		}
		responses = append(responses, m)
	}

	if len(responses) < 2 {
		t.Fatalf("expected at least 2 responses (initialize + tools/list), got %d\noutput:\n%s", len(responses), outBuf.String())
	}

	// Find the tools/list response (id == 2).
	var toolsResp map[string]any
	for _, r := range responses {
		if id, ok := r["id"].(float64); ok && id == 2 {
			toolsResp = r
		}
	}
	if toolsResp == nil {
		t.Fatalf("tools/list response not found\nresponses: %v", responses)
	}

	result, ok := toolsResp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result is not an object: %v", toolsResp["result"])
	}
	toolsRaw, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools field is not an array: %v", result["tools"])
	}

	names := make(map[string]bool)
	for _, toolRaw := range toolsRaw {
		tool, ok := toolRaw.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := tool["name"].(string); ok {
			names[name] = true
		}
	}

	for _, want := range []string{"niwa_check_messages", "niwa_send_message"} {
		if !names[want] {
			t.Errorf("tool %q not in tools/list response; got names: %v", want, names)
		}
	}
}
