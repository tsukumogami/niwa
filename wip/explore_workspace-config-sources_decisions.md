# Exploration Decisions: workspace-config-sources

## Auto Mode

Switched to `--auto` mode at user request after Phase 1 checkpoint approval.
Default `max-rounds = 3`. Each downstream decision (round continuation,
crystallize artifact type, handoff target) will be made by research-first
protocol and recorded here.

## Round 1 — dispatch
- Dispatched 7 agents in parallel, one per lead from the scope file
  (lead-current-architecture, lead-partial-fetch-mechanisms,
  lead-snapshot-shape, lead-discovery-conventions, lead-peer-tool-survey,
  lead-identity-and-state, lead-example-walkthroughs). Rationale: leads
  are independent enough to fan out without serial dependencies; the
  current-architecture audit (L1) is the only one others might want to
  read, but each agent has direct access to the codebase and can audit
  what they need rather than wait.
- Used `general-purpose` agent type for all 7. Rationale: each agent
  needs Write access to its findings file, which the read-only Explore
  agent can't do.
- For lead-example-walkthroughs (L7), instructed the agent to use
  `env -u GH_TOKEN gh ...` to access the two private brain repos with
  the user's personal token, and to capture *structural* patterns only
  (file/dir layout, presence of CLAUDE.md / .claude / docs) — never
  specific private content (PRD bodies, strategy text, internal
  discussions). Findings live in the public niwa repo.

## Round 1 — convergence decisions
- Snapshot model is the direction (no working tree, no .git/). Converges
  across L1, L2, L3, L5, L6, L7 — no serious alternative surfaced.
- `.niwa/` at brain-repo root is the placement convention. L4 + L7 agree;
  reject `niwa.toml`-only as the sole convention for non-toy configs;
  reject `dot-niwa/`-as-dirname (reserve `dot-niwa` strictly as standalone
  repo name).
- Three-marker root-only discovery: `.niwa/workspace.toml` > root
  `workspace.toml` > root `niwa.toml`, hard-error on ambiguity.
- GitHub REST tarball + `tar` extraction is the primary fetch mechanism;
  git-clone fallback for non-GitHub.
- Subpath is first-class in registry; state schema bumps to v3 with
  `config_source` block; lazy migration.
- `vault_scope` keyed on workspace name unchanged.
- No `.niwaroot`-style indirection (L4 reasoning supersedes L5's
  suggestion: registry-time caching makes the chezmoi-style flexibility
  unnecessary).
- Defer multi-workspace shared cache to a follow-up: ship Option A
  (snapshot direct at `<workspace>/.niwa/`) at v1, layer Option C
  (content-addressed cache + symlink) later if multi-workspace dedup
  demand emerges.
- Skip telemetry-source design now (no telemetry pipeline exists).

## Round 1 — loop continuation decision
- **Decision: crystallize, do not run round 2.**
- Rationale: the remaining open questions in the findings file (slug
  delimiter `:` vs `//`, provenance/lock file shape, snapshot direct vs
  symlink-to-cache at v1, default-branch resolution timing, instance.json
  placement, migration cutover ergonomics, `niwa.toml` content_dir
  requirement, multi-host adapter scope) are *decision-shaped*, not
  exploration-shaped. Another discover-converge round would re-cover the
  same surface; the remaining work belongs in an artifact (PRD or design
  doc) that frames the trade-offs and forces a pick. The user signaled
  this trajectory upfront ("This may lead into a PRD before we get into
  a design").
