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

	// DeliveryCodexTrust is the Codex trust writer, which reconciles niwa's
	// per-repository trust entries in the developer's own Codex
	// configuration. It is procedure-routed: the write lands outside every
	// instance, so no plan entry could describe it honestly.
	DeliveryCodexTrust Delivery = "codex-trust"

	// DeliveryRootSkills is the materializer that delivers the workspace's
	// plugin trees into the instance root's skills directory, for an agent
	// whose skills arrive as delivered trees.
	DeliveryRootSkills Delivery = "root-skills"

	// DeliveryRootSettings is the materializer that writes the instance
	// root's settings document, for an agent whose root-installed skills
	// arrive by registration rather than as delivered trees.
	DeliveryRootSettings Delivery = "root-settings"

	// DeliveryNiwaPluginClaude and DeliveryNiwaPluginCodex are niwa's own
	// plugin, per agent.
	//
	// One capability, two delivery names, because the two agents' deliveries
	// are not the same act. One materializes the embedded tree into the
	// developer's own home, outside every instance and outliving all of them;
	// the other materializes it inside one instance and is reclaimed with it.
	// A single name over both would assert an equivalence that does not hold,
	// and the binding test's whole job is to make such an assertion checkable.
	DeliveryNiwaPluginClaude Delivery = "niwa-plugin-claude"
	DeliveryNiwaPluginCodex  Delivery = "niwa-plugin-codex"
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
// without restructuring, the directory-trust writer, and the two instance-root
// rows -- root-installed skills and niwa's own plugin -- whose deliveries are
// per-agent. For the first three, delivery is unchanged and what is new is that
// it is now answerable to the declaration table.
//
// The list is explicit rather than derived from bindings below, so that
// deleting a binding is a test failure instead of a quiet narrowing of what
// gets checked. It grows as later work converts plan-borne capabilities to
// producers and binds the remaining procedure- and launch-routed ones; a
// capability absent from it is unbound, which is a fact this file records
// rather than one it hides.
var boundCapabilities = []Capability{
	DotenvFiles,
	FileDistribution,
	Hooks,
	RootProjectSkills,
	NiwaPlugin,
	DirectoryTrust,
}

// bindings is one row per implemented (capability, agent) pair among the bound
// capabilities. Each binds in exactly the column the table declares it in:
// directory trust is Codex-only and no such concept for Claude, hooks are
// Claude-only, and the two instance-root rows are declared for both agents and
// bind to a different delivery in each. An entry here for a pair the table does
// not declare implemented is a delivery nobody declared, and a bound capability
// implemented with no entry here is a declaration nobody delivers. The
// structural suite checks both directions.
var bindings = []Binding{
	// Both agents bind to the same delivery for the two agent-agnostic
	// materializers: one writer, one set of bytes, two sessions that read them.
	// The pair of rows is not redundant -- it is what makes the binding test
	// able to tell "delivered to both" from "delivered to Claude alone", which
	// is a difference the gap list reports to developers.
	{Capability: DotenvFiles, Agent: agent.AgentClaude, Delivery: DeliveryEnv},
	{Capability: DotenvFiles, Agent: agent.AgentCodex, Delivery: DeliveryEnv},
	{Capability: FileDistribution, Agent: agent.AgentClaude, Delivery: DeliveryFiles},
	{Capability: FileDistribution, Agent: agent.AgentCodex, Delivery: DeliveryFiles},
	{Capability: Hooks, Agent: agent.AgentClaude, Delivery: DeliveryHooks},

	// Row 18 is one capability delivered two ways, because the two agents take
	// root-installed skills by different mechanisms. Codex reads them out of
	// trees delivered into the instance root's skills directory; Claude takes
	// them by registration, so what serves its column is the root settings
	// document that declares the plugins and marketplaces its own startup
	// reconciler then materializes.
	{Capability: RootProjectSkills, Agent: agent.AgentCodex, Delivery: DeliveryRootSkills},
	{Capability: RootProjectSkills, Agent: agent.AgentClaude, Delivery: DeliveryRootSettings},

	// Row 19 is the same shape for a different reason: not two mechanisms but
	// two lifetimes. Codex's delivery extracts niwa's embedded tree inside the
	// instance and links it into the instance root's skills directory, where
	// the session reads it and the instance's own teardown reclaims it.
	//
	// Claude's delivery materializes that same tree at the user-level install
	// path, in Claude Code's plugin format. The claim stops there: it does not
	// say a Claude session resolves it. On the machine that prepared this work
	// the marketplace was absent from Claude Code's own registry and no `niwa:*`
	// skill resolved -- one observation on one machine, recorded so the binding
	// is not read as promising more than the write it names. Repairing the
	// registration is separate work.
	{Capability: NiwaPlugin, Agent: agent.AgentClaude, Delivery: DeliveryNiwaPluginClaude},
	{Capability: NiwaPlugin, Agent: agent.AgentCodex, Delivery: DeliveryNiwaPluginCodex},

	{Capability: DirectoryTrust, Agent: agent.AgentCodex, Delivery: DeliveryCodexTrust},
}

// BoundCapabilities returns the capabilities a named delivery answers for, in
// matrix order. The result is a fresh slice.
func BoundCapabilities() []Capability { return slices.Clone(boundCapabilities) }

// Bindings returns every (capability, agent) pair bound to a named delivery.
// The result is a fresh slice, so a caller cannot shrink the set the binding
// checks range over.
func Bindings() []Binding { return slices.Clone(bindings) }
