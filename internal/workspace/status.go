package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RepoStatus describes the on-disk status of a single repo in an instance.
type RepoStatus struct {
	Name   string
	Status string // "cloned" or "missing"
}

// FileStatus describes the drift status of a single managed file.
//
// The ChangedSources field is populated only when Status is "stale"
// and lists the SourceEntry values whose VersionTokens differ from
// the live-recomputed plaintext sources. Vault-kind entries are not
// re-resolved in the default offline path, so stale detection here
// is driven by plaintext-source rotation; Issue 10's --check-vault
// flag adds opt-in live resolution for vault entries.
type FileStatus struct {
	Path           string
	Status         string // "ok", "drifted", "stale", or "removed"
	ChangedSources []ChangedSource
}

// ChangedSource describes one SourceEntry whose live VersionToken
// differs from the state-recorded token, surfacing the before/after
// tokens and the user-facing Provenance string. Offline status only
// detects plaintext rotation; vault entries populate ChangedSource
// via Issue 10's --check-vault flow.
type ChangedSource struct {
	Kind        string
	SourceID    string
	OldToken    string
	NewToken    string
	Provenance  string
	Description string // optional explanation, e.g., "file missing"
}

// InstanceStatus is the computed status of a workspace instance.
type InstanceStatus struct {
	Name        string
	ConfigName  string
	Root        string
	Created     time.Time
	LastApplied time.Time
	Repos       []RepoStatus
	Files       []FileStatus
	DriftCount  int
}

// ComputeStatus inspects the on-disk state of an instance and returns its
// current status, including repo clone status and managed file drift.
//
// ComputeStatus is fully offline: it reads state and hashes on-disk
// files, but does not invoke vault providers. When a managed file's
// current content hash differs from its recorded ContentHash,
// ComputeStatus re-hashes each plaintext SourceEntry to decide whether
// the drift came from a rotated input (Status == "stale") or a local
// edit (Status == "drifted"). Vault-side rotation is detected
// reactively by a subsequent niwa apply, or proactively by Issue 10's
// --check-vault flag.
func ComputeStatus(state *InstanceState, instanceRoot string) (*InstanceStatus, error) {
	configName := ""
	if state.ConfigName != nil {
		configName = *state.ConfigName
	}

	status := &InstanceStatus{
		Name:        state.InstanceName,
		ConfigName:  configName,
		Root:        instanceRoot,
		Created:     state.Created,
		LastApplied: state.LastApplied,
	}

	// Check repo directories. Repos are cloned under group directories
	// (instanceRoot/group/repoName), so we search one level of nesting.
	for name := range state.Repos {
		rs := RepoStatus{Name: name}
		if findRepoDir(instanceRoot, name) {
			rs.Status = "cloned"
		} else {
			rs.Status = "missing"
		}
		status.Repos = append(status.Repos, rs)
	}

	// Check managed files for drift and stale-source rotation.
	for _, mf := range state.ManagedFiles {
		fs := FileStatus{Path: mf.Path}
		result, err := CheckDrift(mf)
		if err != nil {
			return nil, fmt.Errorf("checking drift for %s: %w", mf.Path, err)
		}
		switch {
		case result.FileRemoved:
			fs.Status = "removed"
			status.DriftCount++
		case result.Drifted():
			// Re-check plaintext sources to classify the drift.
			changed := recomputeChangedPlaintextSources(mf, instanceRoot)
			if len(changed) > 0 {
				fs.Status = "stale"
				fs.ChangedSources = changed
			} else {
				fs.Status = "drifted"
			}
			status.DriftCount++
		default:
			fs.Status = "ok"
		}
		status.Files = append(status.Files, fs)
	}

	return status, nil
}

// recomputeChangedPlaintextSources re-hashes each plaintext
// SourceEntry and returns the list whose current content-hash differs
// from the recorded VersionToken. Vault entries are skipped — offline
// status cannot call into the provider without violating the
// offline-by-default contract.
//
// SourceIDs are resolved against a small set of candidate roots:
// the workspace root (parent of the instance root), the niwa config
// dir (sibling ".niwa" under the workspace root), and — for test
// fixtures whose sources live in the workspace-root parent — the
// workspaceRoot directly. Absolute paths are honored verbatim. The
// candidate walk lets the status code handle both real-world layouts
// (sources under .niwa/) and test fixtures (sources at the workspace
// root level) without threading a configDir argument through
// ComputeStatus. When no candidate exists we record a "file missing"
// ChangedSource so the user notices the broken reference.
func recomputeChangedPlaintextSources(mf ManagedFile, instanceRoot string) []ChangedSource {
	if len(mf.Sources) == 0 {
		return nil
	}
	workspaceRoot := filepath.Dir(instanceRoot)
	candidates := []string{
		filepath.Join(workspaceRoot, ".niwa"),
		workspaceRoot,
	}
	var changed []ChangedSource
	for _, s := range mf.Sources {
		if s.Kind != SourceKindPlaintext {
			continue
		}
		// Non-path SourceIDs ("workspace.toml:env.secrets.KEY", etc.)
		// cannot be re-hashed without re-reading TOML + re-resolving
		// MaybeSecrets. Skip them in the offline path; they fall
		// through to "drifted" classification when the file content
		// changes but no file-source rotation is detectable.
		if !looksLikePath(s.SourceID) {
			continue
		}
		hash, ok := hashFirstExisting(s.SourceID, candidates)
		if !ok {
			changed = append(changed, ChangedSource{
				Kind:        s.Kind,
				SourceID:    s.SourceID,
				OldToken:    s.VersionToken,
				NewToken:    "",
				Provenance:  s.Provenance,
				Description: "file missing or unreadable",
			})
			continue
		}
		if hash != s.VersionToken {
			changed = append(changed, ChangedSource{
				Kind:       s.Kind,
				SourceID:   s.SourceID,
				OldToken:   s.VersionToken,
				NewToken:   hash,
				Provenance: s.Provenance,
			})
		}
	}
	return changed
}

// hashFirstExisting walks candidate roots until it finds a readable
// file matching sourceID. Absolute sourceIDs short-circuit the walk.
// Returns the hex-prefixed hash and true on success; empty, false
// when no candidate resolves. The ok signal is distinct from an
// empty hash so callers can distinguish unreadable from zero-byte.
func hashFirstExisting(sourceID string, candidates []string) (string, bool) {
	if filepath.IsAbs(sourceID) {
		h, err := HashFile(sourceID)
		if err != nil {
			return "", false
		}
		return h, true
	}
	for _, root := range candidates {
		path := filepath.Join(root, sourceID)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		h, err := HashFile(path)
		if err != nil {
			return "", false
		}
		return h, true
	}
	return "", false
}

// looksLikePath classifies a SourceID as a filesystem path rather
// than a synthesized origin label ("workspace.toml:env.secrets.KEY").
// The heuristic is simple because the two categories are written by
// disjoint materializer code paths: synthesized labels always contain
// a colon as a separator between the owning TOML file and the dotted
// location.
func looksLikePath(id string) bool {
	for _, r := range id {
		if r == ':' {
			return false
		}
	}
	return id != ""
}

// findRepoDir checks whether a repo directory exists under instanceRoot.
// It looks for the repo name as a direct child or as a grandchild (under a
// group directory), matching how repos are cloned into group/repoName.
func findRepoDir(instanceRoot, repoName string) bool {
	// Direct child (instanceRoot/repoName).
	direct := filepath.Join(instanceRoot, repoName)
	if info, err := os.Stat(direct); err == nil && info.IsDir() {
		return true
	}

	// One level of nesting (instanceRoot/group/repoName).
	entries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == StateDir {
			continue
		}
		nested := filepath.Join(instanceRoot, entry.Name(), repoName)
		if info, err := os.Stat(nested); err == nil && info.IsDir() {
			return true
		}
	}

	return false
}
