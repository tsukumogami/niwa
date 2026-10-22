package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/authority"
)

// recordGoldenV1 is the stored document for recordGolden(): the same document
// the authority package pins for Record.MarshalJSON, plus the trailing newline
// the writer adds.
const recordGoldenV1 = `{
  "schema": "niwa.authority.record/v1",
  "record": true,
  "dispatch_id": "3c803591-4c01-486f-a927-f96ec399d6a7",
  "parent_session_id": "session_abc-123",
  "parent_dispatch_id": "7f1d2c3b-9a8e-4d6c-b5a4-0e1f2a3b4c5d",
  "parent_dispatch_source": "cwd",
  "level": "strict",
  "level_source": "flag",
  "level_ceiling": "supervised",
  "level_ceiling_source": "workspace",
  "grant_ceiling": [
    "merge-pr",
    "close-issue"
  ],
  "grant_ceiling_source": "machine",
  "roles": [
    "coordinator"
  ],
  "grant": [
    {
      "authority": "merge-pr",
      "sources": [
        "role:coordinator"
      ]
    },
    {
      "authority": "close-issue",
      "sources": [
        "dispatch",
        "role:coordinator"
      ]
    }
  ],
  "unknown_grant": []
}
`

func recordGolden() authority.Record {
	return authority.Record{
		Schema:               authority.SchemaV1,
		Present:              true,
		DispatchID:           "3c803591-4c01-486f-a927-f96ec399d6a7",
		ParentSessionID:      "session_abc-123",
		ParentDispatchID:     "7f1d2c3b-9a8e-4d6c-b5a4-0e1f2a3b4c5d",
		ParentDispatchSource: authority.ParentSourceCwd,
		Level:                authority.Strict,
		LevelSource:          authority.LayerFlag,
		LevelCeiling:         authority.Supervised,
		LevelCeilingSource:   authority.LayerWorkspace,
		GrantCeiling:         []authority.Authority{authority.MergePR, authority.CloseIssue},
		GrantCeilingSource:   authority.LayerMachine,
		Roles:                []string{"coordinator"},
		Grant: []authority.GrantEntry{
			{Authority: authority.MergePR, Sources: []authority.Source{"role:coordinator"}},
			{Authority: authority.CloseIssue, Sources: []authority.Source{"dispatch", "role:coordinator"}},
		},
		UnknownGrant: []authority.RawGrantEntry{},
	}
}

func TestAuthorityRecord_RoundTripAndStoredBytes(t *testing.T) {
	inst := authorityInstance(t)
	if err := WriteAuthorityRecord(inst, recordGolden()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(AuthorityRecordPath(inst))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != recordGoldenV1 {
		t.Errorf("stored bytes =\n%s\nwant\n%s", data, recordGoldenV1)
	}
	got, found, err := ReadAuthorityRecord(inst)
	if err != nil || !found {
		t.Fatalf("ReadAuthorityRecord = found %v, err %v", found, err)
	}
	if !reflect.DeepEqual(got, recordGolden()) {
		t.Errorf("read back\n%+v\nwant\n%+v", got, recordGolden())
	}
}

func TestWriteAuthorityRecord_NoTempFileAfterFailure(t *testing.T) {
	inst := authorityInstance(t)
	// A directory at the final path makes the rename fail after the temp file
	// was written.
	if err := os.MkdirAll(filepath.Join(AuthorityRecordPath(inst), "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteAuthorityRecord(inst, recordGolden()); err == nil {
		t.Fatal("write over a directory succeeded")
	}
	entries, err := os.ReadDir(filepath.Join(inst, StateDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != AuthorityRecordFile {
			t.Errorf("left behind %s", e.Name())
		}
	}
}

func TestReadAuthorityRecord_MissingIsNoRecord(t *testing.T) {
	for name, inst := range map[string]string{
		"no record file": authorityInstance(t),
		"no .niwa":       t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			_, found, err := ReadAuthorityRecord(inst)
			if err != nil || found {
				t.Fatalf("found %v, err %v; want no record and no error", found, err)
			}
		})
	}
}

// assertRecordClass checks an error names its class and nothing it read.
func assertRecordClass(t *testing.T, inst string, err error, class string, secret string) {
	t.Helper()
	var re *AuthorityRecordError
	if !errors.As(err, &re) || re.Class != class {
		t.Fatalf("err = %v, want class %q", err, class)
	}
	msg := err.Error()
	if strings.Contains(msg, inst) || strings.Contains(msg, AuthorityRecordFile) {
		t.Errorf("message %q names the record path", msg)
	}
	if secret != "" && strings.Contains(msg, secret) {
		t.Errorf("message %q echoes the file's contents", msg)
	}
}

func TestReadAuthorityRecord_FIFODoesNotBlock(t *testing.T) {
	inst := authorityInstance(t)
	if err := syscall.Mkfifo(AuthorityRecordPath(inst), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := ReadAuthorityRecord(inst)
		done <- err
	}()
	select {
	case err := <-done:
		assertRecordClass(t, inst, err, RecordClassNotRegular, "")
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO record blocked")
	}
}

func TestReadAuthorityRecord_SymlinkRefused(t *testing.T) {
	inst := authorityInstance(t)
	target := filepath.Join(t.TempDir(), "real.json")
	if err := os.WriteFile(target, []byte(recordGoldenV1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, AuthorityRecordPath(inst)); err != nil {
		t.Fatal(err)
	}
	_, found, err := ReadAuthorityRecord(inst)
	if !found {
		t.Error("a symlinked record read as no record")
	}
	assertRecordClass(t, inst, err, RecordClassSymlink, "")
}

func TestReadAuthorityRecord_OversizeRefused(t *testing.T) {
	inst := authorityInstance(t)
	big := strings.Repeat(" ", authority.MaxRecordBytes) + recordGoldenV1
	if err := os.WriteFile(AuthorityRecordPath(inst), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := ReadAuthorityRecord(inst)
	assertRecordClass(t, inst, err, RecordClassTooLarge, "")
	if want := "is larger than " + strconv.Itoa(authority.MaxRecordBytes) + " bytes"; RecordClassTooLarge != want {
		t.Errorf("class %q disagrees with the cap: want %q", RecordClassTooLarge, want)
	}
}

// grantSection matches the golden document's whole grant array.
var grantSection = regexp.MustCompile(`(?s)"grant": \[.*?\n  \],`)

func TestReadAuthorityRecord_MalformedClasses(t *testing.T) {
	golden := recordGoldenV1
	cases := map[string]struct {
		body   string
		secret string
	}{
		"control byte in parent_session_id": {strings.Replace(golden, `"session_abc-123"`, `"sess\u0007ion"`, 1), "sess"},
		"control byte in a role":            {strings.Replace(golden, `"coordinator"`+"\n  ]", `"coord\u001binator"`+"\n  ]", 1), "coord"},
		"control byte in a source":          {strings.Replace(golden, `"dispatch",`, `"disp\u0000atch",`, 1), "disp"},
		"unknown level":                     {strings.Replace(golden, `"level": "strict"`, `"level": "godmode"`, 1), "godmode"},
		"wrong schema":                      {strings.Replace(golden, `niwa.authority.record/v1`, `secretschema/v1`, 1), "secretschema"},
		"bad dispatch id":                   {strings.Replace(golden, `3c803591-4c01-486f-a927-f96ec399d6a7`, `not-a-uuid-secret`, 1), "not-a-uuid-secret"},
		"known grant out of order": {grantSection.ReplaceAllString(golden,
			`"grant": [{"authority": "close-issue", "sources": ["dispatch"]}, {"authority": "merge-pr", "sources": ["dispatch"]}],`), ""},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			inst := authorityInstance(t)
			if err := os.WriteFile(AuthorityRecordPath(inst), []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, found, err := ReadAuthorityRecord(inst)
			if !found {
				t.Error("a malformed record read as no record")
			}
			assertRecordClass(t, inst, err, RecordClassMalformed, tc.secret)
			var me *authority.MalformedRecordError
			if !errors.As(err, &me) {
				t.Errorf("err %v does not carry the parser's MalformedRecordError", err)
			}
		})
	}
}

func TestReadAuthorityRecord_V1AdditiveReads(t *testing.T) {
	inst := authorityInstance(t)
	body := strings.Replace(recordGoldenV1, `"unknown_grant": []`,
		`"unknown_grant": [], "future_field": {"x": 1}`, 1)
	body = strings.Replace(body, `"grant": [`,
		`"grant": [{"authority": "teardown", "sources": ["dispatch"]},`, 1)
	if err := os.WriteFile(AuthorityRecordPath(inst), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, found, err := ReadAuthorityRecord(inst)
	if err != nil || !found {
		t.Fatalf("found %v, err %v", found, err)
	}
	if len(rec.Grant) != 2 || len(rec.UnknownGrant) != 1 || rec.UnknownGrant[0].Authority != "teardown" {
		t.Errorf("grant %+v unknown %+v", rec.Grant, rec.UnknownGrant)
	}
}

func TestReadAuthorityRecord_UnreadableIsAnError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-0 file")
	}
	inst := authorityInstance(t)
	if err := os.WriteFile(AuthorityRecordPath(inst), []byte(recordGoldenV1), 0o000); err != nil {
		t.Fatal(err)
	}
	_, found, err := ReadAuthorityRecord(inst)
	if !found {
		t.Error("an unreadable record read as no record")
	}
	assertRecordClass(t, inst, err, RecordClassUnreadable, "")
}
