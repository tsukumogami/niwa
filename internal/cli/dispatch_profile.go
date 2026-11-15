package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
)

// A dispatch profile trims one launch: `niwa dispatch --profile <name>` looks
// the name up in [session.dispatch_profiles], asks agentplan how the launched
// agent spells its drops, and adds the result to that launch's settings
// document and environment. Nothing else changes, so a dispatch without the
// flag launches exactly what it did before profiles existed.

// dispatchProfile is the --profile flag's value, "" when it wasn't given.
var dispatchProfile string

const dispatchProfileFlagUsage = "a [session.dispatch_profiles] name to launch the worker with: the tools it names are denied, and bundled skills and the account's connectors are left out when it says so. It applies to this launch only and refuses, before anything is provisioned, for an unknown name, an agent with no profile support, or a profile that drops a tool the dispatch path needs"

// dispatchLaunchProfile is a profile resolved for one launch: its name, the
// settings keys it adds to the launch document, and the env values it sets.
// The zero value is a launch without a profile and contributes nothing.
type dispatchLaunchProfile struct {
	Name string
	Keys map[string]any
	Env  map[string]string
}

// resolveDispatchProfile resolves name for a launch of ag, from the workspace
// config dispatch loaded. Every refusal names its cause, and all of them come
// before provisioning: a profile asked for and silently not applied would
// leave the worker at full context with nobody told.
func resolveDispatchProfile(name string, cfg *config.WorkspaceConfig, ag agent.Agent) (dispatchLaunchProfile, error) {
	if name == "" {
		return dispatchLaunchProfile{}, nil
	}
	if cfg == nil {
		return dispatchLaunchProfile{}, fmt.Errorf("dispatch profile %q: the workspace config didn't load, so no profile can be resolved", name)
	}
	declared, ok := cfg.Session.DispatchProfiles[name]
	if !ok {
		names := make([]string, 0, len(cfg.Session.DispatchProfiles))
		for n := range cfg.Session.DispatchProfiles {
			names = append(names, n)
		}
		slices.Sort(names)
		known := "none are declared"
		if len(names) > 0 {
			known = "declared: " + strings.Join(names, ", ")
		}
		return dispatchLaunchProfile{}, fmt.Errorf("unknown dispatch profile %q: [session.dispatch_profiles] has no such entry (%s)", name, known)
	}
	decl, err := agentplan.Lookup(agentplan.DispatchProfile, ag)
	if err != nil {
		return dispatchLaunchProfile{}, err
	}
	if decl.State != agentplan.StateImplemented {
		return dispatchLaunchProfile{}, fmt.Errorf("--profile does not apply to the %q agent. %s", ag, decl.Reason)
	}
	// The rule holds whether or not keep-alive is on: keep-alive resolves
	// only after provisioning, so a rule that waited for it couldn't refuse
	// before anything is created.
	for _, required := range agentplan.ProfileRequiredTools(ag) {
		if slices.Contains(declared.DropTools, required) {
			return dispatchLaunchProfile{}, fmt.Errorf("dispatch profile %q drops %s, which a dispatched %q worker needs; remove it from drop_tools", name, required, ag)
		}
	}
	keys, env, err := agentplan.ProfileSettings(ag, agentplan.ProfileDrops{
		Tools:             declared.DropTools,
		BundledSkills:     declared.DropBundledSkills,
		AccountConnectors: declared.DropAccountConnectors,
	})
	if err != nil {
		return dispatchLaunchProfile{}, err
	}
	if perms, ok := keys[profilePermissionsKey]; ok {
		deny, err := profileDenyNames(perms)
		if err != nil {
			return dispatchLaunchProfile{}, fmt.Errorf("dispatch profile %q: %w", name, err)
		}
		keys[profilePermissionsKey] = profileDenyList{names: deny}
	}
	return dispatchLaunchProfile{Name: name, Keys: keys, Env: env}, nil
}

// profilePermissionsKey is the settings key a profile's deny list rides.
const profilePermissionsKey = "permissions"

// profileDenyNames takes the deny names out of the permissions value
// agentplan spells, refusing any other shape.
func profileDenyNames(perms any) ([]string, error) {
	m, ok := perms.(map[string]any)
	if !ok || len(m) != 1 {
		return nil, fmt.Errorf("internal: the profile's permissions value is not a lone deny list")
	}
	deny, ok := m["deny"].([]string)
	if !ok {
		return nil, fmt.Errorf("internal: the profile's deny list is not a list of names")
	}
	for _, n := range deny {
		if err := config.ValidateProfileToolName(n); err != nil {
			return nil, err
		}
	}
	return deny, nil
}

// addTo puts the profile's settings keys into the launch document. A key some
// other contributor already set is a niwa bug, reported rather than
// overwritten.
func (p dispatchLaunchProfile) addTo(settings map[string]any) error {
	for k, v := range p.Keys {
		if _, taken := settings[k]; taken {
			return fmt.Errorf("internal: the dispatch profile's %q key is already in the launch settings", k)
		}
		settings[k] = v
	}
	return nil
}
