package config

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

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
// The decoder rejects non-string values in the top-level position. A
// non-table value at a reserved sub-table name is detected by
// validateReservedEnvKeys (run from Parse after decoding), which has
// access to the fully-qualified TOML path through md.Keys() and can
// name the exact sub-table the user should move the entry to.
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
//
// A scalar (non-table) value at one of the reserved sub-table keys is
// handled separately by validateReservedEnvKeys, run post-decode from
// Parse. Detecting the mistake there lets us attach the fully-qualified
// path (e.g., [env.vars.required], [claude.env.secrets.optional]) to
// the error. Here we simply return a non-fatal sentinel so decoding
// continues; the post-decode validator is authoritative.
func coerceDescriptionMap(name string, v any) (map[string]string, error) {
	tbl, ok := v.(map[string]any)
	if !ok {
		// Leave the field unset and defer to validateReservedEnvKeys.
		return nil, nil
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

// validateReservedEnvKeys scans the decoded TOML metadata for scalar
// values placed at reserved sub-table keys (required/recommended/
// optional) under any [env.vars], [env.secrets], [claude.env.vars], or
// [claude.env.secrets] table (including per-repo and per-instance
// overrides). A scalar at those positions almost always means the user
// confused a reserved sub-table name with a regular env var name; the
// silent fallback would classify their variable as a description map
// entry, which is surprising and hard to discover.
//
// The error names the exact path the user wrote and the sub-table they
// should move to, so they can pick the right intent.
func validateReservedEnvKeys(md toml.MetaData) error {
	for _, key := range md.Keys() {
		if len(key) < 3 {
			continue
		}
		last := key[len(key)-1]
		if !reservedEnvVarsSubtables[last] {
			continue
		}
		parent := key[:len(key)-1]
		if !isEnvVarsOrSecretsPath(parent) {
			continue
		}
		// md.Type returns "Hash" for tables. Anything else at a
		// reserved key is a scalar (or array) written at the wrong
		// level.
		if md.Type(key...) == "Hash" {
			continue
		}
		parentPath := strings.Join(parent, ".")
		fullPath := strings.Join(key, ".")
		return fmt.Errorf(
			"[%s] key %q is reserved for the %q sub-table (a "+
				"description map for team-declared expected "+
				"values). To declare an env variable with this "+
				"name, rename it. To declare description "+
				"metadata, move to [%s] with a description string",
			parentPath, last, last, fullPath,
		)
	}
	return nil
}

// isEnvVarsOrSecretsPath reports whether key is a path that ends in
// `env.vars` or `env.secrets`, matching any of the four locations where
// EnvVarsTable is used: `env.vars`, `env.secrets`, `claude.env.vars`,
// `claude.env.secrets`, plus their `repos.<name>.` and `instance.`
// nested variants. The tail of the path is sufficient; any prefix that
// decodes into an EnvVarsTable will have one of these suffixes.
func isEnvVarsOrSecretsPath(key []string) bool {
	if len(key) < 2 {
		return false
	}
	tail := key[len(key)-1]
	if tail != "vars" && tail != "secrets" {
		return false
	}
	return key[len(key)-2] == "env"
}

// IsEmpty reports whether this table has any values or requirement
// entries.
func (t EnvVarsTable) IsEmpty() bool {
	return len(t.Values) == 0 &&
		len(t.Required) == 0 &&
		len(t.Recommended) == 0 &&
		len(t.Optional) == 0
}
