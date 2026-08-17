package agentplan

import (
	"slices"

	"github.com/tsukumogami/niwa/internal/agent"
)

// Delivery names one concrete thing in internal/workspace that delivers a
// capability. The value is the materializer's own Name(), so the workspace-side
// registry can assert that the thing it registered under a name really is the
// thing the name refers to, rather than agreeing with itself.
//
// A named delivery is the binding mechanism for capabilities whose delivery is
// not plan-borne. A plan-borne capability binds by tagging its entries, which
// needs no name; everything else -- an existing materializer kept as it is, a
// procedure with a side effect outside the instance, a gate on the launch path
// -- has no entry to tag, and without a name it would have nothing tying it to
// its declaration either.
type Delivery string

const (
	// DeliveryEnv is EnvMaterializer, which writes the dotenv files.
	DeliveryEnv Delivery = "env"

	// DeliveryFiles is FilesMaterializer, which distributes arbitrary
	// source-to-destination files.
	DeliveryFiles Delivery = "files"

	// DeliveryHooks is HooksMaterializer, which installs hook scripts.
	DeliveryHooks Delivery = "hooks"
)

// Binding says which delivery satisfies one implemented (capability, agent)
// pair. It lives here rather than in internal/workspace for the same reason the
// declaration table does: the pairing is per-agent, and internal/workspace is
// the package that must not name an agent.
type Binding struct {
	Capability Capability
	Agent      agent.Agent
	Delivery   Delivery
}

// boundCapabilities lists the capabilities a named delivery answers for today:
// the agent-agnostic materializers that this contract declares and binds
// without restructuring. Their delivery is unchanged -- what is new is that it
// is now answerable to the declaration table.
//
// The list is explicit rather than derived from bindings below, so that
// deleting a binding is a test failure instead of a quiet narrowing of what
// gets checked. It grows as later work converts plan-borne capabilities to
// producers and binds the procedure- and launch-routed ones; a capability
// absent from it is unbound, which is a fact this file records rather than one
// it hides.
var boundCapabilities = []Capability{
	DotenvFiles,
	FileDistribution,
	Hooks,
}

// bindings is one row per implemented (capability, agent) pair among the bound
// capabilities. Every Codex row is absent because every Codex declaration is
// unavailable; an entry here for a pair the table does not declare implemented
// is a delivery nobody declared, and a bound capability implemented with no
// entry here is a declaration nobody delivers. The structural suite checks both
// directions.
var bindings = []Binding{
	{Capability: DotenvFiles, Agent: agent.AgentClaude, Delivery: DeliveryEnv},
	{Capability: FileDistribution, Agent: agent.AgentClaude, Delivery: DeliveryFiles},
	{Capability: Hooks, Agent: agent.AgentClaude, Delivery: DeliveryHooks},
}

// BoundCapabilities returns the capabilities a named delivery answers for, in
// matrix order. The result is a fresh slice.
func BoundCapabilities() []Capability { return slices.Clone(boundCapabilities) }

// Bindings returns every (capability, agent) pair bound to a named delivery.
// The result is a fresh slice, so a caller cannot shrink the set the binding
// checks range over.
func Bindings() []Binding { return slices.Clone(bindings) }
