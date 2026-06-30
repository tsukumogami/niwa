package config

import (
	"bytes"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestParseGlobalConfig_RemoteControlOnDispatch(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want *bool
	}{
		{"unset", "[global]\nclone_protocol = \"ssh\"\n", nil},
		{"true", "[global]\nremote_control_on_dispatch = true\n", boolPtr(true)},
		{"false", "[global]\nremote_control_on_dispatch = false\n", boolPtr(false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseGlobalConfig([]byte(tc.in))
			if err != nil {
				t.Fatalf("ParseGlobalConfig: %v", err)
			}
			got := cfg.Global.RemoteControlOnDispatch
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("RemoteControlOnDispatch = %v, want nil", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("RemoteControlOnDispatch = nil, want %v", *tc.want)
			case tc.want != nil && got != nil && *got != *tc.want:
				t.Fatalf("RemoteControlOnDispatch = %v, want %v", *got, *tc.want)
			}
		})
	}
}

// TestGlobalSettings_RemoteControlOnDispatch_RoundTrip asserts encode-then-decode
// preserves the value and that the omitempty tag drops a nil pointer.
func TestGlobalSettings_RemoteControlOnDispatch_RoundTrip(t *testing.T) {
	t.Run("true survives round-trip", func(t *testing.T) {
		var buf bytes.Buffer
		in := GlobalConfig{Global: GlobalSettings{RemoteControlOnDispatch: boolPtr(true)}}
		if err := toml.NewEncoder(&buf).Encode(in); err != nil {
			t.Fatalf("encode: %v", err)
		}
		out, err := ParseGlobalConfig(buf.Bytes())
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if out.Global.RemoteControlOnDispatch == nil || !*out.Global.RemoteControlOnDispatch {
			t.Fatalf("round-trip lost the value: %v", out.Global.RemoteControlOnDispatch)
		}
	})

	t.Run("nil is omitted on encode", func(t *testing.T) {
		var buf bytes.Buffer
		in := GlobalConfig{Global: GlobalSettings{CloneProtocol: "ssh"}}
		if err := toml.NewEncoder(&buf).Encode(in); err != nil {
			t.Fatalf("encode: %v", err)
		}
		if bytes.Contains(buf.Bytes(), []byte("remote_control_on_dispatch")) {
			t.Fatalf("nil pointer should be omitted, got:\n%s", buf.String())
		}
	})
}
