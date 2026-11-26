package config

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/remoteurl"
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

	// DispatchEnv, OnReap, BeforeRemove and Removal are read when niwa
	// launches or removes a session, never at apply. See their types.
	DispatchEnv  SessionDispatchEnvConfig  `toml:"dispatch_env,omitempty"`
	OnReap       SessionOnReapConfig       `toml:"on_reap,omitempty"`
	BeforeRemove SessionBeforeRemoveConfig `toml:"before_remove,omitempty"`
	Removal      SessionRemovalConfig      `toml:"removal,omitempty"`

	// DispatchProfiles is [session.dispatch_profiles.<name>], keyed by the
	// profile name. Like DispatchEnv it is read only when `niwa dispatch`
	// launches a worker with a profile, never at apply. See
	// SessionDispatchProfile.
	DispatchProfiles map[string]SessionDispatchProfile `toml:"dispatch_profiles,omitempty"`

	// DefaultDispatchProfile names the profile a `niwa dispatch` takes when it
	// names none with --profile. It must name a declared profile. It applies
	// to `niwa dispatch` only: `niwa watch`'s launches, `niwa resume` and
	// `niwa retask` never read it. Empty means no default.
	DefaultDispatchProfile string `toml:"default_dispatch_profile,omitempty"`
}

// The two values [session.removal] accepts for ignored_files and job_scratch.
// An empty value means RemovalDisposable.
const (
	RemovalDisposable = "disposable"
	RemovalUnique     = "unique"
)

// SessionRemovalConfig is [session.removal]: how niwa classifies what it finds
// in a unit it is about to remove. It is read only when niwa removes a unit,
// never at apply.
//
// IgnoredFiles decides whether a git-ignored path outside a build tree is
// disposable (the default) or unique. JobScratch decides the same for job
// scratch in a unit that has no handoff brief. Each takes RemovalDisposable,
// RemovalUnique, or empty for the default.
//
// Disposable adds workspace patterns whose matches count as disposable. Each
// entry needs a non-empty review naming the review that added the pattern.
// That field is an audit trail, not a control: niwa checks only that it is
// there.
//
// ProofRemotes are URL prefixes, each an https:// or ssh:// URL ending in "/"
// with a host and at least one path segment, under which a repository's
// remote counts as proof that a commit exists elsewhere, beside the clone URL
// the configuration gives it (for people who push to forks). Empty by
// default; a prefix that doesn't parse is a configuration error.
type SessionRemovalConfig struct {
	IgnoredFiles string              `toml:"ignored_files,omitempty"`
	JobScratch   string              `toml:"job_scratch,omitempty"`
	Disposable   []DisposablePattern `toml:"disposable,omitempty"`
	ProofRemotes []string            `toml:"proof_remotes,omitempty"`
}

// DisposablePattern is one [[session.removal.disposable]] entry: a relative,
// slash-separated glob whose segments are each "**" or a path.Match pattern,
// and the review that added it. One trailing "/" is allowed.
type DisposablePattern struct {
	Pattern string `toml:"pattern"`
	Review  string `toml:"review"`
}

// IgnoredUnique reports whether a git-ignored path outside a build tree is
// unique. The default, an empty value, is disposable.
func (s SessionRemovalConfig) IgnoredUnique() bool {
	return s.IgnoredFiles == RemovalUnique
}

// JobScratchUnique reports whether job scratch in a unit with no handoff brief
// is unique. The default, an empty value, is disposable.
func (s SessionRemovalConfig) JobScratchUnique() bool {
	return s.JobScratch == RemovalUnique
}

// SessionDispatchProfile is one [session.dispatch_profiles.<name>] table:
// what a dispatch that selects the profile removes from the launched session,
// and the one setting it may give it.
//
// The keys name what is removed or set, not how an agent spells it. The
// per-agent spelling lives in internal/agentplan, so the workspace file states
// intent and an agent without a spelling refuses the profile instead of
// ignoring it. Nothing here can grant a tool.
//
// DropTools names whole tools, each matching ValidateProfileToolName.
// DropBundledSkills removes the agent's bundled skills. DropAccountConnectors
// stops the account-level connectors from loading. AutoCompactWindow is the
// Claude Code compaction window, written as CLAUDE_CODE_AUTO_COMPACT_WINDOW in
// the launch settings document's env object and nowhere else. A profile must
// declare at least one drop or the window.
type SessionDispatchProfile struct {
	DropTools             []string          `toml:"drop_tools,omitempty"`
	DropBundledSkills     bool              `toml:"drop_bundled_skills,omitempty"`
	DropAccountConnectors bool              `toml:"drop_account_connectors,omitempty"`
	AutoCompactWindow     AutoCompactWindow `toml:"auto_compact_window,omitempty"`
}

// IsEmpty reports whether the profile declares no drop and no setting.
func (p SessionDispatchProfile) IsEmpty() bool {
	return len(p.DropTools) == 0 && !p.DropBundledSkills && !p.DropAccountConnectors && !p.AutoCompactWindow.IsSet()
}

// AutoCompactWindowEnv is the Claude Code setting a profile's
// auto_compact_window is written as.
const AutoCompactWindowEnv = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"

// MinAutoCompactWindow and MaxAutoCompactWindow bound auto_compact_window,
// both included.
const (
	MinAutoCompactWindow = 100_000
	MaxAutoCompactWindow = 1_000_000
)

// AutoCompactWindow is a profile's auto_compact_window. It holds the value
// exactly as the TOML decoder produced it, so a float or a string reaches
// validateDispatchProfiles and is refused there with the profile and the key
// named, rather than by the decoder with neither.
type AutoCompactWindow struct {
	raw any
}

// NewAutoCompactWindow returns a window set to n.
func NewAutoCompactWindow(n int64) AutoCompactWindow {
	return AutoCompactWindow{raw: n}
}

// UnmarshalTOML keeps the decoded value as it is; validation happens at load.
func (w *AutoCompactWindow) UnmarshalTOML(v any) error {
	w.raw = v
	return nil
}

// MarshalTOML writes a valid window as a TOML integer.
func (w AutoCompactWindow) MarshalTOML() ([]byte, error) {
	n, ok := w.Value()
	if !ok {
		return nil, fmt.Errorf("auto_compact_window holds no integer from %d to %d", MinAutoCompactWindow, MaxAutoCompactWindow)
	}
	return []byte(strconv.FormatInt(n, 10)), nil
}

// IsSet reports whether the profile sets the key at all, valid or not.
func (w AutoCompactWindow) IsSet() bool {
	return w.raw != nil
}

// Value returns the window and true when it is an integer inside the bounds,
// or 0 and false otherwise.
func (w AutoCompactWindow) Value() (int64, bool) {
	n, ok := w.raw.(int64)
	if !ok || n < MinAutoCompactWindow || n > MaxAutoCompactWindow {
		return 0, false
	}
	return n, true
}

// check reports why the set value isn't a window. An integer out of bounds is
// quoted; any other value is described by its kind and never printed.
func (w AutoCompactWindow) check() error {
	bounds := fmt.Sprintf("use an integer from %d to %d", MinAutoCompactWindow, MaxAutoCompactWindow)
	switch v := w.raw.(type) {
	case int64:
		if _, ok := w.Value(); !ok {
			return fmt.Errorf("%q is out of range; %s", strconv.FormatInt(v, 10), bounds)
		}
		return nil
	case float64:
		return fmt.Errorf("a float is not a window; %s", bounds)
	case string:
		return fmt.Errorf("a string is not a window; %s", bounds)
	default:
		return fmt.Errorf("not an integer; %s", bounds)
	}
}

// autoCompactWindowPattern is the only text a recorded window may be: a
// decimal integer with no sign and no leading zero.
var autoCompactWindowPattern = regexp.MustCompile(`^[1-9][0-9]{0,15}$`)

// ValidateAutoCompactWindow reports why value, the text a launch record holds
// for CLAUDE_CODE_AUTO_COMPACT_WINDOW, isn't a window, or nil when it is. The
// error never repeats the value, so a caller that prints it quotes it itself.
func ValidateAutoCompactWindow(value string) error {
	if !autoCompactWindowPattern.MatchString(value) {
		return fmt.Errorf("not a decimal integer")
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < MinAutoCompactWindow || n > MaxAutoCompactWindow {
		return fmt.Errorf("outside %d to %d", MinAutoCompactWindow, MaxAutoCompactWindow)
	}
	return nil
}

// profileNamePattern is what a dispatch profile's name must look like.
var profileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ValidProfileName reports whether name has the shape a dispatch profile's
// name has. Config load refuses any other name, so a reader of a profile name
// recorded in a file any same-user process can edit, such as a session
// mapping, uses this to tell a value niwa could have written from one it
// couldn't.
func ValidProfileName(name string) bool {
	return profileNamePattern.MatchString(name)
}

// profileToolNamePatterns are the two shapes a dropped tool's name may take: a
// built-in tool, which starts upper-case, and an MCP tool.
var profileToolNamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`),
	regexp.MustCompile(`^mcp__[A-Za-z0-9_-]+$`),
}

// ValidateProfileToolName reports why name may not appear in a dispatch
// profile's drop_tools, or nil when it may. It is the one copy of the grammar:
// config load, the dispatch-time deny-list renderer and resume all call it.
//
// The grammar refuses a lowercase name, which the agent treats as an unusable
// entry, and a scoped rule such as `Bash(git *)`: a scoped rule is a
// permission rule, not a removal, and doesn't belong in a profile.
func ValidateProfileToolName(name string) error {
	for _, p := range profileToolNamePatterns {
		if p.MatchString(name) {
			return nil
		}
	}
	return fmt.Errorf("not a whole-tool name: use a tool name such as \"Artifact\" or \"mcp__server_tool\", with no lowercase start and no scoped rule")
}

// ProfileEnvNames are the environment names a dispatch profile may set in a
// launched worker's environment. A workspace may not declare any of them in
// [session.dispatch_env], as a passthrough or an unset name: the profile owns
// the slot, and a passthrough would both fight the profile for it and let a
// resume accept the value on a record that names no profile.
//
// AutoCompactWindowEnv is among them, though [session.dispatch_env] refuses it
// first as a reserved CLAUDE_ name.
//
// internal/agentplan can't import this package, so a test in internal/cli
// holds this list equal to agentplan.AllProfileEnvNames().
var ProfileEnvNames = []string{AutoCompactWindowEnv, "ENABLE_CLAUDEAI_MCP_SERVERS"}

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

// DefaultBeforeRemoveTimeout bounds one pre-removal run when
// [session.before_remove] sets no timeout.
const DefaultBeforeRemoveTimeout = 10 * time.Second

// SessionBeforeRemoveConfig is [session.before_remove]: a command niwa runs
// immediately before it removes a path a session may still need, and whose
// answer decides whether the removal happens.
//
// It is the opposite of [session.on_reap] in the one way that matters. That
// command runs after a removal, is retried when it fails and is eventually
// given up on. This one runs before, and exit 0 is the only answer that lets
// the removal go ahead: any other exit, a timeout or a failure to start keeps
// the path, so a broken command can keep directories but never remove one niwa
// would have kept without it. The two are separate tables so neither meaning
// can be declared by mistake for the other.
//
// Command runs under sh -c from the workspace root with stdin empty and a
// fixed environment: PATH, HOME, NIWA_REMOVE_PATH, NIWA_REMOVE_STEP,
// NIWA_SESSION_IDS, and each name in Env that is set in niwa's environment.
// Timeout bounds one run, "10s" when empty. The command has the trust level of
// a hook the workspace configuration installs.
type SessionBeforeRemoveConfig struct {
	Command string   `toml:"command,omitempty"`
	Timeout string   `toml:"timeout,omitempty"`
	Env     []string `toml:"env,omitempty"`
}

// TimeoutDuration returns the per-run timeout. The load refused a value that
// doesn't parse or isn't positive, so anything else reads as the default.
func (s SessionBeforeRemoveConfig) TimeoutDuration() time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(s.Timeout)); err == nil && d > 0 {
		return d
	}
	return DefaultBeforeRemoveTimeout
}

// ReapKeyEnv is the variable niwa sets to a session's reap key: the dispatch
// id for a session niwa launched, the session id for one a SessionStart hook
// provisioned an instance for.
const ReapKeyEnv = "NIWA_REAP_KEY"

// DispatchIDEnv and WorkspaceEnv carry a dispatched worker's identity: the
// dispatch id and the workspace name. Only `niwa dispatch` sets them; every
// other launch removes an inherited value.
const (
	DispatchIDEnv = "NIWA_DISPATCH_ID"
	WorkspaceEnv  = "NIWA_WORKSPACE"
)

// DispatchProfileEnv marks a launch made with a dispatch profile: `niwa
// dispatch --profile <name>` sets it to the profile's name. A launch that
// inherits it removes it, along with every environment name a profile can
// set, so a worker dispatched without a profile from inside a profiled one
// doesn't inherit the profile.
const DispatchProfileEnv = "NIWA_DISPATCH_PROFILE"

// LaunchSettingsEnvNames are the names niwa itself writes into a launched
// worker's settings env object. A declared name equal to one of them would
// fight niwa for the slot. The NIWA_ prefix already covers the reap key and
// the identity names; they are listed so this stays the one place that says
// what niwa sets.
var LaunchSettingsEnvNames = []string{"OTEL_RESOURCE_ATTRIBUTES", ReapKeyEnv, DispatchIDEnv, WorkspaceEnv, DispatchProfileEnv}

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
	for _, list := range []struct {
		field string
		names []string
	}{
		{"session.dispatch_env.passthrough", s.DispatchEnv.Passthrough},
		{"session.dispatch_env.unset", s.DispatchEnv.Unset},
	} {
		for _, name := range list.names {
			upper := strings.ToUpper(name)
			for _, owned := range ProfileEnvNames {
				if upper == owned {
					return fmt.Errorf("%s: %q: a dispatch profile owns %s; remove the entry and declare the drop in [session.dispatch_profiles.<name>] instead", list.field, name, owned)
				}
			}
		}
	}
	if err := validateDispatchProfiles(s.DispatchProfiles); err != nil {
		return err
	}
	if d := s.DefaultDispatchProfile; d != "" {
		if _, ok := s.DispatchProfiles[d]; !ok {
			return fmt.Errorf("session.default_dispatch_profile: %q names no profile in [session.dispatch_profiles]", d)
		}
	}

	if err := checkList("session.on_reap.env", s.OnReap.Env); err != nil {
		return err
	}
	if err := checkTimeout("session.on_reap.timeout", s.OnReap.Timeout); err != nil {
		return err
	}
	if err := checkList("session.before_remove.env", s.BeforeRemove.Env); err != nil {
		return err
	}
	if err := checkTimeout("session.before_remove.timeout", s.BeforeRemove.Timeout); err != nil {
		return err
	}
	if err := validateSessionRemoval(s.Removal); err != nil {
		return &RemovalSettingsError{Err: err}
	}
	return nil
}

// RemovalSettingsError is a refusal of the settings the removal rule reads,
// [session.removal] or a repository's build_trees. A pass that meets one
// removes nothing, since it can't tell what a unit holds without them.
type RemovalSettingsError struct {
	Err error
}

func (e *RemovalSettingsError) Error() string { return e.Err.Error() }

func (e *RemovalSettingsError) Unwrap() error { return e.Err }

// validateSessionRemoval applies the load-time refusals for [session.removal].
func validateSessionRemoval(r SessionRemovalConfig) error {
	if err := checkRemovalValue("session.removal.ignored_files", r.IgnoredFiles); err != nil {
		return err
	}
	if err := checkRemovalValue("session.removal.job_scratch", r.JobScratch); err != nil {
		return err
	}
	for i, d := range r.Disposable {
		field := fmt.Sprintf("session.removal.disposable[%d]", i)
		if strings.TrimSpace(d.Pattern) == "" {
			return fmt.Errorf("%s: pattern is required", field)
		}
		if strings.TrimSpace(d.Review) == "" {
			return fmt.Errorf("%s: review is required", field)
		}
		if err := checkDisposablePattern(d.Pattern); err != nil {
			return fmt.Errorf("%s: pattern %q: %w", field, d.Pattern, err)
		}
	}
	for i, p := range r.ProofRemotes {
		// The reason never repeats the prefix: one carrying userinfo could
		// hold a credential.
		if _, err := remoteurl.ParsePrefix(p); err != nil {
			return fmt.Errorf("session.removal.proof_remotes[%d]: %w", i, err)
		}
	}
	return nil
}

// checkRemovalValue refuses a classification other than empty,
// RemovalDisposable or RemovalUnique.
func checkRemovalValue(field, value string) error {
	switch value {
	case "", RemovalDisposable, RemovalUnique:
		return nil
	}
	return fmt.Errorf("%s: %q must be %q or %q", field, value, RemovalDisposable, RemovalUnique)
}

// checkDisposablePattern reports why pattern is not a usable disposable glob,
// or nil when it is. One trailing "/" is allowed and marks a directory. A
// pattern none of whose segments holds a character other than "*" and "?"
// counts as matching every path and is refused: it would make everything in
// a unit disposable.
func checkDisposablePattern(pattern string) error {
	if strings.Contains(pattern, `\`) {
		return fmt.Errorf("must not contain a backslash; separate segments with \"/\"")
	}
	if strings.HasPrefix(pattern, "/") {
		return fmt.Errorf("must be relative, not start with \"/\"")
	}
	segs := strings.Split(strings.TrimSuffix(pattern, "/"), "/")
	for _, seg := range segs {
		switch seg {
		case "":
			return fmt.Errorf("has an empty segment")
		case ".", "..":
			return fmt.Errorf("has a %q segment", seg)
		case "**":
			continue
		}
		if _, err := path.Match(seg, ""); errors.Is(err, path.ErrBadPattern) {
			return fmt.Errorf("segment %q is not a valid glob", seg)
		}
	}
	for _, seg := range segs {
		if strings.Trim(seg, "*?") != "" {
			return nil
		}
	}
	return errors.New("matches every path")
}

// validateDispatchProfiles applies the load-time refusals for
// [session.dispatch_profiles]. This is the gate that counts: the dispatch-time
// renderer runs after an instance is provisioned, so a bad name has to stop
// here. Profiles are checked in name order so the first refusal is stable.
func validateDispatchProfiles(profiles map[string]SessionDispatchProfile) error {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !ValidProfileName(name) {
			return fmt.Errorf("session.dispatch_profiles: %q is not a profile name: use lowercase letters, digits and '-', starting with a letter or digit", name)
		}
		p := profiles[name]
		field := "session.dispatch_profiles." + name
		if p.IsEmpty() {
			return fmt.Errorf("%s: the profile drops and sets nothing; declare at least one of drop_tools, drop_bundled_skills, drop_account_connectors or auto_compact_window", field)
		}
		if p.AutoCompactWindow.IsSet() {
			if err := p.AutoCompactWindow.check(); err != nil {
				return fmt.Errorf("%s.auto_compact_window: %w", field, err)
			}
		}
		seen := make(map[string]bool, len(p.DropTools))
		for _, tool := range p.DropTools {
			if err := ValidateProfileToolName(tool); err != nil {
				return fmt.Errorf("%s.drop_tools: %q: %w", field, tool, err)
			}
			if seen[tool] {
				return fmt.Errorf("%s.drop_tools: %q is listed more than once", field, tool)
			}
			seen[tool] = true
		}
	}
	return nil
}

// checkTimeout refuses a timeout that is set but doesn't parse as a duration,
// or isn't positive. An empty value means the table's default.
func checkTimeout(field, value string) error {
	t := strings.TrimSpace(value)
	if t == "" {
		return nil
	}
	d, err := time.ParseDuration(t)
	if err != nil {
		return fmt.Errorf("%s: %q is not a duration (for example \"10s\")", field, value)
	}
	if d <= 0 {
		return fmt.Errorf("%s: %q must be positive", field, value)
	}
	return nil
}

// validateAutoCompactWindowNames refuses CLAUDE_CODE_AUTO_COMPACT_WINDOW, in
// any letter case, in every table of this config that renders into Claude
// Code's settings env, once some dispatch profile sets auto_compact_window. A
// profile is then where the window is set, and a value written into the
// instance's settings would reach every session started there, profiled or
// not. A workspace whose profiles set no window keeps whatever it declares.
func validateAutoCompactWindowNames(cfg *WorkspaceConfig) error {
	windowed := false
	for _, p := range cfg.Session.DispatchProfiles {
		if p.AutoCompactWindow.IsSet() {
			windowed = true
			break
		}
	}
	if !windowed {
		return nil
	}
	refuse := func(table string, names []string) error {
		for _, n := range names {
			if strings.EqualFold(n, AutoCompactWindowEnv) {
				return fmt.Errorf("%s: %q: a dispatch profile sets auto_compact_window, so %s is set there and nowhere else; remove the entry", table, n, AutoCompactWindowEnv)
			}
		}
		return nil
	}
	check := func(prefix string, tables []claudeEnvTable) error {
		for _, t := range tables {
			if err := refuse(prefix+t.path, t.names); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check("claude.env", claudeEnvTables(cfg.Claude.Env)); err != nil {
		return err
	}
	if cfg.Instance.Claude != nil {
		if err := check("instance.claude.env", claudeEnvTables(cfg.Instance.Claude.Env)); err != nil {
			return err
		}
	}
	repos := make([]string, 0, len(cfg.Repos))
	for name := range cfg.Repos {
		repos = append(repos, name)
	}
	slices.Sort(repos)
	for _, name := range repos {
		if ov := cfg.Repos[name]; ov.Claude != nil {
			if err := check("repos."+name+".claude.env", claudeEnvTables(ov.Claude.Env)); err != nil {
				return err
			}
		}
	}
	return check("session.env", []claudeEnvTable{
		{".promote", cfg.Session.Env.Promote},
		{".vars", mapKeys(cfg.Session.Env.Vars.Values)},
		{".vars.required", mapKeys(cfg.Session.Env.Vars.Required)},
		{".vars.recommended", mapKeys(cfg.Session.Env.Vars.Recommended)},
		{".vars.optional", mapKeys(cfg.Session.Env.Vars.Optional)},
	})
}

// claudeEnvTable is one table of a Claude env declaration and the names it
// declares; path is relative to the declaration, starting with a dot.
type claudeEnvTable struct {
	path  string
	names []string
}

// claudeEnvTables lists every table of e that declares a name: promote, and
// vars and secrets with their requirement sub-tables.
func claudeEnvTables(e ClaudeEnvConfig) []claudeEnvTable {
	tables := []claudeEnvTable{{".promote", e.Promote}}
	for _, v := range []struct {
		path string
		t    EnvVarsTable
	}{{".vars", e.Vars}, {".secrets", e.Secrets}} {
		tables = append(tables,
			claudeEnvTable{v.path, mapKeys(v.t.Values)},
			claudeEnvTable{v.path + ".required", mapKeys(v.t.Required)},
			claudeEnvTable{v.path + ".recommended", mapKeys(v.t.Recommended)},
			claudeEnvTable{v.path + ".optional", mapKeys(v.t.Optional)},
		)
	}
	return tables
}

// mapKeys returns m's keys, sorted.
func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
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
