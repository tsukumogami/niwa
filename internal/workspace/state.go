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
	"time"
)

const (
	// StateDir is the directory name used for instance state markers.
	StateDir = ".niwa"

	// StateFile is the filename for instance state within StateDir.
	StateFile = "instance.json"

	// SchemaVersion is the current schema version for instance state files.
	SchemaVersion = 1
)

// InstanceState represents the persisted state of a workspace instance.
type InstanceState struct {
	SchemaVersion  int                    `json:"schema_version"`
	ConfigName     *string                `json:"config_name"`
	InstanceName   string                 `json:"instance_name"`
	InstanceNumber int                    `json:"instance_number"`
	Root           string                 `json:"root"`
	Detached       bool                   `json:"detached,omitempty"`
	SkipGlobal     bool                   `json:"skip_global,omitempty"`
	Created        time.Time              `json:"created"`
	LastApplied    time.Time              `json:"last_applied"`
	ManagedFiles   []ManagedFile          `json:"managed_files"`
	Repos          map[string]RepoState   `json:"repos"`
}

// ManagedFile tracks a file written by niwa apply.
type ManagedFile struct {
	Path      string    `json:"path"`
	Hash      string    `json:"hash"`
	Generated time.Time `json:"generated"`
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
		dir := filepath.Join(workspaceRoot, entry.Name())
		if _, err := os.Stat(statePath(dir)); err == nil {
			instances = append(instances, dir)
		}
	}

	return instances, nil
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
		Expected: mf.Hash,
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
