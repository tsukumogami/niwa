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

  # A held release whose tree no longer matches the seal in its held.json was
  # changed after it was placed. The next command moves it aside, fetches it
  # again at its commit and says it was tampered with, whichever instance it
  # runs for, with nothing run inside the instance that links it.
  @critical
  Scenario: An apply in another instance refetches an edited release
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the release "v1.0.0" of "acme/skills" is tampered with in the plugin store of workspace root "." by editing a file
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws-2"
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the release "v1.0.0" of "acme/skills" in the plugin store of workspace root "." matches its tarball
    And the output reports the release "v1.0.0" of "acme/skills" tampered for instances "ws,ws-2"
    And instance "ws" links "acme/skills" to its release "v1.0.0"
    And every marketplace's skill resolves in instance "ws" at its root, in repo "app" and in a session worktree

  # An added file, a removed one and a chmod with no content change each
  # count as tampering.
  @critical
  Scenario Outline: A release changed by <change> is fetched again
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the release "v1.0.0" of "acme/skills" is tampered with in the plugin store of workspace root "." by <change>
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws"
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the release "v1.0.0" of "acme/skills" in the plugin store of workspace root "." matches its tarball
    And the output reports the release "v1.0.0" of "acme/skills" tampered for instance "ws"

    Examples:
      | change          |
      | adding a file   |
      | removing a file |
      | a chmod alone   |

  # An unchanged release passes the walk, whether it was placed by this
  # command or an earlier one, and costs no request.
  @critical
  Scenario: An unchanged held release costs no tarball request
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws"
    Then the exit code is 0
    And the GitHub fake served 0 tarball requests in all
    When I run "niwa apply" in instance "ws"
    Then the exit code is 0
    And the GitHub fake served 0 tarball requests in all
    And the output does not contain "tampered"
    And the error output does not contain "tampered"

  # The tampered line reaches the terminal for a create, dispatch's output
  # and the hook's context, as the other repair lines do.
  @critical
  Scenario Outline: A command that makes an instance refetches a tampered release
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the release "v1.0.0" of "acme/skills" is tampered with in the plugin store of workspace root "." by editing a file
    When the GitHub fake log is cleared
    And I make the second instance with <second>
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the release "v1.0.0" of "acme/skills" in the plugin store of workspace root "." matches its tarball
    And the output reports the release "v1.0.0" of "acme/skills" tampered for instance "ws"
    And every marketplace's skill resolves in instance "ws" at its root, in repo "app" and in a session worktree
    And every marketplace's skill resolves in the second instance at its root, in repo "app" and in a session worktree

    Examples:
      | second                |
      | niwa create ws        |
      | niwa dispatch         |
      | the SessionStart hook |

  # A tampered release whose refetch fails is a warning that says it was
  # tampered with, not a failed command. The changed tree is gone from the
  # release path and nothing is marked held; the next command restores it.
  @critical
  Scenario: A failed refetch of a tampered release warns and the next command restores it
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the release "v1.0.0" of "acme/skills" is tampered with in the plugin store of workspace root "." by editing a file
    When I run "niwa apply" in instance "ws" with every tarball fetch failing
    Then the exit code is 0
    And the output reports the release "v1.0.0" of "acme/skills" not restored after tampering for instance "ws"
    And the plugin store of workspace root "." holds nothing for the release "v1.0.0" of "acme/skills"
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws"
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the release "v1.0.0" of "acme/skills" in the plugin store of workspace root "." matches its tarball
    And the output reports the release "v1.0.0" of "acme/skills" restored for instance "ws"

  # Three instances on one tampered release, and the current instance's own
  # placement after the repair pass, make one request in one command.
  @critical
  Scenario: Three instances on one tampered release make one tarball request
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the release "v1.0.0" of "acme/skills" is tampered with in the plugin store of workspace root "." by adding a file
    When the GitHub fake log is cleared
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the release "v1.0.0" of "acme/skills" in the plugin store of workspace root "." matches its tarball
    And the output reports the release "v1.0.0" of "acme/skills" tampered for instances "ws,ws-2,ws-3"

  # Each provisioning command sweeps the held releases no instance on disk
  # links to, except the newest of each source by commit time. Three releases
  # of one source, the two older linked by instances since taken off disk, go
  # down to the newest, with the staging and trash entries of the two older
  # ones, and nothing is downloaded.
  @critical
  Scenario: Three held releases of a source are swept down to the newest
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.2.0"
    When I run "niwa create ws"
    Then the exit code is 0
    And the plugin store of workspace root "." holds only the releases "v1.0.0,v1.1.0,v1.2.0" of "acme/skills"
    Given instances "ws,ws-2" are removed from disk
    And staging and trash entries are planted in the plugin store of workspace root "." for the releases "v1.0.0,v1.1.0" of "acme/skills"
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws-3"
    Then the exit code is 0
    And the GitHub fake served 0 tarball requests in all
    And the plugin store of workspace root "." holds only the release "v1.2.0" of "acme/skills"
    And the plugin store of workspace root "." holds no staging or trash entry for the releases "v1.0.0,v1.1.0" of "acme/skills"
    And the plugin store of workspace root "." holds only the release "v2.1.0" of "beta/tools"
    And every marketplace's skill resolves in instance "ws-3" at its root, in repo "app" and in a session worktree

  # The newest held release survives with no instance linking it. A release
  # repair restores at its old commit keeps its old commit time, so the newest
  # stays the newest and survives the next sweep as well.
  @critical
  Scenario: The newest release survives unlinked and a restored older release keeps its commit time
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.2.0"
    When I run "niwa create ws"
    Then the exit code is 0
    Given instances "ws-2,ws-3" are removed from disk
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws"
    Then the exit code is 0
    And the GitHub fake served 0 tarball requests in all
    And the plugin store of workspace root "." holds only the releases "v1.0.0,v1.2.0" of "acme/skills"
    Given the release "v1.0.0" of "acme/skills" is deleted from the plugin store of workspace root "."
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws"
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the release "v1.0.0" of "acme/skills" is held again in the plugin store of workspace root "."
    And the plugin store of workspace root "." holds only the releases "v1.0.0,v1.2.0" of "acme/skills"

  # An older release linked only by an instance kept under the sandbox home's
  # kept/ survives every command that provisions or removes an instance of the
  # workspace, and the kept instance's skills still resolve.
  @critical
  Scenario Outline: A kept instance keeps its older release through <command>
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    When I run "niwa create ws"
    Then the exit code is 0
    Given instance "ws" is moved under the sandbox home's kept/
    When I run <command> beside the kept instance
    Then the exit code is 0
    And the plugin store of workspace root "." holds only the releases "v1.0.0,v1.1.0" of "acme/skills"
    And every marketplace's skill resolves in the kept instance "ws" at its root and in repo "app"

    Examples:
      | command                     |
      | niwa create ws              |
      | niwa apply in instance ws-2 |
      | niwa dispatch               |
      | the SessionStart hook       |
      | niwa reset ws-2             |
      | niwa destroy ws-2           |

  # The same holds for an older release linked only by an instance under the
  # root, whether or not it still has its .niwa/instance.json.
  @critical
  Scenario Outline: An instance on disk keeps its older release when it is <holder>
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    When I run "niwa create ws"
    Then the exit code is 0
    Given instance "ws" is <holder>
    When I run "niwa apply" in instance "ws-2"
    Then the exit code is 0
    And the plugin store of workspace root "." holds only the releases "v1.0.0,v1.1.0" of "acme/skills"
    And every marketplace's skill resolves in instance "ws" at its root and in repo "app"

    Examples:
      | holder                               |
      | left as it is                        |
      | stripped of its .niwa/instance.json  |

  # A scan that can't see every instance deletes nothing. When the kept
  # directory or one instance's marketplaces can't be read, the command still
  # succeeds and says, in one line naming the path, that the sweep was
  # skipped; repair's own line about the same listing is not printed beside
  # it. With the mode back, the next command sweeps as usual.
  #
  # An unreadable workspace root never reaches the sweep: apply fails earlier,
  # at the config swap, because clearing the old snapshot needs to list the
  # root. The scenario after the outline asserts that failure and a store
  # with every release still in it, then restores the mode and sweeps.
  @critical
  Scenario Outline: An unreadable scan skips the sweep when <unreadable> cannot be read
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given instance "ws" is removed from disk
    And <unreadable> cannot be read
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws-3"
    Then the exit code is 0
    And the plugin store of workspace root "." holds only the releases "v1.0.0,v1.1.0" of "acme/skills"
    And the output reports the sweep skipped once, naming the path that could not be read, and no repair scan warning
    Given its mode is restored
    When I run "niwa apply" in instance "ws-3"
    Then the exit code is 0
    And the output does not contain "sweep of unused releases"
    And the error output does not contain "sweep of unused releases"
    And the plugin store of workspace root "." holds only the release "v1.1.0" of "acme/skills"

    Examples:
      | unreadable                          |
      | the kept directory                  |
      | the marketplaces of instance "ws-2" |

  @critical
  Scenario: An unreadable workspace root stops apply before the sweep and leaves every release in place
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given instance "ws" is removed from disk
    And the workspace root cannot be read
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws-3"
    Then the exit code is not 0
    And the error output contains "EnsureConfigSnapshot"
    And the error output contains "permission denied"
    And the output does not contain "sweep of unused releases"
    And the error output does not contain "sweep of unused releases"
    And the plugin store of workspace root "." holds only the releases "v1.0.0,v1.1.0" of "acme/skills"
    Given its mode is restored
    When I run "niwa apply" in instance "ws-3"
    Then the exit code is 0
    And the output does not contain "sweep of unused releases"
    And the error output does not contain "sweep of unused releases"
    And the plugin store of workspace root "." holds only the release "v1.1.0" of "acme/skills"

  # Destroying one instance deletes nothing from the store; the next
  # provisioning command sweeps the release it freed.
  @critical
  Scenario: A destroyed instance's release is swept by the next command
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa destroy ws --force" from workspace root
    Then the exit code is 0
    And the plugin store of workspace root "." holds only the releases "v1.0.0,v1.1.0" of "acme/skills"
    When I run "niwa apply" in instance "ws-2"
    Then the exit code is 0
    And the plugin store of workspace root "." holds only the release "v1.1.0" of "acme/skills"

  # Destroying the whole workspace removes its plugin store when no kept
  # instance remains, and its key directory only when nothing else is left in
  # it.
  @critical
  Scenario: Destroying the workspace removes its plugin store
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    When I run "niwa destroy --workspace --confirm ws" from workspace root
    Then the exit code is 0
    And the plugin store of the removed workspace root is gone, and its key directory with it unless something else is in it

  # With a kept instance still there, the store stays, its path is printed,
  # and the kept instance's skills still resolve.
  @critical
  Scenario: Destroying the workspace keeps the plugin store for a kept instance
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    When I run "niwa create ws"
    Then the exit code is 0
    Given instance "ws" is moved under the sandbox home's kept/
    When I run "niwa destroy --workspace --confirm ws" from workspace root
    Then the exit code is 0
    And the plugin store of the removed workspace root is kept, and the output names its path
    And every marketplace's skill resolves in the kept instance "ws" at its root and in repo "app"

  # share_plugin_releases = false under [workspace] gives a create its own
  # copy of each marketplace, fetched by tag as a create did before the store,
  # and nothing is held for that root. Another workspace on the same host,
  # with no setting, still links and downloads nothing it already holds.
  @critical
  Scenario: With share_plugin_releases off a create holds its own copy while another workspace links
    Given a config repo "ws-off" exists with body:
      """
      [workspace]
      name = "ws-off"
      share_plugin_releases = false

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
    When I run niwa init "one" from config repo "ws-off"
    Then the exit code is 0
    When I run niwa init "two" from config repo "ws"
    Then the exit code is 0
    When the GitHub fake log is cleared
    And I run "niwa create one"
    Then the exit code is 0
    And the marketplaces of instance "one" of workspace root "one" are real directories holding their manifests
    And the GitHub fake served one tarball request for each marketplace, at the tag of its newest release, and nothing else for them
    And workspace root "one" has no plugin store
    And every marketplace's skill resolves in instance "one" of workspace root "one" at its root, in repo "app" and in a session worktree
    And workspace root "one" has no plugin store
    When the GitHub fake log is cleared
    And I run "niwa create two"
    Then the exit code is 0
    When the GitHub fake log is cleared
    And I run "niwa create two"
    Then the exit code is 0
    And the GitHub fake served no tarball request
    And the marketplaces of instance "two-2" are only links into the plugin store of workspace root "two"
    And the workspace config of root "two" carries no "share_plugin_releases" key

  # An instance linked before the setting was turned off keeps its link and
  # its skills, and a release of it deleted by hand is restored by the next
  # apply with the setting off. The instance made after the switch keeps its
  # own copy through that apply.
  @critical
  Scenario: An instance linked before the switch keeps its link, its skills and its repair
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the config repo "ws" is force-pushed to:
      """
      [workspace]
      name = "ws"
      share_plugin_releases = false

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
    When I run "niwa create ws"
    Then the exit code is 0
    And the marketplaces of instance "ws-2" of workspace root "." are real directories holding their manifests
    And the marketplaces of instance "ws" are only links into the plugin store of workspace root "."
    Given the release "v1.0.0" of "acme/skills" is deleted from the plugin store of workspace root "."
    When the GitHub fake log is cleared
    And I run "niwa apply" in instance "ws-2"
    Then the exit code is 0
    And the GitHub fake served 1 tarball request for the release "v1.0.0" of "acme/skills"
    And the GitHub fake served 1 tarball request in all
    And the release "v1.0.0" of "acme/skills" is held again in the plugin store of workspace root "."
    And the output reports the release "v1.0.0" of "acme/skills" restored for instance "ws"
    And instance "ws" links "acme/skills" to its release "v1.0.0"
    And every marketplace's skill resolves in instance "ws" at its root, in repo "app" and in a session worktree
    And the marketplace "skills" of instance "ws-2" is the same real directory
    And the marketplace "tools" of instance "ws-2" is the same real directory

  # The sweep still runs with the setting off: an older release no instance
  # links to is deleted by the next create, and a release only an instance
  # made before the switch links to survives it, beside the newest.
  @critical
  Scenario: The sweep runs with the setting off
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.1.0"
    When I run "niwa create ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" publishes release "v1.2.0"
    When I run "niwa create ws"
    Then the exit code is 0
    And the plugin store of workspace root "." holds only the releases "v1.0.0,v1.1.0,v1.2.0" of "acme/skills"
    Given the config repo "ws" is force-pushed to:
      """
      [workspace]
      name = "ws"
      share_plugin_releases = false

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
    And instance "ws-2" is removed from disk
    When I run "niwa create ws"
    Then the exit code is 0
    And the plugin store of workspace root "." holds only the releases "v1.0.0,v1.2.0" of "acme/skills"
    And instance "ws" links "acme/skills" to its release "v1.0.0"

  # A marketplace tracked at a moving ref gives the same content to an
  # instance made with the store on and to one made with it off, while the
  # ref names the same commit. With the store off the ref is fetched as it
  # is, with no commit resolve first.
  @critical
  Scenario: A moving ref gives the same content with the store on and off
    Given a config repo "moving" exists with body:
      """
      [workspace]
      name = "ws"

      [groups.tools]

      [claude]
      plugins = ["skills-kit@skills", "tools-kit@tools"]

      [[claude.marketplaces]]
      source = "acme/skills"
      track = "main"

      [[claude.marketplaces]]
      source = "beta/tools"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    And the GitHub marketplace "acme/skills"'s main branch is at release "v1.0.0"
    When I run niwa init from config repo "moving"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    And the marketplaces of instance "ws" are only links into the plugin store of workspace root "."
    Given the config repo "moving" is force-pushed to:
      """
      [workspace]
      name = "ws"
      share_plugin_releases = false

      [groups.tools]

      [claude]
      plugins = ["skills-kit@skills", "tools-kit@tools"]

      [[claude.marketplaces]]
      source = "acme/skills"
      track = "main"

      [[claude.marketplaces]]
      source = "beta/tools"

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When the GitHub fake log is cleared
    And I run "niwa create ws"
    Then the exit code is 0
    And the marketplaces of instance "ws-2" of workspace root "." are real directories holding their manifests
    And the GitHub fake served one tarball request for "acme/skills" at "HEAD" and no commit-resolve request for it
    And the marketplace "skills" of instance "ws" and of instance "ws-2" hold the same tree

  # Removing the key, or setting it back to true, links the next create into
  # the store again, and an instance holding its own copy is not converted to
  # a link by an apply.
  @critical
  Scenario Outline: Turning sharing back on links new creates and converts nothing
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the config repo "ws" is force-pushed to:
      """
      [workspace]
      name = "ws"
      share_plugin_releases = false

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
    When I run "niwa create ws"
    Then the exit code is 0
    And the marketplaces of instance "ws-2" of workspace root "." are real directories holding their manifests
    Given the config repo "ws" is force-pushed to:
      """
      [workspace]
      name = "ws"
      <setting>

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
    When the GitHub fake log is cleared
    And I run "niwa create ws"
    Then the exit code is 0
    And the GitHub fake served no tarball request
    And the marketplaces of instance "ws-3" are only links into the plugin store of workspace root "."
    When I run "niwa apply" in instance "ws-2"
    Then the exit code is 0
    And the marketplace "skills" of instance "ws-2" is the same real directory
    And the marketplace "tools" of instance "ws-2" is the same real directory

    Examples:
      | setting                      |
      |                              |
      | share_plugin_releases = true |

  # A store niwa can't use falls back to a copy fetched into the instance, as
  # niwa did before the store, and the create says which store and why. The
  # narrowed mode is left as it was: niwa never changes a mode it didn't just
  # create. For "cannot be created" ~/.niwa itself is narrowed, since the git
  # mirror the create checks first puts a narrowed key directory back.
  @critical
  Scenario Outline: A create on a plugin store that <how> falls back to its own copy and names the store
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given the plugin store of workspace root "." <how>
    When the GitHub fake log is cleared
    And I run "niwa create ws"
    Then the exit code is 0
    And the output reports the plugin store of workspace root "." as "<condition>" once, for every marketplace
    And the marketplaces of instance "ws" of workspace root "." are real directories holding their manifests
    And every marketplace's skill resolves in instance "ws" of workspace root "." at its root, in repo "app" and in a session worktree
    And the narrowed path keeps its mode
    When its mode is restored
    Then no marketplace has a release directory or a held.json in the plugin store of workspace root "."

    Examples:
      | how                   | condition          |
      | cannot be read        | unreadable         |
      | cannot be written     | unwritable         |
      | cannot be created     | can't be created   |
      | cannot take a release | release not placed |

  # The write probe is of plugins/ itself, so a writable key directory above
  # it doesn't hide an unwritable store.
  @critical
  Scenario: An unwritable plugins/ under a writable key directory is reported as unwritable
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given the plugin store of workspace root "." cannot be written
    And the key directory of workspace root "." is writable
    When I run "niwa create ws"
    Then the exit code is 0
    And the marketplaces of instance "ws" of workspace root "." are real directories holding their manifests
    And the output reports the plugin store of workspace root "." as "unwritable" once, for every marketplace
    And the narrowed path keeps its mode

  # Dispatch prints the same notice on its stderr, and the SessionStart hook
  # carries it in the context it injects.
  @critical
  Scenario Outline: Dispatch and the hook report an unusable store through <second>
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given the plugin store of workspace root "." cannot be written
    When I run "niwa create ws"
    Then the exit code is 0
    When the GitHub fake log is cleared
    And I make the second instance with <second>
    Then the exit code is 0
    And the output reports the plugin store of workspace root "." as "unwritable" once, for every marketplace
    And the marketplaces of the second instance are real directories holding their manifests
    And every marketplace's skill resolves in the second instance at its root, in repo "app" and in a session worktree

    Examples:
      | second                |
      | niwa dispatch         |
      | the SessionStart hook |

  # Three creates at once on an unwritable store each fall back to their own
  # copy and each says so; none leaves a probe, a staging entry or a release
  # under plugins/. The config stops tracking its source first, as in the
  # overlapping-creates scenario above (issue #297).
  @critical
  Scenario: Three creates in a burst on an unwritable store each fall back
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given the config of workspace root "." no longer tracks its source
    And the plugin store of workspace root "." cannot be written
    When I start 3 "niwa create ws" processes, each with its own name
    Then every started create exits 0
    And every started create reports the plugin store of workspace root "." as "unwritable" for every marketplace
    And every instance of workspace root "." holds each marketplace as a real directory
    And the plugin store of workspace root "." holds nothing

  # A release that isn't held, while its source is down, fails as a fetch
  # does: the create names the source and the failure, and nothing is held or
  # copied for it. The other marketplace is linked as usual.
  @critical
  Scenario: A create of an unheld release with its source down names the source and holds nothing
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    Given the GitHub marketplace "acme/skills" is down
    When I run "niwa create ws"
    Then the exit code is 0
    And the error output contains "acme/skills"
    And the error output contains "503"
    And the plugin store of workspace root "." holds nothing for the release "v1.0.0" of "acme/skills"
    And the file ".niwa/marketplaces/skills" does not exist in instance "ws"
    And instance "ws" links "beta/tools" to its release "v2.1.0"

  # An unreadable store that instances already link into: the new instance
  # falls back and says so, repair reports the store unreadable rather than a
  # release tampered with, and the sweep's notice names the store's condition.
  @critical
  Scenario: An unreadable store with linked instances reports repair and the sweep with the store's condition
    When I run niwa init from config repo "ws"
    Then the exit code is 0
    When I run "niwa create ws"
    Then the exit code is 0
    Given the plugin store of workspace root "." cannot be read
    When I run "niwa create ws"
    Then the exit code is 0
    And the output reports the plugin store of workspace root "." as "unreadable" once, for every marketplace
    And the error output contains "sweep of unused releases was skipped: the plugin store at"
    And the error output contains "is unreadable"
    And the error output does not contain "tampered with"
    When its mode is restored
