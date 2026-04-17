# Decision 3: Token injection layer

## Question

Which layer reads the credential file (`~/.config/niwa/provider-auth.toml`)
and injects the authentication token into the Infisical backend's subprocess
args?

## Chosen option: A -- apply.go reads credential file, injects token into ProviderConfig

## Confidence: high

## Rationale

The current code has a clean, well-separated flow:

1. `apply.go` orchestrates the pipeline: it already reads the global config
   overlay file (`~/.config/niwa/niwa.toml`) at lines 296-308, builds bundles
   via `resolve.BuildBundle`, and runs collision/shadow checks before
   resolution. Adding credential-file reading here follows the established
   pattern.

2. `resolve.BuildBundle` and `specsFromRegistry` are pure transformers: they
   convert `config.VaultRegistry` into `[]vault.ProviderSpec` and hand those
   to `Registry.Build`. They have no filesystem dependencies beyond what the
   caller passes in. Adding credential-file reading here (Option B) would
   break that property.

3. `Factory.Open` in `internal/vault/infisical/infisical.go` is deliberately
   non-blocking and filesystem-free. It reads typed keys from `ProviderConfig`
   (`project`, `env`, `path`, `name`, `_commander`) and returns immediately.
   The subprocess runs lazily in `ensureLoaded`. Having each Factory read the
   credential file (Option C) would duplicate logic across backends and add N
   file reads.

4. Option D (AuthProvider interface) adds a new abstraction layer and an
   untyped `_auth` convention in ProviderConfig. For a standard-tier decision
   this is overengineered. If a second backend needs the same pattern later,
   we can extract the interface then.

### How the token flows under Option A

```
apply.go
  |-- read ~/.config/niwa/provider-auth.toml (once)
  |-- for each ProviderSpec from specsFromRegistry:
  |     look up credentials by (kind, project)
  |     obtain JWT (HTTP POST or cached)
  |     set spec.Config["token"] = jwt
  |
  +-- resolve.BuildBundle(ctx, nil, cfg.Vault, "workspace config")
        |-- specsFromRegistry(vr, sourceFile) -> []vault.ProviderSpec
        |     (specs already carry token in Config map)
        +-- vault.Registry.Build(ctx, specs)
              +-- for each spec: Factory.Open(ctx, spec.Config)
                    |-- reads config["token"] (new key)
                    +-- stores token on Provider struct
                          |
                          ensureLoaded -> runInfisicalExport
                            args = ["export", "--token", jwt,
                                    "--projectId", project, ...]
```

### Key implementation notes

- `apply.go` already reads `~/.config/niwa/niwa.toml` at line 297. Reading
  `provider-auth.toml` from the same directory is consistent.
- The credential file is read ONCE, before any bundle building. JWT
  acquisition (which may involve an HTTP call) happens once per unique
  (kind, project) pair, not once per provider spec.
- `Factory.Open` gains one new optional key (`"token"`). When present, it
  stores the value on the Provider struct. `runInfisicalExport` prepends
  `--token <jwt>` to the args when the field is non-empty.
- The token value is a JWT (a secret). It must be registered on the
  ctx-scoped Redactor so error messages don't leak it. apply.go should
  register it before setting it on ProviderConfig.
- `--token` on argv is acceptable per R21 because it's a short-lived JWT
  (minutes), not a long-lived credential. The credential file itself
  contains a client-secret that never reaches argv.

### What stays unchanged

- `resolve.BuildBundle` and `specsFromRegistry`: no changes needed.
- `vault.ProviderConfig` type: already `map[string]any`, accommodates the
  new `"token"` key without schema changes.
- `vault.Factory` interface: unchanged.
- `commander` interface and `defaultCommander`: unchanged (they already
  accept arbitrary args).

## Alternatives considered

| Option | Why not |
|--------|---------|
| B (resolve reads cred file) | Adds filesystem dependency to a currently-pure transformer package. |
| C (Factory.Open reads cred file) | N reads per apply, duplicates logic across backends, breaks the non-blocking Open contract. |
| D (AuthProvider interface) | Overengineered for v1. Can extract later if a second backend needs it. |
