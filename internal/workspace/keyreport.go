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
		// Optional is walked alongside required and recommended even though
		// nothing enforces it: the report carries each key's declared level,
		// and a column that silently omitted one of the three levels would be
		// wrong rather than merely incomplete.
		collectDeclaredWithNoValue(scope, t, config.LevelRequired, t.Required, c)
		collectDeclaredWithNoValue(scope, t, config.LevelRecommended, t.Recommended, c)
		collectDeclaredWithNoValue(scope, t, config.LevelOptional, t.Optional, c)
	})

	forEachSettingsTable(cfg, func(scope string, s config.SettingsConfig) {
		for key, ms := range s {
			c.AddMark(scope, key, ms.Unresolved)
		}
	})
}

// collectDeclaredWithNoValue records the keys of one requirement sub-table that
// have no entry in the parent table's values map.
//
// A key that HAS an entry is skipped whatever that entry holds. An entry with a
// mark was already recorded from the mark, which carries a truer cause than
// this walk could infer. An empty entry with no mark is a deliberate empty —
// an author's empty literal, or a reference whose ?required=false opted out of
// resolution failure — and reporting it would break the silence that opt-out
// exists to provide.
func collectDeclaredWithNoValue(
	scope string,
	t config.EnvVarsTable,
	level config.RequirementLevel,
	sub map[string]string,
	c *keyreport.Collector,
) {
	for key, desc := range sub {
		if _, ok := t.Values[key]; ok {
			continue
		}
		c.Add(keyreport.Entry{
			Scope:       scope,
			Key:         key,
			Cause:       keyreport.CauseNoSource,
			Level:       level,
			Description: desc,
		})
	}
}
