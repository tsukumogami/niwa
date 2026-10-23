package config

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

const hostEnvConfigBody = `[global]
clone_protocol = "https"

[env]
unset = ["OLD_KEY"]

[env.vars]
FOO = "bar"

[claude.env]
unset = ["KOTO_SESSION_BACKEND"]

[claude.env.vars]
EMPTY = ""
`

func TestParseGlobalConfigHostEnv(t *testing.T) {
	g, err := ParseGlobalConfig([]byte(hostEnvConfigBody))
	if err != nil {
		t.Fatalf("ParseGlobalConfig: %v", err)
	}
	want := &HostEnvLayer{
		Env:    HostEnvTable{Vars: map[string]string{"FOO": "bar"}, Unset: []string{"OLD_KEY"}},
		Claude: HostEnvTable{Vars: map[string]string{"EMPTY": ""}, Unset: []string{"KOTO_SESSION_BACKEND"}},
	}
	if got := g.HostEnv(); !reflect.DeepEqual(got, want) {
		t.Errorf("HostEnv() = %+v, want %+v", got, want)
	}
	if err := g.HostEnv().Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestHostEnvAbsent(t *testing.T) {
	g, err := ParseGlobalConfig([]byte("[global]\nclone_protocol = \"https\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if layer := g.HostEnv(); layer != nil {
		t.Errorf("HostEnv() = %+v, want nil", layer)
	}
	var nilCfg *GlobalConfig
	if nilCfg.HostEnv() != nil {
		t.Error("nil config must have no layer")
	}
	var nilLayer *HostEnvLayer
	if err := nilLayer.Validate(); err != nil {
		t.Errorf("nil layer Validate: %v", err)
	}
}

func TestHostEnvValidate(t *testing.T) {
	cases := []struct {
		name  string
		layer HostEnvLayer
		want  string
	}{
		{"set and unset", HostEnvLayer{Claude: HostEnvTable{Vars: map[string]string{"K": "v"}, Unset: []string{"K"}}}, "[claude.env] K: key is both set"},
		{"vault ref", HostEnvLayer{Env: HostEnvTable{Vars: map[string]string{"K": "vault://p/k"}}}, "[env.vars] K: host-local values are literal"},
		{"empty var key", HostEnvLayer{Env: HostEnvTable{Vars: map[string]string{" ": "v"}}}, "[env.vars]: empty key"},
		{"empty unset key", HostEnvLayer{Claude: HostEnvTable{Unset: []string{""}}}, "[claude.env] unset: empty key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.layer.Validate()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Validate() = %v, want an error containing %q", err, c.want)
			}
		})
	}
	// The same key in different tables is not a conflict: clearing it from the
	// repo env while setting it for the session is a coherent request.
	ok := HostEnvLayer{
		Env:    HostEnvTable{Unset: []string{"K"}},
		Claude: HostEnvTable{Vars: map[string]string{"K": "v"}},
	}
	if err := ok.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

// A `niwa config set` rewrites the file from the struct, so the host tables
// must survive it, and a file without them must encode exactly as before.
func TestSaveGlobalConfigHostEnvRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	g, err := ParseGlobalConfig([]byte(hostEnvConfigBody))
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveGlobalConfigTo(path, g); err != nil {
		t.Fatal(err)
	}
	reread, err := LoadGlobalConfigFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reread.HostEnv(), g.HostEnv()) {
		t.Errorf("host layer after save = %+v, want %+v", reread.HostEnv(), g.HostEnv())
	}

	plain := &GlobalConfig{Global: GlobalSettings{CloneProtocol: "https"}}
	if err := SaveGlobalConfigTo(path, plain); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// What the encoder wrote for this struct before the host tables existed.
	type before struct {
		Global       GlobalSettings           `toml:"global"`
		GlobalConfig GlobalConfigSource       `toml:"global_config"`
		Registry     map[string]RegistryEntry `toml:"registry"`
	}
	var want bytes.Buffer
	if err := toml.NewEncoder(&want).Encode(before{Global: plain.Global}); err != nil {
		t.Fatal(err)
	}
	if string(data) != want.String() {
		t.Errorf("config without host tables encodes differently:\n got: %q\nwant: %q", data, want.String())
	}
}
