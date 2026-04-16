package config

import "fmt"

// reservedEnvVarsSubtables lists the sub-table keys that EnvVarsTable
// carves out for the three-level requirement ladder. Keys with these
// names cannot appear as env var names in the top-level value map; a
// TOML author writing `[env.vars.required]` is classifying, not
// declaring a variable called "required".
var reservedEnvVarsSubtables = map[string]bool{
	"required":    true,
	"recommended": true,
	"optional":    true,
}

// UnmarshalTOML implements toml.Unmarshaler for EnvVarsTable. It routes
// each top-level key under [env.vars] (or [env.secrets], and their
// claude.env counterparts) into one of four buckets:
//
//   - "required" / "recommended" / "optional" sub-tables, each
//     map[string]string of key→description.
//   - Every other key populates Values as MaybeSecret{Plain: <string>}.
//
// The decoder rejects non-string values in the top-level position and
// non-table values for the three reserved sub-tables.
func (t *EnvVarsTable) UnmarshalTOML(data any) error {
	t.Values = nil
	t.Required = nil
	t.Recommended = nil
	t.Optional = nil

	raw, ok := data.(map[string]any)
	if !ok {
		return fmt.Errorf("env vars/secrets table must be a TOML table, got %T", data)
	}

	for k, v := range raw {
		if reservedEnvVarsSubtables[k] {
			descMap, err := coerceDescriptionMap(k, v)
			if err != nil {
				return err
			}
			switch k {
			case "required":
				t.Required = descMap
			case "recommended":
				t.Recommended = descMap
			case "optional":
				t.Optional = descMap
			}
			continue
		}
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf(
				"env vars/secrets: value for %q must be a string, got %T",
				k, v,
			)
		}
		if t.Values == nil {
			t.Values = make(map[string]MaybeSecret)
		}
		t.Values[k] = MaybeSecret{Plain: s}
	}
	return nil
}

// coerceDescriptionMap converts a TOML sub-table value into a
// map[string]string suitable for a requirement-description table. It
// rejects non-string description values.
func coerceDescriptionMap(name string, v any) (map[string]string, error) {
	tbl, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(
			"env vars/secrets.%s must be a TOML table, got %T",
			name, v,
		)
	}
	if len(tbl) == 0 {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(tbl))
	for k, val := range tbl {
		s, ok := val.(string)
		if !ok {
			return nil, fmt.Errorf(
				"env vars/secrets.%s[%q] description must be a string, got %T",
				name, k, val,
			)
		}
		out[k] = s
	}
	return out, nil
}

// IsEmpty reports whether this table has any values or requirement
// entries.
func (t EnvVarsTable) IsEmpty() bool {
	return len(t.Values) == 0 &&
		len(t.Required) == 0 &&
		len(t.Recommended) == 0 &&
		len(t.Optional) == 0
}
