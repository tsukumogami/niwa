package claudetrust

import (
	"reflect"
	"testing"
)

// TestEnsureRecordedGrantKeepsItsPlace covers "first source keeps it" when the
// entry a record names has changed by hand: a reset flag is set again under
// the record's own mode and source, and a removed entry is created again under
// the record's source, whichever source is asking now.
func TestEnsureRecordedGrantKeepsItsPlace(t *testing.T) {
	t.Run("flag reset", func(t *testing.T) {
		for _, mode := range []Mode{ModeCreated, ModeFlagOnly} {
			t.Run(string(mode), func(t *testing.T) {
				f := newFixture(t)
				writeFile(t, f.file, `{"projects": {`+jsonKey(t, f.key)+`: {"hasTrustDialogAccepted": false, "allowedTools": ["Read"]}}}`, 0o600)
				prior := &Grant{ConfigFile: f.file, Key: f.key, Mode: mode, Source: SourceReview}

				out := Ensure(f.env, f.root, prior, SourceWorkspace)
				want := &Grant{ConfigFile: f.file, Key: f.key, Mode: mode, Source: SourceReview}
				if out.Warning != "" || !out.Changed || out.Created || !reflect.DeepEqual(out.Grant, want) {
					t.Fatalf("outcome = %+v (grant %+v), want %+v, changed and not created", out, out.Grant, want)
				}
				e, _ := entryOf(t, f.file, f.key)
				if e["hasTrustDialogAccepted"] != true || e["allowedTools"] == nil {
					t.Errorf("entry = %v, want the flag set and allowedTools kept", e)
				}
			})
		}
	})

	t.Run("entry removed", func(t *testing.T) {
		f := newFixture(t)
		writeFile(t, f.file, `{"projects": {}}`, 0o600)
		prior := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeFlagOnly, Source: SourceReview}

		out := Ensure(f.env, f.root, prior, SourceWorkspace)
		want := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceReview}
		if out.Warning != "" || !out.Changed || !out.Created || !reflect.DeepEqual(out.Grant, want) {
			t.Fatalf("outcome = %+v (grant %+v), want %+v, changed and created", out, out.Grant, want)
		}
	})
}

// TestEnsureRecordsTheAskingSource checks a first grant carries the source of
// the feature that asked for it, review as well as workspace.
func TestEnsureRecordsTheAskingSource(t *testing.T) {
	for _, src := range []Source{SourceWorkspace, SourceReview} {
		t.Run(string(src), func(t *testing.T) {
			f := newFixture(t)
			out := Ensure(f.env, f.root, nil, src)
			want := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: src}
			if out.Warning != "" || !reflect.DeepEqual(out.Grant, want) {
				t.Errorf("outcome = %+v (grant %+v), want %+v", out, out.Grant, want)
			}
		})
	}
}

// TestEnsureReportsWhatItCreated checks Outcome.Created against each write
// Ensure can make, since a caller undoes the write by it.
func TestEnsureReportsWhatItCreated(t *testing.T) {
	t.Run("new entry", func(t *testing.T) {
		f := newFixture(t)
		out := Ensure(f.env, f.root, nil, SourceWorkspace)
		if !out.Changed || !out.Created {
			t.Errorf("outcome = %+v, want changed and created", out)
		}
	})
	t.Run("flag set on an existing entry", func(t *testing.T) {
		f := newFixture(t)
		writeFile(t, f.file, `{"projects": {`+jsonKey(t, f.key)+`: {"x": 1}}}`, 0o600)
		out := Ensure(f.env, f.root, nil, SourceWorkspace)
		if !out.Changed || out.Created {
			t.Errorf("outcome = %+v, want changed and not created", out)
		}
	})
	t.Run("nothing written", func(t *testing.T) {
		f := newFixture(t)
		first := Ensure(f.env, f.root, nil, SourceWorkspace)
		out := Ensure(f.env, f.root, first.Grant, SourceWorkspace)
		if out.Changed || out.Created {
			t.Errorf("outcome = %+v, want neither changed nor created", out)
		}
	})
}

// TestSetLockWaitForTestRestores checks the cross-package hook leaves the
// package as it found it, recorded lock timeouts included.
func TestSetLockWaitForTestRestores(t *testing.T) {
	resetLockState(t)
	const path = "/some/.claude.json.lock"
	markLockTimedOut(path)
	restore := SetLockWaitForTest(0)
	if lockWait != 0 || lockTimedOutAt(path) {
		t.Fatalf("lockWait = %v, timed out %v; want 0 and the timeout forgotten", lockWait, lockTimedOutAt(path))
	}
	markLockTimedOut(path)
	restore()
	if lockWait != defaultLockWait || lockPoll != defaultLockPoll || lockTimedOutAt(path) {
		t.Errorf("after restore: wait %v poll %v timedOut %v", lockWait, lockPoll, lockTimedOutAt(path))
	}
}
