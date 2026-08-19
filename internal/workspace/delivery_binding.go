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
var deliveries = map[agentplan.Delivery]Materializer{
	agentplan.DeliveryEnv:   &EnvMaterializer{},
	agentplan.DeliveryFiles: &FilesMaterializer{},
	agentplan.DeliveryHooks: &HooksMaterializer{},
}

// procedureInput is what the pipeline hands a procedure-routed delivery: the
// developer home it may write under, the repository roots this apply
// materialized, and the record of what earlier applies wrote there.
//
// It is deliberately the same shape for every procedure. A procedure's whole
// distinguishing feature is the side effect it performs outside the instance;
// what it is told about the apply is not where the variety belongs.
type procedureInput struct {
	// DeveloperHome is the developer's own home directory. Empty means the
	// caller has not been wired to supply one, and the pipeline skips every
	// procedure rather than resolving a home itself -- see Applier.DeveloperHome.
	DeveloperHome string

	// RepoRoots are the repository roots this apply materialized, in
	// classification order.
	RepoRoots []string

	// Recorded is what the instance state says this procedure wrote outside
	// the instance on earlier applies. It is the sole authority for what the
	// procedure may retract.
	Recorded []string
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
var procedures = map[agentplan.Delivery]procedure{
	agentplan.DeliveryCodexTrust: codexTrustProcedure{},
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
