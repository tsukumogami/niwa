package config

// The agent-neutral session declaration.
//
// [session] describes what a prepared session is given regardless of which
// agent runs in it. Two tables today: [session.env], the variables a session's
// commands see, and [session.posture], how much the session asks before it
// acts. Each agent's own format is generated from them -- Claude Code's
// settings env block, Codex's shell_environment_policy, Codex's approval_policy
// and sandbox_mode -- so a workspace declares the thing once and each agent
// gets it in the spelling it reads.
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
	Env     SessionEnvConfig     `toml:"env,omitempty"`
	Posture SessionPostureConfig `toml:"posture,omitempty"`
}

// SessionPostureConfig is [session.posture]: how much a prepared session asks
// before it acts, and how much of the machine it may touch when it does.
//
// The two keys are separate fields carrying separate declarations because they
// are separate decisions, and the separation is the safety property rather than
// a tidiness preference. Codex's most complete approval suppression --
// approval_policy = "never" paired with sandbox_mode = "danger-full-access" --
// collapses approvals and both the filesystem and network sandboxes into one
// setting, an asymmetry Claude Code's bypassPermissions does not have. A
// generator that read "this workspace wants fewer prompts" out of Approvals and
// inferred a Sandbox from it would therefore switch sandboxing off on the
// strength of a declaration that never mentioned it. Neither field is ever
// derived from the other: each is written only where its own key was set, and a
// workspace that leaves one empty gets no key for it at all.
//
// Both are empty by default and both are opt-in. A workspace that declares
// nothing here has niwa write nothing, and the agent's own defaults -- the ones
// the developer chose for themselves -- apply unchanged. niwa is never the
// reason a session runs with weaker guardrails than that.
//
// The values are niwa's own vocabulary rather than any agent's, and each agent
// maps them into its own spelling; internal/agentplan holds the mapping and the
// accepted set. Approvals: "on-untrusted", "on-failure", "on-request", "never".
// Sandbox: "read-only", "workspace-write", "full-access".
//
// Claude Code's approval posture keeps coming from [claude.settings]
// permissions, which is unchanged by this table. Pointing that agent at the
// neutral declaration too is a separate change; what this table must not do,
// and does not, is let one agent's key decide another agent's posture.
type SessionPostureConfig struct {
	Approvals string `toml:"approvals,omitempty"`
	Sandbox   string `toml:"sandbox,omitempty"`
}

// IsEmpty reports whether the table declares nothing.
func (s SessionPostureConfig) IsEmpty() bool {
	return s.Approvals == "" && s.Sandbox == ""
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
