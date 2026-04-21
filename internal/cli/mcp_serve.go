package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/mcp"
)

func init() {
	rootCmd.AddCommand(mcpServeCmd)
}

var mcpServeCmd = &cobra.Command{
	Use:    "mcp-serve",
	Short:  "Start the niwa stdio MCP server for session mesh communication",
	Hidden: true, // invoked by Claude Code via .mcp.json, not by users directly
	RunE:   runMCPServe,
}

func runMCPServe(cmd *cobra.Command, args []string) error {
	instanceRoot := os.Getenv("NIWA_INSTANCE_ROOT")
	sessionID := os.Getenv("NIWA_SESSION_ID")
	sessionRole := os.Getenv("NIWA_SESSION_ROLE")

	if instanceRoot == "" {
		return fmt.Errorf("NIWA_INSTANCE_ROOT is not set; is this session in a niwa workspace?")
	}

	sessionsDir := instanceRoot + "/.niwa/sessions"
	inboxDir := ""
	if sessionID != "" {
		inboxDir = sessionsDir + "/" + sessionID + "/inbox"
	}

	srv := mcp.New(inboxDir, sessionsDir, sessionRole, sessionID, instanceRoot)
	return srv.Run(os.Stdin, os.Stdout)
}
