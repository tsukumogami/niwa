package cli

import "testing"

func TestReadInstanceSettings_RemoteControlAtStartup(t *testing.T) {
	cases := []struct {
		name string
		body string
		want *bool // nil => key absent
	}{
		{"true", `{"remoteControlAtStartup": true}`, rcBoolPtr(true)},
		{"false", `{"remoteControlAtStartup": false}`, rcBoolPtr(false)},
		{"absent", `{"enabledPlugins": {}}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instance := writeInstanceSettings(t, tc.body)
			s, err := readInstanceSettings(instance)
			if err != nil {
				t.Fatalf("readInstanceSettings: %v", err)
			}
			got := s.RemoteControlAtStartup
			switch {
			case tc.want == nil && got != nil:
				t.Fatalf("RemoteControlAtStartup = %v, want nil", *got)
			case tc.want != nil && got == nil:
				t.Fatalf("RemoteControlAtStartup = nil, want %v", *tc.want)
			case tc.want != nil && got != nil && *got != *tc.want:
				t.Fatalf("RemoteControlAtStartup = %v, want %v", *got, *tc.want)
			}
		})
	}
}
