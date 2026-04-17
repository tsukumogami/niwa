# Lead 3: Infisical Universal-Auth Token Lifecycle

## Token Acquisition

- **Endpoint**: `POST /api/v1/auth/universal-auth/login`
- **Request**: JSON with `clientId`, `clientSecret`, optional `organizationSlug`
- **Response**: `accessToken`, `expiresIn`, `accessTokenMaxTTL`, `tokenType: Bearer`
- **Single HTTP call**, no browser interaction. ~100ms latency.

## Token TTL and Defaults

- **Access Token TTL**: Default 2,592,000 seconds (30 days), configurable per identity
- **Access Token Max TTL**: Default 2,592,000 seconds (30 days)
- **Client Secret TTL**: Default 0 (never expires)
- Optional "Access Token Period" enables renewable tokens for "secret zero" scenarios

## Refresh Mechanism

- **No refresh token**. Tokens are valid for their full TTL without renewal calls.
- Upon expiration, must obtain a new token via the login endpoint.
- With the optional period setting, tokens can be renewed via API before expiry.

## Feasibility for niwa

- Login call is ~100ms HTTP POST — acceptable for 1-3 orgs per apply.
- 30-day default TTL makes local caching viable: store JWT at 0o600, check `exp` claim before use.
- niwa can cache tokens per org in `~/.config/niwa/tokens/<org-slug>.jwt`.
- On apply: check cache → if valid, use cached JWT → if expired, re-authenticate → pass `--token` to export.

## Mid-Apply Token Expiry

- Export call is atomic: token validated at command start only.
- If token expires mid-apply (extremely unlikely with 30-day TTL), next provider call would fail with 401.
- Mitigation: check `exp` claim before each provider invocation; if < 5 min remaining, re-fetch.

## CLI Token Injection

- `infisical export --token <jwt>` fully bypasses stored session per invocation.
- `INFISICAL_TOKEN` env var provides the same override.
- Both work without any stored `infisical login` session.
- `infisical login --method=universal-auth --silent --plain` returns the JWT on stdout (alternative to HTTP call).

## Key Takeaway

niwa can obtain tokens programmatically via a single HTTP POST or
`infisical login --method=universal-auth --silent --plain`. With 30-day
TTL and local caching, the authentication cost is near-zero for
subsequent applies. The `--token` flag is the designed mechanism for
multi-context use.
