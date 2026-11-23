@mirror-network-env
Feature: The mirror's network calls keep the user's remote-access settings
  The host mirror's clone, refresh fetch and fetch by id reach the remote, so
  they keep the variables a remote may need: the ssh transport, the askpass
  helper, the proxy command, TLS trust, the proxy auth method, and the
  GIT_CONFIG_COUNT entries whose key carries remote access. Every local
  mirror call still strips each inherited GIT_* variable, and every mirror
  call runs with GIT_TERMINAL_PROMPT=0. In the test build niwa create makes
  each new clone borrow and seeds the mirror first, so the borrowers here
  come from niwa create, and a scenario that checks the seed sets the probe
  and the environment before its first create: the create's own seed is the
  mirror's first network call. The test build's environment probe records
  each mirror git process's GIT_* entries in a file in the sandbox home.

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists with 3 commits

  Scenario: Seed, refresh, restore and fetch by id keep the remote-access variables and local calls keep none
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given the mirror's git environment probe is on
    And every remote-access variable is set in niwa's environment
    And I set env "GIT_NIWA_UNLISTED" to "niwa-unlisted-value"
    When I count the probe lines
    And I run "niwa create myws"
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    And the probe gained a network "clone" line
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I add a branch "topic" with one commit to source repo "app"
    And I count the probe lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And the probe gained a network "fetch" line
    And each borrower of "app" has the remote-tracking branch "origin/topic" at the added topic commit
    When I repack the borrowers of "app" against the mirror
    Then each borrower of "app" holds none of the mirror's objects
    Given the borrower of "app" in instance "myws" has a local branch "keep" with one unpushed commit on the added topic commit
    When source repo "app" keeps the added topic commit only under a hidden ref and deletes the added branch
    And I count the probe lines
    And I delete the mirror of "app" and its store
    And I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook's context carries the notice for "app" with fault "missing" and result "restored"
    And the probe gained a network "clone" line
    And the probe gained a network "fetch-by-id" line
    And the mirror of "app" pins the added topic commit and has no branch for it
    And every network line the probe holds carries each remote-access variable niwa was given, with its value
    And every network line the probe holds carries "GIT_TERMINAL_PROMPT" = "0"
    And no line the probe holds carries a GIT variable niwa didn't set itself or keep for the network
    And no line the probe holds names "GIT_NIWA_UNLISTED"
    And nothing mirror-related exists outside the sandbox home

  # The dropped entry sits at index 0, so the kept one shows renumbered.
  Scenario: Only config entries that carry remote access reach the network calls, and no call can prompt
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given the mirror's git environment probe is on
    And I set env "GIT_CONFIG_COUNT" to "2"
    And I set env "GIT_CONFIG_KEY_0" to "remote.origin.prune"
    And I set env "GIT_CONFIG_VALUE_0" to "true"
    And I set env "GIT_CONFIG_KEY_1" to "http.extraHeader"
    And I set env "GIT_CONFIG_VALUE_1" to "X-Niwa-Probe: kept"
    And I set env "GIT_TERMINAL_PROMPT" to "1"
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I add a branch "topic" with one commit to source repo "app"
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the error output carries no mirror warning
    And the probe gained a network "clone" line
    And the probe gained a network "fetch" line
    And every network line the probe holds keeps only the config entry "http.extraHeader" = "X-Niwa-Probe: kept"
    And no line the probe holds names "remote.origin.prune"
    And every network line the probe holds carries "GIT_TERMINAL_PROMPT" = "0"
    And no line the probe holds carries a GIT variable niwa didn't set itself or keep for the network

  # The repo's URL names a host under .invalid, which only the wrapper can
  # reach; niwa's own clones of it reach it the same way. The SessionStart
  # hook that provisions nothing syncs no clone, so every wrapper call after
  # the count before it is the mirror's restore.
  Scenario: A remote reached only through GIT_SSH_COMMAND is mirrored and restored
    Given source repo "app" is reachable only through an ssh wrapper as "app-ssh"
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app-ssh}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    And the mirror log gained a check line and a restore line for "app" with fault "missing" and result "restored" from command "create"
    And the mirror of "app" is a link into its store
    And the mirror of "app" has the remote URL of source repo "app-ssh"
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I delete the mirror of "app" and its store
    And I count the mirror log lines
    And I count the ssh wrapper calls
    And I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook's context carries the notice for "app" with fault "missing" and result "restored"
    And the mirror log gained a check line and a restore line for "app" with fault "missing" and result "restored" from command "from-hook"
    And the mirror of "app" is a link into its store
    And the ssh wrapper was called for source repo "app" since the count
    And each borrower of "app" reads its default branch to the root and fsck is clean
    And nothing mirror-related exists outside the sandbox home

  Scenario: GIT_DIR and GIT_ALTERNATE_OBJECT_DIRECTORIES never reach the healthy check
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    When I repack the borrowers of "app" against the mirror
    Then each borrower of "app" holds none of the mirror's objects
    Given the mirror's git environment probe is on
    And GIT_DIR and GIT_ALTERNATE_OBJECT_DIRECTORIES name an unrelated repo in niwa's environment
    When I remember the mirror link of "app"
    And I count the mirror log lines
    And I count the probe lines
    And I pipe a SessionStart hook for session "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" that is not a background worker
    Then the exit code is 0
    And the hook printed nothing
    And the mirror log gained only a healthy check line for "app" from "from-hook"
    And the mirror link of "app" is unchanged
    And the probe gained a local "rev-parse" line
    And no local line the probe gained carries "GIT_DIR"
    And no local line the probe gained carries "GIT_ALTERNATE_OBJECT_DIRECTORIES"
    And every ref of each borrower of "app" reads to the root and fsck is clean

  Scenario: No environment value reaches the mirror log, which notes what was honoured
    Given a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "GIT_SSH_COMMAND" to "/niwa-sentinel-ssh-command"
    And I set env "GIT_ASKPASS" to "/niwa-sentinel-askpass"
    And I set env "GIT_PROXY_COMMAND" to "/niwa-sentinel-proxy-command"
    And I set env "GIT_SSL_NO_VERIFY" to "niwa-sentinel-no-verify"
    And I set env "GIT_CONFIG_COUNT" to "1"
    And I set env "GIT_CONFIG_KEY_0" to "http.extraHeader"
    And I set env "GIT_CONFIG_VALUE_0" to "X-Niwa: niwa-sentinel-config-value"
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa create myws"
    Then the exit code is 0
    Given the clones of "tools/app" in instances "myws" and "myws-2" were made borrowing by niwa create from the mirror of "app"
    And each borrower of "app" holds no pack a mirror store holds
    When I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror of "app" is a link into its store
    And every restore and refresh line the mirror log gained carries the network notes "ssl-no-verify,proxy-command"
    When I delete the mirror of "app" and its store
    And I count the mirror log lines
    And I run "niwa apply" from workspace root
    Then the exit code is 0
    And the mirror log gained a check line and a restore line for "app" with fault "missing" and result "restored" from command "apply"
    And every restore and refresh line the mirror log gained carries the network notes "ssl-no-verify,proxy-command"
    And the mirror log holds none of "niwa-sentinel-ssh-command,niwa-sentinel-askpass,niwa-sentinel-proxy-command,niwa-sentinel-no-verify,niwa-sentinel-config-value"
