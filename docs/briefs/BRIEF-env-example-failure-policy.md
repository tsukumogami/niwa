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
  variable granularity, which detections fail versus warn, with the most
  specific setting winning, so a security team can restore strict blocking
  exactly where it wants it.
---

# BRIEF: env-example failure policy

## Status

Draft

This brief frames the failure-handling policy for the existing `.env.example`
secret pre-pass. It settles the user-facing behavior: the warn-by-default
posture, the per-detection-category granularity, the user/project/variable
precedence and inheritance, the two sources of a variable-level exemption, the
per-run escape hatch, and the removal of the remote-visibility special case. The
downstream PRD and design own only mechanism: the exact config schema, key
names, and inline-annotation syntax.

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

When an owner *wants* strict blocking, they turn it on deliberately, and they can
turn it on with precision: failures are configurable per detection category, so
an operator can fail on recognized vendor tokens while still only warning on
entropy hits. The response is set at user, project, or variable granularity.

When settings disagree, the most specific one wins: a single variable's policy
overrides its project's, which overrides the operator's personal default. Any
level left unset inherits the broader one, and warn is the floor when nothing is
set anywhere. The owner decides where on the warn-to-fail spectrum each detection
sits, at the granularity that matches how they work, rather than accepting one
global hard-fail or switching detection off entirely.

## User Journeys

### Individual developer hits a placeholder, apply still runs

A developer runs `niwa apply` on a workspace whose repo ships a `.env.example`
with a high-entropy placeholder value. With no failure policy configured, the
detection emits a warning naming the key and the run completes successfully. The
developer sees the signal, recognizes the value as a placeholder, and moves on
without editing config or passing a flag.

### Security-conscious operator opts into blocking, by category

An operator who treats recognized vendor tokens in `.env.example` as a
release-blocker sets a failure policy in their personal niwa configuration so that
vendor-token detections fail the apply, while entropy detections stay warnings.
From then on, every workspace they apply enforces hard blocking on the category
they chose, restoring strict handling on their own terms rather than by default.

### Project maintainer sets a project-level policy

A maintainer responsible for one workspace wants stricter handling for that
project than the operator's personal default provides (or wants to relax it). They
declare a failure policy at the project level. When that workspace is applied, the
project policy governs, overriding the user-level default for that workspace's
repos, while any variable-level setting still wins over the project policy.

### Repo documents a known placeholder inline

A repo author knows one value in their `.env.example` scores high but is a
documented placeholder. They mark that single variable warn-only with an inline
annotation in the file itself. Anyone applying a workspace that includes the repo
has the annotation honored: apply warns on that key and proceeds, while every
other key in the file still gets the governing project or user policy.

### Operator overrides a repo's inline exemption

An operator does not trust a repo's self-applied inline exemption for a particular
key and wants it to fail regardless of what the repo declared. They set a
variable-level policy for that key in their own workspace configuration. On apply,
the operator's variable-level setting wins over the repo's inline annotation, and
the key fails as the operator intends.

## Scope Boundary

**In:**

- A per-detection-category failure policy with a warn response and a fail
  response, replacing the pre-pass's single always-hard-fail behavior. The
  category distinguishes at least vendor-token matches from entropy detections, so
  the two can be set independently.
- A warn-by-default posture: detections warn and do not block the apply; hard
  failures are opt-in.
- Resolution at user, project, and variable granularity, with most-specific-wins
  precedence (variable over project over user), inheritance of any unset level
  from the broader one, and warn as the default when nothing is set.
- Two sources for a variable-level policy: an inline annotation in the repo's
  `.env.example`, and an explicit entry in the operator's workspace configuration,
  with the operator's configuration winning when both name the same variable.
- Retaining the existing per-invocation bypass as a per-run override that
  downgrades failures to warnings for a single apply.
- Removing the remote-visibility special case from the pre-pass: no behavior is
  conditioned on whether a repo's remote is public.
- The configuration surfaces through which the policy is declared at each level
  (the downstream PRD and design pick the exact schema, key names, and
  annotation syntax).

**Out:**

- Changes to the detection heuristics themselves. The entropy threshold value and
  the blocklist/allowlist contents stay as they are; this feature governs the
  response to a detection, not how a detection is made.
- Conditioning policy on remote visibility. It is removed as a default and
  deliberately not reintroduced as a configurable axis; an operator who wants
  strict handling for exposed repos sets a failing policy by level instead.
- Replacing the existing whole-scan on/off control. The ability to disable the
  `.env.example` scan entirely for a repo or workspace remains the "stop looking"
  knob; this feature adds graduated responses between that and a hard fail, it
  does not subsume it.
- Secret handling beyond the `.env.example` pre-pass: runtime scanning of real
  environment files at materialization, and vault-backed secret storage, are
  separate concerns.

## References

- docs/prds/PRD-env-example-integration.md — requirements for the original
  `.env.example` integration this policy amends.
- docs/designs/current/DESIGN-env-example-integration.md — the design whose
  Decision 1 established probable-secret-as-hard-error and the all-or-nothing
  opt-out.
