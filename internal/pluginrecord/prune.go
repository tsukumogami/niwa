package pluginrecord

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Selector decides whether a record should be removed. Prune removes every
// record for which the selector returns true and leaves all others in place,
// so the mutation is always remove-only and criterion-bounded (R9).
type Selector func(Record) bool

// PruneReport summarizes a Prune invocation. It is surfaced to the user by the
// caller so a removal is never silent (R4).
type PruneReport struct {
	// Removed is the total number of records removed across all plugins.
	Removed int

	// PerPlugin maps a plugin key to the number of records removed from it.
	PerPlugin map[string]int

	// DroppedKeys lists plugin keys removed entirely because their record
	// list became empty after filtering. Sorted for determinism.
	DroppedKeys []string

	// BackupPath is the timestamped backup written before the first mutation,
	// or "" when no write happened (dry run, no matches, or absent registry).
	BackupPath string
}

// pruneConfig holds resolved Prune options.
type pruneConfig struct {
	baseDir string
	dryRun  bool
	retain  int
}

// PruneOption configures a Prune invocation.
type PruneOption func(*pruneConfig)

// WithPruneBaseDir overrides the home directory used to locate the registry,
// mirroring WithBaseDir for the load/save path. It exists for tests, which
// point Prune at a t.TempDir instead of the real ~/.claude.
func WithPruneBaseDir(dir string) PruneOption {
	return func(c *pruneConfig) { c.baseDir = dir }
}

// withDryRun makes Prune compute and return the report without taking a backup
// or writing the registry. It is an internal/test capability and is
// deliberately unexported, so no user-facing command can request it.
func withDryRun() PruneOption {
	return func(c *pruneConfig) { c.dryRun = true }
}

// locateOptions translates the Prune base-dir config into the Option form the
// load/save core understands.
func (c pruneConfig) locateOptions() []Option {
	if c.baseDir == "" {
		return nil
	}
	return []Option{WithBaseDir(c.baseDir)}
}

// Dangling matches a record whose referenced directory is already gone. A
// record is dangling when its non-empty InstallPath is missing, or its
// non-empty ProjectPath is missing (R9).
//
// Existence is checked with Lstat, never Stat, so a record whose path is a
// symlink pointing at a removed target is judged on the link itself (present),
// not the followed target. The check decides removal only; it never writes
// through the path. On any stat error other than "does not exist" the path is
// treated as present, so a record is removed only when it is provably gone —
// keeping the mutation conservatively remove-only.
func Dangling(rec Record) bool {
	if rec.InstallPath != "" && pathMissing(rec.InstallPath) {
		return true
	}
	if rec.ProjectPath != "" && pathMissing(rec.ProjectPath) {
		return true
	}
	return false
}

// pathMissing reports whether path provably does not exist, using Lstat so a
// symlink is judged on the link rather than its target.
func pathMissing(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrNotExist)
}

// InstanceOwned matches a record whose ProjectPath lies within root. Both paths
// are cleaned and compared with filepath.Rel containment, so a sibling instance
// whose path merely shares a textual prefix is NOT matched (R9). A record whose
// ProjectPath equals root counts as owned.
func InstanceOwned(root string) Selector {
	cleanRoot := filepath.Clean(root)
	return func(rec Record) bool {
		if rec.ProjectPath == "" {
			return false
		}
		cleanProject := filepath.Clean(rec.ProjectPath)
		rel, err := filepath.Rel(cleanRoot, cleanProject)
		if err != nil {
			return false
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return false
		}
		if filepath.IsAbs(rel) {
			return false
		}
		return true
	}
}

// Prune removes every record matching selector and returns a report. It re-reads
// the latest registry immediately before writing (minimal-delta), so it never
// clobbers a concurrent foreign addition beyond a benign self-healing window.
//
// Discipline:
//  1. Load the registry. A malformed file returns an error wrapping ErrMalformed
//     and leaves the file untouched (R12); an absent file is an empty no-op.
//  2. Compute matches on the loaded content. With no matches, Prune returns an
//     empty report without taking a backup or writing — it never churns the file.
//  3. Take a timestamped backup before the first write (R11).
//  4. Re-read the latest file and recompute the removal set against it.
//  5. Filter matching records out, drop now-empty plugin keys, and write
//     atomically via the load/save core (R10).
//
// The internal dry-run mode (test-only) computes the same report against the
// loaded content with no backup and no write.
func Prune(selector Selector, opts ...PruneOption) (PruneReport, error) {
	cfg := pruneConfig{retain: defaultBackupRetention}
	for _, opt := range opts {
		opt(&cfg)
	}

	reg, err := Load(cfg.locateOptions()...)
	if err != nil {
		return PruneReport{}, err
	}

	// Dry run reports against the loaded content with no side effects.
	if cfg.dryRun {
		report, _ := apply(reg, selector)
		report.BackupPath = ""
		return report, nil
	}

	// Avoid backing up or rewriting a file we would not change.
	if preview, changed := apply(reg, selector); !changed {
		return preview, nil
	}

	backupPath, err := reg.Backup(cfg.retain)
	if err != nil {
		return PruneReport{}, err
	}

	// Re-read the freshest content and recompute against it (minimal-delta),
	// so a foreign write that landed since the first load is respected.
	reg, err = Load(cfg.locateOptions()...)
	if err != nil {
		return PruneReport{BackupPath: backupPath}, err
	}

	report, changed := apply(reg, selector)
	report.BackupPath = backupPath
	if !changed {
		// A concurrent writer healed the registry between load and re-read;
		// nothing left to remove, so skip the rewrite.
		return report, nil
	}

	if err := reg.Save(); err != nil {
		return report, err
	}
	return report, nil
}

// apply removes matching records from reg in place and returns the report plus
// whether anything changed. Plugin keys whose record list becomes empty are
// deleted from the registry; Marshal only emits keys still present in Plugins,
// so dropped keys naturally disappear from the written document.
func apply(reg *Registry, selector Selector) (PruneReport, bool) {
	report := PruneReport{PerPlugin: map[string]int{}}

	for key, records := range reg.Plugins {
		kept := records[:0:0]
		removed := 0
		for _, rec := range records {
			if selector(rec) {
				removed++
				continue
			}
			kept = append(kept, rec)
		}
		if removed == 0 {
			continue
		}
		report.Removed += removed
		report.PerPlugin[key] = removed
		if len(kept) == 0 {
			delete(reg.Plugins, key)
			report.DroppedKeys = append(report.DroppedKeys, key)
		} else {
			reg.Plugins[key] = kept
		}
	}

	sort.Strings(report.DroppedKeys)
	return report, report.Removed > 0
}
