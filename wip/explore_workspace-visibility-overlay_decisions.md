# Exploration Decisions: workspace-visibility-overlay

## Round 1

- **Ruled out GitHub variables**: User explicitly noted GitHub variables for config placement is not recommended. Eliminated from consideration.
- **All-or-nothing private access accepted**: Selective per-repo access within the private extension (some but not all private repos visible to partial-access users) is out of scope for v1. The acceptable tradeoff: access to the companion repo is binary.
- **Secrets out of scope**: PR #52 (vault integration) covers vault. This design only addresses structural privacy — repo names, configurations, group names, org identifiers.
- **Auto-discovery warning leaks are a known tradeoff in v1**: Teams sharing a GitHub org between public and private repos must use explicit repo lists in their public config. This constrains the zero-config auto-discovery value proposition but is unavoidable without a more complex org-level filter mechanism.
- **Content override for existing public repos deferred**: Adding private annotations to an existing public repo's CLAUDE.local.md is a v2+ concern. The CLAUDE.private.md injection pattern is the natural extension path.
- **PRD chosen as artifact type**: Requirements are partially known but user stories need articulation. Multiple viable implementation paths exist that depend on requirements choices. The "what to build" is the open question.
