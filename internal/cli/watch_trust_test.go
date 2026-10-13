package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/claudetrust"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// These tests drive resolveAskPosture through the real claudetrust writer. HOME
// and the reviewHomeDir seam point at a t.TempDir and CLAUDE_CONFIG_DIR is
// cleared or set to another one, so no test can reach the developer's own
// Claude config.

// stubReviewHome points resolveAskPosture (and anything else reading HOME) at a
// fresh temporary home with CLAUDE_CONFIG_DIR cleared, and returns the home and
// the config file it names.
func stubReviewHome(t *testing.T) (home, claudeJSON string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	prev := reviewHomeDir
	reviewHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { reviewHomeDir = prev })
	return home, filepath.Join(home, ".claude.json")
}

// makeReviewInstance creates an instance directory with a minimal state file
// under a temp dir that is not symlink-resolved (on macOS t.TempDir sits under
// the /var symlink), so a test can tell the resolved key from the path given.
func makeReviewInstance(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "test-ws+watch_acme_widget_7-abababab")
	if err := writeMinimalInstanceState(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// resolvedKey is the projects key Claude Code computes for dir.
func resolvedKey(t *testing.T, dir string) string {
	t.Helper()
	key, err := claudetrust.Key(dir)
	if err != nil {
		t.Fatalf("claudetrust.Key(%s): %v", dir, err)
	}
	return key
}

// claudeEntry returns projects[key] from the Claude config at path, and
// whether it exists. A missing file has no entries.
func claudeEntry(t *testing.T, path, key string) (map[string]any, bool) {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc struct {
		Projects map[string]map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	e, ok := doc.Projects[key]
	return e, ok
}

// assertTrusted fails unless the config at path trusts key.
func assertTrusted(t *testing.T, path, key string) {
	t.Helper()
	if e, ok := claudeEntry(t, path, key); !ok || e["hasTrustDialogAccepted"] != true {
		t.Errorf("%s: projects[%s] = %v (present %v), want hasTrustDialogAccepted true", path, key, e, ok)
	}
}

// assertNoEntry fails if the config at path has an entry for key.
func assertNoEntry(t *testing.T, path, key string) {
	t.Helper()
	if e, ok := claudeEntry(t, path, key); ok {
		t.Errorf("%s: projects[%s] = %v, want no entry", path, key, e)
	}
}

// recordedGrant returns the Claude trust grant in dir's instance state.
func recordedGrant(t *testing.T, dir string) *claudetrust.Grant {
	t.Helper()
	state, err := workspace.LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState(%s): %v", dir, err)
	}
	return state.ClaudeTrust
}

// recordGrant stores g as dir's recorded grant.
func recordGrant(t *testing.T, dir string, g *claudetrust.Grant) {
	t.Helper()
	state, err := workspace.LoadState(dir)
	if err != nil {
		t.Fatalf("LoadState(%s): %v", dir, err)
	}
	state.ClaudeTrust = g
	if err := workspace.SaveState(dir, state); err != nil {
		t.Fatalf("SaveState(%s): %v", dir, err)
	}
}

// fileState is a file's bytes and identity, to prove a call left it alone.
type fileState struct {
	data []byte
	info os.FileInfo
}

func statFile(t *testing.T, path string) fileState {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fileState{data: data, info: info}
}

// assertUnchanged fails unless path has the same bytes, inode and mtime.
func assertUnchanged(t *testing.T, path string, before fileState) {
	t.Helper()
	after := statFile(t, path)
	if !bytes.Equal(before.data, after.data) {
		t.Errorf("%s changed:\nbefore: %s\nafter:  %s", path, before.data, after.data)
	}
	if !os.SameFile(before.info, after.info) || !before.info.ModTime().Equal(after.info.ModTime()) {
		t.Errorf("%s was rewritten", path)
	}
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestResolveAskPosture_RecordsReviewGrant checks the grant lands in the file
// CLAUDE_CONFIG_DIR or HOME names, keyed by the symlink-resolved root, and is
// recorded with source review; a second call (a continuation) rewrites
// nothing.
func TestResolveAskPosture_RecordsReviewGrant(t *testing.T) {
	for _, viaConfigDir := range []bool{false, true} {
		name := "HOME"
		if viaConfigDir {
			name = "CLAUDE_CONFIG_DIR"
		}
		t.Run(name, func(t *testing.T) {
			_, homeJSON := stubReviewHome(t)
			file := homeJSON
			if viaConfigDir {
				dir := filepath.Join(t.TempDir(), "claude-config")
				t.Setenv("CLAUDE_CONFIG_DIR", dir)
				file = filepath.Join(dir, ".claude.json")
			}
			inst := makeReviewInstance(t)
			key := resolvedKey(t, inst)

			var stderr bytes.Buffer
			if !resolveAskPosture(&stderr, inst, true) {
				t.Fatalf("resolveAskPosture = false, want true; stderr: %s", stderr.String())
			}
			if stderr.Len() != 0 {
				t.Errorf("unexpected warnings: %s", stderr.String())
			}
			assertTrusted(t, file, key)
			want := &claudetrust.Grant{ConfigFile: file, Key: key, Mode: claudetrust.ModeCreated, Source: claudetrust.SourceReview}
			if g := recordedGrant(t, inst); !reflect.DeepEqual(g, want) {
				t.Errorf("record = %+v, want %+v", g, want)
			}
			if viaConfigDir {
				if _, err := os.Lstat(homeJSON); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s was created (err %v), want only the CLAUDE_CONFIG_DIR file written", homeJSON, err)
				}
			}

			before := statFile(t, file)
			if !resolveAskPosture(io.Discard, inst, true) {
				t.Fatal("second resolveAskPosture = false, want true")
			}
			assertUnchanged(t, file, before)
			if g := recordedGrant(t, inst); !reflect.DeepEqual(g, want) {
				t.Errorf("record after the second call = %+v, want %+v", g, want)
			}
		})
	}
}

// TestResolveAskPosture_NoSandbox: a non-sandboxed review never uses the ask
// posture and never writes trust.
func TestResolveAskPosture_NoSandbox(t *testing.T) {
	_, file := stubReviewHome(t)
	inst := makeReviewInstance(t)
	if resolveAskPosture(io.Discard, inst, false) {
		t.Error("a non-sandboxed review must not use the ask posture")
	}
	if _, err := os.Lstat(file); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s was created (err %v)", file, err)
	}
	if g := recordedGrant(t, inst); g != nil {
		t.Errorf("record = %+v, want none", g)
	}
}

// TestResolveAskPosture_UnderClaudeConfigHomeFallsBack: an instance under the
// Claude config home (HOME/.claude, or CLAUDE_CONFIG_DIR when set) falls back
// to the hard-deny posture and writes nothing, and so does a relative
// CLAUDE_CONFIG_DIR, which claudetrust would refuse anyway.
func TestResolveAskPosture_UnderClaudeConfigHomeFallsBack(t *testing.T) {
	cases := []struct {
		name string
		// setup returns the instance path and the config file to check.
		setup func(t *testing.T, home string) (inst, file string)
	}{
		{"under HOME/.claude", func(t *testing.T, home string) (string, string) {
			return filepath.Join(home, ".claude", "instances", "watch-x"), filepath.Join(home, ".claude.json")
		}},
		{"under CLAUDE_CONFIG_DIR", func(t *testing.T, home string) (string, string) {
			dir := filepath.Join(t.TempDir(), "claude-config")
			t.Setenv("CLAUDE_CONFIG_DIR", dir)
			return filepath.Join(dir, "instances", "watch-x"), filepath.Join(dir, ".claude.json")
		}},
		{"relative CLAUDE_CONFIG_DIR", func(t *testing.T, home string) (string, string) {
			t.Setenv("CLAUDE_CONFIG_DIR", "relative/config")
			return filepath.Join(t.TempDir(), "watch-x"), filepath.Join(home, ".claude.json")
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, _ := stubReviewHome(t)
			inst, file := tc.setup(t, home)
			if err := writeMinimalInstanceState(inst); err != nil {
				t.Fatal(err)
			}
			if resolveAskPosture(io.Discard, inst, true) {
				t.Error("resolveAskPosture = true, want the hard-deny fallback")
			}
			if _, err := os.Lstat(file); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s was created (err %v)", file, err)
			}
			if g := recordedGrant(t, inst); g != nil {
				t.Errorf("record = %+v, want none", g)
			}
		})
	}
}

// TestResolveAskPosture_SkippedWriteFallsBack: a config file claudetrust can't
// parse is left byte-identical, the skip is reported naming the file, nothing
// is recorded, and the posture is hard-deny.
func TestResolveAskPosture_SkippedWriteFallsBack(t *testing.T) {
	_, file := stubReviewHome(t)
	writeTestFile(t, file, "{ this is not json")
	before := statFile(t, file)
	inst := makeReviewInstance(t)

	var stderr bytes.Buffer
	if resolveAskPosture(&stderr, inst, true) {
		t.Error("resolveAskPosture = true after a skipped trust write, want false")
	}
	assertUnchanged(t, file, before)
	if g := recordedGrant(t, inst); g != nil {
		t.Errorf("record = %+v, want none", g)
	}
	if !strings.Contains(stderr.String(), "warning") || !strings.Contains(stderr.String(), file) {
		t.Errorf("stderr = %q, want a warning naming %s", stderr.String(), file)
	}
}

// TestResolveAskPosture_NoInstanceStateFallsBack: with no instance state to
// record a grant in, nothing is written and the posture is hard-deny.
func TestResolveAskPosture_NoInstanceStateFallsBack(t *testing.T) {
	_, file := stubReviewHome(t)
	inst := t.TempDir()
	if resolveAskPosture(io.Discard, inst, true) {
		t.Error("resolveAskPosture = true with no instance state, want false")
	}
	if _, err := os.Lstat(file); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s was created (err %v)", file, err)
	}
}

// TestResolveAskPosture_SaveFailureUndoesTheWrite: when the instance state
// can't be saved after seeding, this call's write is taken back -- a created
// entry removed, a flag set on an existing entry retracted with the entry's
// other keys kept -- and the posture is hard-deny.
func TestResolveAskPosture_SaveFailureUndoesTheWrite(t *testing.T) {
	cases := []struct {
		name     string
		existing bool // an untrusted entry for the root is already there
	}{
		{"created entry", false},
		{"flag on an existing entry", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, file := stubReviewHome(t)
			inst := makeReviewInstance(t)
			key := resolvedKey(t, inst)
			if tc.existing {
				keyJSON, _ := json.Marshal(key)
				writeTestFile(t, file, `{"projects": {`+string(keyJSON)+`: {"allowedTools": ["Read"]}}}`)
			}
			prev := saveReviewState
			saveReviewState = func(string, *workspace.InstanceState) error { return errors.New("disk full") }
			t.Cleanup(func() { saveReviewState = prev })

			var stderr bytes.Buffer
			if resolveAskPosture(&stderr, inst, true) {
				t.Error("resolveAskPosture = true after a failed state save, want false")
			}
			if tc.existing {
				e, ok := claudeEntry(t, file, key)
				if !ok {
					t.Fatalf("the existing entry was removed, want it kept")
				}
				if _, set := e["hasTrustDialogAccepted"]; set {
					t.Errorf("entry = %v, want the flag retracted", e)
				}
				if e["allowedTools"] == nil {
					t.Errorf("entry = %v, want allowedTools kept", e)
				}
			} else {
				assertNoEntry(t, file, key)
			}
			if g := recordedGrant(t, inst); g != nil {
				t.Errorf("record = %+v, want none", g)
			}
			if !strings.Contains(stderr.String(), "disk full") {
				t.Errorf("stderr = %q, want the save error reported", stderr.String())
			}
		})
	}
}

// TestResolveAskPosture_RelabelsWorkspaceGrant: an instance created with
// trust_instances on already holds a workspace grant; the review takes it
// over as source review without rewriting the file.
func TestResolveAskPosture_RelabelsWorkspaceGrant(t *testing.T) {
	home, file := stubReviewHome(t)
	inst := makeReviewInstance(t)
	seeded := claudetrust.Ensure(claudetrust.Env{Home: home}, inst, nil, claudetrust.SourceWorkspace)
	if seeded.Grant == nil || seeded.Warning != "" {
		t.Fatalf("seeding a workspace grant: %+v", seeded)
	}
	recordGrant(t, inst, seeded.Grant)
	before := statFile(t, file)

	if !resolveAskPosture(io.Discard, inst, true) {
		t.Fatal("resolveAskPosture = false, want true")
	}
	assertUnchanged(t, file, before)
	want := *seeded.Grant
	want.Source = claudetrust.SourceReview
	if g := recordedGrant(t, inst); g == nil || *g != want {
		t.Errorf("record = %+v, want %+v", g, want)
	}
}

// TestResolveAskPosture_AlreadyTrustedIsNotRecorded: an entry someone else
// already trusted counts as trusted, but it isn't niwa's, so nothing is
// recorded and the file is left alone.
func TestResolveAskPosture_AlreadyTrustedIsNotRecorded(t *testing.T) {
	_, file := stubReviewHome(t)
	inst := makeReviewInstance(t)
	keyJSON, _ := json.Marshal(resolvedKey(t, inst))
	writeTestFile(t, file, `{"projects": {`+string(keyJSON)+`: {"hasTrustDialogAccepted": true}}}`)
	before := statFile(t, file)

	if !resolveAskPosture(io.Discard, inst, true) {
		t.Error("resolveAskPosture = false for an already-trusted root, want true")
	}
	assertUnchanged(t, file, before)
	if g := recordedGrant(t, inst); g != nil {
		t.Errorf("record = %+v, want none", g)
	}
}
