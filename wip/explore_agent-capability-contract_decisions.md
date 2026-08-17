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

## Round 2

- **Two states, not three.** Trust is a capability niwa delivers
  (`codex_trust.go` edits the developer's config atomically and additively),
  not an external precondition. Conditional cases become Implemented with a
  `Requires: [CapabilityDirectoryTrust]` edge, enforced by a closure test.
  Rationale: a "conditional" state is a soft word a real gap hides inside, and
  it would force the guide's gap-list generator to make a judgment instead of
  applying a filter -- the exact failure the brief names in the prior attempt.

- **A leaf package producing plans, plus one agent-blind executor.**
  `internal/agentplan` (sibling to `internal/agent`) holds the capability set,
  the support matrix, the plan types, and the per-agent producers.
  `internal/workspace` gains a generic executor over a closed four-op set.
  Boundary stated as "reads inputs, declares outputs", checked by an AST
  assertion that the leaf never writes to disk. Rejected: per-agent files
  inside `internal/workspace`, which has no precedent in this repo and makes
  every assertion cost a tmpdir.

- **PR 1 ships zero config renames.** A compatibility alias is
  behavior-preserving but not diff-free. `Content`/`content_dir` and the
  `claude.enabled` gate restructure both move to PR 2, where a second agent
  first gives them something to mis-gate. The other six `ClaudeConfig` fields
  stay Claude-named -- renaming them now is speculative.

- **Characterization test before refactor, not after.** Commit a
  `ManagedFiles`-based path+hash assertion on `main` first, so it characterizes
  current behavior rather than being written to match the new code. Point it at
  the eight context writers, where a mechanical conversion is most likely to
  drop an accumulated boundary rule.

- **Close the payload-config permission and exclude defects in the same change
  that first writes secrets there.** 0o644 -> secretFileMode, and git-exclude
  coverage for non-`.local` names at the instance root. Not deferred: the leak
  only exists once environment delivery lands, so the fix belongs with it.

- **Measure rather than infer the three unresolved matrix rows.** The installed
  binary is the exact version the spike measured, so approval/sandbox
  settability, the worktree `.git`-file marker question, and
  `shell_environment_policy.set` trust-gating are all measurable. Two prior
  attempts got Codex behavior wrong by reasoning from the outside; an admitted
  gap beats a confident inference.
