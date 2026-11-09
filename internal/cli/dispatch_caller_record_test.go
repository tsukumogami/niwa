package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// envOf is a getenv reading only the given values.
func envOf(vals map[string]string) func(string) string {
	return func(name string) string { return vals[name] }
}

func TestReadCallerSession(t *testing.T) {
	spec := claudeLaunchSpec()
	l := spec.Lineage
	recordDir := callerRecordDir(t, callerSessionID, "/somewhere")
	const varID = "a2a2a2a2-2222-4222-8222-a2a2a2a2a2a2"
	cases := []struct {
		name         string
		env          map[string]string
		wantID       string
		wantStripped bool
	}{
		{"plain terminal", map[string]string{}, "", false},
		{"variable set, no markers", map[string]string{l.ParentSessionEnv: varID}, varID, false},
		{"variable set with markers", map[string]string{l.ParentSessionEnv: varID, l.SessionMarkerEnv[0]: "1"}, varID, false},
		{"variable unset, record names the session", map[string]string{l.SessionRecordEnv: recordDir, l.SessionMarkerEnv[0]: "1"}, callerSessionID, false},
		{"variable wins over the record", map[string]string{l.ParentSessionEnv: varID, l.SessionRecordEnv: recordDir}, varID, false},
		{"marker only", map[string]string{l.SessionMarkerEnv[1]: "cli"}, "", true},
		{"record directory with no record", map[string]string{l.SessionRecordEnv: t.TempDir()}, "", true},
		{"malformed variable, marker set", map[string]string{l.ParentSessionEnv: "not a session", l.SessionMarkerEnv[0]: "1"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := readCallerSession(spec, envOf(tc.env))
			if c.ID() != tc.wantID || c.Stripped() != tc.wantStripped {
				t.Errorf("caller = %+v: ID %q stripped %v, want ID %q stripped %v", c, c.ID(), c.Stripped(), tc.wantID, tc.wantStripped)
			}
		})
	}
}

func TestReadCallerSession_StrippedReasonNamesTheVariables(t *testing.T) {
	spec := claudeLaunchSpec()
	c := readCallerSession(spec, envOf(map[string]string{spec.Lineage.SessionMarkerEnv[0]: "1"}))
	reason := c.strippedReason()
	if !strings.Contains(reason, spec.Lineage.SessionMarkerEnv[0]) || !strings.Contains(reason, spec.Lineage.ParentSessionEnv) {
		t.Errorf("reason = %q, want it to name the marker and the session variable", reason)
	}
}

// TestDispatchCallerSession_UnmappedVariableStillCappedByRecord: replacing
// the session variable with an id no mapping names doesn't escape the
// dispatcher the session's own record leads to.
func TestDispatchCallerSession_UnmappedVariableStillCappedByRecord(t *testing.T) {
	resetParentFrom(t)
	root, _ := authorityWorkspace(t, r8aExample, "")
	inst := grantlessDispatchedInstance(t, root)
	agentSessionEnv(t, callerRecordDir(t, callerSessionID, inst))
	t.Setenv("CLAUDE_CODE_SESSION_ID", "b3b3b3b3-3333-4333-8333-b3b3b3b3b3b3")
	dispatchRoles = []string{"coordinator"}
	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatalf("a grantless session with an unmapped variable dispatched --role coordinator; want the never-wider refusal\n%s", stderr)
	}
	if !strings.Contains(err.Error(), "wider than the dispatcher's grant") || !strings.Contains(err.Error(), parentDispatchID) {
		t.Errorf("refusal = %v, want the never-wider refusal naming dispatch %s", err, parentDispatchID)
	}
}

// TestReadCallerRecordID_OnlyASafeRecordIsRead: the record directory comes
// from the environment, so a record reached any unsafe way names no session.
func TestReadCallerRecordID_OnlyASafeRecordIsRead(t *testing.T) {
	r := claudeLaunchSpec().Records
	good := callerRecordDir(t, callerSessionID, "/somewhere")
	if got := readCallerRecordID(good, r); got != callerSessionID {
		t.Fatalf("a plain record read %q, want %q", got, callerSessionID)
	}

	t.Run("relative directory", func(t *testing.T) {
		t.Chdir(filepath.Dir(good))
		if got := readCallerRecordID(filepath.Base(good), r); got != "" {
			t.Errorf("read %q through a relative directory", got)
		}
	})
	t.Run("directory is a symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "job")
		if err := os.Symlink(good, link); err != nil {
			t.Fatal(err)
		}
		if got := readCallerRecordID(link, r); got != "" {
			t.Errorf("read %q through a symlinked directory", got)
		}
	})
	t.Run("record is a symlink", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Symlink(filepath.Join(good, r.FileName), filepath.Join(dir, r.FileName)); err != nil {
			t.Fatal(err)
		}
		if got := readCallerRecordID(dir, r); got != "" {
			t.Errorf("read %q through a symlinked record", got)
		}
	})
	t.Run("record is a directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, r.FileName), 0o700); err != nil {
			t.Fatal(err)
		}
		if got := readCallerRecordID(dir, r); got != "" {
			t.Errorf("read %q from a directory", got)
		}
	})
	t.Run("record too large", func(t *testing.T) {
		dir := t.TempDir()
		big := `{"pad":"` + strings.Repeat("x", maxCallerRecordBytes) + `"}`
		if err := os.WriteFile(filepath.Join(dir, r.FileName), []byte(big), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := readCallerRecordID(dir, r); got != "" {
			t.Errorf("read %q from an oversized record", got)
		}
	})
	t.Run("record not JSON", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, r.FileName), []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := readCallerRecordID(dir, r); got != "" {
			t.Errorf("read %q from a malformed record", got)
		}
	})
}
