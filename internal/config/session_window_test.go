package config

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// windowProfile is a profile that sets only a compaction window.
func windowProfile(value string) string {
	return "[session.dispatch_profiles.worker]\nauto_compact_window = " + value + "\n"
}

// TestAutoCompactWindowLoads: an integer from 100000 to 1000000 loads, alone
// or beside drops, and reads back as the integer it is.
func TestAutoCompactWindowLoads(t *testing.T) {
	for _, tc := range []struct {
		literal string
		want    int64
	}{
		{"100000", 100000},
		{"400000", 400000},
		{"1_000_000", 1000000},
		{"1000000", 1000000},
	} {
		t.Run(tc.literal, func(t *testing.T) {
			result, err := Parse([]byte(sessionConfigWith(windowProfile(tc.literal))))
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if len(result.Warnings) != 0 {
				t.Errorf("warnings: %v", result.Warnings)
			}
			p := result.Config.Session.DispatchProfiles["worker"]
			if got, ok := p.AutoCompactWindow.Value(); !ok || got != tc.want {
				t.Errorf("Value() = %d, %v; want %d, true", got, ok, tc.want)
			}
			if p.IsEmpty() {
				t.Error("a profile that sets a window reports itself empty")
			}
		})
	}
	t.Run("beside drops", func(t *testing.T) {
		result, err := Parse([]byte(sessionConfigWith(coordinatorProfile + "auto_compact_window = 250000\n")))
		if err != nil {
			t.Fatalf("refused: %v", err)
		}
		if got, ok := result.Config.Session.DispatchProfiles["coordinator"].AutoCompactWindow.Value(); !ok || got != 250000 {
			t.Errorf("Value() = %d, %v", got, ok)
		}
	})
	t.Run("absent", func(t *testing.T) {
		result, err := Parse([]byte(sessionConfigWith(coordinatorProfile)))
		if err != nil {
			t.Fatalf("refused: %v", err)
		}
		if w := result.Config.Session.DispatchProfiles["coordinator"].AutoCompactWindow; w.IsSet() {
			t.Errorf("a profile without the key reports a window: %+v", w)
		}
	})
}

// TestAutoCompactWindowRefusals: every value that isn't an integer inside the
// bounds is refused at load, naming the profile and the key, never with the
// TOML decoder's own message.
func TestAutoCompactWindowRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		literal string
		also    string
	}{
		{"one below", "99999", `"99999"`},
		{"one above", "1000001", `"1000001"`},
		{"zero", "0", `"0"`},
		{"negative", "-400000", `"-400000"`},
		{"float", "400000.0", "float"},
		{"exponent float", "4e5", "float"},
		{"string", `"400000"`, "string"},
		{"bool", "true", "not an integer"},
		{"array", "[400000]", "not an integer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(sessionConfigWith(windowProfile(tc.literal))))
			if err == nil {
				t.Fatal("loaded without complaint")
			}
			for _, m := range []string{"session.dispatch_profiles.worker.auto_compact_window: ", tc.also, "100000", "1000000"} {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("the error does not say %s: %v", m, err)
				}
			}
		})
	}
}

// TestDispatchProfileWithNothingStillRefused: a profile with no drop and no
// window is refused, and the message names the window as one way out.
func TestDispatchProfileWithNothingStillRefused(t *testing.T) {
	_, err := Parse([]byte(sessionConfigWith("[session.dispatch_profiles.coord]\ndrop_tools = []\ndrop_bundled_skills = false\n")))
	if err == nil {
		t.Fatal("loaded without complaint")
	}
	for _, m := range []string{"session.dispatch_profiles.coord", "auto_compact_window"} {
		if !strings.Contains(err.Error(), m) {
			t.Errorf("the error does not say %s: %v", m, err)
		}
	}
}

// TestAutoCompactWindowNameRefusedInDispatchEnv: the window's name is a
// reserved CLAUDE_ name in [session.dispatch_env], in either list and any
// letter case, whether or not a profile sets a window.
func TestAutoCompactWindowNameRefusedInDispatchEnv(t *testing.T) {
	for _, profile := range []string{"", windowProfile("400000")} {
		for _, list := range []string{"passthrough", "unset"} {
			for _, entry := range []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "claude_code_auto_compact_window"} {
				t.Run(fmt.Sprintf("%s/%s/windowed=%v", list, entry, profile != ""), func(t *testing.T) {
					_, err := Parse([]byte(sessionConfigWith(profile + fmt.Sprintf("[session.dispatch_env]\n%s = [%q]\n", list, entry))))
					if err == nil {
						t.Fatal("loaded without complaint")
					}
					for _, m := range []string{fmt.Sprintf("%q", entry), "session.dispatch_env." + list, "reserved"} {
						if !strings.Contains(err.Error(), m) {
							t.Errorf("the error does not say %s: %v", m, err)
						}
					}
				})
			}
		}
	}
}

// windowNameTables is every table that renders into Claude Code's settings
// env, with a fragment declaring name there. The repo fragment declares its
// repository first.
func windowNameTables(name string) []struct{ path, fragment string } {
	var out []struct{ path, fragment string }
	for _, prefix := range []string{"claude.env", "instance.claude.env", "repos.app.claude.env"} {
		out = append(out, struct{ path, fragment string }{prefix + ".promote", "[" + prefix + "]\npromote = [\"" + name + "\"]\n"})
		for _, table := range []string{"vars", "secrets"} {
			out = append(out, struct{ path, fragment string }{prefix + "." + table, "[" + prefix + "." + table + "]\n" + name + " = \"400000\"\n"})
			for _, sub := range []string{"required", "recommended", "optional"} {
				out = append(out, struct{ path, fragment string }{prefix + "." + table + "." + sub, "[" + prefix + "." + table + "." + sub + "]\n" + name + " = \"the compaction window\"\n"})
			}
		}
	}
	out = append(out, struct{ path, fragment string }{"session.env.promote", "[session.env]\npromote = [\"" + name + "\"]\n"})
	out = append(out, struct{ path, fragment string }{"session.env.vars", "[session.env.vars]\n" + name + " = \"400000\"\n"})
	for _, sub := range []string{"required", "recommended", "optional"} {
		out = append(out, struct{ path, fragment string }{"session.env.vars." + sub, "[session.env.vars." + sub + "]\n" + name + " = \"the compaction window\"\n"})
	}
	return out
}

// TestAutoCompactWindowNameRefusedInClaudeEnvTables: once a profile sets a
// window, every table that renders into Claude Code's settings env refuses
// the window's name in any letter case, naming the table. Without a windowed
// profile the same tables load.
func TestAutoCompactWindowNameRefusedInClaudeEnvTables(t *testing.T) {
	for _, name := range []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "Claude_Code_Auto_Compact_Window"} {
		for _, tc := range windowNameTables(name) {
			t.Run(name+"/"+tc.path, func(t *testing.T) {
				_, err := Parse([]byte(sessionConfigWith(windowProfile("400000") + tc.fragment)))
				if err == nil {
					t.Fatal("loaded without complaint")
				}
				for _, m := range []string{tc.path + ": ", fmt.Sprintf("%q", name), "auto_compact_window"} {
					if !strings.Contains(err.Error(), m) {
						t.Errorf("the error does not say %s: %v", m, err)
					}
				}
			})
		}
	}
	for _, tc := range windowNameTables("CLAUDE_CODE_AUTO_COMPACT_WINDOW") {
		t.Run("no window/"+tc.path, func(t *testing.T) {
			if _, err := Parse([]byte(sessionConfigWith(coordinatorProfile + tc.fragment))); err != nil {
				t.Errorf("a workspace whose profiles set no window refused it: %v", err)
			}
		})
	}
}

// TestDefaultDispatchProfile: the default must name a declared profile.
func TestDefaultDispatchProfile(t *testing.T) {
	result, err := Parse([]byte(sessionConfigWith("[session]\ndefault_dispatch_profile = \"worker\"\n" + windowProfile("400000"))))
	if err != nil {
		t.Fatalf("refused: %v", err)
	}
	if got := result.Config.Session.DefaultDispatchProfile; got != "worker" {
		t.Errorf("DefaultDispatchProfile = %q, want worker", got)
	}
	if len(result.Warnings) != 0 {
		t.Errorf("warnings: %v", result.Warnings)
	}
	for name, fragment := range map[string]string{
		"undeclared":  "[session]\ndefault_dispatch_profile = \"lead\"\n" + windowProfile("400000"),
		"no profiles": "[session]\ndefault_dispatch_profile = \"lead\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(sessionConfigWith(fragment)))
			if err == nil {
				t.Fatal("loaded without complaint")
			}
			for _, m := range []string{"session.default_dispatch_profile", `"lead"`} {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("the error does not say %s: %v", m, err)
				}
			}
		})
	}
}

// TestValidateAutoCompactWindow is the recorded-value check resume and retask
// make: a canonical decimal integer inside the bounds, and nothing else. The
// error never repeats the value.
func TestValidateAutoCompactWindow(t *testing.T) {
	for _, ok := range []string{"100000", "400000", "1000000"} {
		if err := ValidateAutoCompactWindow(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "99999", "1000001", "0", "-400000", "+400000", "0400000", "400000.0", "4e5", " 400000", "400000\n", "abc", "99999999999999999999"} {
		err := ValidateAutoCompactWindow(bad)
		if err == nil {
			t.Errorf("%q accepted", bad)
			continue
		}
		if bad != "" && strings.Contains(err.Error(), bad) {
			t.Errorf("the error for %q repeats the value: %v", bad, err)
		}
	}
}

// TestAutoCompactWindowTOMLRoundTrip: an encoded config writes the window as
// a TOML integer and decodes back to the same window; a profile without one
// writes no key.
func TestAutoCompactWindowTOMLRoundTrip(t *testing.T) {
	cfg := WorkspaceConfig{Workspace: WorkspaceMeta{Name: "ws"}, Session: SessionConfig{
		DefaultDispatchProfile: "worker",
		DispatchProfiles: map[string]SessionDispatchProfile{
			"worker": {AutoCompactWindow: NewAutoCompactWindow(400000)},
			"lean":   {DropBundledSkills: true},
		},
	}}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		t.Fatalf("encode: %v", err)
	}
	out := buf.String()
	if strings.Count(out, "auto_compact_window") != 1 || !strings.Contains(out, "auto_compact_window = 400000") {
		t.Fatalf("encoding:\n%s", out)
	}
	result, err := Parse([]byte(out))
	if err != nil {
		t.Fatalf("parsing the encoded config: %v\n%s", err, out)
	}
	if got, ok := result.Config.Session.DispatchProfiles["worker"].AutoCompactWindow.Value(); !ok || got != 400000 {
		t.Errorf("decoded window = %d, %v", got, ok)
	}
	if result.Config.Session.DefaultDispatchProfile != "worker" {
		t.Errorf("decoded default = %q", result.Config.Session.DefaultDispatchProfile)
	}
}
