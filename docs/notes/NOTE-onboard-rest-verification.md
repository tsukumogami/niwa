# NOTE: niwa onboard REST/CLI assumption verification

Phase 0 of DESIGN-niwa-onboard.md flagged three assumptions carried from the
superseded provision design and asked that they be checked against current,
authoritative sources before Phase 1 code depends on them. This note records
the findings. It is the durable artifact PLAN-niwa-onboard.md Issue 1 asks
for; downstream issues (2 onward) build against it instead of re-deriving
these paths from memory.

Verification method: current Infisical API reference pages fetched directly
(https://infisical.com/docs/api-reference/...) and the CLI docs page for
`infisical login status`, cross-checked against the locally installed
`infisical` CLI's `--help` output and a live `infisical login status --json`
run.

## Assumption A — Universal-Auth-attach and environment-grant landing checks

**Verdict: corrected.** Both checks are separate reads, but one of them is
free (piggybacks on a call the mint pipeline already makes) and the other
needs a dedicated read with a documented gap.

- **Universal-Auth-attach.** The read-identity call the mint pipeline already
  performs is `GET /api/v1/auth/universal-auth/identities/{identityId}`
  (see Assumption B). Its own success/failure *is* the attach check: a 200
  means Universal Auth is attached to the identity, a 404 means it isn't.
  No extra call is needed — this rides the same read-identity response the
  design anticipated it might ride, just not the org-level
  `GET /api/v1/identities/{identityId}` response (that endpoint does not
  expose auth-method-specific configuration, only a general `authMethods`
  array).
  Source: https://infisical.com/docs/api-reference/endpoints/universal-auth/retrieve
- **Environment-grant landing check.** No single read exposes this for free.
  A separate endpoint exists —
  `GET /api/v1/projects/{projectId}/memberships/identities/{identityId}`,
  authenticated with the operator's own session bearer — and returns an
  `identityMembership` object with a `roles` array (each entry has `role`,
  `customRoleId`/`customRoleName`/`customRoleSlug`, temporary-access fields).
  This confirms *project-level* role assignment readably. It does not expose,
  in an easily parseable form, which environments a custom role's permission
  conditions scope access to — that detail lives inside the custom role's
  permission rules, not in the membership response.
  Source: https://infisical.com/docs/api-reference/endpoints/project-identities-membership/get-by-id

  **Consequence for implementation:** wire the membership read for the
  project-level landing check (it is a real, working REST call, so use it
  rather than skipping straight to the fallback). Apply the
  trust-the-operator-claim fallback only for the finer-grained
  environment-scoping detail inside a custom role, which this endpoint does
  not surface readably.

## Assumption B — Management REST paths (read-identity, mint, revoke, R9 read-hop)

**Verdict: corrected.** Three of four paths differ from what the superseded
provision design carried forward; the fourth (mint) is confirmed.

| Call | Path pinned by this verification | Auth | Notes |
|---|---|---|---|
| read-identity | `GET /api/v1/auth/universal-auth/identities/{identityId}` | `Authorization: Bearer <operator token>` | Returns an `identityUniversalAuth` object; the field is `clientId` (camelCase), not `client_id`. This is the Universal-Auth-specific retrieve endpoint, not the generic `GET /api/v1/identities/{identityId}` (which returns org-level identity metadata and an `authMethods` array but no `clientId`). |
| mint-client-secret | `POST /api/v1/auth/universal-auth/identities/{identityId}/client-secrets` | `Authorization: Bearer <operator token>` | Confirmed as carried. Response has `clientSecret` (the secret value) and `clientSecretData.id` (the non-secret identifier — this `id` field is what R20 capture stores as the mint record's secret reference, not a field literally named `secret_id`). |
| revoke | `POST /api/v1/auth/universal-auth/identities/{identityId}/client-secrets/{clientSecretId}/revoke` | `Authorization: Bearer <operator token>` | **Correction:** this is a `POST .../revoke` action endpoint, not a `DELETE` verb as the carried assumption stated. No request body. |
| R9 read-hop (`ReadEnvironmentSecrets`) | `GET /api/v4/secrets` with query params `projectId`, `environment`, `secretPath` (defaults to `/`) | `Authorization: Bearer <minted pair's access token>` | Current, non-deprecated endpoint. The v3 equivalent (`GET /api/v3/secrets/raw` and `GET /api/v3/secrets/raw/{secretName}`) now lives under the docs' `deprecated/` section — do not target it. All identifying values (`projectId`, `environment`, `secretPath`) are non-secret query parameters; the access token is header-carried only, confirming the no-argv requirement. |

Sources: https://infisical.com/docs/api-reference/endpoints/universal-auth/retrieve,
https://infisical.com/docs/api-reference/endpoints/universal-auth/create-client-secret,
https://infisical.com/docs/api-reference/endpoints/universal-auth/revoke-client-secret,
https://infisical.com/docs/api-reference/endpoints/secrets/read,
https://infisical.com/docs/api-reference/endpoints/deprecated/secrets/read

**Consequence for implementation:** `ReadIdentity` must target the Universal
Auth retrieve path (not the generic identity path) so it doubles as the
attach check. `RevokeClientSecret` must issue a `POST .../revoke`, not a
`DELETE`. `ReadEnvironmentSecrets` must target `GET /api/v4/secrets`, not a
v3 raw path. R20's captured field should be described as "the mint record's
`id`," not `secret_id`, to match the actual response shape.

## Assumption C — `infisical login status` parseability for org context

**Verdict: confirmed.** `infisical login status --json` exists, is
documented, and was exercised locally against a real authenticated session.
The command's own `--help` text states it "reports whether the CLI is
authenticated to Infisical and, when available, the organization the active
session is scoped to." A live run against an authenticated session returned
a JSON object with a top-level `sessions` array; each entry carries
`principalType`, `status`, `domain`, `authMethod`, `tokenSource`,
`organization` (the org identifier), and a nested `verification.state`. The
`organization` field is exactly the parseable org-context signal the wizard
needs.

Source: https://infisical.com/docs/cli/commands/login (documents the `--json`
flag and the human-readable equivalent, which the docs show rendering an
`Organization: <id>` line) plus a local `infisical login status --json` run
against the locally installed CLI (version 0.43.101).

**Consequence for implementation:** the wizard's detection funnel can use
`infisical login status --json` directly to read org context rather than
falling back to classifying the management call's own error. The
classify-the-error fallback should still be implemented for the case where
the session is a machine-identity token or an unauthenticated/expired
session — the design's own local-first detection funnel already treats an
identity-not-found or identity-found-no-credential result as a routing
signal, and `login status` reporting `"status": "authenticated"` with no
`organization` field (or a non-zero exit) is the trigger for that fallback
path.

## Summary of path corrections for downstream issues

- `ReadIdentity` targets `GET /api/v1/auth/universal-auth/identities/{identityId}`,
  not the generic identity endpoint.
- `RevokeClientSecret` issues `POST .../client-secrets/{id}/revoke`, not a
  `DELETE`.
- `ReadEnvironmentSecrets` targets `GET /api/v4/secrets`, not a v3 raw path.
- The mint response's non-secret capture field is `clientSecretData.id`.
- The environment-grant landing check has a working project-level REST read
  (`GET /api/v1/projects/{projectId}/memberships/identities/{identityId}`);
  only the environment-scoped nuance inside a custom role falls back to
  trusting the operator's claim.
- `infisical login status --json` is confirmed parseable for org context;
  the classify-the-error fallback remains for sessions without a readable
  `organization` field.
