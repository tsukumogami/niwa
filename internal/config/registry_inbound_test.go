package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestParseGlobalConfig_AcceptSessionMessagesOnDispatch(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want *bool
	}{
		{"unset", "[global]\nclone_protocol = \"ssh\"\n", nil},
		{"true", "[global]\naccept_session_messages_on_dispatch = true\n", boolPtr(true)},
		{"false", "[global]\naccept_session_messages_on_dispatch = false\n", boolPtr(false)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseGlobalConfig([]byte(tc.in))
			if err != nil {
				t.Fatalf("ParseGlobalConfig: %v", err)
			}
			got := cfg.Global.AcceptSessionMessagesOnDispatch
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("AcceptSessionMessagesOnDispatch = %v, want nil", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("AcceptSessionMessagesOnDispatch = nil, want %v", *tc.want)
			case tc.want != nil && got != nil && *got != *tc.want:
				t.Fatalf("AcceptSessionMessagesOnDispatch = %v, want %v", *got, *tc.want)
			}
		})
	}
}

// TestParseGlobalConfig_AcceptSessionMessagesOnDispatch_NonBoolean asserts that
// a non-boolean value fails the whole parse rather than only this key: the
// caller gets an error and no config, so the clone_protocol beside it is lost
// too.
func TestParseGlobalConfig_AcceptSessionMessagesOnDispatch_NonBoolean(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"string", "[global]\nclone_protocol = \"ssh\"\naccept_session_messages_on_dispatch = \"yes\"\n"},
		{"integer", "[global]\nclone_protocol = \"ssh\"\naccept_session_messages_on_dispatch = 1\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseGlobalConfig([]byte(tc.in))
			if err == nil {
				t.Fatalf("ParseGlobalConfig succeeded, want an error")
			}
			if cfg != nil {
				t.Fatalf("ParseGlobalConfig returned a config alongside the error: %+v", cfg)
			}
		})
	}
}

// TestLoadGlobalConfigFrom_AcceptSessionMessagesOnDispatch_NonBoolean asserts
// that a malformed value fails the file-level load with no config. The dispatch
// resolver treats any load error as "setting absent", so a malformed value
// leaves the behavior off rather than half-applied; the test deliberately
// accepts any error, since the resolver does not distinguish them.
func TestLoadGlobalConfigFrom_AcceptSessionMessagesOnDispatch_NonBoolean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	body := "[global]\naccept_session_messages_on_dispatch = \"yes\"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	cfg, err := LoadGlobalConfigFrom(path)
	if err == nil {
		t.Fatalf("LoadGlobalConfigFrom succeeded, want an error")
	}
	if cfg != nil {
		t.Fatalf("LoadGlobalConfigFrom returned a config alongside the error: %+v", cfg)
	}
}

// TestGlobalSettings_AcceptSessionMessagesOnDispatch_RoundTrip asserts that a
// save-then-load through the real file path preserves both explicit values and
// that the omitempty tag drops a nil pointer.
func TestGlobalSettings_AcceptSessionMessagesOnDispatch_RoundTrip(t *testing.T) {
	for _, want := range []bool{true, false} {
		t.Run(fmt.Sprintf("%v survives save and load", want), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			in := &GlobalConfig{Global: GlobalSettings{AcceptSessionMessagesOnDispatch: boolPtr(want)}}
			if err := SaveGlobalConfigTo(path, in); err != nil {
				t.Fatalf("SaveGlobalConfigTo: %v", err)
			}
			out, err := LoadGlobalConfigFrom(path)
			if err != nil {
				t.Fatalf("LoadGlobalConfigFrom: %v", err)
			}
			got := out.Global.AcceptSessionMessagesOnDispatch
			if got == nil {
				t.Fatalf("round-trip lost the value: got nil, want %v", want)
			}
			if *got != want {
				t.Fatalf("round-trip changed the value: got %v, want %v", *got, want)
			}
		})
	}

	t.Run("nil is omitted on encode", func(t *testing.T) {
		var buf bytes.Buffer
		in := GlobalConfig{Global: GlobalSettings{CloneProtocol: "ssh"}}
		if err := toml.NewEncoder(&buf).Encode(in); err != nil {
			t.Fatalf("encode: %v", err)
		}
		if bytes.Contains(buf.Bytes(), []byte("accept_session_messages_on_dispatch")) {
			t.Fatalf("nil pointer should be omitted, got:\n%s", buf.String())
		}
	})
}

// TestCrossSessionInboundKey pins the spelling Claude Code reads. The constant
// is the only place the key is written, and Claude Code ignores unknown settings
// keys, so a typo or rename would silently stop the dispatch setting from taking
// effect with nothing else failing.
func TestCrossSessionInboundKey(t *testing.T) {
	if CrossSessionInboundKey != "crossSessionInbound" {
		t.Fatalf("CrossSessionInboundKey = %q, want %q", CrossSessionInboundKey, "crossSessionInbound")
	}
}
