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

// RunnerKind says what an agent's worker binary is: whether it puts the worker
// in the background itself, or runs the whole turn in the foreground of the
// process niwa starts. That is a property of the agent, so it is declared here.
//
// It is deliberately not the process model. A runner that executes the turn in
// the foreground offers two -- run it where the developer can watch, or detach
// it -- and which one an invocation gets is decided with --detach in hand. A
// runner that backgrounds its own session offers one, and no flag overrides it
// into the other, because there is nothing to run in the foreground.
type RunnerKind uint8

const (
	// RunnerSelfBackgrounding means the binary puts the worker in the
	// background itself and exits, so the process niwa starts is the hand-off
	// rather than the work.
	RunnerSelfBackgrounding RunnerKind = iota + 1

	// RunnerForeground means the binary runs the whole turn in the foreground
	// of the process niwa starts.
	RunnerForeground
)

// LaunchMode says how the dispatch path runs an agent's worker binary for one
// invocation. It is resolved from both of its inputs -- what the agent's runner
// is, and whether this invocation asked to detach -- and never from the
// declaration alone. See ModeFor.
type LaunchMode uint8

const (
	// LaunchBackgrounded means the binary backgrounds the worker itself. The
	// launcher runs it and waits, because the process it started is not the
	// worker -- waiting costs the time it takes to hand off, and its exit
	// status says whether the hand-off happened.
	LaunchBackgrounded LaunchMode = iota + 1

	// LaunchForeground means the worker's whole turn runs in the caller's
	// terminal. The launcher waits for it and the developer watches it, which
	// for a foreground runner is what attaching to the session would have been.
	LaunchForeground

	// LaunchDetached means the worker's turn is put out of the terminal's
	// reach. The launcher starts it in a session of its own, hands it a closed
	// stdin and files for its output, and releases it without waiting --
	// waiting would block the dispatch for the length of the task, and a
	// context cancelled when the dispatch returns would kill the worker
	// outright.
	LaunchDetached
)

// ModeFor resolves the process model for one invocation from both of its
// inputs: what this runner is, and whether the developer asked to detach.
//
// The flag is an input here rather than a step that runs afterwards. Deciding
// the model from the runner alone leaves --detach unable to change how a worker
// is started: a foreground runner would be detached whether or not anybody
// asked, and the flag would only choose whether a step followed.
func (r RunnerKind) ModeFor(detach bool) LaunchMode {
	if r == RunnerForeground {
		if detach {
			return LaunchDetached
		}
		return LaunchForeground
	}
	// A runner that backgrounds its own session has one process model, and
	// --detach cannot override it into another. What the flag still decides
	// for it is whether the attach step runs afterwards.
	return LaunchBackgrounded
}

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

	// LivenessRecordActivity means the record is never removed, so its
	// presence proves nothing, but it is appended to whenever the session is
	// worked in and the agent holds an advisory lock for as long as a writer
	// is attached to it. Together those answer a question presence cannot: a
	// record nobody has appended to for long enough, with no writer holding
	// its lock, belongs to a session nobody came back to.
	//
	// This kind asks for a grace period rather than proving a session is
	// gone, and the difference is deliberate. An agent with no delete verb
	// never produces the event LivenessRecordPresence waits for, so the
	// choice for one of these is between a bounded wait and an instance that
	// is never reclaimed at all.
	LivenessRecordActivity
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

	// WriterLockPath is the directory holding one advisory lock file per
	// session, as path components below the agent's own directory under home
	// -- the component HomeEnv overrides and HomePath's first element names,
	// so the lock directory is a sibling of the record store rather than a
	// child of it. Empty for an agent that takes no such lock, and read only
	// when Liveness is LivenessRecordActivity.
	//
	// The lock file within it is the session's own id followed by
	// WriterLockSuffix. It exists only while a writer is attached: the agent
	// unlinks it on a clean exit, and the kernel drops the lock itself when a
	// holder dies without unlinking, so a file left behind by a killed worker
	// reads as unheld rather than as a live session forever.
	WriterLockPath []string

	// WriterLockSuffix is appended to the session id to name the lock file.
	WriterLockSuffix string
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
type LaunchSpec struct {
	// Binary is the executable name looked up on PATH.
	Binary string

	// Runner says what the binary is: whether it backgrounds its own worker,
	// or runs the turn in the foreground. Which process model an invocation
	// gets is ModeFor's answer, not this field's.
	Runner RunnerKind

	// LeadingArgs are the arguments that always precede the pass-through
	// flags: a subcommand, and whatever policy niwa fixes for every dispatch.
	LeadingArgs []string

	// DetachedArgs are the arguments added only when the worker is detached:
	// the ones that exist because niwa, rather than a developer, is the reader
	// of the output. In the foreground the developer is the reader, and handing
	// them a machine-readable event stream instead of the human output would be
	// a regression justified as consistency. Empty for an agent with no such
	// argument.
	DetachedArgs []string

	// PromptSeparator says a bare "--" must precede the prompt, so a prompt
	// that begins with a dash is read as the prompt rather than as a flag.
	PromptSeparator bool

	// Flags is the spelling of each pass-through intent.
	Flags LaunchFlags

	// WorkdirGrantArgs are the arguments that grant this one invocation the
	// write posture niwa intends for a directory it owns, with the absolute
	// working directory substituted for the single %q verb in the last
	// element. Empty for an agent that needs no such grant.
	//
	// The grant is per invocation and deliberately so. An agent that decides a
	// session's posture from a persistent registry offers niwa two ways to
	// elevate one: write the registry, or override it for the process being
	// started. Writing it vouches for anything anybody ever runs in that
	// directory afterwards, including sessions niwa did not launch and has no
	// opinion about; overriding it vouches for exactly the worker niwa is
	// starting, and the grant is gone when that worker exits. The narrower one
	// is the right one, and it also happens to leave the developer's own
	// configuration untouched.
	WorkdirGrantArgs []string

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

	// ResumeDuringTurn reports whether the agent will hand over a session
	// whose launched turn is still running.
	//
	// It exists because dispatch's last step, without --detach, is to resume
	// the session it just started -- at which point the worker is by
	// definition still working. An agent that backgrounds its own session
	// expects exactly that and hands it over. An agent that holds an exclusive
	// writer on the session for the length of the turn refuses, and the
	// refusal is not niwa's to route around: waiting for the turn would make
	// dispatch block for as long as the task, and forcing it would corrupt the
	// record both sides are writing. So niwa declines to try, says the worker
	// is still running, and leaves the developer the resume command.
	//
	// False is the conservative default: an agent whose behavior here has not
	// been measured gets the honest message rather than an error from its own
	// session store.
	ResumeDuringTurn bool

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
		Runner:      RunnerSelfBackgrounding,
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
		// A backgrounded session is meant to be attached to while it works;
		// that is what backgrounding it is for.
		ResumeDuringTurn: true,
		HintVerbs:        []string{"attach", "logs", "stop"},
	},

	agent.AgentCodex: {
		Binary: "codex",
		// The exec subcommand runs the whole turn in the foreground of the
		// process it is started as, so this agent offers both process models
		// and --detach picks one.
		Runner: RunnerForeground,
		// --skip-git-repo-check is not optional, because a dispatch instance
		// root is not a git repository and the run refuses to start in one
		// without it. Trusting the directory does not satisfy that check --
		// measured, and the identical startup failure reproduces with the path
		// trusted.
		//
		// --ephemeral is deliberately absent and must stay absent: it runs
		// without persisting the session record, which is the substrate
		// capture reads.
		LeadingArgs: []string{"exec", "--skip-git-repo-check"},
		// --json makes the run's own event stream parseable, which is what the
		// instance log is for. It rides the detached launch only: there, the
		// output goes to a file nobody is watching, and niwa is the one that
		// has to be able to read it back.
		DetachedArgs:    []string{"--json"},
		PromptSeparator: true,
		// The grant is a whole-table override rather than a dotted path. The
		// dotted form parses without error and silently does nothing, which is
		// worth knowing generally: a clean exit from one of these overrides is
		// not evidence it took effect.
		WorkdirGrantArgs: []string{"-c", `projects={%q={trust_level="trusted"}}`},
		Flags: LaunchFlags{
			Model: "--model",
			// The sandbox is this agent's permission posture. niwa passes
			// nothing here of its own -- the grant above decides the default
			// -- so this spelling exists for a developer who asks for a
			// posture deliberately.
			PermissionMode: "--sandbox",
			// No subagent types, no session display name, and no inline
			// settings document. Each is a niwa-side intent this agent has no
			// flag for, and an intent with no flag is dropped rather than
			// guessed at.
		},
		ModelCategories: map[string]string{
			"fast":     "gpt-5-codex-mini",
			"balanced": "gpt-5-codex",
			"powerful": "gpt-5",
		},
		KnownModels: []string{"gpt-5", "gpt-5-codex", "gpt-5-codex-mini"},
		Records: SessionRecords{
			HomeEnv:  "CODEX_HOME",
			HomePath: []string{".codex", "sessions"},
			// The records are nested under the year, month, and day the
			// session started -- in the host's local time rather than UTC.
			Depth: 3,
			// One file per session, and the record is that file's first line:
			// the rest of it is the conversation.
			FileGlob:      "rollout-*.jsonl",
			FirstLineOnly: true,
			CwdPath:       []string{"payload", "cwd"},
			IDPath:        []string{"payload", "session_id"},
			// The session id is the handle: this agent's resume verb takes it
			// directly and there is no shorter form.
			Handle: HandleSessionID,
			// A record is never removed, so its presence says a session once
			// existed and nothing about whether it still does. What the record
			// does carry is when it was last appended to, and the agent holds
			// an advisory lock beside it for as long as a writer is attached,
			// so the pair answers what presence alone cannot.
			//
			// Measured against 0.147.0. The lock is a real flock taken
			// LOCK_EX|LOCK_NB at session bootstrap and held unbroken for the
			// whole turn -- 157.8s and 67.3s across two runs polled twice a
			// second, with no free sample in between -- and released within
			// about 0.3s of the worker exiting. A clean exit unlinks the file;
			// a killed worker leaves it behind, but the kernel drops the lock,
			// so it reads as unheld. The file is a fresh inode every time,
			// which is why nothing here may be cached across probes.
			//
			// The rollout's own mtime is the other half: it stops moving for
			// good when a session ends (five rollouts re-stat'd seven minutes
			// later each matched their own last line's timestamp to the
			// millisecond, so nothing rewrites them afterwards) and it moves
			// again on a resume, which appends to the same file. That is what
			// makes staleness mean "nobody came back" rather than "the turn
			// ended".
			Liveness: LivenessRecordActivity,

			WriterLockPath:   []string{"thread-writer-locks"},
			WriterLockSuffix: ".lock",
		},
		ResumeArgs: []string{"resume"},
		// Measured against 0.147.0, both verbs: resuming a session whose turn
		// is still running exits 1 with "thread-store conflict: thread <id>
		// already has an active writer", the interactive form failing the same
		// way during TUI bootstrap under a real pty. A per-thread writer lock
		// is what produces it, so the refusal is deterministic rather than a
		// race, and it clears when the turn ends.
		ResumeDuringTurn: false,
		HintVerbs:        []string{"resume"},
	},
}

// LaunchableAgents returns the agents niwa can launch a background worker for,
// in the accepted set's order.
//
// It exists so a refusal can tell a developer what to do instead without naming
// an agent this code has decided on. The answer is read from the declarations,
// so the day a row flips the message changes with it -- the alternative,
// spelling one agent's name into an error string, is a sentence that goes stale
// silently and at the worst possible moment, since the person reading it is by
// definition the person who does not already know the answer.
func LaunchableAgents() []agent.Agent {
	var out []agent.Agent
	for _, ag := range agent.All() {
		d, err := Lookup(DispatchLaunch, ag)
		if err == nil && d.State == StateImplemented {
			out = append(out, ag)
		}
	}
	return out
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
