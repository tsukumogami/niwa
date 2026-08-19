package agentplan

import (
	"slices"

	"github.com/tsukumogami/niwa/internal/agent"
)

// This file is the launch-routed half of the contract: what niwa needs to know
// about an agent in order to start a background worker for it, find out which
// session that worker became, ask later whether that session still exists, and
// step back into it.
//
// It is data, in the shape payloadLayouts and compositions already use -- a
// per-agent table read through a method on Producer -- and for the same reason.
// internal/cli does the launching, the polling, and the exec; this package says
// what to launch, where to look, and what to run to get back in. The leaf scan
// in layout_scan_test.go keeps that division honest from this side, and the
// dispatch-path scan in internal/cli keeps it honest from the other: with the
// per-agent facts here, no file on the dispatch path has any reason to name an
// agent at all.
//
// The binding for these rows is in dispatch_test.go rather than in binding.go.
// The Delivery-name mechanism cannot reach a launch: it matches a name against
// a Materializer registered in internal/workspace, and there is nothing in
// internal/workspace that launches anything. What replaces it is the same check
// in both drift directions, run against the table below instead of against a
// registry in another package -- an implemented declaration with no spec behind
// it fails, and a spec for an agent the table does not declare implemented
// fails.

// HandleKind says what the user-facing handle for a session is: the thing a
// developer types after the binary's own management verbs, and the thing
// niwa's resume passes.
type HandleKind uint8

const (
	// HandleRecordDir means the handle is the name of the directory the
	// session's record sits in. Claude Code's management verbs reject the
	// full UUID and take this short form.
	HandleRecordDir HandleKind = iota + 1

	// HandleSessionID means the session id is itself the handle.
	HandleSessionID
)

// LivenessKind says whether the presence of a session's record proves the
// session still exists.
type LivenessKind uint8

const (
	// LivenessRecordPresence means the record is removed when the developer
	// deletes the session, so its presence is a faithful proxy for "the
	// session is still there, running or resumable".
	LivenessRecordPresence LivenessKind = iota + 1

	// LivenessNone means the agent records no signal niwa can read to tell a
	// live session from a deleted one. A caller that cannot prove a session is
	// gone must not act as though it is.
	LivenessNone
)

// SessionRecords describes where an agent writes the record that says which
// session a launched worker became, and how to read one. It is a description
// rather than a reader: this package names the paths and the fields, and
// internal/cli walks and decodes.
//
// The two agents' records differ in three ways and agree in every way that
// matters to correlation. Claude Code writes one directory per job holding a
// pretty-printed JSON object; Codex writes one JSONL file per session, nested
// under a date, whose first line carries the same two fields under a payload
// key. Both record the working directory the session was started in, which is
// what lets a launched worker be matched to the unique instance directory niwa
// gave it.
type SessionRecords struct {
	// HomeEnv is the environment variable that relocates the agent's own
	// state directory, empty when it has none.
	HomeEnv string

	// HomePath is the record root under the developer's home directory, as
	// path components, used when HomeEnv is unset or empty.
	HomePath []string

	// Depth is how many directory levels below the record root the records
	// sit. Claude Code's jobs directory is one level; Codex nests its
	// rollouts under year, month, and day.
	Depth int

	// FileName is the record's name within its containing directory, empty
	// when the candidate is the file itself rather than a directory holding
	// one.
	FileName string

	// FileGlob matches the record files directly, empty when FileName names
	// them instead. Exactly one of FileName and FileGlob is set.
	FileGlob string

	// FirstLineOnly says the record is one JSON object on the file's first
	// line rather than the whole file. A Codex rollout is a JSONL transcript
	// whose first line is its session metadata; reading the whole file would
	// read the entire conversation to learn two fields.
	FirstLineOnly bool

	// CwdPath is the dotted path to the working-directory field.
	CwdPath []string

	// IDPath is the dotted path to the session-id field.
	IDPath []string

	// Handle says which of the two available strings is the user-facing
	// handle for a matched session.
	Handle HandleKind

	// Liveness says whether the record's presence proves the session exists.
	Liveness LivenessKind
}

// modelCategories is niwa's portable, vendor-neutral vocabulary for model
// capability: the words a caller reaches for when they care about what a model
// can do rather than who makes it. It belongs to niwa rather than to any agent,
// which is why it is declared once here and each agent's spec binds every one of
// these names to a concrete model of its own. A spec that answered for two of
// them would leave a portable request unanswerable, which the spec suite fails
// on.
var modelCategories = []string{"balanced", "fast", "powerful"}

// ModelCategories returns the portable categories, sorted, as a fresh slice.
func ModelCategories() []string { return slices.Clone(modelCategories) }

// LaunchFlags is the spelling each niwa-side intent takes on an agent's command
// line. An empty value means the agent has no such flag and the intent is
// dropped rather than guessed at -- forwarding a flag an agent does not accept
// would fail the launch, and inventing a near-equivalent would hand a developer
// something they did not ask for.
type LaunchFlags struct {
	// Model selects the worker's main-loop model.
	Model string

	// PermissionMode selects the worker's permission posture.
	PermissionMode string

	// SubagentType names a subagent type the worker runs as. This is the
	// intent behind `niwa dispatch --agent`, which is a passthrough of the
	// agent's own flag and not a choice of which agent to launch.
	SubagentType string

	// DisplayName gives the session a human-readable name.
	DisplayName string

	// Settings hands the worker a settings document inline.
	Settings string
}

// LaunchSpec is everything the dispatch path needs to know about one agent.
// A spec exists exactly for the agents the table declares DispatchLaunch
// implemented for; see dispatch_test.go for the check in both directions.
//
// One thing it deliberately does not carry yet is the process model. Every
// agent niwa launches today puts its own worker in the background and exits, so
// there is one process model and no field is choosing between them. Declaring
// the choice before a second answer exists would put a constant here that
// nothing branches on, which is the shape this contract exists to catch.
type LaunchSpec struct {
	// Binary is the executable name looked up on PATH.
	Binary string

	// LeadingArgs are the arguments that always precede the pass-through
	// flags: a subcommand, and whatever policy niwa fixes for every dispatch.
	LeadingArgs []string

	// PromptSeparator says a bare "--" must precede the prompt, so a prompt
	// that begins with a dash is read as the prompt rather than as a flag.
	PromptSeparator bool

	// Flags is the spelling of each pass-through intent.
	Flags LaunchFlags

	// ModelCategories maps niwa's portable capability categories to the
	// concrete versionless model name this agent is given. The values are
	// deliberately versionless so niwa stays out of the version-pinning
	// business: the agent resolves the alias itself.
	ModelCategories map[string]string

	// KnownModels is the set of versionless vendor names niwa recognizes and
	// forwards unchanged. Membership only suppresses the "unrecognized"
	// warning; an unknown value is still forwarded, so a brand-new alias
	// keeps working before niwa learns about it.
	KnownModels []string

	// Records describes where the session identity is written.
	Records SessionRecords

	// ResumeArgs are the arguments that precede the handle when stepping back
	// into a session interactively.
	ResumeArgs []string

	// HintVerbs are the agent's own management verbs, printed after a
	// successful dispatch as "<binary> <verb> <handle>" so a developer can
	// reach the session without niwa in the way. They are the agent's
	// vocabulary, not niwa's.
	HintVerbs []string
}

// launchSpecs is the per-agent table. It holds a row for each agent the
// declaration table says DispatchLaunch is implemented for, and no others.
var launchSpecs = map[agent.Agent]LaunchSpec{
	agent.AgentClaude: {
		Binary:      "claude",
		LeadingArgs: []string{"--bg"},
		Flags: LaunchFlags{
			Model:          "--model",
			PermissionMode: "--permission-mode",
			SubagentType:   "--agent",
			DisplayName:    "--name",
			Settings:       "--settings",
		},
		ModelCategories: map[string]string{
			"fast":     "haiku",
			"balanced": "sonnet",
			"powerful": "opus",
		},
		KnownModels: []string{"fable", "opus", "sonnet", "haiku"},
		Records: SessionRecords{
			HomePath: []string{".claude", "jobs"},
			Depth:    1,
			FileName: "state.json",
			CwdPath:  []string{"cwd"},
			IDPath:   []string{"sessionId"},
			// The management verbs reject the full UUID with "No job
			// matching ...", so the handle is the job directory's own name.
			// It is the actual directory name and never a slice of the UUID,
			// which keeps it correct if the two ever stop coinciding.
			Handle: HandleRecordDir,
			// The job entry is present while the session exists -- running or
			// idle-but-resumable -- and gone once the developer deletes it.
			Liveness: LivenessRecordPresence,
		},
		ResumeArgs: []string{"attach"},
		HintVerbs:  []string{"attach", "logs", "stop"},
	},
}

// LaunchSpec returns this producer's agent's launch description, and false for
// an agent niwa does not launch a background worker for. The caller's next move
// on false is to consult the declaration for the reason, not to invent one.
func (p Producer) LaunchSpec() (LaunchSpec, bool) {
	resolved, err := agent.ParseAgent(string(p.ag))
	if err != nil {
		return LaunchSpec{}, false
	}
	spec, ok := launchSpecs[resolved]
	return spec, ok
}

// ModelCategoryNames returns the portable categories in sorted order. It exists
// so a caller can render the accepted vocabulary in help text without ranging
// over the map itself and getting a different order each run.
func (s LaunchSpec) ModelCategoryNames() []string {
	out := make([]string, 0, len(s.ModelCategories))
	for name := range s.ModelCategories {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// KnownModelNames returns the recognized versionless names in sorted order, for
// the same reason.
func (s LaunchSpec) KnownModelNames() []string {
	out := slices.Clone(s.KnownModels)
	slices.Sort(out)
	return out
}
