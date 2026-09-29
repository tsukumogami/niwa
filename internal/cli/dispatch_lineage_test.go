package cli

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// TestLineageAttributeNamesArePinned pins the six names and their order. Other
// tools read these attributes by name, so a rename here breaks every reader
// without a compile error anywhere; this is the one place that notices.
func TestLineageAttributeNamesArePinned(t *testing.T) {
	want := []string{
		"niwa.dispatch.id",
		"niwa.dispatch.slug",
		"niwa.parent.session.id",
		"niwa.requested.skill",
		"niwa.brief.id",
		"niwa.workspace",
	}
	if !slices.Equal(lineageAttributeNames, want) {
		t.Fatalf("lineage attribute names = %v, want exactly %v", lineageAttributeNames, want)
	}
}

func TestLineageEntriesOrderAndOmission(t *testing.T) {
	var warn bytes.Buffer
	got := lineageEntries(lineageInputs{
		Workspace:  "acme",
		DispatchID: "00000000-0000-4000-8000-000000000001",
		BriefID:    "sha256:abc",
	}, &warn)
	want := []string{
		"niwa.dispatch.id=00000000-0000-4000-8000-000000000001",
		"niwa.brief.id=sha256:abc",
		"niwa.workspace=acme",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
	if warn.Len() != 0 {
		t.Fatalf("empty values must be left out silently, got warning %q", warn.String())
	}
}

func TestLineageEntriesDropValuesOutsideTheAlphabet(t *testing.T) {
	long128 := strings.Repeat("a", 128)
	long129 := strings.Repeat("a", 129)
	cases := []struct {
		name, value string
		kept        bool
	}{
		{"128 characters", long128, true},
		{"129 characters", long129, false},
		{"comma", "a,b", false},
		{"equals", "a=b", false},
		{"percent", "a%20b", false},
		{"space", "a b", false},
		{"allowed punctuation", "a.b_c:d/e@f-g", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var warn bytes.Buffer
			got := lineageEntries(lineageInputs{Workspace: c.value}, &warn)
			if c.kept {
				if len(got) != 1 || warn.Len() != 0 {
					t.Fatalf("value should be kept: entries %v, warning %q", got, warn.String())
				}
				return
			}
			if len(got) != 0 {
				t.Fatalf("value should be dropped, got entries %v", got)
			}
			if !strings.Contains(warn.String(), "niwa.workspace") {
				t.Fatalf("warning should name the attribute, got %q", warn.String())
			}
			if strings.Contains(warn.String(), c.value) {
				t.Fatalf("warning must not quote the value, got %q", warn.String())
			}
		})
	}
}

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestNewDispatchIDIsAVersion4UUID(t *testing.T) {
	a, err := newDispatchID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newDispatchID(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{a, b} {
		if !uuidV4.MatchString(id) {
			t.Fatalf("dispatch id %q is not a lowercase version-4 UUID", id)
		}
	}
	if a == b {
		t.Fatal("two dispatch ids are equal")
	}
}

func TestNewDispatchIDFailsOnAShortRead(t *testing.T) {
	if _, err := newDispatchID(bytes.NewReader([]byte{1, 2, 3})); err == nil {
		t.Fatal("a random source that runs dry must fail the dispatch id")
	}
}

func TestLineageSlugMapsUnderscores(t *testing.T) {
	slug := sanitizeInstanceSlug("auth layer")
	if got := lineageSlug(slug); got != "auth-layer" {
		t.Fatalf("lineageSlug(%q) = %q, want auth-layer", slug, got)
	}
	if got := lineageSlug(sanitizeInstanceSlug("!!!")); got != "" {
		t.Fatalf("a slug with nothing usable must stay empty, got %q", got)
	}
}

func TestLineageParentSessionID(t *testing.T) {
	spec := agentplan.LaunchSpec{Lineage: agentplan.Lineage{ParentSessionEnv: "PARENT_ID"}}
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "PARENT_ID" {
				return v
			}
			return ""
		}
	}
	if got := lineageParentSessionID(spec, env("6f1a2b3c-aaaa-bbbb-cccc-1234567890ab")); got != "6f1a2b3c-aaaa-bbbb-cccc-1234567890ab" {
		t.Fatalf("valid id not returned, got %q", got)
	}
	for _, bad := range []string{"", "short", "has space in it", strings.Repeat("a", 129)} {
		if got := lineageParentSessionID(spec, env(bad)); got != "" {
			t.Fatalf("id %q should be ignored, got %q", bad, got)
		}
	}
	if got := lineageParentSessionID(agentplan.LaunchSpec{}, env("6f1a2b3c-aaaa-bbbb-cccc-1234567890ab")); got != "" {
		t.Fatalf("an agent that declares no variable has no parent, got %q", got)
	}
}

func TestValidateRequestedSkill(t *testing.T) {
	for _, ok := range []string{"", "shirabe:scope", "a1:b-2"} {
		if err := validateRequestedSkill(ok); err != nil {
			t.Fatalf("%q should be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"scope", "Shirabe:scope", ":scope", "shirabe:", "a:b:c", "a b:c"} {
		if err := validateRequestedSkill(bad); err == nil {
			t.Fatalf("%q should be refused", bad)
		}
	}
}

func TestBriefDigest(t *testing.T) {
	// sha256("hello\n")
	const want = "sha256:5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
	if got := briefDigest([]byte("hello\n")); got != want {
		t.Fatalf("briefDigest = %s, want %s", got, want)
	}
}

func TestReadBriefFile(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "briefs", "b.md")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inside, []byte("the brief"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := readBriefFile(inside, root); err != nil || string(got) != "the brief" {
		t.Fatalf("a brief inside the workspace must be read: %q, %v", got, err)
	}

	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBriefFile(outside, root); err == nil {
		t.Fatal("a file outside the workspace must be refused")
	}
	link := filepath.Join(root, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readBriefFile(link, root); err == nil {
		t.Fatal("a symlink pointing outside the workspace must be refused")
	}
	if _, err := readBriefFile(filepath.Join(root, "briefs"), root); err == nil {
		t.Fatal("a directory must be refused")
	}
	big := filepath.Join(root, "big.md")
	if err := os.WriteFile(big, make([]byte, maxLineageFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readBriefFile(big, root); err == nil {
		t.Fatal("a file over the size cap must be refused")
	}
	if _, err := readBriefFile(filepath.Join(root, "missing.md"), root); err == nil {
		t.Fatal("a missing file must be refused")
	}
	// Last, because a platform without FIFOs skips from here.
	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if _, err := readBriefFile(fifo, root); err == nil {
		t.Fatal("a FIFO must be refused")
	}
}

// settingsSpec is Claude's declaration shape with a fixed variable name, so
// the tests can point it at a temporary directory.
var settingsSpec = agentplan.LaunchSpec{Lineage: agentplan.Lineage{
	SettingsHomeEnv:  "TEST_CONFIG_DIR",
	SettingsHomePath: []string{".agent"},
	SettingsFile:     "settings.json",
}}

func writeSettings(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUserSettingsAttributesLocatesTheFile(t *testing.T) {
	home := t.TempDir()
	override := t.TempDir()
	writeSettings(t, filepath.Join(home, ".agent"), `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"from=home"}}`)
	writeSettings(t, override, `{"env":{"OTEL_RESOURCE_ATTRIBUTES":"from=override"}}`)

	noEnv := func(string) string { return "" }
	if v, ok, err := userSettingsAttributes(settingsSpec, home, noEnv); err != nil || !ok || v != "from=home" {
		t.Fatalf("home default: %q %v %v", v, ok, err)
	}
	withEnv := func(k string) string {
		if k == "TEST_CONFIG_DIR" {
			return override
		}
		return ""
	}
	if v, ok, err := userSettingsAttributes(settingsSpec, home, withEnv); err != nil || !ok || v != "from=override" {
		t.Fatalf("the override directory must be read instead of home: %q %v %v", v, ok, err)
	}
}

func TestUserSettingsAttributesAbsentCases(t *testing.T) {
	noEnv := func(string) string { return "" }
	home := t.TempDir()
	if _, ok, err := userSettingsAttributes(settingsSpec, home, noEnv); ok || err != nil {
		t.Fatalf("a missing file contributes nothing: %v %v", ok, err)
	}
	writeSettings(t, filepath.Join(home, ".agent"), `{"env":{"OTHER":"x"}}`)
	if _, ok, err := userSettingsAttributes(settingsSpec, home, noEnv); ok || err != nil {
		t.Fatalf("a file without the key contributes nothing: %v %v", ok, err)
	}
	if _, ok, err := userSettingsAttributes(agentplan.LaunchSpec{}, home, noEnv); ok || err != nil {
		t.Fatalf("an agent with no settings file contributes nothing: %v %v", ok, err)
	}
}

func TestUserSettingsAttributesMalformed(t *testing.T) {
	noEnv := func(string) string { return "" }
	for name, body := range map[string]string{
		"invalid JSON":     `{"env":`,
		"non-string value": `{"env":{"OTEL_RESOURCE_ATTRIBUTES":42}}`,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeSettings(t, filepath.Join(home, ".agent"), body)
			_, _, err := userSettingsAttributes(settingsSpec, home, noEnv)
			var target errInheritedAttributes
			if !errors.As(err, &target) {
				t.Fatalf("want errInheritedAttributes, got %v", err)
			}
		})
	}
	t.Run("not a regular file", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(filepath.Join(home, ".agent", "settings.json"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, _, err := userSettingsAttributes(settingsSpec, home, noEnv)
		var target errInheritedAttributes
		if !errors.As(err, &target) {
			t.Fatalf("want errInheritedAttributes, got %v", err)
		}
	})
}

func TestComposeResourceAttributes(t *testing.T) {
	own := []string{"niwa.dispatch.id=d1", "niwa.workspace=acme"}
	cases := []struct {
		name, settings, env, want string
	}{
		{
			name:     "settings entries first, byte for byte, then niwa's",
			settings: "team.id=platform,host.name=h%2F1",
			want:     "team.id=platform,host.name=h%2F1,niwa.dispatch.id=d1,niwa.workspace=acme",
		},
		{
			name: "environment only",
			env:  "team.id=shell",
			want: "team.id=shell,niwa.dispatch.id=d1,niwa.workspace=acme",
		},
		{
			name:     "settings win a conflicting key",
			settings: "team.id=platform",
			env:      "team.id=other,extra=1",
			want:     "team.id=platform,extra=1,niwa.dispatch.id=d1,niwa.workspace=acme",
		},
		{
			name:     "inherited niwa entries are replaced or removed",
			settings: "niwa.workspace=old,team.id=platform",
			env:      "niwa.brief.id=stale",
			want:     "team.id=platform,niwa.dispatch.id=d1,niwa.workspace=acme",
		},
		{
			name:     "blank entries are skipped",
			settings: "team.id=platform,,",
			want:     "team.id=platform,niwa.dispatch.id=d1,niwa.workspace=acme",
		},
		{
			name: "nothing inherited",
			want: "niwa.dispatch.id=d1,niwa.workspace=acme",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := composeResourceAttributes(c.settings, c.env, own)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Fatalf("composed %q, want %q", got, c.want)
			}
		})
	}
	if got, err := composeResourceAttributes("", "", nil); err != nil || got != "" {
		t.Fatalf("nothing to send must compose to empty: %q %v", got, err)
	}
}

func TestComposeRefusesAnInheritedValueTheParserWouldReject(t *testing.T) {
	secret := "tok3n-value"
	for name, value := range map[string]string{
		"no equals":        "team.id",
		"two equals":       "a=b=" + secret,
		"empty key":        "=" + secret,
		"space":            "a=" + secret + " x",
		"semicolon":        "a=" + secret + ";",
		"backslash":        `a=` + secret + `\`,
		"quote":            `a="` + secret,
		"value over limit": "a=" + strings.Repeat("v", 256),
		"non-ASCII":        "a=é",
	} {
		t.Run(name, func(t *testing.T) {
			for _, where := range []string{"settings", "env"} {
				settings, env := value, ""
				if where == "env" {
					settings, env = "", value
				}
				_, err := composeResourceAttributes(settings, env, []string{"niwa.workspace=acme"})
				var target errInheritedAttributes
				if !errors.As(err, &target) {
					t.Fatalf("%s: want errInheritedAttributes, got %v", where, err)
				}
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("%s: the error must not quote the inherited value: %q", where, err)
				}
			}
		})
	}
}
