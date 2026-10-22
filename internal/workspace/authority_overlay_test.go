package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

const recordedOverlayURL = "acme/recorded-overlay"
const conventionSourceURL = "acme/ws"

// placeOverlayClone writes a local overlay clone for url holding body.
func placeOverlayClone(t *testing.T, url, body string) {
	t.Helper()
	dir, err := config.OverlayDir(url)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "workspace-overlay.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func authorityStateDir(t *testing.T, state *InstanceState) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	if state != nil {
		if err := SaveState(root, state); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestResolveAuthorityOverlay_NoOverlay(t *testing.T) {
	root := authorityStateDir(t, &InstanceState{NoOverlay: true, OverlayURL: recordedOverlayURL})
	placeOverlayClone(t, recordedOverlayURL, "[authority]\nlevel = \"strict\"\n")
	o, err := ResolveAuthorityOverlay(root, conventionSourceURL)
	if err != nil || o != nil {
		t.Errorf("got %v, %v; want no overlay", o, err)
	}
}

func TestResolveAuthorityOverlay_RecordedURL(t *testing.T) {
	t.Run("present clone", func(t *testing.T) {
		root := authorityStateDir(t, &InstanceState{OverlayURL: recordedOverlayURL})
		placeOverlayClone(t, recordedOverlayURL, "[authority]\nlevel = \"strict\"\n")
		o, err := ResolveAuthorityOverlay(root, conventionSourceURL)
		if err != nil || o == nil || o.Authority.Level != "strict" {
			t.Errorf("got %+v, %v", o, err)
		}
	})
	t.Run("missing clone", func(t *testing.T) {
		root := authorityStateDir(t, &InstanceState{OverlayURL: recordedOverlayURL})
		_, err := ResolveAuthorityOverlay(root, conventionSourceURL)
		if err == nil || !strings.Contains(err.Error(), "niwa apply") {
			t.Errorf("err = %v, want one naming niwa apply", err)
		}
	})
	t.Run("unparseable clone", func(t *testing.T) {
		root := authorityStateDir(t, &InstanceState{OverlayURL: recordedOverlayURL})
		placeOverlayClone(t, recordedOverlayURL, "[authority\n")
		_, err := ResolveAuthorityOverlay(root, conventionSourceURL)
		if err == nil || !strings.Contains(err.Error(), "niwa apply") {
			t.Errorf("err = %v, want one naming niwa apply", err)
		}
	})
}

func TestResolveAuthorityOverlay_Convention(t *testing.T) {
	conventionURL, _ := config.DeriveOverlayURL(conventionSourceURL)
	t.Run("clone present", func(t *testing.T) {
		root := authorityStateDir(t, &InstanceState{})
		placeOverlayClone(t, conventionURL, "[authority]\nceiling = []\n")
		o, err := ResolveAuthorityOverlay(root, conventionSourceURL)
		if err != nil || o == nil || o.Authority.Ceiling == nil {
			t.Errorf("got %+v, %v; want the convention overlay", o, err)
		}
	})
	t.Run("no state file, clone present", func(t *testing.T) {
		root := authorityStateDir(t, nil)
		placeOverlayClone(t, conventionURL, "[authority]\nlevel = \"supervised\"\n")
		o, err := ResolveAuthorityOverlay(root, conventionSourceURL)
		if err != nil || o == nil {
			t.Errorf("got %v, %v", o, err)
		}
	})
	t.Run("no clone", func(t *testing.T) {
		root := authorityStateDir(t, &InstanceState{})
		o, err := ResolveAuthorityOverlay(root, conventionSourceURL)
		if err != nil || o != nil {
			t.Errorf("got %v, %v; want no overlay", o, err)
		}
	})
	t.Run("no config source", func(t *testing.T) {
		root := authorityStateDir(t, &InstanceState{})
		placeOverlayClone(t, conventionURL, "[authority]\nlevel = \"supervised\"\n")
		o, err := ResolveAuthorityOverlay(root, "")
		if err != nil || o != nil {
			t.Errorf("got %v, %v; want no overlay", o, err)
		}
	})
}
