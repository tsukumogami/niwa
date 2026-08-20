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
// acceptedAgentNames renders the accepted set for help text, quoted and
// comma-joined. It derives from agent.All() for the same reason
// launchAgentFlagUsage does: a third agent should update the help rather than
// leave a hardcoded list quietly wrong. The dispatch-path scan cannot catch a
// stale one, because the names would sit inside a long literal rather than
// being a literal the scan recognizes.
func acceptedAgentNames() string {
	quoted := make([]string, 0, len(agent.All()))
	for _, a := range agent.All() {
		quoted = append(quoted, `"`+string(a)+`"`)
	}
	if len(quoted) < 2 {
		return strings.Join(quoted, "")
	}
	return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
}

// builtinDefaultAgentName is what resolving with no source set produces, so
// help text says what the resolution does rather than repeating a name that
// would have to be kept in step with it.
func builtinDefaultAgentName() string {
	fallback, _ := agent.ResolveAgent("", "", "", "")
	return string(fallback)
}

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

// launchAgentMismatchWarning returns the line to print when --agent's value
// reads like a request to launch a different agent, or "" when there is
// nothing to say.
//
// It fires on exactly one shape: the value parses as an agent niwa knows, and
// that agent is not the one this dispatch resolved to launch. Anything else is
// silence. A value that is not an agent name is an ordinary subagent type and
// carries no confusion; a value that agrees with the launched agent is
// consistent whatever the developer meant by it.
//
// The line names three things because a developer who typed the wrong flag
// needs all three: what --agent actually does, which agent is being launched
// instead, and the flag that would have selected the one they named.
func launchAgentMismatchWarning(agentFlagValue string, launched agent.Agent) string {
	if agentFlagValue == "" {
		return ""
	}
	named, err := agent.ParseAgent(agentFlagValue)
	if err != nil || named == launched {
		return ""
	}
	return "--agent " + agentFlagValue + " names a subagent type to forward into the launched agent, not which agent to launch. This dispatch launches " +
		string(launched) + ". Use --" + launchAgentFlagName + " " + string(named) + " to launch " + string(named) + " instead"
}

// resolveSessionAgent resolves the session-global coding agent once from its
// four sources, in precedence order flag > NIWA_AGENT env > workspace
// default_agent > host default_agent > claude.
//
// flagValue is the agent-selection flag's value: `--launch-agent` on `niwa
// dispatch`, which is the only command that selects a launch target and the
// only caller of this function. "" means the flag was not given, and the
// remaining rungs decide.
//
// An earlier version of this comment said the empty case also covered entry
// points that expose no such flag, naming init, reset, from-hook and worktree.
// Those commands do not consult agent selection at all -- none of them launches
// anything, and every apply prepares the tree for every supported agent
// regardless. The sentence was arguably true when dispatch passed "" as well,
// and adding the flag made it false.
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
