// Package main implements the scripted worker fake used by the functional
// test harness (Issue #10). The binary is invoked by the niwa daemon in
// place of the real `claude -p` worker via the NIWA_WORKER_SPAWN_COMMAND
// override. It starts `niwa mcp-serve` as a stdio subprocess so the real
// MCP tool surface is exercised (not bypassed) and then runs a scripted
// scenario selected by NIWA_FAKE_SCENARIO.
//
// The PPID chain from the MCP server is:
//
//	daemon -> worker-fake -> niwa mcp-serve
//
// so PPID(mcp-serve) == PID(worker-fake), which the daemon records as
// state.json.worker.pid at spawn time. The executor-kind authorization
// check therefore succeeds without any special-casing.
//
// Supported scenarios (selected via NIWA_FAKE_SCENARIO):
//
//   - finish-completed         — check_messages, report_progress, finish(completed)
//   - finish-abandoned         — check_messages, finish(abandoned)
//   - progress-then-exit-zero  — check_messages, report_progress, exit(0)
//   - progress-then-crash      — check_messages, report_progress, exit(1)
//   - stall-forever            — check_messages, sleep forever
//   - ignore-sigterm           — check_messages, block SIGTERM, sleep forever
//   - reply-to-ask             — check_messages, send_message reply
//   - dump-args                — write os.Args to .niwa/.test/worker_spawn_args.txt, then finish(completed)
//   - ask-and-finish           — check_messages, niwa_ask(coordinator), finish(completed) with answer
//   - ask-roundtrip            — branches on role: worker calls niwa_ask; coordinator answers via finish_task
//
// Authorization window: workers may call tools before the daemon backfills
// state.json.worker.{pid, start_time} (the "worker.pid==0 backfill
// window"). The fake retries the first tool call on NOT_TASK_PARTY for up
// to 2 s with 50 ms linear backoff.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// authRetryDeadline is the maximum time the fake keeps retrying an
// authorization-path tool call (one that failed with NOT_TASK_PARTY).
// 2 s is well above the daemon's backfill latency (single flock write).
const authRetryDeadline = 2 * time.Second

// authRetryInterval is the sleep between authorization retries.
const authRetryInterval = 50 * time.Millisecond

// mainErr is the actual logic; main just maps errors → exit codes.
func main() {
	os.Exit(run())
}

// run executes the scripted scenario. Returns an exit code — 0 on success,
// 1 on scenario-selected crash, 2 on unexpected setup errors.
func run() int {
	scenario := os.Getenv("NIWA_FAKE_SCENARIO")
	if scenario == "" {
		fmt.Fprintln(os.Stderr, "worker-fake: NIWA_FAKE_SCENARIO not set")
		return 2
	}
	instanceRoot := os.Getenv("NIWA_INSTANCE_ROOT")
	if instanceRoot == "" {
		fmt.Fprintln(os.Stderr, "worker-fake: NIWA_INSTANCE_ROOT not set")
		return 2
	}
	role := os.Getenv("NIWA_SESSION_ROLE")
	if role == "" {
		fmt.Fprintln(os.Stderr, "worker-fake: NIWA_SESSION_ROLE not set")
		return 2
	}
	taskID := os.Getenv("NIWA_TASK_ID")
	if taskID == "" && scenario != "reply-to-ask" {
		fmt.Fprintln(os.Stderr, "worker-fake: NIWA_TASK_ID not set")
		return 2
	}

	// NIWA_FAKE_TEST_BINARY points at the niwa test binary that should be
	// used to start mcp-serve. The daemon only passes NIWA_WORKER_SPAWN_COMMAND
	// (the fake's own path); we receive the niwa binary path through a
	// separate env var so the fake does not have to rely on PATH lookup.
	niwaBin := os.Getenv("NIWA_FAKE_TEST_BINARY")
	if niwaBin == "" {
		fmt.Fprintln(os.Stderr, "worker-fake: NIWA_FAKE_TEST_BINARY not set")
		return 2
	}

	// ignore-sigterm scenario installs the signal handler BEFORE starting
	// the MCP server so a quick SIGTERM still loses the race to ignore.
	if scenario == "ignore-sigterm" {
		signal.Ignore(syscall.SIGTERM)
	}

	client, err := startMCPClient(niwaBin, instanceRoot, role, taskID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: starting mcp-serve: %v\n", err)
		return 2
	}
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := client.Initialize(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: mcp initialize failed: %v\n", err)
		return 2
	}

	switch scenario {
	case "finish-completed":
		return runFinishCompleted(ctx, client, taskID)
	case "finish-abandoned":
		return runFinishAbandoned(ctx, client, taskID)
	case "progress-then-exit-zero":
		return runProgressThenExit(ctx, client, taskID, 0)
	case "progress-then-crash":
		return runProgressThenExit(ctx, client, taskID, 1)
	case "stall-forever":
		return runStallForever(ctx, client, taskID)
	case "ignore-sigterm":
		return runIgnoreSIGTERM(ctx, client, taskID)
	case "reply-to-ask":
		return runReplyToAsk(ctx, client, role)
	case "dump-args":
		return runDumpArgs(ctx, client, taskID, instanceRoot)
	case "ask-and-finish":
		return runAskAndFinish(ctx, client, taskID)
	case "ask-roundtrip":
		return runAskRoundtrip(ctx, client, taskID, role)
	default:
		fmt.Fprintf(os.Stderr, "worker-fake: unknown scenario %q\n", scenario)
		return 2
	}
}

// ---------------------------------------------------------------------
// Scenario implementations
// ---------------------------------------------------------------------

// runFinishCompleted: check_messages -> report_progress -> finish(completed).
// The result payload echoes the envelope body back so the test harness can
// assert round-trip integrity.
func runFinishCompleted(ctx context.Context, c *mcpClient, taskID string) int {
	body, err := extractTaskBody(callCheckMessages(ctx, c))
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: check_messages: %v\n", err)
		return 2
	}

	if _, err := callToolWithAuthRetry(ctx, c, "niwa_report_progress",
		map[string]any{"task_id": taskID, "summary": "working"}); err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: report_progress: %v\n", err)
		return 2
	}

	result := body
	if len(result) == 0 {
		result = json.RawMessage(`{"ok":true}`)
	}
	if _, err := callToolWithAuthRetry(ctx, c, "niwa_finish_task", map[string]any{
		"task_id": taskID,
		"outcome": "completed",
		"result":  result,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: finish_task: %v\n", err)
		return 2
	}
	return 0
}

// runFinishAbandoned: check_messages -> finish(abandoned, reason="scripted-abandon").
func runFinishAbandoned(ctx context.Context, c *mcpClient, taskID string) int {
	if _, err := callCheckMessagesTR(ctx, c); err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: check_messages: %v\n", err)
		return 2
	}
	if _, err := callToolWithAuthRetry(ctx, c, "niwa_finish_task", map[string]any{
		"task_id": taskID,
		"outcome": "abandoned",
		"reason":  map[string]any{"error": "scripted-abandon"},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: finish_task: %v\n", err)
		return 2
	}
	return 0
}

// runProgressThenExit: check_messages, report_progress, then exit with the
// given code WITHOUT calling finish_task — the daemon should classify this
// as an unexpected exit.
func runProgressThenExit(ctx context.Context, c *mcpClient, taskID string, code int) int {
	if _, err := callCheckMessagesTR(ctx, c); err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: check_messages: %v\n", err)
		return 2
	}
	if _, err := callToolWithAuthRetry(ctx, c, "niwa_report_progress",
		map[string]any{"task_id": taskID, "summary": "attempt"}); err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: report_progress: %v\n", err)
		return 2
	}
	return code
}

// runStallForever: check_messages, then sleep indefinitely. The daemon's
// stall watchdog is expected to SIGTERM/SIGKILL this process.
//
// Use a polling sleep loop so Go's runtime does not mark the main goroutine
// as deadlocked. SIGTERM/SIGKILL from the daemon still terminates the
// process — no Go-level cooperation needed.
func runStallForever(ctx context.Context, c *mcpClient, taskID string) int {
	_, _ = callCheckMessagesTR(ctx, c)
	_ = taskID
	for {
		time.Sleep(time.Second)
	}
}

// runIgnoreSIGTERM: install SIG_IGN for SIGTERM (done in main before MCP
// startup), then sleep forever. The daemon's SIGTERM-to-SIGKILL escalation
// is expected to kill this process.
//
// Sleep in a bounded loop rather than blocking on a channel — Go's runtime
// detects unrecoverable blocking on unbuffered channels as a deadlock and
// panics, which would exit with code 2 before the daemon's SIGKILL could
// fire.
func runIgnoreSIGTERM(ctx context.Context, c *mcpClient, taskID string) int {
	_, _ = callCheckMessagesTR(ctx, c)
	_ = taskID
	for {
		time.Sleep(time.Second)
	}
}

// runReplyToAsk: check_messages to read the question, parse {"kind":"ask",
// "body":{...}}, send a question.answer back to the delegator.
//
// The role this fake runs as is the *target* of niwa_ask. niwa_ask creates
// a first-class task where the body is {"kind":"ask","body":<q>} so the
// fake must retrieve the task body, extract the original question body,
// then reply via niwa_send_message + finish the task (so the ask task can
// transition to completed and the caller unblocks).
func runReplyToAsk(ctx context.Context, c *mcpClient, role string) int {
	body, err := extractTaskBody(callCheckMessages(ctx, c))
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: check_messages: %v\n", err)
		return 2
	}
	_ = role

	// The task body for an ask task is {"kind":"ask","body":<original>}.
	var askWrap struct {
		Kind string          `json:"kind"`
		Body json.RawMessage `json:"body"`
	}
	_ = json.Unmarshal(body, &askWrap)

	taskID := os.Getenv("NIWA_TASK_ID")

	// Finish the ask task with outcome=completed and the scripted answer.
	answer := map[string]any{"answer": "scripted"}
	if _, err := callToolWithAuthRetry(ctx, c, "niwa_finish_task", map[string]any{
		"task_id": taskID,
		"outcome": "completed",
		"result":  answer,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: finish_task: %v\n", err)
		return 2
	}
	return 0
}

// runAskAndFinish: check_messages → niwa_ask(to="coordinator") → finish(completed)
// with the answer from the ask result embedded in the original task result.
// Supports both the live-coordinator path (task.ask routed to coordinator inbox)
// and the fallback spawn path (task.delegate creates an ephemeral coordinator worker).
// Timeout is 30 s — generous enough for the test harness to answer before expiry.
func runAskAndFinish(ctx context.Context, c *mcpClient, taskID string) int {
	if _, err := callCheckMessagesTR(ctx, c); err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake(ask-and-finish): check_messages: %v\n", err)
		return 2
	}

	// Ask the coordinator a test question.
	askResult, err := callToolWithAuthRetry(ctx, c, "niwa_ask", map[string]any{
		"to":              "coordinator",
		"body":            map[string]any{"question": "test question from worker"},
		"timeout_seconds": 30,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake(ask-and-finish): niwa_ask: %v\n", err)
		return 2
	}

	// Extract the answer from the ask result (body.result if present, else raw).
	var askPayload struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	_ = json.Unmarshal(extractFirstContentText(askResult), &askPayload)
	answer := askPayload.Result
	if len(answer) == 0 {
		answer = json.RawMessage(`{"answer":"received"}`)
	}

	if _, err := callToolWithAuthRetry(ctx, c, "niwa_finish_task", map[string]any{
		"task_id": taskID,
		"outcome": "completed",
		"result":  answer,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake(ask-and-finish): finish_task: %v\n", err)
		return 2
	}
	return 0
}

// runAskRoundtrip dispatches on NIWA_SESSION_ROLE:
//   - "worker":      reads task body, calls niwa_ask to coordinator, finishes with answer.
//   - "coordinator": reads the ask task body, answers via niwa_finish_task.
//
// This supports Scenario 3 (fallback to spawn): the daemon spawns the same
// binary for both the worker and the ephemeral coordinator worker, and the
// NIWA_SESSION_ROLE env distinguishes which side each fake is playing.
func runAskRoundtrip(ctx context.Context, c *mcpClient, taskID, role string) int {
	switch role {
	case "coordinator":
		// Coordinator side: read the ask task body and answer it.
		return runReplyToAsk(ctx, c, role)
	default:
		// Worker side: ask the coordinator and finish with the answer.
		return runAskAndFinish(ctx, c, taskID)
	}
}

// extractFirstContentText unwraps the first content block's text from a raw
// MCP toolResult, returning nil if missing.
func extractFirstContentText(raw json.RawMessage) json.RawMessage {
	var tr struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil || len(tr.Content) == 0 {
		return nil
	}
	return json.RawMessage(tr.Content[0].Text)
}

// runDumpArgs writes os.Args to .niwa/.test/worker_spawn_args.txt in the
// instance root so the test harness can assert on --permission-mode and
// --allowed-tools without running a real claude session. Written before
// any MCP calls so the file is present even if the MCP flow errors. After
// writing it drives the standard finish-completed flow.
func runDumpArgs(ctx context.Context, c *mcpClient, taskID, instanceRoot string) int {
	testDir := filepath.Join(instanceRoot, ".niwa", ".test")
	if err := os.MkdirAll(testDir, 0o700); err == nil {
		argsFile := filepath.Join(testDir, "worker_spawn_args.txt")
		_ = os.WriteFile(argsFile, []byte(strings.Join(os.Args, "\n")), 0o600)
	}
	return runFinishCompleted(ctx, c, taskID)
}

// ---------------------------------------------------------------------
// MCP client (stdio JSON-RPC) over a niwa mcp-serve subprocess.
// ---------------------------------------------------------------------

// mcpClient is a minimal JSON-RPC 2.0 client wrapping a niwa mcp-serve
// subprocess. It serializes calls (one in-flight at a time) so the line-
// delimited protocol remains unambiguous.
type mcpClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader

	mu sync.Mutex
	id int
}

// startMCPClient launches niwaBin mcp-serve with the given env. The child
// inherits NIWA_* env vars from the process environment (copied into
// cmd.Env) so authorizeTaskCall can read them.
func startMCPClient(niwaBin, instanceRoot, role, taskID string) (*mcpClient, error) {
	cmd := exec.Command(niwaBin, "mcp-serve")
	// Pass through the full environment but ensure NIWA_INSTANCE_ROOT,
	// NIWA_SESSION_ROLE, NIWA_TASK_ID are set as the daemon specified them.
	env := os.Environ()
	env = appendOrReplace(env, "NIWA_INSTANCE_ROOT", instanceRoot)
	env = appendOrReplace(env, "NIWA_SESSION_ROLE", role)
	if taskID != "" {
		env = appendOrReplace(env, "NIWA_TASK_ID", taskID)
	}
	cmd.Env = env

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// stderr can go to our stderr so failures surface in the daemon task log.
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}
	return &mcpClient{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReaderSize(stdout, 1<<20),
		id:     0,
	}, nil
}

// Close signals the MCP server to exit (by closing stdin) and waits briefly
// for the child to terminate.
func (c *mcpClient) Close() {
	_ = c.stdin.Close()
	done := make(chan struct{})
	go func() {
		_ = c.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		<-done
	}
}

// Initialize drives the JSON-RPC initialize + notifications/initialized
// handshake.
func (c *mcpClient) Initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "worker-fake",
			"version": "0.1.0",
		},
	})
	if err != nil {
		return err
	}
	// notifications/initialized is a notification — no ID, no response.
	return c.notify("notifications/initialized", nil)
}

// CallTool issues a tools/call request for the given tool and returns the
// raw toolResult JSON (the `result` field of the JSON-RPC response).
func (c *mcpClient) CallTool(ctx context.Context, name string, args any) (json.RawMessage, error) {
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal args: %w", err)
	}
	return c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": json.RawMessage(argsJSON),
	})
}

func (c *mcpClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.id++
	id := c.id
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	// Read response.
	for {
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("read: %w", err)
		}
		if len(line) == 0 {
			continue
		}
		var resp struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      any             `json:"id"`
			Result  json.RawMessage `json:"result"`
			Error   *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(line, &resp); err != nil {
			// Malformed or non-JSON frame (should not happen) — keep reading.
			continue
		}
		// Skip server-initiated notifications (no ID) until we get our reply.
		if resp.ID == nil {
			continue
		}
		// Match our ID (JSON numbers decode as float64).
		if idFloat, ok := resp.ID.(float64); !ok || int(idFloat) != id {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

func (c *mcpClient) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

// ---------------------------------------------------------------------
// Tool helpers
// ---------------------------------------------------------------------

// callCheckMessages invokes niwa_check_messages and returns the raw toolResult
// JSON. Authorization is not required for this tool, but we still retry on
// NOT_TASK_PARTY for symmetry with the other wrappers.
func callCheckMessages(ctx context.Context, c *mcpClient) json.RawMessage {
	result, err := callToolWithAuthRetry(ctx, c, "niwa_check_messages", map[string]any{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "worker-fake: check_messages err=%v\n", err)
	}
	return result
}

// callCheckMessagesTR returns the raw toolResult JSON without extraction.
func callCheckMessagesTR(ctx context.Context, c *mcpClient) (json.RawMessage, error) {
	return callToolWithAuthRetry(ctx, c, "niwa_check_messages", map[string]any{})
}

// callToolWithAuthRetry calls a tool, retrying on NOT_TASK_PARTY errors for
// up to authRetryDeadline. This covers the daemon-backfill window where
// state.json.worker.{pid, start_time} is still zero because cmd.Start's
// backfill has not landed yet.
func callToolWithAuthRetry(ctx context.Context, c *mcpClient, name string, args any) (json.RawMessage, error) {
	deadline := time.Now().Add(authRetryDeadline)
	var lastErr error
	for {
		result, err := c.CallTool(ctx, name, args)
		if err != nil {
			return nil, err
		}
		if code := extractErrorCode(result); code == "NOT_TASK_PARTY" {
			lastErr = fmt.Errorf("NOT_TASK_PARTY")
			if time.Now().After(deadline) {
				return result, nil // give up, return the error result verbatim
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(authRetryInterval):
			}
			continue
		}
		_ = lastErr
		return result, nil
	}
}

// extractErrorCode parses the first content-block text for an "error_code:"
// prefix. Returns "" when the toolResult is not an error response.
func extractErrorCode(raw json.RawMessage) string {
	var tr struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return ""
	}
	if !tr.IsError || len(tr.Content) == 0 {
		return ""
	}
	text := tr.Content[0].Text
	const prefix = "error_code: "
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return ""
	}
	rest := text[idx+len(prefix):]
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		rest = rest[:nl]
	}
	return rest
}

// extractTaskBody parses a niwa_check_messages toolResult and extracts the
// body of the task.delegate message wrapped in _niwa_task_body. Returns
// empty RawMessage if no task.delegate body is found.
func extractTaskBody(raw json.RawMessage) (json.RawMessage, error) {
	var tr struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, err
	}
	if len(tr.Content) == 0 {
		return nil, nil
	}
	text := tr.Content[0].Text
	// The body we care about is inside a ```json ... ``` fence. Each message
	// has such a fence; we take the first one (there should be exactly one
	// task.delegate per task). The content is a _niwa_task_body wrapper.
	start := strings.Index(text, "```json")
	if start < 0 {
		return nil, nil
	}
	start += len("```json\n")
	end := strings.Index(text[start:], "```")
	if end < 0 {
		return nil, nil
	}
	raw = json.RawMessage(strings.TrimSpace(text[start : start+end]))
	// Unwrap _niwa_task_body.
	var wrap struct {
		NiwaTaskBody json.RawMessage `json:"_niwa_task_body"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return raw, nil
	}
	if len(wrap.NiwaTaskBody) > 0 {
		return wrap.NiwaTaskBody, nil
	}
	return raw, nil
}

// ---------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------

// appendOrReplace sets key=value in env, replacing any existing assignment
// for the same key.
func appendOrReplace(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// filepath join helper used by some tests; imported to keep lint clean even
// when the imports are pruned.
var _ = filepath.Join
