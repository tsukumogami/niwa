---
schema: brief/v1
status: Draft
problem: |
  niwa's .env.example pre-pass hard-fails apply on any probable-secret
  detection, but .env.example files hold placeholders that routinely trip
  the entropy heuristic. The only knobs are all-or-nothing: block, or
  disable scanning. There is no per-variable or per-level control, so
  example and demo environments hit unavoidable false-positive failures.
outcome: |
  An owner runs apply and probable-secret detections warn without blocking
  by default. Owners opt into hard failures and tune, at user, project, and
  variable granularity, which detections fail versus warn, so a security
  team can restore strict blocking exactly where it wants it.
---

# BRIEF: env-example failure policy

## Status

Draft

This brief frames the failure-handling policy for the existing `.env.example`
secret pre-pass. It stops at problem, outcome, journeys, and scope; the
downstream PRD owns the config schema, precedence rules, and the fate of the
existing per-invocation bypass flag.

## Problem Statement

When niwa runs `apply`, it reads each repo's `.env.example` and classifies every
value. A value matching a known vendor-token prefix, or whose Shannon entropy
clears a fixed threshold, is treated as a probable secret and **aborts the apply
with a non-zero exit**. That hard-fail is the only response the pre-pass has to a
detection.

The trouble is what `.env.example` files are for. They exist to document the
shape of an environment with stand-in values, and realistic-looking stand-ins
(UUID-like tokens, base64 samples, long hex strings) regularly score above the
entropy threshold. So the file whose entire purpose is to hold examples is the
file most likely to trip the detector, and the result is an apply that refuses to
run over a value the author already knew was fake.

The escape hatches are all coarse. An owner can declare the key, land on a small
fixed allowlist, pass a per-invocation bypass flag (which only applies when the
repo has a public remote), or turn the whole scan off for a repo or workspace.
There is nothing between "block the apply" and "stop looking", no way to say
"warn me but proceed", and no way to set that response differently for one
variable, one project, or one operator. For anyone standing up example or demo
environments, the feature reads as high-entropy noise: failures that fire
unpredictably on placeholder data with no proportionate way to dial them down.

## User Outcome

A workspace owner runs `apply` against repos that carry `.env.example` files and
the run completes. Probable-secret detections surface as warnings the owner can
read and act on, but they no longer halt the apply on their own. The owner stays
informed without being blocked by false positives on placeholder values.

When an owner *wants* strict blocking, they turn it on deliberately. A
security-conscious operator can make probable-secret detections fail across all
their workspaces; a project can set its own policy; a single known-placeholder
variable can be exempted even when its project blocks by default. The owner
decides where on the warn-to-fail spectrum each detection sits, at the
granularity that matches how they work, rather than accepting one global hard-fail
or switching detection off entirely.

## User Journeys

### Individual developer hits a placeholder, apply still runs

A developer runs `niwa apply` on a workspace whose repo ships a `.env.example`
with a high-entropy placeholder value. With no failure policy configured, the
detection emits a warning naming the key and the run completes successfully. The
developer sees the signal, recognizes the value as a placeholder, and moves on
without editing config or passing a flag.

### Security-conscious operator opts into blocking everywhere

An operator who treats any probable secret in `.env.example` as a release-blocker
sets a failure policy in their personal niwa configuration so that probable-secret
detections fail the apply. From then on, every workspace they apply enforces hard
blocking on detections, restoring the old strict posture by their own choice
rather than by default.

### Project maintainer sets a project-level policy

A maintainer responsible for one workspace wants stricter handling for that
project than the operator's personal default provides (or wants to relax it). They
declare a failure policy at the project level. When that workspace is applied, the
project policy governs, overriding the user-level default for that workspace's
repos.

### Known placeholder is exempted at the variable level

A project blocks on probable-secret detections, but one variable in a repo's
`.env.example` is a documented placeholder that scores high. The owner marks that
single variable as warn-only at the variable level. Apply warns on that key and
proceeds, while still blocking on any other probable-secret detection in the same
file.

## Scope Boundary

**In:**

- A per-detection failure policy with at least a warn response and a fail
  response, replacing the pre-pass's single always-hard-fail behavior.
- A default posture in which detections warn and do not block the apply; hard
  failures are opt-in.
- Resolution of the policy at user, project, and variable granularity, with a
  defined precedence when the levels disagree.
- The configuration surface(s) through which the policy is declared at each
  level (the downstream PRD and design pick the exact schema and key names).

**Out:**

- Changes to the detection heuristics themselves. The entropy threshold value and
  the blocklist/allowlist contents stay as they are; this feature governs the
  response to a detection, not how a detection is made.
- Replacing the existing whole-scan on/off control. The ability to disable the
  `.env.example` scan entirely for a repo or workspace remains the "stop looking"
  knob; this feature adds graduated responses between that and a hard fail, it
  does not subsume it.
- Secret handling beyond the `.env.example` pre-pass: runtime scanning of real
  environment files at materialization, and vault-backed secret storage, are
  separate concerns.

## Open Questions

These framing details are deferred to the downstream PRD; none blocks the framing.

- The exact configuration schema and key names for declaring the policy at each
  level, and where "user-level" configuration physically lives.
- The precedence rule when user, project, and variable policies disagree
  (most-specific-wins is the working assumption; the PRD confirms it).
- The granularity of the policy key: a single warn/fail switch for all detections
  versus a policy keyed per detection category (vendor-token match versus entropy).
- Whether the existing per-invocation bypass flag and the public-remote
  special-case branch are retained, folded into the new policy, or deprecated once
  failures are opt-in.

## References

- docs/prds/PRD-env-example-integration.md — requirements for the original
  `.env.example` integration this policy amends.
- docs/designs/current/DESIGN-env-example-integration.md — the design whose
  Decision 1 established probable-secret-as-hard-error and the all-or-nothing
  opt-out.
