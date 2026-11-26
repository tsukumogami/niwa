package scrub

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// Token values are built from pieces so no whole credential-shaped string
// sits in the source.
var (
	fakeBody36 = strings.Repeat("A1b2", 9)
	fakeJWT    = "eyJ" + "hbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiIxIn0" + "." + "c2lnbmF0dXJl"
)

func TestScrubEvidenceStripsURLUserinfo(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://user:token@host/x", "https://host/x"},
		{"fetch https://x-access-token:ghs_abc@github.com/o/r failed", "fetch https://github.com/o/r failed"},
		{"https://user@host", "https://host"},
		{"ssh://git:pa@ss@host:22/r", "ssh://host:22/r"},
		{"clone URL unreachable: https://u:p@127.0.0.1:9/repo?x=1", "clone URL unreachable: https://127.0.0.1:9/repo?x=1"},
		{"https://host/path@ref", "https://host/path@ref"},
		{"git@github.com:o/r", "git@github.com:o/r"},
	} {
		if got := ScrubEvidence(tc.in, 400); got != tc.want {
			t.Errorf("ScrubEvidence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestScrubEvidenceRedactsTokenShapes(t *testing.T) {
	for _, tc := range []struct{ name, secret string }{
		{"ghp_", "ghp_" + fakeBody36},
		{"gho_", "gho_" + fakeBody36},
		{"ghs_", "ghs_" + fakeBody36},
		{"github_pat_", "github_pat_" + fakeBody36 + "_" + fakeBody36},
		{"glpat-", "glpat-" + "x1Y2z3-W4v5u6_T7s8r9"},
		{"sk-", "sk-" + "proj-" + fakeBody36},
		{"xoxb-", "xoxb-" + "1234567890-0987654321-abcdef"},
		{"xoxp-", "xoxp-" + "1234567890-abcdefghij"},
		{"AKIA", "AKIA" + "IOSFODNN7EXAMPLE"},
		{"ASIA", "ASIA" + "IOSFODNN7EXAMPLE"},
		{"three-part eyJ", fakeJWT},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := "untracked files: notes-" + tc.secret + ".txt"
			got := ScrubEvidence(in, 400)
			if strings.Contains(got, tc.secret) || !strings.Contains(got, Redacted) {
				t.Fatalf("ScrubEvidence(%q) = %q; want the token redacted", in, got)
			}
			if !strings.HasPrefix(got, "untracked files: notes-") || !strings.HasSuffix(got, ".txt") {
				t.Fatalf("ScrubEvidence(%q) = %q; want the text around the token kept", in, got)
			}
		})
	}
	for _, keep := range []string{
		"task-list-for-the-release-candidate",
		"disk-image-of-the-build-machine",
		"branch ghp_short",
		"AKIA-not-a-key",
	} {
		if got := ScrubEvidence(keep, 400); got != keep {
			t.Errorf("ScrubEvidence(%q) = %q; want it unchanged", keep, got)
		}
	}
}

func TestScrubEvidenceRedactsAuthorizationAndBearer(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Authorization: Bearer abc.def", "Authorization: Bearer [redacted]"},
		{"authorization:\tBasic dXNlcjpwYXNz", "authorization: Basic [redacted]"},
		{"Authorization:\t Bearer x", "Authorization: Bearer [redacted]"},
		{"AUTHORIZATION: opaque-value", "AUTHORIZATION: [redacted]"},
		{"curl -H Bearer xyz failed", "curl -H Bearer [redacted] failed"},
		{"header bearer\n\tq1w2e3", "header bearer [redacted]"},
	} {
		if got := ScrubEvidence(tc.in, 400); got != tc.want {
			t.Errorf("ScrubEvidence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// The same header scrubs the same whatever white space it carries.
	if a, b := ScrubEvidence("Authorization:\t Bearer x", 400), ScrubEvidence("Authorization: Bearer x", 400); a != b {
		t.Errorf("white space changed the result: %q and %q", a, b)
	}
}

func TestScrubEvidenceRedactsSecretNamedPairs(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"token=abc", "token=[redacted]"},
		{"SECRET: hunter2", "SECRET: [redacted]"},
		{"Password=hunter2 rest", "Password=[redacted] rest"},
		{"passwd=hunter2", "passwd=[redacted]"},
		{"api_key=abc123", "api_key=[redacted]"},
		{"X-Auth=abc", "X-Auth=[redacted]"},
		{"credential: abc", "credential: [redacted]"},
		{"my.Credential_Helper=store", "my.Credential_Helper=[redacted]"},
		{"https://host/r?access_token=abc&x=1", "https://host/r?access_token=[redacted]&x=1"},
		{"a=1;session_token=abc,b=2", "a=1;session_token=[redacted],b=2"},
		{`opts "apikey=abc" done`, `opts "apikey=[redacted]" done`},
		// A colon with no white space after it is not a pair, so a
		// configuration key holding a URL keeps its URL.
		{"repository configuration names a program: credential.https://example.com.helper", "repository configuration names a program: credential.https://example.com.helper"},
		// The pair rule fires on paths too, toward hiding more.
		{"unreadable: .niwa/keys: permission denied", "unreadable: .niwa/keys: [redacted] denied"},
		{"name=value", "name=value"},
	} {
		if got := ScrubEvidence(tc.in, 400); got != tc.want {
			t.Errorf("ScrubEvidence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestScrubEvidenceRedactsAnAuthorizationPairWrittenWithEquals(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"authorization=secret", "authorization=[redacted]"},
		{"Authorization=secret rest", "Authorization=[redacted] rest"},
		{"https://host/r?authorization=secret&x=1", "https://host/r?authorization=[redacted]&x=1"},
		{"proxy_authorization = secret", "proxy_authorization = [redacted]"},
		// The colon form is the header, scrubbed with its scheme kept.
		{"Authorization: Bearer abc", "Authorization: Bearer [redacted]"},
		{"authorization: basic xyz", "authorization: basic [redacted]"},
		{"authorization : opaque", "authorization : [redacted]"},
	} {
		got := ScrubEvidence(tc.in, 400)
		if got != tc.want {
			t.Errorf("ScrubEvidence(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for _, secret := range []string{"secret", "abc", "xyz", "opaque"} {
			if strings.Contains(got, secret) {
				t.Errorf("ScrubEvidence(%q) = %q; it keeps %q", tc.in, got, secret)
			}
		}
	}
}

func TestScrubEvidenceRedactsSecretPairsWithSpacesQuotesAndJSON(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		// White space around = and before the colon.
		{"token = abc", "token = [redacted]"},
		{"token =abc", "token =[redacted]"},
		{"password : abc", "password : [redacted]"},
		{"api_key= abc rest", "api_key= [redacted] rest"},
		// A quoted value goes whole, white space and all.
		{`token="abc"`, `token="[redacted]"`},
		{`token='abc'`, `token='[redacted]'`},
		{`secret: "abc def"`, `secret: "[redacted]"`},
		{`password = 'hunter two' rest`, `password = '[redacted]' rest`},
		{`token="never closed and more`, `token="[redacted]`},
		// The JSON form, with or without white space after the colon.
		{`"token": "value"`, `"token": "[redacted]"`},
		{`{"api_key":"value","x":1}`, `{"api_key":"[redacted]","x":1}`},
		{`{'secret': 'value'}`, `{'secret': '[redacted]'}`},
		{`"authorization": "Bearer abc"`, `"authorization": "[redacted]"`},
	} {
		got := ScrubEvidence(tc.in, 400)
		if got != tc.want {
			t.Errorf("ScrubEvidence(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for _, secret := range []string{"abc", "def", "value", "hunter", "never closed"} {
			if strings.Contains(got, secret) {
				t.Errorf("ScrubEvidence(%q) = %q; it keeps %q", tc.in, got, secret)
			}
		}
	}
	sha1 := "0f3a9c41b7e2d58a6c0b91e4f27d3a8c5e6b1d09"
	for _, keep := range []string{
		"key points",
		"monkey business",
		"the key points are these",
		"a monkey business plan",
		"HEAD at " + sha1,
		`commit "` + sha1 + `"`,
		"repository configuration names a program: credential.https://example.com.helper",
		"credential.https://example.com",
		`a "quoted" word`,
		"name = value",
	} {
		if got := ScrubEvidence(keep, 400); got != keep {
			t.Errorf("ScrubEvidence(%q) = %q; want it unchanged", keep, got)
		}
	}
}

func TestScrubEvidenceRedactsLongMixedBase64(t *testing.T) {
	mixed32 := "aB3dE5fG7hI9jK1lM3nO5pQ7rS9tU1vW"
	if len(mixed32) != 32 {
		t.Fatalf("fixture is %d characters", len(mixed32))
	}
	for _, tc := range []struct{ in, want string }{
		{"blob " + mixed32, "blob [redacted]"},
		{"x " + mixed32 + "+/=" + " y", "x [redacted]/= y"},
		{"k: " + mixed32, "k: [redacted]"},
		// = is in the alphabet, so a run takes a plain key with it.
		{"k=" + mixed32, "[redacted]"},
		{mixed32[:31], mixed32[:31]},
		{strings.Repeat("abcdefgh", 5), strings.Repeat("abcdefgh", 5)},
		{strings.Repeat("ABCD1234", 5), strings.Repeat("ABCD1234", 5)},
		{strings.Repeat("abcdEFGH", 5), strings.Repeat("abcdEFGH", 5)},
		// / and . break a run, so an ordinary path survives.
		{"/Users/SomeName/projects2/AnotherDirectory9/file.txt", "/Users/SomeName/projects2/AnotherDirectory9/file.txt"},
		{"tools/MyProject2.Component3.Subsystem4.Thing5/x", "tools/MyProject2.Component3.Subsystem4.Thing5/x"},
	} {
		if got := ScrubEvidence(tc.in, 400); got != tc.want {
			t.Errorf("ScrubEvidence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestScrubEvidenceKeepsAGitObjectID(t *testing.T) {
	sha1 := "0f3a9c41b7e2d58a6c0b91e4f27d3a8c5e6b1d09"
	sha256 := "9b2e4f1a7c3d5e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f6071"
	if len(sha1) != 40 || len(sha256) != 64 {
		t.Fatalf("fixtures are %d and %d characters", len(sha1), len(sha256))
	}
	for _, in := range []string{
		sha1,
		sha256,
		"commits the clone URL does not show: HEAD at " + sha1,
		"worktree listing has a HEAD that does not parse: \"HEAD " + sha256 + "\"",
	} {
		if got := ScrubEvidence(in, 400); got != in {
			t.Errorf("ScrubEvidence(%q) = %q; want it unchanged", in, got)
		}
	}
}

func TestScrubEvidenceRemovesControlSequences(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"a\x1b[31mred\x1b[0m", "ared"},
		{"\x1b[31m", ""},
		{"\x1b[1;2;3H\x1b[?25lx", "x"},
		{"x\x1b]0;title\x07y", "xy"},
		{"x\x1b]8;;http://e\x1b\\link\x1b]8;;\x1b\\y", "xlinky"},
		{"\x1b(Bz", "z"},
		{"\x1b7save\x1b8", "save"},
		{"tail\x1b", "tail"},
		{"x\x1b]unterminated title", "x"},
		{"a\x00b\x07c\x7fd", "a b c d"},
		{"a\u0085b\u009bc", "a b c"},
		{"a‮b⁦c‏d", "a b c d"},
		{"a\xffb", "a�b"},
		{"  lots \t\n of   space  ", "lots of space"},
	} {
		got := ScrubEvidence(tc.in, 400)
		if got != tc.want {
			t.Errorf("ScrubEvidence(%q) = %q, want %q", tc.in, got, tc.want)
		}
		for _, r := range got {
			if r == 0x1b || unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
				t.Errorf("ScrubEvidence(%q) = %q; it holds %U", tc.in, got, r)
			}
		}
	}
}

func TestScrubEvidenceCutsAfterScrubbing(t *testing.T) {
	token := "ghp_" + fakeBody36
	for _, max := range []int{200, 400} {
		// Placed so a cut made first would leave the token's front behind.
		for _, before := range []int{max - 20, max - 6, max - 4, max - 2} {
			in := strings.Repeat("a", before) + token + " tail"
			got := ScrubEvidence(in, max)
			if strings.Contains(got, "ghp_") || strings.Contains(got, fakeBody36[:8]) {
				t.Errorf("max %d, token at %d: %q keeps part of the token", max, before, got)
			}
			if n := utf8.RuneCountInString(got); n > max {
				t.Errorf("max %d, token at %d: %d characters", max, before, n)
			}
		}
	}
	// The cut counts characters, not bytes.
	got := ScrubEvidence(strings.Repeat("é", 300), 200)
	if want := strings.Repeat("é", 197) + "..."; got != want {
		t.Errorf("cut of 300 two-byte characters = %d characters, want 200", utf8.RuneCountInString(got))
	}
	if got := ScrubEvidence(strings.Repeat("é", 200), 200); got != strings.Repeat("é", 200) {
		t.Errorf("exactly the maximum was cut: %d characters", utf8.RuneCountInString(got))
	}
	if got := ScrubEvidence("abcdef", 2); got != ".." {
		t.Errorf("a maximum under three: %q", got)
	}
}

func TestStripControlsAndCutTextMatchTheirParts(t *testing.T) {
	if got := CutText(StripControls(StripBidi("a‮b\x00\x00c  ")), 200); got != "a b c" {
		t.Errorf("gate composition = %q", got)
	}
	if got := CutText("abcdefghijk", 10); got != "abcdefg..." {
		t.Errorf("CutText = %q", got)
	}
}
