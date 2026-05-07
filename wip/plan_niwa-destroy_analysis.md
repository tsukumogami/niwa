# Plan Analysis: niwa-destroy

## Source

- input_type: design
- source: docs/designs/current/DESIGN-niwa-destroy.md
- status: Planned (niwa equivalent of "Accepted")
- visibility: Public
- scope: Tactical

## Components from the design

### Code surface

| Path | Status | Purpose |
|---|---|---|
| `internal/cli/destroy.go` | rewrite | Cobra command + dispatcher + mode-specific runners |
| `internal/cli/destroy_test.go` | extend | Control flow + error wording |
| `internal/cli/prompt.go` | new | `IsStdinTTY`, `ReadConfirmation` |
| `internal/cli/prompt_test.go` | new | Prompt tests |
| `internal/cli/shell_init.go` | modify | One-line case extension to add `destroy` |
| `internal/cli/shell_init_test.go` | refactor | Per-subcommand membership checks |
| `internal/tui/picker.go` | new (copy) | Copied from `tsukumogami/tsuku@c8f58101` |
| `internal/tui/sanitize.go` | new (copy) | Copied from upstream |
| `internal/tui/picker_test.go` | new (copy) | Copied from upstream |
| `internal/workspace/cwd_classify.go` | new | `ClassifyCwd`, `Classify`, `CwdClass` |
| `internal/workspace/cwd_classify_test.go` | new | Table-driven cwd-class tests |
| `internal/workspace/scan.go` | new | `LossKind`, `Loss`, `RepoScan`, `InstanceScan`, `ScanInstance`, `ScanInstancesParallel`, `FormatScans` |
| `internal/workspace/scan_test.go` | new | Per-LossKind detection + edge cases |
| `internal/workspace/destroy_workspace.go` | new | `DestroyWorkspace` |
| `internal/workspace/destroy_workspace_test.go` | new | Happy path + invariant test |
| `internal/workspace/destroy.go` | UNTOUCHED | Reset shares this — must not change |

### Functional tests

| Path | Status | Purpose |
|---|---|---|
| `test/functional/features/destroy.feature` | new | 4 @critical + 3 standard scenarios |

### Doc surface

| Path | Edit type |
|---|---|
| `docs/prds/PRD-shell-integration.md` | amend (R1, R11, D3, Out-of-Scope) |
| `docs/prds/PRD-cross-session-communication.md` | amend (R38, AC-P11) |
| `docs/prds/PRD-workspace-config-sources.md` | amend (line 1001) |
| `docs/designs/current/DESIGN-instance-lifecycle.md` | amend (Decision 4) |
| `docs/designs/current/DESIGN-shell-navigation-protocol.md` | amend (cd-eligible list) |
| `docs/designs/current/DESIGN-contextual-completion.md` | amend (Decision 3) |

## Decomposition strategy choice

**Horizontal**, not walking skeleton.

Rationale: the design lays out clear layered components (picker package, helpers, command rewrite, tests). Each helper has a stable interface and is independently testable. Walking-skeleton is appropriate when integration risk is high and components must be exercised end-to-end early; here, the wiring is mechanical once the helpers exist. Horizontal lets each helper land with its own unit tests, then a single command-rewrite issue stitches them together.

## Execution mode

**single-pr** (user-directed). Implementation work continues on `docs/niwa-destroy-rework` (PR #106). All issues land as one squash-merged commit; no per-issue branches or PRs.
