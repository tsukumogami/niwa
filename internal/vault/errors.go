package vault

import "errors"

// ErrKeyNotFound is returned by Provider.Resolve when the requested
// key does not exist in the backend. Callers check via errors.Is; the
// resolver stage consults this sentinel to decide whether to
// downgrade a missing optional key to an empty value.
var ErrKeyNotFound = errors.New("vault: key not found")

// ErrProviderUnreachable is returned by Provider.Resolve (or
// Factory.Open) when the backend cannot be contacted: auth failure,
// network error, missing CLI binary, expired session. Callers check
// via errors.Is; --allow-missing-secrets consults this sentinel to
// decide whether to downgrade.
var ErrProviderUnreachable = errors.New("vault: provider unreachable")

// ErrProviderNameCollision is returned when a personal overlay
// declares a provider whose name is already declared by the team
// config. Per R12 (vault-integration design), the personal overlay
// may ADD new provider names but cannot REPLACE team-declared ones.
var ErrProviderNameCollision = errors.New("vault: personal overlay cannot replace team-declared provider")

// ErrTeamOnlyLocked is returned when a personal overlay attempts to
// set a value for a key that the team config marked as team_only.
// Per R8, team_only keys are not overridable by personal overlays.
var ErrTeamOnlyLocked = errors.New("vault: key is locked by team_only")
