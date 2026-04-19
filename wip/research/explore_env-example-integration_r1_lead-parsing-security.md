# Research: dotenv parsing + security hazards

## Parsing requirements

Real-world `.env` / `.env.example` files include:

- **Quoted values**: single-quoted = literal; double-quoted with `\n`, `\t`, `\"` escapes.
- **Multiline values**: backslash line continuation; double-quoted blocks spanning lines.
- **Variable expansion**: `${VAR}`, `${VAR:-default}`, and the rarer `$VAR`.
- **Comments**: full-line `#` prevalent; inline `# comment` after value is supported by some parsers.
- **`export` prefix**: `export KEY=VALUE` should be treated like `KEY=VALUE`.
- **Empty values**: `KEY=` (empty string) vs. omitting the line entirely.
- **Line endings**: CRLF common on Windows-authored files; must be handled.
- **UTF-8 BOM**: some editors add one; tolerate silently.

niwa's current `parseEnvFile()` in `internal/workspace/materialize.go` (~lines 727-746) is minimal: line-based, skip blanks and `#` comments, simple `KEY=VALUE` split. No quote handling, no expansion, no `export`.

## Go library options

| Library | Stars | Last release | Notes |
|---------|-------|--------------|-------|
| `github.com/joho/godotenv` | ~10.4k | Feb 2023 | Stable, feature-complete. Supports `export`, double-quoted multiline, `${VAR}` expansion. ~50 open issues but mostly feature requests. |
| `github.com/subosito/gotenv` | ~307 | Aug 2023 | Actively maintained. `Parse` + `StrictParse` variants, variable expansion. Small API surface. |

**Recommendation:** niwa should not vendor a dependency for this. Reasons:
1. niwa already has a working parser for the existing `[env].files` path.
2. `.env.example` files are read-only stubs — we don't need `godotenv`'s `Load()` behavior.
3. Secret-detection logic has to be custom regardless; might as well keep the parser close.
4. Upgrading the dedicated parser to handle quoted values + `export` + basic expansion is ~50 lines.

## Stub-vs-secret detection

The codespar example ships `pk_test_Y2FyaW5...` which is a legitimate Clerk publishable test key — safe to commit by Clerk's design. A generic parser can't tell that apart from a real `sk_live_…` secret. Three candidate approaches:

1. **Known-prefix blocklist** (`sk_`, `AKIA`, `ghp_`, `github_pat_`, `gitlab_pat_`, `xoxb-`, `xoxp-`, Slack app tokens, Bitbucket, GCP service account JSON markers).
   - Simple, explicit.
   - Requires maintenance as new vendors appear.
   - Catches the obvious cases; misses custom/vendor-unknown secrets.

2. **Shannon entropy threshold** (~3.0 bits/char, used by truffleHog and gitleaks).
   - Practical and battle-tested.
   - Low false-positive rate.
   - Can miss structured secrets (JWT, base32 tokens).

3. **Hybrid: entropy + safe-prefix allowlist** (`pk_test_`, `test_`, `EXAMPLE_`).
   - Entropy catches unknown secret shapes.
   - Allowlist permits intentional test keys and explicit placeholders.
   - Smallest false-positive rate; most UX-friendly for `.env.example` which deliberately contains stubs.

**Recommended:** Option 3. Use entropy as primary signal; allowlist known-safe prefixes to permit intentional example values. Document that `.env.example` is for stubs only; real values belong in the vault or a gitignored local override.

## Guardrail implications

R23 in `PRD-vault-integration.md` rejects `niwa apply` when a public-GitHub remote still has plaintext in `[env.secrets]`. Today that walk covers only in-memory values from parsed TOML.

For a `.env.example` integration:
- Values read from `.env.example` end up in `.local.env` (in the managed repo's workspace), not the config repo. R23's write-back concern is already respected.
- But: the public-repo *plaintext-secret* concern still applies if the managed repo is public. A plaintext secret read from a public repo's `.env.example` and materialized into its `.local.env` would be a new leak surface — even if `.local.env` is `.gitignore`d, the value itself came from a public file.
- The guardrail's existing walk should extend to values sourced from `.env.example`. If entropy/prefix detection flags a value as probably-secret, apply fails with an error pointing at the `.env.example` line and recommending moving the key to `[env.secrets.required]` plus a vault ref.
- An escape hatch paralleling `--allow-plaintext-secrets` would cover the one-shot cases where a value flagged as secret is actually a known-safe example (e.g., a very high-entropy test key).

## Summary

- niwa's current parser handles the lowest-common-denominator case; needs modest upgrade (~50 LOC) for real-world `.env.example` files.
- Don't vendor `godotenv` or `gotenv` — not worth the dependency.
- Stub detection: Shannon entropy threshold + safe-prefix allowlist is the most ergonomic approach.
- The public-repo guardrail must extend to `.env.example` content when the managed repo is public; materialization should refuse probably-secret plaintext unless an explicit escape hatch is invoked.
