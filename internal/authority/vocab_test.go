package authority

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in      string
		want    *Level
		wantErr bool
	}{
		{"autonomous", lvl(Autonomous), false},
		{"supervised", lvl(Supervised), false},
		{"strict", lvl(Strict), false},
		{"  strict\t", lvl(Strict), false},
		{"", nil, false},
		{"   ", nil, false},
		{"Strict", nil, true},
		{"STRICT", nil, true},
		{"unbounded", nil, true},
		{"str ict", nil, true},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseLevel(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseLevel(%q) = %v, want %v", c.in, got, c.want)
		}
		if err != nil && !strings.Contains(err.Error(), "autonomous, supervised, strict") {
			t.Errorf("ParseLevel(%q) error %q does not list the accepted values", c.in, err)
		}
	}
}

func TestParseAuthority(t *testing.T) {
	cases := []struct {
		in         string
		want       Authority
		wantErr    bool
		wellFormed bool
	}{
		{"merge-pr", MergePR, false, false},
		{"close-issue", CloseIssue, false, false},
		{"cut-release", CutRelease, false, false},
		{" merge-pr ", MergePR, false, false},
		{"Merge-PR", "", true, false},
		{"merge pr", "", true, false},
		{"teardown", "", true, true},
		{"merge", "", true, true},
		{"", "", true, false},
		{"none", "", true, true},
	}
	for _, c := range cases {
		got, err := ParseAuthority(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseAuthority(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if got != c.want {
			t.Errorf("ParseAuthority(%q) = %q, want %q", c.in, got, c.want)
		}
		if err == nil {
			continue
		}
		var uv *UnknownValueError
		if !errors.As(err, &uv) {
			t.Fatalf("ParseAuthority(%q) error is %T, want *UnknownValueError", c.in, err)
		}
		if uv.WellFormed != c.wellFormed {
			t.Errorf("ParseAuthority(%q) WellFormed = %v, want %v", c.in, uv.WellFormed, c.wellFormed)
		}
		if !strings.Contains(err.Error(), "merge-pr, close-issue, cut-release") {
			t.Errorf("ParseAuthority(%q) error %q does not list the accepted values", c.in, err)
		}
	}
}

func TestAuthoritiesInR3Order(t *testing.T) {
	want := []Authority{MergePR, CloseIssue, CutRelease}
	if got := Authorities(); !reflect.DeepEqual(got, want) {
		t.Errorf("Authorities() = %v, want %v", got, want)
	}
}

// TestParseNameList covers the one comma-list parser shared by --grant,
// --role and `config set authority-ceiling`.
func TestParseNameList(t *testing.T) {
	split, err := ParseNameList([]string{"merge-pr,close-issue"})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := ParseNameList([]string{"merge-pr", "close-issue"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(split, repeated) {
		t.Errorf("comma list %v differs from repeated flags %v", split, repeated)
	}
	if got, _ := ParseNameList([]string{" merge-pr "}); !reflect.DeepEqual(got, []string{"merge-pr"}) {
		t.Errorf("padded entry parsed as %v", got)
	}
	if got, _ := ParseNameList([]string{"none"}); !reflect.DeepEqual(got, []string{"none"}) {
		t.Errorf("none parsed as %v; only config set gives it a meaning", got)
	}
	for _, bad := range []string{"merge-pr,", ",merge-pr", "merge-pr,,close-issue", "", "  "} {
		_, err := ParseNameList([]string{bad})
		var ee *EmptyEntryError
		if !errors.As(err, &ee) {
			t.Errorf("ParseNameList(%q) err = %v, want *EmptyEntryError", bad, err)
			continue
		}
		if !strings.Contains(err.Error(), "empty entry") || !strings.Contains(err.Error(), quote(bad)) {
			t.Errorf("ParseNameList(%q) error %q does not name the empty entry", bad, err)
		}
	}
}

func TestValidIDs(t *testing.T) {
	if !ValidDispatchID("3c803591-4c01-486f-a927-f96ec399d6a7") {
		t.Error("a v4 UUID is not a valid dispatch id")
	}
	for _, bad := range []string{"", "3C803591-4C01-486F-A927-F96EC399D6A7", "not-a-uuid", "3c803591-4c01-186f-a927-f96ec399d6a7"} {
		if ValidDispatchID(bad) {
			t.Errorf("ValidDispatchID(%q) = true", bad)
		}
	}
	if !ValidSessionID("abcdefgh") || !ValidSessionID(strings.Repeat("a", MaxSessionIDLen)) {
		t.Error("valid session ids refused")
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", MaxSessionIDLen+1), "has space1"} {
		if ValidSessionID(bad) {
			t.Errorf("ValidSessionID(%q) = true", bad)
		}
	}
}

func lvl(l Level) *Level { return &l }
