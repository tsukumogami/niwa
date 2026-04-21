package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/mcp"
)

var sessionRegisterRepo string
var sessionRegisterCheckOnly bool

func init() {
	sessionCmd.AddCommand(sessionRegisterCmd)
	sessionRegisterCmd.Flags().StringVar(&sessionRegisterRepo, "repo", "", "repo name (defaults to cwd-inferred repo)")
	sessionRegisterCmd.Flags().BoolVar(&sessionRegisterCheckOnly, "check-only", false, "check if already registered and exit 0 silently if so; register normally if not (full behavior implemented in Issue 3)")
}

var sessionRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register this session with the workspace mesh",
	RunE:  runSessionRegister,
}

func runSessionRegister(cmd *cobra.Command, args []string) error {
	// --check-only: no-op placeholder until Issue 3 implements the full
	// already-registered detection. The flag is accepted so the hook script
	// written by InstallChannelInfrastructure does not fail at the CLI level.
	if sessionRegisterCheckOnly {
		return nil
	}

	instanceRoot := os.Getenv("NIWA_INSTANCE_ROOT")
	if instanceRoot == "" {
		return fmt.Errorf("NIWA_INSTANCE_ROOT is not set")
	}

	role := deriveRole(sessionRegisterRepo)
	sessionID := mcp.NewSessionID()
	pid := os.Getpid()

	startTime, _ := mcp.PIDStartTime(pid)

	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	inboxDir := filepath.Join(sessionsDir, sessionID, "inbox")

	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		return fmt.Errorf("cannot create inbox: %w", err)
	}

	entry := mcp.SessionEntry{
		ID:           sessionID,
		Role:         role,
		Repo:         sessionRegisterRepo,
		PID:          pid,
		StartTime:    startTime,
		InboxDir:     inboxDir,
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := writeSessionEntry(sessionsDir, entry); err != nil {
		return fmt.Errorf("cannot write session entry: %w", err)
	}

	// Count pending messages across all peer sessions in the instance.
	pending := countPending(sessionsDir, sessionID)

	fmt.Printf("session_id=%s role=%s\n", sessionID, role)
	if pending > 0 {
		fmt.Printf("pending=%d\n", pending)
	}

	return nil
}

func deriveRole(repo string) string {
	if role := os.Getenv("NIWA_SESSION_ROLE"); role != "" {
		return role
	}
	if repo != "" {
		parts := strings.Split(strings.TrimRight(repo, "/"), "/")
		return parts[len(parts)-1]
	}
	return "coordinator"
}

func writeSessionEntry(sessionsDir string, entry mcp.SessionEntry) error {
	registryPath := filepath.Join(sessionsDir, "sessions.json")

	// Read existing registry or start fresh.
	var registry mcp.SessionRegistry
	if data, err := os.ReadFile(registryPath); err == nil {
		_ = json.Unmarshal(data, &registry)
	}

	// Remove stale entry for same role (dead PID or PID mismatch).
	var kept []mcp.SessionEntry
	for _, s := range registry.Sessions {
		if s.Role == entry.Role {
			if mcp.IsPIDAlive(s.PID, s.StartTime) {
				return fmt.Errorf("role %q already registered by live session PID %d (registered %s); use NIWA_SESSION_ROLE to override or run: niwa session unregister %s",
					entry.Role, s.PID, s.RegisteredAt, entry.Role)
			}
			continue // prune stale entry
		}
		kept = append(kept, s)
	}
	registry.Sessions = append(kept, entry)

	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	tmp := registryPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, registryPath)
}

// countPending counts pending (.json) messages across all peer sessions in the
// instance (every session directory other than currentSessionID). It provides
// no per-role filtering because callers use it only to surface a "you have
// messages waiting" hint at registration time.
func countPending(sessionsDir, currentSessionID string) int {
	// Look for any inbox dir belonging to a peer session.
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() || e.Name() == currentSessionID {
			continue
		}
		inboxDir := filepath.Join(sessionsDir, e.Name(), "inbox")
		files, err := os.ReadDir(inboxDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".json") {
				count++
			}
		}
	}
	return count
}
