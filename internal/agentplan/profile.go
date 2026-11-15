package agentplan

import (
	"fmt"
	"slices"

	"github.com/tsukumogami/niwa/internal/agent"
)

// This file is the agent side of a dispatch profile: how one agent spells the
// three things a profile can drop, which tools a profile may never drop on that
// agent, and which environment names any profile can set. The profile itself
// is declared in the workspace config; the caller hands its drops over as a
// plain ProfileDrops value so this package keeps internal/agent as its one
// dependency and never imports internal/config.

// ProfileDrops is what one dispatch profile removes from a launched session.
// It mirrors the three fields of a declared profile.
type ProfileDrops struct {
	// Tools are whole tool names to deny, in the order the profile declares
	// them.
	Tools []string
	// BundledSkills turns off the agent's bundled skills.
	BundledSkills bool
	// AccountConnectors leaves the account's connectors unloaded.
	AccountConnectors bool
}

// profileSpelling is one agent's vocabulary for a profile. Only an agent whose
// DispatchProfile row is implemented has one.
type profileSpelling struct {
	// connectorsEnv is the environment name that turns the account's
	// connectors off, set to connectorsOff.
	connectorsEnv string
	connectorsOff string
	// requiredTools are tools the agent's dispatch path depends on, so a
	// profile may not drop them.
	requiredTools []string
}

// profileSpellings holds the spelling for each agent that has one. Claude
// Code drops a tool through a permissions deny rule, drops its bundled skills
// with disableBundledSkills, and leaves the account's connectors unloaded when
// ENABLE_CLAUDEAI_MCP_SERVERS is false. Its dispatch path needs ToolSearch,
// since no deferred tool can load without it, and CronCreate, since niwa's
// keep-alive is a recurring session cron.
var profileSpellings = map[agent.Agent]profileSpelling{
	agent.AgentClaude: {
		connectorsEnv: "ENABLE_CLAUDEAI_MCP_SERVERS",
		connectorsOff: "false",
		requiredTools: []string{"ToolSearch", "CronCreate"},
	},
}

// ProfileSettings returns how ag spells a profile with the given drops: the
// settings keys for the launch's settings document and the environment values
// for its env block. A drop the profile doesn't make adds nothing, so a profile
// with only tools to drop returns no env entry.
//
// For Claude the keys are permissions (a map holding deny, the tool names as a
// plain list in declared order) and disableBundledSkills, and the env value is
// ENABLE_CLAUDEAI_MCP_SERVERS=false. For an agent whose DispatchProfile row
// isn't implemented it returns an error naming the row and its reason, never
// an empty spelling: a profile asked for and silently not applied would leave
// the session at full context with nobody told.
func ProfileSettings(ag agent.Agent, drops ProfileDrops) (map[string]any, map[string]string, error) {
	spelling, err := profileSpellingFor(ag)
	if err != nil {
		return nil, nil, err
	}
	keys := map[string]any{}
	env := map[string]string{}
	if len(drops.Tools) > 0 {
		keys["permissions"] = map[string]any{"deny": slices.Clone(drops.Tools)}
	}
	if drops.BundledSkills {
		keys["disableBundledSkills"] = true
	}
	if drops.AccountConnectors {
		env[spelling.connectorsEnv] = spelling.connectorsOff
	}
	return keys, env, nil
}

// ProfileRequiredTools returns the tools a profile may not drop for ag. An
// agent without a profile spelling has none, since ProfileSettings refuses it
// first.
func ProfileRequiredTools(ag agent.Agent) []string {
	resolved, err := agent.ParseAgent(string(ag))
	if err != nil {
		return nil
	}
	return slices.Clone(profileSpellings[resolved].requiredTools)
}

// AllProfileEnvNames returns every environment name a profile can set, across
// every agent, sorted. It takes no agent on purpose: the launch-environment
// shaping that strips these names from an inherited environment runs where the
// agent that launched the parent session isn't known.
func AllProfileEnvNames() []string {
	var names []string
	for _, spelling := range profileSpellings {
		if spelling.connectorsEnv != "" && !slices.Contains(names, spelling.connectorsEnv) {
			names = append(names, spelling.connectorsEnv)
		}
	}
	slices.Sort(names)
	return names
}

// profileSpellingFor returns ag's spelling, or an error naming the
// DispatchProfile row when ag's declaration isn't implemented.
func profileSpellingFor(ag agent.Agent) (profileSpelling, error) {
	d, err := Lookup(DispatchProfile, ag)
	if err != nil {
		return profileSpelling{}, err
	}
	if d.State != StateImplemented {
		return profileSpelling{}, fmt.Errorf("agentplan: capability %s is unsupported for %s: %s", DispatchProfile, d.Agent, d.Reason)
	}
	spelling, ok := profileSpellings[d.Agent]
	if !ok {
		return profileSpelling{}, fmt.Errorf("agentplan: capability %s is declared for %s but has no spelling", DispatchProfile, d.Agent)
	}
	return spelling, nil
}
