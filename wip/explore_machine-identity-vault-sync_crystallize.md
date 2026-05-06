# Crystallize Decision: machine-identity-vault-sync

## Chosen Type
PRD

## Rationale

The exploration produced a clear shape for the feature but multiple
design questions remain that benefit from being framed as requirements
before they're answered as architecture. The question "what should this
feature do, and what invariants must it preserve" is the gating
question — the "how to wire it" follows naturally once those are
agreed.

Critical signals pointing to a PRD:

- The feature touches an explicitly-rejected design space (R12/D-9
  bulk provider replacement). The PRD must distinguish what this
  proposal IS and IS NOT, with crisp wording, before any design lands.
- Override visibility (R31) is a named existing invariant that this
  feature has to extend or honor. That's a requirement-level question:
  "what does the user need to see and audit," not "where does the
  code live."
- Conflict resolution between local file and vault-sourced credentials
  is a contract decision (which source wins, what's surfaced) before
  it's an implementation decision.
- The bootstrap chicken-and-egg constraint produces user-facing
  requirements (the personal vault MUST be reachable via CLI session;
  edge cases for non-default-org personal vaults).
- Conventions inside the vault (key layout) become a published
  contract that other tools and users will depend on once shipped —
  PRDs are where contracts live.

## Signal Evidence

### Signals Present
- **Stakeholder contract needed**: The proposal redefines what
  `provider-auth.toml` represents (now a *layer* in a credential
  pool, not the only source). Users need a written contract about
  precedence and visibility.
- **Engages existing invariants**: R31 (override visibility) and
  the rationale behind R12/D-9 must be explicitly addressed.
- **User journeys differ before vs after**: The "bootstrap fresh
  laptop" flow changes meaningfully. Worth documenting as USs.
- **Multiple accepted approaches require an explicit choice**: 
  conflict policy, materialization, schema layout — all design knobs
  whose answers shape user-visible behavior.

### Anti-Signals Checked
- **Pure technical/internal refactor**: Not present — this is
  user-facing behavior change.
- **Single obvious approach**: Not present — three of the six leads
  have credible alternatives.
- **Already-decided requirements**: Not present — this is fresh
  surface area built on existing primitives.

## Alternatives Considered

- **Design Doc**: Ranked second. The architecture is constrained
  enough that a design could be written today (the existing
  `injectProviderTokens` flow is the obvious extension point). But
  writing the design before the contract is settled risks
  re-litigating "what should this feature do" inside the design,
  which is the failure mode PRDs prevent. PRD first, then design.
- **Plan**: Premature. There is no PRD or design to decompose yet.
- **No artifact**: Ruled out. The exploration produced four
  decisions (it's not the rejected pattern; in-memory only;
  local-wins on conflict; require CLI-session bootstrap for the
  personal vault) that need a permanent home. wip/ is cleaned
  before merge.

## Deferred Types
None applicable.
