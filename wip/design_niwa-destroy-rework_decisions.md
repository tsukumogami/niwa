# Design Decisions: niwa-destroy-rework

Decisions made during the design phase via the lightweight decision protocol
(per `--auto` mode in the design skill). All decision blocks are inline in
`docs/designs/current/DESIGN-niwa-destroy.md` under `## Considered Options`.

| ID | Artifact | Tier | Status | Question |
|---|---|---|---|---|
| control-flow | docs/designs/current/DESIGN-niwa-destroy.md | 2 | confirmed | `runDestroy` control flow shape: branched RunE vs dispatch-to-mode-runners |
| cwd-discriminator | docs/designs/current/DESIGN-niwa-destroy.md | 2 | confirmed | CWD discriminator: reuse pattern vs new helper |
| scan-api | docs/designs/current/DESIGN-niwa-destroy.md | 2 | confirmed | Scan API shape: simple functions vs builder pattern |
| picker-description | docs/designs/current/DESIGN-niwa-destroy.md | 2 | assumed | Picker `Choice.Description` content |
| wipe-ordering | docs/designs/current/DESIGN-niwa-destroy.md | 2 | confirmed | Workspace-wipe daemon-kill ordering |
| test-plan | docs/designs/current/DESIGN-niwa-destroy.md | 2 | confirmed | Test plan: scenarios and unit-test layout |

## Carry-over from exploration

The following exploration decisions are also load-bearing for this design and
recorded in the design doc's "Decisions Already Made" section. Cross-reference
to `wip/explore_niwa-destroy-rework_decisions.md` for full context.

### From the user (settled before research, all confirmed)

- Empty workspace + `niwa destroy` (no arg) deletes without `--force`.
- Non-empty workspace + `niwa destroy` (no arg) shows a picker.
- `niwa destroy <name>` is only valid from the workspace root.
- Per-instance destroy keeps today's `--force` semantics.
- Empty-workspace definition is **lax**.
- Workspace-self-destroy under `--force` scans every instance for non-pushed
  work, including across worktrees.

### From research (all confirmed)

- Picker reuse: copy `tsuku/internal/tui/picker.go` into `niwa/internal/tui/`.
- Wrapper change is a one-line case extension.
- New helper file `internal/workspace/scan.go`.
- New helper file `internal/workspace/destroy_workspace.go`.
- Sequential workspace-wipe ordering (alphabetical).
- Typed-confirmation fires BEFORE `writeLandingPath`.
- Confirmation token is the override-aware workspace name.

### From the explore-phase lightweight protocol (all confirmed)

- Reset stays out of scope.
- Single-instance picker is skip-and-go.
- PRD-shell-integration R2 cleanup is out of scope.
- No confirmation prompt when destroying from inside an instance.
