package config

import (
	"bytes"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestParseGlobalConfig_DefaultAgent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"unset", "[global]\nclone_protocol = \"ssh\"\n", ""},
		{"no global section at all", "[registry]\n", ""},
		{"codex", "[global]\ndefault_agent = \"codex\"\n", "codex"},
		{"claude", "[global]\ndefault_agent = \"claude\"\n", "claude"},
		// The field is a raw string, not a validated type. An unknown value
		// decodes here and is rejected later at the single agent.ParseAgent
		// boundary, so nothing can skip validation by trusting the field's type.
		{"unknown value still decodes", "[global]\ndefault_agent = \"gemini\"\n", "gemini"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseGlobalConfig([]byte(tc.in))
			if err != nil {
				t.Fatalf("ParseGlobalConfig: %v", err)
			}
			if got := cfg.DefaultAgent(); got != tc.want {
				t.Fatalf("DefaultAgent() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGlobalConfig_DefaultAgent_NilReceiver holds the tolerance the dispatch
// path depends on: a host config niwa could not load reads as "unset", not as a
// panic.
func TestGlobalConfig_DefaultAgent_NilReceiver(t *testing.T) {
	var cfg *GlobalConfig
	if got := cfg.DefaultAgent(); got != "" {
		t.Fatalf("(*GlobalConfig)(nil).DefaultAgent() = %q, want %q", got, "")
	}
}

// TestGlobalSettings_DefaultAgent_RoundTrip asserts encode-then-decode
// preserves the value and that the omitempty tag drops an empty one, so
// `niwa config set` followed by `niwa config unset` leaves no stray key behind.
func TestGlobalSettings_DefaultAgent_RoundTrip(t *testing.T) {
	t.Run("value survives round-trip", func(t *testing.T) {
		var buf bytes.Buffer
		in := GlobalConfig{Global: GlobalSettings{DefaultAgent: "codex"}}
		if err := toml.NewEncoder(&buf).Encode(in); err != nil {
			t.Fatalf("encode: %v", err)
		}
		out, err := ParseGlobalConfig(buf.Bytes())
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got := out.DefaultAgent(); got != "codex" {
			t.Fatalf("round-trip gave %q, want %q", got, "codex")
		}
	})

	t.Run("empty is omitted on encode", func(t *testing.T) {
		var buf bytes.Buffer
		in := GlobalConfig{Global: GlobalSettings{CloneProtocol: "ssh"}}
		if err := toml.NewEncoder(&buf).Encode(in); err != nil {
			t.Fatalf("encode: %v", err)
		}
		if bytes.Contains(buf.Bytes(), []byte("default_agent")) {
			t.Fatalf("an empty default_agent should be omitted, got:\n%s", buf.String())
		}
	})
}
