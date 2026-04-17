# Decision 2: JWT Caching Strategy

**Question:** Should niwa cache the short-lived JWT obtained from Infisical's
universal-auth, or re-authenticate on every apply?

**Chosen:** Option 1 -- No cache, re-auth every apply.

**Confidence:** High

## Analysis

### Option 1: No cache -- re-auth every apply

Each `niwa apply` performs one `POST /api/v1/auth/universal-auth/login` per org
that supplies credentials. For a typical multi-org user (2-3 orgs), that adds
200-300ms total to the apply.

**Strengths:**
- Zero new code paths for file I/O, TTL parsing, or cache invalidation.
- No JWT on disk -- the token exists only in process memory for the duration of
  one apply. Eliminates an entire class of bugs (stale cache, corrupt file,
  wrong permissions, leaked token on shared machine).
- Fully stateless across invocations. No cleanup, no migration, no cache
  directory to create.
- The credential file (`provider-auth.toml`) already stores client_id +
  client_secret. Adding a JWT cache file per org is a second layer of secrets
  on disk with marginal benefit.

**Weaknesses:**
- 100ms per org per apply. At 2-3 orgs, that is 200-300ms overhead on every
  apply. Acceptable given `niwa apply` already shells out to `infisical export`
  (network-bound, typically 300-800ms per provider).

### Option 2: File-based JWT cache

Cache to `~/.config/niwa/tokens/<project-id>.jwt` at 0o600. Parse the JWT `exp`
claim (base64-decode the payload segment, unmarshal to get `exp`, compare to
`time.Now().Unix()`). Use cached token if not expired; re-auth if expired.

**Strengths:**
- Near-zero auth latency on cache hit (~99.99% of applies with 30-day TTL).
- Saves 200-300ms per apply for multi-org users.

**Weaknesses:**
- File management: create directory, write atomically, set permissions, handle
  races between concurrent applies.
- JWT parsing: base64 decode + JSON unmarshal of the payload segment. Stdlib
  only (no new deps), but still ~40 lines of code that must handle malformed
  tokens, clock skew, and missing `exp` claims.
- Stale-token recovery: if the JWT is revoked server-side before expiry (admin
  rotated the machine identity), niwa must detect the 401 from `infisical
  export`, evict the cache, re-auth, and retry. That retry loop is new
  complexity.
- Security surface: a plaintext JWT with 30-day TTL sitting on disk. The threat
  model accepts same-user processes reading 0o600 files, but the JWT grants
  read access to all secrets in that org's projects -- broader blast radius than
  the per-project `infisical export` call it replaces.
- More test surface: cache-hit path, cache-miss path, expired-token path,
  corrupt-file path, permission-error path, concurrent-write path.

### Option 3: In-memory cache only (process lifetime)

Cache the JWT within a single `niwa apply` invocation. Multiple providers
sharing the same org reuse one token.

**Strengths:**
- Saves a redundant auth call when two providers are in the same org.
- No files, no persistence.

**Weaknesses:**
- Marginal benefit. Most multi-org users have one provider per org. The saving
  is one auth call (~100ms) in the uncommon case of multiple providers per org.
- Still re-auths across applies (the common case).
- Adds a token-registry data structure to track org-to-JWT mappings within a
  single apply. Small but non-zero complexity for a rare scenario.

### Option 4: Delegate to `infisical login --method=universal-auth`

Let the Infisical CLI manage token caching in `~/.infisical/`.

**Strengths:**
- Zero token-management code in niwa.

**Weaknesses:**
- Overwrites the stored session. A user logged into their personal org via
  `infisical login` loses that session when niwa runs `infisical login
  --method=universal-auth` for a different org. This breaks the single-org
  default path -- the exact scenario the design must protect.
- Couples niwa to undocumented Infisical CLI session-storage internals.
- Eliminated during exploration (Lead 1 findings).

## Rationale

The 200-300ms overhead of re-auth is small relative to the total `niwa apply`
latency (which includes multiple `infisical export` subprocess calls, each
network-bound at 300-800ms). The absolute wall-clock cost is under 5% of a
typical multi-org apply.

The complexity cost of file-based caching is disproportionate to the latency
saved. It introduces a new secrets-on-disk surface, a retry-on-revocation loop,
file permission management, and six new test paths -- all to save a fraction of
a second on an operation the user runs interactively.

If latency becomes a user-reported problem (e.g., someone with 10+ orgs), a
file-based cache can be added as a backward-compatible enhancement. The auth
layer's interface (return a JWT string for a given org's credentials) does not
change -- only its internal implementation. This makes "no cache now, cache
later if needed" a safe incremental path.

In-memory caching (Option 3) could be added opportunistically if multiple
providers per org becomes common, but it is not worth the complexity today.

## Decision

**No cache. Re-authenticate on every apply.**

The auth layer reads client_id + client_secret from `provider-auth.toml`,
performs a single HTTP POST per org, and returns the JWT as an in-memory string.
The JWT is passed to `infisical export --token` and discarded when the apply
completes. No token touches disk. No cache invalidation logic. No retry loop.

### Deferred work

If multi-org users report that auth latency is a bottleneck (likely threshold:
5+ orgs), add file-based JWT caching as a follow-up. The auth-layer interface
will not change -- only the internal implementation gains a cache-check before
the HTTP POST.
