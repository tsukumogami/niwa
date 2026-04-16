# Architect review: PRD-vault-integration

Reviewing the PRD against `internal/config/config.go`, `internal/workspace/override.go`,
`internal/workspace/materialize.go`, and `internal/workspace/state.go`.

Structural summary: the PRD introduces three cross-cutting concepts (vault
providers, reference resolution, three-level env requirements) and attempts to
wire all three through the existing `MergeOverrides` / `MergeGlobalOverride`
pipeline unchanged. Two of the three fit cleanly. The third (reference
resolution under the file-local scoping rule) does not. Separately, the
`[x.required]` / `[x.recommended]` / `[x.optional]` pattern is replicated at
five schema locations without being factored, and `SourceFingerprint` has no
defined reduction semantics for mixed-source files.

---

## MUST-FIX

### M-1. `MergeGlobalOverride` cannot carry `vault://` URIs as opaque strings (R3/D-9 contradicts D-6).

**Concern.** D-6 says references ride the existing merge pipelines "with zero
new merge logic — last-writer-wins per key already handles vault references."
D-9/R3 says a URI like `vault://team/github-pat` is only meaningful in the file
that declared `[vault.providers.team]`. Those statements are incompatible. If a
team config puts `GITHUB_TOKEN = "vault://team/pat"` in `[env.vars]` and a
personal overlay leaves that key alone, the last-writer-wins merge in
`MergeGlobalOverride` (`internal/workspace/override.go:416`) hands the
*resolved string* (still `vault://team/pat`) to the materializer with no memory
of which file it came from. Resolution then happens in a context where "team"
may or may not mean the same provider — or may not exist, if the user's
personal overlay redefined `team` via R12's per-provider-name last-writer-wins
on `GlobalOverride.Vault.Providers`. R12 explicitly allows this ("replace a
team-declared provider for the same name"), and US-9 path 1 depends on it.

The hidden assumption is that every `vault://` value carries its origin file's
provider table. Once the merge flattens the config, that origin is lost.

**Fix.** Resolve `vault://` URIs to a `secret.Value` (or a typed `SecretRef`
handle) *before* merging, inside each source file's provider context. Post-
resolution, the merge pipeline carries already-resolved opaque values and its
last-writer-wins behavior is correct. This reverses the order the PRD
implies (merge → resolve becomes resolve → merge) but is the only way to
honor file-local scoping without a second "remember which file this string
came from" pass. Re-read D-6 in light of this: the merge pipeline stays
intact, but references must become typed values upstream of it. Alternative
is to annotate every `string` field in `EnvConfig` / `SettingsConfig` / `Files`
with a parallel "source file provider table" map — strictly worse.

### M-2. R12 `GlobalOverride.Vault.Providers` contradicts R3 cross-config reference prohibition.

**Concern.** R12 requires per-provider-name last-writer-wins across layers:
"personal can … replace a team-declared provider for the same name." R3
forbids a file from naming a provider that file didn't declare. US-9 path 1
shows a personal overlay writing `[workspaces.tsukumogami.vault.providers.team]`
to shadow a team provider also called `team`. This only works if the personal
config is *allowed to know the team's provider name* — which R3 says it
mustn't. The PRD tries to resolve this by saying the personal file declares
its own provider happening-to-be-named `team`, and the merge matches them by
string — but that is a rendezvous-by-name contract, which D-9 explicitly
rejected ("Rendezvous names leak").

**Fix.** Pick one: (a) drop R12's cross-file same-name merge entirely; force
contributors who need to replace a team provider to override individual
references (US-9 path 2), not the provider; (b) keep R12 but acknowledge that
provider names ARE a public contract across layers, and update D-9 to match.
Option (a) preserves the "file-local names stay local" invariant at cost of
some US-9 ergonomics. The PRD currently wants both; only one is consistent.

### M-3. Five-location schema duplication is a missing abstraction (R33/D-10).

**Concern.** The PRD adds `[required]` / `[recommended]` / `[optional]`
triplets at five locations: `[env.*]`, `[claude.env.*]`, `[repos.<name>.env.*]`,
`[instance.env.*]`, `[files.*]`. Each triplet is
`map[string]string` (key → description). That is fifteen new TOML tables and
five new code paths for "load a description map, diff against resolved keys,
emit error/warning/info." The existing schema already has parallel pain:
`ClaudeEnvConfig` is nearly identical to `EnvConfig` but not quite, and
`ClaudeOverride` was split from `ClaudeConfig` specifically to avoid schema
drift at override sites (see `config.go:39-51`). Adding fifteen more tables
without a common type compounds that drift.

**Fix.** Introduce a single `Requirements` struct
(`{Required, Recommended, Optional map[string]string}`), embed it in
`EnvConfig`, in a new `FilesConfig` wrapping `map[string]string`, and in
`ClaudeEnvConfig`. One validator, one diagnostic emitter, one merge path. The
five locations become five *uses* of one type, not five parallel schemas.
Without this, every future env-or-file-adjacent feature (v0.9 added
`[instance.env]`; v0.7 moved `[content]` under `[claude]`) will either miss
the pattern or replicate it again.

### M-4. `SourceFingerprint` for mixed-source files has no defined reduction (R15).

**Concern.** R15 says `ManagedFile.SourceFingerprint` captures "the resolution
inputs (config reference + vault version/etag metadata)." The `.local.env`
file materialized by `EnvMaterializer` is assembled from up to four sources
(see `materialize.go:399-452`): workspace-level `env.files` (plaintext on
disk), discovered workspace/repo env files, `env.vars` entries (plaintext or
vault refs), and overlay-merged `env.vars`. A single `.local.env` can contain
values from all four. What is the fingerprint? The PRD does not say. Options:
(a) hash of a canonicalized (source, version) tuple list — well-defined but
says nothing useful about which sub-source changed; (b) hash of only the
vault-resolved portion — can't distinguish "user edited plaintext line" from
"vault rotated"; (c) a map of `key -> (source, version)` written to state —
serializable but inflates `state.json`.

Without a defined reduction, status's three-way verdict
(`ok` / `drifted` / `stale`) is ambiguous the moment a file mixes sources.
That is the common case, not an edge case.

**Fix.** Specify the reduction in the PRD. Recommended shape: fingerprint is
hash of a stable-sorted list of `(source-id, version-token)` tuples, where
plaintext sources contribute `(file-path, content-hash)` and vault sources
contribute `(provider-name, vault-key, vault-etag)`. "Stale" then means at
least one tuple changed; distinguishing which requires storing the tuple list
not just the hash. Decide now whether to store the list (expensive, precise)
or just the rollup hash (cheap, ambiguous). Either is defensible; leaving it
unspecified is not.

---

## SHOULD-FIX

### S-1. Public-repo detection is a git concern, not a config concern (R14/R32).

**Concern.** R14/R32 requires niwa to detect "the config repo has a public
GitHub remote" and block apply. The config package (`internal/config/`) is a
TOML parser. Adding remote-URL classification to it pulls git-awareness into a
layer that currently has none. The adjacent question is where it would live:
`internal/config/` would have to import git-remote-parsing logic;
`internal/workspace/apply.go` is probably the right home but is higher-level.

**Fix.** Put the guardrail in `internal/workspace/apply.go` (or a dedicated
`internal/guardrail/` package), passing the detected remote as a parameter
into apply rather than inferring inside config-load. Keep `internal/config/`
ignorant of git. Also narrow R14's scope: it only detects GitHub HTTPS/SSH
URL patterns (explicitly noted in Out of Scope). The guardrail should be
clearly named (`githubPublicRemoteGuard`), not a generic
`publicRepoGuard`, so non-GitHub remotes don't silently pass a check they
weren't actually evaluated under.

### S-2. Interface `Resolve(key) -> Secret + metadata` is under-specified for R15 and future backends (D-1).

**Concern.** D-1 commits to a pluggable interface but sketches it as
`Resolve(key) -> Secret + metadata`. The security invariants and rotation
needs demand more:

- R15 needs a version/etag token in the metadata to compute
  `SourceFingerprint`. Sops decrypts a local file and has no natural etag
  (file mtime? content hash of the encrypted blob?); Infisical has a
  version field; 1Password has a version int; HashiCorp Vault has a lease
  + version; Bitwarden Secrets Manager has a `revisionDate`. Each backend
  synthesizes "version" differently and the interface has to admit that.
- R22 (`secret.Value`) argues the return type should be `secret.Value`,
  not `string` — the interface spec should name it.
- R29 / R31 (no `os.Setenv`, no unfiltered child env) mean the resolver
  should never hand a backend a mutable `context.Context`-with-env; the
  interface should take an explicit auth context.
- A 1Password plugin needs session-lifetime auth (the `op` CLI caches a
  session token per shell); sops needs none. The interface has to let
  backends opt into a lifecycle beyond single-call.

**Fix.** In the design-doc phase, expand the interface sketch to at least:

```go
type Provider interface {
    Name() string
    Kind() string
    Resolve(ctx context.Context, ref Ref) (secret.Value, VersionToken, error)
    Close() error // for session teardown; no-op for stateless backends
}
```

Where `VersionToken` is `string` (opaque per-backend) and `Ref` carries both
the key and the required-ness. The PRD doesn't need to spec the full
interface but should commit to those three return values (value + version +
error) so R15 is achievable and sops/Infisical/1Password/Vault can all fit.

### S-3. Reference-accepting locations list is an enumeration that will go stale.

**Concern.** R3 enumerates reference-accepting locations: `[env.vars]`,
`[claude.env.vars]`, `[repos.<name>.env.vars]`, `[instance.env.vars]`,
`[files]` source keys, `[claude.settings]` values. And excludes:
`[claude.content.*]`, `[env.files]`, `[vault.providers.*]`, identifier
fields. Six-item allow-list, four-item deny-list, inside a schema that's
still growing (v0.9 added `[instance]`, v0.7 moved `[content]`). Every new
string field will force a PRD amendment to classify it.

**Fix.** Invert the default. All string values accept `vault://` unless the
field is in a short deny-list. The deny-list is the structural truth — paths
and identifiers shouldn't be secrets. This also removes the need for a
schema-level list traversed by a resolver; the resolver just walks strings
and the small deny-list protects paths. Implementation: tag deny-list fields
via a struct tag (`niwa:"no-vault"`) checked by the resolver.

### S-4. `ResolveGlobalOverride` + `MergeGlobalOverride` produce a single `*WorkspaceConfig` with no per-layer provenance, breaking R3 scoping.

**Concern.** See M-1. Concretely: after `MergeGlobalOverride` at
`override.go:327`, the returned `*WorkspaceConfig` is a merged view with
team and personal values coexisting and no marker for which came from where.
The subsequent resolver has to reconstruct provenance from scratch or accept
that file-local scoping cannot be enforced post-merge.

**Fix.** Either carry a sibling `map[fieldpath]layerID` alongside the merged
config, or (preferred per M-1) resolve URIs to `secret.Value` before
`MergeGlobalOverride` runs. The latter preserves the layer boundary
naturally: by the time fields merge, they're typed secrets, not URIs.

---

## NIT

### N-1. `vault_scope` on zero-source workspaces (Q-5) is under-defined.

Open Question 5 in the PRD notes that `niwa init <name>` (no `--from`) leaves
a workspace with no `[[sources]]`. The implicit scope is empty. The PRD
leaves the default undecided. This is fine for a PRD but the design doc
should pick before implementation; my read is "no implicit scope, require
`vault_scope` explicitly if personal vault is used" is cleaner than "default
to workspace name" (workspace name and source-org are different namespaces
and conflating them in the rare case will confuse the common case).

### N-2. `raw:` prefix handling is a parse-layer concern, not a resolver concern (R17/D-8).

The `raw:` escape (`raw:vault://foo` → literal `vault://foo`) should be
unescaped during config parse, not during resolution. If it's pushed to
resolution, every reference-accepting location's resolver has to implement
the unescape. At parse, one place handles it. Small thing, but worth
pinning in the design doc.

### N-3. D-5 rationale ("vault is cross-cutting, not Claude-scoped") is load-bearing but understated.

D-5 places `[vault]` at the top level because references appear in
`[env.vars]`, `[claude.env.vars]`, `[repos.*.env.vars]`, `[files]`, and
`[claude.settings]`. That is the right call. But nothing in the PRD
formalizes it — a future maintainer could easily move `[vault]` under
`[claude]` for "symmetry with `[claude.content]`" and break references in
`[env.vars]` (which would have no access to a `[claude.vault]`). Add a
note under D-5: "Vault is a workspace-level concern because it's consumed by
at least two top-level tables (`[env]` and `[claude.env]`)."

---

## Fits the architecture cleanly

- The URI scheme over existing string slots (D-6) is the right instinct,
  *provided* M-1/M-2 are resolved. It keeps vault out of the schema's type
  system at the reference site.
- Fail-hard resolution (D-4) with explicit opt-outs matches the existing
  "error on unknown fields → warn" conservative default pattern.
- Re-resolution on every apply (R16/D-7) — correct; caching is a separate
  concern and provider CLIs already solve it.
- `0o600` + `.local` infix + `.gitignore` coverage (R24–R26) fits the
  existing `.local*` convention from settings and env materialization.

These are structurally aligned and don't need revision.
