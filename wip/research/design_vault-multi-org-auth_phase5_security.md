# Security Review: vault-multi-org-auth

Status: Complete
Reviewer role: Security researcher
Date: 2025-04-15

## Dimension 1: Credential file permissions (0o600)

**Finding: Gap -- enforcement is read-time reject only, no guidance on creation.**

The design specifies that `loadProviderAuth` returns an error when the file
is not 0o600. This is the right call -- it prevents niwa from silently
reading a world-readable credential file.

However, the design does not address:

1. **Race between creation and permission fix.** A user who creates the file
   with their editor (or `touch`) gets the umask default (typically 0o644).
   There is a window between creation and `chmod 0o600` where other users on
   a shared system can read the file. This is mitigated on single-user
   machines (the common case), but worth a documentation note.

2. **No creation helper.** Without a `niwa vault auth init` command (deferred
   to post-v1), users must know to `touch && chmod 0o600` before editing.
   The documentation walkthrough should include this explicitly, ideally
   with a one-liner: `install -m 0o600 /dev/null ~/.config/niwa/provider-auth.toml`.

3. **Parent directory permissions.** If `~/.config/niwa/` is world-readable
   (0o755, which is common for XDG config dirs), a user who creates the file
   at 0o644 by mistake has their secrets exposed until niwa rejects it on
   next apply. The file-level check is sufficient per the threat model
   (same-user processes out of scope; other-user access is covered by 0o600),
   but the doc should note that niwa does NOT check directory permissions.

**Severity:** Low. The threat model explicitly excludes multi-user attacks, and
single-user machines are unaffected. The documentation gap is the real issue.

**Recommendation:** Add a documentation note to Phase 3. No design change needed.

## Dimension 2: JWT on subprocess argv

**Finding: Acceptable per threat model, but worth documenting the exposure window.**

`--token <jwt>` is visible via `ps aux` for 300-800ms per subprocess
invocation. The JWT has a default TTL of 30 days (Infisical's universal-auth
default). An attacker who can read /proc or run `ps` on the same user can
capture the JWT and use it for up to 30 days.

The parent design's Security Considerations (Explicit Non-Scope) states:
"Malicious same-user processes. Can read 0o600 files the user owns."
Since same-user `ps` inspection is strictly weaker than reading 0o600 files,
this is within the accepted threat model.

**Environment variable alternative.** The design notes that env vars are
"equally visible to same-user processes." This is correct on Linux
(`/proc/<pid>/environ` is readable by the owning user). However, env vars
are NOT visible via `ps aux` (only argv is). This is a marginal improvement
in the casual-observer scenario but irrelevant for the defined threat model.

The `infisical` CLI does support `INFISICAL_TOKEN` env var. If a future
revision wants to reduce the `ps` surface, passing the token via
`cmd.Env = append(os.Environ(), "INFISICAL_TOKEN="+jwt)` would work. But
this creates tension with the parent design's invariant 3: "Inherited env
is passed through unchanged." The current argv approach is simpler and
consistent.

**Severity:** Informational. No change needed.

## Dimension 3: HTTP POST to custom domains

**Finding: Gap -- design hardcodes app.infisical.com; no self-hosted support.**

The design says `authenticateProvider` POSTs to
`https://app.infisical.com/api/v1/auth/universal-auth/login`. The Security
Considerations section mentions "(or the configured domain)" in passing,
but the Solution Architecture section does not define any mechanism for
configuring a custom domain.

The existing Infisical backend code also hardcodes `app.infisical.com` --
the `infisical` CLI handles self-hosted routing internally via its own
config. But for the new HTTP POST in apply.go (which bypasses the CLI),
niwa would need to know the correct API URL.

Options:
- Add an optional `api_url` field to provider-auth.toml entries.
- Derive the URL from the `infisical` CLI's stored config.
- Defer self-hosted support entirely (document the limitation).

**Severity:** Medium for self-hosted users, N/A for SaaS users. If no
self-hosted users exist in the near term, deferral is fine. But the
parenthetical "(or the configured domain)" should either be backed by
a real mechanism or removed to avoid implying support that does not exist.

**Recommendation:** Either add `api_url` to the credential schema or remove
the "(or the configured domain)" claim. This is a design change if the
former, a doc fix if the latter.

## Dimension 4: Error scrubbing of client_secret

**Finding: Adequate with one caveat.**

`secret.Errorf` constructs a `secret.Error` whose `Error()` method runs
every registered fragment through a `Redactor` that replaces them with
`***`. This works well when the client_secret is passed as a `secret.Value`
argument to `Errorf`.

The design says `authenticateProvider` wraps errors via `secret.Errorf`.
For this to scrub the client_secret from HTTP response bodies, the
implementation must:

1. Pass the client_secret as a `secret.Value` argument (not just a string).
2. Ensure the HTTP response body is included in the error message AFTER
   the `secret.Error` wrapping (so the Redactor sees it).

**Caveat: Infisical echoing the client_secret.** If Infisical's error
response body contains the client_secret (e.g., "invalid credentials for
client b67723bd..."), the Redactor will scrub it -- provided the full
client_secret string appears as a registered fragment. Partial matches
(truncated secrets in the response) would not be scrubbed. This is an
inherent limitation of substring-based redaction and is acceptable.

However, if the HTTP response body is logged or returned OUTSIDE the
`secret.Errorf` path (e.g., a debug log, a panic, or an unintended
code path that formats the raw HTTP response), the scrubbing would be
bypassed. The implementation must ensure ALL error paths from
`authenticateProvider` go through `secret.Errorf`.

**Severity:** Low. The `secret.Errorf` mechanism is sound. The risk is
implementation error (forgetting to wrap a path), not a design gap.

**Recommendation:** Add an acceptance test that verifies the client_secret
does not appear in any error output when the HTTP POST fails (mock a 401
response that echoes the client_secret back).

## Dimension 5: Fallback behavior and privilege escalation

**Finding: No silent degradation. Fallback is to less-privileged, not more.**

When no credential file exists, niwa falls back to the Infisical CLI's
stored session. This session is scoped to ONE org (whichever the user
last logged into). The multi-org credential file ADDS per-provider
machine-identity tokens that are typically scoped to a single project
(Infisical machine identities are project-scoped).

The privilege ordering is:
- CLI session: user-level access to one org (potentially all projects).
- Machine identity JWT: scoped to one project within one org.

So the fallback (CLI session) could be MORE privileged than the explicit
token in terms of project breadth within the logged-in org, but LESS
privileged in terms of cross-org reach.

**Potential scenario:** User has credential file entries for orgs A and B.
Org A's entry has a machine identity scoped to project X only. If the
credential file is deleted or becomes unreadable (wrong permissions), niwa
falls back to the CLI session. If the CLI session is logged into org A
with full user privileges, the `infisical export` command now runs with
broader access than the machine identity had.

This is not a security vulnerability -- the user already has those
privileges. It is a principle-of-least-privilege regression that happens
silently. The user gets no warning that they are now using the CLI session
instead of the scoped machine identity.

**Severity:** Low. Not a vulnerability per se, but silent behavior change.

**Recommendation:** Document this in the Security Considerations section.
Consider logging a warning when a provider that previously used a token
falls back to the CLI session (requires state tracking -- may be overkill
for v1).

## Dimension 6: R21 compliance (client_secret never on argv)

**Finding: Compliant.**

R21 states secrets must never reach subprocess argv. The design correctly
routes the client_secret through an HTTP POST body (`net/http` +
`encoding/json`), never through a subprocess. The client_id is also sent
via HTTP POST, not argv.

The JWT (which is a derived credential, not the client_secret itself) does
appear on argv (`--token <jwt>`). The parent design's R21 invariant
table says: "Infisical and sops subprocess invocations read auth from
provider-CLI env/keychain, never from argv." This was written before the
multi-org design introduced `--token`. There is a tension:

- The client_secret (the stored long-lived credential) never touches argv.
  R21 compliant.
- The JWT (a derived, time-bounded credential) is on argv. This is a new
  pattern not covered by the R21 invariant as originally written.

The parent design should update R21's "Realized by" column to acknowledge
that the multi-org path passes a derived JWT on argv, while the
client_secret remains off-argv.

**Severity:** Low. The spirit of R21 (protect long-lived secrets from
/proc exposure) is preserved. The letter of R21's invariant table needs
a minor update.

**Recommendation:** Update the parent design's R21 row in the Invariant
Coverage table when implementing the multi-org feature.

## Summary

| Dimension | Severity | Action |
|-----------|----------|--------|
| 1. File permissions | Low | Document creation best practice |
| 2. JWT on argv | Info | No change |
| 3. Custom domains | Medium | Design change or doc fix |
| 4. Error scrubbing | Low | Add acceptance test |
| 5. Fallback privilege | Low | Document in Security Considerations |
| 6. R21 compliance | Low | Update parent invariant table |

## Chosen Option: OPTION 1 -- Design changes needed

Dimension 3 (custom domain support) is the only item that requires a
design-level decision. The design currently claims self-hosted support
("or the configured domain") without defining the mechanism. This needs
either:

(a) Add an optional `api_url` field to the `[[providers]]` schema in
    provider-auth.toml, with `https://app.infisical.com` as the default.
    ~5 lines of additional code in `authenticateProvider`.

(b) Remove the "(or the configured domain)" parenthetical and document
    that self-hosted Infisical is not supported in v1. Add a future-work
    note.

All other findings are documentation additions or test requirements that
fit within the existing design structure.

## Blocking Issues

None. Dimension 3 is a design gap but not a blocker -- it can be resolved
by choosing option (a) or (b) above. No security vulnerability exists in
the proposed design.
