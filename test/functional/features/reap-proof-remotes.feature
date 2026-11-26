Feature: niwa reap counts a commit as held only when a proof remote shows it
  The remotes in a repository's configuration are written by the agent, so
  only a proof remote can show that a commit exists somewhere else: a remote
  whose URL equals the clone URL the workspace configuration gives, or one
  under a [session.removal] proof_remotes prefix. Any other remote is never
  contacted, and a commit only it could hold keeps the unit.

  The suite's workspace repositories are local bare repositories reached by
  file URL, admitted on the removal path by NIWA_TEST_REMOVAL_FILE_REMOTES.
  The scenarios that need a remote niwa must not contact use an https git
  server that counts every request; its CA reaches the removal path through
  NIWA_TEST_REMOVAL_CA_FILE, which the step that starts it sets.

  @critical
  Scenario: a unit whose only proof remote is unreachable keeps with git's error
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And a fake claude for dispatch with session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1"
    When I run "niwa dispatch unreachable-proof --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When the dispatch session "f1f1f1f1-f1f1-4f1f-8f1f-f1f1f1f1f1f1" is deleted from the Agent View
    And the workspace configuration's URL for repo "myapp" points at a refused local address with "" and query ""
    And repo "tools/myapp" of the dispatch instance has its origin at the workspace configuration's URL for repo "myapp"
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And the dispatch instance still exists
    And the error output contains "clone URL unreachable: "
    And the error output contains "unable to access"

  Scenario: an ext:: remote and a file:// remote are never contacted and keep the unit
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a fake claude for dispatch with session "f2f2f2f2-f2f2-4f2f-8f2f-f2f2f2f2f2f2"
    When I run "niwa dispatch refused-remotes --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When repo "tools/myapp" of the dispatch instance has remote "helper" at "ext::sh -c touch% {marker}"
    And the dispatch session "f2f2f2f2-f2f2-4f2f-8f2f-f2f2f2f2f2f2" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "remote URL not allowed: scheme not allowed: ext (remote helper)"
    And the output contains "remote URL not allowed: scheme not allowed: file (remote origin)"
    And the ext marker was never written

  Scenario: an agent-added remote that advertises the unit's commit is never contacted
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And an https git server is running
    And a fake claude for dispatch with session "f3f3f3f3-f3f3-4f3f-8f3f-f3f3f3f3f3f3"
    When I run "niwa dispatch agent-remote --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit with a file change is made on branch "agent-work" in repo "tools/myapp" of the dispatch instance
    And the https git server serves "agent/myapp.git" holding branch "agent-work" of repo "tools/myapp" in the dispatch instance
    And repo "tools/myapp" of the dispatch instance has remote "agent" at "https://{server}/agent/myapp.git"
    And the dispatch session "f3f3f3f3-f3f3-4f3f-8f3f-f3f3f3f3f3f3" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "remote not recognized: agent"
    And the output contains "commits the clone URL does not show"
    And the https git server saw no request

  Scenario: a commit pushed to the clone URL is held and the unit is removed
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And a fake claude for dispatch with session "f4f4f4f4-f4f4-4f4f-8f4f-f4f4f4f4f4f4"
    When I run "niwa dispatch pushed-work --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit with a file change is made on branch "pushed-work" in repo "tools/myapp" of the dispatch instance
    And branch "pushed-work" of repo "tools/myapp" in the dispatch instance is pushed to "origin"
    And the dispatch session "f4f4f4f4-f4f4-4f4f-8f4f-f4f4f4f4f4f4" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains

  Scenario: a workspace clone whose origin is re-pointed keeps and its new origin is never contacted
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And an https git server is running
    And a fake claude for dispatch with session "f5f5f5f5-f5f5-4f5f-8f5f-f5f5f5f5f5f5"
    When I run "niwa dispatch repointed-origin --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit with a file change is made on branch "moved-work" in repo "tools/myapp" of the dispatch instance
    And the https git server serves "elsewhere/myapp.git" holding branch "moved-work" of repo "tools/myapp" in the dispatch instance
    And repo "tools/myapp" of the dispatch instance has remote "origin" at "https://{server}/elsewhere/myapp.git"
    And the dispatch session "f5f5f5f5-f5f5-4f5f-8f5f-f5f5f5f5f5f5" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "remote not recognized: origin"
    And the https git server saw no request

  Scenario: a bare repository whose remote equals the clone URL is held when pushed and kept when not
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And a fake claude for dispatch with session "f6f6f6f6-f6f6-4f6f-8f6f-f6f6f6f6f6f6"
    When I run "niwa dispatch bare-clone --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a bare clone of repo "myapp" from the workspace configuration's URL sits at "keep.git" in the dispatch instance
    And the dispatch session "f6f6f6f6-f6f6-4f6f-8f6f-f6f6f6f6f6f6" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "reap"
    When a commit with a file change is made on branch "local-only" in repo "tools/myapp" of the dispatch instance
    And branch "local-only" of repo "tools/myapp" in the dispatch instance is pushed to "keep.git"
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "keep.git: commits the clone URL does not show: branch local-only"

  Scenario: a fully pushed clone at extra/other, outside every provisioned repository, is held
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And a fake claude for dispatch with session "f7f7f7f7-f7f7-4f7f-8f7f-f7f7f7f7f7f7"
    When I run "niwa dispatch extra-clone --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a clone of repo "myapp" from the workspace configuration's URL sits at "extra/other" in the dispatch instance
    And the dispatch session "f7f7f7f7-f7f7-4f7f-8f7f-f7f7f7f7f7f7" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains

  Scenario: a commit on a remote under a proof_remotes prefix is held and the unit is removed
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And an https git server is running
    And a fake claude for dispatch with session "f8f8f8f8-f8f8-4f8f-8f8f-f8f8f8f8f8f8"
    When I run "niwa dispatch fork-work --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit with a file change is made on branch "fork-work" in repo "tools/myapp" of the dispatch instance
    And the https git server serves "dana/myapp.git" holding branch "fork-work" of repo "tools/myapp" in the dispatch instance
    And repo "tools/myapp" of the dispatch instance has remote "fork" at "https://{server}/dana/myapp.git"
    And the workspace configuration lists the proof remote prefix "https://{server}/dana/"
    And the dispatch session "f8f8f8f8-f8f8-4f8f-8f8f-f8f8f8f8f8f8" is deleted from the Agent View
    And I run niwa reap from the workspace root
    Then the exit code is 0
    And no dispatch instance remains
    And the https git server saw a request for "dana/myapp.git"

  Scenario: remotes a proof_remotes prefix must not vouch for keep the unit and are never contacted
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And an https git server is running
    And a fake claude for dispatch with session "f9f9f9f9-f9f9-4f9f-8f9f-f9f9f9f9f9f9"
    When I run "niwa dispatch prefix-refusals --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a commit with a file change is made on branch "unpushed" in repo "tools/myapp" of the dispatch instance
    And the https git server serves "mallory/myapp.git" holding branch "unpushed" of repo "tools/myapp" in the dispatch instance
    And the https git server serves "dana-evil/myapp.git" holding branch "unpushed" of repo "tools/myapp" in the dispatch instance
    And the https git server serves "dana/myapp.git" holding branch "unpushed" of repo "tools/myapp" in the dispatch instance
    And repo "tools/myapp" of the dispatch instance has remote "dots" at "https://{server}/dana/../mallory/myapp.git"
    And repo "tools/myapp" of the dispatch instance has remote "encoded" at "https://{server}/dana/%2e%2e/mallory/myapp.git"
    And repo "tools/myapp" of the dispatch instance has remote "creds" at "https://dana:hunter2@{server}/dana/myapp.git"
    And repo "tools/myapp" of the dispatch instance has remote "evil" at "https://{server}/dana-evil/myapp.git"
    And repo "tools/myapp" of the dispatch instance has remote "port" at "https://{otherport}/dana/myapp.git"
    And the workspace configuration lists the proof remote prefix "https://{server}/dana/"
    And the dispatch session "f9f9f9f9-f9f9-4f9f-8f9f-f9f9f9f9f9f9" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "remote URL not allowed: the path holds a dot segment (remote dots)"
    And the output contains "remote URL not allowed: the URL carries percent-encoding (remote encoded)"
    And the output contains "remote URL not allowed: the URL carries userinfo (remote creds)"
    And the output contains "remote not recognized: evil"
    And the output contains "remote not recognized: port"
    And no output contains "hunter2"
    And the https git server saw no request

  Scenario: a local tag keeps the unit until a proof remote lists the same tag at the same object
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "myapp" exists with a commit
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.myapp]
      url = "{repo:myapp}"
      group = "tools"
      """
    When I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_REMOVAL_FILE_REMOTES" to "1"
    And a fake claude for dispatch with session "fafafafa-fafa-4faf-8faf-fafafafafafa"
    When I run "niwa dispatch local-tags --detach" from the workspace root
    Then the exit code is 0
    And a dispatch instance was created with a well-formed instance file
    When a lightweight tag "v1" is made in repo "tools/myapp" of the dispatch instance
    And the dispatch session "fafafafa-fafa-4faf-8faf-fafafafafafa" is deleted from the Agent View
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "tag v1"
    When branch "refs/tags/v1" of repo "tools/myapp" in the dispatch instance is pushed to "origin"
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "reap"
    When the origin of repo "tools/myapp" in the dispatch instance holds an annotated tag "v1" at another object
    And I run "niwa reap --verdict-only" from the workspace root
    Then the exit code is 0
    And the output gives the dispatch instance the verdict "keep"
    And the output contains "tag v1"
