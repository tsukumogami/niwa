package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/mcp"
)

var sessionRegisterRepo string
var sessionRegisterRole string
var sessionRegisterCheckOnly bool

func init() {
	sessionCmd.AddCommand(sessionRegisterCmd)
	sessionRegisterCmd.Flags().StringVar(&sessionRegisterRepo, "repo", "", "repo name (defaults to cwd-inferred repo)")
	sessionRegisterCmd.Flags().StringVar(&sessionRegisterRole, "role", "", "role override (highest priority; overrides NIWA_SESSION_ROLE, --repo, and pwd fallback)")
	sessionRegisterCmd.Flags().BoolVar(&sessionRegisterCheckOnly, "check-only", false, "skip registration silently when this role is already registered with an active session; register normally otherwise")
}

var sessionRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register this session with the workspace mesh",
	RunE:  runSessionRegister,
}

func runSessionRegister(cmd *cobra.Command, args []string) error {
	instanceRoot := os.Getenv("NIWA_INSTANCE_ROOT")
	if instanceRoot == "" {
		return fmt.Errorf("NIWA_INSTANCE_ROOT is not set")
	}

	role := deriveRole(sessionRegisterRole, sessionRegisterRepo, instanceRoot)

	if sessionRegisterCheckOnly && isAlreadyRegistered(instanceRoot, role) {
		return nil
	}

	sessionID := mcp.NewSessionID()
	pid := os.Getpid()

	startTime, _ := mcp.PIDStartTime(pid)

	homeDir, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()

	claudeSessionID := mcp.DiscoverClaudeSessionID(homeDir, cwd)
	if claudeSessionID == "" {
		fmt.Fprintln(os.Stderr, "warning: could not discover Claude session ID; claude_session_id will be omitted")
	}

	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	inboxDir := filepath.Join(sessionsDir, sessionID, "inbox")

	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		return fmt.Errorf("cannot create inbox: %w", err)
	}

	entry := mcp.SessionEntry{
		ID:              sessionID,
		Role:            role,
		Repo:            sessionRegisterRepo,
		PID:             pid,
		StartTime:       startTime,
		InboxDir:        inboxDir,
		RegisteredAt:    time.Now().UTC().Format(time.RFC3339),
		ClaudeSessionID: claudeSessionID,
	}

	if err := mcp.WriteSessionEntry(sessionsDir, entry); err != nil {
		// In --check-only mode a concurrent registration between our liveness
		// check and the write means the goal is achieved; return success.
		if sessionRegisterCheckOnly && errors.Is(err, mcp.ErrAlreadyRegistered) {
			return nil
		}
		if errors.Is(err, mcp.ErrAlreadyRegistered) {
			return fmt.Errorf("%w\n\nUse NIWA_SESSION_ROLE to override or run: niwa session unregister %s",
				err, role)
		}
		return fmt.Errorf("cannot write session entry: %w", err)
	}

	fmt.Printf("session_id=%s role=%s\n", sessionID, role)
	return nil
}

// isAlreadyRegistered returns true when sessions.json has a live entry for role.
func isAlreadyRegistered(instanceRoot, role string) bool {
	jsonPath := filepath.Join(instanceRoot, ".niwa", "sessions", "sessions.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return false
	}
	var registry mcp.SessionRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return false
	}
	for _, s := range registry.Sessions {
		if s.Role == role && mcp.IsPIDAlive(s.PID, s.StartTime) {
			return true
		}
	}
	return false
}

// deriveRole determines the session role using a four-tier priority:
//
//  1. --role flag (explicit, highest priority)
//  2. NIWA_SESSION_ROLE environment variable
//  3. --repo flag (basename of the repo path)
//  4. pwd relative to instanceRoot (filepath.Base of the relative path;
//     "coordinator" when cwd == instanceRoot). For a shallow path like
//     <root>/myrepo this returns "myrepo". For a deeper path like
//     <root>/myrepo/src/pkg it returns "pkg" (the immediate directory
//     name), not "myrepo". Register from the repo root to get the expected
//     repo name.
func deriveRole(flagRole, repo, instanceRoot string) string {
	if flagRole != "" {
		return flagRole
	}
	if role := os.Getenv("NIWA_SESSION_ROLE"); role != "" {
		return role
	}
	if repo != "" {
		parts := strings.Split(strings.TrimRight(repo, "/"), "/")
		return parts[len(parts)-1]
	}
	// Tier 4: derive from pwd relative to instanceRoot.
	if instanceRoot != "" {
		cwd, err := os.Getwd()
		if err == nil {
			// Resolve symlinks so comparison works on platforms where /tmp is a symlink.
			resolvedCwd, err1 := filepath.EvalSymlinks(cwd)
			resolvedRoot, err2 := filepath.EvalSymlinks(instanceRoot)
			if err1 == nil && err2 == nil {
				cwd = resolvedCwd
				instanceRoot = resolvedRoot
			}
			if cwd == instanceRoot {
				return "coordinator"
			}
			rel, err := filepath.Rel(instanceRoot, cwd)
			if err == nil && rel != "" && rel != "." && !strings.HasPrefix(rel, "..") {
				return filepath.Base(rel)
			}
		}
	}
	return "coordinator"
}

