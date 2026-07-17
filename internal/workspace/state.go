package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	// StateDir is the directory name where instance state files live.
	// Intentionally shares the value with SnapshotDir: per the
	// 2026-04-23 amendment to DESIGN-workspace-config-sources Decision 2,
	// niwa-local state lives alongside config-source content under
	// .niwa/, and the snapshot writer's preserveInstanceState helper
	// carries the state file across the atomic swap.
	StateDir = ".niwa"

	// SnapshotDir is the directory name where the workspace config
	// snapshot is materialized at the workspace root. Shares the value
	// with StateDir by design (see StateDir docs).
	SnapshotDir = ".niwa"

	// StateFile is the filename for instance state within StateDir.
	StateFile = "instance.json"

	// ProvenanceFile is the filename for the snapshot provenance marker
	// within SnapshotDir. See internal/workspace/provenance.go.
	ProvenanceFile = ".niwa-snapshot.toml"

	// SchemaVersion is the current schema version for instance state files.
	// v2 adds ManagedFile.SourceFingerprint and ManagedFile.Sources.
	// v3 adds InstanceState.ConfigSource (the source-identity tuple
	// recorded at last apply).
	// v4 adds InstanceState.AuthSources, the credential-source audit
	// map persisted at apply time so `niwa status --audit-auth` can
	// render fully offline (machine-identity-vault-sync PRD R11).
	// v1, v2, and v3 files load through migration shims in LoadState
	// and are rewritten on the next SaveState. The v3→v4 migration is
	// a no-op at the JSON level (auth_sources is omitempty), so v3
	// files unmarshal cleanly with AuthSources == nil and a save
	// rewrites them as v4.
	SchemaVersion = 4
)

// SourceKind enumerates the provenance categories recognised by
// Sources[] entries on a ManagedFile.
const (
	// SourceKindPlaintext identifies a source whose version is a
	// content-hash of bytes read from a non-secret config file
	// (env.files entries, workspace.toml plaintext values, etc.).
	SourceKindPlaintext = "plaintext"

	// SourceKindVault identifies a source resolved through a vault
	// provider. VersionToken carries the provider-opaque revision
	// identifier returned by Provider.Resolve.
	SourceKindVault = "vault"

	// SourceKindEnvExample identifies a source whose values were read from a
	// .env.example file in the repository directory during the pre-pass.
	SourceKindEnvExample = "env_example"
)

// InstanceState represents the persisted state of a workspace instance.
//
// Shadows are persisted alongside ManagedFiles as part of schema v2:
// they record the personal-overlay-vs-team key conflicts detected at
// the last apply so offline `niwa status` can surface a summary line
// without re-running the resolver. Empty when the last apply saw no
// shadows.
//
// ConfigSource arrives in schema v3: it carries the source-identity
// tuple recorded at the last apply. Lazy-populated from the registry
// mirror plus the snapshot provenance marker on the first v3-aware
// SaveState; nil for state files written by older binaries until they
// next save.
//
// AuthSources arrives in schema v4: a map keyed by "<kind>/<project>"
// of the credential-source decisions the credential pool recorded at
// the last apply. Read by `niwa status --audit-auth` (offline) per
// machine-identity-vault-sync PRD R11. Both fields on the value are
// categorical strings (never credential bytes) per PRD R18.
type InstanceState struct {
	SchemaVersion  int     `json:"schema_version"`
	ConfigName     *string `json:"config_name"`
	InstanceName   string  `json:"instance_name"`
	InstanceNumber int     `json:"instance_number"`
	Root           string  `json:"root"`
	Detached       bool    `json:"detached,omitempty"`
	SkipGlobal     bool    `json:"skip_global,omitempty"`
	OverlayURL     string  `json:"overlay_url,omitempty"`
	NoOverlay      bool    `json:"no_overlay,omitempty"`
	OverlayCommit  string  `json:"overlay_commit,omitempty"`
	// NoWorktreeDelegation records the `niwa init --no-worktree-delegation`
	// opt-out. When true, the apply pipeline skips the entire
	// worktree-delegation integration (no probe, no hook, no deny). Mirrors
	// SkipGlobal / NoOverlay: written at init time and read on every apply.
	// omitempty keeps the field invisible to old binaries reading new state
	// files.
	NoWorktreeDelegation bool `json:"no_worktree_delegation,omitempty"`
	// EphemeralSessionMode records whether the workspace root is opted in to
	// per-session ephemeral instance provisioning (the master switch of the
	// `niwa instance from-hook` SessionStart guard). It is only meaningful on
	// the workspace-root state file. Default false: when absent, the hook is
	// inert, so an ordinary workspace is never touched. Written at init time
	// (a later issue); read on every SessionStart hook invocation via
	// EphemeralSessionMode. Mirrors SkipGlobal / NoOverlay / NoWorktreeDelegation
	// as an additive bool; omitempty keeps it invisible to old binaries.
	EphemeralSessionMode bool `json:"ephemeral_session_mode,omitempty"`
	// ConfigNameOverride records an explicit workspace name supplied to
	// `niwa init <name>` when it differs from (or stands in for) the
	// cloned config's `[workspace] name`. Apply.Create / Apply.Apply
	// resolve the effective name via EffectiveConfigName so downstream
	// readers (status, apply output, registry) all surface the same
	// value. Empty when no override is in play; omitempty keeps the
	// field invisible to old binaries reading new state files.
	ConfigNameOverride string                      `json:"config_name_override,omitempty"`
	Created            time.Time                   `json:"created"`
	LastApplied        time.Time                   `json:"last_applied"`
	ManagedFiles       []ManagedFile               `json:"managed_files"`
	Repos              map[string]RepoState        `json:"repos"`
	Shadows            []Shadow                    `json:"shadows,omitempty"`
	DisclosedNotices   []string                    `json:"disclosed_notices,omitempty"`
	ConfigSource       *ConfigSource               `json:"config_source,omitempty"`
	AuthSources        map[string]AuthSourceRecord `json:"auth_sources,omitempty"`
}

// AuthSourceRecord is one row of the credential-source audit map
// persisted in InstanceState.AuthSources at apply time. Both fields
// are categorical strings — never credential bytes (PRD R18).
//
//   - Source: where the credential came from in the last apply.
//     One of "local-file", "vault:personal-overlay" (or
//     "vault:personal-overlay(<name>)" when the credential-sync
//     provider has a name; see PRD AC-39 / renderVaultProvider),
//     "cli-session", or "none".
//   - Fallback: the non-active source that ALSO had an entry, if
//     any. "vault:personal-overlay" (with the same name-disambiguator
//     suffix when applicable) when the local file won and the vault
//     also had an entry; otherwise empty.
//
// `niwa status --audit-auth` reads this map (and only this map) to
// render its four-column table, so the apply-time write is the
// single source of truth for the audit surface.
type AuthSourceRecord struct {
	Source   string `json:"source"`
	Fallback string `json:"fallback,omitempty"`
}

// ConfigSource records the resolved source identity for a workspace's
// config snapshot. Persisted alongside InstanceState in schema v3 per
// PRD R24. Equivalent in shape to the on-disk provenance marker
// (workspace/provenance.go); state retains a copy so `niwa status`
// can render source identity without cracking the snapshot dir.
type ConfigSource struct {
	URL            string    `json:"url"`
	Host           string    `json:"host"`
	Owner          string    `json:"owner"`
	Repo           string    `json:"repo"`
	Subpath        string    `json:"subpath,omitempty"`
	Ref            string    `json:"ref,omitempty"`
	ResolvedCommit string    `json:"resolved_commit"`
	FetchedAt      time.Time `json:"fetched_at"`
}

// ManagedFile tracks a file written by niwa apply.
//
// In state schema v2, every ManagedFile carries a SourceFingerprint
// (a SHA-256 rollup of its Sources[] tuple list) and a Sources slice
// describing each input that contributed bytes to the file. v1 files
// that loaded through the migration shim have empty SourceFingerprint
// and nil Sources; they are rewritten as v2 on the next SaveState.
//
// The JSON tag on ContentHash is kept at "hash" so v1 state files
// unmarshal directly — the field rename is Go-only.
type ManagedFile struct {
	Path              string        `json:"path"`
	ContentHash       string        `json:"hash"`
	SourceFingerprint string        `json:"source_fingerprint,omitempty"`
	Sources           []SourceEntry `json:"sources,omitempty"`
	Generated         time.Time     `json:"generated"`
}

// SourceEntry describes one input that contributed to a materialized
// file. SourceEntry values never carry secret material: SourceID and
// VersionToken are derived from non-secret metadata (file paths,
// provider-opaque revision IDs, plaintext content hashes). Backends
// MUST NOT populate these fields from decrypted secret bytes (see
// DESIGN-vault-integration.md Decision 4 and R15).
type SourceEntry struct {
	// Kind names the source category. One of SourceKindPlaintext,
	// SourceKindVault, or SourceKindEnvExample.
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

// RepoState tracks clone status for a repo.
type RepoState struct {
	URL    string `json:"url"`
	Cloned bool   `json:"cloned"`
}

// DriftResult describes whether a managed file has drifted from its recorded hash.
type DriftResult struct {
	Path        string
	Expected    string
	Actual      string
	FileRemoved bool
}

// Drifted returns true if the file has changed or been removed.
func (d DriftResult) Drifted() bool {
	return d.FileRemoved || d.Expected != d.Actual
}

// statePath returns the path to instance.json within a directory.
func statePath(dir string) string {
	return filepath.Join(dir, StateDir, StateFile)
}

// LoadState reads an InstanceState from the .niwa/instance.json file in dir.
//
// v1, v2, and v3 state files load via an implicit no-op migration:
// every new field added by a later schema version carries a JSON
// omitempty tag, so older files unmarshal cleanly with the new
// fields zero-valued (nil maps, nil pointers, empty slices). The
// pattern is centralised in the SchemaVersion constant block at
// the top of this file — see that comment for the per-version
// contract. LoadState does NOT bump the schema version on read —
// the caller's next SaveState rewrites the file at the current
// SchemaVersion.
//
// Forward-version state files (schema_version > SchemaVersion) are
// rejected per PRD R25. The on-disk file is byte-identical to its
// pre-load state when this happens; LoadState does not attempt
// down-conversion.
func LoadState(dir string) (*InstanceState, error) {
	data, err := os.ReadFile(statePath(dir))
	if err != nil {
		return nil, fmt.Errorf("reading instance state: %w", err)
	}

	var state InstanceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing instance state: %w", err)
	}

	if state.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf(
			"state file at schema_version %d is newer than this binary supports (max %d); upgrade niwa to read this state",
			state.SchemaVersion, SchemaVersion)
	}

	return &state, nil
}

// SaveState writes an InstanceState to .niwa/instance.json in dir.
func SaveState(dir string, state *InstanceState) error {
	stateDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling instance state: %w", err)
	}

	if err := os.WriteFile(statePath(dir), data, 0o644); err != nil {
		return fmt.Errorf("writing instance state: %w", err)
	}

	return nil
}

// isWorkspaceRoot reports whether dir is a workspace root, i.e. it carries
// .niwa/workspace.toml. A workspace root is never an instance: instances live
// *under* a root and carry .niwa/instance.json without a workspace.toml. The
// root may legitimately hold its own instance.json (init writes one to persist
// the config-name override and ephemeral-session flags), so the presence of
// workspace.toml is the authoritative signal that distinguishes the two.
func isWorkspaceRoot(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, StateDir, WorkspaceConfigFile))
	return err == nil
}

// DiscoverInstance walks up from startPath to find the nearest directory
// containing .niwa/instance.json. It returns the directory containing the
// instance state, or an error if none is found.
func DiscoverInstance(startPath string) (string, error) {
	absPath, err := filepath.Abs(startPath)
	if err != nil {
		return "", fmt.Errorf("resolving path: %w", err)
	}

	dir := absPath
	for {
		if _, err := os.Stat(statePath(dir)); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no instance found walking up from %s", startPath)
		}
		dir = parent
	}
}

// EnumerateInstances scans immediate subdirectories of workspaceRoot for
// .niwa/instance.json markers and returns the directories that contain them.
// Entries with names containing ASCII controls or Unicode Cf/Zl/Zp codepoints
// are filtered out; see ValidName for the full rationale.
func EnumerateInstances(workspaceRoot string) ([]string, error) {
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("reading workspace root: %w", err)
	}

	var instances []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !ValidName(entry.Name()) {
			continue
		}
		dir := filepath.Join(workspaceRoot, entry.Name())
		if _, err := os.Stat(statePath(dir)); err == nil {
			instances = append(instances, dir)
		}
	}

	return instances, nil
}

// InstanceRecord is a machine-readable summary of one instance under a
// workspace root, emitted by `niwa list --json`. Name is the instance
// directory's base name; Path is its absolute directory; Ephemeral is true
// when the instance is backed by an ephemeral session mapping (see the
// session mapping store).
type InstanceRecord struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Ephemeral bool   `json:"ephemeral"`
	// KeepAlive marks an instance whose backing session was dispatched with
	// keep-alive armed AND is still live. EnumerateInstanceRecords leaves it
	// false: the liveness half of the join (the Claude Code job-entry signal)
	// lives at the CLI layer, so the list command fills this in. omitempty
	// keeps the --json shape unchanged for every non-participating instance.
	KeepAlive bool `json:"keep_alive,omitempty"`
}

// EnumerateInstanceRecords enumerates the instances under workspaceRoot as
// machine-readable records, sorted by name. The Ephemeral flag is resolved
// from the workspace-root session mapping store: an instance is ephemeral
// when some session mapping points at its directory. Workspaces with no
// mapping store (the common case) yield Ephemeral:false for every instance.
func EnumerateInstanceRecords(workspaceRoot string) ([]InstanceRecord, error) {
	dirs, err := EnumerateInstances(workspaceRoot)
	if err != nil {
		return nil, err
	}

	ephemeralPaths := ephemeralInstancePaths(workspaceRoot)

	records := make([]InstanceRecord, 0, len(dirs))
	for _, dir := range dirs {
		records = append(records, InstanceRecord{
			Name:      filepath.Base(dir),
			Path:      dir,
			Ephemeral: ephemeralPaths[dir],
		})
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Name < records[j].Name
	})
	return records, nil
}

// ephemeralInstancePaths returns the set of instance directories (absolute
// paths) backed by an ephemeral session mapping under workspaceRoot.
//
// It reads the workspace-root session mapping store at
// .niwa/sessions/<session_id>.json, the single source of truth shared with
// the session teardown path and the reaper. When the store is absent (the
// common case for non-ephemeral workspaces) it returns an empty set.
// Malformed or unreadable entries are skipped rather than failing the scan,
// since `niwa list` must stay usable even with a partially written store.
func ephemeralInstancePaths(workspaceRoot string) map[string]bool {
	out := make(map[string]bool)

	sessionsDir := filepath.Join(workspaceRoot, StateDir, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		return out
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sessionsDir, entry.Name()))
		if err != nil {
			continue
		}
		var m struct {
			InstancePath string `json:"instance_path"`
			Ephemeral    bool   `json:"ephemeral"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.Ephemeral && m.InstancePath != "" {
			out[m.InstancePath] = true
		}
	}

	return out
}

// EnumerateRepos scans instanceRoot for repo directories and returns a sorted,
// deduped list of repo names (names only, no paths). The scan walks two
// levels: each immediate child of instanceRoot is treated as a group
// directory, and each subdirectory inside a group is treated as a repo.
//
// Entries are skipped when:
//   - The top-level entry is one of the reserved control directories
//     (".niwa", ".claude") or starts with "."
//   - The second-level entry starts with "."
//   - Either name fails ValidName (see ValidName for details)
//
// When the same repo name appears under multiple groups, it is returned
// once. Callers that need group-level disambiguation should walk the
// filesystem directly.
//
// Returns ([]string{}, nil) when the instance root is empty or has no
// matching entries. Returns (nil, err) when os.ReadDir(instanceRoot) itself
// fails. Unreadable group directories are skipped rather than failing the
// whole walk.
func EnumerateRepos(instanceRoot string) ([]string, error) {
	entries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("reading instance root: %w", err)
	}

	seen := make(map[string]struct{})
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() {
			continue
		}
		if name == StateDir || name == ".claude" {
			continue
		}
		if len(name) > 0 && name[0] == '.' {
			continue
		}
		if !ValidName(name) {
			continue
		}

		groupDir := filepath.Join(instanceRoot, name)
		repos, err := os.ReadDir(groupDir)
		if err != nil {
			// Skip unreadable groups rather than failing the whole walk.
			continue
		}
		for _, r := range repos {
			rname := r.Name()
			if !r.IsDir() {
				continue
			}
			if len(rname) > 0 && rname[0] == '.' {
				continue
			}
			if !ValidName(rname) {
				continue
			}
			seen[rname] = struct{}{}
		}
	}

	if len(seen) == 0 {
		return []string{}, nil
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// ValidName reports whether a filesystem-derived name is safe to emit through
// cobra's __complete line/TAB protocol. It rejects any character matching
// unicode.IsControl (ASCII controls plus Unicode Cc) as well as Unicode
// format, line separator, and paragraph separator categories (Cf/Zl/Zp).
// Those codepoints could corrupt cobra's line-oriented output, confuse the
// completion test parser, or visually spoof legitimate names via bidi
// overrides.
func ValidName(name string) bool {
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
		if unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}

// NextInstanceNumber finds the lowest unused instance number by scanning
// subdirectories of workspaceRoot. It fills gaps left by deleted instances
// (e.g., if instances 2, 4, 5 exist, it returns 3). Returns 1 if no
// instances exist.
func NextInstanceNumber(workspaceRoot string) (int, error) {
	instances, err := EnumerateInstances(workspaceRoot)
	if err != nil {
		return 0, err
	}

	used := make(map[int]bool)
	for _, dir := range instances {
		state, err := LoadState(dir)
		if err != nil {
			continue
		}
		used[state.InstanceNumber] = true
	}

	// Find the first unused number starting from 1.
	for n := 1; ; n++ {
		if !used[n] {
			return n, nil
		}
	}
}

// noticeDisclosed reports whether notice has been recorded in s.DisclosedNotices.
func noticeDisclosed(s *InstanceState, notice string) bool {
	if s == nil {
		return false
	}
	for _, n := range s.DisclosedNotices {
		if n == notice {
			return true
		}
	}
	return false
}

// mergeDisclosedNotices returns the union of existing and added, preserving
// order and omitting duplicates. Returns existing unchanged when added is empty.
func mergeDisclosedNotices(existing, added []string) []string {
	if len(added) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing)+len(added))
	merged := make([]string, 0, len(existing)+len(added))
	for _, n := range existing {
		if _, ok := seen[n]; !ok {
			seen[n] = struct{}{}
			merged = append(merged, n)
		}
	}
	for _, n := range added {
		if _, ok := seen[n]; !ok {
			seen[n] = struct{}{}
			merged = append(merged, n)
		}
	}
	return merged
}

// HashFile computes the SHA-256 hash of a file and returns it with a "sha256:"
// prefix.
func HashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("opening file for hashing: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashing file: %w", err)
	}

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// CheckDrift compares the current hash of a managed file against its recorded
// hash in the state. Returns a DriftResult indicating whether the file has changed.
func CheckDrift(mf ManagedFile) (DriftResult, error) {
	result := DriftResult{
		Path:     mf.Path,
		Expected: mf.ContentHash,
	}

	currentHash, err := HashFile(mf.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			result.FileRemoved = true
			return result, nil
		}
		return result, fmt.Errorf("checking drift for %s: %w", mf.Path, err)
	}

	result.Actual = currentHash
	return result, nil
}
