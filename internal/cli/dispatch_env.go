package cli

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
)

// The environment a launched worker starts with.
//
// Every launch niwa makes -- `niwa dispatch`, and `niwa watch`'s review and
// its continuation -- shapes the worker's environment the same way, through
// applyLaunchEnv. Keeping it in one function is the point: a second launch
// path that built its own environment would be the one that leaks a removed
// variable or forgets the reap key.

// reapKeyPattern is the only shape a reap key may have when niwa sets it,
// writes it, or hands it to the on-reap command. Dispatch ids and session ids
// match it; anything else is left unset.
var reapKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// validReapKey reports whether key may be used as a reap key.
func validReapKey(key string) bool {
	return reapKeyPattern.MatchString(key)
}

// launchEnvShape is what a launch does to the worker's environment on top of
// the environment it starts from.
type launchEnvShape struct {
	// Lineage is the composed OTEL_RESOURCE_ATTRIBUTES value, "" for none.
	Lineage string
	// Passthrough and Unset are [session.dispatch_env]'s lists.
	Passthrough []string
	Unset       []string
	// ReapKey is the value NIWA_REAP_KEY gets. One that doesn't match
	// reapKeyPattern is not set, and an inherited value is removed either way.
	ReapKey string
}

// dispatchEnvLists reads [session.dispatch_env] from a loaded workspace
// configuration. A configuration that couldn't be loaded declares nothing.
func dispatchEnvLists(cfg *config.WorkspaceConfig) (passthrough, unset []string) {
	if cfg == nil {
		return nil, nil
	}
	return cfg.Session.DispatchEnv.Passthrough, cfg.Session.DispatchEnv.Unset
}

// loadDispatchEnvLists reads [session.dispatch_env] from the workspace
// configuration under workspaceRoot, for a launch path that hasn't loaded it.
//
// A configuration that can't be loaded declares nothing here, as it does for
// dispatch. It isn't reported because it can't get this far on its own: both
// launch paths provision the instance first, from the same file, and a file
// that doesn't load fails that step. The on-reap path is stricter for the
// opposite reason: there the file is read after the session is gone, and a
// silent "nothing declared" would skip a close nobody could repeat.
func loadDispatchEnvLists(workspaceRoot string) (passthrough, unset []string) {
	cfg, _ := loadWorkspaceConfig(workspaceRoot)
	return dispatchEnvLists(cfg)
}

// applyLaunchEnv shapes one launch: it returns the worker's process
// environment, built from base, and, when the agent takes a settings document,
// puts the matching "env" entry into settings. An agent with no settings flag
// gets the process environment only.
//
// The process environment is base with every Unset name, any inherited
// NIWA_REAP_KEY and, when a lineage value is composed, the inherited
// OTEL_RESOURCE_ATTRIBUTES removed; then the lineage value and the reap key
// are appended. Passthrough names need nothing here: base is niwa's own
// environment, so a declared name that is set is already in it.
//
// The settings env holds the lineage value, each Passthrough name set in base
// with its value from base, and the reap key. A name set to an empty string is
// set; one absent from base is skipped without a word.
//
// The gate is the agent's settings flag, not a capability-table row of its
// own: the "env" object is part of the settings document itself, so an agent
// that takes the document takes the object, and the one value that does
// depend on the agent (lineage) is already gated by its own row before it
// reaches Lineage here. An agent with no settings flag still gets the whole
// shaping in its process environment, which is the half every agent reads.
func applyLaunchEnv(spec agentplan.LaunchSpec, settings map[string]any, base []string, shape launchEnvShape) []string {
	proc, env := shapeLaunchEnv(base, shape)
	if spec.Flags.Settings != "" && env.Len() > 0 {
		settings["env"] = env
	}
	return proc
}

// shapeLaunchEnv computes both halves of applyLaunchEnv. See there.
func shapeLaunchEnv(base []string, shape launchEnvShape) ([]string, launchEnv) {
	drop := map[string]bool{config.ReapKeyEnv: true}
	for _, n := range shape.Unset {
		drop[n] = true
	}
	if shape.Lineage != "" {
		drop["OTEL_RESOURCE_ATTRIBUTES"] = true
	}

	values := make(map[string]string, len(base))
	proc := make([]string, 0, len(base)+2)
	for _, kv := range base {
		name, value, _ := strings.Cut(kv, "=")
		if drop[name] {
			continue
		}
		values[name] = value
		proc = append(proc, kv)
	}

	var env launchEnv
	if shape.Lineage != "" {
		proc = append(proc, "OTEL_RESOURCE_ATTRIBUTES="+shape.Lineage)
		env.Set("OTEL_RESOURCE_ATTRIBUTES", shape.Lineage)
	}
	for _, n := range shape.Passthrough {
		if v, ok := values[n]; ok {
			env.Set(n, v)
		}
	}
	if validReapKey(shape.ReapKey) {
		proc = append(proc, config.ReapKeyEnv+"="+shape.ReapKey)
		env.Set(config.ReapKeyEnv, shape.ReapKey)
	}
	return proc, env
}

// launchEnv is the launch settings document's "env" value: a small ordered
// map of string to string. Its fields are unexported and it marshals to a JSON
// object of string values and nothing else, so no value can add a settings
// key or change the document's shape.
type launchEnv struct {
	keys   []string
	values map[string]string
}

// Set adds name, or replaces its value in place when it is already present.
func (e *launchEnv) Set(name, value string) {
	if e.values == nil {
		e.values = map[string]string{}
	}
	if _, ok := e.values[name]; !ok {
		e.keys = append(e.keys, name)
	}
	e.values[name] = value
}

// Get returns name's value and whether it is present.
func (e launchEnv) Get(name string) (string, bool) {
	v, ok := e.values[name]
	return v, ok
}

// Len is the number of entries.
func (e launchEnv) Len() int { return len(e.keys) }

// MarshalJSON renders the entries as one JSON object, in insertion order.
func (e launchEnv) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range e.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(e.values[k])
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
