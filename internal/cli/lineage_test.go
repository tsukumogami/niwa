package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The dispatch id minted when every random byte is 0xab: the version nibble
// and the variant bits are set over the stubbed bytes.
const constABDispatchID = "abababab-abab-4bab-abab-abababababab"

// storeFiles lists the lineage store's files, or nil when it has no store.
func storeFiles(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(workspace.LineageDir(root))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestDispatchLineage_WritesEntryBesideTheRecord(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	before := time.Now().UTC().Add(-time.Second)
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	rec := readRecord(t, f)
	stored, found, err := workspace.ReadLineageEntry(root, rec.DispatchID)
	if err != nil || !found {
		t.Fatalf("entry for %s: found %v, err %v", rec.DispatchID, found, err)
	}
	e := stored.Parsed.Entry
	written, perr := time.Parse(time.RFC3339, e.WrittenAt)
	if perr != nil || written.Before(before.Truncate(time.Second)) || !strings.HasSuffix(e.WrittenAt, "Z") {
		t.Errorf("written_at = %q (%v)", e.WrittenAt, perr)
	}
	want := lineage.Entry{
		Schema:         lineage.SchemaEntryV1,
		DispatchID:     rec.DispatchID,
		Instance:       filepath.Base(f.instancePath),
		Agent:          "claude",
		Origin:         "dispatch",
		WrittenAt:      e.WrittenAt,
		SessionID:      dispatchTestSessionID,
		ParentSession:  lineage.Link{Source: "none"},
		ParentDispatch: lineage.Link{Source: "none"},
		Principal:      lineage.Principal{Source: "none", Trust: "observed"},
	}
	if e != want {
		t.Errorf("entry = %+v\nwant    %+v", e, want)
	}
	if files := storeFiles(t, root); len(files) != 1 || files[0] != rec.DispatchID+".json" {
		t.Errorf("store files = %v", files)
	}
	info, _ := os.Stat(workspace.LineageDir(root))
	if info.Mode().Perm() != 0o700 {
		t.Errorf("store mode = %o", info.Mode().Perm())
	}
	info, _ = os.Stat(filepath.Join(workspace.LineageDir(root), rec.DispatchID+".json"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("entry mode = %o", info.Mode().Perm())
	}
	if f.destroyCalled != 0 {
		t.Errorf("a kept dispatch was rolled back")
	}
}

func TestDispatchLineage_FailedCreateRefusesAndRollsBack(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	prev := createLineageEntryFunc
	createLineageEntryFunc = func(string, lineage.Entry) error { return errors.New("no space left on device") }
	t.Cleanup(func() { createLineageEntryFunc = prev })
	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil || !strings.Contains(err.Error(), "writing the dispatch's lineage entry") {
		t.Fatalf("err = %v", err)
	}
	if f.destroyCalled != 1 || f.launchCalled != 0 {
		t.Errorf("destroyed %d, launched %d; want the rollback and no launch", f.destroyCalled, f.launchCalled)
	}
	if files := storeFiles(t, root); len(files) != 0 {
		t.Errorf("store files = %v", files)
	}
}

func TestDispatchLineage_ExistingEntryForTheMintedIDIsKept(t *testing.T) {
	if !authority.ValidDispatchID(constABDispatchID) {
		t.Fatalf("%s is not a dispatch id", constABDispatchID)
	}
	root, f := authorityWorkspace(t, "", "")
	stubDispatchRand(t, constByteReader(0xab))
	if err := os.Mkdir(workspace.LineageDir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace.LineageDir(root), constABDispatchID+".json")
	existing := []byte(`{"schema":"niwa.lineage.entry/v1","dispatch_id":"` + constABDispatchID + `","instance":"earlier"}` + "\n")
	if err := os.WriteFile(path, existing, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil || !errors.Is(err, workspace.ErrLineageEntryExists) {
		t.Fatalf("err = %v, want the existing entry refused", err)
	}
	if rec := readRecord(t, f); rec.DispatchID != constABDispatchID {
		t.Fatalf("the stubbed id was not minted: %s", rec.DispatchID)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, existing) {
		t.Errorf("existing entry changed or removed (err %v):\n%s", err, after)
	}
	if f.destroyCalled != 1 || f.launchCalled != 0 {
		t.Errorf("destroyed %d, launched %d", f.destroyCalled, f.launchCalled)
	}
}

func TestDispatchLineage_RefusedAtAuthorityResolutionLeavesNoEntry(t *testing.T) {
	root, f := authorityWorkspace(t, "[authority]\nceiling = [\"merge-pr\"]\n", "")
	dispatchGrants = []string{"close-issue"}
	if _, _, err := runDispatchCmd(t, "do a thing"); err == nil {
		t.Fatal("the dispatch was not refused")
	}
	if f.provisionCalled != 0 {
		t.Fatalf("provisioned %d times", f.provisionCalled)
	}
	if files := storeFiles(t, root); files != nil {
		t.Errorf("store exists with %v", files)
	}
}

func TestDispatchLineage_FailedProvisioningLeavesNoEntry(t *testing.T) {
	root, _ := authorityWorkspace(t, "", "")
	provisionInstanceFunc = func(context.Context, string, string, string, string, int) (provisionResult, error) {
		return provisionResult{}, errors.New("clone failed")
	}
	if _, _, err := runDispatchCmd(t, "do a thing"); err == nil {
		t.Fatal("the dispatch succeeded")
	}
	if files := storeFiles(t, root); files != nil {
		t.Errorf("store exists with %v", files)
	}
}

func TestDispatchLineage_RollbackAfterTheWriteRemovesTheEntry(t *testing.T) {
	root, f := authorityWorkspace(t, "", "")
	dispatchLaunch = func(context.Context, launchRequest) error {
		f.launchCalled++
		if files := storeFiles(t, root); len(files) != 1 {
			t.Errorf("at launch the store holds %v, want the entry", files)
		}
		return errors.New("exec failed")
	}
	if _, _, err := runDispatchCmd(t, "do a thing"); err == nil {
		t.Fatal("the dispatch succeeded")
	}
	if f.destroyCalled != 1 {
		t.Errorf("destroyed %d", f.destroyCalled)
	}
	if files := storeFiles(t, root); len(files) != 0 {
		t.Errorf("store files after rollback = %v", files)
	}
}

// runLineageShowCmd runs `lineage show` with args and the --json flag as
// given, from the working directory.
func runLineageShowCmd(t *testing.T, asJSON bool, args ...string) (stdout string, err error) {
	t.Helper()
	prev := lineageShowJSON
	lineageShowJSON = asJSON
	t.Cleanup(func() { lineageShowJSON = prev })
	var out bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := lineageShowCmd.Args(cmd, args); err != nil {
		return "", err
	}
	err = runLineageShow(cmd, args)
	return out.String(), err
}

func lineageShowWorkspace(t *testing.T) string {
	t.Helper()
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	none := &dispatchAuthority{
		parentSession:  lineage.Link{Source: lineage.SourceNone},
		parentDispatch: lineage.Link{Source: lineage.SourceNone},
		principal:      lineage.Principal{Source: lineage.SourceNone, Trust: lineage.TrustObserved},
	}
	e := dispatchLineageEntry(constABDispatchID, "test-ws+task-abababab", "claude", time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), none)
	if err := workspace.CreateLineageEntry(root, e); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLineageShow_Text(t *testing.T) {
	lineageShowWorkspace(t)
	out, err := runLineageShowCmd(t, false, constABDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	want := "dispatch " + constABDispatchID + "\n" +
		"  instance:        test-ws+task-abababab\n" +
		"  agent:           claude\n" +
		"  origin:          dispatch\n" +
		"  written at:      2026-10-23T12:00:00Z\n" +
		"  session:         none\n" +
		"  parent session:  none (source none)\n" +
		"  parent dispatch: none (source none)\n" +
		"  principal:       none (source none, trust observed)\n"
	if out != want {
		t.Errorf("show =\n%s\nwant\n%s", out, want)
	}
}

func TestLineageShow_JSON(t *testing.T) {
	lineageShowWorkspace(t)
	out, err := runLineageShowCmd(t, true, constABDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "schema": "niwa.lineage.chain/v1",
  "chain": [
    {
      "schema": "niwa.lineage.entry/v1",
      "dispatch_id": "abababab-abab-4bab-abab-abababababab",
      "instance": "test-ws+task-abababab",
      "agent": "claude",
      "origin": "dispatch",
      "written_at": "2026-10-23T12:00:00Z",
      "parent_session": {
        "id": "",
        "source": "none"
      },
      "parent_dispatch": {
        "id": "",
        "source": "none"
      },
      "principal": {
        "name": "",
        "source": "none",
        "trust": "observed"
      }
    }
  ],
  "stop": "root",
  "matched": 1,
  "matches": [
    "abababab-abab-4bab-abab-abababababab"
  ],
  "instance_name_fallback": false
}
`
	if out != want {
		t.Errorf("show --json =\n%s\nwant\n%s", out, want)
	}
}

func TestLineageShow_KeepsUnknownMembersInJSON(t *testing.T) {
	root := lineageShowWorkspace(t)
	path := filepath.Join(workspace.LineageDir(root), constABDispatchID+".json")
	data, _ := os.ReadFile(path)
	data = append(bytes.TrimSuffix(bytes.TrimSpace(data), []byte("}")), []byte(`,"future":"<kept>"}`)...)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runLineageShowCmd(t, true, constABDispatchID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"future": "<kept>"`) {
		t.Errorf("unknown member not printed as stored:\n%s", out)
	}
}

func TestLineageShow_LaterVersion(t *testing.T) {
	root := lineageShowWorkspace(t)
	path := filepath.Join(workspace.LineageDir(root), constABDispatchID+".json")
	body := `{"schema":"niwa.lineage.entry/v2","dispatch_id":"` + constABDispatchID + `","secret_field":"x"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runLineageShowCmd(t, false, constABDispatchID)
	if err != nil || out != constABDispatchID+" later version niwa.lineage.entry/v2\n" {
		t.Errorf("text: %q, %v", out, err)
	}
	out, err = runLineageShowCmd(t, true, constABDispatchID)
	if err != nil || !strings.Contains(out, `"known": false`) || strings.Contains(out, "secret_field") || !strings.Contains(out, `"stop": "later-version"`) {
		t.Errorf("json: %s, %v", out, err)
	}
}

func TestLineageShow_Failures(t *testing.T) {
	t.Run("no entry", func(t *testing.T) {
		lineageShowWorkspace(t)
		_, err := runLineageShowCmd(t, false, "1b9a3c1e-5f2d-4a7b-8c6e-1d2f3a4b5c6d")
		if exitCode(t, err) != 1 || !strings.Contains(err.Error(), "no lineage entry") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no store", func(t *testing.T) {
		chdir(t, setupDispatchWorkspace(t))
		_, err := runLineageShowCmd(t, false, constABDispatchID)
		if exitCode(t, err) != 1 || !strings.Contains(err.Error(), "no lineage entry") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("not a dispatch id", func(t *testing.T) {
		lineageShowWorkspace(t)
		_, err := runLineageShowCmd(t, false, "../../etc/passwd")
		if exitCode(t, err) != 1 || !strings.Contains(err.Error(), "no lineage entry") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("unreadable", func(t *testing.T) {
		root := lineageShowWorkspace(t)
		path := filepath.Join(workspace.LineageDir(root), constABDispatchID+".json")
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := runLineageShowCmd(t, true, constABDispatchID)
		if exitCode(t, err) != 1 || !strings.Contains(out, `"stop": "unreadable"`) || !strings.Contains(out, `"error": "lineage entry is malformed"`) {
			t.Errorf("out %s, err %v", out, err)
		}
	})
	t.Run("outside a workspace", func(t *testing.T) {
		chdir(t, canonicalTempDir(t))
		_, err := runLineageShowCmd(t, false, constABDispatchID)
		if exitCode(t, err) != 1 || !strings.Contains(err.Error(), "not inside a niwa workspace") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("usage", func(t *testing.T) {
		lineageShowWorkspace(t)
		for _, args := range [][]string{nil, {constABDispatchID, constABDispatchID}} {
			if _, err := runLineageShowCmd(t, false, args...); exitCode(t, err) != 2 {
				t.Errorf("args %v: err = %v, want exit 2", args, err)
			}
		}
	})
}
