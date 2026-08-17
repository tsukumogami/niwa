# Consolidated decisions: agent-capability-contract

All seven decisions resolved inline per the sub-agent dispatch contract
(decision-bypass-with-inline-resolution); provenance recorded in the design's
frontmatter as `decision_provenance: inline-resolved`. Evidence base: the
round-1/2/3 exploration research in wip/research/ (prep-path map, plan-shaped
contract, support matrix, no-behavior-change proof, rename blast radius,
Go-pattern precedent, and the measured codex-cli 0.147.0 behavior). The
committed design cites the underlying evidence (code paths, PR numbers, the
standing spike), never these wip paths.

| id | artifact | tier | status | question |
|----|----------|------|--------|----------|
| 1 | DESIGN Considered Options, Decision 1 | 4 | confirmed | Plan-producing leaf `internal/agentplan` + agent-blind executor; four-op closed set (write, append-line, replace-section, deliver-tree), ops 1-3 in PR 1 |
| 2 | DESIGN Considered Options, Decision 2 | 4 | confirmed | Two states + three reason kinds + Requires edges; row 4 Implemented (measured), row 12 Unavailable not-built (measured settable; consent design deliberately deferred) |
| 3 | DESIGN Considered Options, Decision 3 | 3 | confirmed | AST layout scan (two halves, filename half RED at eight sites today), pure plan-shape/exhaustiveness table test, wiring + two-direction binding test |
| 4 | DESIGN Considered Options, Decision 4 | 3 | confirmed | ManagedFiles characterization committed before refactor; golden = sorted (path, normalized hash); normalize `{workspace}` expansion and inject fixed NiwaPath |
| 5 | DESIGN Considered Options, Decision 5 | 4 | confirmed | Structured `[mcp.servers.*]` neutral declaration generating both formats; validate + re-decode before atomic write; collision = hard error via developer-config read; per-server `agents` scoping as the SSE escape hatch |
| 6 | DESIGN Considered Options, Decision 6 | 3 | confirmed | Reinstate `[content]` as canonical, `[claude.content]` becomes the deprecated alias (reverses #51 openly); `content_dir` stays top-level; `claude.enabled` restructured to gate Claude-only deliveries; `[repos.*.codex].enabled` added in PR 2 |
| 7 | DESIGN Considered Options, Decision 7 | 3 | assumed | 0600 + instance-root `.codex/` gitignore entry + repo-exclude patterns land in the session-env increment; niwa does not write `ignore_default_excludes`; guide safety note instead |
