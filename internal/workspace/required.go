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

// checkRequiredKeys enforces PRD R33/R34: every key listed under a
// `*.required` sub-table in the effective (post-merge) workspace config
// MUST have a non-empty value in the parent Values map. Recommended and
// optional sub-tables emit warnings (to stderrOut) and stay non-fatal.
//
// The check runs on the post-merge config so personal-overlay-supplied
// values satisfy team-declared required keys (R33 example in the PRD:
// team lists GITHUB_TOKEN in [env.secrets.required], personal overlay
// supplies the value under [env.secrets]).
//
// --allow-missing-secrets (a.AllowMissingSecrets in the caller) does
// NOT downgrade required misses per R34. The resolver has already
// downgraded vault-backed required keys to empty MaybeSecret values
// when the flag is set; this check catches that downgrade by looking
// at the post-resolve value and failing if it's empty. The key point:
// the flag routes around a resolver miss, but *.required is about
// whether the value is present at apply time — if the downgrade left
// the value empty, the required check fires regardless of the flag.
func checkRequiredKeys(cfg *config.WorkspaceConfig, stderrOut io.Writer) error {
	if cfg == nil {
		return nil
	}

	var missing []missingRequired

	// Top-level [env.vars] / [env.secrets].
	missing = append(missing, collectMissing("env.vars", cfg.Env.Vars)...)
	missing = append(missing, collectMissing("env.secrets", cfg.Env.Secrets)...)

	// Top-level [claude.env.vars] / [claude.env.secrets].
	missing = append(missing, collectMissing("claude.env.vars", cfg.Claude.Env.Vars)...)
	missing = append(missing, collectMissing("claude.env.secrets", cfg.Claude.Env.Secrets)...)

	// Per-repo overrides.
	for name, ov := range cfg.Repos {
		prefix := fmt.Sprintf("repos.%s", name)
		missing = append(missing, collectMissing(prefix+".env.vars", ov.Env.Vars)...)
		missing = append(missing, collectMissing(prefix+".env.secrets", ov.Env.Secrets)...)
		if ov.Claude != nil {
			missing = append(missing, collectMissing(prefix+".claude.env.vars", ov.Claude.Env.Vars)...)
			missing = append(missing, collectMissing(prefix+".claude.env.secrets", ov.Claude.Env.Secrets)...)
		}
	}

	// Instance-level overrides.
	missing = append(missing, collectMissing("instance.env.vars", cfg.Instance.Env.Vars)...)
	missing = append(missing, collectMissing("instance.env.secrets", cfg.Instance.Env.Secrets)...)
	if cfg.Instance.Claude != nil {
		missing = append(missing, collectMissing("instance.claude.env.vars", cfg.Instance.Claude.Env.Vars)...)
		missing = append(missing, collectMissing("instance.claude.env.secrets", cfg.Instance.Claude.Env.Secrets)...)
	}

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
	sb.WriteString("declare each key under the matching table with a value, " +
		"supply it via the personal overlay, or remove it from the required sub-table")
	return fmt.Errorf("%s", sb.String())
}

// collectMissing returns the ordered list of keys in t.Required that
// have no corresponding non-empty value in t.Values. Non-empty means
// either a resolved secret (IsSecret() == true) or a non-empty Plain.
// The resolver may downgrade a missing vault-backed required key to an
// empty MaybeSecret under --allow-missing-secrets; that's exactly the
// R34 case this function catches.
func collectMissing(scope string, t config.EnvVarsTable) []missingRequired {
	if len(t.Required) == 0 {
		return nil
	}
	var out []missingRequired
	for key, desc := range t.Required {
		ms, ok := t.Values[key]
		if ok && !isEmptyMaybeSecret(ms) {
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
// resolved secret nor a non-empty Plain. A resolver-downgraded required
// key under --allow-missing-secrets produces a fully-zero MaybeSecret
// (both Secret and Plain empty), which is exactly what this function
// rejects so R34 stays enforced.
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
	emit := func(scope string, t config.EnvVarsTable) {
		for key, desc := range t.Recommended {
			ms, ok := t.Values[key]
			if ok && !isEmptyMaybeSecret(ms) {
				continue
			}
			fmt.Fprintf(stderrOut,
				"warning: recommended env key %q not supplied: %s (scope %s)\n",
				key, desc, scope)
		}
	}

	emit("env.vars", cfg.Env.Vars)
	emit("env.secrets", cfg.Env.Secrets)
	emit("claude.env.vars", cfg.Claude.Env.Vars)
	emit("claude.env.secrets", cfg.Claude.Env.Secrets)

	for name, ov := range cfg.Repos {
		prefix := fmt.Sprintf("repos.%s", name)
		emit(prefix+".env.vars", ov.Env.Vars)
		emit(prefix+".env.secrets", ov.Env.Secrets)
		if ov.Claude != nil {
			emit(prefix+".claude.env.vars", ov.Claude.Env.Vars)
			emit(prefix+".claude.env.secrets", ov.Claude.Env.Secrets)
		}
	}
	emit("instance.env.vars", cfg.Instance.Env.Vars)
	emit("instance.env.secrets", cfg.Instance.Env.Secrets)
	if cfg.Instance.Claude != nil {
		emit("instance.claude.env.vars", cfg.Instance.Claude.Env.Vars)
		emit("instance.claude.env.secrets", cfg.Instance.Claude.Env.Secrets)
	}
}
