// Package otelprecedence computes the OTEL_RESOURCE_ATTRIBUTES value a
// dispatched Claude worker ends up with, from what niwa handed it: the launch's
// argv and environment, plus the developer's user settings file.
//
// It exists for tests. niwa sends its lineage value twice -- as the "env" entry
// of the --settings document and in the launched process's environment --
// because which of those wins is Claude Code's choice, not niwa's, and could
// change between releases. The tests check the worker's value under each order
// Claude Code could apply, so a launch missing either copy fails at least one
// of them. Nothing in the product imports this package.
package otelprecedence

import (
	"encoding/json"
	"os"
	"strings"
)

// Variable is the environment variable every layer carries.
const Variable = "OTEL_RESOURCE_ATTRIBUTES"

// Mode is one order in which the layers could be applied. Each layer replaces
// the whole value of the one below it; none of them merges.
type Mode int

const (
	// SettingsFirst: the process environment, then the user settings file's
	// "env", then the --settings document's "env", each replacing the last.
	SettingsFirst Mode = iota
	// LaunchEnvFirst: the process environment's value when it is set, and
	// the settings chain otherwise.
	LaunchEnvFirst
	// NoLaunchEnv: the settings chain alone, as for a worker whose process
	// environment doesn't come from the launch at all.
	NoLaunchEnv
)

// Modes lists every Mode, for tests that range over them.
var Modes = []Mode{SettingsFirst, LaunchEnvFirst, NoLaunchEnv}

func (m Mode) String() string {
	switch m {
	case SettingsFirst:
		return "settings-first"
	case LaunchEnvFirst:
		return "launch-environment-first"
	case NoLaunchEnv:
		return "no-launch-environment"
	}
	return "unknown"
}

// Launch is what a test recorded of one launch.
type Launch struct {
	// Argv is the launched command's arguments; the element after
	// --settings, when present, is the settings document.
	Argv []string
	// Env is the launched process's environment as KEY=VALUE entries.
	Env []string
	// UserSettings is the value of Variable in the user settings file's
	// "env" object, and UserSettingsSet says whether it had one.
	UserSettings    string
	UserSettingsSet bool
}

// Effective returns the worker's value under mode, and whether any layer set
// one at all.
func (l Launch) Effective(mode Mode) (string, bool) {
	chain := func(base string, set bool) (string, bool) {
		if l.UserSettingsSet {
			base, set = l.UserSettings, true
		}
		if v, ok := l.settingsValue(); ok {
			base, set = v, true
		}
		return base, set
	}
	envValue, envSet := l.envValue()
	switch mode {
	case SettingsFirst:
		return chain(envValue, envSet)
	case LaunchEnvFirst:
		if envSet {
			return envValue, true
		}
		return chain("", false)
	case NoLaunchEnv:
		return chain("", false)
	}
	return "", false
}

func (l Launch) envValue() (string, bool) {
	value, set := "", false
	for _, kv := range l.Env {
		if v, ok := strings.CutPrefix(kv, Variable+"="); ok {
			value, set = v, true
		}
	}
	return value, set
}

func (l Launch) settingsValue() (string, bool) {
	value, set := "", false
	for i := 0; i+1 < len(l.Argv); i++ {
		if l.Argv[i] != "--settings" {
			continue
		}
		if v, ok := envValueIn([]byte(l.Argv[i+1])); ok {
			value, set = v, true
		}
	}
	return value, set
}

// UserSettingsValue reads Variable from the "env" object of the settings file
// at path. A missing file, a document that isn't JSON, or an "env" without the
// variable all report unset.
func UserSettingsValue(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return envValueIn(data)
}

func envValueIn(doc []byte) (string, bool) {
	var parsed struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		return "", false
	}
	v, ok := parsed.Env[Variable]
	return v, ok
}

// Entries splits a value into its key=value entries, keyed by key. A later
// entry for the same key replaces an earlier one, as an OpenTelemetry resource
// parser does.
func Entries(value string) map[string]string {
	out := map[string]string{}
	if value == "" {
		return out
	}
	for _, e := range strings.Split(value, ",") {
		k, v, _ := strings.Cut(e, "=")
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}
