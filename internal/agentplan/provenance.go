package agentplan

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// SourceEntry describes one input that contributed to a materialized
// file. SourceEntry values never carry secret material: SourceID and
// VersionToken are derived from non-secret metadata (file paths,
// provider-opaque revision IDs, plaintext content hashes). Backends
// MUST NOT populate these fields from decrypted secret bytes (see
// DESIGN-vault-integration.md Decision 4 and R15).
//
// This type and ComputeSourceFingerprint live here rather than in
// internal/workspace so plan entries can carry provenance without the
// two packages importing each other. internal/workspace keeps a type
// alias, so the persisted state schema and its JSON tags are
// unchanged.
type SourceEntry struct {
	// Kind names the source category: "plaintext", "vault", or
	// "env_example". internal/workspace names those values as
	// SourceKindPlaintext, SourceKindVault, and SourceKindEnvExample.
	Kind string `json:"kind"`

	// SourceID identifies the origin: a file path for plaintext
	// sources, or "provider-name/key" for vault sources (the
	// anonymous provider uses "/key").
	SourceID string `json:"source_id"`

	// VersionToken is the opaque per-backend revision identifier.
	// For plaintext sources this is the SHA-256 content-hash of the
	// source bytes at resolve time. For vault sources this is the
	// provider-returned VersionToken.Token.
	VersionToken string `json:"version_token"`

	// Provenance is a user-facing pointer (audit-log URL, git SHA,
	// fixture identifier) copied from VersionToken.Provenance for
	// vault sources, or left empty for plaintext. Never a secret.
	Provenance string `json:"provenance,omitempty"`
}

// ComputeSourceFingerprint returns the hex-encoded SHA-256 of a
// stable-sorted, null-separated list of (SourceID, VersionToken)
// tuples. Reducing a file's inputs to a single 32-byte digest is what
// lets niwa status distinguish user-edited drift (content changed,
// fingerprint matches) from upstream rotation (at least one source's
// VersionToken changed).
//
// An empty or nil slice hashes to a stable zero-input digest
// (SHA-256 of the empty byte string), so callers don't need to
// special-case files with no recorded sources.
func ComputeSourceFingerprint(sources []SourceEntry) string {
	// Build a local slice of (SourceID, VersionToken) pairs so the
	// sort is deterministic regardless of how the caller ordered the
	// input. We sort pairs rather than mutating the original slice
	// because callers hand-build the SourceEntry list in a logical
	// order (plaintext files first, inline vars next) that is useful
	// to preserve for diagnostic output.
	type pair struct {
		id, token string
	}
	pairs := make([]pair, len(sources))
	for i, s := range sources {
		pairs[i] = pair{s.SourceID, s.VersionToken}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].id != pairs[j].id {
			return pairs[i].id < pairs[j].id
		}
		return pairs[i].token < pairs[j].token
	})

	h := sha256.New()
	for _, p := range pairs {
		h.Write([]byte(p.id))
		h.Write([]byte{0})
		h.Write([]byte(p.token))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
