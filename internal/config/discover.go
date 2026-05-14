package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// ConfigDir is the directory name where workspace config lives.
	ConfigDir = ".niwa"
	// ConfigFile is the workspace config filename.
	ConfigFile = "workspace.toml"
)

// Discover walks up from startDir looking for .niwa/workspace.toml.
// It returns the absolute path to the config file and the config directory.
func Discover(startDir string) (configPath string, configDir string, err error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return "", "", fmt.Errorf("resolving start directory: %w", err)
	}

	for {
		candidate := filepath.Join(dir, ConfigDir, ConfigFile)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, filepath.Join(dir, ConfigDir), nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return "", "", fmt.Errorf("no %s/%s found in any parent of %s", ConfigDir, ConfigFile, startDir)
}

// MarkerSet describes which marker files a probe should look for at the
// source-root level of a config source. A MarkerSet is paired with a "found"
// MarkerSet (passed to RankDecider) whose fields are non-empty only for
// markers actually observed by the probe.
//
// The convention is: Rank1Dir + Rank1File together name the rank-1 marker
// (e.g., ".niwa" + "workspace.toml" → ".niwa/workspace.toml"); Rank2Path
// names the rank-2 marker at the source root (e.g., "workspace.toml").
//
// Empty Rank1Dir / Rank1File / Rank2Path in the "found" set means "the probe
// did not observe this marker." Empty directories — for example, a `.niwa/`
// dir that exists at the source root but contains no `workspace.toml` —
// MUST NOT cause HasRank1 to return true (PRD R6 / AC-D8).
type MarkerSet struct {
	Rank1Dir  string
	Rank1File string
	Rank2Path string
}

// HasRank1 reports whether the rank-1 marker was observed at the source
// root. A non-empty Rank1Dir AND a non-empty Rank1File together signal
// that the probe found `<Rank1Dir>/<Rank1File>`. An empty Rank1File means
// the probe saw the directory but not the marker file inside it; in that
// case HasRank1 returns false (PRD R6).
func (m MarkerSet) HasRank1() bool {
	return m.Rank1Dir != "" && m.Rank1File != ""
}

// HasRank2 reports whether the rank-2 marker was observed at the source
// root. Non-empty Rank2Path signals that the probe found that file at
// the root.
func (m MarkerSet) HasRank2() bool {
	return m.Rank2Path != ""
}

// DeprecationNotice is the value RankDecider returns alongside a successful
// rank-2 resolution. Callers route it through the workspace disclosure
// helper (EmitRank2Notice in internal/workspace/disclosure.go) so that the
// one-time stderr notice fires once per workspace per artifact (PRD R14).
//
// Rank is set to 2 today; the field is intentionally typed as int so future
// ranks (if any are reintroduced) can reuse the same shape without an enum
// migration.
type DeprecationNotice struct {
	Rank    int
	Markers MarkerSet
}

// OverlayConfigFile is the workspace overlay config filename.
const OverlayConfigFile = "workspace-overlay.toml"

// TeamConfigMarkerSet returns the marker set used to probe the team config
// source (the workspace's `--from` slug). Rank-1 is `.niwa/workspace.toml`;
// rank-2 is root `workspace.toml`.
func TeamConfigMarkerSet() MarkerSet {
	return MarkerSet{
		Rank1Dir:  ConfigDir,
		Rank1File: ConfigFile,
		Rank2Path: ConfigFile,
	}
}

// OverlayMarkerSet returns the marker set used to probe the auto-discovered
// workspace overlay source. Rank-1 is `.niwa/workspace-overlay.toml`;
// rank-2 is root `workspace-overlay.toml`.
func OverlayMarkerSet() MarkerSet {
	return MarkerSet{
		Rank1Dir:  ConfigDir,
		Rank1File: OverlayConfigFile,
		Rank2Path: OverlayConfigFile,
	}
}

// AmbiguousMarkersError is returned by RankDecider for the documented
// ambiguity cases (PRD R3). It carries the markers found and the markers
// expected so callers can render a user-actionable diagnostic listing both
// files.
type AmbiguousMarkersError struct {
	Found   MarkerSet
	Markers MarkerSet
}

func (e *AmbiguousMarkersError) Error() string {
	return fmt.Sprintf("ambiguous niwa config: found both %s/%s and %s at source root",
		e.Markers.Rank1Dir, e.Markers.Rank1File, e.Markers.Rank2Path)
}

// NoMarkerError is returned by RankDecider when neither rank-1 nor rank-2
// is found (PRD R4), and also when only rank-2 is found in a deployment
// where rank-2 acceptance has been turned off (forward-compat for the
// PRD-config-source-discovery R15 hard-removal release).
type NoMarkerError struct {
	Markers MarkerSet
}

func (e *NoMarkerError) Error() string {
	return fmt.Sprintf("no niwa config found: probed %s/%s and %s at source root",
		e.Markers.Rank1Dir, e.Markers.Rank1File, e.Markers.Rank2Path)
}

// rankTwoAcceptedTestHook lets tests exercise the forward-compat path
// where the rank-2 deprecated branch is removed. Production code MUST
// leave this nil; in that case the BEGIN/END-bracketed constant below
// wins. Tests set the hook to point at `false` to verify that rank-2
// resolution surfaces as a no-marker error when the guard is off.
//
// This hook stays unexported so callers can't accidentally bypass the
// production constant. The follow-up release that removes rank-2 deletes
// both the hook and the bracketed branch below.
var rankTwoAcceptedTestHook *bool

// RankDecider resolves the probe's findings to a resolved subpath and an
// optional deprecation notice. It implements PRD R3 / R4 / R13:
//
//   - both ranks found at the same source root: AmbiguousMarkersError
//     (PRD R3 / AC-D5). The user must remove one marker or pass an
//     explicit subpath via the slug.
//   - rank-1 found (alone): returns (Rank1Dir, nil, nil).
//   - rank-2 found (alone) AND rank-2 still accepted: returns
//     ("", &DeprecationNotice{...}, nil).
//   - neither rank found: NoMarkerError (PRD R4).
//   - only rank-2 found AND rank-2 acceptance turned off: NoMarkerError
//     (forward-compat for the deprecated-branch removal in
//     PRD-config-source-discovery R15).
//
// RankDecider is pure: it performs no I/O, reads no globals other than the
// test hook above, and modifies no state.
func RankDecider(found, markers MarkerSet) (subpath string, notice *DeprecationNotice, err error) {
	// PRD R3 / AC-D5: both markers at the same source root are
	// unconditionally ambiguous; the user must disambiguate.
	if found.HasRank1() && found.HasRank2() {
		return "", nil, &AmbiguousMarkersError{Found: found, Markers: markers}
	}

	if found.HasRank1() {
		return markers.Rank1Dir, nil, nil
	}

	// BEGIN rank-2 deprecated branch — remove in the follow-up release
	// that hard-removes rank-2 discovery per PRD-config-source-discovery R15.
	const rankTwoAccepted = true
	accepted := rankTwoAccepted
	if rankTwoAcceptedTestHook != nil {
		accepted = *rankTwoAcceptedTestHook
	}
	if accepted && found.HasRank2() {
		return "", &DeprecationNotice{Rank: 2, Markers: markers}, nil
	}
	// END rank-2 deprecated branch — PRD-config-source-discovery R15

	return "", nil, &NoMarkerError{Markers: markers}
}

// IsAmbiguousMarkers reports whether err is an *AmbiguousMarkersError.
// Provided so callers can branch on the diagnostic without depending on
// the concrete error type.
func IsAmbiguousMarkers(err error) bool {
	var target *AmbiguousMarkersError
	return errors.As(err, &target)
}

// IsNoMarker reports whether err is a *NoMarkerError.
func IsNoMarker(err error) bool {
	var target *NoMarkerError
	return errors.As(err, &target)
}
