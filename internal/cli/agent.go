package cli

import (
	"os"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

// launchAgentFlagName is the flag that selects WHICH coding agent a command
// launches. It is deliberately not spelled --agent: on niwa dispatch that name
// already means the subagent type forwarded INTO the launched agent (a role
// within it), and the two are different enough that sharing a name would be a
// trap rather than a convenience. "launch" is the word the rest of this code
// already uses for the distinction -- agentplan.DispatchLaunch, the launch
// spec, and the refusal that says an agent cannot be launched.
const launchAgentFlagName = "launch-agent"

// launchAgentFlagUsage builds the flag's help line. The accepted values are
// read from the closed set rather than typed out here, so adding a third agent
// updates the help instead of leaving it quietly wrong.
//
// The line carries the whole precedence ladder on purpose. Every rung has to be
// reachable from the command a developer is already running, not just from a
// guide they would have to know to go looking for.
func launchAgentFlagUsage() string {
	names := make([]string, 0, len(agent.All()))
	for _, a := range agent.All() {
		names = append(names, string(a))
	}
	// The fallback is not spelled out here. It is whatever resolving with no
	// source set produces, so the help says what the resolution does rather
	// than repeating a name that would have to be kept in step with it.
	fallback, _ := agent.ResolveAgent("", "", "", "")
	return "which coding agent to launch (" + strings.Join(names, ", ") + "). " +
		"Full precedence: this flag, then NIWA_AGENT, then the workspace's " +
		"[workspace].default_agent, then your own [global].default_agent " +
		"(niwa config set default-agent), then " + string(fallback) + ". " +
		"This is not --agent, which forwards a subagent type into whichever agent is launched"
}

// resolveSessionAgent resolves the session-global coding agent once from its
// four sources, in precedence order flag > NIWA_AGENT env > workspace
// default_agent > host default_agent > claude.
//
// flagValue is the agent-selection flag's value ("" when the entry point does
// not expose the flag, e.g. init/reset/from-hook/worktree -- those still honor
// the NIWA_AGENT env override and both config defaults).
//
// cfg is the workspace config; gc is the developer's personal niwa config
// (~/.config/niwa/config.toml), the broadest rung. Either may be nil, which
// reads as "that source is unset" -- a config niwa could not load resolves
// against the sources it does have rather than failing the command here.
//
// An unknown value from any source returns an error naming the accepted set.
func resolveSessionAgent(flagValue string, cfg *config.WorkspaceConfig, gc *config.GlobalConfig) (agent.Agent, error) {
	def := ""
	if cfg != nil {
		def = cfg.Workspace.DefaultAgent
	}
	return agent.ResolveAgent(flagValue, os.Getenv("NIWA_AGENT"), def, gc.DefaultAgent())
}
