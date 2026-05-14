package plugin

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// withFakeHome redirects $HOME to a t.TempDir so install runs under
// test isolation and the t.Cleanup restores the prior value.
func withFakeHome(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	orig := os.Getenv("HOME")
	t.Setenv("HOME", tmp)
	t.Cleanup(func() { os.Setenv("HOME", orig) })
	return tmp
}

func readManifestAt(t *testing.T, dir string) manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read installed manifest at %s: %v", dir, err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse installed manifest: %v", err)
	}
	return m
}

// AC-I2: fresh install from a clean home produces (Installed, nil),
// writes manifest to the install path, records the notice, and
// leaves no .next/.prev staging directories behind.
func TestInstall_FreshInstall(t *testing.T) {
	home := withFakeHome(t)
	state := &workspace.InstanceState{}
	var buf bytes.Buffer
	reporter := workspace.NewReporter(&buf)

	action, err := Install(state, reporter, InstallOpts{})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if action != Installed {
		t.Errorf("action = %v, want Installed", action)
	}

	installPath := filepath.Join(home, ".claude", "plugins", "marketplaces", "niwa")
	got := readManifestAt(t, installPath)
	if got.Name != "niwa" {
		t.Errorf("installed manifest name = %q, want niwa", got.Name)
	}

	embedded, _ := Embedded()
	if got.Version != embedded.Version {
		t.Errorf("installed version = %q, want %q", got.Version, embedded.Version)
	}

	if _, statErr := os.Stat(installPath + ".next"); statErr == nil {
		t.Error(".next/ staging dir survived install")
	}
	if _, statErr := os.Stat(installPath + ".prev"); statErr == nil {
		t.Error(".prev/ staging dir survived install")
	}

	found := 0
	for _, n := range state.DisclosedNotices {
		if n == workspace.NoticeIDPluginInstalled {
			found++
		}
	}
	if found != 1 {
		t.Errorf("NoticeIDPluginInstalled recorded %d times, want 1", found)
	}
}

// AC-I3: a second Install on the same machine returns (UpToDate, nil)
// and does NOT append a duplicate NoticeIDPluginInstalled.
func TestInstall_IdempotentReinvocation(t *testing.T) {
	withFakeHome(t)

	state := &workspace.InstanceState{}
	var buf bytes.Buffer
	reporter := workspace.NewReporter(&buf)

	if _, err := Install(state, reporter, InstallOpts{}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if len(state.DisclosedNotices) != 1 {
		t.Fatalf("expected 1 disclosed notice after first install, got %d", len(state.DisclosedNotices))
	}

	action, err := Install(state, reporter, InstallOpts{})
	if err != nil {
		t.Fatalf("second install: %v", err)
	}
	if action != UpToDate {
		t.Errorf("second action = %v, want UpToDate", action)
	}
	if len(state.DisclosedNotices) != 1 {
		t.Errorf("DisclosedNotices grew on idempotent install: %v", state.DisclosedNotices)
	}
}

// AC-I4: manually deleting the install path causes the next Install
// call to recreate it (returns Installed, not UpToDate).
func TestInstall_SelfHealAfterDelete(t *testing.T) {
	home := withFakeHome(t)
	state := &workspace.InstanceState{}
	var buf bytes.Buffer
	reporter := workspace.NewReporter(&buf)

	if _, err := Install(state, reporter, InstallOpts{}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	installPath := filepath.Join(home, ".claude", "plugins", "marketplaces", "niwa")
	if err := os.RemoveAll(installPath); err != nil {
		t.Fatalf("delete install path: %v", err)
	}

	// Reset state so the notice can fire again (the previous notice
	// was already recorded, but the InstanceState is per-workspace —
	// a fresh workspace state should see the notice).
	state2 := &workspace.InstanceState{}
	var buf2 bytes.Buffer
	reporter2 := workspace.NewReporter(&buf2)
	action, err := Install(state2, reporter2, InstallOpts{})
	if err != nil {
		t.Fatalf("self-heal install: %v", err)
	}
	if action != Installed {
		t.Errorf("action = %v, want Installed", action)
	}
	if _, statErr := os.Stat(filepath.Join(installPath, "manifest.json")); statErr != nil {
		t.Errorf("install path not recreated: %v", statErr)
	}
}

// AC-I5a: opt-out via opts.SkipInstall=true returns (Skipped, nil)
// and records NoticeIDPluginSkipped with the manual-install command.
func TestInstall_SkipInstallOptOut(t *testing.T) {
	home := withFakeHome(t)
	state := &workspace.InstanceState{}
	var buf bytes.Buffer
	reporter := workspace.NewReporter(&buf)

	action, err := Install(state, reporter, InstallOpts{SkipInstall: true})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if action != Skipped {
		t.Errorf("action = %v, want Skipped", action)
	}

	installPath := filepath.Join(home, ".claude", "plugins", "marketplaces", "niwa")
	if _, statErr := os.Stat(installPath); statErr == nil {
		t.Error("install path exists after Skipped action")
	}

	if !strings.Contains(buf.String(), "niwa --install-plugins") {
		t.Errorf("skip notice missing manual-install command:\n%s", buf.String())
	}

	found := false
	for _, n := range state.DisclosedNotices {
		if n == workspace.NoticeIDPluginSkipped {
			found = true
		}
	}
	if !found {
		t.Errorf("NoticeIDPluginSkipped not recorded: %v", state.DisclosedNotices)
	}
}

// AC-I6: read-only $HOME → (Failed, nil) + skip notice + no install
// path. The install path must not exist, and .next/ must be cleaned up.
func TestInstall_ReadOnlyHomeFailsGracefully(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("read-only test does not work under root (chmod is bypassed)")
	}
	home := withFakeHome(t)
	// Make the home directory read-only so MkdirAll under it fails.
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(home, 0o755) })

	state := &workspace.InstanceState{}
	var buf bytes.Buffer
	reporter := workspace.NewReporter(&buf)

	action, err := Install(state, reporter, InstallOpts{})
	if err != nil {
		t.Fatalf("Install should not return error on user-env failure: %v", err)
	}
	if action != Failed {
		t.Errorf("action = %v, want Failed", action)
	}

	installPath := filepath.Join(home, ".claude", "plugins", "marketplaces", "niwa")
	if _, statErr := os.Stat(installPath); statErr == nil {
		t.Error("install path exists after Failed action")
	}
	if _, statErr := os.Stat(installPath + ".next"); statErr == nil {
		t.Error(".next/ survived mid-failure cleanup")
	}

	found := false
	for _, n := range state.DisclosedNotices {
		if n == workspace.NoticeIDPluginSkipped {
			found = true
		}
	}
	if !found {
		t.Errorf("NoticeIDPluginSkipped not recorded: %v", state.DisclosedNotices)
	}
}

// TestInstallPath_ComputedFromHomeOnly verifies that the install
// path is computed purely from $HOME and no user-supplied component.
func TestInstallPath_ComputedFromHomeOnly(t *testing.T) {
	home := withFakeHome(t)
	p, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	want := filepath.Join(home, ".claude", "plugins", "marketplaces", "niwa")
	if p.Path != want {
		t.Errorf("Path = %q, want %q", p.Path, want)
	}
}

// TestPlugin_NoArchiveDeps verifies the plugin package itself does
// not directly import any archive parser. The installer must
// materialize the embedded plugin via embed.FS + fs.WalkDir +
// os.WriteFile — never via archive/tar, archive/zip, or
// compress/gzip. (Transitive deps via workspace.InstanceState are
// expected and unrelated; this test pins direct imports only.)
func TestPlugin_NoArchiveDeps(t *testing.T) {
	cmd := exec.Command("go", "list", "-f", "{{range .Imports}}{{.}}\n{{end}}", "github.com/tsukumogami/niwa/internal/plugin")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, banned := range []string{"archive/tar", "archive/zip", "compress/gzip", "compress/zlib"} {
		if strings.Contains(string(out), banned+"\n") {
			t.Errorf("internal/plugin directly imports forbidden package %q", banned)
		}
	}
}
