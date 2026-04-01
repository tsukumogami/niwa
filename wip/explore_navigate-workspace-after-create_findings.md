# Exploration Findings: navigate-workspace-after-create

## Core Question

How should niwa handle post-create navigation into the workspace directory, given
that a compiled binary can't change the parent shell's working directory? And does
the right solution imply that tsuku needs a general mechanism for post-install shell
integration?

## Round 1

### Key Insights

- The eval-init pattern (`eval "$(tool init shell)"`) is the dominant modern approach
  for compiled CLIs needing shell integration. Zoxide is the closest analog: binary
  resolves path, shell function does `cd`. (lead: shell-navigation-patterns)
- Niwa already has `~/.niwa/env` sourced in bash/zsh rc files -- the delivery mechanism
  for shell functions exists. (lead: niwa-shell-integration)
- Cobra gives niwa `niwa completion bash/zsh/fish` for free with zero code. This
  eliminates completions as a validating use case for tsuku generalization.
  (lead: completion-install-patterns)
- Tsuku has no post-install shell setup capability. No action for sourceable files, no
  auto-sourcing. The `set_env` action creates env.sh files nothing reads.
  (lead: tsuku-action-system)
- Niwa-only solution costs ~50 LOC (Go + shell). Tsuku generalization would be 200+
  LOC across two repos with no second consumer. (lead: complexity-boundary)
- Demand is maintainer-driven, consistent with early stage. Feature explicitly deferred
  in DESIGN-workspace-config.md, not rejected. (lead: adversarial-demand)

### Tensions

- User's tsuku instinct vs. research: initial hypothesis was tsuku might need post-install
  scripts. All leads converge on niwa owning this.
- Two viable niwa-only patterns: env file (transparent UX, coupled to install.sh) vs.
  `niwa init <shell>` (modern convention, requires user to add eval line).

### Gaps

- Binary-to-shell communication protocol not designed (output parsing vs. directive file
  vs. structured protocol)
- Fish support unclear for env file approach; eval-init handles it naturally
- Subcommand scope: issue mentions repo-level navigation, not just workspace root

### Decisions

- Niwa owns shell integration, not tsuku
- Completions don't validate tsuku generalization
- Eval-init pattern is the right approach
- Demand is legitimate but early-stage

### User Focus

Auto-mode: user indicated focus on niwa, with tsuku involvement contingent on
solution shape. Research resolved the contingency -- tsuku involvement not warranted.

## Accumulated Understanding

The core question is answered: niwa should own its shell integration using the
eval-init pattern (`eval "$(niwa init bash)"`). This is the proven modern approach
used by zoxide, direnv, and mise. It requires ~50 lines of code, no tsuku changes,
and handles both navigation and completions through a single mechanism.

The remaining design questions are tactical:
1. What subcommands should the shell function intercept? (at minimum `create`, possibly
   a new `go` subcommand for navigating to existing workspaces)
2. How does the binary communicate "cd to this path" to the shell function? (structured
   output protocol, directive file, or output parsing)
3. Should `niwa init` bundle completions in its output?
4. What happens to the existing `~/.niwa/env` file? (replaced by `niwa init`, or
   `niwa init` generates the env file content)

These are design doc questions, not exploration questions. The problem space is
well-understood and the approach is clear.

## Decision: Crystallize
