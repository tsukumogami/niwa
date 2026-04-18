---
status: Proposed
decision: |
  `vault://` URIs in anonymous-provider files accept an optional folder
  path as zero or more leading segments: `vault://<segments.../>/<key>`.
  Everything before the last `/` is the folder path; the final segment
  is the key. The `Ref` type gains a `Path` field. Backends that
  understand folder paths honor `Ref.Path` on every resolve, overriding
  any `Factory.Open`-time default when non-empty. Named-provider URIs
  keep their one-slash-max shape (`vault://<name>/<key>`) and are
  validated at config-load time: a URI whose first segment doesn't
  match any declared provider name is a hard error naming the URI,
  the unknown name, and the declared providers.
rationale: |
  PRD US-3 showed `vault://tsukumogami/github-pat` against an anonymous
  provider with the author's commentary describing it as a folder
  path — but PRD R3 as written forbade that shape and the implementation
  followed R3. Extending the anonymous URI form to accept path segments
  matches the user story, keeps named-provider URIs working unchanged,
  and lets one anonymous `[vault.provider]` reach every folder in a
  project without the per-folder named-provider explosion. File-local
  provider declarations already disambiguate the two shapes; the only
  cross-check needed is validating that named-provider URIs reference
  a declared name, and surfacing a clear error when they don't.
---

# ADR: Vault URI Folder Paths

## Status

Proposed

## Context

PRD-vault-integration.md describes the `vault://` URI scheme in two
places. R3 (the formal rule) restricts anonymous-provider files to
segment-free URIs (`vault://<key>`) and restricts named-provider files
to one-slash URIs (`vault://<name>/<key>`). US-3 (the headline user
story for personal-overlay layering) shows `vault://tsukumogami/github-pat`
against an anonymous `[global.vault.provider]` with prose calling the
leading segment "the folder path I chose". These two specifications
contradict each other.

The implementation followed R3: `internal/vault/ref.go` rejects any URI
with more than one slash after `vault://`, so the US-3 example is
unreachable in the shipped product. Users who want to reach multiple
folder paths within a single Infisical project today must declare one
named provider per folder, which scales poorly and clutters the
overlay for what is fundamentally a per-URI concern.

The decision needed now: what URI grammar and resolver contract
supports folder-path resolution under an anonymous provider, while
keeping existing named-provider URIs working unchanged?

## Decision

Extend the anonymous `vault://` URI form to accept an optional folder
path as zero or more leading segments. The URI `vault://<segments…>/<key>`
carries the folder path (everything before the last `/`) plus the key
(the final segment). `Ref` gains a `Path` field populated from the
leading segments; `Ref.Key` holds only the final segment.

Backends receive `Ref` per resolve and decide what to do with `Path`:
Infisical uses it to override its `Factory.Open`-time default `path`
when non-empty; sops (as it stands today) ignores it; future backends
opt in or reject as appropriate for their key namespace.

Named-provider URIs retain their `vault://<name>/<key>` shape — exactly
one slash, `<name>` disambiguated file-locally by the file's
`[vault.providers.<name>]` declarations. Validation at config-load
time rejects any named-file URI whose first segment doesn't match a
declared provider name, with an error that lists the URI, the unknown
name, and the declared names. No silent fallback to folder-path
interpretation in named-provider files.

Named-provider-plus-folder-path (`vault://<name>/<path.../>/<key>`) is
out of scope for this iteration and may be added later without
breaking the grammar introduced here.

## Options Considered

**Chosen: extend anonymous URIs to accept path segments.** The
anonymous grammar becomes `vault://[<path-segments.../>]<key>[?required=<bool>]`
(path-segments optional). File-local provider-declaration shape
determines which grammar applies to URIs in that file — the existing
R3 principle, extended rather than replaced.

**Rejected: triple-slash delimiter (`vault:///<path>/<key>`).**
Unambiguous but uglier, and breaks US-3's two-slash syntax. No
material benefit over leveraging file-local disambiguation.

**Rejected: query parameter (`vault://<key>?path=/folder`).** Works for
every backend uniformly but verbose, and still doesn't match US-3. The
backend contract is cleaner with a structured `Ref.Path` field than
with free-form query parameters parsed by each backend.

**Rejected: named providers for every folder path (status quo).**
Forces `[vault.providers.<scope>]` per folder, polluting personal
overlays that model the same Infisical project as several "providers"
solely to reach different paths. The user explicitly rejected this
pattern.

**Rejected: concatenate `Factory.Open.path` with `Ref.Path` rather
than override.** More flexible (lets a provider be "based" at `/team/`
with URIs narrowing further) but no current use case demands it, and
concat semantics are harder to reason about. Override is simpler;
concat can be added later without breaking callers.

## Consequences

**Positive:**

- US-3 becomes reachable as written. No config rewriting for users who
  already followed the user story's pattern.
- Personal overlays collapse from N named-per-folder providers to one
  anonymous provider with folder-path URIs.
- Backends opt in to path semantics by honoring `Ref.Path`. Backends
  that don't (sops) work unchanged.
- Named-provider URIs that typo a provider name now fail at parse
  time with an actionable error instead of at resolve time with a
  backend-level "key not found".

**Negative / accepted trade-offs:**

- `Ref.ProviderName` and `Ref.Path` are mutually exclusive in this
  iteration (named-provider files forbid path segments); a future
  design may allow both.
- Existing URI parser tests that asserted "nested slashes rejected"
  need to be rewritten — the new grammar accepts them for anonymous
  URIs.
- Backends now need to decide what `Ref.Path` means for them.
  Documenting that default is a small ongoing cost per backend.

**Implementation constraints this introduces:**

- `ParseRef` must return a `Ref` with both `Path` and `Key` populated;
  the old single-`Key` shape changes.
- The `Provider`-facing contract now passes a per-resolve `Ref.Path`;
  backends that stored `path` at `Factory.Open` time need to read
  `Ref.Path` first and fall through to the Open-time default.
- `internal/config/validate_vault_refs.go` gains the unknown-name
  check and error message.
