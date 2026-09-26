package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
)

// The capture suite runs every assertion against three differently-shaped
// record stores. That is the whole point of it. Correlating a launched worker
// to its instance is one behavior -- normalize both sides of the path, refuse
// to guess when two records claim the same directory, keep polling while an id
// has not appeared, give up at the deadline -- and it is one behavior whichever
// agent was launched. Running it against a single store would prove one path
// works and prove nothing about whether this is a capture or one agent's
// capture with a parameter bolted on.
//
// Two of the three are the shapes niwa actually declares. The third is a
// fixture and is deliberately shaped like neither -- a different environment
// override, different nesting, different field names -- so the reader is
// exercised past the two shapes it was written against rather than only
// between them.

// The handles the fixtures plant are deliberately not prefixes of the session
// ids beside them. An earlier version used "1234" against a session id starting
// "12345678", which left a `sessionID[:4]` implementation passing every
// assertion here except the one written specifically to catch it -- so the
// suite as a whole stopped discriminating, and one test carried the weight
// alone.

// captureStore is one record-store shape plus the fixture writer for it.
type captureStore struct {
	name    string
	records agentplan.SessionRecords
	// write plants a record for a session with the given handle, id, and
	// working directory. handle is what the store's declaration says a
	// developer types; a store whose handle is the id ignores it.
	write func(t *testing.T, root, handle, sessionID, cwd string)
	// wantHandle is what capture should return for a record planted with this
	// handle and id.
	wantHandle func(handle, sessionID string) string
}

// claudeStore is the shape niwa ships today: one directory per job holding a
// pretty-printed JSON object, with the directory's name as the handle.
func claudeStore() captureStore {
	return captureStore{
		name:    "directory-per-session",
		records: claudeLaunchSpec().Records,
		write: func(t *testing.T, root, handle, sessionID, cwd string) {
			t.Helper()
			dir := filepath.Join(root, handle)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatalf("mkdir record dir: %v", err)
			}
			body := `{
  "sessionId": "` + sessionID + `",
  "template": "bg",
  "state": "running",
  "cwd": "` + cwd + `",
  "updatedAt": "2026-01-01T00:00:00Z"
}`
			if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(body), 0o600); err != nil {
				t.Fatalf("write record: %v", err)
			}
		},
		wantHandle: func(handle, _ string) string { return handle },
	}
}

// nestedStore is the fixture shape: records nested three directories deep,
// matched by a glob, each one a transcript whose first line is its metadata,
// with the two fields under a nested key and the id as its own handle.
func nestedStore() captureStore {
	return captureStore{
		name: "nested-transcript-first-line",
		records: agentplan.SessionRecords{
			HomeEnv:       "NIWA_TEST_RECORD_HOME",
			HomePath:      []string{".fixture", "sessions"},
			Depth:         3,
			FileGlob:      "session-*.jsonl",
			FirstLineOnly: true,
			CwdPath:       []string{"meta", "working_dir"},
			IDPath:        []string{"meta", "id"},
			Handle:        agentplan.HandleSessionID,
			Liveness:      agentplan.LivenessNone,
		},
		write: func(t *testing.T, root, handle, sessionID, cwd string) {
			t.Helper()
			dir := filepath.Join(root, "2026", "08", "18")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatalf("mkdir record dir: %v", err)
			}
			first := `{"kind":"meta","meta":{"id":"` + sessionID + `","working_dir":"` + cwd + `","origin":"headless"}}`
			// A second line, so a reader that swallowed the whole file would
			// fail to parse rather than quietly succeed.
			body := first + "\n" + `{"kind":"turn","text":"hello"}` + "\n"
			name := "session-" + handle + ".jsonl"
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatalf("write record: %v", err)
			}
		},
		wantHandle: func(_, sessionID string) string { return sessionID },
	}
}

// codexStore is the second real shape, driven by the declared record
// description rather than by a restatement of it, against a fixture written in
// the envelope a real session record uses: one JSON object per line, the
// session metadata on the first, the two fields under a payload key, and the
// file nested under the date the session started.
//
// It is here rather than only in the synthetic store because a description can
// be internally consistent and still not describe the thing it is about. This
// is the test that fails if the declared field paths, the nesting depth, or the
// glob stop matching what the agent actually writes.
func codexStore(t *testing.T) captureStore {
	t.Helper()
	spec, ok := agentplan.For(agent.AgentCodex).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for codex")
	}
	return captureStore{
		name:    "codex",
		records: spec.Records,
		write: func(t *testing.T, root, handle, sessionID, cwd string) {
			t.Helper()
			// The date directories are the host's local time, which is why the
			// fixture picks its own rather than deriving one: capture walks the
			// tree, so any date works and none is special.
			dir := filepath.Join(root, "2026", "08", "19")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatalf("mkdir record dir: %v", err)
			}
			meta := `{"timestamp":"2026-08-19T04:00:00.000Z","ordinal":0,"type":"session_meta","payload":` +
				`{"id":"` + sessionID + `","session_id":"` + sessionID + `","timestamp":"2026-08-19T04:00:00.000Z",` +
				`"cwd":"` + cwd + `","originator":"codex_exec","cli_version":"0.147.0","source":"exec"}}`
			// A second line, because the real file is a transcript and a reader
			// that swallowed the whole thing would fail to parse rather than
			// quietly succeed.
			body := meta + "\n" + `{"timestamp":"2026-08-19T04:00:01.000Z","ordinal":1,"type":"turn_context","payload":{}}` + "\n"
			name := "rollout-2026-08-19T04-00-00-" + handle + ".jsonl"
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatalf("write record: %v", err)
			}
		},
		wantHandle: func(_, sessionID string) string { return sessionID },
	}
}

func captureStores(t *testing.T) []captureStore {
	t.Helper()
	return []captureStore{claudeStore(), nestedStore(), codexStore(t)}
}

func TestCaptureSessionID(t *testing.T) {
	const sid = "12345678-90ab-cdef-1234-567890abcdef"
	const other = "ffffffff-ffff-ffff-ffff-ffffffffffff"

	for _, store := range captureStores(t) {
		t.Run(store.name, func(t *testing.T) {
			t.Run("present immediately returns the id and the handle", func(t *testing.T) {
				root := t.TempDir()
				instanceDir := t.TempDir()
				store.write(t, root, "handle-a", sid, instanceDir)

				got, handle, err := captureSessionID(store.records, root, instanceDir, time.Second, nil, time.Millisecond)
				if err != nil {
					t.Fatalf("capture: %v", err)
				}
				if got != sid {
					t.Errorf("got %q, want %q", got, sid)
				}
				if want := store.wantHandle("handle-a", sid); handle != want {
					t.Errorf("handle = %q, want %q", handle, want)
				}
			})

			t.Run("appears within the bound", func(t *testing.T) {
				root := t.TempDir()
				instanceDir := t.TempDir()

				// Written shortly after the call begins; the poll re-reads each
				// pass so it is picked up before the timeout.
				go func() {
					time.Sleep(20 * time.Millisecond)
					store.write(t, root, "handle-a", sid, instanceDir)
				}()

				got, _, err := captureSessionID(store.records, root, instanceDir, 2*time.Second, nil, 5*time.Millisecond)
				if err != nil {
					t.Fatalf("capture: %v", err)
				}
				if got != sid {
					t.Errorf("got %q, want %q", got, sid)
				}
			})

			t.Run("never appears yields a timeout error", func(t *testing.T) {
				_, _, err := captureSessionID(store.records, t.TempDir(), t.TempDir(), 30*time.Millisecond, nil, 5*time.Millisecond)
				if err == nil {
					t.Fatal("expected a timeout error, got nil")
				}
			})

			t.Run("two sessions same cwd is ambiguous", func(t *testing.T) {
				root := t.TempDir()
				instanceDir := t.TempDir()
				store.write(t, root, "handle-a", sid, instanceDir)
				store.write(t, root, "5678", other, instanceDir)

				_, _, err := captureSessionID(store.records, root, instanceDir, time.Second, nil, time.Millisecond)
				if err == nil {
					t.Fatal("expected an ambiguity error, got nil")
				}
			})

			t.Run("non-matching cwd is ignored", func(t *testing.T) {
				root := t.TempDir()
				instanceDir := t.TempDir()
				store.write(t, root, "9999", other, t.TempDir())

				_, _, err := captureSessionID(store.records, root, instanceDir, 30*time.Millisecond, nil, 5*time.Millisecond)
				if err == nil {
					t.Fatal("expected a timeout error (no matching cwd), got nil")
				}
			})

			t.Run("invalid id keeps polling then times out", func(t *testing.T) {
				root := t.TempDir()
				instanceDir := t.TempDir()
				// Matching cwd but a non-UUID id: treated as not-yet-ready.
				store.write(t, root, "handle-a", "not-a-uuid", instanceDir)

				_, _, err := captureSessionID(store.records, root, instanceDir, 30*time.Millisecond, nil, 5*time.Millisecond)
				if err == nil {
					t.Fatal("expected a timeout error for an invalid id, got nil")
				}
			})

			t.Run("symlinked instance path still matches", func(t *testing.T) {
				root := t.TempDir()
				realDir := t.TempDir()
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(realDir, link); err != nil {
					t.Skipf("symlink unsupported: %v", err)
				}
				// The record holds the real dir; capture runs against the link.
				store.write(t, root, "handle-a", sid, realDir)

				got, _, err := captureSessionID(store.records, root, link, time.Second, nil, time.Millisecond)
				if err != nil {
					t.Fatalf("capture via symlink: %v", err)
				}
				if got != sid {
					t.Errorf("got %q, want %q", got, sid)
				}
			})
		})
	}
}

// TestCaptureHandleIsNotAnIDSlice pins the handle to what the store actually
// records rather than to a prefix of the id. A store whose handle is the name
// of a directory hands out that name; deriving it from the id instead would go
// on working right up until the two stopped coinciding, and then hand a
// developer a command that fails.
func TestCaptureHandleIsNotAnIDSlice(t *testing.T) {
	const sid = "12345678-90ab-cdef-1234-567890abcdef"
	store := claudeStore()
	root := t.TempDir()
	instanceDir := t.TempDir()
	// A name that is deliberately not the leading slice of the id.
	store.write(t, root, "job-xyz", sid, instanceDir)

	got, handle, err := captureSessionID(store.records, root, instanceDir, time.Second, nil, time.Millisecond)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if got != sid {
		t.Errorf("got %q, want %q", got, sid)
	}
	if handle != "job-xyz" {
		t.Errorf("handle = %q, want the record's own directory name %q", handle, "job-xyz")
	}
}

// TestRecordStoreRoot covers the two ways a store's root is resolved, including
// the environment override an agent may offer for its own state directory.
func TestRecordStoreRoot(t *testing.T) {
	claude := claudeLaunchSpec().Records
	nested := nestedStore().records

	if got, want := recordStoreRoot(claude, "/home/dev", func(string) string { return "" }), filepath.Join("/home/dev", ".claude", "jobs"); got != want {
		t.Errorf("root under home = %q, want %q", got, want)
	}
	// A store with no environment override ignores one that happens to be set.
	if got, want := recordStoreRoot(claude, "/home/dev", func(string) string { return "/elsewhere" }), filepath.Join("/home/dev", ".claude", "jobs"); got != want {
		t.Errorf("root with an irrelevant override = %q, want %q", got, want)
	}
	// A store that declares an override honors it, keeping everything below the
	// agent's own directory.
	env := func(name string) string {
		if name == "NIWA_TEST_RECORD_HOME" {
			return "/opt/fixture"
		}
		return ""
	}
	if got, want := recordStoreRoot(nested, "/home/dev", env), filepath.Join("/opt/fixture", "sessions"); got != want {
		t.Errorf("root under the override = %q, want %q", got, want)
	}
	// No home and no override is no evidence, not a guess at the filesystem
	// root.
	if got := recordStoreRoot(claude, "", nil); got != "" {
		t.Errorf("root with no home = %q, want empty", got)
	}
}
