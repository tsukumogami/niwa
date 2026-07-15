package cli

import (
	"os"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

// resolveSessionAgent resolves the session-global coding agent once from its
// three sources, in precedence order flag > NIWA_AGENT env > workspace default
// > claude. flagValue is the --agent flag value ("" when the entry point does
// not expose the flag, e.g. init/reset/from-hook/worktree — those still honor
// the NIWA_AGENT env override and the workspace default). An unknown value from
// any source returns an error naming the accepted set.
func resolveSessionAgent(flagValue string, cfg *config.WorkspaceConfig) (agent.Agent, error) {
	def := ""
	if cfg != nil {
		def = cfg.Workspace.DefaultAgent
	}
	return agent.ResolveAgent(flagValue, os.Getenv("NIWA_AGENT"), def)
}
