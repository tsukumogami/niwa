package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// A session niwa dispatched without a grant can run `niwa dispatch` from the
// workspace root with its session variable unset. These tests pin what that
// dispatch must not be able to do: get a child wider than its own grant, or
// write a record that reads like an operator's own plain-terminal dispatch.

// callerSessionID is the session a dispatched caller's own record names.
const callerSessionID = "e1e1e1e1-1111-4111-8111-e1e1e1e1e1e1"

// agentSessionEnv sets the environment an agent session's commands run in,
// with the session variable unset: the markers on, and the record directory
// at recordDir ("" for none).
func agentSessionEnv(t *testing.T, recordDir string) {
	t.Helper()
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("CLAUDE_JOB_DIR", recordDir)
}

// plainTerminalEnv clears every variable an agent session sets, the way an
// operator's own terminal has none of them.
func plainTerminalEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_JOB_DIR"} {
		t.Setenv(name, "")
	}
}

// callerRecordDir writes the session record a running session's record
// directory holds, naming sessionID started in cwd, laid out as the launch
// spec's Records declare it, and returns the directory.
func callerRecordDir(t *testing.T, sessionID, cwd string) string {
	t.Helper()
	r := claudeLaunchSpec().Records
	doc := map[string]any{}
	setAtPath(doc, r.IDPath, sessionID)
	setAtPath(doc, r.CwdPath, cwd)
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, r.FileName), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// grantlessDispatchedInstance installs an instance dispatch made, holding no
// grant, with the dispatch-written mapping for callerSessionID, and returns
// its path.
func grantlessDispatchedInstance(t *testing.T, root string) string {
	t.Helper()
	inst := installedInstance(t, root, "test-ws-caller", parentRecord(t))
	checkMapping(t, root, callerSessionID, inst, workspace.MappingOriginDispatch, parentDispatchID)
	return inst
}

// TestDispatchCallerSession_RecordCapsTheChild: at the root with the session
// variable unset, the caller's own session record still names the session,
// so the dispatch it was launched by caps the child.
func TestDispatchCallerSession_RecordCapsTheChild(t *testing.T) {
	resetParentFrom(t)
	root, _ := authorityWorkspace(t, r8aExample, "")
	inst := grantlessDispatchedInstance(t, root)
	agentSessionEnv(t, callerRecordDir(t, callerSessionID, inst))
	dispatchRoles = []string{"coordinator"}
	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatalf("a grantless session with its variable unset dispatched --role coordinator from the root; want the never-wider refusal\n%s", stderr)
	}
	if !strings.Contains(err.Error(), "wider than the dispatcher's grant") || !strings.Contains(err.Error(), parentDispatchID) {
		t.Errorf("refusal = %v, want the never-wider refusal naming dispatch %s", err, parentDispatchID)
	}
}

// TestDispatchCallerSession_StrippedRefusesNamedAuthority: the caller looks
// like an agent session but no session resolves, so it is treated as holding
// nothing, and a role it asks for is refused.
func TestDispatchCallerSession_StrippedRefusesNamedAuthority(t *testing.T) {
	resetParentFrom(t)
	authorityWorkspace(t, r8aExample, "")
	agentSessionEnv(t, "")
	dispatchRoles = []string{"coordinator"}
	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatalf("an agent session with no resolvable session dispatched --role coordinator from the root; want a refusal saying its parent session is stripped\n%s", stderr)
	}
	if !strings.Contains(err.Error(), "wider than the dispatcher's grant") {
		t.Errorf("refusal = %v, want the never-wider refusal", err)
	}
}

// TestDispatchCallerSession_StrippedIsRecorded: the same caller asking for no
// named authority still dispatches, but its record says the parent session
// was stripped rather than reading like an operator's.
func TestDispatchCallerSession_StrippedIsRecorded(t *testing.T) {
	resetParentFrom(t)
	_, f := authorityWorkspace(t, r8aExample, "")
	agentSessionEnv(t, "")
	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	if rec := readRecord(t, f); rec.ParentSessionSource != "stripped" {
		t.Errorf("parent_session_source = %q, want %q", rec.ParentSessionSource, "stripped")
	}
	if !strings.Contains(stderr, "stripped") {
		t.Errorf("no warning that the parent session is recorded as stripped:\n%s", stderr)
	}
}

// TestDispatchCallerSession_EmptyPrincipalTrustNone: an operator's own
// plain-terminal root dispatch with no principal declared observed nobody,
// so the record's principal trust is none.
func TestDispatchCallerSession_EmptyPrincipalTrustNone(t *testing.T) {
	resetParentFrom(t)
	_, f := authorityWorkspace(t, r8aExample, "")
	plainTerminalEnv(t)
	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	rec := readRecord(t, f)
	if rec.Principal == nil || rec.Principal.Name != "" || rec.Principal.Trust != "none" {
		t.Errorf("principal = %+v, want no name with trust none", rec.Principal)
	}
	if rec.ParentSessionSource != "none" || rec.ParentDispatchSource != "none" {
		t.Errorf("an operator's plain-terminal dispatch changed its links: session %q dispatch %q", rec.ParentSessionSource, rec.ParentDispatchSource)
	}
}
