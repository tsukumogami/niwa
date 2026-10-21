package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestAuthorityRecordRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := AuthorityRecord{Level: "strict", Source: "workspace"}
	if err := WriteAuthorityRecord(dir, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	path := filepath.Join(dir, ".niwa", "authority.json")
	if AuthorityRecordPath(dir) != path {
		t.Fatalf("AuthorityRecordPath = %q, want %q", AuthorityRecordPath(dir), path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("the temporary file was left behind: %v", err)
	}
	got, err := ReadAuthorityRecord(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != want {
		t.Fatalf("read %+v, want %+v", got, want)
	}
}

func TestAuthorityRecordMissing(t *testing.T) {
	_, err := ReadAuthorityRecord(t.TempDir())
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestAuthorityRecordValidate(t *testing.T) {
	cases := []struct {
		level, source string
		ok            bool
	}{
		{"autonomous", "default", true},
		{"supervised", "flag", true},
		{"strict", "machine", true},
		{"strict", "workspace", true},
		{"Strict", "workspace", false},
		{" strict", "workspace", false},
		{"", "workspace", false},
		{"bogus", "flag", false},
		{"strict", "declaration", false},
		{"strict", "", false},
		{"strict", "Flag", false},
	}
	for _, tc := range cases {
		err := AuthorityRecord{Level: tc.level, Source: tc.source}.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("Validate(%q, %q) = %v, want ok=%v", tc.level, tc.source, err, tc.ok)
		}
	}
}

func TestAuthorityRecordRejectsBadContent(t *testing.T) {
	if err := WriteAuthorityRecord(t.TempDir(), AuthorityRecord{Level: "bogus", Source: "flag"}); err == nil {
		t.Error("writing an invalid record must fail")
	}
	for name, body := range map[string]string{
		"not json":    "{",
		"bogus level": `{"authority_level":"bogus","authority_source":"flag"}`,
		"no source":   `{"authority_level":"strict"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.MkdirAll(filepath.Join(dir, ".niwa"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(AuthorityRecordPath(dir), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := ReadAuthorityRecord(dir)
			if err == nil || errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("err = %v, want a content error", err)
			}
		})
	}
}
