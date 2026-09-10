---
complexity: testable
complexity_rationale: The change is test code plus at most small behavior-preserving seams in the watch launch path, but it's the end-to-end guard for a security-relevant launch setting and has to assert exact argv, stderr, record, and marker outcomes across terminal and non-terminal runs.
---

## Goal

Cover `niwa dispatch --accept-session-messages` and the `[global] accept_session_messages_on_dispatch` machine setting end to end. That means `@critical` functional scenarios for every case in the PRD's functional-coverage requirement, scenarios for the configuration sources that must not turn the behavior on, for the Codex warning, and for a prompt beginning with `--settings=`, plus a unit test that shows neither `niwa watch` launch site ever carries `crossSessionInbound`.

## Context

Earlier issues in this plan add the behavior. <<ISSUE:4>> adds the flag, the resolver, the capability row, the inbound key in the one rendered `--settings` document, and the audit, override, and warning lines. <<ISSUE:5>> adds the session-mapping and `niwa list` record. <<ISSUE:6>> adds the one-time explanation and its marker. <<ISSUE:1>> adds the review-session deny hook. Unit tests in those issues pin each piece in isolation. This issue runs the real binary against the functional suite's fake `claude` and fake `codex`, so it checks the pieces together: the recorded launch argv, the stderr transcript, the durable record, and the marker file.

Facts about the existing harness that the scenarios build on:

- The dispatch suite's fake `claude` (`dispatchFakeClaudeScript` in `test/functional/dispatch_steps_test.go`) records its `--bg` argv as one space-joined line to `$HOME/dispatch-launch-argv`, which `launchedClaudeArgv` in `keepalive_steps_test.go` and the `the launched claude was invoked with "..."` step read. A space-joined line can't tell the `--settings` element apart from a prompt that also begins with `--settings=`, so the new steps need an element-preserving record.
- The fake `codex` records to `$HOME/dispatch-codex-argv`, read by the `the codex launch argv (does not) contain "..."` steps.
- `buildEnv` in `steps_test.go` sets `HOME` to the scenario home and `XDG_CONFIG_HOME` to `$HOME/.config`, so niwa's machine configuration is `$HOME/.config/niwa/config.toml` and the marker lands at `$HOME/.config/niwa/accept-session-messages-notice`. `niwa init` writes the registry into that same `config.toml`.
- The `I run "..." under a pty with input "..."` step (`iRunUnderPTYWithInput` in `steps_init_bootstrap_test.go`) runs the command under util-linux `script`, which merges stderr into stdout, and copies the merged transcript into both `s.stdout` and `s.stderr`. Terminal scenarios therefore run dispatch with `--detach`, so no `claude attach` takes the terminal, and assert on the merged transcript.
- The existing `the output contains "..."` and `the error output contains "..."` steps take a pattern that can't contain a double quote. The explanation and the warning contain double quotes (`"Messages from your other sessions"`, `"crossSessionInbound": "accept"`, `the "codex" agent`), so verbatim assertions need a docstring step.
- No functional step skips a scenario when tests run as root today. godog v0.15.1 supports `godog.ErrSkip`.
- No unit test drives `stageReview` or `continueReview` in `internal/cli/watch.go` today. Both call `dispatchLaunch` (the package variable in `dispatch_launcher.go`) with a passthrough from `buildDispatchPassthrough`, at about lines 844 and 581.

Design: `docs/designs/DESIGN-dispatch-sendmessage-approval.md` (Implementation Approach > Phase 6, the scenario part; Solution Architecture > Components, the watch-site unit test; Key Interfaces for the fixed strings). Upstream requirements: `docs/prds/PRD-dispatch-sendmessage-approval.md` (R3, R13, R14, R18 as amended, and the Automated Acceptance Criteria).

The fixed strings the scenarios assert come from the design's Key Interfaces section. The guide URL is `https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md`.

## Acceptance Criteria

### Harness and step definitions

- [ ] The fake `claude`'s `--bg` branch also writes each argv element NUL-separated (for example `printf '%s\0' "$@"`) to a new file `$HOME/dispatch-launch-argv-elements`. The existing `$HOME/dispatch-launch-argv` line and every step that reads it keep working unchanged.
- [ ] New step definitions live in a new file `test/functional/session_message_steps_test.go`, registered from `initializeScenario` through a `registerSessionMessageSteps` function, the way `registerKeepAliveSteps` is. At least these steps exist, with these patterns or ones that differ only in wording:
  - `the niwa machine config global table contains:` (docstring). It adds the given lines to the `[global]` table of `$XDG_CONFIG_HOME/niwa/config.toml` as the scenario environment resolves it, creating the table if it's missing and keeping the registry entries `niwa init` wrote. It never produces a second `[global]` header.
  - `the niwa machine config is replaced with:` (docstring), which writes the file verbatim (for the invalid-TOML fixture), and `the niwa machine config is not readable`, which sets mode 000.
  - `I record the niwa machine config` and `the niwa machine config is byte-for-byte unchanged`.
  - `the launched claude settings document has crossSessionInbound "accept"` and `the launched claude settings document has no crossSessionInbound`. Both read `$HOME/dispatch-launch-argv-elements` and take the element after the only `--settings` element that comes before the `--` prompt separator. The first step parses that element as JSON and checks the key. The second passes when that element parses and lacks the key, or when there's no `--settings` element. Both fail when more than one `--settings` element comes before `--`.
  - `the launched claude settings document has remoteControlAtStartup true`.
  - `the launched claude prompt is "..."`, which asserts that the element right after `--` equals the given text and is the last element.
  - `the session-message notice marker exists` and `the session-message notice marker does not exist`, checked with `os.Lstat` at `$XDG_CONFIG_HOME/niwa/accept-session-messages-notice`.
  - `the dispatch mapping for session "..." records session-message acceptance`, which reads `.niwa/sessions/<id>.json` like `readMappingKeepAlive` and requires `accepts_session_messages: true`. Its counterpart `... does not record session-message acceptance` requires the key to be absent, because the field is omitempty.
  - `the list JSON reports the dispatch instance as accepting session messages` and `... as not accepting session messages`. The second requires the field to be present and `false`.
  - `the error output has exactly (\d+) lines? containing "..."`, which counts matching lines in `s.stderr`. With a count of 0 it's the "no such line" assertion.
  - `the error output contains the text:` and `the error output does not contain the text:` (docstring), for strings that contain double quotes.
  - `I remember the dispatch standard output` and `the dispatch standard output matches the remembered one apart from instance names and session identifiers`. Before comparing, the second step replaces dispatch instance names (the `dispatchInstanceNameRe` shape) and UUIDs or short session ids with placeholders.
  - `the scenario is skipped when tests run as root`, which returns `godog.ErrSkip` when `os.Geteuid() == 0`.
- [ ] The scenarios live in a new feature file, `test/functional/features/session-message-acceptance.feature`. Its description names the design doc and explains that real delivery between live sessions is covered by the PRD's manual delivery check, not here. Every dispatch in it passes `--detach`.

### `@critical` scenarios (one per functional-coverage case)

- [ ] **Off by default.** With no `accept_session_messages_on_dispatch` key and no flag, then again in a second scenario or example row with the key set to `false`, `niwa dispatch <task> --detach` exits 0. The launched claude settings document has no `crossSessionInbound`. The error output has 0 lines containing `accepts messages from other sessions without asking`. The mapping doesn't record session-message acceptance. `niwa list --json` reports the instance as not accepting session messages.
- [ ] **On by flag.** With the key absent, `--accept-session-messages` and, in a second example, `--accept-session-messages=true` each produce a launched claude settings document with crossSessionInbound `"accept"`. The error output has exactly 1 line containing `accepts messages from other sessions without asking`, and that line contains `(source: --accept-session-messages)` and the guide URL. The mapping records session-message acceptance. `niwa list --json` reports the instance as accepting session messages. `niwa list` output contains `(accepts session messages)`.
- [ ] **On by machine setting.** With `accept_session_messages_on_dispatch = true` and no flag, the document has crossSessionInbound `"accept"`. The error output has exactly 1 line containing `accepts messages from other sessions without asking`, and it contains `(source: machine setting accept_session_messages_on_dispatch)` and the guide URL. The error output has 0 lines containing `keeps Claude Code's default for messages from other sessions`. With `--accept-session-messages=true` added, there is still exactly 1 audit line and 0 override lines.
- [ ] **Machine setting on, flag off.** With the key `true` and `--accept-session-messages=false`, the document has no `crossSessionInbound`. There are 0 audit lines, and exactly 1 line that contains both `keeps Claude Code's default for messages from other sessions` and `--accept-session-messages=false`. The mapping doesn't record acceptance. A second example with the key absent and `--accept-session-messages=false` has 0 audit lines and 0 override lines.
- [ ] **One-time explanation and marker.** This one runs under the pty helper with the flag on and no marker present:
  - Before the first dispatch the scenario records the niwa machine config.
  - The first `niwa dispatch <task> --accept-session-messages --detach` under a pty exits 0. Its transcript contains, verbatim through the docstring step, each of these sentences from the design's explanation:
    - the inbound-only sentence ending "dispatching that session again with the behavior on clears it";
    - the interactive-sessions sentence;
    - the `/config` sentence naming `"Messages from your other sessions"` and `"crossSessionInbound": "accept"`;
    - the "applies to every Claude Code session you run" sentence;
    - `niwa won't show this again; it's also at <guide URL>`.
  - After the first dispatch the marker exists and the niwa machine config is byte-for-byte unchanged.
  - A second pty dispatch, which provisions a fresh instance, exits 0, still has exactly 1 audit line, and its transcript doesn't contain `accepting messages without asking is inbound only`.
- [ ] **Explanation without a terminal.** In a companion `@critical` scenario, a non-terminal dispatch (`I run "..." from the workspace root`) with the flag on prints the explanation with `niwa will show this again until it's been shown at a terminal; it's also at <guide URL>`, and the marker doesn't exist afterwards. A following pty dispatch prints the explanation with the "won't show this again" sentence and creates the marker.
- [ ] **Agent that can't receive it.** A Codex dispatch (`niwa dispatch <task> --harness codex --accept-session-messages --detach`, with the fake codex) exits 0. The error output contains the text `niwa dispatch: --accept-session-messages does not apply to the "codex" agent and was ignored.` The codex launch argv doesn't contain `crossSessionInbound`. There are 0 audit lines, and the output doesn't contain `accepting messages without asking is inbound only`. The marker doesn't exist. The mapping doesn't record session-message acceptance.
  - A second scenario or example sets only `accept_session_messages_on_dispatch = true` and adds no flag. It prints no `does not apply to the` warning, no audit line, and no explanation, creates no marker, and doesn't carry the setting.
  - With `--accept-session-messages=false` added to that machine-setting case, it also prints no override line.

### Additional scenarios (not required to be `@critical`)

- [ ] **Configuration sources can't turn it on.** With the machine key absent and no flag, each fixture below dispatches with no `crossSessionInbound` in the launched claude settings document and no audit line:
  - `accept_session_messages_on_dispatch = true` in the workspace's `workspace.toml`, once in its top-level or `[workspace]` table and once in a per-repository table;
  - `crossSessionInbound = "accept"` under `[claude.settings]` in `workspace.toml`;
  - `"crossSessionInbound": "accept"` in the `.claude/settings.json` at the workspace root;
  - the same key in a cloned repository's committed `.claude/settings.json`, and in its `.claude/settings.local.json`.

  For each fixture, the settings files niwa materializes into the dispatch instance don't contain `crossSessionInbound`. Those are the instance-root `.claude/settings.json` and the `.claude/settings.local.json` niwa writes into each repository, but not a file the fixture itself committed. An unknown-key warning from `workspace.toml` is allowed, and the dispatch still exits 0. The repository fixtures reuse the existing `a source repo "..." exists with the staged files` and `a staged file "..." with body:` steps from `codex_agent_steps_test.go`.
- [ ] **Coexistence with other launch configuration.** The machine config sets `remote_control_on_dispatch = true`, `keep_alive_on_dispatch = true`, and `accept_session_messages_on_dispatch = true`, the workspace declares a bypass posture, and `ANTHROPIC_API_KEY` is removed from the scenario environment (extend `buildEnv`'s filter through `envOverrides` or a new field if needed). The recorded argv has exactly one `--settings` element before `--`, and that document has both crossSessionInbound `"accept"` and remoteControlAtStartup `true`. The launched claude was invoked with `--permission-mode bypassPermissions`, the prompt contains the keep-alive arming instruction, and the mapping records keep-alive.
- [ ] **A prompt beginning with `--settings=`.** With the flag on, the scenario dispatches the prompt `--settings={}`. It goes through niwa's own `--` (`niwa dispatch --accept-session-messages --detach -- --settings={}`), or through the pty capture if dispatch doesn't accept a positional prompt after `--`. The launched claude prompt is `--settings={}`, and the launched claude settings document (the one before `--`) has crossSessionInbound `"accept"`. There is exactly 1 audit line, and the mapping records session-message acceptance.
- [ ] **Launch failure.** With the flag on and `a fake claude for dispatch that fails to launch`, dispatch exits non-zero. There are 0 audit lines, the output doesn't contain `accepting messages without asking is inbound only`, the marker doesn't exist, and no dispatch-origin mapping remains.
- [ ] **Stdout unchanged.** In a non-terminal scenario, the stdout of a dispatch with the flag on matches the stdout of the same dispatch with it off, apart from instance names and session identifiers. That covers the printed resume commands.
- [ ] **Unwritable configuration directory.** The scenario starts with `the scenario is skipped when tests run as root`. After `niwa init`, it makes `$XDG_CONFIG_HOME/niwa` mode 0555 and restores it in cleanup. A pty dispatch with the flag on exits 0, prints the explanation, and leaves no marker. A second pty dispatch prints the explanation again.
- [ ] **Unreadable machine configuration.** Each fixture is dispatched without the flag and then with `--accept-session-messages`. Without the flag the behavior stays off with no audit line. With the flag the document has crossSessionInbound `"accept"` and exactly 1 audit line naming `(source: --accept-session-messages)`. The fixtures:
  - a `config.toml` with mode 000, preceded by `the scenario is skipped when tests run as root`;
  - a `config.toml` that isn't valid TOML;
  - `accept_session_messages_on_dispatch = "yes"`.
- [ ] **Personal settings untouched.** With the flag on and `$HOME/.claude/settings.json` present, the file's contents and modification time are unchanged after a dispatch. With the file at mode 000 (preceded by `the scenario is skipped when tests run as root`), the dispatch exits 0 and the error output doesn't contain `settings.json`.

### Watch-site unit test

- [ ] A unit test in `internal/cli` (in `watch_test.go` or a new `watch_inbound_test.go`) covers both `niwa watch` launch sites:
  - Setup: point `XDG_CONFIG_HOME` at a `t.TempDir()` whose `niwa/config.toml` sets `[global] accept_session_messages_on_dispatch = true`, and also set the dispatch flag variable to on and restore it afterwards.
  - It replaces `dispatchLaunch` with a stub that captures each `launchRequest` and restores it in `t.Cleanup`.
  - It drives both real launch sites: the fresh-stage launch in `stageReview` (near `watch.go:844`) and the resume launch in `continueReview` (near `watch.go:581`).
  - It asserts that the stub was called once per site, and that no element of either captured `Passthrough` and nothing in either `Body` contains `crossSessionInbound`.
- [ ] If either function can't be driven in a unit test as it stands, the change adds only behavior-preserving seams: package-level function variables for the GitHub, capture, stop, or settings calls around the launch, following `stopSessionFunc` and `dispatchLaunch`. It doesn't copy the passthrough-building code into the test, so the test fails if either real site starts adding the key. Existing watch tests pass unchanged.

### Suite health

- [ ] Existing scenarios in `dispatch.feature`, `keep-alive.feature`, and `codex-agent.feature` pass. Any argv expectation that changes because Claude's prompt now follows `--` was already updated by the issue that introduced the separator and isn't reworked here.
- [ ] `make test-functional-critical` passes with the new `@critical` scenarios included. `make test-functional`, `go test ./...`, and `go vet ./...` pass, and every changed Go file is gofmt-clean.

## Dependencies

Blocked by <<ISSUE:1>>, <<ISSUE:4>>, <<ISSUE:5>>, <<ISSUE:6>>

## Downstream Dependencies

None
