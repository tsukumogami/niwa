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
type FileStatus struct {
	Path   string
	Status string // "ok", "drifted", or "removed"
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

	// Check managed files for drift.
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
			fs.Status = "drifted"
			status.DriftCount++
		default:
			fs.Status = "ok"
		}
		status.Files = append(status.Files, fs)
	}

	return status, nil
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
