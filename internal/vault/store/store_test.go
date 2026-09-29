//go:build unix

package store

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/vault"
)

func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) != "" {
		os.Exit(runHelper())
	}
	os.Exit(m.Run())
}

// isolate points XDG_STATE_HOME and HOME at fresh temp directories so no
// test reads or writes the operator's real store, and returns Dir().
func isolate(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	return dir
}

func testIdentity() vault.Identity {
	return vault.Identity{
		Kind:        "infisical",
		APIDomain:   "https://app.infisical.com",
		ProjectID:   "proj-1",
		Environment: "dev",
		FolderPath:  "/",
	}
}

func entry(value string, at time.Time, token string) Entry {
	return Entry{Value: []byte(value), ResolvedAt: at, VersionToken: token, Provenance: "prov-" + token}
}

func dataPath(dir string, id vault.Identity) string {
	stem, _ := fileStem(id)
	return filepath.Join(dir, stem+".json")
}

func lockPath(dir string, id vault.Identity) string {
	stem, _ := fileStem(id)
	return filepath.Join(dir, stem+".lock")
}

func mustUpdate(t *testing.T, dir string, id vault.Identity, puts map[string]Entry, evictKeys []string, evictAll bool) {
	t.Helper()
	if err := Update(dir, id, puts, evictKeys, evictAll); err != nil {
		t.Fatalf("Update: %v", err)
	}
}

func mustLoad(t *testing.T, dir string, id vault.Identity) map[string]Entry {
	t.Helper()
	got, err := Load(dir, id)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return got
}

func keysOf(m map[string]Entry) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func assertKeys(t *testing.T, got map[string]Entry, want ...string) {
	t.Helper()
	sort.Strings(want)
	gk := keysOf(got)
	if len(gk) != len(want) {
		t.Fatalf("keys = %v, want %v", gk, want)
	}
	for i := range gk {
		if gk[i] != want[i] {
			t.Fatalf("keys = %v, want %v", gk, want)
		}
	}
}

func jsonFiles(t *testing.T, dir string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestDir(t *testing.T) {
	home := t.TempDir()
	state := t.TempDir()
	homeDefault := filepath.Join(home, ".local", "state", "niwa", "secret-cache")

	cases := []struct {
		name  string
		unset bool
		xdg   string
		want  string
	}{
		{name: "absolute", xdg: state, want: filepath.Join(state, "niwa", "secret-cache")},
		{name: "unset", unset: true, want: homeDefault},
		{name: "empty", xdg: "", want: homeDefault},
		{name: "relative", xdg: "relative/state", want: homeDefault},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("XDG_STATE_HOME", tc.xdg)
			if tc.unset {
				os.Unsetenv("XDG_STATE_HOME")
			}
			got, err := Dir()
			if err != nil {
				t.Fatalf("Dir: %v", err)
			}
			if got != tc.want {
				t.Errorf("Dir() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The store's location follows XDG_STATE_HOME alone: a working directory
// and XDG_CONFIG_HOME inside a git work tree don't pull it in.
func TestDirIgnoresWorkTreeCwdAndConfig(t *testing.T) {
	isolate(t)
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(repo, "config")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Chdir(repo)

	dir, err := Dir()
	if err != nil {
		t.Fatalf("Dir: %v", err)
	}
	if want := filepath.Join(state, "niwa", "secret-cache"); dir != want {
		t.Fatalf("Dir() = %q, want %q", dir, want)
	}
	mustUpdate(t, dir, testIdentity(), map[string]Entry{"X": entry("x", time.Now(), "t")}, nil, false)

	var inRepo []string
	_ = filepath.WalkDir(repo, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			inRepo = append(inRepo, p)
		}
		return nil
	})
	if len(inRepo) != 0 {
		t.Fatalf("files written inside the work tree: %v", inRepo)
	}
	if len(jsonFiles(t, dir)) != 1 {
		t.Fatalf("expected one data file under %s", dir)
	}
}

func TestIdentitiesThatDifferGetSeparateFiles(t *testing.T) {
	base := testIdentity()
	variants := map[string]func(*vault.Identity){
		"api domain":  func(id *vault.Identity) { id.APIDomain = "https://eu.infisical.com" },
		"environment": func(id *vault.Identity) { id.Environment = "prod" },
		"folder path": func(id *vault.Identity) { id.FolderPath = "/team" },
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			dir := isolate(t)
			other := base
			mutate(&other)
			mustUpdate(t, dir, base, map[string]Entry{"K": entry("a", time.Now(), "1")}, nil, false)
			mustUpdate(t, dir, other, map[string]Entry{"K": entry("b", time.Now(), "2")}, nil, false)
			if n := len(jsonFiles(t, dir)); n != 2 {
				t.Fatalf("data files = %d, want 2", n)
			}
			if got := mustLoad(t, dir, base)["K"].Value; string(got) != "a" {
				t.Errorf("base value = %q, want a", got)
			}
			if got := mustLoad(t, dir, other)["K"].Value; string(got) != "b" {
				t.Errorf("other value = %q, want b", got)
			}
		})
	}
}

func TestIdentitySpellingsShareAFile(t *testing.T) {
	groups := map[string][]func(*vault.Identity){
		"folder path": {
			func(id *vault.Identity) { id.FolderPath = "/a/b" },
			func(id *vault.Identity) { id.FolderPath = "a/b" },
			func(id *vault.Identity) { id.FolderPath = "/a/b/" },
		},
		"api domain": {
			func(id *vault.Identity) { id.APIDomain = "https://App.Infisical.com/api" },
			func(id *vault.Identity) { id.APIDomain = "https://app.infisical.com" },
		},
	}
	for name, spellings := range groups {
		t.Run(name, func(t *testing.T) {
			dir := isolate(t)
			for i, spell := range spellings {
				id := testIdentity()
				spell(&id)
				mustUpdate(t, dir, id, map[string]Entry{"K" + string(rune('0'+i)): entry("v", time.Now(), "t")}, nil, false)
			}
			if n := len(jsonFiles(t, dir)); n != 1 {
				t.Fatalf("data files = %d, want 1", n)
			}
			id := testIdentity()
			spellings[0](&id)
			if got := mustLoad(t, dir, id); len(got) != len(spellings) {
				t.Fatalf("entries = %v, want %d", keysOf(got), len(spellings))
			}
		})
	}
}

func TestRoundTripKeepsBytesExactly(t *testing.T) {
	dir := isolate(t)
	id := testIdentity()
	at := time.Date(2026, 9, 29, 10, 0, 0, 123, time.FixedZone("x", 3600))
	puts := map[string]Entry{
		"BINARY": {Value: []byte{0xff, 0xfe, 0x00, 'a', 0x80}, ResolvedAt: at, VersionToken: "v1", Provenance: "p1"},
		"EMPTY":  {Value: []byte{}, ResolvedAt: at, VersionToken: "v2", Provenance: "p2"},
		"NOTOK":  {Value: []byte("plain"), ResolvedAt: at},
		// An empty secret revealed as nil is stored as JSON null and
		// comes back empty.
		"NIL": {Value: nil, ResolvedAt: at, VersionToken: "v3"},
	}
	mustUpdate(t, dir, id, puts, nil, false)
	got := mustLoad(t, dir, id)
	assertKeys(t, got, "BINARY", "EMPTY", "NOTOK", "NIL")
	for k, want := range puts {
		g := got[k]
		if !bytes.Equal(g.Value, want.Value) || !g.ResolvedAt.Equal(want.ResolvedAt) ||
			g.VersionToken != want.VersionToken || g.Provenance != want.Provenance {
			t.Errorf("%s = %+v, want %+v", k, g, want)
		}
		if g.ResolvedAt.Location() != time.UTC {
			t.Errorf("%s resolved_at not UTC: %v", k, g.ResolvedAt)
		}
	}
}

func TestDataFileFormat(t *testing.T) {
	dir := isolate(t)
	id := vault.Identity{Kind: "infisical", APIDomain: "HTTPS://App.Infisical.com/api/", ProjectID: "p", Environment: "dev", FolderPath: "team/"}
	at := time.Date(2026, 9, 29, 10, 0, 0, 0, time.FixedZone("x", -7200))
	mustUpdate(t, dir, id, map[string]Entry{"K": {Value: []byte("secret-bytes"), ResolvedAt: at, VersionToken: "tok", Provenance: "prov"}}, nil, false)

	raw, err := os.ReadFile(dataPath(dir, id))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		FormatVersion int                          `json:"format_version"`
		Identity      map[string]string            `json:"identity"`
		Keys          map[string]map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("data file is not JSON: %v", err)
	}
	if doc.FormatVersion != 1 {
		t.Errorf("format_version = %d, want 1", doc.FormatVersion)
	}
	wantID := map[string]string{
		"kind": "infisical", "api_domain": "https://app.infisical.com", "project_id": "p",
		"environment": "dev", "folder_path": "/team",
	}
	if !sameEcho(doc.Identity, wantID) {
		t.Errorf("identity = %v, want %v", doc.Identity, wantID)
	}
	k := doc.Keys["K"]
	if k["value"] != base64.StdEncoding.EncodeToString([]byte("secret-bytes")) {
		t.Errorf("value = %q, want base64", k["value"])
	}
	if k["resolved_at"] != "2026-09-29T12:00:00Z" {
		t.Errorf("resolved_at = %q, want RFC 3339 UTC", k["resolved_at"])
	}
	if k["version_token"] != "tok" || k["provenance"] != "prov" {
		t.Errorf("token fields = %v", k)
	}

}

// The file names are the SHA-256 of the normalised five fields encoded as
// a JSON array. The digest is computed outside the package
// (printf '%s' '["infisical","https://app.infisical.com","p","dev","/team"]' | sha256sum),
// so a change of encoding, field order or normalisation fails here
// instead of silently orphaning every stored file.
func TestFileNamesAreTheIdentityHash(t *testing.T) {
	dir := isolate(t)
	const golden = "6f6a47515934ea641cc80779cd4675dc80ac9e55cfdab542ebb5b75ef9d297f9"
	id := vault.Identity{Kind: "infisical", APIDomain: "HTTPS://App.Infisical.com/api/", ProjectID: "p", Environment: "dev", FolderPath: "team/"}

	mustUpdate(t, dir, id, map[string]Entry{"K": entry("v", time.Now(), "t")}, nil, false)
	testHookBeforeRename = func() error { return errors.New("abort") }
	_ = Update(dir, id, map[string]Entry{"K": entry("v2", time.Now(), "t")}, nil, false)
	testHookBeforeRename = nil

	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range names {
		got = append(got, n.Name())
	}
	sort.Strings(got)
	if len(got) != 3 || !strings.HasPrefix(got[0], "."+golden+".json.tmp-") ||
		got[1] != golden+".json" || got[2] != golden+".lock" {
		t.Fatalf("store files = %v, want .%s.json.tmp-*, %s.json and %s.lock", got, golden, golden, golden)
	}
}

// Workspace A resolves X and Y, workspace B resolves only X: Y stays.
func TestUpdateMergesPerKey(t *testing.T) {
	dir := isolate(t)
	id := testIdentity()
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	mustUpdate(t, dir, id, map[string]Entry{"X": entry("x1", t1, "a"), "Y": entry("y1", t1, "b")}, nil, false)
	mustUpdate(t, dir, id, map[string]Entry{"X": entry("x2", t2, "c")}, nil, false)

	got := mustLoad(t, dir, id)
	assertKeys(t, got, "X", "Y")
	if string(got["X"].Value) != "x2" || !got["X"].ResolvedAt.Equal(t2) {
		t.Errorf("X = %+v", got["X"])
	}
	if string(got["Y"].Value) != "y1" || !got["Y"].ResolvedAt.Equal(t1) {
		t.Errorf("Y = %+v, want original value and resolved_at", got["Y"])
	}
}

func TestUpdateEvicts(t *testing.T) {
	now := time.Now()
	seed := map[string]Entry{"A": entry("a", now, "1"), "B": entry("b", now, "2"), "C": entry("c", now, "3")}

	t.Run("evictKeys removes only the named keys", func(t *testing.T) {
		dir := isolate(t)
		id := testIdentity()
		mustUpdate(t, dir, id, seed, nil, false)
		mustUpdate(t, dir, id, nil, []string{"A", "C", "MISSING"}, false)
		assertKeys(t, mustLoad(t, dir, id), "B")
	})
	t.Run("evictAll runs before the same call's puts", func(t *testing.T) {
		dir := isolate(t)
		id := testIdentity()
		mustUpdate(t, dir, id, seed, nil, false)
		mustUpdate(t, dir, id, map[string]Entry{"D": entry("d", now, "4")}, nil, true)
		assertKeys(t, mustLoad(t, dir, id), "D")
	})
	t.Run("evictKeys runs before the same call's puts", func(t *testing.T) {
		dir := isolate(t)
		id := testIdentity()
		mustUpdate(t, dir, id, seed, nil, false)
		mustUpdate(t, dir, id, map[string]Entry{"A": entry("new", now, "5")}, []string{"A"}, false)
		if got := mustLoad(t, dir, id)["A"]; string(got.Value) != "new" {
			t.Errorf("A = %q, want new", got.Value)
		}
	})
	t.Run("evicting absent keys doesn't rewrite the file", func(t *testing.T) {
		dir := isolate(t)
		id := testIdentity()
		mustUpdate(t, dir, id, seed, nil, false)
		before, err := os.Stat(dataPath(dir, id))
		if err != nil {
			t.Fatal(err)
		}
		mustUpdate(t, dir, id, nil, []string{"NOPE", "ALSO_NOPE"}, false)
		mustUpdate(t, dir, id, map[string]Entry{"A": seed["A"]}, nil, false) // identical put
		after, err := os.Stat(dataPath(dir, id))
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(before, after) {
			t.Fatal("data file was rewritten by an update that changed nothing")
		}
	})
	t.Run("evicting from an absent identity writes nothing", func(t *testing.T) {
		dir := isolate(t)
		mustUpdate(t, dir, testIdentity(), nil, []string{"A"}, true)
		if n := len(jsonFiles(t, dir)); n != 0 {
			t.Fatalf("data files = %d, want 0", n)
		}
	})
}

func TestLoadMissingStoreIsEmpty(t *testing.T) {
	dir := isolate(t)
	got, err := Load(dir, testIdentity())
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("Load on a missing store = %v, %v; want empty map, nil", got, err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load created the store directory")
	}
}
