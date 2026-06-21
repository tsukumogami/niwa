package workspace

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// claudeBaselineVersion is the known-good Claude Code version for per-repo
// WorktreeCreate/WorktreeRemove hooks. The feasibility spike
// (docs/spikes/SPIKE-niwa-default-worktree.md) exercised these hooks live
// against Claude Code CLI v2.1.183 and confirmed they fire at per-repo scope
// and replace default worktree creation. Versions at or above this baseline are
// treated as supporting worktree hooks.
const claudeBaselineVersion = "2.1.183"

// claudeVersionPattern extracts the leading semver-ish X.Y.Z from a version
// string. `claude --version` prints output like "2.1.183 (Claude Code)"; this
// matches the leading "2.1.183". Parsing is deliberately lenient so that
// surrounding text or extra version components do not defeat the match.
var claudeVersionPattern = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// SupportsWorktreeHooks reports whether the Claude Code harness on PATH honors
// the per-repo worktree hooks this integration relies on. It runs
// `claude --version`, parses the version, and compares it to the baseline
// (claudeBaselineVersion).
//
// The probe is OPTIMISTIC on failure: if `claude` is not on PATH, the command
// errors, or the version output is unparseable, it returns true (supported).
// This matches design Decision 4's "optimistic on probe error" and the
// PATH-trusted threat model, avoiding spurious denies when the harness cannot
// be probed. The deliberate fallback to unsupported only happens when a version
// is successfully parsed and is below the baseline.
func SupportsWorktreeHooks(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "claude", "--version").Output()
	if err != nil {
		// claude not on PATH or probe failed: optimistic — assume supported.
		return true
	}
	return supportsWorktreeHooks(string(out))
}

// supportsWorktreeHooks parses raw `claude --version` output and reports whether
// the version is at or above the baseline. Unparseable output is treated
// optimistically as supported. This is the pure, unit-testable comparator that
// SupportsWorktreeHooks wraps around the exec call.
func supportsWorktreeHooks(versionOutput string) bool {
	version, ok := parseClaudeVersion(versionOutput)
	if !ok {
		// Unparseable output: optimistic — assume supported.
		return true
	}
	baseline, _ := parseClaudeVersion(claudeBaselineVersion)
	return compareClaudeVersions(version, baseline) >= 0
}

// claudeVersion is a parsed X.Y.Z semver triple.
type claudeVersion struct {
	major int
	minor int
	patch int
}

// parseClaudeVersion extracts the leading X.Y.Z semver from s. It returns the
// parsed version and true on success, or the zero value and false when no
// semver-ish token is present. Parsing is defensive: it scans for the first
// X.Y.Z run anywhere in the string, tolerating surrounding text such as
// "(Claude Code)".
func parseClaudeVersion(s string) (claudeVersion, bool) {
	m := claudeVersionPattern.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return claudeVersion{}, false
	}
	// The regex guarantees three digit groups, so Atoi cannot fail here.
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return claudeVersion{major: major, minor: minor, patch: patch}, true
}

// compareClaudeVersions returns -1 if a < b, 0 if a == b, and 1 if a > b,
// comparing major, then minor, then patch.
func compareClaudeVersions(a, b claudeVersion) int {
	switch {
	case a.major != b.major:
		return cmpInt(a.major, b.major)
	case a.minor != b.minor:
		return cmpInt(a.minor, b.minor)
	default:
		return cmpInt(a.patch, b.patch)
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
