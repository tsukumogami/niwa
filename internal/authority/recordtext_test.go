package authority

import (
	"strings"
	"testing"
)

func TestRecordText_Golden(t *testing.T) {
	want := `dispatch record: present
dispatch id: 3c803591-4c01-486f-a927-f96ec399d6a7
level: strict from flag
level ceiling: supervised from workspace
grant ceiling: merge-pr, close-issue from machine
roles: coordinator
grant: merge-pr (role coordinator), close-issue (dispatch, role coordinator)
parent session id: session_abc-123
parent dispatch id: 7f1d2c3b-9a8e-4d6c-b5a4-0e1f2a3b4c5d (from cwd)
`
	if got := goldenRecord().Text(); got != want {
		t.Errorf("text =\n%s\nwant\n%s", got, want)
	}
}

// An entry this build can't hold is listed on its own line and never on the
// grant line.
func TestRecordText_UnknownGrantOnItsOwnLine(t *testing.T) {
	rec := mustParse(t, doc(t, map[string]any{
		"grant":      grantJSON([]string{"merge-pr", "dispatch"}, []string{"teardown", "dispatch"}),
		"supervisor": "someone",
	}))
	text := rec.Text()
	var grantLine, unknownLine string
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, "grant: "):
			grantLine = line
		case strings.HasPrefix(line, "not recognised by this niwa"):
			unknownLine = line
		}
	}
	if grantLine != "grant: merge-pr (dispatch)" {
		t.Errorf("grant line = %q", grantLine)
	}
	if unknownLine != "not recognised by this niwa, so not held: teardown (dispatch)" {
		t.Errorf("unknown line = %q\n%s", unknownLine, text)
	}
}

func TestRecordText_NoCeilingDeclaredAndNoParent(t *testing.T) {
	rec := goldenRecord()
	rec.GrantCeiling, rec.GrantCeilingSource = []Authority{}, LayerNone
	rec.Roles, rec.Grant = nil, nil
	rec.ParentSessionID, rec.ParentDispatchID, rec.ParentDispatchSource = "", "", ParentSourceNone
	text := rec.Text()
	for _, want := range []string{
		"grant ceiling: (empty) from none\n",
		"roles: none\n",
		"grant: none\n",
		"parent session id: none\n",
		"parent dispatch id: none\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
}

func TestRecordText_DeclaredEmptyCeiling(t *testing.T) {
	rec := goldenRecord()
	rec.GrantCeiling, rec.GrantCeilingSource = []Authority{}, LayerWorkspace
	rec.Roles, rec.Grant = nil, nil
	if text := rec.Text(); !strings.Contains(text, "grant ceiling: (empty) from workspace\n") {
		t.Errorf("text lacks the declared-empty ceiling line:\n%s", text)
	}
}

func TestRecordText_NewerVersion(t *testing.T) {
	rec := mustParse(t, []byte(`{"schema": "niwa.authority.record/v2", "dispatch_id": "`+testDispatchID+`", "grant": [{"authority": "merge-pr"}]}`))
	want := "dispatch record: newer version niwa.authority.record/v2; grant none\ndispatch id: " + testDispatchID + "\n"
	if got := rec.Text(); got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
}

func TestNoRecord(t *testing.T) {
	b, err := NoRecordJSON(Supervised, LayerWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "schema": "niwa.authority.record/v1",
  "record": false,
  "grant": [],
  "level": "supervised",
  "level_source": "workspace"
}`
	if string(b) != want {
		t.Errorf("document =\n%s\nwant\n%s", b, want)
	}
	if got := NoRecordText(Autonomous, LayerBuiltIn); got != "dispatch record: none\nlevel: autonomous from built-in (no dispatch record)\ngrant: none\n" {
		t.Errorf("text = %q", got)
	}
}
