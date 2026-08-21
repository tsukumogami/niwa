package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// seedGlobalConfig writes a config holding one workspace entry and returns the
// path plus the exact bytes on disk, so a later assertion can compare against
// what was really there rather than against a re-encoding of the same struct.
func seedGlobalConfig(t *testing.T) (path string, before []byte) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "config.toml")
	cfg := &GlobalConfig{
		Global: GlobalSettings{CloneProtocol: "ssh"},
		Registry: map[string]RegistryEntry{
			"already-registered": {
				Source: "/ws/.niwa/workspace.toml",
				Root:   "/ws",
			},
		},
	}
	if err := SaveGlobalConfigTo(path, cfg); err != nil {
		t.Fatalf("seeding the config: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the seeded config: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("the seeded config is empty; the test would assert nothing")
	}
	return path, before
}

// TestGlobalConfigSurvivesAFailedEncode is the case the atomic write exists
// for. This file is the whole workspace registry, so a write that dies partway
// used to leave a registry with no workspaces in it -- the truncate happened
// first and the bytes never arrived.
//
// The encoder here writes a plausible-looking prefix and then fails, which is
// what a full disk or an interrupted process looks like from the writer's side.
// Afterwards the target must still hold the previous config, byte for byte, and
// the prefix must not be observable anywhere the config is read from.
func TestGlobalConfigSurvivesAFailedEncode(t *testing.T) {
	path, before := seedGlobalConfig(t)

	const partial = "[global]\nclone_protocol = \"htt"
	encodeErr := errors.New("no space left on device")
	err := writeGlobalConfigFile(path, func(w io.Writer) error {
		if _, werr := io.WriteString(w, partial); werr != nil {
			return werr
		}
		return encodeErr
	})
	if err == nil {
		t.Fatal("writeGlobalConfigFile returned nil for an encode that failed")
	}
	if !errors.Is(err, encodeErr) {
		t.Errorf("error = %v; want it to wrap the encode failure", err)
	}

	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("the config is unreadable after a failed write: %v", readErr)
	}
	if string(after) != string(before) {
		t.Errorf("the config changed under a failed write.\n before: %q\n  after: %q", before, after)
	}
	if strings.Contains(string(after), partial) {
		t.Error("the target path holds the partial write; a failed encode must not be observable there")
	}

	// The registry has to survive as data, not just as bytes: the whole point
	// is that the next reader still finds the workspaces it had.
	loaded, err := LoadGlobalConfigFrom(path)
	if err != nil {
		t.Fatalf("the surviving config no longer parses: %v", err)
	}
	if loaded.LookupWorkspace("already-registered") == nil {
		t.Error("the registered workspace is gone after a failed write")
	}
}

// TestFailedEncodeLeavesNoTemporaryFile asserts the cleanup half. A temp file
// left in the config directory after every failure accumulates, and one left
// with a name a later reader might glob for is worse than the truncation this
// replaced.
func TestFailedEncodeLeavesNoTemporaryFile(t *testing.T) {
	path, _ := seedGlobalConfig(t)
	dir := filepath.Dir(path)

	err := writeGlobalConfigFile(path, func(w io.Writer) error {
		_, _ = io.WriteString(w, "[global]\n")
		return errors.New("encode failed")
	})
	if err == nil {
		t.Fatal("writeGlobalConfigFile returned nil for an encode that failed")
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("reading the config directory: %v", readErr)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(path) {
			t.Errorf("the config directory holds %q after a failed write; the temporary file was not cleaned up", e.Name())
		}
	}
}

// TestSuccessfulWriteLeavesNoTemporaryFile is the same assertion on the happy
// path: the rename consumes the temp file rather than leaving a copy beside the
// config.
func TestSuccessfulWriteLeavesNoTemporaryFile(t *testing.T) {
	path, _ := seedGlobalConfig(t)
	dir := filepath.Dir(path)

	if err := SaveGlobalConfigTo(path, &GlobalConfig{Global: GlobalSettings{CloneProtocol: "https"}}); err != nil {
		t.Fatalf("save error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the config directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("the config directory holds %v; want only %q", names, filepath.Base(path))
	}
}

// TestConcurrentWritesAreNeverObservedPartial runs writers and readers against
// one config at the same time. Every read must return a config that parses and
// carries the registry entry one of the writers wrote -- never a truncated
// file, never a splice of two.
//
// Goroutines are a weaker model than separate processes, but the failure they
// reproduce is the same one: with an in-place truncate the target spends real
// time holding zero or partial bytes, and a reader that lands in that window
// sees a workspace registry with nothing in it. This branch's premise is many
// dispatches running at once, each writing this file.
func TestConcurrentWritesAreNeverObservedPartial(t *testing.T) {
	const writers = 4
	const rounds = 25
	const entries = 40

	// Every config any writer produces holds the same number of entries, so a
	// reader that counts anything else has observed a file mid-write. A wide
	// registry also makes each encode long enough to be caught in that window.
	build := func(w int) *GlobalConfig {
		cfg := &GlobalConfig{
			Global:   GlobalSettings{CloneProtocol: "ssh"},
			Registry: map[string]RegistryEntry{},
		}
		for i := 0; i < entries; i++ {
			name := fmt.Sprintf("ws-%d-%d", w, i)
			cfg.Registry[name] = RegistryEntry{
				Source: "/workspaces/" + name + "/.niwa/workspace.toml",
				Root:   "/workspaces/" + name,
			}
		}
		return cfg
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := SaveGlobalConfigTo(path, build(0)); err != nil {
		t.Fatalf("seeding the config: %v", err)
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				if err := SaveGlobalConfigTo(path, build(w)); err != nil {
					t.Errorf("writer %d round %d: %v", w, r, err)
					return
				}
			}
		}(w)
	}

	var readerWG sync.WaitGroup
	for reader := 0; reader < 2; reader++ {
		readerWG.Add(1)
		go func() {
			defer readerWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				cfg, err := LoadGlobalConfigFrom(path)
				if err != nil {
					t.Errorf("a concurrent read saw an unparseable config: %v", err)
					return
				}
				if len(cfg.Registry) != entries {
					t.Errorf("a concurrent read saw %d registry entries; every write puts %d there, so this file was observed part-written", len(cfg.Registry), entries)
					return
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	readerWG.Wait()
}
