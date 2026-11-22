package mirror

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sshWrapper writes a stand-in for ssh that serves the remote locally and
// records each call in calls. git runs GIT_SSH_COMMAND as
// "<command> <host> <remote command>"; the wrapper runs the remote command
// (git-upload-pack '<path>') as "git upload-pack '<path>'" on this host, so a
// URL whose host resolves nowhere is reachable only through it.
func sshWrapper(t *testing.T) (command, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	script := filepath.Join(dir, "ssh-wrapper.sh")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> '" + calls + "'\n" +
		"for last; do :; done\n" +
		"case \"$last\" in\n" +
		"git-upload-pack\\ *) exec sh -c \"git ${last#git-}\" ;;\n" +
		"esac\n" +
		"exit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	return "'" + script + "'", calls
}

// sshOnlyHost is a host name under the reserved .invalid domain, so no ssh
// but the wrapper can reach it.
const sshOnlyHost = "niwa-mirror-test.invalid"

func TestNetworkEnv_ARemoteReachedOnlyThroughGIT_SSH_COMMANDIsMirrored(t *testing.T) {
	f := newFixture(t)
	command, calls := sshWrapper(t)
	t.Setenv("GIT_SSH_COMMAND", command)
	t.Setenv("GIT_SSH_VARIANT", "simple")
	repo := Repo{ID: "app", URL: sshOnlyHost + ":" + f.remote}

	res := Seed(context.Background(), f.seedOpts(), repo)
	if res.Fault != FaultMissing || res.Outcome != OutcomeRestored || res.Err != nil {
		t.Fatalf("seeding from a remote reached only through GIT_SSH_COMMAND = %+v, want missing/restored with no error", res)
	}
	data, err := os.ReadFile(calls)
	if err != nil || !strings.Contains(string(data), sshOnlyHost) {
		t.Fatalf("the seed never ran GIT_SSH_COMMAND (calls %q, %v)", data, err)
	}

	if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.mdir(), ".store")); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(calls, 0); err != nil {
		t.Fatal(err)
	}
	res = Seed(context.Background(), f.seedOpts(), repo)
	if res.Fault != FaultMissing || res.Outcome != OutcomeRestored || res.Err != nil {
		t.Fatalf("restoring from a remote reached only through GIT_SSH_COMMAND = %+v, want missing/restored with no error", res)
	}
	data, err = os.ReadFile(calls)
	if err != nil || !strings.Contains(string(data), sshOnlyHost) {
		t.Fatalf("the restore never ran GIT_SSH_COMMAND (calls %q, %v)", data, err)
	}
}
