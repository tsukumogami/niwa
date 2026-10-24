package config

import (
	"strings"
	"testing"
)

func TestGlobalConfigPrincipal(t *testing.T) {
	cases := []struct {
		name, toml, want string
		bad              bool
	}{
		{name: "absent", toml: "[global]\n"},
		{name: "email", toml: "[global]\nprincipal = \"dana+ci@example.com\"\n", want: "dana+ci@example.com"},
		{name: "every allowed character", toml: "[global]\nprincipal = \"A.z_0-9@+\"\n", want: "A.z_0-9@+"},
		{name: "128 characters", toml: "[global]\nprincipal = \"" + strings.Repeat("a", 128) + "\"\n", want: strings.Repeat("a", 128)},
		{name: "129 characters", toml: "[global]\nprincipal = \"" + strings.Repeat("a", 129) + "\"\n", bad: true},
		{name: "a space", toml: "[global]\nprincipal = \"Dana Smith\"\n", bad: true},
		{name: "a slash", toml: "[global]\nprincipal = \"corp/dana\"\n", bad: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gc, err := ParseGlobalConfig([]byte(tc.toml))
			if err != nil {
				t.Fatal(err)
			}
			got, err := gc.Principal()
			if tc.bad {
				if err == nil || !strings.Contains(err.Error(), "[global] principal") {
					t.Errorf("err = %v, want one naming [global] principal", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("Principal() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	var nilConfig *GlobalConfig
	if got, err := nilConfig.Principal(); got != "" || err != nil {
		t.Errorf("nil config: %q, %v", got, err)
	}
}
