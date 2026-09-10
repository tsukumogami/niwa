# Lead: Blast radius of pre-approving SendMessage, and guardrails that scope it

Research lead 7 of `wip/explore_dispatch-sendmessage-approval_scope.md`. Evidence is
either VERIFIED (read from code, a doc in this repo, or the `SendMessage` tool contract
as it is presented to a session in this harness) or INFERRED (reasoned from those, and
labelled).

## Findings

### 1. What the approval prompt is actually protecting

VERIFIED, from the `SendMessage` tool contract itself: the tool's own documentation
names the threat in plain words. It instructs the model:

> Permission boundaries are per-session: NEVER ask a peer to perform an action that was
> denied or blocked in your session, or that you expect your own permission settings
> would block — a peer doing it for you bypasses the user's permission decision
> (cross-session permission laundering). Route blocked work back to your user instead.

Two things follow. First, the harness authors consider peer messaging a permission
boundary in its own right, not a neutral transport. Second — and this is the load-bearing
part — that instruction is *prose addressed to the model*. It is not an enforced check.
The only enforcement that exists in the shipped design is the human in the loop at
delivery time. Pre-approving delivery removes the only enforcement and leaves the
instruction.

VERIFIED, also from the contract: the prompt is a *content-admission* gate, not an
authority grant. The tool describes a session state in which it "holds peer messages for
approval" (it changes whether an idle notice is shown to the model or only to the user),
which places the hold on the receiving side. What the human is approving is the arrival
of externally-authored text into a session's context.

The concrete chain, grounded in niwa's own prior analysis rather than invented here.
`DESIGN-dispatch-permission-mode.md` (Security Considerations) and
`DESIGN-watch-operator-approval.md` (Context and Problem Statement) already establish, for
this repo, that a dispatched worker processing untrusted content — "an issue body, a PR,
a web page" — is a prompt-injection surface niwa takes seriously enough to have built an
OS sandbox, an egress-deny hook and a filesystem guard around it for `niwa watch`. A
delivered peer message is a fourth content channel of exactly the same kind, with two
properties the other three lack: it arrives from inside the trust perimeter (a peer the
user launched, so it reads as authoritative rather than as fetched material), and it is
addressed — it can name the recipient's task, its instance path, and what to do next.

So the chain is: worker A reads untrusted content in the course of its task → A's
subsequent behaviour is shaped by that content → A messages B, C, D → their contexts now
carry instructions that originated outside any of them, wearing the voice of a trusted
peer. The human approval on delivery is the one point where a person reads that text
before it lands.

### 2. The propagation question: what bounds the reachable set

This is the finding that most changes the shape of the answer. VERIFIED from the
`SendMessage` contract:

- Targets are discovered with `ListAgents` and addressed **by name**: "the name IS the
  address; there is no separate address syntax."
- Delivery is explicitly **not machine-bounded**: "a name that exactly matches one live
  agent or session (**on this machine, on another machine, or in the cloud**) delivers
  directly."
- The protocol *does* know how to scope to a machine when it wants to — `notify_when_idle`
  is documented as applying only to "a session ON THIS MACHINE". Delivery carries no such
  qualifier. The asymmetry is deliberate, not an omission.
- A send to an agent that has already finished **resumes it**: "names keep working after
  an agent completes (a send resumes it from its transcript)."
- Subagents do not have their own address: "if you are a subagent, your send goes out
  under your parent session's address, and any reply is delivered to the parent session's
  conversation".

INFERRED, but with high confidence: the reachable set is bounded by the Claude Code
identity fabric — whatever set of sessions the user's account and device registration make
mutually visible — and by **nothing niwa controls**. niwa does not route these messages
and has no hook in the path. Its own session records
(`internal/workspace/session_map.go`, `.niwa/sessions/<id>.json` under a workspace root)
are bookkeeping for instance reclamation and re-entry; they are not an address book the
messaging layer consults. So "same workspace" and "same instance" are not boundaries that
exist at the layer where delivery happens. On a machine where remote control is on
(`internal/cli/dispatch_remotecontrol.go`, which requires a claude.ai login and refuses
under `ANTHROPIC_API_KEY`), the session is by construction reachable from Agent View,
claude.ai and mobile — the fabric is account-level, and so, plausibly, is the peer set.

Reachability is therefore transitive and unbounded by niwa: A→B, B→C, and C need not be
in A's workspace, A's instance, or even A's machine. If B is pre-approved and C is not, C
still prompts — the pre-approval is per-recipient — so the *silent* set is the set of
recipients that carry the setting. That is the one real bound, and it is exactly the bound
the flag creates.

**niwa-specific hazard the address model creates.** VERIFIED: `niwa dispatch` forwards the
sanitized `--name` slug to the worker as the session's display name
(`internal/cli/dispatch.go` `buildDispatchPassthrough`, mapped to `--name` at
`internal/agentplan/dispatch.go:356`). The slug is `[a-z0-9_]`, capped at 40 runes,
**and nothing makes it unique**. The uniqueness niwa generates — the 8 hex digits from
`dispatchNameSuffix` — goes into the *instance directory name*, not the session name. Two
`niwa dispatch --name review` invocations produce two sessions both addressable as
`review`, and the contract's disambiguation rule is "latest wins" for a bare name. A
message intended for one worker deterministically lands on the other. That is a
correctness bug before it is a security one, and it gets worse with pre-approval, because
today the misdelivery at least surfaces to a human.

### 3. Does the recipient's own permission system still apply?

INFERRED from the contract's "Permission boundaries are per-session" statement, and
consistent with everything in this repo: yes in principle. A delivered message is text in
a context; whatever the recipient then does — a write, a push, a `gh` call — goes through
the recipient's own permission engine. Pre-approving delivery is not pre-approving the
consequences.

In principle. For niwa's actual population of dispatched sessions, that reassurance is
close to worthless, and this is the crux of the whole lead:

- VERIFIED (`internal/cli/dispatch.go`, step 9a-derive, lines ~536-557): when a workspace
  declares `permissions = "bypass"` in `workspace.toml`, `niwa dispatch` derives and
  forwards `--permission-mode bypassPermissions` to the worker. So the recipient's
  permission engine is, for those workspaces, off.
- VERIFIED (`DESIGN-watch-operator-approval.md`, problem statement): under
  `bypassPermissions` a PreToolUse hook's `ask` decision "is silently treated as allow
  (fail-open)". Deny still works — `internal/watch/guardfs.go` calls its hard-deny posture
  "inert-under-bypassPermissions safe -- the exit code is the whole contract" — but *asking*
  does not.
- VERIFIED (`DESIGN-dispatch-permission-mode.md`, Security Considerations, stated as a
  known open gap): "`niwa dispatch` has no containment for a bypass worker, unlike `niwa
  watch`'s sandbox mode. ... `internal/cli/dispatch*.go` has no equivalent hooks today".
  The design explicitly leaves this to a follow-up issue.

So for a bypass workspace the chain has no gate anywhere: no approval on delivery (if
pre-approved), no permission prompt on the action, no containment hook, no sandbox. The
message *is* the action. That is a materially different proposition from pre-approving
delivery into a session whose permission engine is live, and it is the specific fact that
justifies "off by default" far better than a general appeal to prompt-injection risk.

**Second-order reach, verified and easy to miss.** Because a send resumes a completed
session, and because `docs/guides/ephemeral-session-instances.md` says an instance is kept
for as long as its session exists ("A session that finishes its task, goes idle, or is
suspended keeps its instance — it is still listed and resumable ... only an explicit delete
tears the instance down"), a message is a **wake primitive over a standing fleet**. Every
one of those instances holds a materialized clone and, per `docs/guides/vault-integration.md`,
a vault-materialized secrets file at mode 0600. The cost of one misdirected or
injection-shaped message is not "text in a context"; it is "a finished worker, in an
instance with live credentials, starts running again with no human present."

### 4. Guardrail options, and which niwa can actually enforce

Ranked by whether niwa can hold them, not by how good they sound.

**Enforceable, and worth doing.**

*Per-invocation delivery only, never a config-file channel.* niwa already has the pattern:
remote control is injected as `--settings '{...}'` on the dispatch argv
(`internal/cli/dispatch_remotecontrol.go` + step 9c), and permission mode as a CLI flag,
precisely because since Claude Code 2.1.258 the materialized-settings channel is no longer
honoured for posture. Delivering the pre-approval the same way means it lives and dies with
the invocation: the setting cannot be inherited by an interactive session, cannot be
carried into a workspace by a checked-in `settings.json`, and cannot outlive the session it
was granted to. This is genuine time-bounding and it costs nothing, because it is the only
channel available anyway.

*Restriction to sessions niwa itself launched.* Enforceable, by construction: niwa can only
inject launch arguments into workers it starts. It reaches nothing else. Note carefully what
this does and does not mean — it bounds the set of *pre-approved recipients*, which is the
useful half; it does not bound who may message them.

*Audit visibility on the sending side.* Partly enforceable. `internal/watch/containment.go`
shows the working pattern — a PreToolUse hook with a tool-name matcher (`"WebFetch|WebSearch|mcp__"`)
that fires under `bypassPermissions` — so a hook matching `SendMessage` in a dispatch
instance could log every outbound send, and could hard-deny sends to targets outside a list.
Two limits: it sees only what the *dispatched* session sends, never what it receives; and
`to` is a user-chosen name, not an authenticated identity, so a name filter is a
convenience, not an identity check. Under `bypassPermissions` such a hook can only allow or
deny, never ask.

*A dispatch-time audit line.* Free and consistent with existing practice —
`DESIGN-dispatch-permission-mode.md` added exactly this for the derived bypass posture
("Audit signal ... so an operator inspecting `niwa dispatch`'s output can tell, after the
fact, whether `bypassPermissions` came from an explicit flag or from the workspace's
declared posture"). The same line should print when a dispatch pre-approves peer messages.

**Not enforceable. Do not ship a key that implies them.**

*Scoping to peers in the same workspace or the same instance.* niwa cannot hold this. It is
not in the routing path, the address space is names rather than instance-qualified
identifiers, and the contract states delivery crosses machines. A config key spelled
`allow_peer_messages_from_workspace` would be a false statement about a boundary that does
not exist at the delivery layer, and false boundaries are worse than absent ones because
people plan around them.

*A named allowlist of senders.* Same problem, one level down: the pre-approval as it exists
is a property of the *recipient's* posture and carries no sender predicate that niwa could
populate. Even if one existed, names are unauthenticated slugs with no uniqueness guarantee
(finding 2). Advisory at best.

*Making the prompt "visible rather than silent" on the receiving side.* Not available under
`bypassPermissions`, where `ask` fails open. This is the option that reads best on paper and
is dead on arrival for exactly the workers that need it.

### 5. The counter-argument, made honestly

The case that this is over-cautious runs: one user, one machine, their own sessions,
launched by them, doing their work. The messages are the user's own agents talking. The
approval prompt is friction; in a fan-out of eight workers a human will click through it
without reading, which makes it a *ritual* control rather than a real one, and rituals that
train people to click through are net-negative. The genuine control has always been the
recipient's own permission engine, which still stands.

Two of those three claims hold. The click-through critique is correct and should be said
out loud: an approval prompt that fires often enough is not a security control. And it is
true that pre-approving delivery grants no authority by itself.

The case fails on two verified facts. "On my own machine" is not the boundary — the
contract says delivery crosses machines and reaches the cloud, so the population is
account-scoped, not host-scoped, and a user's mental model of "my laptop" does not describe
it. And "the recipient's permission engine still stands" is false for the workspaces that
dispatch most heavily: `niwa dispatch` derives `bypassPermissions` from a declared workspace
posture, dispatch has no containment hooks, and `ask` fails open under bypass. Those are
this repo's own findings, in `internal/cli/dispatch.go` and
`DESIGN-dispatch-permission-mode.md`, not an outside worry imported here.

So the honest verdict is narrower than either framing. The prompt has little value when the
recipient is an interactive session with a human at the keyboard and a live permission
engine. It has real value when the recipient is an unattended bypass worker with no
containment. niwa dispatches the second kind. Off by default is right — but the reason to
put in the design doc is the *combination* (unattended + bypass + no containment + a wake
primitive over instances holding credentials), not "messaging is risky", which would not
survive a sceptical reviewer.

### 6. What the repo has already decided in this area

VERIFIED. niwa deleted its own agent-to-agent messaging layer, deliberately and in full.
`docs/designs/current/DESIGN-niwa-mesh-removal.md` removes the MCP server, the task
delegation substrate, per-role inboxes, peer messaging, the coordinator session registry
and the change-review surface; `docs/prds/PRD-cross-session-communication.md` is marked Done
and superseded, and `docs/designs/archive/DESIGN-cross-session-communication.md` — which
specified peer messaging with a per-task authorization check (`NIWA_TASK_ID` env + role
match + PPID start-time match) — is Superseded.

Read the rationale carefully, because it cuts against the obvious reading. The mesh was
removed because it was **non-functional and off-identity** ("nothing exercises it end to
end", "the product is a workspace and worktree manager, not an agent-facing tool"), not
because agent-to-agent messaging was judged dangerous. That is a decision about what niwa
builds, not a prohibition on the harness providing it. Re-enabling delivery for a
harness-native `SendMessage` does not reverse the mesh decision, and the scope file is right
to keep that off the table.

Two threads from that decision do bear on this one, though. The removal ends with "Downstream
replacement coordination ... remains out of scope and gated on this removal, so the workspace
never carries two coordination mechanisms at once" — a knob that makes harness-native peer
messaging frictionless inside niwa-dispatched workers is, functionally, niwa taking a
position on the replacement, and should say so rather than arrive as a permissions tweak.
And the deleted design had something the harness does not: an *authenticated* sender
(`authorizeTaskCall`, verifying env token, role and process ancestry). The mesh niwa deleted
could tell who was calling. The mechanism replacing it addresses by unauthenticated
user-chosen name. Any guardrail proposal that assumes sender identity is available should be
checked against that.

## Implications

**The default should be off, and the flag should be per-invocation.** The user's instinct is
right, for a sharper reason than they gave: not because messaging is dangerous in general,
but because niwa's dispatched workers are the specific case where every other gate in the
chain is already open. Put that reasoning in the design doc — "unattended worker, derived
`bypassPermissions`, no dispatch containment, `ask` fails open, and a send can resume a
finished session in an instance holding materialized secrets" — because it is defensible and
"prompt injection is a risk" is not.

**Deliver it through the launch-argument channel only, and never add a `workspace.toml` key
for it.** This is the substantive shape recommendation. The per-invocation channel gives real
time-bounding for free, keeps the grant out of any file a repo or branch could carry, and
matches how remote control and permission mode already reach a worker
(`internal/cli/dispatch_remotecontrol.go`, step 9c/9a-derive). A host-level
`[global]` preference plus a `niwa dispatch` flag — the `remote_control_on_dispatch` +
`--keep-alive` precedent — is the right precedence chain, provided the host preference acts
as a *default-fill for dispatch only*, exactly as `resolveDispatchRemoteControl` does, and
never as a materialized setting.

**Name the key for what it does, not for a boundary niwa cannot hold.** Something that reads
as "accept peer messages without asking" is honest. Anything containing `workspace`,
`instance`, `peers`, or `local` implies a scope that does not exist at the delivery layer and
will be believed.

**Print an audit line when it engages,** following the precedent
`DESIGN-dispatch-permission-mode.md` set for the derived bypass posture. A grant that changes
a worker's trust posture and leaves no trace in the dispatch output is the thing that makes
an incident unreconstructable.

**Fix the session-name collision while you are here, or file it.** Forwarding a non-unique
`--name` slug as the session's address (finding 2) is a live misdelivery bug that
pre-approval makes silent. niwa already generates the uniqueness it needs; it just spends it
on the directory name instead of the session name.

**Treat dispatch containment as the real follow-up.** `DESIGN-dispatch-permission-mode.md`
already flagged that `niwa dispatch` lacks `watch`-equivalent PreToolUse hooks and suggested
an issue. Pre-approving peer messages raises the value of that issue rather than creating it.
If this exploration produces one out-of-scope recommendation, that is the one.

## Surprises

- **Delivery is explicitly not machine-scoped, while a sibling feature in the same tool
  explicitly is.** `notify_when_idle` says "ON THIS MACHINE"; delivery says "on this machine,
  on another machine, or in the cloud". Any guardrail premised on "it's all local anyway" is
  premised on something the contract denies in writing.
- **A message resumes a completed session.** Delivery is a wake primitive, not just a write
  into a live context. Combined with niwa keeping ephemeral instances alive until explicit
  deletion, and with those instances holding vault-materialized secrets, the standing
  population a pre-approved fleet exposes is much larger than "the workers running right now".
- **The tool contract carries the anti-laundering rule as model-directed prose.** The
  cross-session permission boundary is asserted, described, and named — and enforced only by
  the model choosing to obey it, plus the human at delivery. Removing the human leaves the
  honour system.
- **`ask` fails open under `bypassPermissions`.** The most attractive middle-ground guardrail
  — "don't block it, just surface it" — is unavailable for precisely the workers that need it.
  Only allow and deny exist there.
- **niwa forwards a non-unique name as the session's address.** The one piece of the address
  space niwa actually controls, it currently sets in a way that can collide.
- **The mesh was removed for identity and dead-code reasons, not security.** Worth stating
  plainly, because it is tempting to cite the removal as a prior decision against
  agent-to-agent messaging, and the document does not support that reading.

## Open Questions

- Which side the pre-approval setting actually attaches to, and what its concrete spelling is.
  This lead establishes that the hold is recipient-side and that per-invocation delivery is
  the right channel; leads 1-3 of the scope file own the mechanism. If the only available
  knob turns out to be sender-side, the guardrail ranking here shifts — a sending-side
  allowlist hook becomes the primary control rather than a secondary one.
- What exactly bounds the peer set in practice. "Account and device registration" is
  inferred from the contract's wording and from remote control's claude.ai login requirement.
  Whether a session on a second machine under the same account genuinely appears in
  `ListAgents`, and whether cloud sessions do, is an empirical question worth one experiment
  before the design asserts a boundary.
- Whether a PreToolUse hook matching `SendMessage` fires and can deny in a dispatch instance.
  The pattern is proven for `WebFetch|WebSearch|mcp__` in `internal/watch/containment.go`; that
  it generalises to this tool name is inferred, not tested.
- Whether the human should decide this per dispatch at all. An alternative worth one
  paragraph in the design: make pre-approval implicit for a *named fan-out* the user launched
  in one gesture, and unavailable otherwise. Nobody has proposed it and it may be worse, but
  it is the only option that ties the grant to an intent the user actually expressed.
- Whether `niwa dispatch` should gain containment hooks before or alongside this flag. Sequencing
  question for a human: shipping the flag first is defensible (it restores an ergonomic the
  user wants, off by default), but it widens a gap the repo has already written down.

## Summary

Pre-approving `SendMessage` removes the only enforced check on a boundary the tool's own
contract calls a permission boundary, and it does so for a population where every downstream
gate is already open: niwa derives `bypassPermissions` for dispatched workers of a bypass
workspace, `niwa dispatch` has no containment hooks (a gap `DESIGN-dispatch-permission-mode.md`
already records), hook `ask` decisions fail open under bypass, and a message resumes finished
sessions in instances that still hold materialized secrets. Off by default is the right call,
but the guardrails that sound best are illusory — delivery is addressed by unauthenticated,
non-unique names and reaches sessions on other machines and in the cloud, so niwa cannot scope
pre-approval to a workspace, an instance, or a host; what it can genuinely enforce is
per-invocation delivery that dies with the session, restriction to workers it launched itself,
a sending-side deny hook, and an audit line. The biggest open question is empirical: what the
peer set actually contains across machines and cloud sessions under one account, since every
boundary claim the design might make depends on it.
