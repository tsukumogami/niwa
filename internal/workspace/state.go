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
	"time"
	"unicode"
)

const (
	// StateDir is the directory name used for instance state markers.
	StateDir = ".niwa"

	// StateFile is the filename for instance state within StateDir.
	StateFile = "instance.json"

	// SchemaVersion is the current schema version for instance state files.
	// v2 adds ManagedFile.SourceFingerprint and ManagedFile.Sources; v1
	// files load through the migration shim in LoadState and are rewritten
	// as v2 on the next SaveState.
	SchemaVersion = 2
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
)

// InstanceState represents the persisted state of a workspace instance.
type InstanceState struct {
	SchemaVersion  int                  `json:"schema_version"`
	ConfigName     *string              `json:"config_name"`
	InstanceName   string               `json:"instance_name"`
	InstanceNumber int                  `json:"instance_number"`
	Root           string               `json:"root"`
	Detached       bool                 `json:"detached,omitempty"`
	SkipGlobal     bool                 `json:"skip_global,omitempty"`
	Created        time.Time            `json:"created"`
	LastApplied    time.Time            `json:"last_applied"`
	ManagedFiles   []ManagedFile        `json:"managed_files"`
	Repos          map[string]RepoState `json:"repos"`
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
	// Kind names the source category. One of SourceKindPlaintext or
	// SourceKindVault.
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
// v1 state files (SchemaVersion == 1) load via an in-memory migration
// shim: the new v2 fields on each ManagedFile (SourceFingerprint,
// Sources) are already JSON-omitempty, so unmarshaling a v1 file
// leaves them at the zero value. LoadState does NOT bump the schema
// version on read — the caller's next SaveState rewrites the file as
// v2. Downgrading a v2-written state back to a pre-Issue-7 binary
// will fail to parse (the unknown schema_version value trips the
// strict-parse path there).
func LoadState(dir string) (*InstanceState, error) {
	data, err := os.ReadFile(statePath(dir))
	if err != nil {
		return nil, fmt.Errorf("reading instance state: %w", err)
	}

	var state InstanceState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing instance state: %w", err)
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
