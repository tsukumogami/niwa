package workspace

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// missingRequired describes a single key that was declared in a
// `*.required` sub-table but has no corresponding value in the
// parent table's Values map after merge.
type missingRequired struct {
	// Scope locates the table the key came from, e.g. "env.secrets",
	// "claude.env.vars", "repos.app.env.secrets", "instance.env.vars".
	Scope string

	// Key is the env var (or settings) name the team declared as
	// required.
	Key string

	// Description is the human-readable string the team wrote alongside
	// the key in the required sub-table.
	Description string
}

// checkRequiredKeys is where fatality is decided, once, against the
// post-merge picture. It implements the strict-when-reachable rule: a
// key listed under a `*.required` sub-table is fatal only when a
// provider was configured, was reachable, and reported that it does
// not hold the key. Recommended and optional sub-tables emit warnings
// (to stderrOut) and stay non-fatal.
//
// The check runs on the post-merge config so personal-overlay-supplied
// values satisfy team-declared required keys (the PRD's example: team
// lists GITHUB_TOKEN in [env.secrets.required], personal overlay
// supplies the value under [env.secrets]).
//
// Every other shortfall is tolerated here and reported instead. A
// contributor with no vault backend configured, or with the client
// binary absent, is not being told about a fault they can fix by
// editing configuration — they are being told the environment cannot
// supply the value, which is a different message and not a reason to
// refuse to materialize the instance.
//
// The signature deliberately does not take vault state. The mark on
// each value carries everything the decision needs, which keeps this
// post-merge check from coupling to the resolver.
func checkRequiredKeys(cfg *config.WorkspaceConfig, stderrOut io.Writer) error {
	if cfg == nil {
		return nil
	}

	var missing []missingRequired
	forEachEnvTable(cfg, func(scope string, t config.EnvVarsTable) {
		missing = append(missing, collectMissing(scope, t)...)
	})

	// Recommended: non-fatal, stderr warning line per miss.
	warnRecommended(cfg, stderrOut)

	if len(missing) == 0 {
		return nil
	}

	sort.Slice(missing, func(i, j int) bool {
		if missing[i].Scope != missing[j].Scope {
			return missing[i].Scope < missing[j].Scope
		}
		return missing[i].Key < missing[j].Key
	})

	var sb strings.Builder
	sb.WriteString("required env keys not supplied:\n")
	for _, m := range missing {
		fmt.Fprintf(&sb, "  [%s] %s: %s\n", m.Scope, m.Key, m.Description)
	}
	sb.WriteString("the provider was reached and does not hold these keys: " +
		"add each key to the provider, supply it via the personal overlay, " +
		"or remove it from the required sub-table")
	return fmt.Errorf("%s", sb.String())
}

// collectMissing returns the keys in t.Required that are fatal: those
// whose value is empty AND carries a mark saying a reachable provider
// does not hold the key.
//
// Three shapes are deliberately NOT fatal here:
//
//   - No entry in t.Values at all. Nothing ever tried to resolve the
//     key, so no provider capable of supplying it was configured. This
//     is the no-vault contributor's exact case.
//   - An empty value with no mark. Either an author wrote an empty
//     literal, or a vault:// reference opted out of resolution failure
//     with ?required=false. Both are deliberate empties, not
//     shortfalls.
//   - An empty value marked with any cause other than key-not-found:
//     an unreachable provider, an absent client binary, or a reference
//     naming a provider nothing declares. None of these establishes
//     that a reachable backend lacks the key.
//
// All three are reported rather than enforced.
func collectMissing(scope string, t config.EnvVarsTable) []missingRequired {
	if len(t.Required) == 0 {
		return nil
	}
	var out []missingRequired
	for key, desc := range t.Required {
		ms, ok := t.Values[key]
		if !ok || !isEmptyMaybeSecret(ms) {
			continue
		}
		if ms.Unresolved == nil || ms.Unresolved.Cause != config.CauseKeyNotFound {
			continue
		}
		out = append(out, missingRequired{
			Scope:       scope,
			Key:         key,
			Description: desc,
		})
	}
	return out
}

// isEmptyMaybeSecret reports whether a MaybeSecret carries neither a
// resolved secret nor a non-empty Plain. A marked (unresolved) value is
// empty by construction: the resolver clears Plain when it marks.
func isEmptyMaybeSecret(ms config.MaybeSecret) bool {
	if ms.IsSecret() {
		return false
	}
	return ms.Plain == ""
}

// warnRecommended scans every *.recommended sub-table and emits a
// single stderr line per miss. Optional sub-tables are silent in v1
// (no verbose flag yet; when one lands the loop can emit an info line
// under it).
func warnRecommended(cfg *config.WorkspaceConfig, stderrOut io.Writer) {
	forEachEnvTable(cfg, func(scope string, t config.EnvVarsTable) {
		for key, desc := range t.Recommended {
			ms, ok := t.Values[key]
			if ok && !isEmptyMaybeSecret(ms) {
				continue
			}
			fmt.Fprintf(stderrOut,
				"warning: recommended env key %q not supplied: %s (scope %s)\n",
				key, desc, scope)
		}
	})
}

// forEachEnvTable invokes fn once per env table in the config, passing the
// dotted scope the table was declared at. Three walks need this enumeration —
// the required-key check, the recommended warning, and the key report — and
// keeping one copy is what stops a table added to the config from reaching two
// of the three.
//
// Repo iteration follows map order, so callers that care about output order
// sort afterwards rather than relying on the walk.
func forEachEnvTable(cfg *config.WorkspaceConfig, fn func(scope string, t config.EnvVarsTable)) {
	if cfg == nil {
		return
	}
	fn("env.vars", cfg.Env.Vars)
	fn("env.secrets", cfg.Env.Secrets)
	fn("claude.env.vars", cfg.Claude.Env.Vars)
	fn("claude.env.secrets", cfg.Claude.Env.Secrets)

	for name, ov := range cfg.Repos {
		prefix := fmt.Sprintf("repos.%s", name)
		fn(prefix+".env.vars", ov.Env.Vars)
		fn(prefix+".env.secrets", ov.Env.Secrets)
		if ov.Claude != nil {
			fn(prefix+".claude.env.vars", ov.Claude.Env.Vars)
			fn(prefix+".claude.env.secrets", ov.Claude.Env.Secrets)
		}
	}

	fn("instance.env.vars", cfg.Instance.Env.Vars)
	fn("instance.env.secrets", cfg.Instance.Env.Secrets)
	if cfg.Instance.Claude != nil {
		fn("instance.claude.env.vars", cfg.Instance.Claude.Env.Vars)
		fn("instance.claude.env.secrets", cfg.Instance.Claude.Env.Secrets)
	}
}

// forEachSettingsTable is the same enumeration for [claude.settings] blocks.
// Settings have no requirement sub-tables, so they contribute marks to the key
// report and nothing to the required or recommended checks — which is why they
// have their own walk rather than a widened EnvVarsTable one.
func forEachSettingsTable(cfg *config.WorkspaceConfig, fn func(scope string, s config.SettingsConfig)) {
	if cfg == nil {
		return
	}
	fn("claude.settings", cfg.Claude.Settings)
	for name, ov := range cfg.Repos {
		if ov.Claude != nil {
			fn(fmt.Sprintf("repos.%s.claude.settings", name), ov.Claude.Settings)
		}
	}
	if cfg.Instance.Claude != nil {
		fn("instance.claude.settings", cfg.Instance.Claude.Settings)
	}
}
