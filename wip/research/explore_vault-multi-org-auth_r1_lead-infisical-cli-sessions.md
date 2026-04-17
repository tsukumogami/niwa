# Lead 1: Infisical CLI Session Storage and Token Handling

## Session Storage

- Sessions stored in `~/.infisical/infisical-config.json`
- Config contains: logged-in user email, API domain, user list (minimal metadata)
- The actual JWT/auth token uses the OS keyring system (not plaintext files)
- CLI respects `INFISICAL_TOKEN` env var for token-based auth

## Session Scoping

- Sessions are **per-organization**: `infisical login` creates one session scoped to one org
- Only ONE active login session at a time per machine
- The `--organization-id` flag on login switches org context

## `--token` Flag Behavior

- `--token` on `infisical export` accepts service tokens or machine identity access tokens
- The flag is **per-command** — it does NOT persist or overwrite the stored session
- The warning "Your logged-in session is being overwritten by the token provided from the --token flag" is per-invocation only
- `infisical export --token <JWT> --projectId <id>` works **without any stored session** (confirmed by CI integration tests using `INFISICAL_TOKEN`)
- `INFISICAL_TOKEN` env var provides the same per-command override

## Key Implications for Multi-Org

- To access multiple orgs, you MUST use `--token` or `INFISICAL_TOKEN` — NOT multiple `infisical login` sessions
- `--token` is the designed multi-context mechanism
- niwa's current implementation correctly does NOT mutate the stored session (`cmd.Env = nil` inherits parent env)
- The single-org path (`infisical login` → CLI session) stays untouched; multi-org adds `--token` per invocation
