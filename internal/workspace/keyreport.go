package workspace

import (
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/keyreport"
)

// collectUnresolvedKeys records every key the run could not supply into the
// caller's collector. It runs once, on the post-merge config, beside the
// required-key check.
//
// It has two sources, and both are necessary:
//
//   - The marks carried on values. A vault:// reference that could not be
//     resolved leaves an empty value with an Unresolved mark on it, and that
//     mark holds the cause, the declared level and description, and the kind of
//     the provider that was asked.
//
//   - Keys declared in a requirement sub-table with no entry in the values map
//     at all. The resolver's walker never visits those — there is no value to
//     hang a mark on — and that is exactly the shape a contributor with no
//     provider configured hits: nothing in the configuration references the key,
//     so nothing tried and failed. A report built only by scanning marks comes
//     back empty for the user this reporting exists to serve.
//
// The walk runs post-merge rather than inside the resolver on purpose. Values
// resolve layer by layer and the last layer wins, so a key marked unresolved
// while resolving the team config may be supplied by the personal overlay
// moments later. Recording at mark time would report that key as missing when
// it has a value; recording here reports the merged truth.
func collectUnresolvedKeys(cfg *config.WorkspaceConfig, c *keyreport.Collector) {
	if cfg == nil || c == nil {
		return
	}

	forEachEnvTable(cfg, func(scope string, t config.EnvVarsTable) {
		for key, ms := range t.Values {
			c.AddMark(scope, key, ms.Unresolved)
		}
		forEachDeclaredWithNoValue(t, func(key string, level config.RequirementLevel, desc string) {
			c.Add(keyreport.Entry{
				Scope:       scope,
				Key:         key,
				Cause:       keyreport.CauseNoSource,
				Level:       level,
				Description: desc,
			})
		})
	})

	forEachSettingsTable(cfg, func(scope string, s config.SettingsConfig) {
		for key, ms := range s {
			c.AddMark(scope, key, ms.Unresolved)
		}
	})
}

// forEachDeclaredWithNoValue invokes fn for every key declared in one of the
// table's requirement sub-tables that has no entry in the table's values map —
// the second shape of unresolved key, the one no mark can describe.
//
// A key that HAS an entry is skipped whatever that entry holds. An entry with a
// mark is already accounted for by the mark, which carries a truer cause than
// this walk could infer. An empty entry with no mark is a deliberate empty —
// an author's empty literal, or a reference whose ?required=false opted out of
// resolution failure — and reporting it would break the silence that opt-out
// exists to provide.
//
// Optional is walked alongside required and recommended even though nothing
// enforces it: the report carries each key's declared level, and a column that
// silently omitted one of the three levels would be wrong rather than merely
// incomplete.
//
// This is the single derivation of the shape. The key report and the
// [claude.env] promote branch both consume it, because a promoted key that was
// declared and unsupplied is the same shortfall the report describes; two walks
// would be two chances to disagree about which keys those are.
func forEachDeclaredWithNoValue(t config.EnvVarsTable, fn func(key string, level config.RequirementLevel, desc string)) {
	subs := []struct {
		level config.RequirementLevel
		keys  map[string]string
	}{
		{config.LevelRequired, t.Required},
		{config.LevelRecommended, t.Recommended},
		{config.LevelOptional, t.Optional},
	}
	for _, sub := range subs {
		for key, desc := range sub.keys {
			if _, ok := t.Values[key]; ok {
				continue
			}
			fn(key, sub.level, desc)
		}
	}
}
