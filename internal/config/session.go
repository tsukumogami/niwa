package config

// The agent-neutral session declaration.
//
// [session] describes what a prepared session is given regardless of which
// agent runs in it. Today that is one table, [session.env]: the variables a
// session's commands see. Each agent's own format is generated from it --
// Claude Code's settings env block, Codex's shell_environment_policy -- so a
// workspace declares the variable once and both agents get it.
//
// It is deliberately not spelled under any agent's namespace. [claude.env]
// stays where it is and keeps its meaning, because it writes into a
// Claude-owned file format and no second agent reads it; what it must not do
// is decide whether another agent's session gets an environment at all. That
// is why the neutral table exists rather than a second consumer being pointed
// at the Claude-named one.

// SessionConfig is the workspace-level [session] table.
//
// It is workspace-scoped, like [mcp]: the environment a session runs with is a
// property of the prepared workspace rather than of one repository inside it,
// and no per-repo override position merges it. A repository that needs its own
// variables has the [env] pipeline, which is per-repo and untouched by this.
type SessionConfig struct {
	Env SessionEnvConfig `toml:"env,omitempty"`
}

// SessionEnvConfig is [session.env]: the variables every prepared session gets.
//
// Promote lists keys to pull from the resolved [env] pipeline, exactly as
// [claude.env].promote does. Vars is the inline key-value map, and its values
// are MaybeSecret, so a vault:// reference resolves through the same pipeline
// every other secret slot uses. There is no separate secrets sub-table: a
// neutral declaration has one destination shape per agent, so a second
// sensitivity-coded sibling would deliver identically to the first, and the
// vault reference in Vars is already how a secret is declared here.
//
// What lands in a generated file is always the resolved literal. Neither
// generated format performs variable expansion -- Codex measurably performs
// none anywhere -- so a value that still carries interpolation syntax after
// resolution is an authoring bug the generators refuse rather than pass on.
type SessionEnvConfig struct {
	Promote []string     `toml:"promote,omitempty"`
	Vars    EnvVarsTable `toml:"vars,omitempty"`
}

// IsEmpty reports whether the table declares nothing.
func (s SessionEnvConfig) IsEmpty() bool {
	return len(s.Promote) == 0 && s.Vars.IsEmpty()
}
