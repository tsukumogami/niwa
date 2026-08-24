package workspace

import (
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
)

// deliveries registers the materializer behind each named delivery in the
// capability contract. It is what turns a name in the declaration table into a
// concrete thing in this package: agentplan says which agent receives a
// capability and which delivery serves it, and this map says what that delivery
// actually is.
//
// The materializers here are the agent-agnostic ones -- dotenv files, arbitrary
// file distribution, hook installation. They deliver the same bytes to whoever
// asks and are bound to the contract without being restructured, because
// restructuring code that was never agent-shaped would be churn with no
// property behind it.
//
// The values are zero-value instances used as the identity of the delivery, not
// as runnable materializers: the pipeline builds its own with the call-site
// options each one takes. What they are good for is answering "is the thing
// registered under this name really this materializer", which is what the
// binding test asks and what keeps the registration from agreeing with itself.
// The two root materializers are registered ahead of the binding that names
// them, which agentplan.PendingDeliveries records. Registration is not what
// makes them run -- the pipeline drives both on every apply already -- it is
// what will make them traceable from the declaration table once the rows they
// serve are bound.
var deliveries = map[agentplan.Delivery]Materializer{
	agentplan.DeliveryEnv:          &EnvMaterializer{},
	agentplan.DeliveryFiles:        &FilesMaterializer{},
	agentplan.DeliveryHooks:        &HooksMaterializer{},
	agentplan.DeliveryRootSkills:   &RootSkillsMaterializer{},
	agentplan.DeliveryRootSettings: &RootSettingsMaterializer{},
}

// procedureInput is what the pipeline hands a procedure-routed delivery: what
// this apply prepared, and where the delivery may write.
//
// It is deliberately the same shape for every procedure, and every procedure
// ignores most of it. What distinguishes a procedure-routed delivery is that a
// plan entry cannot describe it honestly -- the trust entry lands outside every
// instance, and the niwa plugin tree has to reach disk before an entry can name
// it as a source -- and that is a statement about the write, not about what the
// writer needs to be told. So the fields grow as deliveries arrive and the
// procedures that do not want them do not read them; a shape per procedure
// would put the variety in the wrong place.
type procedureInput struct {
	// DeveloperHome is the developer's own home directory. Empty means the
	// caller has not been wired to supply one, and the pipeline skips every
	// procedure rather than resolving a home itself -- see Applier.DeveloperHome.
	DeveloperHome string

	// InstanceRoot is the instance this apply prepared. A delivery that lands
	// inside it -- niwa's own plugin tree, extracted where a session started at
	// the root can be linked to it -- reads it from here rather than resolving
	// a root of its own.
	InstanceRoot string

	// Producer is the agent's producer for this delivery, already gated. A
	// procedure that finishes through the plan machinery declares its entry
	// from this, so the layout stays the producer's answer even where the
	// delivery could not be plan-borne end to end.
	Producer agentplan.Producer

	// RepoRoots are the repository roots this apply materialized, in
	// classification order.
	RepoRoots []string

	// Recorded is what the instance state says this procedure wrote outside
	// the instance on earlier applies. It is the sole authority for what the
	// procedure may retract.
	Recorded []string

	// SkipPluginInstall carries the developer's own opt-out of niwa's plugin
	// install (--no-install-plugins, or the global setting). It reaches the
	// delivery as data for the same reason the home does: a procedure that
	// read the flag itself would be deciding a user's preference from inside
	// the write it applies to.
	SkipPluginInstall bool

	// Reporter is where a delivery says what it did. It is here rather than in
	// the result because what these procedures report is a notice -- the
	// installation status a developer expects to see once -- and a notice is
	// not a warning, which is all a procedureResult can carry. Nil is a no-op,
	// which is what every unit suite in this package hands it.
	Reporter *Reporter
}

// procedureResult is what one returns: the record to persist and what the user
// needs to hear.
type procedureResult struct {
	// Recorded replaces the prior record outright, empty included: a
	// retraction that cleared the last key must leave an empty record rather
	// than the stale one it just withdrew.
	Recorded []string

	// Warnings are reported alongside the pipeline's other deferred warnings.
	Warnings []string
}

// procedure is one procedure-routed delivery: a side effect outside the
// instance, which a plan entry cannot express honestly because the executor
// only writes inside the tree it is preparing.
//
// Name is the delivery name the contract binds it under, which is what lets the
// binding test ask whether the thing registered under a name really is that
// thing rather than watch the registry agree with itself.
type procedure interface {
	Name() string
	Deliver(procedureInput) (procedureResult, error)
}

// procedures registers the procedure behind each procedure-routed delivery the
// contract names, the way deliveries above does for the materializers. Directory
// trust is the first: Codex reads trust from layers it merges before any project
// layer exists, so the entry that makes a prepared instance usable goes into the
// developer's own configuration and could not be a plan entry.
//
// Not every procedure-routed capability is here. Marketplace registration and
// git-exclude bookkeeping are implemented and still unbound; their bindings land
// with the work that converts them, and until then their absence from
// agentplan.BoundCapabilities is what records that honestly.
//
// The two niwa-plugin procedures are the other way around: they are registered
// ahead of the binding that reaches them, so procedureFor answers false for
// both agents and nothing calls them yet. That staging is recorded in
// agentplan.PendingDeliveries rather than left for a reader to infer from a
// registration nothing points at.
var procedures = map[agentplan.Delivery]procedure{
	agentplan.DeliveryCodexTrust:       codexTrustProcedure{},
	agentplan.DeliveryNiwaPluginClaude: claudeNiwaPluginProcedure{},
	agentplan.DeliveryNiwaPluginCodex:  codexNiwaPluginProcedure{},
}

// procedureFor returns the procedure that delivers c to ag, and false when the
// contract does not declare the pair implemented or binds it to nothing this
// package registers.
//
// This is the whole of how a procedure-routed delivery is reached. The caller
// ranges over agent.All() and asks the table, so which agent gets directory
// trust is a row in the declaration table rather than a branch in the pipeline
// -- which is the difference between a delivery under the contract and a second
// hardcoded pass beside it.
func procedureFor(c agentplan.Capability, ag agent.Agent) (procedure, bool) {
	// The zero agent is the default one rather than an unknown one, so it is
	// resolved before the bindings are scanned: the table answers for it, and
	// the binding rows are written in canonical values.
	resolved, err := agent.ParseAgent(string(ag))
	if err != nil {
		return nil, false
	}
	d, err := agentplan.Lookup(c, resolved)
	if err != nil || d.State != agentplan.StateImplemented {
		return nil, false
	}
	for _, b := range agentplan.Bindings() {
		if b.Capability != c || b.Agent != resolved {
			continue
		}
		p, ok := procedures[b.Delivery]
		return p, ok
	}
	return nil, false
}
