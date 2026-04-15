# Crystallize Decision: claude-key-consolidation

## Chosen Type

Design Doc

## Rationale

Exploration made three small architectural decisions that need to live
past `wip/` cleanup: the `ClaudeConfig` ↔ `ClaudeOverride` type split,
the deprecation mechanic riding on `Parse` warnings, and a policy on
whether `workspace.content_dir` also renames. None of them are large,
but together they shape the final schema and future contributors will
want the record of why the override-time type differs from the
workspace-time type.

Requirements are fully specified (rename `[content]` to
`[claude.content]`, deprecate the old form, preserve shape). "How"
covers the three decisions above plus the specific
BurntSushi/toml metadata API for detection. That's squarely the
Design Doc's job.

Scope is small enough (~150 LOC, single PR) that the doc can be lean.

## Signal Evidence

### Signals Present

- **What to build is clear, but how to build it is not**: the rename
  is settled; the type split, deprecation mechanic, and
  `content_dir` policy are not.
- **Technical decisions need to be made between approaches**: type
  split now vs defer; Marketplaces moves with the split or stays;
  `content_dir` rename in-scope or follow-on.
- **Exploration surfaced multiple viable implementation paths**:
  Lead 3 offered two enforcement options (type split vs runtime
  validation); Lead 2 offered two deprecation detection approaches.
- **Architectural decisions were made during exploration that should
  be on record**: type-split recommendation, deprecation-via-Parse
  recommendation, scope narrowing around `content_dir`.
- **Core question is "how should we build this?"**: the user already
  set the "what" (rename) and the "why" (explicit claude coupling).

### Anti-Signals Checked

- **What to build is still unclear**: not present.
- **No meaningful technical risk or trade-offs**: not present — the
  type split is a real architectural choice with compile-time
  enforcement as its benefit.
- **Problem is operational, not architectural**: not present — this
  is a schema change.

## Alternatives Considered

- **PRD**: Not needed. Requirements are fully specified by the user's
  initial message and the research findings. No stakeholder alignment
  gap.

- **Plan**: Score near-zero. No upstream artifact exists, the type
  split and deprecation-mechanic choices need to be settled before a
  plan can sequence work meaningfully.

- **No Artifact**: Scored -1 (architectural decisions were made that
  must be recorded). The rename itself is borderline-trivial, but the
  `ClaudeConfig` type split is a shape change that future readers
  will want to understand.

- **Decision Record**: Fits one of the three decisions (type split)
  but not all of them. A design doc covering the whole migration is
  the right granularity.

## Deferred Types

None applicable.
