package mcp

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// TestServer_Initialize_ToolsList verifies the MCP server responds to
// initialize and tools/list with all four expected tool names.
func TestServer_Initialize_ToolsList(t *testing.T) {
	pr, pw := io.Pipe()
	var outBuf strings.Builder

	srv := New("coordinator", "")

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

	wantTools := []string{
		"niwa_check_messages",
		"niwa_send_message",
		"niwa_ask",
		"niwa_delegate",
		"niwa_query_task",
		"niwa_await_task",
		"niwa_report_progress",
		"niwa_finish_task",
		"niwa_list_outbound_tasks",
		"niwa_update_task",
		"niwa_cancel_task",
	}
	for _, want := range wantTools {
		if !names[want] {
			t.Errorf("tool %q not in tools/list response; got names: %v", want, names)
		}
	}
}

// TestServer_Initialize_AdvertisesToolsCapability guards a regression where
// the initialize response declared an empty tools capability that
// encoding/json's omitempty stripped on the wire. Claude Code reads a
// missing "tools" field as hasTools=false and never calls tools/list, so
// the whole MCP surface becomes invisible. The response MUST contain a
// non-empty "tools" object.
func TestServer_Initialize_AdvertisesToolsCapability(t *testing.T) {
	pr, pw := io.Pipe()
	var outBuf strings.Builder
	srv := New("coordinator", "")
	done := make(chan error, 1)
	go func() { done <- srv.Run(pr, &outBuf) }()

	if err := json.NewEncoder(pw).Encode(request{JSONRPC: "2.0", ID: 1, Method: "initialize"}); err != nil {
		t.Fatalf("encode initialize: %v", err)
	}
	pw.Close()
	if err := <-done; err != nil {
		t.Fatalf("server run: %v", err)
	}

	var resp map[string]any
	sc := bufio.NewScanner(strings.NewReader(outBuf.String()))
	if !sc.Scan() {
		t.Fatalf("no initialize response; output: %q", outBuf.String())
	}
	if err := json.Unmarshal(sc.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal initialize: %v", err)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %v", resp["result"])
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities is not an object: %v", result["capabilities"])
	}
	tools, ok := caps["tools"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities.tools is missing or not an object; got %v (full capabilities: %v)", caps["tools"], caps)
	}
	if len(tools) == 0 {
		t.Fatalf("capabilities.tools must be a non-empty object so json.omitempty cannot strip it; got %v", tools)
	}
}
