package pluginstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/tsukumogami/niwa/internal/hostdir"
)

// MarketplacesDir is the directory under an instance's own .niwa where its
// marketplace links live.
const MarketplacesDir = "marketplaces"

// Linked is one release of this store and the instances on disk whose
// marketplace links name it.
type Linked struct {
	Release   Release
	Instances []string // instance directories, sorted
}

// RepairEntry is one linked release Repair did something about, or could not.
type RepairEntry struct {
	Release   Release
	Instances []string
	// Reason says why the release was refused or not restored. Empty for a
	// restored, tampered or undeclared one.
	Reason string
}

// RepairResult is what one Repair pass did, release by release, each list in
// release-id order.
type RepairResult struct {
	// Restored were missing or unmarked, and are now held.
	Restored []RepairEntry
	// Tampered were held but their tree failed Verify, changed after it was
	// placed; they were fetched again and are now held.
	Tampered []RepairEntry
	// Refused belong to a declared source but their commit is not shown to be
	// in its own history; nothing was fetched or held, and no link changed.
	Refused []RepairEntry
	// Undeclared belong to a source the workspace no longer declares; they
	// were left alone.
	Undeclared []RepairEntry
	// Failed could not be restored this time (the fetch failed, say, or the
	// store can't be used); nothing was marked held for them, and the next
	// command tries again.
	Failed []RepairEntry
	// ScanErr is an instance listing that failed part way. The releases found
	// before it were still repaired.
	ScanErr error
}

// Name is "github.com/<owner>/<repo>@<commit>", how a repair message names a
// release.
func (r Release) Name() string { return Forge + "/" + r.Owner + "/" + r.Repo + "@" + r.Commit }

// Linked lists every release of this store that a marketplace link of an
// instance on disk names, with the instances that link it. Instances come from
// hostdir.InstanceDirs, so a half-created or kept instance counts. Each
// instance's .niwa/marketplaces must be a real directory by Lstat, and each
// entry in it counts when it is a symlink whose target parses as a release
// link. A target under another home or workspace key (an instance moved
// here) still names its release, which is held here; that instance's own
// next provisioning command, the only writer of its links, relinks it to
// this store's path. Nothing is read through a link.
func (s Store) Linked() ([]Linked, error) {
	instances, scanErr := hostdir.InstanceDirs(s.Home, s.WorkspaceRoot)
	// The lenient scan returns no error of its own.
	linked, _ := scanLinks(instances, false)
	return linked, scanErr
}

// scanLinks reads the release links of each instance in instances, the one
// parser both the lenient and the strict scan use. Each instance's
// .niwa/marketplaces must be a real directory by Lstat; an instance without
// one has no links. Each entry counts when it is a symlink whose target
// parses as a release link, and nothing is read through it.
//
// The lenient scan, repair's, passes over an instance it can't read. The
// strict scan, the sweep's, fails instead, naming the path: a Lstat of
// <inst>/.niwa or <inst>/.niwa/marketplaces that fails for a reason other
// than absence, a marketplaces directory that can't be listed, or a link that
// can't be read. The strict scan never counts fewer links than the lenient
// one: an instance whose .niwa is itself a link is still read, because a
// release it links to must not look unused to the sweep.
func scanLinks(instances []string, strict bool) ([]Linked, error) {
	byID := map[string]*Linked{}
	for _, inst := range instances {
		if strict {
			niwaDir := filepath.Join(inst, ".niwa")
			_, err := os.Lstat(niwaDir)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, fmt.Errorf("inspecting %s: %w", niwaDir, err)
			}
			if err != nil {
				continue
			}
		}
		dir := filepath.Join(inst, ".niwa", MarketplacesDir)
		info, err := os.Lstat(dir)
		if strict && err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("inspecting %s: %w", dir, err)
		}
		if err != nil || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if strict {
				return nil, fmt.Errorf("reading %s: %w", dir, err)
			}
			continue
		}
		seen := map[string]bool{}
		for _, e := range entries {
			if e.Type()&os.ModeSymlink == 0 {
				continue
			}
			link := filepath.Join(dir, e.Name())
			target, err := os.Readlink(link)
			if err != nil {
				if strict {
					return nil, fmt.Errorf("reading link %s: %w", link, err)
				}
				continue
			}
			r, ok := ParseLink(target)
			if !ok {
				continue
			}
			l := byID[r.ID()]
			if l == nil {
				l = &Linked{Release: r}
				byID[r.ID()] = l
			}
			if !seen[r.ID()] {
				seen[r.ID()] = true
				l.Instances = append(l.Instances, inst)
			}
		}
	}

	out := make([]Linked, 0, len(byID))
	for _, l := range byID {
		sort.Strings(l.Instances)
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Release.ID() < out[j].Release.ID() })
	return out, nil
}

// Repair restores every release an instance on disk links to that is not
// held: its <commit> directory is absent, or present without a valid
// held.json. It also walks each distinct held release once with Verify, and
// one whose tree no longer matches its seal is moved aside and fetched again
// like a missing one, then reported as tampered. It is the one pass that puts
// back a release nobody asked for by name, so no command, flag or hint has
// to.
//
// A held release that passes Verify is left as it is. A release of a source
// not in declared is reported and left alone. Any other is placed through
// Ensure with no link, so it is fetched only when reachable shows its commit
// belongs to the
// source's own history, under the release's lock, and with the same
// all-or-nothing placement: a failed fetch leaves no held.json and no
// <commit> directory. Repair writes only inside the store and runs nothing
// inside any instance.
func (s Store) Repair(ctx context.Context, declared map[string]bool, reachable Reachable) RepairResult {
	var res RepairResult
	linked, scanErr := s.Linked()
	res.ScanErr = scanErr
	for _, l := range linked {
		r := l.Release
		entry := RepairEntry{Release: r, Instances: l.Instances}
		held, cond := s.Held(r)
		if cond != CondNone {
			entry.Reason = fmt.Sprintf("the plugin store at %s is %s", s.pluginsDir(), cond)
			res.Failed = append(res.Failed, entry)
			continue
		}
		tampered := false
		if held {
			if intact, _ := s.Verify(r); intact {
				continue
			}
			tampered = true
		}
		if !declared[r.Source()] {
			res.Undeclared = append(res.Undeclared, entry)
			continue
		}
		if reachable == nil {
			// A commit taken from a link is never fetched unchecked.
			entry.Reason = "its history could not be checked"
			res.Refused = append(res.Refused, entry)
			continue
		}
		// Ensure verifies again under the lock, so a release another command
		// already put back comes back not restored and is not reported.
		restored, cond, err := s.Ensure(ctx, r, "", declared, reachable)
		// A tampered release whose refetch failed says it was tampered with.
		failed := func(reason string) {
			if tampered {
				reason = "it was changed after it was placed; " + reason
			}
			entry.Reason = reason
			res.Failed = append(res.Failed, entry)
		}
		switch {
		case errors.Is(err, ErrRefused):
			entry.Reason = err.Error()
			res.Refused = append(res.Refused, entry)
		case cond != CondNone:
			reason := fmt.Sprintf("the plugin store at %s is %s", s.pluginsDir(), cond)
			if err != nil {
				reason += ": " + err.Error()
			}
			failed(reason)
		case err != nil:
			failed(err.Error())
		case restored && tampered:
			res.Tampered = append(res.Tampered, entry)
		case restored:
			res.Restored = append(res.Restored, entry)
		}
	}
	return res
}
