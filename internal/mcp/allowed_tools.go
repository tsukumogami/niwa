// allowed_tools.go publishes the flag-formatted niwa MCP tool names that
// callers pass to Claude Code's `--allowed-tools` to suppress per-tool
// approval prompts. Two callers consume this list — the daemon's worker
// spawn (internal/cli/mesh_watch.go::spawnWorker) and the functional-test
// coordinator launcher (test/functional/mesh_steps_test.go) — and any
// drift between them is silently fatal: a tool present in one but missing
// from the other is treated as un-approved by Claude, which then blocks
// on the first call (workers stall, headless coordinator tests time out
// after 15 minutes). One copy here is the only safe shape.
//
// Must stay in sync with the tools/list response in server.go.
package mcp

// ClaudeAllowedTools is the canonical `--allowed-tools` value, in the
// `mcp__<server>__<tool>` form Claude Code expects (server id "niwa"
// matches .mcp.json). Callers join with commas.
var ClaudeAllowedTools = []string{
	"mcp__niwa__niwa_delegate",
	"mcp__niwa__niwa_query_task",
	"mcp__niwa__niwa_await_task",
	"mcp__niwa__niwa_report_progress",
	"mcp__niwa__niwa_finish_task",
	"mcp__niwa__niwa_list_outbound_tasks",
	"mcp__niwa__niwa_update_task",
	"mcp__niwa__niwa_cancel_task",
	"mcp__niwa__niwa_ask",
	"mcp__niwa__niwa_send_message",
	"mcp__niwa__niwa_check_messages",
}

// WorkerFallbackBashTools are appended to ClaudeAllowedTools when spawning
// workers that are not in bypassPermissions mode. These patterns grant
// access to common dev tools without requiring full bypass. They are
// usability defaults for headless workers, not security boundaries — the
// patterns are broad (e.g. Bash(gh *) allows any gh subcommand). Users
// who need tighter control should configure permissions = "bypass" explicitly.
var WorkerFallbackBashTools = []string{
	"Bash(gh *)",
	"Bash(git *)",
	"Bash(go test *)",
	"Bash(go build *)",
	"Bash(make *)",
}
