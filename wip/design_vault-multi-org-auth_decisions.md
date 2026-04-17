# Design Decisions Log: vault-multi-org-auth

## Phase 0: Setup

### D0.1 Execution mode confirmed as --auto
**Evidence:** User instruction "proceed with the /design in --auto mode"
**Decision:** Follow research-first protocol at all decision points.

### D0.2 Stay on current branch
**Evidence:** User instruction "stay-on-branch"
**Decision:** Continue on `docs/vault-multi-org-auth`.

### D0.3 Rich exploration context available
**Evidence:** Complete /explore with 5 leads, findings, and crystallize
decision at `wip/explore_vault-multi-org-auth_*.md`.
**Decision:** Skip Phase 0 scoping conversation. Synthesize from
exploration artifacts.

## Phase 3: Cross-Validation

### D3.1 Conflict: D1 (Factory.Open reads file) vs D3 (apply.go reads file)
**Evidence:** D1 chose Factory.Open as the read location (backend
self-contained). D3 chose apply.go (keep Factory.Open filesystem-free).
Both arguments have merit. D3's rationale is stronger because:
- apply.go already reads `~/.config/niwa/` (global overlay) in the same
  function — reading `provider-auth.toml` from the same directory is
  natural.
- Factory.Open is deliberately non-blocking and filesystem-free in the
  current codebase. Making it read a TOML file + do an HTTP POST breaks
  that contract.
- Reading once in apply.go and injecting `token` into ProviderConfig
  avoids N redundant file reads (one per provider).
- resolve.BuildBundle and specsFromRegistry stay pure — valuable for
  testing.
**Decision:** Adopt D3's apply.go location. Keep D1's schema
(kind+project tuple matching) and D2's no-cache strategy.

### D3.2 Reconciled: auth call (HTTP POST vs subprocess)
**Evidence:** D1 recommended HTTP POST to avoid client_secret on argv.
D2 mentioned `infisical login --method=universal-auth --silent --plain`
as an alternative. D3 didn't opine.
**Decision:** Use HTTP POST (`net/http` + `encoding/json`, both stdlib).
Avoids `client_secret` on subprocess argv (R21 alignment) and avoids
overwriting the Infisical CLI's stored session.
