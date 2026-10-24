package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// While the instance lives, `authority show --json` and `lineage show --json`
// report the same parent session source and principal, and the record's
// parent_dispatch_source cwd matches the entry's observed.
func TestShow_AdvisoryFieldsAgreeWithTheLineageEntry(t *testing.T) {
	w := newShowWorkspace(t, "")
	inst := w.instance(t, "ws+one")
	auth := &dispatchAuthority{
		parentID:       showDispatchB,
		parentSrc:      authority.ParentSourceCwd,
		parentSession:  lineage.Link{ID: showSessionA, Source: lineage.SourceObserved},
		parentDispatch: lineage.Link{ID: showDispatchB, Source: lineage.SourceObserved},
		principal:      lineage.Principal{Name: "dana@example.com", Source: lineage.PrincipalInherited, Trust: lineage.TrustObserved},
	}
	rec := stampDispatchRecord(showRecord("", authority.Supervised, authority.LayerWorkspace, authority.MergePR), auth, showDispatchA, showSessionA)
	writeShowRecord(t, inst, rec)
	entry := dispatchLineageEntry(showDispatchA, "ws+one", "claude", time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC), auth)
	if err := workspace.CreateLineageEntry(w.root, entry); err != nil {
		t.Fatal(err)
	}

	var shown struct {
		ParentDispatchSource string                  `json:"parent_dispatch_source"`
		ParentSessionSource  string                  `json:"parent_session_source"`
		Principal            *authority.PrincipalDoc `json:"principal"`
	}
	out := runAuthorityCmd(t, inst, "show", "--json").mustSucceed(t)
	if err := json.Unmarshal([]byte(out.stdout), &shown); err != nil {
		t.Fatal(err)
	}
	lout, err := runLineageShowCmd(t, true, showDispatchA)
	if err != nil {
		t.Fatal(err)
	}
	var chain struct {
		Chain []lineage.Entry `json:"chain"`
	}
	if err := json.Unmarshal([]byte(lout), &chain); err != nil || len(chain.Chain) == 0 {
		t.Fatalf("lineage show --json: %v\n%s", err, lout)
	}
	e := chain.Chain[0]
	if shown.ParentSessionSource != e.ParentSession.Source {
		t.Errorf("parent session source: record %q, entry %q", shown.ParentSessionSource, e.ParentSession.Source)
	}
	if shown.Principal == nil || *shown.Principal != (authority.PrincipalDoc{Name: e.Principal.Name, Source: e.Principal.Source, Trust: e.Principal.Trust}) {
		t.Errorf("principal: record %+v, entry %+v", shown.Principal, e.Principal)
	}
	if shown.ParentDispatchSource != authority.ParentSourceCwd || e.ParentDispatch.Source != lineage.SourceObserved {
		t.Errorf("parent dispatch source: record %q, entry %q", shown.ParentDispatchSource, e.ParentDispatch.Source)
	}

	text := runAuthorityCmd(t, inst, "show").mustSucceed(t).stdout
	for _, line := range []string{"parent session source: observed\n", "principal: dana@example.com, source inherited, trust observed\n"} {
		if !strings.Contains(text, line) {
			t.Errorf("show lacks %q:\n%s", line, text)
		}
	}
}

// A record written before the fields prints them as unrecorded and omits
// them from --json; one holding unreadable values does the same, and its
// grant and decision are those of the same record without them.
func TestShow_AdvisoryFieldsUnrecorded(t *testing.T) {
	w := newShowWorkspace(t, "")
	older := w.instance(t, "ws+older")
	writeShowRecord(t, older, showRecord(showDispatchA, authority.Supervised, authority.LayerWorkspace, authority.MergePR))
	bad := w.instance(t, "ws+bad")
	b, err := showRecord(showDispatchB, authority.Supervised, authority.LayerWorkspace, authority.MergePR).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(b), `"parent_dispatch_source": "none",`,
		`"parent_dispatch_source": "none", "parent_session_source": 7, "principal": {"name": "has space", "source": "declared", "trust": "observed"},`, 1)
	writeRawRecord(t, bad, body)

	for _, inst := range []string{older, bad} {
		text := runAuthorityCmd(t, inst, "show").mustSucceed(t).stdout
		if !strings.Contains(text, "parent session source: unrecorded\n") || !strings.Contains(text, "principal: unrecorded\n") {
			t.Errorf("%s: show =\n%s", inst, text)
		}
		js := runAuthorityCmd(t, inst, "show", "--json").mustSucceed(t).stdout
		if strings.Contains(js, "parent_session_source") || strings.Contains(js, `"principal"`) {
			t.Errorf("%s: --json printed an unrecorded field:\n%s", inst, js)
		}
		if !strings.Contains(js, `"authority": "merge-pr"`) {
			t.Errorf("%s: grant lost:\n%s", inst, js)
		}
	}
	a := runAuthorityCmd(t, older, "decide", "merge").mustSucceed(t).stdout
	c := runAuthorityCmd(t, bad, "decide", "merge").mustSucceed(t).stdout
	if a != c {
		t.Errorf("decide differs:\n%s\nvs\n%s", a, c)
	}
}
