# Lead

Can one host-level default permission key serve both Claude and Codex dispatch, given their different vocabularies?

## Findings

### How niwa spells the permission intent per agent today

- `LaunchFlags.PermissionMode` is one intent with a per-agent spelling (`internal/agentplan/dispatch.go:253-263`). Claude spells it `--permission-mode` (`dispatch.go:387`); Codex spells it `--sandbox` (`dispatch.go:459`). The comment at `dispatch.go:455-458` says niwa passes nothing on the Codex flag itself. The spelling exists only so that a posture a developer asks for deliberately has somewhere to go.
- Codex workers get their write posture from `WorkdirGrantArgs`, a per-invocation trust override: `-c projects={"<dir>"={trust_level="trusted"}}` (`dispatch.go:448-452`, field doc `dispatch.go:308-322`). DESIGN-codex-background-dispatch.md Decision 9 (lines ~906-1003) records the measurements. A plain `codex exec` at an untrusted root lands `approval: never, sandbox: read-only`, and with the grant it lands `sandbox: workspace-write [workdir, /tmp, $TMPDIR]`. Passing an elevated `--sandbox` (or `-c sandbox_mode=...`, or `--dangerously-bypass-approvals-and-sandbox`) makes Codex append a `[projects."<cwd>"] trust_level="trusted"` stanza to the developer's own `~/.codex/config.toml`. That side effect is why niwa never passes `--sandbox` itself.
- The grant comes before the pass-through flags in the argv, so a developer's explicit `--sandbox` gets the last word (DESIGN-codex-background-dispatch.md ~line 968-972). DESIGN-codex-dispatch-posture-persistence.md (~lines 476-486) measured that `--sandbox read-only` appended after the grant wins. Note that `codex exec resume` has no sandbox flag at all.

### What an explicit `--permission-mode` does for a Codex dispatch today

- `niwa dispatch --permission-mode X` is registered as "permission mode to forward to the background worker; dropped for an agent that has no such flag" (`internal/cli/dispatch.go:31`).
- `derivePermissionMode` returns an explicit value unchanged (`internal/cli/dispatch.go:1237-1240`). `buildDispatchPassthrough` then pairs it with `flags.PermissionMode` (`dispatch.go:1267-1283`). **For Codex the value goes out raw as `--sandbox X`.** There's no validation and no translation.
  - `--permission-mode bypassPermissions` on a Codex dispatch becomes `codex exec ... --sandbox bypassPermissions`, which Codex rejects. Its accepted values are `read-only | workspace-write | danger-full-access`.
  - `--permission-mode workspace-write` works, but it also triggers the trust-stanza write that Decision 9 exists to avoid. The grant already yields workspace-write, so that request is redundant and harmful.
  - `--permission-mode danger-full-access` works, and it writes a trust stanza too.
- The workspace-posture derivation is gated on the literal string `flags.PermissionMode == "--permission-mode"` (`dispatch.go:1241`). The rationale is at `dispatch.go:585-594` and DESIGN-dispatch-permission-mode.md DD3 (~lines 118-148, 270-279): forwarding "bypassPermissions" to Codex "would be wrong rather than merely unhelpful". So Codex never gets a derived value. Only the explicit flag reaches it, and it arrives raw.

### Codex's vocabulary (external)

Source: https://learn.chatgpt.com/docs/agent-approvals-security (redirected from developers.openai.com/codex/agent-approvals-security), and https://learn.chatgpt.com/docs/cli/reference.

- **Sandbox** (`sandbox_mode` / `--sandbox`, `-s`) has three values. `read-only`; `workspace-write`, which keeps network off unless `[sandbox_workspace_write] network_access = true` and keeps `.git`, `.agents` and `.codex` read-only; and `danger-full-access`, which means no sandbox.
- **Approval** (`approval_policy` / `-a`, `--ask-for-approval`) values:
  - `on-request`
  - `never`
  - granular `{ granular = { sandbox_approval, rules, mcp_elicitations, request_permissions, skill_approval } }`
  - `untrusted`, which is now retired: "can stop the client from starting"
  - `on-failure`, which the current docs don't list
- **Presets**:
  - The "Auto" preset is the interactive default: `--sandbox workspace-write --ask-for-approval on-request`.
  - `--dangerously-bypass-approvals-and-sandbox` (alias `--yolo`) means no sandbox and no approvals.
  - `--full-auto` is deprecated on `codex exec`.
- **On `codex exec`**: the CLI reference's exec table lists `--sandbox`, `--full-auto` (deprecated), `--yolo` and `-c`, but not `-a`. niwa measured on 0.147.0 that `-a` and `--full-auto` don't exist on exec (DESIGN-codex-background-dispatch.md ~line 899-904), and that exec already lands `approval: never` by default.
- **Closest analog to Claude `auto` mode**: `approvals_reviewer = "auto_review"` (the "guardian"), settable as `-c approvals_reviewer=auto_review`. A reviewer model decides approval requests: it allows low and medium risk, denies critical risk, needs user authorization for high risk, and fails closed. It only applies when approvals are interactive (`on-request` or granular). That doesn't fit `codex exec`, where approval is effectively `never`. So for a headless Codex worker the nearest "auto" is just the current default: workspace-write sandbox with no prompts.

### How niwa already models per-agent differences

- **Posture vocabulary (workspace level)**:
  - `[session.posture]` uses a niwa-owned, agent-neutral vocabulary with two separate fields. Approvals are `on-untrusted|on-failure|on-request|never`; sandbox is `read-only|workspace-write|full-access` (`internal/config/session.go:31-64`).
  - `internal/agentplan/posture.go:67-95` maps these onto Codex keys, and the map doubles as the accepted-value set (closed vocabulary, refuse unknowns, `validateSessionPosture` at `posture.go:105-121`).
  - The file comment (`posture.go:32-40`) and the session.go comment (`session.go:36-48`) record the bypassPermissions asymmetry. Codex's fullest suppression (`never` + `danger-full-access`) also switches off both the filesystem and network sandboxes; Claude's bypassPermissions relaxes approvals with no sandbox dimension. niwa therefore never derives a sandbox value from an approval declaration.
- **Capability contract**: the declaration table plus `agentplan.Lookup` (DESIGN-agent-capability-contract.md, row 12 "Approval/sandbox posture" at ~line 407; discussion ~lines 465-512). Each agent's row says I / U(reason), and launch data lives in per-agent `LaunchSpec` rows. An intent with no flag is dropped rather than guessed at (`dispatch.go:253-257`).
- **Host-default precedent: `[global] dispatch_model`** (`internal/config/registry.go:69-75`) is one host-level key serving both agents. It takes a niwa-portable vocabulary (`fast|balanced|powerful`, `dispatch.go:240-250`) that each `LaunchSpec.ModelCategories` binds per agent. Known vendor names pass through, and an unknown value is forwarded raw with a warning (`internal/cli/dispatch_model.go:10-49`). The explicit `--model` overrides it (`dispatch.go:576-580`). This is the closest existing pattern for what the lead asks.

### Weighing the options

- **(a) Per-harness keys** (e.g. `dispatch_permission_mode_claude`, `dispatch_sandbox_codex`): simple and honest about the vocabularies. But it multiplies keys per agent, breaks the "one intent, per-agent spelling" model of `LaunchFlags`, and has no precedent in `[global]`.
- **(b) A niwa-owned vocabulary mapped per agent** (e.g. `ask|auto|bypass`): matches both the `dispatch_model` categories and `[session.posture]`. Each `LaunchSpec` would carry a map from niwa word to agent value, or to "no flag" (drop).
  - The hard part is Codex's mapping. `bypass` -> `danger-full-access` would kill the sandbox as a side effect, which is exactly what `posture.go:32-40` forbids. It would also write trust stanzas.
  - The honest mapping for Codex is probably "drop", leaving the grant's workspace-write in charge, for every word except maybe `ask`. Even `ask` has no exec equivalent.
  - So for Codex the key would mostly be a no-op. That's acceptable under the drop-rather-than-guess rule, but it needs to be reported so nobody assumes it took effect.
- **(c) Raw passthrough** of the agent's own value: this is what explicit `--permission-mode` already does. As a host default it's actively dangerous with mixed agents. A host set to `bypassPermissions` would turn every Codex dispatch into `--sandbox bypassPermissions`, and the launch would fail. Raw only works if it's scoped to one agent.
- **(d) A table keyed by harness** (e.g. `[global.dispatch_permission_mode] claude = "auto"; codex = "..."`): this is (a) structured as one key. It's explicit and avoids mapping errors, and it suits the "same workspace config, different host" goal, since the host owner knows which agents they run. It's still agent-vocabulary leakage into host config, but bounded. It could combine with (b)'s validation: each agent's accepted set comes from its `LaunchSpec`.

## Implications

- One key with a niwa vocabulary is workable and has precedent in `dispatch_model`. But the vocabulary has to be about approvals only, and Codex should map to "forward nothing" for any value that would need `--sandbox` elevation. Otherwise the feature reintroduces both the trust-stanza write and the sandbox-off side effect that two designs worked to rule out.
- The motivating case, a host that refuses `bypassPermissions` and needs `auto`, is purely Claude-side. Codex's analog (`auto_review`) doesn't apply to headless `codex exec`. A Claude-only scope, plus "Codex: not applicable, nothing forwarded" in the capability table, may be the honest answer for Codex in v1. The key could still be named neutrally so a Codex mapping can be added later.
- Whatever is chosen, the explicit `--permission-mode` raw passthrough to Codex `--sandbox` should be revisited. As a CLI flag it's survivable, because a human typed it for one launch. As a host default applied to every dispatch it isn't.
- The literal-string gate `flags.PermissionMode == "--permission-mode"` (`dispatch.go:1241`) is a weak substitute for a capability declaration. A per-agent permission vocabulary on `LaunchSpec`, like `ModelCategories`, would replace it cleanly and fit the capability-contract pattern.

## Surprises

- The explicit `--permission-mode` is forwarded raw to Codex as `--sandbox <value>`, with no validation. Typing the Claude value fails the Codex launch, and typing a valid elevated Codex value writes a trust stanza into `~/.codex/config.toml`, the exact footprint Decision 9 rejected.
- niwa's `[session.posture]` approvals vocabulary maps `on-untrusted` -> `untrusted` (`posture.go:76-81`), but current Codex docs say `untrusted` is retired and "can stop the client from starting". `on-failure` no longer appears in the docs either. That's a possible latent break, outside this lead's scope but worth an issue.
- Codex's real "auto" analog is a reviewer model (`approvals_reviewer = "auto_review"`), not a permission mode. It only works with interactive approvals, so it does nothing for `codex exec` workers.

## Open Questions

- Does the current codex-cli `exec` accept `-a`/`--ask-for-approval` and `-c approvals_reviewer=auto_review`? The docs leave exec's `-a` support unclear, and niwa's measurement is from 0.147.0. It needs a measurement against the binary.
- Should a host default ever be allowed to elevate Codex beyond the grant (to `danger-full-access`), given the trust-stanza side effect and the environment-policy leak measured at full access (DESIGN-codex-background-dispatch.md ~lines 1040-1055)?
- If the vocabulary is niwa-owned, is it `ask|auto|bypass` (approval-only), or does it need a sandbox axis to mirror `[session.posture]`? Should the host key and the to-be-deprecated workspace `permissions` posture share a vocabulary to ease migration (`bypass`/`ask` exist today, `materialize.go:315-340`)?
- Should an unrecognized value forward raw with a warning (the `dispatch_model` behavior) or be refused (the `[session.posture]` behavior)? For permissions, refusal seems safer, because a raw value reaching Codex `--sandbox` either fails or elevates.

## Summary

Today niwa models the permission intent as one `LaunchFlags.PermissionMode` with per-agent spellings. Codex workers get workspace-write from a per-invocation trust grant, not a flag. But an explicit `--permission-mode` is forwarded raw as Codex `--sandbox <value>` (`internal/cli/dispatch.go:1237-1283`), so a Claude value fails the launch, and an elevated Codex value writes a trust stanza into `~/.codex/config.toml`. Codex has no headless analog of Claude's `auto`: its sandbox modes are read-only, workspace-write and danger-full-access, its approval policies are on-request, never and granular, and its `approvals_reviewer=auto_review` reviewer only works when approvals are interactive. The best-fit design follows the `[global] dispatch_model` precedent: one host key with a niwa-owned, approval-only vocabulary that each agent's `LaunchSpec` maps, refusing unknown values. Codex should map to "forward nothing", because mapping bypass to danger-full-access would switch the sandbox off and write trust, which `posture.go:32-40` forbids. Raw passthrough as a host default would break mixed-agent hosts.
