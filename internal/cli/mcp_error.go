package cli

import (
	"encoding/json"
	"fmt"
	"strings"
)

// renderMCPError parses the structured error code prefix from an MCP
// errResult content block and returns a human-readable error message that
// includes a per-code recovery hint when available. Used by session
// create/destroy and the niwa task redelegate handlers.
//
// Two MCP error shapes are recognized:
//   - Legacy two-line: "error_code: <CODE>\ndetail: <message>"
//   - Structured JSON: {"error_code":"<CODE>", "detail":"...", ...}
//
// The hint table covers the codes the design names plus the codes the CLI
// already commonly surfaces. Unknown codes pass through unchanged so future
// codes don't break the renderer.
func renderMCPError(text string) error {
	code, detail, body := parseMCPErrorPayload(text)
	if code == "" {
		// Not a structured MCP error — pass through verbatim.
		return fmt.Errorf("%s", strings.TrimSpace(text))
	}
	hint := mcpErrorHint(code, body)
	if detail == "" {
		detail = text
	}
	if hint == "" {
		return fmt.Errorf("%s: %s", code, detail)
	}
	return fmt.Errorf("%s: %s\n  hint: %s", code, detail, hint)
}

// parseMCPErrorPayload extracts (code, detail, body) from an MCP error
// content text. body is non-nil only for structured-JSON shapes; the hint
// table can use it to compose code-specific recovery actions (e.g.
// MISSING_SKILLS lists the missing entries).
func parseMCPErrorPayload(text string) (code string, detail string, body map[string]any) {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]any
		if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
			if c, ok := obj["error_code"].(string); ok && c != "" {
				code = c
				if d, ok := obj["detail"].(string); ok {
					detail = d
				}
				body = obj
				return
			}
		}
	}
	const prefix = "error_code: "
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return "", "", nil
	}
	rest := text[idx+len(prefix):]
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		code = rest[:nl]
		rest = rest[nl+1:]
	} else {
		code = rest
		return code, "", nil
	}
	const detailPrefix = "detail: "
	if dIdx := strings.Index(rest, detailPrefix); dIdx >= 0 {
		detail = strings.TrimSpace(rest[dIdx+len(detailPrefix):])
	}
	return code, detail, nil
}

// mcpErrorHint returns a per-code recovery hint or "" when no hint is
// known for the code. Body, when non-nil, may be inspected to compose
// code-specific hints (e.g. MISSING_SKILLS lists the missing entries).
func mcpErrorHint(code string, body map[string]any) string {
	switch code {
	case "DAEMON_SPAWN_TIMEOUT":
		return "the per-worktree daemon did not start within 500ms; check <worktree>/.niwa/daemon.log for the spawn trace. The session was rolled back."
	case "MISSING_SKILLS":
		missingList := ""
		if body != nil {
			if missing, ok := body["missing"].([]any); ok && len(missing) > 0 {
				ms := make([]string, 0, len(missing))
				for _, m := range missing {
					if s, ok := m.(string); ok {
						ms = append(ms, s)
					}
				}
				missingList = strings.Join(ms, ", ")
			}
		}
		if missingList != "" {
			return fmt.Sprintf("the target session is missing required skills: %s. Install them, pick a different --session-id, or remove them from the body. Run `niwa session list` to find candidates.", missingList)
		}
		return "the target session is missing one or more required skills declared in body.required_skills."
	case "SOURCE_BODY_LOST":
		return "the source task's envelope.json is gone. Re-supply the body via --body-overrides @body.json on niwa task redelegate."
	case "UNKNOWN_ROLE":
		return "the target role is not registered. Run `niwa apply` to register roles, or check `<workspace>/.niwa/roles/`."
	case "SESSION_NOT_FOUND":
		return "the session ID does not match any active session. Run `niwa session list` to see active sessions."
	case "SESSION_REQUIRED":
		return "this delegation needs a session_id. Provision one with `niwa session create <repo> <purpose>`, or set read_only:true for non-mutating tasks."
	case "TASK_ALREADY_TERMINAL":
		return "the task is already in a terminal state. Use `niwa task show <id>` to see the final state, or `niwa task redelegate <id>` to re-fire."
	case "NOT_TASK_OWNER":
		return "only the task's original delegator may modify or redelegate it."
	case "NOT_TASK_PARTY":
		return "the task is not visible to the calling role. Verify the task ID and your session role."
	}
	return ""
}
