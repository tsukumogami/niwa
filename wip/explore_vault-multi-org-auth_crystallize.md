# Crystallize Decision: vault-multi-org-auth

## Chosen Type

Design Doc

## Rationale

The exploration produced a clear technical approach (local credential file
+ `--token` per invocation + CLI session fallback) with well-defined
scope (~20 lines of backend change + credential-file reading logic +
optional JWT caching). The "what to build" is settled; the "how to build
it" needs the design doc's structure to resolve implementation questions:
where the credential file is read (apply layer vs resolver vs backend),
the `provider-auth.toml` schema, the caching strategy, the bootstrap UX,
and the interaction with CI.

## Signal Evidence

### Signals Present (Design Doc)

- **Technical approach is clear**: all five leads converged on the same
  architecture. No competing approaches remain.
- **Core question is "how should we build this?"**: the credential-file
  schema, reading layer, caching strategy, and bootstrap UX are all
  "how" questions, not "what" or "why."
- **Scope is bounded**: ~20 lines of backend change + credential-reading
  + optional cache. Small enough for one design doc → one PR.
- **The decision has implementation consequences**: reading credentials at
  BuildBundle vs Factory.Open affects the apply.go code path. The design
  doc resolves this.

### Anti-Signals Checked

- **Requirements unclear?** No — the PRD's vault integration requirements
  still apply. The multi-org auth is an extension of R1 (pluggable
  provider interface), not new requirements.
- **Multiple independent features?** No — this is one feature (multi-org
  auth) with one deliverable.

## Alternatives Considered

- **PRD**: Rejected. Requirements are already captured in the vault
  integration PRD. This is an implementation gap within the existing
  requirements, not a new product question.
- **Plan**: Rejected. No upstream design artifact exists yet; the design
  doc needs to resolve the credential-reading layer and caching strategy
  before a plan can sequence the work.
- **No Artifact**: Rejected. The architecture decisions (credential-file
  schema, reading layer, caching) need to be documented to survive the
  session boundary.
- **Decision Record**: Considered but rejected. The trade-offs are clear
  (Lead 4 ranked the options decisively) but the implementation approach
  needs more structure than a decision record provides.
