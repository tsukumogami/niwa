package removal

import (
	"context"
	"strings"
	"testing"
)

func TestSelectProofsMatchesTheClonesOwnURLOnly(t *testing.T) {
	const clone = "git@github.com:org/app.git"
	ps := selectProofs([]remoteEntry{{"origin", "ssh://git@GitHub.com/org/app"}}, clone, proofPolicy{}, false)
	if len(ps.remotes) != 1 || ps.remotes[0].url != clone || ps.remotes[0].viaPrefix || len(ps.notes) != 0 {
		t.Fatalf("proofs %+v; want the configured clone URL itself", ps)
	}

	// An origin re-pointed elsewhere never brings the clone URL in.
	ps = selectProofs([]remoteEntry{{"origin", "https://evil.example/org/app"}}, clone, proofPolicy{}, false)
	if len(ps.remotes) != 0 || len(ps.notes) != 1 || !strings.Contains(ps.notes[0], "remote not recognized: origin") || !strings.Contains(ps.notes[0], "proof_remotes") {
		t.Fatalf("proofs %+v; want origin not recognized and nothing to contact", ps)
	}

	// A workspace clone's remote equal to another repository's clone URL
	// isn't its proof remote.
	pp := proofPolicy{cloneURLs: []string{"git@github.com:org/other.git"}}
	ps = selectProofs([]remoteEntry{{"origin", "git@github.com:org/other.git"}}, clone, pp, false)
	if len(ps.remotes) != 0 {
		t.Fatalf("proofs %+v; want another repository's clone URL refused for a named clone", ps)
	}
}

func TestSelectProofsForAnUnnamedRepository(t *testing.T) {
	pp := proofPolicy{
		cloneURLs: []string{"https://github.com/org/app"},
		cloneURLOf: func(name string) (string, bool) {
			if name == "tool" {
				return "git@github.com:org/tool.git", true
			}
			return "", false
		},
	}
	ps := selectProofs([]remoteEntry{
		{"origin", "https://github.com/org/app.git"},
		{"up", "git@github.com:org/tool"},
		{"x", "https://github.com/org/unknown"},
	}, "", pp, false)
	if len(ps.remotes) != 2 || ps.remotes[0].url != "https://github.com/org/app" || ps.remotes[1].url != "git@github.com:org/tool.git" {
		t.Fatalf("proofs %+v; want both configured clone URLs, as configured", ps.remotes)
	}
	if len(ps.notes) != 1 || !strings.Contains(ps.notes[0], "remote not recognized: x") {
		t.Fatalf("notes %q; want x not recognized", ps.notes)
	}
	if ps := selectProofs(nil, "", pp, false); len(ps.remotes) != 0 || len(ps.notes) != 0 {
		t.Fatalf("a repository with no remote has proofs %+v", ps)
	}
}

func TestSelectProofsThroughAPrefix(t *testing.T) {
	pp := proofPolicy{prefixes: []string{"https://host/dana/", "not a prefix"}}
	ps := selectProofs([]remoteEntry{
		{"fork", "https://HOST:443/dana/repo.git"},
		{"evil", "https://host/dana-evil/repo"},
		{"port", "https://host:8443/dana/repo"},
		{"dots", "https://host/dana/../mallory/repo"},
		{"enc", "https://host/dana/%2e%2e/mallory/repo"},
		{"cred", "https://dana:hunter2@host/dana/repo"},
	}, "", pp, false)
	if len(ps.remotes) != 1 || ps.remotes[0].url != "https://host/dana/repo.git" || !ps.remotes[0].viaPrefix {
		t.Fatalf("proofs %+v; want the fork rebuilt from the prefix", ps.remotes)
	}
	want := []string{
		"remote not recognized: evil",
		"remote not recognized: port",
		"remote URL not allowed: the path holds a dot segment (remote dots)",
		"remote URL not allowed: the URL carries percent-encoding (remote enc)",
		"remote URL not allowed: the URL carries userinfo (remote cred)",
	}
	if len(ps.notes) != len(want) {
		t.Fatalf("notes %q; want %q", ps.notes, want)
	}
	for i, w := range want {
		if !strings.Contains(ps.notes[i], w) {
			t.Errorf("note %d = %q, want %q", i, ps.notes[i], w)
		}
		if strings.Contains(ps.notes[i], "hunter2") {
			t.Errorf("note %q repeats the userinfo", ps.notes[i])
		}
	}
}

func TestSelectProofsAdmitsFileOnlyByEqualityWithTheSeamOn(t *testing.T) {
	const clone = "file:///srv/app.git"
	remotes := []remoteEntry{{"origin", "file:///srv/app"}}
	if ps := selectProofs(remotes, clone, proofPolicy{}, false); len(ps.remotes) != 0 || len(ps.notes) != 2 {
		t.Fatalf("seam off: proofs %+v; want the clone URL and the remote refused", ps)
	}
	ps := selectProofs(remotes, clone, proofPolicy{}, true)
	if len(ps.remotes) != 1 || ps.remotes[0].url != clone {
		t.Fatalf("seam on: proofs %+v; want the clone URL", ps)
	}
	// Never through a prefix, even with the seam on.
	ps = selectProofs([]remoteEntry{{"origin", "file:///srv/dana/app"}}, "", proofPolicy{prefixes: []string{"https://srv/dana/"}}, true)
	if len(ps.remotes) != 0 {
		t.Fatalf("proofs %+v; want a file remote never matched by a prefix", ps)
	}
}

func TestRefusedCloneURLVouchesForNothing(t *testing.T) {
	const clone = "https://dana:hunter2@github.com/org/app"
	ps := selectProofs([]remoteEntry{{"origin", clone}}, clone, proofPolicy{}, false)
	if len(ps.remotes) != 0 {
		t.Fatalf("proofs %+v; want nothing contacted", ps)
	}
	joined := strings.Join(ps.notes, "\n")
	if !strings.Contains(joined, "remote URL not allowed: the URL carries userinfo (the clone URL)") ||
		!strings.Contains(joined, "remote URL not allowed: the URL carries userinfo (remote origin)") ||
		strings.Contains(joined, "hunter2") {
		t.Fatalf("notes %q", ps.notes)
	}
}

func TestReadConfigRecordsRemoteURLs(t *testing.T) {
	f := newFixture(t)
	gitRun(t, f.clone, "remote", "add", "My.Fork", "https://example.invalid/fork")
	p := f.policy(t)
	rc, err := p.Env.readConfig(context.Background(), repoDirs{GitDir: f.clone + "/.git", WorkTree: f.clone})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rc.remotes {
		got[r.Name] = r.URL
	}
	if got["origin"] != f.remoteURL || got["My.Fork"] != "https://example.invalid/fork" || len(got) != 2 {
		t.Fatalf("remotes %+v", rc.remotes)
	}
}
