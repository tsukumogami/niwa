package workspace

import "github.com/tsukumogami/niwa/internal/agentplan"

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
