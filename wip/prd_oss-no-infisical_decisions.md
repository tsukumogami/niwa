# PRD Decisions: oss-no-infisical

Auto-mode decision log per the decision protocol: gather evidence, form a
recommendation, follow it, record it.

## D1 — Skip a fresh Phase 2 research fan-out

**Evidence.** Ten research files from a completed two-round exploration already
sit in this worktree, covering the failure trace across every command, the
provenance and rationale of the required-key contract, provider-absence versus
unreachability, static detectability, peer-tool prior art, per-key necessity of
every declared secret, the existing escape hatches, SessionStart hook
semantics established empirically, and how the annotation and strictness switch
land in code.

**Decision.** Do not re-run a discovery fan-out. Draft directly against the
existing findings.

**Rationale.** The research-first protocol asks for evidence before a decision,
not for evidence to be regenerated. A second fan-out over the same questions
would spend budget to reproduce conclusions already recorded and reviewed.

## D2 — Close the BRIEF's three deferred questions in the PRD

The BRIEF deferred three framing details to this PRD's Decisions and Trade-offs
section, which is their canonical closure surface.

**Q1 — Is the strictness surface a flag, a configuration declaration, or both?**
Both, with the configuration declaration as the primary. Round-2 research
established that `realProvisionInstance` — the function serving both `niwa
dispatch` and the SessionStart hook — already loads the workspace and global
configuration rungs, so a declaration there reaches the unattended paths a flag
structurally cannot. A flag remains as the interactive front door for a
one-off run.

**Q2 — One switch for all four gates, or per-gate granularity?**
One switch. The four gates fail for different reasons, but they express a
single user intent: whether an unresolvable secret stops the command. Per-gate
granularity would ask an operator to understand niwa's internal failure
taxonomy in order to state a preference they hold at one altitude above it.
Recorded as a reversible choice: adding granularity later is additive, while
removing it would be a breaking change.

**Q3 — What should a total provisioning failure say, as distinct from a partial
one?** Left as an acknowledged remaining unknown, scoped rather than answered.
A partial failure is in scope and specified. A total failure — no instance at
all — is a different condition whose remedy depends on Claude Code's session
semantics beyond the unresolved-secret case, and it is recorded in Known
Limitations rather than specified here.

## D3 — Strict-when-reachable as the retained hard failure

**Decision.** A required key stays fatal when a provider is configured, is
reachable, and simply does not hold the key.

**Rationale.** That case is a genuine authoring or provisioning error with a
clear owner and a clear fix, and it is the maintainer's steady state — so the
change does not weaken the guarantee where the guarantee was doing work. The
alternative, softening every case uniformly, would remove a signal that
currently catches real mistakes.

## D4 — Naming discipline for the existing flag

**Evidence.** The prior-art survey found a convergent convention in which an
escape hatch names the exact failure it downgrades, and identified
`--allow-missing-secrets` — which does not allow missing required secrets and
does not cover an unreachable provider — as the shape that convention exists to
prevent.

**Decision.** The PRD requires that the flag's behaviour and its documented
description agree. It does not mandate a rename; whether agreement is reached
by widening the behaviour, renaming the flag, or both is a design question.
