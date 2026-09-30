Feature: provisioning falls back on stored secret values
  When the Infisical CLI's login has lapsed, or the service can't be reached
  or doesn't answer in time, provisioning serves the last values that
  resolved for each provider folder from niwa's store and warns that they
  may be stale. These scenarios drive the real binary against the shared
  infisical stub (INFISICAL_STUB_EXPORT_FAIL picks the export's failure,
  INFISICAL_STUB_LOGIN_STATUS the session probe's answer) and fill the store
  directly with backdated values. NIWA_TEST_VAULT_TIMEOUT shortens the 30 s
  export and 15 s probe bounds to 1 s, so "within 6 seconds" is the bound
  plus the 5 s the requirements allow.

  PRD: docs/prds/PRD-dispatch-offline-secrets.md
  Design: docs/designs/current/DESIGN-dispatch-offline-secrets.md

  Background:
    Given a clean niwa environment
    And a local git server is set up
    And a source repo "app" exists
    And a config repo "myws" exists with body:
      """
      [workspace]
      name = "myws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [vault.provider]
      kind = "infisical"
      project = "fb-proj"

      [env.secrets]
      ROOT_KEY = "vault://ROOT_KEY"
      FOLDER_KEY = "vault://folder/FOLDER_KEY"
      """
    And the secret store holds "ROOT_KEY" = "stored-root-value" for infisical project "fb-proj" env "dev" at path "/" resolved 3 days ago
    And the secret store holds "FOLDER_KEY" = "stored-folder-value" for infisical project "fb-proj" env "dev" at path "/folder" resolved 2 hours ago

  # --- Every provisioning surface serves a lapsed login from the store ---

  # Before failures were classified, this stub output was a hard error; the
  # golden fixture recorded then still says so.
  Scenario: niwa dispatch serves stored values while logged out
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    Given the vault golden fixture "logged-out-no-valid-session" records the error "infisical: export exited 1: error: No valid login session found"
    And I set env "INFISICAL_STUB_EXPORT_FAIL" to "no-valid-session"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "none"
    And a fake claude for dispatch with session "f0f0f0f0-f0f0-4f0f-8f0f-f0f0f0f0f0f0"
    When I run "niwa dispatch fallback-task --detach" from the workspace root
    Then the exit code is 0
    And the file "tools/app/.local.env" in the dispatch instance contains "ROOT_KEY=stored-root-value"
    And the file "tools/app/.local.env" in the dispatch instance contains "FOLDER_KEY=stored-folder-value"
    And the error output contains "warning: using stored values that may be stale for infisical project fb-proj (env dev, path /, https://app.infisical.com): the provider is logged out or expired; the oldest value is 3 days old. Run `infisical login` to refresh them."
    And the error output contains "warning: using stored values that may be stale for infisical project fb-proj (env dev, path /folder, https://app.infisical.com): the provider is logged out or expired; the oldest value is 2 hours old. Run `infisical login` to refresh them."
    And the error output does not contain "export exited 1"

  Scenario: niwa create serves stored values while logged out
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "INFISICAL_STUB_EXPORT_FAIL" to "session-expired"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "none"
    When I run "niwa create myws"
    Then the exit code is 0
    And the file "tools/app/.local.env" in instance "myws" contains "ROOT_KEY=stored-root-value"
    And the file "tools/app/.local.env" in instance "myws" contains "FOLDER_KEY=stored-folder-value"
    And the error output contains "may be stale for infisical project fb-proj (env dev, path /, https://app.infisical.com): the provider is logged out or expired; the oldest value is 3 days old. Run `infisical login` to refresh them."

  # The instance is created while logged out too: a create that reached the
  # stub would find these keys missing upstream and evict them.
  Scenario: niwa apply serves stored values while logged out
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "INFISICAL_STUB_EXPORT_FAIL" to "could-not-find-login"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "none"
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa apply myws"
    Then the exit code is 0
    And the file "tools/app/.local.env" in instance "myws" contains "ROOT_KEY=stored-root-value"
    And the error output contains "may be stale for infisical project fb-proj (env dev, path /folder, https://app.infisical.com): the provider is logged out or expired; the oldest value is 2 hours old. Run `infisical login` to refresh them."

  Scenario: niwa reset serves stored values while logged out
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "INFISICAL_STUB_EXPORT_FAIL" to "no-valid-session"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "none"
    When I run "niwa create myws"
    Then the exit code is 0
    When I run "niwa reset myws" from workspace root
    Then the exit code is 0
    And the file "tools/app/.local.env" in instance "myws" contains "FOLDER_KEY=stored-folder-value"
    And the error output contains "may be stale for infisical project fb-proj (env dev, path /, https://app.infisical.com): the provider is logged out or expired; the oldest value is 3 days old. Run `infisical login` to refresh them."

  # --- An answered refusal is never served, and it evicts ---

  # A verified session makes a 401/403 a real refusal, and a 404 is always
  # one: today's handling (a tolerated mark for the 403, a hard error for the
  # 404), nothing served, and every key the run requested from the folder is
  # evicted, so the next lapse has nothing to serve for it. Only the root
  # folder fails: a 404 stops the resolve walk, and keys resolve in no fixed
  # order, so failing both folders would leave which one is evicted to chance.
  Scenario Outline: an answered <failure> evicts the folder's stored keys
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    And the secret store holds "ROOT_KEY" for infisical project "fb-proj" env "dev" at path "/"
    Given I set env "INFISICAL_STUB_EXPORT_FAIL" to "<failure>"
    And I set env "INFISICAL_STUB_EXPORT_FAIL_PATH" to "/"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "verified"
    When I run "niwa create myws"
    Then the exit code is <exit>
    And the error output contains "<today>"
    And the error output does not contain "may be stale"
    And the secret store no longer holds "ROOT_KEY" for infisical project "fb-proj" env "dev" at path "/"
    Given I set env "INFISICAL_STUB_EXPORT_FAIL" to "no-valid-session"
    And I set env "INFISICAL_STUB_EXPORT_FAIL_PATH" to ""
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "none"
    When I run "niwa create myws"
    Then the exit code is 0
    And the error output does not contain "may be stale for infisical project fb-proj (env dev, path /,"
    And the error output contains "warning: infisical project fb-proj (env dev, path /, https://app.infisical.com) could not be used and no previously resolved value exists to fall back on. Run `infisical login`."

    Examples:
      | failure      | exit  | today                |
      | response-403 | 0     | could not be reached |
      | response-404 | not 0 | Response Code: 404   |

  # --- Bounded calls ---

  Scenario: a hanging export for a CLI session times out and falls back
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_VAULT_TIMEOUT" to "1s"
    And I set env "INFISICAL_STUB_EXPORT_FAIL" to "hang"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "verified"
    When I run "niwa create myws"
    Then the exit code is 0
    And the command finished within 6 seconds
    And the file "tools/app/.local.env" in instance "myws" contains "ROOT_KEY=stored-root-value"
    And the file "tools/app/.local.env" in instance "myws" contains "FOLDER_KEY=stored-folder-value"
    And the error output contains "may be stale for infisical project fb-proj (env dev, path /, https://app.infisical.com): the provider timed out; the oldest value is 3 days old. Run `infisical login` to refresh them."
    And the error output contains "may be stale for infisical project fb-proj (env dev, path /folder, https://app.infisical.com): the provider timed out; the oldest value is 2 hours old. Run `infisical login` to refresh them."
    And the error output contains "may be stale" 2 times
    And the error output contains "NIWA_TEST_VAULT_TIMEOUT=1s is shortening vault call deadlines"

  Scenario: a hanging export whose child holds stdout open is killed with its child
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_VAULT_TIMEOUT" to "1s"
    And I set env "INFISICAL_STUB_EXPORT_FAIL" to "hang-fork"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "verified"
    And the infisical stub records the PID of any child it forks
    When I run "niwa create myws"
    Then the exit code is 0
    And the command finished within 6 seconds
    And the infisical stub's forked child is gone
    And the file "tools/app/.local.env" in instance "myws" contains "ROOT_KEY=stored-root-value"
    And the error output contains "the provider timed out"

  # A minted principal (a machine identity from provider-auth.toml, logged in
  # against the REST double) needs no probe: a hanging export is unreachable
  # on its own.
  Scenario: a hanging export for a minted principal times out without a probe
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    Given an infisical REST double is configured
    And the infisical REST double exchanges client_secret "fb-client-secret" for access token "fb-access-token"
    And a provider-auth file for infisical project "fb-proj" with client_secret "fb-client-secret"
    And the secret store holds "ROOT_KEY" = "minted-root-value" for infisical project "fb-proj" env "dev" at path "/" resolved 1 day ago
    And the secret store holds "FOLDER_KEY" = "minted-folder-value" for infisical project "fb-proj" env "dev" at path "/folder" resolved 1 day ago
    And I set env "NIWA_TEST_VAULT_TIMEOUT" to "1s"
    And I set env "INFISICAL_STUB_EXPORT_FAIL" to "hang"
    And the infisical stub logs its invocations
    When I run "niwa create myws"
    Then the exit code is 0
    And the command finished within 6 seconds
    And the infisical REST double recorded a request containing "/v1/auth/universal-auth/login"
    And the file "tools/app/.local.env" in instance "myws" contains "ROOT_KEY=minted-root-value"
    And the file "tools/app/.local.env" in instance "myws" contains "FOLDER_KEY=minted-folder-value"
    And the error output contains "the provider timed out"
    And the infisical stub logged 0 "login status" invocations

  Scenario: a hanging probe after a quick export failure times out and falls back
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_VAULT_TIMEOUT" to "1s"
    And I set env "INFISICAL_STUB_EXPORT_FAIL" to "connection-refused"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "hang"
    And the infisical stub logs its invocations
    When I run "niwa create myws"
    Then the exit code is 0
    And the command finished within 6 seconds
    And the infisical stub logged 1 "export" invocation
    And the infisical stub logged 1 "login status" invocation
    And the file "tools/app/.local.env" in instance "myws" contains "ROOT_KEY=stored-root-value"
    And the error output contains "may be stale for infisical project fb-proj (env dev, path /, https://app.infisical.com): the provider timed out"

  Scenario: a hanging probe whose child holds stdout open is killed with its child
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    Given I set env "NIWA_TEST_VAULT_TIMEOUT" to "1s"
    And I set env "INFISICAL_STUB_EXPORT_FAIL" to "connection-refused"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "hang-fork"
    And the infisical stub records the PID of any child it forks
    When I run "niwa create myws"
    Then the exit code is 0
    And the command finished within 6 seconds
    And the infisical stub's forked child is gone
    And the file "tools/app/.local.env" in instance "myws" contains "FOLDER_KEY=stored-folder-value"
    And the error output contains "the provider timed out"

  # One hanging domain costs one export bound and one probe bound per run.
  # Credential sync runs first, against the same domain, so its export is the
  # one that times out; it takes its existing soft path, and the two team
  # folders never start an export. The sync source has a stored entry too,
  # which it must never read.
  Scenario: one hanging domain is tried once per run
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    Given a personal overlay exists with body:
      """
      [global.vault.provider]
      kind = "infisical"
      project = "fb-sync-proj"
      """
    And the secret store holds "p-fb-proj" = "stored-sync-body" for infisical project "fb-sync-proj" env "dev" at path "/niwa/provider-auth/infisical" resolved 1 day ago
    And I set env "NIWA_TEST_VAULT_TIMEOUT" to "1s"
    And I set env "INFISICAL_STUB_EXPORT_FAIL" to "hang"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "verified"
    And the infisical stub logs its invocations
    When I run "niwa create myws"
    Then the exit code is 0
    And the command finished within 6 seconds
    And the infisical stub logged 1 "export" invocation
    And the infisical stub logged 1 "login status" invocation
    And the file "tools/app/.local.env" in instance "myws" contains "ROOT_KEY=stored-root-value"
    And the file "tools/app/.local.env" in instance "myws" contains "FOLDER_KEY=stored-folder-value"
    And the error output contains "(env dev, path /, https://app.infisical.com): the provider timed out"
    And the error output contains "(env dev, path /folder, https://app.infisical.com): the provider timed out"
    And the error output contains "unreachable; falling back to local-file and cli-session credentials"
    And the error output does not contain "fb-sync-proj (env dev"

  # A lapsed login is likewise tried once per domain: three folders cost one
  # export and one probe. (A minted provider on a hanging domain skipping its
  # export while its universal-auth login is still made is covered by
  # TestRunStateSkipsAfterTimeout in internal/vault/infisical.)
  Scenario: one logged-out domain is tried once per run
    Given a config repo "three" exists with body:
      """
      [workspace]
      name = "three"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [vault.provider]
      kind = "infisical"
      project = "fb-proj"

      [env.secrets]
      ROOT_KEY = "vault://ROOT_KEY"
      FOLDER_KEY = "vault://folder/FOLDER_KEY"
      OTHER_KEY = "vault://other/OTHER_KEY"
      """
    And the secret store holds "OTHER_KEY" = "stored-other-value" for infisical project "fb-proj" env "dev" at path "/other" resolved 5 minutes ago
    When I run niwa init from config repo "three"
    Then the exit code is 0
    Given I set env "INFISICAL_STUB_EXPORT_FAIL" to "no-valid-session"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "none"
    And the infisical stub logs its invocations
    When I run "niwa create three"
    Then the exit code is 0
    And the infisical stub logged 1 "export" invocation
    And the infisical stub logged 1 "login status" invocation
    And the file "tools/app/.local.env" in instance "three" contains "OTHER_KEY=stored-other-value"
    And the error output contains "may be stale" 3 times

  # --- The session-start hook carries the notices in its payload ---

  Scenario: the hook's payload carries the stale-value warning
    Given a config repo "hookws" exists with body:
      """
      [workspace]
      name = "hookws"

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [vault.provider]
      kind = "infisical"
      project = "proj-123"
      env = "prod"

      [env.secrets]
      BACKEND_KEY = "vault://backend/BACKEND_KEY"
      """
    And the secret store holds "BACKEND_KEY" = "stored-backend-value" for infisical project "proj-123" env "prod" at path "/backend" resolved 5 days ago
    When I run niwa init from config repo "hookws"
    Then the exit code is 0
    Given I set env "INFISICAL_STUB_EXPORT_FAIL" to "no-valid-session"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "none"
    And a background job state exists for session "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1"
    When I pipe a SessionStart hook for session "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1"
    Then the exit code is 0
    And the hook payload's context holds the notice fixture "context-served.txt"
    And the file "tools/app/.local.env" in instance "hookws-a1a1a1a1-a1a" contains "BACKEND_KEY=stored-backend-value"
    And the error output does not contain "may be stale"

  Scenario Outline: the hook's payload says when nothing is stored to fall back on
    Given a config repo "hookws" exists with body:
      """
      [workspace]
      name = "hookws"
      <strict>

      [groups.tools]

      [repos.app]
      url = "{repo:app}"
      group = "tools"

      [vault.provider]
      kind = "infisical"
      project = "proj-123"
      env = "prod"

      [env.secrets]
      BACKEND_KEY = "vault://backend/BACKEND_KEY"

      [env.secrets.required]
      BACKEND_KEY = "the backend needs it"
      """
    When I run niwa init from config repo "hookws"
    Then the exit code is 0
    Given the secret store is empty
    And I set env "INFISICAL_STUB_EXPORT_FAIL" to "no-valid-session"
    And I set env "INFISICAL_STUB_LOGIN_STATUS" to "none"
    And a background job state exists for session "b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2"
    When I pipe a SessionStart hook for session "b2b2b2b2-b2b2-4b2b-8b2b-b2b2b2b2b2b2"
    Then the exit code is 0
    And the hook payload's context holds the notice fixture "context-nothing-to-fall-back-on.txt"
    And the hook payload's context <refusal> "No niwa instance was provisioned for this session"

    # Without strict mode a required key behind a lapsed login is reported,
    # not fatal, as it was before the fallback; strict mode refuses, and the
    # hook delivers that refusal as a payload too.
    Examples:
      | strict                | refusal             |
      |                       | does not contain    |
      | strict_secrets = true | contains            |

  Scenario: the hook's payload warns that the store is inside a git work tree
    Given a config repo "hookws" exists with body:
      """
      [workspace]
      name = "hookws"

      [vault.provider]
      kind = "infisical"
      project = "proj-123"

      [env.secrets]
      HOOK_KEY = "vault://HOOK_KEY"
      """
    And the infisical stub holds "HOOK_KEY" = "hook-value" in project "proj-123" at path "/"
    When I run niwa init from config repo "hookws"
    Then the exit code is 0
    Given the secret store is inside a git work tree
    And a background job state exists for session "c3c3c3c3-c3c3-4c3c-8c3c-c3c3c3c3c3c3"
    When I pipe a SessionStart hook for session "c3c3c3c3-c3c3-4c3c-8c3c-c3c3c3c3c3c3"
    Then the exit code is 0
    And the hook payload's context contains "the secret store directory $STORE_DIR is inside a git work tree; niwa stored no values in it this run."

  Scenario: the hook's payload warns that the store is unwritable
    Given a config repo "hookws" exists with body:
      """
      [workspace]
      name = "hookws"

      [vault.provider]
      kind = "infisical"
      project = "proj-123"

      [env.secrets]
      HOOK_KEY = "vault://HOOK_KEY"
      """
    And the infisical stub holds "HOOK_KEY" = "hook-value" in project "proj-123" at path "/"
    When I run niwa init from config repo "hookws"
    Then the exit code is 0
    Given the secret store directory is unwritable
    And a background job state exists for session "d4d4d4d4-d4d4-4d4d-8d4d-d4d4d4d4d4d4"
    When I pipe a SessionStart hook for session "d4d4d4d4-d4d4-4d4d-8d4d-d4d4d4d4d4d4"
    Then the exit code is 0
    And the hook payload's context contains "the secret store directory $STORE_DIR could not be read or written; values resolved in this run were not stored."

  # --- Secret values stay in the store and the instance ---

  # Four runs over one instance: a fallback, a success, a strict run that
  # fails with nothing stored, and a 404 answer. After
  # each, the marker values may appear only in the store and in the
  # instance's files, and the token the probe printed appears nowhere.
  Scenario: secret values and the probe's token never leak
    Given I run niwa init from config repo "myws"
    Then the exit code is 0
    Given the infisical stub holds "ROOT_KEY" = "marker-fresh-root" in project "fb-proj" at path "/"
    And the infisical stub holds "FOLDER_KEY" = "marker-fresh-folder" in project "fb-proj" at path "/folder"
    And the secret store holds "ROOT_KEY" = "marker-stored-root" for infisical project "fb-proj" env "dev" at path "/" resolved 1 day ago
    And the secret store holds "FOLDER_KEY" = "marker-stored-folder" for infisical project "fb-proj" env "dev" at path "/folder" resolved 1 day ago
    And I set env "INFISICAL_STUB_PROBE_TOKEN" to "probe-token-marker"
    And the infisical stub logs its invocations
    And I record the sandbox files
    # A 401 under the default, unverified session runs the probe, which
    # prints the token, and falls back.
    Given I set env "INFISICAL_STUB_EXPORT_FAIL" to "response-401"
    When I run "niwa create myws"
    Then the exit code is 0
    And the error output contains "may be stale"
    And the infisical stub logged 1 "login status" invocation
    And the last run leaked none of "marker-fresh-root,marker-fresh-folder,marker-stored-root,marker-stored-folder" outside the store and instance "myws", nor the probe token "probe-token-marker"
    Given I set env "INFISICAL_STUB_EXPORT_FAIL" to ""
    When I run "niwa apply myws"
    Then the exit code is 0
    And the file "tools/app/.local.env" in instance "myws" contains "ROOT_KEY=marker-fresh-root"
    And the last run leaked none of "marker-fresh-root,marker-fresh-folder,marker-stored-root,marker-stored-folder" outside the store and instance "myws", nor the probe token "probe-token-marker"
    Given the secret store is empty
    And I set env "INFISICAL_STUB_EXPORT_FAIL" to "response-401"
    When I run "niwa apply myws --strict-secrets"
    Then the exit code is not 0
    And the error output contains "no previously resolved value exists to fall back on"
    And the last run leaked none of "marker-fresh-root,marker-fresh-folder,marker-stored-root,marker-stored-folder" outside the store and instance "myws", nor the probe token "probe-token-marker"
    Given I set env "INFISICAL_STUB_EXPORT_FAIL" to "response-404"
    When I run "niwa apply myws"
    Then the exit code is not 0
    And the last run leaked none of "marker-fresh-root,marker-fresh-folder,marker-stored-root,marker-stored-folder" outside the store and instance "myws", nor the probe token "probe-token-marker"
