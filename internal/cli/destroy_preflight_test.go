package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// ---- destroy's preflight ----
//
// These run `niwa destroy` against a scratch workspace under t.TempDir(), with
// Claude Code's session listing faked and the pre-removal command a neutral
// shell one-liner that logs each run to a scratch file.

// mapNamedSession maps a Claude session with a session name to instanceDir.
func mapNamedSession(t *testing.T, root, sid, instanceDir, name string) {
	t.Helper()
	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    sid,
		InstanceName: filepath.Base(instanceDir),
		InstancePath: instanceDir,
		Created:      time.Now(),
		Ephemeral:    true,
		Agent:        "claude",
		SessionName:  name,
	}); err != nil {
		t.Fatal(err)
	}
}

// declareLoggingGate declares a [session.before_remove] command that appends
// "<step> <path>" to a scratch log on each run, then prints "kept by test"
// and exits 1 for a path ending in keepSuffix and exits 0 for any other. It
// returns the log's path.
func declareLoggingGate(t *testing.T, root, keepSuffix string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "gate.log")
	cmd := fmt.Sprintf(`echo "$NIWA_REMOVE_STEP $NIWA_REMOVE_PATH" >> '%s'; case "$NIWA_REMOVE_PATH" in *%s) echo 'kept by test'; exit 1;; esac; exit 0`, log, keepSuffix)
	appendBeforeRemoveConfig(t, root, fmt.Sprintf("command = %q", cmd))
	return log
}

func gateRuns(t *testing.T, log string) []string {
	t.Helper()
	data, err := os.ReadFile(log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func runningRow(sid string) claudeSessionRow {
	return claudeSessionRow{ID: sid[:8], SessionID: sid, PID: 4242, Status: "busy"}
}

func TestDestroyPreflight_RunningSessionRefuses(t *testing.T) {
	t.Run("with a session name", func(t *testing.T) {
		root := destroyTestSetup(t, []string{"alpha", "beta"})
		instance := filepath.Join(root, "alpha")
		mapNamedSession(t, root, ownerSID, instance, "fix-thing")
		fake := fakeClaudeSessions{rows: []claudeSessionRow{runningRow(ownerSID)}}
		installFakeClaudeSessions(t, &fake)
		chdirTo(t, root)
		stderr := quietDestroyCmd()
		setDestroyFlags(t, false, false, "")

		err := runDestroy(destroyCmd, []string{"alpha"})
		if err == nil || !strings.Contains(err.Error(), "nothing was destroyed") {
			t.Fatalf("err = %v, want a refusal", err)
		}
		out := stderr.String()
		if !strings.Contains(out, "Claude session 11111111 (fix-thing) is still running") {
			t.Errorf("refusal should name the short id and session name:\n%s", out)
		}
		if strings.Contains(out, "Type ") || strings.Contains(out, "> ") {
			t.Errorf("a refused destroy prompted:\n%s", out)
		}
		if len(fake.removed) != 0 {
			t.Errorf("removed %v", fake.removed)
		}
		assertInstancesExist(t, root, "alpha", "beta")
	})
	t.Run("without a session name", func(t *testing.T) {
		root := destroyTestSetup(t, []string{"alpha"})
		instance := filepath.Join(root, "alpha")
		writeTestMapping(t, root, ownerSID, instance, "claude")
		fake := fakeClaudeSessions{rows: []claudeSessionRow{runningRow(ownerSID)}}
		installFakeClaudeSessions(t, &fake)
		chdirTo(t, root)
		stderr := quietDestroyCmd()
		setDestroyFlags(t, false, false, "")

		if err := runDestroy(destroyCmd, []string{"alpha"}); err == nil {
			t.Fatal("destroy succeeded")
		}
		if !strings.Contains(stderr.String(), "Claude session 11111111 is still running") {
			t.Errorf("refusal should name the short id alone:\n%s", stderr.String())
		}
		assertInstancesExist(t, root, "alpha")
	})
}

func TestDestroyPreflight_RunningSessionAndGateBothListed(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha"})
	instance := filepath.Join(root, "alpha")
	mapNamedSession(t, root, ownerSID, instance, "fix-thing")
	log := declareLoggingGate(t, root, "/alpha")
	fake := fakeClaudeSessions{rows: []claudeSessionRow{runningRow(ownerSID)}}
	installFakeClaudeSessions(t, &fake)
	chdirTo(t, root)
	stderr := quietDestroyCmd()
	setDestroyFlags(t, false, false, "")

	if err := runDestroy(destroyCmd, []string{"alpha"}); err == nil {
		t.Fatal("destroy succeeded")
	}
	out := stderr.String()
	for _, want := range []string{"Claude session 11111111 (fix-thing) is still running", `reason from before_remove: "kept by test"`} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal missing %q:\n%s", want, out)
		}
	}
	runs := gateRuns(t, log)
	if len(runs) != 1 || runs[0] != "destroy "+instance {
		t.Errorf("gate runs = %q, want one with step destroy", runs)
	}
	assertInstancesExist(t, root, "alpha")
}

func TestDestroyPreflight_ForceOverridesAndRemoves(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	instance := filepath.Join(root, "alpha")
	mapNamedSession(t, root, ownerSID, instance, "fix-thing")
	declareLoggingGate(t, root, "/alpha")
	fake := fakeClaudeSessions{rows: []claudeSessionRow{runningRow(ownerSID)}}
	installFakeClaudeSessions(t, &fake)
	chdirTo(t, root)
	stderr := quietDestroyCmd()
	setDestroyFlags(t, true, false, "")

	if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
		t.Fatalf("destroy --force: %v", err)
	}
	out := stderr.String()
	for _, want := range []string{"--force overrides what still needs alpha", "Claude session 11111111 (fix-thing) is still running", `reason from before_remove: "kept by test"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if fake.listCalls != 1 {
		t.Errorf("listed %d times, want once", fake.listCalls)
	}
	if _, err := os.Stat(instance); !os.IsNotExist(err) {
		t.Errorf("instance should be gone: %v", err)
	}
}

func TestDestroyPreflight_WorkspaceRefusesAsAWhole(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta", "gamma"})
	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")
	mapNamedSession(t, root, ownerSID, alpha, "fix-thing")
	log := declareLoggingGate(t, root, "/beta")
	fake := fakeClaudeSessions{rows: []claudeSessionRow{runningRow(ownerSID)}}
	installFakeClaudeSessions(t, &fake)
	chdirTo(t, root)
	stderr := quietDestroyCmd()
	setDestroyFlags(t, false, true, "testws")

	err := runDestroy(destroyCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "nothing was destroyed") {
		t.Fatalf("err = %v, want a refusal", err)
	}
	out := stderr.String()
	for _, want := range []string{"alpha is still needed", "Claude session 11111111 (fix-thing) is still running", "beta is still needed", `reason from before_remove: "kept by test"`} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "gamma is still needed") {
		t.Errorf("gamma named as kept:\n%s", out)
	}
	runs := gateRuns(t, log)
	if len(runs) != 3 {
		t.Errorf("gate runs = %q, want one per instance", runs)
	}
	for _, r := range runs {
		if !strings.HasPrefix(r, "destroy-workspace ") {
			t.Errorf("gate run %q, want step destroy-workspace", r)
		}
	}
	if !strings.Contains(strings.Join(runs, "\n"), beta) {
		t.Errorf("gate never asked about %s: %q", beta, runs)
	}
	assertInstancesExist(t, root, "alpha", "beta", "gamma")
	if _, err := os.Stat(filepath.Join(root, ".niwa", "workspace.toml")); err != nil {
		t.Errorf("workspace root should be intact: %v", err)
	}

	setDestroyFlags(t, true, true, "")
	if err := runDestroy(destroyCmd, nil); err == nil {
		t.Error("--workspace --force was accepted")
	}
}

func TestDestroyPreflight_ListingFailure(t *testing.T) {
	t.Run("refuses an instance with a mapped session", func(t *testing.T) {
		root := destroyTestSetup(t, []string{"alpha"})
		writeTestMapping(t, root, ownerSID, filepath.Join(root, "alpha"), "claude")
		fake := fakeClaudeSessions{listErr: errors.New("claude binary not found in PATH")}
		installFakeClaudeSessions(t, &fake)
		chdirTo(t, root)
		stderr := quietDestroyCmd()
		setDestroyFlags(t, false, false, "")

		if err := runDestroy(destroyCmd, []string{"alpha"}); err == nil {
			t.Fatal("destroy succeeded")
		}
		if !strings.Contains(stderr.String(), "could not list Claude Code's sessions") {
			t.Errorf("refusal should say the listing failed:\n%s", stderr.String())
		}
		assertInstancesExist(t, root, "alpha")
	})
	t.Run("force proceeds", func(t *testing.T) {
		root := destroyTestSetup(t, []string{"alpha", "beta"})
		instance := filepath.Join(root, "alpha")
		writeTestMapping(t, root, ownerSID, instance, "claude")
		fake := fakeClaudeSessions{listErr: errors.New("claude binary not found in PATH")}
		installFakeClaudeSessions(t, &fake)
		chdirTo(t, root)
		quietDestroyCmd()
		setDestroyFlags(t, true, false, "")

		if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
			t.Fatalf("destroy --force: %v", err)
		}
		if _, err := os.Stat(instance); !os.IsNotExist(err) {
			t.Errorf("instance should be gone: %v", err)
		}
	})
	t.Run("an unmapped instance never lists", func(t *testing.T) {
		root := destroyTestSetup(t, []string{"alpha", "beta"})
		instance := filepath.Join(root, "alpha")
		fake := fakeClaudeSessions{listErr: errors.New("claude binary not found in PATH")}
		installFakeClaudeSessions(t, &fake)
		chdirTo(t, root)
		quietDestroyCmd()
		setDestroyFlags(t, false, false, "")

		if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
			t.Fatalf("destroy: %v", err)
		}
		if fake.listCalls != 0 {
			t.Errorf("listed %d times for an unmapped instance", fake.listCalls)
		}
		if _, err := os.Stat(instance); !os.IsNotExist(err) {
			t.Errorf("instance should be gone: %v", err)
		}
	})
}

func TestDestroyPreflight_NothingNeedsItBehavesAsBefore(t *testing.T) {
	root := destroyTestSetup(t, []string{"alpha", "beta"})
	instance := filepath.Join(root, "alpha")
	writeTestMapping(t, root, ownerSID, instance, "claude")
	fake := fakeClaudeSessions{rows: []claudeSessionRow{{ID: "11111111", SessionID: ownerSID}}}
	installFakeClaudeSessions(t, &fake)

	log := filepath.Join(t.TempDir(), "shell.log")
	stub := filepath.Join(t.TempDir(), "sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho run >> '"+log+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := onReapShell
	onReapShell = stub
	t.Cleanup(func() { onReapShell = prev })

	chdirTo(t, root)
	stderr := quietDestroyCmd()
	setDestroyFlags(t, false, false, "")

	if err := runDestroy(destroyCmd, []string{"alpha"}); err != nil {
		t.Fatalf("destroy: %v\n%s", err, stderr.String())
	}
	if _, err := os.Stat(instance); !os.IsNotExist(err) {
		t.Errorf("instance should be gone: %v", err)
	}
	if strings.Contains(stderr.String(), "still needed") || strings.Contains(stderr.String(), "overrides") {
		t.Errorf("preflight reported with nothing to report:\n%s", stderr.String())
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Error("a shell started for the gate with nothing declared")
	}
	if strings.Join(fake.removed, ",") != "11111111" || fake.listCalls != 1 {
		t.Errorf("removed %v after %d listings, want the finished session removed after one", fake.removed, fake.listCalls)
	}
}
