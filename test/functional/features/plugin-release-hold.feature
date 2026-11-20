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
  # commit, and the second instance holds nothing but links. niwa dispatch
  # launches the suite's fake claude; the SessionStart hook is piped the way
  # Claude Code runs it for a background session.
  @critical
  Scenario Outline: A second instance downloads no held release
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the GitHub fake served one tarball request for each marketplace, at the commit of its newest release
    When the GitHub fake log is cleared
    And I make the second instance with <second>
    Then the exit code is 0
    And the GitHub fake served no tarball request
    And the GitHub fake served one commit-resolve request for each marketplace and nothing else for them
    And the marketplaces of the second instance are only links into the plugin store of workspace root "."
    And the marketplaces of instance "ws" are only links into the plugin store of workspace root "."
    And every marketplace's skill resolves in instance "ws" at its root, in repo "app" and in a session worktree
    And every marketplace's skill resolves in the second instance at its root, in repo "app" and in a session worktree
    And the held releases are read-only and marked
    And no file of a release tree exists under workspace root "."
    And the workspace config of root "." carries no "share_plugin_releases" key

    Examples:
      | second                |
      | niwa create ws        |
      | niwa dispatch         |
      | the SessionStart hook |

  # Three creates of one workspace start together with neither release held.
  # The first to reach a download is held there while the other two queue on
  # the release's lock; once it is let go, each release is downloaded once and
  # every instance links the same tree. The config stops tracking its source
  # first: a config source that is not GitHub is re-materialized by every
  # create, and overlapping creates collide in the snapshot writer's staging
  # directory before any plugin is reached (issue #297, tracked on its own).
  @critical
  Scenario: Three overlapping creates download each release once
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given the config of workspace root "." no longer tracks its source
    When I start 3 "niwa create ws" processes, the first plugin store download held
    And the held plugin store download is let go
    Then every started create exits 0
    And the GitHub fake served 1 tarball request for each marketplace, at the commit of its newest release
    And every instance of workspace root "." links each marketplace to the same release tree
    And the held releases are read-only and marked

  # A create killed while its download is held open leaves the release
  # absent, not half placed: no release directory and no mark. The next
  # create fetches it once and links the complete release, and the killed
  # create's staging is gone once it finishes.
  @critical
  Scenario: An interrupted create leaves its release unheld
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I start 1 "niwa create ws" process, the first plugin store download held
    And the held create is killed
    Then no marketplace has a release directory or a held.json in the plugin store of workspace root "."
    When the GitHub fake log is cleared
    And I run "niwa create ws"
    Then the exit code is 0
    And the GitHub fake served one tarball request for each marketplace, at the commit of its newest release
    And the marketplaces of the newest instance are only links into the plugin store of workspace root "."
    And every marketplace's skill resolves in the newest instance at its root, in repo "app" and in a session worktree
    And the held releases are read-only and marked
    And the plugin store of workspace root "." holds no staging entry for any marketplace

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

  # A release an instance links to that goes missing from the store comes
  # back with the next provisioning command, whichever instance it runs for,
  # with nothing run inside the instance that links it. The restore is one
  # tarball request and one line naming the release and its instances.
  @critical
  Scenario: An apply in another instance restores a deleted release
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the release "v1.0.0" of "acme/skills" is deleted from the plugin store of workspace root "."
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws-2"
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the release "v1.0.0" of "acme/skills" is held again in the plugin store of workspace root "."
    And the output reports the release "v1.0.0" of "acme/skills" restored for instances "ws,ws-2"
    And instance "ws" links "acme/skills" to its release "v1.0.0"
    And every marketplace's skill resolves in instance "ws" at its root, in repo "app" and in a session worktree

  # The same restore runs when the next command makes a new instance: a
  # create, a dispatch or the SessionStart hook. Its line reaches the
  # terminal, dispatch's output or the hook's context.
  @critical
  Scenario Outline: A command that makes an instance restores a deleted release
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the release "v1.0.0" of "acme/skills" is deleted from the plugin store of workspace root "."
    When the GitHub fake log is cleared
    And I make the second instance with <second>
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the release "v1.0.0" of "acme/skills" is held again in the plugin store of workspace root "."
    And the output reports the release "v1.0.0" of "acme/skills" restored for instance "ws"
    And every marketplace's skill resolves in instance "ws" at its root, in repo "app" and in a session worktree
    And every marketplace's skill resolves in the second instance at its root, in repo "app" and in a session worktree

    Examples:
      | second                |
      | niwa create ws        |
      | niwa dispatch         |
      | the SessionStart hook |

  # A create whose track now resolves to a newer release still restores the
  # older release another instance links to: one request for each.
  @critical
  Scenario: A create on a newer release restores the older instance's release
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the release "v1.0.0" of "acme/skills" is deleted from the plugin store of workspace root "."
    And the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    When the GitHub fake log is cleared
    And I run "niwa create ws"
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request for the release "v1.1.0" of "acme/skills"
    And the GitHub fake served 2 tarball requests in all
    And the release "v1.0.0" of "acme/skills" is held again in the plugin store of workspace root "."
    And the output reports the release "v1.0.0" of "acme/skills" restored for instance "ws"
    And instance "ws" links "acme/skills" to its release "v1.0.0"
    And instance "ws-2" links "acme/skills" to its release "v1.1.0"
    And every marketplace's skill resolves in instance "ws" at its root, in repo "app" and in a session worktree

  # A release directory without its held.json is not held: the next command
  # fetches it again and marks it.
  @critical
  Scenario: A release whose held.json is gone is fetched again and marked
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the held.json of the release "v1.0.0" of "acme/skills" is deleted from the plugin store of workspace root "."
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws"
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the release "v1.0.0" of "acme/skills" is held again in the plugin store of workspace root "."
    And the output reports the release "v1.0.0" of "acme/skills" restored for instance "ws"

  # A link names a commit; the store fetches it back only when the commit is
  # in the source's own history. One only a fork holds is refused and its
  # link left alone; one behind a tag but at no tip is restored, which the
  # compare endpoint answers.
  @critical
  Scenario: A fork-only commit is refused and a commit behind a tag is restored
    Given the GitHub marketplace "acme/skills" gains an untagged commit
    And the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    And a fork of "acme/skills" holds a commit
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given instance "ws" links "acme/skills" to the fork commit
    And instance "ws-2" links "acme/skills" to the untagged commit
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws-2"
    Then the exit code is 0
    And the GitHub fake served 0 tarball requests for the fork commit of "acme/skills"
    And the GitHub fake served 1 tarball request for the untagged commit of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the plugin store of workspace root "." holds nothing for the fork commit of "acme/skills"
    And instance "ws" still links "acme/skills" to the fork commit
    And the output reports the fork commit of "acme/skills" refused for instance "ws"
    And the untagged commit of "acme/skills" is held again in the plugin store of workspace root "."
    And the output reports the untagged commit of "acme/skills" restored for instance "ws-2"
    And every marketplace's skill resolves in instance "ws-2" at its root, in repo "app" and in a session worktree

  # A refetch that fails is a warning naming the release, not a failed
  # command, and leaves nothing marked held. The next command, with the
  # network back, restores it.
  @critical
  Scenario: A failed refetch warns and the next command restores the release
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the release "v1.0.0" of "acme/skills" is deleted from the plugin store of workspace root "."
    When I run "niwa apply" in instance "ws" with every tarball fetch failing
    Then the exit code is 0
    And the output reports the release "v1.0.0" of "acme/skills" not restored for instance "ws"
    And the plugin store of workspace root "." holds nothing for the release "v1.0.0" of "acme/skills"
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws"
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the release "v1.0.0" of "acme/skills" is held again in the plugin store of workspace root "."
    And the output reports the release "v1.0.0" of "acme/skills" restored for instance "ws"

  # The repair runs once per command per workspace: an apply across three
  # instances that all link one missing release fetches it once.
  @critical
  Scenario: Three instances on one missing release make one tarball request
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the release "v1.0.0" of "acme/skills" is deleted from the plugin store of workspace root "."
    When the GitHub fake log is cleared
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the output reports the release "v1.0.0" of "acme/skills" restored for instances "ws,ws-2,ws-3"
