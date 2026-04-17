# Exploration Findings: vault-multi-org-auth

## Round 1

### Key Insights

1. **`--token` is the designed multi-org mechanism.** The Infisical CLI's
   `--token` flag is per-command and does NOT mutate the stored session
   (Lead 1). It works without any stored `infisical login` session.
   This means the backend can pass `--token <jwt>` for multi-org providers
   while the CLI's stored session handles the single-org default — zero
   conflict between the two paths. (Lead 1)

2. **Universal-auth tokens are cheap to obtain and long-lived.** A single
   HTTP POST to `/api/v1/auth/universal-auth/login` with
   client_id + client_secret returns a JWT with 30-day default TTL. ~100ms
   latency per call. Tokens can be cached locally and reused across applies
   until expiry. (Lead 3)

3. **The AWS named-profiles pattern is the closest precedent.** `~/.aws/credentials`
   stores per-profile access keys at 0o600; `--profile` selects per command;
   no profile = default credentials. Every surveyed multi-account tool uses
   the same three-layer model: local credential store → per-command selection
   → session/default fallback. (Lead 2)

4. **The backend change is ~20 lines across 2 files.** Add an optional
   `token` field to `Provider`, read from `ProviderConfig["token"]` at
   `Factory.Open`, and conditionally append `--token <jwt>` in
   `runInfisicalExport` args. No interface changes, no test-hook changes,
   fully backward compatible. (Lead 5)

5. **Two-layer storage: credentials file + JWT cache.** Long-lived
   machine-identity credentials (client_id + client_secret) in
   `~/.config/niwa/provider-auth.toml` at 0o600. Short-lived JWTs cached
   in `~/.config/niwa/tokens/` at 0o600, auto-refreshed by niwa on expiry.
   Personal overlay repo is REJECTED as a credential store. (Lead 4)

### Tensions

- **Credential file vs "no new secrets on disk" philosophy.** The vault
  feature was built to eliminate plaintext secrets from repos. Putting
  machine-identity credentials in a local config file is plaintext-on-disk.
  The mitigation: the file is local-only (never committed), 0o600, and
  same-user-process access is already out of the PRD's threat model. The
  JWT cache layer further limits exposure — if niwa only stores the
  short-lived JWT and obtains it on demand from the credential file, the
  JWT expires naturally even if leaked. But the credential file itself
  (client_secret) is long-lived and doesn't expire by default.

- **Where does niwa read the credential file — at provider build time or
  at resolve time?** If the credential file is read at `BuildBundle` time
  (before any provider is opened), niwa can inject the token into
  `ProviderConfig` before the Factory sees it. If read at resolve time,
  the Provider needs a callback. Lead 5 suggests reading at Factory.Open
  via ProviderConfig — simpler, no callback needed. But that means
  `BuildBundle` (or its caller in `apply.go`) needs to read the credential
  file and merge tokens into configs. The credential-file-reading logic
  lives outside the Infisical backend — in niwa's resolver or apply layer.

### Gaps

- **No investigation of `niwa vault auth` or `niwa vault login` UX** for
  bootstrapping the credential file. How does a multi-org user SET UP the
  credentials? Manual TOML editing? A `niwa vault auth add` command?
  This affects adoption friction.

- **Token caching implementation details.** Where exactly in `~/.config/niwa/`?
  What filename convention? How does niwa know which cached JWT maps to
  which provider? JWT `exp` claim parsing in Go? These are design-doc-level
  details, not exploration-level.

- **CI story.** CI uses `INFISICAL_TOKEN` env var today. Does the
  multi-org auth change affect CI at all? Probably not — CI uses a single
  machine identity per workflow. But worth confirming.

### Convergence Assessment

The problem space is well-understood. All five leads converged on the
same architecture: local credential file + `--token` per invocation +
CLI session fallback. The tensions are tractable (credential-file
location is a design choice, not an open question). The gaps (bootstrap
UX, caching details) are implementation concerns for a design doc, not
exploration questions.

## Decision: Crystallize
