@plugin-release-hold
Feature: niwa holds each plugin marketplace release once per host
  A github marketplace's content used to be fetched into every instance's
  .niwa/marketplaces/. niwa now resolves the marketplace's track to a commit
  and holds that release once per workspace under ~/.niwa, read-only and
  marked, and an instance holds only a link to it. So the second instance of a
  workspace downloads nothing.

  GitHub is the suite's tarball fake: it resolves each tag to its commit and
  serves the tarball, and its log is what "downloaded" means. The marketplaces' tags are listed from bare repos on the local git
  server, which the sandbox home's git config puts in place of github.com.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a fake claude for plugin pre-warming
    And the GitHub marketplace "acme/skills" publishes release "v1.0.0"
    And the GitHub marketplace "beta/tools" publishes release "v2.1.0"
    And a source repo "app" exists
    And a config repo "ws" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [claude]
      plugins = ["skills-kit@skills", "tools-kit@tools"]

      [[claude.marketplaces]]
      source = "acme/skills"

      [[claude.marketplaces]]
      source = "beta/tools"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """

  # Each row makes the second instance a different way; the first is always
  # niwa create. Across both creates each release is downloaded once, at its
  # commit, and the second instance holds nothing but links.
  @critical
  Scenario Outline: A second instance downloads no held release
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the GitHub fake served one tarball request for each marketplace, at the commit of its newest release
    When the GitHub fake log is cleared
    And I run "<second>"
    Then the exit code is 0
    And the GitHub fake served no tarball request
    And the GitHub fake served one commit-resolve request for each marketplace and nothing else for them
    And the marketplaces of instance "ws-2" are only links into the plugin store of workspace root "."
    And the marketplaces of instance "ws" are only links into the plugin store of workspace root "."
    And every marketplace's skill resolves in instance "ws" at its root, in repo "app" and in a session worktree
    And every marketplace's skill resolves in instance "ws-2" at its root, in repo "app" and in a session worktree
    And the held releases are read-only and marked
    And no file of a release tree exists under workspace root "."
    And the workspace config of root "." carries no "share_plugin_releases" key

    Examples:
      | second         |
      | niwa create ws |

  # An instance stays on its release for life: a newer tag changes nothing for
  # an instance already linked, and an apply downloads nothing.
  @critical
  Scenario: An instance stays on its release after a newer tag
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    And the GitHub marketplace "beta/tools" publishes release "v2.2.0"
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws"
    Then the exit code is 0
    And the GitHub fake served no tarball request
    And instance "ws" links "acme/skills" to its release "v1.0.0"
    And instance "ws" links "beta/tools" to its release "v2.1.0"
    And every marketplace's skill resolves in instance "ws" at its root, in repo "app" and in a session worktree

  # An instance made before the store holds its marketplace as a real
  # directory. It keeps it, unchanged, and fetches nothing.
  @critical
  Scenario: An instance holding its own copy keeps it
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the marketplace "skills" of instance "ws" is replaced by a real copy of its tree
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws"
    Then the exit code is 0
    And the GitHub fake served no tarball request
    And the marketplace "skills" of instance "ws" is the same real directory

  # The store's directory is keyed by the workspace root's real path: two
  # roots get two, and a root reached through a symlink gets its own one.
  @critical
  Scenario: Each workspace root has its own store, whatever path reaches it
    Given a config repo "ws1" exists with body:
      """
      [workspace]
      name = "ws1"

      [groups.tools]

      [claude]
      plugins = ["skills-kit@skills", "tools-kit@tools"]

      [[claude.marketplaces]]
      source = "acme/skills"

      [[claude.marketplaces]]
      source = "beta/tools"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    And a config repo "ws2" exists with body:
      """
      [workspace]
      name = "ws2"

      [groups.tools]

      [claude]
      plugins = ["skills-kit@skills", "tools-kit@tools"]

      [[claude.marketplaces]]
      source = "acme/skills"

      [[claude.marketplaces]]
      source = "beta/tools"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init "one" from config repo "ws1"
    Then the exit code is 0
    When I run niwa init "two" from config repo "ws2"
    Then the exit code is 0
    When I run "niwa create one"
    Then the exit code is 0
    When I run "niwa create two"
    Then the exit code is 0
    Then the sandbox home holds plugin stores under 2 workspace key directories
    And the marketplaces of instance "one" are only links into the plugin store of workspace root "one"
    And the marketplaces of instance "two" are only links into the plugin store of workspace root "two"
    Given I link "one-alias" to workspace root "one"
    When the GitHub fake log is cleared
    And I run "niwa create" from the link "one-alias"
    Then the exit code is 0
    And the GitHub fake served no tarball request
    And the sandbox home holds plugin stores under 2 workspace key directories
    And the marketplaces of instance "one-2" are only links into the plugin store of workspace root "one"

  # With no workspace root the instance is the root, and its marketplaces stay
  # inside it as before; nothing is written under ~/.niwa for them.
  @critical
  Scenario: A single-instance layout keeps its own copy and writes nothing under ~/.niwa
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa apply ws"
    Then the exit code is 0
    And the marketplace "skills" of the workspace root is a real directory holding its manifest
    And the marketplace "tools" of the workspace root is a real directory holding its manifest
    And nothing for plugins exists under the sandbox home's .niwa
