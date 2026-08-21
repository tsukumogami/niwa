package cli

import (
	"fmt"
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

// launchAgentFlagUsage builds the flag's help line. The accepted values are
// read from the closed set rather than typed out here, so adding a third agent
// updates the help instead of leaving it quietly wrong. The fallback comes from
// builtinDefaultAgentName for the same reason.
//
// The line carries the whole precedence ladder on purpose. Every rung has to be
// reachable from the command a developer is already running, not just from a
// guide they would have to know to go looking for.
func launchAgentFlagUsage() string {
	names := make([]string, 0, len(agent.All()))
	for _, a := range agent.All() {
		names = append(names, string(a))
	}
	return "which coding agent to launch (" + strings.Join(names, ", ") + "). " +
		"Full precedence: this flag, then NIWA_AGENT, then the workspace's " +
		"[workspace].default_agent, then your own [global].default_agent " +
		"(niwa config set default-agent), then " + builtinDefaultAgentName() + ". " +
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
// cfg is the workspace config and cfgPath the file it was read from; gc is the
// developer's personal niwa config (~/.config/niwa/config.toml), the broadest
// rung. Either config may be nil, which reads as "that source is unset" -- a
// config niwa could not load resolves against the sources it does have rather
// than failing the command here.
//
// An unknown value from any source returns an error naming the accepted set AND
// the rung the value came from. The labeling happens here rather than in
// agent.ParseAgent because this is the only place that knows which string came
// from where, and it is the whole difference between a developer editing the
// right file and hunting for one.
func resolveSessionAgent(flagValue string, cfg *config.WorkspaceConfig, cfgPath string, gc *config.GlobalConfig) (agent.Agent, error) {
	def := ""
	if cfg != nil {
		def = cfg.Workspace.DefaultAgent
	}
	env := os.Getenv("NIWA_AGENT")
	hostDef := gc.DefaultAgent()
	chosen, err := agent.ResolveAgent(flagValue, env, def, hostDef)
	if err != nil {
		return "", fmt.Errorf("%s: %w", agentSourceLabel(flagValue, env, def, cfgPath), err)
	}
	return chosen, nil
}

// agentSourceLabel names the rung whose value agent.ResolveAgent rejected, in
// the form a developer would go and edit: the flag as they would type it, the
// variable by name, and each config table with the file that holds it.
//
// The ordering mirrors ResolveAgent's precedence, because the rung it consulted
// is the first one that was set. Resolution errors only when some rung holds a
// value, so with the first three empty the host default is the one that failed
// and needs no test of its own here.
func agentSourceLabel(flagValue, env, workspaceDefault, cfgPath string) string {
	switch {
	case flagValue != "":
		return "--" + launchAgentFlagName
	case env != "":
		return "NIWA_AGENT"
	case workspaceDefault != "":
		return "[workspace].default_agent in " + workspaceConfigDisplayPath(cfgPath)
	default:
		return "[global].default_agent in " + globalConfigDisplayPath()
	}
}

// workspaceConfigDisplayPath is the workspace file to name in a resolution
// error. A caller that has no path -- every test that resolves against structs
// rather than files -- still gets a label that says which file to look for.
func workspaceConfigDisplayPath(cfgPath string) string {
	if cfgPath == "" {
		return "the workspace config"
	}
	return cfgPath
}

// globalConfigDisplayPath is the personal config file to name in a resolution
// error. It asks config for the real path, honoring XDG_CONFIG_HOME, so the
// error names the file that actually holds the value rather than the one it
// usually would. Without a resolvable home there is no path to print, and the
// description is still enough to find it.
func globalConfigDisplayPath() string {
	path, err := config.GlobalConfigPath()
	if err != nil {
		return "your personal niwa config"
	}
	return path
}
