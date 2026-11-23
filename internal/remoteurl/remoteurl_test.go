package remoteurl

import (
	"strings"
	"testing"
)

func TestParseNormalizesForComparison(t *testing.T) {
	cases := []struct {
		raw, key string
	}{
		{"https://github.com/dana/repo", "https://github.com:443/dana/repo"},
		{"HTTPS://GitHub.COM/dana/repo.git", "https://github.com:443/dana/repo"},
		{"https://github.com/dana/repo/", "https://github.com:443/dana/repo"},
		{"https://github.com/dana/repo.git/", "https://github.com:443/dana/repo"},
		{"https://github.com:443/dana/repo", "https://github.com:443/dana/repo"},
		{"https://github.com:0443/dana/repo", "https://github.com:443/dana/repo"},
		{"ssh://git@github.com/dana/repo.git", "ssh://git@github.com:22/dana/repo"},
		{"git@github.com:dana/repo.git", "ssh://git@github.com:22/dana/repo"},
		{"git@GitHub.com:dana/repo", "ssh://git@github.com:22/dana/repo"},
		{"github.com:dana/repo", "ssh://github.com:22/dana/repo"},
		{"ssh://git@host:2222/dana/repo", "ssh://git@host:2222/dana/repo"},
		{"https://[::1]:8443/dana/repo", "https://[::1]:8443/dana/repo"},
		// The path keeps its case: only scheme and host are lowercased.
		{"https://github.com/Dana/Repo", "https://github.com:443/Dana/Repo"},
	}
	for _, c := range cases {
		u, err := Parse(c.raw, false)
		if err != nil {
			t.Errorf("Parse(%q): %v", c.raw, err)
			continue
		}
		if got := u.Key(); got != c.key {
			t.Errorf("Parse(%q).Key() = %q, want %q", c.raw, got, c.key)
		}
	}
}

func TestParseRefusesWithTheReason(t *testing.T) {
	cases := []struct {
		raw, reason string
	}{
		{"ext::sh -c touch% /tmp/x", "scheme not allowed: ext"},
		{"http://github.com/dana/repo", "scheme not allowed: http"},
		{"git://github.com/dana/repo", "scheme not allowed: git"},
		{"/srv/git/repo.git", "scheme not allowed: a local path"},
		{"./repo", "scheme not allowed: a local path"},
		{"file:///srv/git/repo.git", "scheme not allowed: file"},
		{"https://github.com/dana/../mallory/repo", "dot segment"},
		{"https://github.com/dana/./repo", "dot segment"},
		{"https://github.com/dana//repo", "empty segment"},
		{"https://github.com/dana/%2e%2e/mallory/repo", "percent-encoding"},
		{`https://github.com/dana\repo`, "backslash"},
		{"https://github.com/dana/re\npo", "control character"},
		{"https://github.com/dana/re\x7fpo", "control character"},
		{"https://dana@github.com/dana/repo", "the URL carries userinfo"},
		{"https://dana:hunter2@github.com/dana/repo", "the URL carries userinfo"},
		{"ssh://dana@github.com/dana/repo", "userinfo other than the user git"},
		{"ssh://git:pw@github.com/dana/repo", "userinfo other than the user git"},
		{"dana@github.com:dana/repo", "userinfo other than the user git"},
		{"https://-oProxyCommand=x/dana/repo", "the host starts with -"},
		{"-uhost:dana/repo", "the URL starts with -"},
		{"host:-dana/repo", "the path starts with -"},
		{"ssh://host/-dana/repo", "the path starts with -"},
		{"https:///dana/repo", "no host"},
		{"https://github.com:99999/dana/repo", "port does not parse"},
		{"https://github.com:/dana/repo", "port does not parse"},
		{"https://git hub.com/dana/repo", "space"},
		{"https://gïthub.com/dana/repo", "non-ASCII"},
	}
	for _, c := range cases {
		_, err := Parse(c.raw, false)
		if err == nil {
			t.Errorf("Parse(%q) accepted it; want %q", c.raw, c.reason)
			continue
		}
		if !strings.Contains(err.Error(), c.reason) {
			t.Errorf("Parse(%q) = %q, want the reason %q", c.raw, err, c.reason)
		}
	}
}

func TestParseNeverRepeatsUserinfo(t *testing.T) {
	for _, raw := range []string{
		"https://dana:hunter2@github.com/dana/repo",
		"ssh://hunter2@github.com/dana/repo",
		"hunter2@github.com:dana/repo",
	} {
		_, err := Parse(raw, false)
		if err == nil || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("Parse(%q) = %v; want a refusal that doesn't repeat the userinfo", raw, err)
		}
	}
}

func TestParseAdmitsFileOnlyWhenAsked(t *testing.T) {
	if _, err := Parse("file:///srv/git/repo.git", false); err == nil {
		t.Fatal("a file URL parsed with the file seam off")
	}
	u, err := Parse("file:///srv/git/repo.git", true)
	if err != nil {
		t.Fatal(err)
	}
	if u.Key() != "file:///srv/git/repo" || u.String() != "file:///srv/git/repo.git" {
		t.Errorf("file URL key %q string %q", u.Key(), u.String())
	}
	if _, err := Parse("file://host/srv/repo", true); err == nil {
		t.Error("a file URL naming a host parsed")
	}
	if _, err := Parse("file:///srv/../repo", true); err == nil {
		t.Error("a file URL with a dot segment parsed")
	}
	p, err := ParsePrefix("https://host/srv/")
	if err != nil {
		t.Fatal(err)
	}
	if u.UnderPrefix(p) {
		t.Error("a file URL matched a prefix")
	}
}

func TestUnderPrefixMatchesWholeSegmentsAndTheWholeAuthority(t *testing.T) {
	p, err := ParsePrefix("https://host/dana/")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		raw   string
		under bool
	}{
		{"https://host/dana/repo", true},
		{"https://HOST/dana/repo.git", true},
		{"https://host:443/dana/repo", true},
		{"https://host/dana/team/repo", true},
		{"https://host/dana-evil/repo", false},
		{"https://host/danarepo", false},
		{"https://host/dana", false},
		{"https://host/dana/", false},
		{"https://host:8443/dana/repo", false},
		{"https://other/dana/repo", false},
		{"ssh://git@host/dana/repo", false},
		{"https://host/Dana/repo", false},
	}
	for _, c := range cases {
		u, err := Parse(c.raw, false)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.raw, err)
		}
		if got := u.UnderPrefix(p); got != c.under {
			t.Errorf("%q under %q = %v, want %v", c.raw, "https://host/dana/", got, c.under)
		}
	}
}

func TestRebuildTakesOnlyThePathFromTheRemote(t *testing.T) {
	p, err := ParsePrefix("ssh://git@host:2222/dana/")
	if err != nil {
		t.Fatal(err)
	}
	u, err := Parse("ssh://git@HOST:2222/dana/repo.git", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Rebuild(p); got != "ssh://git@host:2222/dana/repo.git" {
		t.Errorf("Rebuild = %q", got)
	}
	h, _ := ParsePrefix("https://host/dana/")
	r, _ := Parse("https://host:443/dana/repo", false)
	if got := r.Rebuild(h); got != "https://host/dana/repo" {
		t.Errorf("Rebuild = %q, want the default port left out", got)
	}
}

func TestParsePrefixRefusals(t *testing.T) {
	cases := []struct {
		raw, reason string
	}{
		{"https://host/dana", "end in /"},
		{"https://host/", "at least one path segment"},
		{"git@host:dana/", "https:// or ssh://"},
		{"http://host/dana/", "scheme not allowed: http"},
		{"https://user@host/dana/", "userinfo"},
		{"https://host/dana/../x/", "dot segment"},
		{"file:///srv/", "scheme not allowed: file"},
	}
	for _, c := range cases {
		_, err := ParsePrefix(c.raw)
		if err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("ParsePrefix(%q) = %v, want %q", c.raw, err, c.reason)
		}
	}
}

func TestLastSegment(t *testing.T) {
	u, _ := Parse("git@github.com:org/tool.git", false)
	if u.LastSegment() != "tool" {
		t.Errorf("LastSegment = %q", u.LastSegment())
	}
}
