package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/mirror"
)

// fakeDissociate replaces the engine until the test ends: it records the
// options each call gets and answers with rep and err.
func fakeDissociate(t *testing.T, rep mirror.DissociateReport, err error) *[]mirror.DissociateOptions {
	t.Helper()
	var calls []mirror.DissociateOptions
	orig := dissociateFunc
	dissociateFunc = func(_ context.Context, opts mirror.DissociateOptions) (mirror.DissociateReport, error) {
		calls = append(calls, opts)
		return rep, err
	}
	t.Cleanup(func() { dissociateFunc = orig })
	return &calls
}

// runDissociateCmd runs the command with args, returning its stdout, stderr
// and error.
func runDissociateCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err := runDissociate(cmd, args)
	return out.String(), errOut.String(), err
}

func TestDissociateCmd_WiredIntoRoot(t *testing.T) {
	for _, c := range rootCmd.Commands() {
		if c.Name() == "dissociate" {
			if err := c.Args(c, []string{"a", "b"}); err == nil {
				t.Error("dissociate takes at most one instance")
			}
			return
		}
	}
	t.Fatal("expected 'dissociate' command to be wired into rootCmd")
}

func TestDissociate_NoArgumentActsOnTheEnclosingInstance(t *testing.T) {
	home := lockTestHome(t)
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	inst := filepath.Join(root, "beta")
	chdirTo(t, filepath.Join(inst, ".niwa"))
	calls := fakeDissociate(t, mirror.DissociateReport{Changed: []string{"tools/app"}}, nil)

	out, _, err := runDissociateCmd(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("engine called %d times, want once", len(*calls))
	}
	got := (*calls)[0]
	if got.Instance != inst || got.WorkspaceRoot != root || got.Home != home {
		t.Fatalf("engine got %+v, want instance %s, root %s, home %s", got, inst, root, home)
	}
	if out != "dissociated tools/app\n" {
		t.Fatalf("output = %q", out)
	}
}

func TestDissociate_ANameFromTheRootResolvesThatInstance(t *testing.T) {
	lockTestHome(t)
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	chdirTo(t, root)
	calls := fakeDissociate(t, mirror.DissociateReport{}, nil)

	out, _, err := runDissociateCmd(t, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0].Instance != filepath.Join(root, "alpha") {
		t.Fatalf("engine calls = %+v, want one for alpha", *calls)
	}
	if out != "nothing to change\n" {
		t.Fatalf("output = %q, want nothing to change", out)
	}
}

func TestDissociate_AnEmptyNameChangesNothing(t *testing.T) {
	lockTestHome(t)
	root := destroyTestSetup(t, []string{"alpha"})
	chdirTo(t, root)
	calls := fakeDissociate(t, mirror.DissociateReport{}, nil)
	if _, _, err := runDissociateCmd(t, " "); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if len(*calls) != 0 {
		t.Fatal("the engine ran for an empty name")
	}
}

func TestDissociate_AHeldInstanceLockRefusesWithNothingChanged(t *testing.T) {
	home := lockTestHome(t)
	root := destroyTestSetup(t, []string{"alpha"})
	inst := filepath.Join(root, "alpha")
	holdRealInstanceLock(t, home, inst, lockTestHolder)
	chdirTo(t, root)
	calls := fakeDissociate(t, mirror.DissociateReport{}, nil)

	start := time.Now()
	_, _, err := runDissociateCmd(t, "alpha")
	if err == nil {
		t.Fatal("dissociate ran under another command's lock")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the refusal waited %s for the lock", time.Since(start))
	}
	for _, want := range []string{"nothing was changed", "alpha", "lock is held", lockTestHolder} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want it to name %q", err, want)
		}
	}
	if len(*calls) != 0 {
		t.Fatal("the engine ran under another command's lock")
	}
}

func TestDissociate_OutputListsWhatChangedEvenWhenItStops(t *testing.T) {
	lockTestHome(t)
	root := destroyTestSetup(t, []string{"alpha"})
	chdirTo(t, filepath.Join(root, "alpha"))
	fakeDissociate(t, mirror.DissociateReport{
		Changed:  []string{"tools/app"},
		Restored: []string{"tools/docs"},
		Outside:  []mirror.OutsideClone{{Clone: "tools/ext", Line: "/elsewhere/objects"}},
	}, errors.New("git fsck in tools/lib failed"))

	out, errOut, err := runDissociateCmd(t)
	if err == nil || !strings.Contains(err.Error(), "tools/lib") {
		t.Fatalf("err = %v, want the engine's error", err)
	}
	if out != "dissociated tools/app\n" {
		t.Fatalf("output = %q", out)
	}
	for _, want := range []string{
		"warning: tools/ext borrows from /elsewhere/objects, outside this workspace's mirrors; left as it is",
		"put back the alternates file of tools/docs",
	} {
		if !strings.Contains(errOut, want) {
			t.Fatalf("error output %q lacks %q", errOut, want)
		}
	}
}

func TestDissociate_OutsideClonesAloneDontFailTheCommand(t *testing.T) {
	lockTestHome(t)
	root := destroyTestSetup(t, []string{"alpha"})
	chdirTo(t, filepath.Join(root, "alpha"))
	fakeDissociate(t, mirror.DissociateReport{
		Outside: []mirror.OutsideClone{{Clone: "tools/ext", Line: "/elsewhere/objects"}},
	}, nil)
	out, errOut, err := runDissociateCmd(t)
	if err != nil {
		t.Fatal(err)
	}
	if out != "nothing to change\n" || !strings.Contains(errOut, "tools/ext") {
		t.Fatalf("output = %q, error output = %q", out, errOut)
	}
}
