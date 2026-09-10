---
complexity: simple
complexity_rationale: It adds one guide and one index line and changes no code; the manual delivery check is careful work, but its results only set what the guide states and what the PR description records.
---

## Goal

Write `docs/guides/session-message-acceptance.md` covering everything the PRD's documentation requirement lists, add it to the contributor-guide index, check the flag's help and completion, and run the manual delivery check whose results set the guide's Claude Code version line and its description of who can send.

## Context

Design: `docs/designs/DESIGN-dispatch-sendmessage-approval.md`

Every audit line and every one-time explanation that `niwa dispatch` prints ends with the guide URL `https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md`. Issue <<ISSUE:4>> fixes it as the `inboundGuideURL` constant. So the guide has to exist at exactly that path, and it's the only place a developer can read the explanation again after the marker suppresses it. The PRD's documentation requirement (R17) lists what the guide must cover, and asks for the same top-level headings as `docs/guides/session-keep-alive.md` so the two opt-in dispatch behaviors read alike. `docs/guides/remote-control-on-dispatch.md` is the other sibling guide for a host-level dispatch preference, and is useful for tone and for how it describes the `[global]` table.

The fake `claude` the functional tests use can't show real delivery between two live Claude Code sessions. So the PRD's goals that a message is delivered, and still delivered after a restart or reopen, are verified only by the PRD's manual delivery check. Phase 6 of the design adds five more measurements to the same session. Two of them decide what the guide says. The first is whether a process that isn't Claude Code can send over Claude Code's local unix-socket inbox: that sets how wide the guide says the sender set is. The second is the tool-list comparison: it confirms the denied list (`SendMessage`, `SendFile`, `RemoteTrigger`, `ListAgents`, taken from Claude Code 2.1.267) that <<ISSUE:1>> puts into review sessions still covers every tool that reaches another session.

The guide also carries the Security Considerations material a developer needs before turning the behavior on: the recommended posture, how to withdraw a grant, the routes by which a workspace can change which machine configuration a nested dispatch reads, and the limits of the review-session deny hook. The texts it quotes come from the blocking issues: the audit, override, and warning lines from <<ISSUE:4>>, the `niwa list` marker and field from <<ISSUE:5>>, the explanation and marker from <<ISSUE:6>>, and the deny hook's tool list and limits from <<ISSUE:1>>.

## Acceptance Criteria

- [ ] `docs/guides/session-message-acceptance.md` exists, and `inboundGuideURL` in `internal/cli/dispatch_inbound.go` equals `https://github.com/tsukumogami/niwa/blob/main/docs/guides/session-message-acceptance.md`, so the path the URL names is the file this issue adds.
- [ ] The guide's `##` headings match the keep-alive guide's five top-level headings one for one and in the same order. `## Opting in`, `## How it works`, and `## Validation` are verbatim. The two headings specific to keep-alive are adapted to this feature: the one about seeing which instances accept messages takes the place of "Seeing what is kept alive", and the one about withdrawing the grant takes the place of "Releasing keep-alive: close, don't archive". There are no other `##` headings; everything else sits under `###` headings inside them.
- [ ] The guide covers each item R17 lists:
  - [ ] the `[global] accept_session_messages_on_dispatch` machine key in `config.toml` (under `~/.config/niwa/`, or `$XDG_CONFIG_HOME/niwa/` when set), the tri-state `--accept-session-messages[=true|false]` flag, and the precedence: flag, then machine setting, then off. It also says no workspace config, instance setting, `[claude.settings]` entry, or settings file a repository carries can turn the behavior on or off, and that there's no `niwa config set` for the key;
  - [ ] the audit line, with both source spellings (`machine setting accept_session_messages_on_dispatch` and `--accept-session-messages`), and the override line, quoted as `niwa dispatch` prints them;
  - [ ] the `(accepts session messages)` marker in `niwa list` and the always-present `accepts_session_messages` field in `niwa list --json`, including that it stays `true` after the session finishes and that it means niwa launched the worker with the setting, not that Claude Code confirmed it;
  - [ ] that the behavior is inbound only: a worker's message into a session launched without it still waits there when the two are in different permission-mode classes, and dispatching that session again with the behavior on clears it;
  - [ ] that `--accept-session-messages=false` keeps Claude Code's default, which delivers messages between sessions of the same class, so it doesn't isolate a worker;
  - [ ] that sessions `niwa watch` launches or resumes never receive the behavior;
  - [ ] the behavior for agents that can't receive it: for Codex, the flag prints the warning and is ignored, the machine setting alone prints nothing, and in both cases the behavior doesn't take effect;
  - [ ] that turning the machine setting off doesn't reach sessions already dispatched, because Claude Code reapplies their launch settings on `claude respawn` and `claude attach`;
  - [ ] that a worker stopped with `claude stop` receives nothing, because the send fails, until something reopens it;
  - [ ] that a foreground `claude --resume` of a dispatched conversation doesn't carry the behavior;
  - [ ] the marker path, `accept-session-messages-notice` beside `config.toml`; that deleting it shows the explanation again and creating it by hand suppresses it; that it's created only when a terminal showed the explanation; and that a developer who only dispatches through agents sees the explanation repeatedly until then;
  - [ ] the step for the developer's own interactive sessions: the "Messages from your other sessions" row in Claude Code's `/config`, or `"crossSessionInbound": "accept"` in `~/.claude/settings.json`. It gives every cost R10 names: the change applies to every Claude Code session the developer runs, and to messages from any session able to reach theirs, on this machine or elsewhere. It also says niwa never makes the change;
  - [ ] the Claude Code version the manual delivery check last passed on, stated as a version line under `## Validation`.
- [ ] The guide covers the design's Security Considerations:
  - [ ] the recommended posture: turn the behavior on only on machines where every session sharing the Claude Code account is one the developer would let direct a bypass-mode worker, and prefer the per-dispatch flag to the machine key when only some fan-outs need it;
  - [ ] withdrawing the grant: list the instances reporting `"accepts_session_messages": true` with `niwa list --json` and stop their sessions, since turning the key off doesn't reach them;
  - [ ] the two relocation routes: a workspace's `[claude.env]` or `[session.env]`, or an overlay's `[env]`, setting `XDG_CONFIG_HOME` or `HOME`, which changes which `config.toml` a nested `niwa dispatch` reads. Relocating `HOME` also moves the Claude Code user settings a nested session reads, and can grant acceptance with no audit line or record;
  - [ ] that any agent can pass the flag, so a steered worker can dispatch more accepting workers;
  - [ ] the review-session deny hook: the denied list and the Claude Code version it was taken from; that with `watch_sandbox = off` it's accident prevention only; that in sandbox mode, Bash is kept off the local inbox because niwa's no-egress stanza leaves unix sockets disallowed; that `disableAllHooks` in any settings source turns it off along with every other review hook; and that a review staged before the upgrade runs without the hook until watch re-stages it, so in-flight reviews should re-stage before the behavior is turned on.
- [ ] The guide describes who can send according to the inbox measurement. If a process that isn't Claude Code could send over the local inbox, the guide says any local process running as the developer can reach an accepting worker, not only sessions on the account. If it couldn't, the guide says the sender set is sessions able to address the worker by name across the developer's whole Claude Code account, including other machines and the cloud. Either way it notes that worker names are predictable, because niwa uses the dispatch name as the display name.
- [ ] `CLAUDE.md`'s Contributor Guides list gains a `docs/guides/session-message-acceptance.md — ...` entry in the same form as its neighbors, placed right after the `session-keep-alive.md` entry. The entry names the key, the flag, the audit and `niwa list` surfaces, and the manual check.
- [ ] `niwa dispatch --help`, run against a freshly built binary, lists `--accept-session-messages` with the help text the design fixes ("accept messages from other Claude Code sessions without an approval prompt; overrides the [global] accept_session_messages_on_dispatch machine setting in either direction"). `niwa __complete dispatch --acc` offers `--accept-session-messages`. If either is missing, the registration from <<ISSUE:4>> is fixed in this PR.
- [ ] The PRD's manual delivery check is run with its preconditions: Claude Code version recorded, no `crossSessionInbound` or bypass default in the tester's user settings, no managed settings file, a workspace declaring `permissions = "bypass"`, and a sender started with `claude --bg` and no permission-mode flag. Each case gets the outcome the PRD expects: case 0 held, case 0b held after respawn and after stop and reopen, case 1 delivered, case 2 delivered after `claude respawn`, case 2b delivered after `claude stop` and reopen, case 2c send fails, case 3 held in the sender, case 4 delivered. A case with any other outcome blocks the merge.
- [ ] The same session records the extra measurements from the design's Phase 6:
  - [ ] a detached `claude --bg ... -- <prompt>` launch, the argv shape <<ISSUE:3>> introduces, starts normally and takes the prompt as its task;
  - [ ] the permission class a review session in the hard-deny posture actually runs in;
  - [ ] whether a review session's subagents are blocked by the session-reach deny hook. If they aren't, the guide states it as a limit of the hook;
  - [ ] whether a process that isn't Claude Code can send over the local inbox; this sets the sender-set wording above;
  - [ ] Claude Code's tool list, taken from the init event of `claude -p --output-format stream-json`, compared against `SendMessage|SendFile|RemoteTrigger|ListAgents`. If a tool that reaches another session is missing from the denied list, the matcher from <<ISSUE:1>> is updated in this PR and the guide's list matches it.
- [ ] The manual check's case results, the Claude Code version, and the extra measurements are recorded in the PR description. They aren't committed in any file under a temporary directory. The guide carries only the version line, the denied list, and the conclusions the measurements set.
- [ ] The guide follows the repository's public-docs rules: no links to private resources, no internal tooling names, no emojis, and no reference to any temporary workflow path.

## Dependencies

Blocked by <<ISSUE:1>>, <<ISSUE:4>>, <<ISSUE:5>>, <<ISSUE:6>>

## Downstream Dependencies

None
