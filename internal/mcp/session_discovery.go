package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// sessionIDRegex validates Claude session IDs before they are stored or used.
// Values that don't match are rejected so invalid input can never reach
// sessions.json or exec.Command.
var sessionIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,128}$`)

// DiscoverClaudeSessionID tries three tiers to find the Claude session ID:
//
//  1. CLAUDE_SESSION_ID env var (validated, returned immediately if valid;
//     warns to stderr when set but invalid)
//  2. ~/.claude/sessions/<ppid>.json PPID walk (two levels, cwd cross-check)
//  3. ~/.claude/projects/<base64url-cwd>/*.jsonl mtime scan
//
// homeDir and cwd are passed as parameters so unit tests can inject fake roots.
// Returns "" if all tiers fail; the caller should warn when that happens.
func DiscoverClaudeSessionID(homeDir, cwd string) string {
	// Tier 1: env var.
	if id := os.Getenv("CLAUDE_SESSION_ID"); id != "" {
		if sessionIDRegex.MatchString(id) {
			return id
		}
		// Set but invalid — warn here so the user knows their value was rejected.
		fmt.Fprintln(os.Stderr, "warning: CLAUDE_SESSION_ID has invalid format; ignoring")
	}

	// Tier 2: PPID walk.
	if id := discoverViaPPIDWalk(homeDir, cwd); id != "" {
		return id
	}

	// Tier 3: project dir scan.
	if id := discoverViaProjectScan(homeDir, cwd); id != "" {
		return id
	}

	return ""
}

// claudeSessionFile is the on-disk format of ~/.claude/sessions/<pid>.json.
type claudeSessionFile struct {
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
}

// discoverViaPPIDWalk tries two levels of the process tree:
//
//	Level 1 (direct parent):  os.Getppid() — cross-platform.
//	Level 2 (grandparent):    readPPID(ppid1) — Linux only, 0 elsewhere.
//
// In production the chain is: Claude Code → hook script → niwa.
// Level 1 is the hook script; level 2 is the Claude Code process whose
// session file lives at ~/.claude/sessions/<pid>.json.
func discoverViaPPIDWalk(homeDir, cwd string) string {
	sessionsDir := filepath.Join(homeDir, ".claude", "sessions")

	ppid1 := os.Getppid()
	for _, ppid := range []int{ppid1, readPPID(ppid1)} {
		if ppid <= 1 {
			continue
		}
		path := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", ppid))
		if id := readClaudeSessionFile(path, cwd); id != "" {
			return id
		}
	}
	return ""
}

// readClaudeSessionFile parses the session file at path and returns the
// session ID if the file is valid and its cwd matches the expected cwd.
func readClaudeSessionFile(path, cwd string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var sf claudeSessionFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return ""
	}
	if sf.CWD != cwd {
		return ""
	}
	if !sessionIDRegex.MatchString(sf.SessionID) {
		return ""
	}
	return sf.SessionID
}

// discoverViaProjectScan lists *.jsonl files in ~/.claude/projects/<encoded-cwd>/
// sorted by mtime descending and returns the session ID from the most recent
// filename whose basename (minus .jsonl) passes the regex.
//
// The base64url encoding (no padding) matches Claude Code's project directory
// naming convention as of Claude Code CLI v1.x.
func discoverViaProjectScan(homeDir, cwd string) string {
	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString([]byte(cwd))
	projectDir := filepath.Join(homeDir, ".claude", "projects", encoded)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		return ""
	}

	type entry struct {
		name  string
		mtime int64
	}
	var files []entry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, entry{name: e.Name(), mtime: info.ModTime().UnixNano()})
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime > files[j].mtime
	})

	for _, f := range files {
		name := strings.TrimSuffix(f.name, ".jsonl")
		if sessionIDRegex.MatchString(name) {
			return name
		}
	}
	return ""
}
