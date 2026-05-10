# Exploration Decisions: session-attach

Auto-mode decisions made during exploration. Each entry: what was decided, why,
and how it shapes downstream artifacts.

## Round 1

- **Topic = `session-attach`**: derived from issue #117 title; kebab-case for branch and wip artifact naming.
- **7 round-1 leads, equal weight**: per user direction "spend equal amount with agents investigating each thoroughly". One agent per major open question in the issue body, plus the adversarial-demand lead required by the `needs-prd` label.
- **Adversarial demand finding documented as a known caveat, not a stop-gate**: the round-1 demand-validation agent reported "demand not validated" (single-author proposal, zero corroborating asks). User direction is to proceed; the PRD will surface this as a risk/assumption rather than route to a "don't pursue" outcome.

## Round 2 (planned)

- **5 round-2 agents, all UX-focused per user direction**: full-breadth UX investment.
  - `ux-cli-tone`: niwa CLI tone audit (consistency / voice / output formatting / error messages across all existing commands)
  - `ux-peer-patterns`: peer-tool human-takeover patterns (tmux, kubectl exec/attach, fly ssh console, screen, ssh)
  - `ux-scenarios`: 5-7 concrete scenario walkthroughs with exact terminal output mockups
  - `ux-mcp-surface`: MCP-tool surface review (does attach need a new tool, or extend existing ones)
  - `transcript-failure-modes`: drill-down on round 1's highest-stakes finding (claude --resume CWD requirement, silent failure modes)
- **Round 2 agents must peek at PR #115**: per user direction, to avoid divergence with the in-flight mesh-reliability design (DESIGN-niwa-mesh-reliability covering #108/#109/#111/#112).

## Pipeline-level decisions

- **Auto mode for the entire pipeline**: explore → /shirabe:prd → /shirabe:design → /shirabe:plan (single-pr) → /shirabe:work-on. User will check status in the morning.
- **Single branch, single PR**: all work lands on `docs/session-attach`. Draft PR opens after the PRD is committed.
- **v1 scope = full**: attach + `niwa session detach <id> --force` + `AVAILABILITY` column on `niwa session list`. One coherent PR.
- **Blocker policy**: if I hit a hard blocker, run /shirabe:decision framework, document the chosen path, keep going.
- **Done bar**: unit tests + `@critical` Gherkin functional test + docs updated + go vet clean + I run scenarios locally myself + observed UX documented.
- **Out-of-scope**: don't fix #108/#109/#111/#112 directly. PR #115 is fixing them.
