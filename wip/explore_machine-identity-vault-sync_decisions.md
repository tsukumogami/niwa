# Exploration Decisions: machine-identity-vault-sync

## Round 1
- **Treat as distinct from R12/D-9 rejected pattern**: This proposal
  does NOT swap which vault is queried; it only changes the source of
  the credentials used to authenticate. The PRD must state this
  distinction explicitly.
- **In-memory only on every apply**: No on-disk cache for fetched
  credentials. Aligns with the existing no-token-cache invariant.
- **Local file wins on conflict**: When the same (kind, project) pair
  has both a local-file entry and a vault-sourced entry, the local
  file wins. Mirrors how the personal overlay relates to team config
  (personal wins on shadow). Augmentation, not replacement.
- **Personal vault must auth via CLI session, not via itself**:
  Avoids chicken-and-egg. The vault provider that stores
  machine-identity entries is itself authenticated via
  `infisical login`. This is a binding contract on the user's setup.
- **Personal vault is the only credential-sync source**: Team configs
  cannot supply machine identities (they're public-facing and must
  not carry credentials). Only the global-config personal overlay
  can opt into credential sync.
- **Single Infisical key per project, packed body**: The schema is
  one Infisical key per `(kind, project)` pair, with a packed
  TOML/JSON body containing `client_id`/`client_secret`/`api_url`.
  No manifest needed since niwa already knows the pairs it wants.
