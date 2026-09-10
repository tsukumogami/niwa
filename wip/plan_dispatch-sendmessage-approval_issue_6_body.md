---
complexity: testable
complexity_rationale: One helper and two call sites with no new authority. The marker only suppresses a message, but the timing around the attach, the terminal check, exclusive create, symlink handling, and error tolerance each need unit tests to pin them.
---

## Goal

On the first dispatch where session-message acceptance takes effect, print the one-time explanation to stderr once the developer can read it. Remember that it was shown by creating an empty marker file beside `config.toml`, and only when stderr is a terminal.

## Context

When a worker accepts messages from other sessions without asking, that only covers what the worker receives. Replies into sessions launched without the behavior still wait there, and that includes the developer's own interactive sessions. The PRD requires niwa to explain this once, with the step and the cost of extending it to the developer's own sessions (R10). It also requires the explanation to be suppressed afterwards by a marker file named `accept-session-messages-notice` in niwa's configuration directory (R11). The marker is created only when a terminal showed the explanation, so an agent that dispatches first can't use it up unseen. niwa never rewrites `config.toml` to record it, and a failure to create the marker never fails the dispatch.

<<ISSUE:4>> introduces `inboundApplied`, the one boolean that says the behavior is on, deliverable, and in the launch settings document. It also adds the stderr audit line written between step 12 (rollback disarmed) and step 13 (stdout hints) of `runDispatch`. This issue hangs the explanation off that same boolean, so the explanation, the audit line, and the session record can't disagree.

The design fixes these points:

- **Where the marker lives.** The directory part of `config.GlobalConfigPath()` (`internal/config/registry.go`), which is `$XDG_CONFIG_HOME/niwa/` or `~/.config/niwa/`. `config.GlobalConfigDir()` is not used, because it returns the overlay clone directory `.../niwa/global`. If `GlobalConfigPath()` returns an error, niwa prints the explanation with its non-terminal closing sentence and creates no marker.
- **How presence is checked.** The marker counts as present when `os.Lstat` on its path succeeds. Any error, including a permission error on an unsearchable directory, counts as absent. A dangling symlink therefore counts as present, which matches what an exclusive create would find.
- **How the marker is created.** Only when `IsStderrTTY()` (`internal/cli/prompt.go`) reports a terminal. niwa runs `os.MkdirAll(dir, 0o755)`, the mode `writeGlobalConfigFile` already uses for this directory, then `os.OpenFile(path, O_CREATE|O_EXCL|O_WRONLY, 0o600)` and closes the file without writing to it. "Already exists" means a concurrent dispatch won the race. Every error from either call is ignored, because the dispatch has already succeeded, and the explanation simply prints again next time.
- **When it prints.** For Claude, a dispatch from a terminal without `--detach` hands the terminal to `claude attach` right after the launch, so anything printed at the audit-line point scrolls away under the attached session. When an attach follows, niwa prints the explanation and creates the marker only after `dispatchAttach` returns, on both its success and failure branches. When no attach follows (`--detach`, an agent whose spec has `ResumeDuringTurn` false, or a foreground launch), it prints right after the audit line. These are the same conditions step 14 already branches on.

The helper is `showInboundExplanation(w io.Writer, dir string, dirErr error, isTTY func() bool)` in `internal/cli/dispatch_inbound.go`, the file <<ISSUE:4>> creates. Production passes `cmd.ErrOrStderr()`, `filepath.Dir` of `config.GlobalConfigPath()` along with that call's error, and `IsStderrTTY`. The explanation is one stderr line. Its text is fixed in the design's Key Interfaces section and uses `inboundGuideURL` from <<ISSUE:4>>:

> `niwa dispatch: note: accepting messages without asking is inbound only. A message this worker sends into a session launched without it, such as a coordinator dispatched earlier, one dispatched with the behavior off, or one another tool started, still waits for approval there when the two run in different permission modes; dispatching that session again with the behavior on clears it. Your own interactive Claude Code sessions are one such case, and they are governed by your Claude Code user settings, which niwa doesn't change. To accept there too, set "Messages from your other sessions" to accept in Claude Code's /config, or add "crossSessionInbound": "accept" to ~/.claude/settings.json. That change applies to every Claude Code session you run and to messages from any session able to reach yours, on this machine or elsewhere. niwa won't show this again; it's also at <guide URL>`

When stderr isn't a terminal, or the configuration directory can't be resolved, the last sentence is instead `niwa will show this again until it's been shown at a terminal; it's also at <guide URL>`.

Design: `docs/designs/DESIGN-dispatch-sendmessage-approval.md` (Decision 3; Solution Architecture > Key Interfaces; Data Flow steps 6 and 8; Implementation Approach > Phase 5). Upstream requirements: `docs/prds/PRD-dispatch-sendmessage-approval.md` (R10, R11, R16).

## Acceptance Criteria

Helper and text:

- [ ] `internal/cli/dispatch_inbound.go` declares a constant `inboundNoticeMarker = "accept-session-messages-notice"` and the helper `showInboundExplanation(w io.Writer, dir string, dirErr error, isTTY func() bool)`. The helper returns no error, so nothing it does can fail a dispatch.
- [ ] The explanation text is held as named constants in `dispatch_inbound.go`: a shared body, the terminal closing sentence, and the non-terminal closing sentence. Together they reproduce the design's Key Interfaces text exactly, with `inboundGuideURL` substituted for `<guide URL>`. The helper writes it as one line starting `niwa dispatch: note: ` and ending in a newline.
- [ ] A unit test asserts that the printed line contains each of these verbatim: `accepting messages without asking is inbound only`; `dispatching that session again with the behavior on clears it`; `Your own interactive Claude Code sessions are one such case, and they are governed by your Claude Code user settings, which niwa doesn't change`; `"Messages from your other sessions"`; `"crossSessionInbound": "accept"`; `~/.claude/settings.json`; `That change applies to every Claude Code session you run and to messages from any session able to reach yours, on this machine or elsewhere`; and the guide URL. It also asserts the line contains exactly one of the two closing sentences.
- [ ] No path this issue adds opens, stats, or writes anything under `~/.claude/`, and nothing reads or writes `config.toml`.

Helper behavior. All of these are unit tests against `t.TempDir()` that pass `isTTY` as a stub, and none of them touch the real `IsStderrTTY`:

- [ ] Terminal path: with no marker and `isTTY` returning true, the helper prints the explanation ending in `niwa won't show this again; it's also at <guide URL>`. Afterwards `<dir>/accept-session-messages-notice` exists as a regular, empty file whose `Mode().Perm()` is `0o600`. A second call with the same arguments prints nothing.
- [ ] Non-terminal path: with no marker and `isTTY` returning false, the helper prints the explanation ending in `niwa will show this again until it's been shown at a terminal; it's also at <guide URL>` and creates nothing in `dir`. A second non-terminal call prints it again. A later terminal call prints the terminal version and creates the marker.
- [ ] Marker already present: with an existing marker file, the helper prints nothing, whatever `isTTY` returns, and the file is not modified.
- [ ] Dangling symlink: when the marker path is a symlink to a nonexistent target, the helper prints nothing, and it doesn't create the target even with `isTTY` returning true. The same holds for a symlink pointing at an existing file, and that file's contents are unchanged.
- [ ] Missing directory: with `dir` set to a path under `t.TempDir()` that doesn't exist yet (two levels deep) and `isTTY` returning true, the helper prints the terminal explanation, creates the directory, and creates the marker in it.
- [ ] Unwritable directory: with `dir` at mode `0o500` and `isTTY` returning true, the helper prints the terminal explanation, doesn't panic, and creates no marker. A second call prints the explanation again. The test is skipped when `os.Geteuid() == 0` and restores the mode in cleanup.
- [ ] Unsearchable directory: with `dir` at mode `0o000`, where `os.Lstat` fails with a permission error, the helper treats the marker as absent and prints the explanation without panicking. Skipped as root.
- [ ] Unresolvable configuration path: with `dirErr` non-nil, the helper prints the explanation with the non-terminal closing sentence even when `isTTY` returns true, and creates nothing. The test stubs `isTTY` to record whether it was called and asserts that it wasn't.
- [ ] Concurrent first calls: 8 goroutines calling the helper at once against the same missing marker, all with `isTTY` returning true, all return. Afterwards exactly one marker exists, empty and at mode `0o600`, and at least one goroutine's writer received the explanation. The test passes under `go test -race`.

Wiring in `runDispatch`. These tests drive `runDispatch` with the existing stubs for launch, capture, and `dispatchAttach`. They stub `IsStderrTTY` with save and restore, as `dispatch_prompt_test.go` does, and set `XDG_CONFIG_HOME` to a `t.TempDir()`:

- [ ] The explanation is shown only when `inboundApplied` is true. A dispatch where the behavior is off (no key and no flag, or the key `true` with `--accept-session-messages=false`) prints no explanation and creates no marker. So does a dispatch where the behavior isn't deliverable, such as a Codex dispatch with the flag.
- [ ] Detached path: with `--detach`, the behavior on, and `IsStderrTTY` stubbed true, the explanation is the stderr line directly after the audit line. The marker exists under `$XDG_CONFIG_HOME/niwa/` when `runDispatch` returns, and `dispatchAttach` is not called.
- [ ] Foreground path: a foreground launch, driven the way `dispatch_launchmode_test.go` drives one, prints the explanation directly after the audit line and before step 14's "the turn ended" line.
- [ ] Attach path, success: without `--detach`, with the behavior on and `IsStderrTTY` stubbed true, a `dispatchAttach` stub captures the stderr buffer and runs `os.Lstat` on the marker path at the moment it's called. At that moment stderr holds the audit line but no explanation, and the marker doesn't exist. After `runDispatch` returns, stderr ends with the explanation and the marker exists.
- [ ] Attach path, failure: the same setup with a `dispatchAttach` stub that returns an error. The existing `niwa: warning: could not attach to session` and `niwa: the session is running; attach later with:` lines still print, unchanged. The explanation prints after the attach call returns, and the marker exists when `runDispatch` returns. `runDispatch` still returns nil.
- [ ] Non-terminal dispatch: with `IsStderrTTY` stubbed false and `--detach`, the dispatch prints the non-terminal explanation and creates nothing under `$XDG_CONFIG_HOME/niwa/` beyond what the test itself placed there.
- [ ] `XDG_CONFIG_HOME` is honored: with it set, the marker is created at `$XDG_CONFIG_HOME/niwa/accept-session-messages-notice`, and nothing is created under `$HOME/.config/niwa/` (the test points `HOME` at a separate `t.TempDir()`). With `XDG_CONFIG_HOME` unset and `HOME` set, the marker is created under `$HOME/.config/niwa/`.
- [ ] Missing configuration directory at dispatch: with `XDG_CONFIG_HOME` pointing at an empty temp directory (so `niwa/` doesn't exist), `--accept-session-messages`, `--detach`, and `IsStderrTTY` stubbed true, the dispatch succeeds and creates both `$XDG_CONFIG_HOME/niwa/` and the marker.
- [ ] Failed launch: when the launch stub returns an error, when capture fails, or when `WriteSessionMapping` fails, all with the behavior on and `IsStderrTTY` stubbed true, stderr contains no explanation and no marker is created.
- [ ] Marker creation never fails a dispatch: with `$XDG_CONFIG_HOME/niwa/` at mode `0o500` and `IsStderrTTY` stubbed true, the dispatch returns nil and prints the explanation, and a second dispatch prints it again. Skipped as root.
- [ ] `config.toml` is unchanged: a test writes `$XDG_CONFIG_HOME/niwa/config.toml` with a comment line and `accept_session_messages_on_dispatch = true` under `[global]`, runs a dispatch that prints the explanation and creates the marker, and asserts that the file's bytes and modification time are the same afterwards.
- [ ] Stdout is unchanged: for the same stubbed dispatch, stdout has no explanation text, whether `IsStderrTTY` is stubbed true or false.
- [ ] Tests that stub `IsStderrTTY` or `dispatchAttach`, or that set `XDG_CONFIG_HOME`, restore them in `t.Cleanup`. The existing tests in `dispatch_contract_test.go`, `dispatch_launchmode_test.go`, and `dispatch_prompt_test.go` pass unchanged.
- [ ] `dispatch_inbound.go` stays in `dispatchPathFiles` in `internal/cli/dispatch_layout_test.go`, and the layout scan passes. The helper names no agent.
- [ ] `go test ./...` and `go vet ./...` pass, and the changed files are gofmt-clean.

Downstream deliverables:

- [ ] Must deliver: a built `niwa` binary that, on a Claude dispatch where the behavior takes effect, writes the explanation to stderr with the terminal or non-terminal closing sentence exactly as the design's Key Interfaces section gives it. At a terminal it creates the empty file `$XDG_CONFIG_HOME/niwa/accept-session-messages-notice`, and it leaves `config.toml` byte-unchanged. With `--detach`, the explanation comes right after the audit line, so the functional scenarios that run under the `script` pty helper can see it in the combined transcript (required by <<ISSUE:7>>)
- [ ] Must deliver: the final marker file name `accept-session-messages-notice`, its location beside `config.toml` (the directory of `config.GlobalConfigPath()`, following `XDG_CONFIG_HOME`), and the rule that the explanation shows whenever the marker is absent. Deleting the marker shows the explanation again, and creating it by hand (even as an empty file or a symlink) suppresses it. The verbatim explanation text lives in named constants the guide can quote (required by <<ISSUE:8>>)

## Dependencies

Blocked by <<ISSUE:4>>

## Downstream Dependencies

<<ISSUE:7>> (test(functional)) runs the built binary. It needs the explanation to come right after the audit line on a `--detach` dispatch, because its terminal scenarios run dispatch with `--detach` under the `script` pty helper and read the merged transcript. It asserts the marker exists, or doesn't, at `$XDG_CONFIG_HOME/niwa/accept-session-messages-notice`, that a second dispatch prints nothing, that a non-terminal dispatch prints the "will show this again" sentence and creates no marker, that four parallel dispatches all succeed, and that `config.toml` is byte-unchanged. So the file name, its location, and both closing sentences must be final here.

<<ISSUE:8>> (docs(guide)) documents the marker's path under `$XDG_CONFIG_HOME/niwa/` or `~/.config/niwa/`, that deleting it shows the explanation again and creating it by hand suppresses it, and that the marker is created only after a terminal showed the explanation. That includes the attach case, where it's created only when the developer returns from `claude attach`. The guide quotes the explanation's content, so it needs the text to be fixed in the constants this issue adds.
