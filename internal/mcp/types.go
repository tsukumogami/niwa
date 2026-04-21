package mcp

import "encoding/json"

// JSON-RPC wire types

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP protocol types

type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
}

type capabilities struct {
	Experimental map[string]any `json:"experimental,omitempty"`
	Tools        map[string]any `json:"tools,omitempty"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

type toolDef struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema inputSchema `json:"inputSchema"`
}

type inputSchema struct {
	Type       string                `json:"type"`
	Properties map[string]schemaProp `json:"properties,omitempty"`
	Required   []string              `json:"required,omitempty"`
}

type schemaProp struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type toolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type channelNotificationParams struct {
	Source  string `json:"source"`
	Content string `json:"content"`
}

// Domain types

// Sessions directory layout under <instance-root>/.niwa/sessions/:
//
//	sessions.json              — SessionRegistry: all active sessions
//	<session-uuid>/
//	  inbox/                   — incoming message files (<msg-uuid>.json)
//	    read/                  — consumed messages (moved here by notifyNewFile for reply-awaited messages, TTL sweep for others)
//	    expired/               — expired messages (moved here on check)
type SessionEntry struct {
	ID              string `json:"id"`
	Role            string `json:"role"`
	Repo            string `json:"repo,omitempty"`
	PID             int    `json:"pid"`
	StartTime       int64  `json:"start_time"`
	InboxDir        string `json:"inbox_dir"`
	RegisteredAt    string `json:"registered_at"`
	ClaudeSessionID string `json:"claude_session_id,omitempty"`
}

type SessionRegistry struct {
	Sessions []SessionEntry `json:"sessions"`
}

type MessageFrom struct {
	Instance string `json:"instance,omitempty"`
	Role     string `json:"role"`
	Repo     string `json:"repo,omitempty"`
	PID      int    `json:"pid,omitempty"`
}

type MessageTo struct {
	Instance string `json:"instance,omitempty"`
	Role     string `json:"role"`
}

type Message struct {
	V         int             `json:"v"`
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	From      MessageFrom     `json:"from"`
	To        MessageTo       `json:"to"`
	ReplyTo   string          `json:"reply_to,omitempty"`
	TaskID    string          `json:"task_id,omitempty"`
	SentAt    string          `json:"sent_at"`
	ExpiresAt string          `json:"expires_at,omitempty"`
	Body      json.RawMessage `json:"body"`
}

type sendMessageArgs struct {
	To        string          `json:"to"`
	Type      string          `json:"type"`
	Body      json.RawMessage `json:"body"`
	ReplyTo   string          `json:"reply_to,omitempty"`
	TaskID    string          `json:"task_id,omitempty"`
	ExpiresAt string          `json:"expires_at,omitempty"`
}
