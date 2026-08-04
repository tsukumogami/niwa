Feature: niwa create refreshes the root-managed config
  End-to-end coverage that `niwa create` re-materializes the workspace root, so
  the root skills and the dispatched-worker agreement are present and current
  before the new instance is used.

  MaterializeWorkspaceRoot otherwise runs only on a root-scope `niwa apply` and
  on `niwa init` in named/clone mode. A workspace whose owner only ever applies
  a single instance would never receive niwa's shipped content -- nor a
  correction to it. `niwa dispatch` reaches an instance through create, which
  makes create the moment before a dispatched worker reads
  .niwa/dispatch-briefs/_common.md.

  The scenario deletes the root-managed paths after init on purpose: without the
  delete it would pass whether or not create materializes the root, because init
  already wrote them.

  Runs offline against the localGitServer bare-repo fake; no GitHub access.

  Design: docs/designs/DESIGN-orchestration-learnings.md

  @critical
  Scenario: niwa create re-materializes deleted root-managed content
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init "ws" from config repo "ws"
    Then the exit code is 0
    And the file ".claude/skills/dispatch/SKILL.md" exists under workspace root "ws"
    And the file ".niwa/dispatch-briefs/_common.md" exists under workspace root "ws"

    When I delete ".claude/skills" under workspace root "ws"
    And I delete ".niwa/dispatch-briefs" under workspace root "ws"
    And I run "niwa create ws"
    Then the exit code is 0
    And the file ".claude/skills/dispatch/SKILL.md" exists under workspace root "ws"
    And the file ".claude/skills/fleet/SKILL.md" exists under workspace root "ws"
    And the file ".claude/skills/fleet/references/review-standard.md" exists under workspace root "ws"
    And the file ".niwa/dispatch-briefs/_common.md" exists under workspace root "ws"
    And the file ".niwa/dispatch-briefs/_common.md" under workspace root "ws" contains "niwa:dispatch-brief-common:start"
    And the file ".niwa/dispatch-briefs/_common.md" under workspace root "ws" contains "Common working agreement for dispatched workers"
