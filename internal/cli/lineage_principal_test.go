package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func TestDispatchLineage_InvalidPrincipalRefusesAtTheMachineCheck(t *testing.T) {
	root, f := authorityWorkspace(t, "", "[global]\nprincipal = \"Dana Smith\"\n")
	_, _, err := runDispatchCmd(t, "do a thing")
	if err == nil || !strings.Contains(err.Error(), "invalid [global] principal") || !strings.Contains(err.Error(), "config.toml") {
		t.Fatalf("err = %v, want a refusal naming the key and the file", err)
	}
	if f.provisionCalled != 0 {
		t.Errorf("provisioned %d times", f.provisionCalled)
	}
	if files := storeFiles(t, root); files != nil {
		t.Errorf("store exists with %v", files)
	}
}

// TestDispatchLineage_DeclaredPrincipalIsTheOnlySource declares a principal
// with a GitHub token set and a gh on PATH that would report another login:
// the entry names the declared value, gh is never run, and no part of the
// worker's environment carries the name.
func TestDispatchLineage_DeclaredPrincipalIsTheOnlySource(t *testing.T) {
	const declared = "dana@example.com"
	root, f := authorityWorkspace(t, "", "[global]\nprincipal = \""+declared+"\"\n")
	bin := t.TempDir()
	ran := filepath.Join(bin, "gh-ran")
	gh := "#!/bin/sh\ntouch " + ran + "\necho mallory\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GH_TOKEN", "stub-token-for-the-test")
	var env []string
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		f.launchCalled++
		env = req.Env
		return nil
	}
	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	got := dispatchedEntry(t, root, f).Parsed.Entry.Principal
	want := lineage.Principal{Name: declared, Source: "declared", Trust: "observed"}
	if got != want {
		t.Errorf("principal = %+v, want %+v", got, want)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("dispatch ran gh")
	}
	sawOTEL := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "OTEL_RESOURCE_ATTRIBUTES=") {
			sawOTEL = true
		}
		if strings.Contains(kv, declared) {
			t.Errorf("worker environment carries the principal: %s", kv)
		}
	}
	if !sawOTEL {
		t.Error("the worker got no OTEL_RESOURCE_ATTRIBUTES, so the check above proves nothing")
	}
}

func TestDispatchLineage_PrincipalOutsideTheMachineConfigHasNoEffect(t *testing.T) {
	// The body lands in the [workspace] table; a [global] table is tried too.
	for name, body := range map[string]string{
		"workspace table": "principal = \"mallory\"\n",
		"global table":    "[global]\nprincipal = \"mallory\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			root, f := authorityWorkspace(t, body, "")
			conventionOverlay(t, "test-ws", "principal = \"mallory\"\n[global]\nprincipal = \"mallory\"\n", "")
			if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v\n%s", err, stderr)
			}
			got := dispatchedEntry(t, root, f).Parsed.Entry.Principal
			if got != (lineage.Principal{Source: "none", Trust: "observed"}) {
				t.Errorf("principal = %+v, want none", got)
			}
		})
	}
}

// TestDispatchLineage_InheritsTheParentsPrincipal dispatches from the
// parent's instance, whose own entry names a principal, with the machine
// declaring another.
func TestDispatchLineage_InheritsTheParentsPrincipal(t *testing.T) {
	const sess = "c0c0c0c0-1111-4111-8111-c0c0c0c0c0c0"
	cases := []struct {
		name        string
		anchor      bool
		parentTrust string
		noEntry     bool
		want        lineage.Principal
	}{
		{name: "from inside, observed", parentTrust: "observed",
			want: lineage.Principal{Name: "dana", Source: "inherited", Trust: "observed"}},
		{name: "through --parent-from", anchor: true, parentTrust: "observed",
			want: lineage.Principal{Name: "dana", Source: "inherited", Trust: "handed"}},
		{name: "a handed parent principal", parentTrust: "handed",
			want: lineage.Principal{Name: "dana", Source: "inherited", Trust: "handed"}},
		{name: "the parent has no entry", noEntry: true,
			want: lineage.Principal{Name: "local-person", Source: "declared", Trust: "handed"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, f := authorityWorkspace(t, "", "[global]\nprincipal = \"local-person\"\n")
			parent := installedInstance(t, root, "test-ws-parent", parentRecord(t))
			if !tc.noEntry {
				if err := workspace.CreateLineageEntry(root, lineage.Entry{
					DispatchID: parentDispatchID, Instance: "test-ws-parent", Agent: "claude", Origin: "dispatch",
					WrittenAt:      time.Now().UTC().Format(time.RFC3339),
					ParentSession:  lineage.Link{Source: "none"},
					ParentDispatch: lineage.Link{Source: "none"},
					Principal:      lineage.Principal{Name: "dana", Source: "declared", Trust: tc.parentTrust},
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
				SessionID: sess, InstanceName: "test-ws-parent", InstancePath: parent,
				DispatchID: parentDispatchID, Created: time.Now().UTC(), Origin: "dispatch",
			}); err != nil {
				t.Fatal(err)
			}
			t.Setenv("CLAUDE_CODE_SESSION_ID", sess)
			if tc.anchor {
				dispatchParentFrom, dispatchParentFromSet = parent, true
			} else {
				chdir(t, parent)
			}
			if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v\n%s", err, stderr)
			}
			if got := dispatchedEntry(t, root, f).Parsed.Entry.Principal; got != tc.want {
				t.Errorf("principal = %+v, want %+v", got, tc.want)
			}
		})
	}
}
