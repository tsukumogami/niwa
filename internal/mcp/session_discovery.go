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
// Values that don't match are silently discarded so invalid input can never
// reach sessions.json or exec.Command.
var sessionIDRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,128}$`)

// DiscoverClaudeSessionID tries three tiers to find the Claude session ID:
//
//  1. CLAUDE_SESSION_ID env var (validated, returned immediately if valid)
//  2. ~/.claude/sessions/<ppid>.json PPID walk (up to depth 2, cwd cross-check)
//  3. ~/.claude/projects/<base64url-cwd>/*.jsonl mtime scan
//
// homeDir and cwd are passed as parameters so unit tests can inject fake roots.
// Returns "" if all tiers fail; the caller writes SessionEntry without the field.
func DiscoverClaudeSessionID(homeDir, cwd string) string {
	// Tier 1: env var.
	if id := os.Getenv("CLAUDE_SESSION_ID"); id != "" {
		if sessionIDRegex.MatchString(id) {
			return id
		}
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

// discoverViaPPIDWalk walks up to 2 levels of PPID, looking for a
// ~/.claude/sessions/<pid>.json whose cwd matches.
func discoverViaPPIDWalk(homeDir, cwd string) string {
	sessionsDir := filepath.Join(homeDir, ".claude", "sessions")

	pid := os.Getpid()
	for range 2 {
		ppid := readPPID(pid)
		if ppid <= 1 {
			break
		}
		path := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", ppid))
		if id := readClaudeSessionFile(path, cwd); id != "" {
			return id
		}
		pid = ppid
	}
	return ""
}

// readPPID reads the PPID of a process from /proc/<pid>/stat.
// Returns 0 on any error or on non-Linux platforms.
func readPPID(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(data)
	// /proc/<pid>/stat: "pid (comm) state ppid ..."
	// Find the closing ')' of the comm field first because it may contain spaces.
	idx := strings.LastIndex(s, ")")
	if idx < 0 {
		return 0
	}
	fields := strings.Fields(s[idx+1:])
	// fields[0] = state, fields[1] = ppid (field 4 in the /proc spec, 1-indexed)
	if len(fields) < 2 {
		return 0
	}
	var ppid int
	if _, err := fmt.Sscanf(fields[1], "%d", &ppid); err != nil {
		return 0
	}
	return ppid
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
