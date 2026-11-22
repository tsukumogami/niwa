package mirror

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

// sshSpelling, userSpelling and httpsSpelling are three spellings of one
// remote: the scp-like SSH form, the same with another user, and HTTPS.
const (
	sshSpelling   = "git@mirror.test:o/app.git"
	userSpelling  = "deploy@mirror.test:o/app.git"
	httpsSpelling = "https://mirror.test/o/app.git"
)

// writeOldStateURL rewrites the fixture's state file in the format v0.43.0
// writes, with url set to the recorded form of raw and no identity field.
func writeOldStateURL(t *testing.T, f fixture, raw string) {
	t.Helper()
	data, err := os.ReadFile(statePath(f.mdir(), "app"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["url"] = recordURL(raw)
	delete(fields, "identity")
	if data, err = json.Marshal(fields); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(f.mdir(), "app"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// networkProcs are the git processes among procs that may reach a remote:
// the ones run without the local-read environment.
func networkProcs(procs []gitProc) []gitProc {
	var out []gitProc
	for _, p := range procs {
		if !slices.Contains(p.Env, "GIT_CONFIG_GLOBAL=/dev/null") {
			out = append(out, p)
		}
	}
	return out
}

// A mirror recorded under the SSH spelling of its remote is the same remote
// when the config spells it over HTTPS, as ssh://, or with another user: the
// offline check finds it healthy and reaches for no remote.
func TestCheck_SpellingChangeIsSameRemote(t *testing.T) {
	f := built(t)
	writeOldStateURL(t, f, sshSpelling)
	procs := recordGitProcs(t)
	for _, url := range []string{httpsSpelling, "ssh://git@mirror.test/o/app", "ssh://git@mirror.test:22/o/app.git", userSpelling} {
		if got := check(f.gitCtx(t), f.mdir(), Repo{ID: "app", URL: url}); got != FaultNone {
			t.Errorf("check with the state recording %q and the config giving %q = %q, want no fault", sshSpelling, url, got)
		}
	}
	if net := networkProcs(procs()); len(net) != 0 {
		t.Fatalf("the check started %d git processes that may reach the remote: %+v", len(net), net)
	}
}
