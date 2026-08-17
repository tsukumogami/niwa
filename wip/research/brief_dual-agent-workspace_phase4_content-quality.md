# Verdict: PASS

## Criterion findings

### 1. Problem Statement states a problem, not a smuggled solution

Met. The section describes a gap in the world: an exclusive switch built by a
prior increment ("A workspace declares one agent, and niwa prepares the
instance for that agent and not the other"), the asymmetry it created ("the
Codex side of the switch delivers less than the Claude side does"), and the
structural fault ("the choice is front-loaded to the moment the developer
knows least"). No sentence names the thing about to be built. The closing
line — "The workspace should not be the thing that decides which agent gets
used" — is a statement about where a decision belongs, not a feature in
disguise. The examples of why agent choice is task-dependent (quota
exhaustion, model fit, second opinion) ground the problem in real developer
situations rather than abstractions.

### 2. User Outcome is outcome-shaped

Met. The section describes what is different for a user: "switching agents is
closing one and typing the other command in the same directory"; "the session
sees the workspace context for where it's standing." The commitments are all
user-observable — no setup ritual, clean `git status`, the developer's own
Codex configuration and login untouched. Nothing in the section enumerates
components, phases, or internal machinery. The "For Claude Code, nothing
changes" paragraph is a legitimate outcome statement (an invariant the user
can verify), not a feature.

### 3. User Journeys are concrete and distinct

Met. All four name a concrete user, a trigger, and an outcome shape, and each
enters from a genuinely different point:

- Mara: mid-task quota exhaustion → agent switch in the same directory.
  Exercises context parity between the two agents' sessions.
- Theo: fresh terminal, three directories deep in a cloned repo → cold start.
  Exercises location-independent discovery, a different property from Mara's
  parity.
- Iris: `niwa worktree` checkout → exercises the worktree layer, which the
  problem statement identifies as skipped entirely today.
- Noah: upgrade path for a workspace with a legacy agent declaration →
  exercises compatibility, an entry point none of the others touch.

Each journey ends with an explicit outcome sentence ("switching agents costs
her the conversation, not the workspace") that stays at behavior level.

### 4. Scope Boundary has real in/out exclusions

Met. The OUT items are exclusions a reader might reasonably assume are IN:

- "`niwa dispatch` remains Claude-only" — dispatch is a core niwa flow; a
  reader would plausibly assume dual-agent means dual-dispatch.
- Codex credentials — niwa binds secrets for Claude, so "niwa binds no API
  key for Codex" excludes something the feature's symmetry framing invites,
  and points at niwa#228 for the deferred piece.
- Ephemeral session provisioning, the skills-plugin invariant ("nothing is
  rewritten for Codex"), the Claude-untouched invariant, and the cross-repo
  gap (niwa#247) are all specific enough that a requirements author knows
  where the feature ends.

The IN list is behavior-level throughout: layered content parity, skills
delivery, start-anywhere sessions, writable sessions without a setup step,
clean repositories, config compatibility. No filler on either list.

### 5. Open Questions genuinely defer to the PRD

Met. Both questions are framing details the PRD can own:

- Hook delivery: "Nothing niwa ships today requires a Codex-side hook" — a
  scoping call, correctly parked with the reason it's deferrable.
- TUI clean-start: verification was in flight when framing closed; the brief
  states both landing spots (acceptance criterion, or stated limitation).
  This is exactly what an open question should look like — a known unknown
  with its resolution path named, not a blocker that should have stopped the
  brief.

## Altitude check

This is where I expected the document to fail, given how much settled
mechanism the handoff carries, and it does not. The brief consistently omits
what the handoff settles: no `.codex/` directory, no `AGENTS.override.md`, no
symlink layout, no `trusted_hash` or SHA-256 canonicalization, no
`project_doc_max_bytes` budget, no git-exclude pattern form, no
`trust_level` entries. Where the handoff says "the git-exclude pattern must
be the bare `.codex`, not `.codex/`", the brief says "the files niwa
materializes for Codex never show up in a cloned repo's `git status`" — the
correct behavioral restatement. The Status section explicitly assigns the
mechanism downstream: "The downstream DESIGN owns the delivery mechanism:
where the Codex-readable context and skills live on disk and how they are
kept in sync."

Applying the rewrite test to the borderline sentences:

- "Preparing for Codex today writes Codex's context only at the instance and
  group levels and skips the repository and worktree levels" (Problem
  Statement) — the layer names are the product's own context model, and the
  sentence describes current shipped behavior, which a problem statement must
  do. Survives.
- "no context files showing up as untracked noise in `git status`" (User
  Outcome) — user-observable. Survives.
- Frontmatter `motivating_context`: "overturned the premise that serving
  both agents requires niwa to manage a per-instance Codex home." This names
  a discarded mechanism, but as history of the framing, not as
  specification — it records why the premise changed, and if the eventual
  implementation differed, this sentence would still be true. Borderline but
  acceptable; noted under optional improvements.

No content sits below brief altitude.

## Required changes

None.

## Optional improvements

- The frontmatter `motivating_context` is the one place mechanism vocabulary
  ("per-instance Codex home", "Codex discovers project-level context and
  skills on its own") appears. It functions as framing history rather than
  specification, but it could be softened to "overturned the premise that
  niwa must manage agent-side configuration to serve both agents" if the
  author wants the frontmatter as clean as the body.
- Mara's and Theo's journeys share surface texture (both are "run `codex`
  and it works"). They test different properties — session-parity versus
  location-independent discovery — and the outcome sentences make that
  distinction, but Theo's journey could name the parity-vs-discovery
  contrast one notch more explicitly.
