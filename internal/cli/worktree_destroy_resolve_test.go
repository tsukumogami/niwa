package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func mappingFor(sessionID, handle, instancePath string, created time.Time) workspace.SessionMapping {
	return workspace.SessionMapping{
		SessionID:    sessionID,
		Handle:       handle,
		InstancePath: instancePath,
		Created:      created,
	}
}

const (
	uuidA = "6f1f8a0e-1f1a-4a3b-9c2d-5e6f70818283"
	uuidB = "a1b2c3d4-0000-4000-8000-000000000001"
)

// TestMatchMappings covers the three id forms a developer might hold and the
// near-misses that must not match. The prefix rule is the subtle one: it exists
// so a handle-less mapping is still addressable by something short, and it is
// switched off for a mapping that records a handle so one session never answers
// to two short forms.
func TestMatchMappings(t *testing.T) {
	withHandle := mappingFor(uuidA, "brave-otter", "/ws/a", time.Unix(10, 0))
	noHandle := mappingFor(uuidB, "", "/ws/b", time.Unix(20, 0))
	// What niwa writes for a Codex session: the handle is the session id.
	selfHandle := mappingFor(uuidA, uuidA, "/ws/a", time.Unix(10, 0))

	tests := []struct {
		name  string
		ms    []workspace.SessionMapping
		v     string
		want  []string
		notes string
	}{
		{name: "full session id", ms: []workspace.SessionMapping{withHandle, noHandle}, v: uuidA, want: []string{uuidA}},
		{name: "recorded handle", ms: []workspace.SessionMapping{withHandle, noHandle}, v: "brave-otter", want: []string{uuidA}},
		{name: "8-hex prefix of a handle-less mapping", ms: []workspace.SessionMapping{withHandle, noHandle}, v: "a1b2c3d4", want: []string{uuidB}},
		{name: "a mapping with a handle is not matched by its prefix", ms: []workspace.SessionMapping{withHandle}, v: "6f1f8a0e", want: nil},
		{name: "handle equal to the session id yields one match", ms: []workspace.SessionMapping{selfHandle}, v: uuidA, want: []string{uuidA}},
		{name: "7-character prefix matches nothing", ms: []workspace.SessionMapping{noHandle}, v: "a1b2c3d", want: nil},
		{name: "uppercase handle matches nothing", ms: []workspace.SessionMapping{withHandle}, v: "BRAVE-OTTER", want: nil},
		{name: "non-hex value matches nothing", ms: []workspace.SessionMapping{noHandle}, v: "zzzzzzzz", want: nil},
		{name: "empty value matches nothing", ms: []workspace.SessionMapping{withHandle, noHandle}, v: "", want: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := matchMappings(tc.ms, tc.v)
			if len(got) != len(tc.want) {
				t.Fatalf("want %v, got %d matches: %+v", tc.want, len(got), got)
			}
			for i, id := range tc.want {
				if got[i].SessionID != id {
					t.Fatalf("match %d: want %s, got %s", i, id, got[i].SessionID)
				}
			}
		})
	}
}

// TestMatchMappings_UnsafeHandleLosesHandleMatch: a handle reaches a terminal in
// the ambiguity line, and it comes from a file any same-user process can write.
// An unsafe one stops being a match key without the mapping disappearing.
func TestMatchMappings_UnsafeHandleLosesHandleMatch(t *testing.T) {
	for _, handle := range []string{"bad\x1b[31mred", "rtl‮override", strings.Repeat("x", 200)} {
		m := mappingFor(uuidA, handle, "/ws/a", time.Unix(10, 0))
		if got := matchMappings([]workspace.SessionMapping{m}, handle); len(got) != 0 {
			t.Errorf("handle %q should not be a match key, got %+v", handle, got)
		}
		if got := matchMappings([]workspace.SessionMapping{m}, uuidA); len(got) != 1 {
			t.Errorf("handle %q: the mapping must still match by session id, got %+v", handle, got)
		}
	}
}

// TestLoadMappingsForDestroy_FiltersNonUUID: in the single-instance layout the
// worktree lifecycle records share this directory, and they are keyed by eight
// hex characters. They must never be read as mappings.
func TestLoadMappingsForDestroy_FiltersNonUUID(t *testing.T) {
	root := t.TempDir()
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(sessionsDir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("real.json", `{"session_id":"`+uuidA+`","instance_path":"/ws/a"}`)
	write("ff001122.json", `{"session_id":"ff001122","repo":"niwa","status":"active"}`)
	write("garbage.json", `{not json`)

	got, err := loadMappingsForDestroy(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].SessionID != uuidA {
		t.Fatalf("want only the UUID mapping, got %+v", got)
	}
}

// TestLoadMappingsForDestroy_MissingStoreIsEmpty pins the direction the unlocked
// read fails in. A read taken while the root's config directory is mid-swap sees
// the directory missing; the resolver must then report "nothing matched" rather
// than inventing one. This is the known limitation #297 closes.
func TestLoadMappingsForDestroy_MissingStoreIsEmpty(t *testing.T) {
	got, err := loadMappingsForDestroy(t.TempDir())
	if err != nil {
		t.Fatalf("a missing store is not an error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want no mappings, got %+v", got)
	}

	scope := destroyScope{workspaceRoot: t.TempDir()}
	_, resolveErr := resolveDestroyTarget(scope, uuidA, got)
	var ece *sessionattach.ExitCodeError
	if !errors.As(resolveErr, &ece) || ece.Code != 3 {
		t.Fatalf("want exit 3 (no match), got %v", resolveErr)
	}
}

// TestResolveDestroyTarget covers each outcome of the contract, including the
// one place scope changes the answer: a value that is both a worktree id here
// and a session is ambiguous inside an instance and unambiguous at the root,
// where the worktree reading does not exist.
func TestResolveDestroyTarget(t *testing.T) {
	// An instance whose lifecycle store holds worktree id "a1b2c3d4".
	instanceDir := t.TempDir()
	wtSessions := filepath.Join(instanceDir, ".niwa", "sessions")
	if err := os.MkdirAll(wtSessions, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	rec := `{"v":1,"session_id":"a1b2c3d4","repo":"niwa","status":"active","worktree_path":"/x","creation_time":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(wtSessions, "a1b2c3d4.json"), []byte(rec), 0o600); err != nil {
		t.Fatalf("write record: %v", err)
	}

	// A handle-less mapping whose session id starts with the same eight hex.
	collide := mappingFor("a1b2c3d4-0000-4000-8000-000000000001", "", "/ws/b", time.Unix(20, 0))
	other := mappingFor(uuidA, "brave-otter", "/ws/a", time.Unix(10, 0))

	t.Run("worktree id with no session match", func(t *testing.T) {
		s := destroyScope{instanceDir: instanceDir}
		got, err := resolveDestroyTarget(s, "a1b2c3d4", []workspace.SessionMapping{other})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.worktreeID != "a1b2c3d4" || got.mapping != nil {
			t.Fatalf("want the worktree reading, got %+v", got)
		}
	})

	t.Run("single session match", func(t *testing.T) {
		s := destroyScope{}
		got, err := resolveDestroyTarget(s, "brave-otter", []workspace.SessionMapping{other, collide})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.mapping == nil || got.mapping.SessionID != uuidA {
			t.Fatalf("want the session reading, got %+v", got)
		}
	})

	t.Run("no match exits 3", func(t *testing.T) {
		s := destroyScope{}
		_, err := resolveDestroyTarget(s, "deadbeef", []workspace.SessionMapping{other})
		var ece *sessionattach.ExitCodeError
		if !errors.As(err, &ece) || ece.Code != 3 {
			t.Fatalf("want exit 3, got %v", err)
		}
		if !strings.Contains(ece.Msg, "no worktree or session matches") {
			t.Fatalf("unexpected message %q", ece.Msg)
		}
	})

	t.Run("two session matches exit 4 and name both", func(t *testing.T) {
		a := mappingFor(uuidA, "shared", "/ws/a", time.Unix(10, 0))
		b := mappingFor(uuidB, "shared", "/ws/b", time.Unix(20, 0))
		_, err := resolveDestroyTarget(destroyScope{}, "shared", []workspace.SessionMapping{a, b})
		var ece *sessionattach.ExitCodeError
		if !errors.As(err, &ece) || ece.Code != 4 {
			t.Fatalf("want exit 4, got %v", err)
		}
		if !strings.Contains(ece.Msg, uuidA) || !strings.Contains(ece.Msg, uuidB) {
			t.Fatalf("both matches must be named, got %q", ece.Msg)
		}
	})

	t.Run("worktree id that is also a session is ambiguous inside an instance", func(t *testing.T) {
		s := destroyScope{instanceDir: instanceDir}
		_, err := resolveDestroyTarget(s, "a1b2c3d4", []workspace.SessionMapping{collide})
		var ece *sessionattach.ExitCodeError
		if !errors.As(err, &ece) || ece.Code != 4 {
			t.Fatalf("want exit 4, got %v", err)
		}
		if !strings.Contains(ece.Msg, "--by-path") {
			t.Fatalf("the ambiguity line must say how to disambiguate, got %q", ece.Msg)
		}
	})

	t.Run("the same value at the root resolves to the session", func(t *testing.T) {
		s := destroyScope{} // no instanceDir: the worktree reading does not exist
		got, err := resolveDestroyTarget(s, "a1b2c3d4", []workspace.SessionMapping{collide})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.mapping == nil || got.mapping.SessionID != collide.SessionID {
			t.Fatalf("want the session reading at the root, got %+v", got)
		}
	})
}

// TestResolveDestroyScope_HonoursInstanceRootOverride pins R3's override on the
// positional path. `--by-path` went through resolveInstanceRoot and honoured
// NIWA_INSTANCE_ROOT from the start; the positional path classified cwd
// directly and ignored it, so at a multi-instance root a worktree id the
// override makes resolvable exited 3 instead of destroying. niwa exports the
// variable into worktree setup scripts, so this is reachable in ordinary use.
func TestResolveDestroyScope_HonoursInstanceRootOverride(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceRoot(t, root)
	writeInstanceState(t, root, "")
	instanceDir := filepath.Join(root, "inst-a")
	writeInstanceState(t, instanceDir, "inst-a")

	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	// Without the override, standing at a multi-instance root means no
	// instance, so a worktree id has nothing to resolve against.
	t.Setenv("NIWA_INSTANCE_ROOT", "")
	bare, err := resolveDestroyScope()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bare.instanceDir != "" {
		t.Fatalf("at a multi-instance root the scope has no instance; got %q", bare.instanceDir)
	}

	// With it, the named instance is the scope, and the workspace root is
	// still found so session ids keep resolving.
	t.Setenv("NIWA_INSTANCE_ROOT", instanceDir)
	got, err := resolveDestroyScope()
	if err != nil {
		t.Fatalf("the override must never be refused, got: %v", err)
	}
	if got.instanceDir != instanceDir {
		t.Fatalf("want instanceDir %q, got %q", instanceDir, got.instanceDir)
	}
	if got.workspaceRoot != root {
		t.Fatalf("want workspaceRoot %q, got %q", root, got.workspaceRoot)
	}
}

// TestResolveDestroyScope_OverrideTakenVerbatim: the value is niwa's own, and
// resolveInstanceRoot returns it without classifying. A path that resolves to
// no workspace still names an instance, so a worktree id resolves there and a
// session id finds no mappings rather than erroring.
func TestResolveDestroyScope_OverrideTakenVerbatim(t *testing.T) {
	orphan := t.TempDir()
	t.Setenv("NIWA_INSTANCE_ROOT", orphan)

	got, err := resolveDestroyScope()
	if err != nil {
		t.Fatalf("the override must never be refused, got: %v", err)
	}
	if got.instanceDir != orphan {
		t.Fatalf("want the value verbatim (%q), got %q", orphan, got.instanceDir)
	}
}

// TestCheckSessionInstance walks the ordered rungs. The returned directory is
// always the enumerated one, never the recorded string: a mapping can name any
// path, and only those the workspace root actually holds as instances survive.
func TestCheckSessionInstance(t *testing.T) {
	newRoot := func(t *testing.T) (root, inst string) {
		t.Helper()
		root = t.TempDir()
		writeWorkspaceRoot(t, root)
		inst = filepath.Join(root, "inst-a")
		writeInstanceState(t, inst, "inst-a")
		return root, inst
	}

	t.Run("enumerated instance resolves", func(t *testing.T) {
		root, inst := newRoot(t)
		m := mappingFor(uuidA, "", inst, time.Unix(10, 0))
		dir, gone, err := checkSessionInstance(root, m, []workspace.SessionMapping{m})
		if err != nil || gone {
			t.Fatalf("want a resolved instance, got (%q, %v, %v)", dir, gone, err)
		}
		if dir != inst {
			t.Fatalf("want %q, got %q", inst, dir)
		}
	})

	t.Run("missing directory is the exit-0 gone outcome", func(t *testing.T) {
		root, _ := newRoot(t)
		m := mappingFor(uuidA, "", filepath.Join(root, "never-existed"), time.Unix(10, 0))
		_, gone, err := checkSessionInstance(root, m, []workspace.SessionMapping{m})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !gone {
			t.Fatal("want gone=true for a missing instance directory")
		}
	})

	t.Run("path outside the workspace is refused", func(t *testing.T) {
		root, _ := newRoot(t)
		m := mappingFor(uuidA, "", t.TempDir(), time.Unix(10, 0))
		_, gone, err := checkSessionInstance(root, m, []workspace.SessionMapping{m})
		if err == nil || gone {
			t.Fatal("want a refusal for a path outside the workspace root")
		}
	})

	t.Run("the workspace root itself is refused", func(t *testing.T) {
		root, _ := newRoot(t)
		m := mappingFor(uuidA, "", root, time.Unix(10, 0))
		if _, _, err := checkSessionInstance(root, m, []workspace.SessionMapping{m}); err == nil {
			t.Fatal("want a refusal for the root itself")
		}
	})

	t.Run("a child that is not an instance is refused", func(t *testing.T) {
		root, _ := newRoot(t)
		plain := filepath.Join(root, "not-an-instance")
		if err := os.MkdirAll(plain, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		m := mappingFor(uuidA, "", plain, time.Unix(10, 0))
		if _, _, err := checkSessionInstance(root, m, []workspace.SessionMapping{m}); err == nil {
			t.Fatal("want a refusal: the directory is not an enumerated instance")
		}
	})

	t.Run("a superseded session is refused and names the newer one", func(t *testing.T) {
		root, inst := newRoot(t)
		older := mappingFor(uuidA, "", inst, time.Unix(10, 0))
		newer := mappingFor(uuidB, "", inst, time.Unix(20, 0))
		_, _, err := checkSessionInstance(root, older, []workspace.SessionMapping{older, newer})
		if err == nil {
			t.Fatal("want a refusal for a superseded session")
		}
		if !strings.Contains(err.Error(), uuidB) {
			t.Fatalf("the refusal must name the newer session, got %q", err)
		}
	})

	t.Run("the newest session for the instance resolves", func(t *testing.T) {
		root, inst := newRoot(t)
		older := mappingFor(uuidA, "", inst, time.Unix(10, 0))
		newer := mappingFor(uuidB, "", inst, time.Unix(20, 0))
		dir, _, err := checkSessionInstance(root, newer, []workspace.SessionMapping{older, newer})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dir != inst {
			t.Fatalf("want %q, got %q", inst, dir)
		}
	})
}

// TestNewestMappingPerInstance pins the tie-break, so the result does not depend
// on directory-read order.
func TestNewestMappingPerInstance(t *testing.T) {
	same := time.Unix(10, 0)
	a := mappingFor(uuidA, "", "/ws/a", same)
	b := mappingFor(uuidB, "", "/ws/a", same)
	got := workspace.NewestMappingPerInstance([]workspace.SessionMapping{b, a})
	if got["/ws/a"].SessionID != uuidA {
		t.Fatalf("ties keep the first session id in order; got %s", got["/ws/a"].SessionID)
	}

	newer := mappingFor(uuidB, "", "/ws/a", time.Unix(20, 0))
	got = workspace.NewestMappingPerInstance([]workspace.SessionMapping{a, newer})
	if got["/ws/a"].SessionID != uuidB {
		t.Fatalf("want the newest by Created; got %s", got["/ws/a"].SessionID)
	}

	// Two spellings of one directory group together.
	unclean := mappingFor(uuidB, "", "/ws/a/", time.Unix(30, 0))
	got = workspace.NewestMappingPerInstance([]workspace.SessionMapping{a, unclean})
	if len(got) != 1 {
		t.Fatalf("want one group for one directory, got %d: %+v", len(got), got)
	}
}
