# Exploration Decisions: agent-capability-contract

## Round 1

- **Explore further rather than crystallize.** Round 1 mapped the terrain but
  produced four tensions (capability state model, package boundary vs.
  dependency graph, PR 1 scope, rename ownership) that are measurable rather
  than decidable. Crystallizing now would push all four into the design phase
  as guesses.

- **The contract must govern materially more than context files.** The
  inventory found the discriminator reaches 2 of ~20 capabilities. A contract
  that ends up governing only context files reproduces the prior attempt's
  failure at smaller scale, whatever its interface looks like. Rationale: the
  brief's rule is that wherever dual-agent capability lands, the refactor lands
  with it -- and Codex has a defensible answer (implemented, conditional, or
  declared unavailable) for most of the inventory.

- **Rule out re-deriving Codex discovery mechanics.** The spike is measured
  against codex-cli 0.147.0 and the brief forbids re-derivation. Round 2 leads
  extract and apply it; none re-tests it.

- **Rule out `golang.org/x/tools` for the property tests.** `go/ast`, `go/parser`,
  and `go/token` are stdlib and sufficient for a syntactic call-site scan; x/tools
  would be a new dependency needing `go mod tidy` review, against a repo
  convention of standard toolchain only.

- **Adopt `internal/vault` as the structural precedent to evaluate first.**
  Mandatory interface + optional interface probed by type assertion +
  fail-closed registry + implementations in self-registering sub-packages is
  the closest existing match to a capability contract with declared-unavailable
  states.

- **Treat the skills/Claude-Code dependency as a fix, not a declared
  limitation.** niwa already owns the tarball-fetch primitives, so the
  Claude-Code-independent route is cheap. The brief allows either; the evidence
  favors closing it.
