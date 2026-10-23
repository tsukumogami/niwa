package config

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

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

	// DispatchEnv and OnReap are read when niwa launches or removes a
	// session, never at apply. See their types.
	DispatchEnv SessionDispatchEnvConfig `toml:"dispatch_env,omitempty"`
	OnReap      SessionOnReapConfig      `toml:"on_reap,omitempty"`
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

// IsEmpty reports whether the table declares nothing. The dispatch-time
// [session.dispatch_env] lists are a different table and never count here.
func (s SessionEnvConfig) IsEmpty() bool {
	return len(s.Promote) == 0 && s.Vars.IsEmpty()
}

// SessionDispatchEnvConfig is [session.dispatch_env]: variables niwa moves
// between its own environment and a worker's when it launches one.
//
// It is dispatch-time, unlike [session.env], which is written at apply. That
// table is resolved and written into generated files when the workspace is
// applied, so every session started in an instance sees it. This one is read
// when niwa launches a worker, from niwa's own environment at that moment,
// and reaches only that worker. Nothing that runs at apply reads it, and it
// counts toward neither SessionEnvConfig.IsEmpty nor any env block apply
// writes.
//
// Passthrough names variables copied from niwa's environment into a launched
// worker's environment: its settings document's env object, for an agent that
// takes one, and its process environment. A name that isn't set when the
// launch runs is skipped without a word. Unset names variables removed from
// the worker's process environment, so a value the launching shell carries
// never reaches a worker that has nothing to replace it with.
//
// The settings document rides the worker's command line, where other users of
// the host can read it in a process listing, so the load refuses names that
// look like credentials (see ValidateSessionEnvName). The heuristic can't
// catch every secret; what keeps credentials out is that a workspace declares
// exact names, and only the ones it needs.
type SessionDispatchEnvConfig struct {
	Passthrough []string `toml:"passthrough,omitempty"`
	Unset       []string `toml:"unset,omitempty"`
}

// IsEmpty reports whether the table declares nothing.
func (s SessionDispatchEnvConfig) IsEmpty() bool {
	return len(s.Passthrough) == 0 && len(s.Unset) == 0
}

// DefaultOnReapTimeout bounds one on-reap run when [session.on_reap] sets no
// timeout.
const DefaultOnReapTimeout = 10 * time.Second

// SessionOnReapConfig is [session.on_reap]: a command niwa runs once for each
// session it removes.
//
// Command runs under sh -c from the workspace root with a fixed environment:
// PATH, HOME, NIWA_REAP_KEY, NIWA_SESSION_ID, NIWA_INSTANCE_PATH, and each
// name in Env that is set in niwa's environment. Env is the command's own
// list. It reaches only the command's process environment, never a command
// line, so a name may appear here and in [session.dispatch_env] too; it gets
// the same load-time refusals all the same.
//
// Timeout bounds one run, "10s" when empty. The command has the trust level of
// a hook the workspace configuration installs.
type SessionOnReapConfig struct {
	Command string   `toml:"command,omitempty"`
	Timeout string   `toml:"timeout,omitempty"`
	Env     []string `toml:"env,omitempty"`
}

// TimeoutDuration returns the per-run timeout. The load refused a value that
// doesn't parse or isn't positive, so anything else reads as the default.
func (s SessionOnReapConfig) TimeoutDuration() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(s.Timeout)); err == nil && d > 0 {
		return d
	}
	return DefaultOnReapTimeout
}

// ReapKeyEnv is the variable niwa sets to a session's reap key: the dispatch
// id for a session niwa launched, the session id for one a SessionStart hook
// provisioned an instance for.
const ReapKeyEnv = "NIWA_REAP_KEY"

// LaunchSettingsEnvNames are the names niwa itself writes into a launched
// worker's settings env object. A declared name equal to one of them would
// fight niwa for the slot. The NIWA_ prefix already covers the reap key; it is
// listed so this stays the one place that says what niwa sets.
var LaunchSettingsEnvNames = []string{"OTEL_RESOURCE_ATTRIBUTES", ReapKeyEnv}

// credentialSuffixes and credentialPrefixes are the shapes of a variable name
// that usually holds a credential, matched against the upper-cased name.
var (
	credentialSuffixes = []string{"_KEY", "_APIKEY", "_SECRET", "_TOKEN", "_PAT", "_PASSWORD", "_PASSWD", "_CREDENTIALS"}
	credentialPrefixes = []string{"AWS_"}
)

// envNamePattern is what a declared variable name must look like at all.
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateSessionEnvName reports why name may not be declared in
// [session.dispatch_env] or [session.on_reap] env, or nil when it may. Letter
// case never rescues a name: every refusal compares the upper-cased form.
//
// The credential refusal exists for the pass-through list, whose values ride
// a worker's command line. [session.on_reap] env takes the same refusals even
// though its values reach only the command's environment: one rule for every
// list a workspace declares names in, so a name that is fine in one is never
// a surprise in the other, and the close command, which runs unattended on
// every reap, is not where a credential should be handed out either.
func ValidateSessionEnvName(name string) error {
	if !envNamePattern.MatchString(name) {
		return fmt.Errorf("not a variable name: use letters, digits and '_', not starting with a digit")
	}
	upper := strings.ToUpper(name)
	for _, p := range []string{"NIWA_", "CLAUDE_"} {
		if strings.HasPrefix(upper, p) {
			return fmt.Errorf("names starting %s are reserved", p)
		}
	}
	for _, n := range LaunchSettingsEnvNames {
		if upper == n {
			return fmt.Errorf("niwa sets %s itself", n)
		}
	}
	for _, s := range credentialSuffixes {
		if strings.HasSuffix(upper, s) {
			return fmt.Errorf("it looks like a credential (ends %s); these lists never carry credentials", s)
		}
	}
	for _, p := range credentialPrefixes {
		if strings.HasPrefix(upper, p) {
			return fmt.Errorf("it looks like a credential (starts %s); these lists never carry credentials", p)
		}
	}
	return nil
}

// validateSessionDeclaration applies the load-time refusals for the
// dispatch-time and reap-time tables.
func validateSessionDeclaration(s SessionConfig) error {
	declared := sessionEnvDeclaredNames(s.Env)
	checkList := func(field string, names []string) error {
		seen := make(map[string]bool, len(names))
		for _, name := range names {
			if err := ValidateSessionEnvName(name); err != nil {
				return fmt.Errorf("%s: %q: %w", field, name, err)
			}
			if seen[name] {
				return fmt.Errorf("%s: %q is listed more than once", field, name)
			}
			seen[name] = true
			if declared[name] {
				return fmt.Errorf("%s: %q is also declared in [session.env] vars or promote; declare it in one place", field, name)
			}
		}
		return nil
	}

	if err := checkList("session.dispatch_env.passthrough", s.DispatchEnv.Passthrough); err != nil {
		return err
	}
	if err := checkList("session.dispatch_env.unset", s.DispatchEnv.Unset); err != nil {
		return err
	}
	passed := make(map[string]bool, len(s.DispatchEnv.Passthrough))
	for _, name := range s.DispatchEnv.Passthrough {
		passed[name] = true
	}
	for _, name := range s.DispatchEnv.Unset {
		if passed[name] {
			return fmt.Errorf("session.dispatch_env: %q is listed in both passthrough and unset", name)
		}
	}

	if err := checkList("session.on_reap.env", s.OnReap.Env); err != nil {
		return err
	}
	if t := strings.TrimSpace(s.OnReap.Timeout); t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			return fmt.Errorf("session.on_reap.timeout: %q is not a duration (for example \"10s\")", s.OnReap.Timeout)
		}
		if d <= 0 {
			return fmt.Errorf("session.on_reap.timeout: %q must be positive", s.OnReap.Timeout)
		}
	}
	return nil
}

// sessionEnvDeclaredNames is every name [session.env] declares, through
// promote or any of the vars tables.
func sessionEnvDeclaredNames(e SessionEnvConfig) map[string]bool {
	names := map[string]bool{}
	for _, n := range e.Promote {
		names[n] = true
	}
	for n := range e.Vars.Values {
		names[n] = true
	}
	for _, m := range []map[string]string{e.Vars.Required, e.Vars.Recommended, e.Vars.Optional} {
		for n := range m {
			names[n] = true
		}
	}
	return names
}
