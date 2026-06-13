package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// OutputFormat is the serialization niwa writes a secret-output target in:
// dotenv (KEY=value), json (flat object), or shell (export KEY='value'). An
// empty OutputFormat on an OutputTarget means "infer from the path extension"
// (see InferFormat); a resolved target (ResolvedTarget) always carries a
// concrete format.
type OutputFormat string

const (
	// FormatDotenv is the historical .local.env serialization (KEY=value).
	FormatDotenv OutputFormat = "dotenv"
	// FormatJSON is a flat JSON object of the resolved secrets.
	FormatJSON OutputFormat = "json"
	// FormatShell is sourceable shell-export (export KEY='value').
	FormatShell OutputFormat = "shell"
)

// UnmarshalText implements encoding.TextUnmarshaler so an explicit `format`
// value in TOML decodes into OutputFormat. Only the three known formats are
// accepted; any other value is a parse error. Mirrors the Action enum.
func (f *OutputFormat) UnmarshalText(text []byte) error {
	switch s := string(text); OutputFormat(s) {
	case FormatDotenv, FormatJSON, FormatShell:
		*f = OutputFormat(s)
		return nil
	default:
		return fmt.Errorf("invalid env_output format %q (want \"dotenv\", \"json\", or \"shell\")", s)
	}
}

// MarshalText implements encoding.TextMarshaler, emitting the format literal.
func (f OutputFormat) MarshalText() ([]byte, error) { return []byte(f), nil }

// OutputTarget is a single secret-output destination: a repo-relative path and
// an optional explicit format. An empty Format means the format is inferred
// from the path extension.
type OutputTarget struct {
	Path   string       `toml:"path"`
	Format OutputFormat `toml:"format,omitempty"`
}

// OutputTargets is the list of secret-output targets declared at one config
// rung (workspace, per-repo, or personal/global). It decodes from three TOML
// shapes:
//
//   - a bare string: one target, format inferred (output = ".env.local")
//   - a list of strings: each a target, format inferred
//     (output = [".env.local", "secrets.json"])
//   - a list of tables: each {path, format} (output = [{ path = "x", format = "shell" }])
//
// A single array mixing bare strings and tables is not a supported shape; use a
// list of tables when any element needs an explicit format.
type OutputTargets []OutputTarget

// UnmarshalTOML implements toml.Unmarshaler (BurntSushi/toml passes the raw
// decoded value). It normalizes the three accepted shapes into []OutputTarget.
func (t *OutputTargets) UnmarshalTOML(v interface{}) error {
	switch val := v.(type) {
	case string:
		*t = OutputTargets{{Path: val}}
		return nil
	case []interface{}:
		out := make(OutputTargets, 0, len(val))
		for i, elem := range val {
			tgt, err := outputTargetFromAny(elem)
			if err != nil {
				return fmt.Errorf("env_output[%d]: %w", i, err)
			}
			out = append(out, tgt)
		}
		*t = out
		return nil
	case map[string]interface{}:
		tgt, err := outputTargetFromAny(val)
		if err != nil {
			return err
		}
		*t = OutputTargets{tgt}
		return nil
	default:
		return fmt.Errorf("env_output must be a string, a list of strings/tables, or a table; got %T", v)
	}
}

// outputTargetFromAny decodes one list element (a string or a {path, format}
// table) into an OutputTarget.
func outputTargetFromAny(v interface{}) (OutputTarget, error) {
	switch val := v.(type) {
	case string:
		return OutputTarget{Path: val}, nil
	case map[string]interface{}:
		p, ok := val["path"]
		if !ok {
			return OutputTarget{}, fmt.Errorf("target table missing required \"path\" key")
		}
		ps, ok := p.(string)
		if !ok {
			return OutputTarget{}, fmt.Errorf("target \"path\" must be a string, got %T", p)
		}
		tgt := OutputTarget{Path: ps}
		if f, ok := val["format"]; ok {
			fs, ok := f.(string)
			if !ok {
				return OutputTarget{}, fmt.Errorf("target \"format\" must be a string, got %T", f)
			}
			var of OutputFormat
			if err := of.UnmarshalText([]byte(fs)); err != nil {
				return OutputTarget{}, err
			}
			tgt.Format = of
		}
		return tgt, nil
	default:
		return OutputTarget{}, fmt.Errorf("target must be a string or a table, got %T", v)
	}
}

// DefaultEnvOutputPath is the secret-output target used when no env_output is
// configured at any rung. It preserves niwa's historical behavior.
const DefaultEnvOutputPath = ".local.env"

// ResolvedTarget is a fully resolved secret-output target: a path plus a
// concrete (never-inferred) format.
type ResolvedTarget struct {
	Path   string
	Format OutputFormat
}

// InferFormat returns the format implied by a target path's extension:
// .json -> json, .sh -> shell, everything else (including .env, .local.env,
// .env.local, and extensionless names) -> dotenv.
func InferFormat(path string) OutputFormat {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return FormatJSON
	case ".sh":
		return FormatShell
	default:
		return FormatDotenv
	}
}

// resolve turns declared targets into resolved ones, filling each unset format
// from the path extension.
func (t OutputTargets) resolve() []ResolvedTarget {
	out := make([]ResolvedTarget, 0, len(t))
	for _, tgt := range t {
		f := tgt.Format
		if f == "" {
			f = InferFormat(tgt.Path)
		}
		out = append(out, ResolvedTarget{Path: tgt.Path, Format: f})
	}
	return out
}

// EffectiveEnvOutput resolves the secret-output targets for one repo. Precedence
// is most-specific-wins at the list level: a non-empty per-repo env_output
// replaces a non-empty workspace env_output, which replaces a non-empty global
// env_output; an unset (empty) rung inherits the next broader one. When no rung
// sets it, the result is a single .local.env dotenv target, preserving historical
// behavior. global is the resolved personal/global-override list, passed
// explicitly because it is not part of WorkspaceConfig (mirrors
// EffectiveEnvExamplePolicy).
func EffectiveEnvOutput(global OutputTargets, ws *WorkspaceConfig, repoName string) []ResolvedTarget {
	if ws != nil {
		if override, ok := ws.Repos[repoName]; ok && len(override.EnvOutput) > 0 {
			return override.EnvOutput.resolve()
		}
		if len(ws.Workspace.EnvOutput) > 0 {
			return ws.Workspace.EnvOutput.resolve()
		}
	}
	if len(global) > 0 {
		return global.resolve()
	}
	return []ResolvedTarget{{Path: DefaultEnvOutputPath, Format: FormatDotenv}}
}
