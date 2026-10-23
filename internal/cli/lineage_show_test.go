package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
)

const (
	lnA = "a0000000-0000-4000-8000-00000000000a"
	lnB = "b0000000-0000-4000-8000-00000000000b"
	lnC = "c0000000-0000-4000-8000-00000000000c"
	lnD = "d0000000-0000-4000-8000-00000000000d"
	lnS = "5e550000-0000-4000-8000-000000000001"
	lnT = "5e550000-0000-4000-8000-000000000002"
)

// lnEntry is an entry for instance written at hh:00, its parent dispatch
// and parent session observed when set.
func lnEntry(id, instance string, hour int, session, parentDispatch, parentSession string) lineage.Entry {
	e := lineage.Entry{
		Schema:         lineage.SchemaEntryV1,
		DispatchID:     id,
		Instance:       instance,
		Agent:          "claude",
		Origin:         lineage.OriginDispatch,
		WrittenAt:      fmt.Sprintf("2026-10-23T%02d:00:00Z", hour),
		SessionID:      session,
		ParentSession:  lineage.Link{Source: lineage.SourceNone},
		ParentDispatch: lineage.Link{Source: lineage.SourceNone},
		Principal:      lineage.Principal{Name: "dana", Source: lineage.PrincipalDeclared, Trust: lineage.TrustObserved},
	}
	if parentDispatch != "" {
		e.ParentDispatch = lineage.Link{ID: parentDispatch, Source: lineage.SourceObserved}
	}
	if parentSession != "" {
		e.ParentSession = lineage.Link{ID: parentSession, Source: lineage.SourceObserved}
	}
	return e
}

func putEntry(t *testing.T, root string, e lineage.Entry) {
	t.Helper()
	if err := workspace.CreateLineageEntry(root, e); err != nil {
		t.Fatal(err)
	}
}

// putRawEntry writes body as id's entry file, as a hand edit would.
func putRawEntry(t *testing.T, root, id, body string) {
	t.Helper()
	if err := os.MkdirAll(workspace.LineageDir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace.LineageDir(root), id+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

type lnChain struct {
	Schema   string            `json:"schema"`
	Chain    []json.RawMessage `json:"chain"`
	Stop     string            `json:"stop"`
	Matched  int               `json:"matched"`
	Matches  []string          `json:"matches"`
	Fallback bool              `json:"instance_name_fallback"`
}

func (c lnChain) ids(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, l := range c.Chain {
		var m struct {
			ID string `json:"dispatch_id"`
		}
		if err := json.Unmarshal(l, &m); err != nil {
			t.Fatal(err)
		}
		out = append(out, m.ID)
	}
	return out
}

func showChain(t *testing.T, args ...string) lnChain {
	t.Helper()
	out, err := runLineageShowCmd(t, true, args...)
	if err != nil {
		t.Fatalf("show --json %v: %v\n%s", args, err, out)
	}
	var c lnChain
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if c.Schema != lineageChainSchemaV1 {
		t.Errorf("schema %q", c.Schema)
	}
	compactAll(t, c.Chain)
	return c
}

// compactAll compacts each object in place, so a test compares it to a
// one-line literal.
func compactAll(t *testing.T, objs []json.RawMessage) {
	t.Helper()
	for i, o := range objs {
		var b bytes.Buffer
		if err := json.Compact(&b, o); err != nil {
			t.Fatal(err)
		}
		objs[i] = b.Bytes()
	}
}

func showText(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runLineageShowCmd(t, false, args...)
	if err != nil {
		t.Fatalf("show %v: %v\n%s", args, err, out)
	}
	return out
}

func wantShowFail(t *testing.T, code int, msg string, args ...string) {
	t.Helper()
	_, err := runLineageShowCmd(t, false, args...)
	if exitCode(t, err) != code || !strings.Contains(fmt.Sprint(err), msg) {
		t.Errorf("show %v: err %v, want exit %d with %q", args, err, code, msg)
	}
}

func TestLineageShow_Selectors(t *testing.T) {
	w := newShowWorkspace(t, "")
	one := w.instance(t, "ws+one")
	two := w.instance(t, "ws+two")
	gone := w.instance(t, "ws+gone")
	writeShowRecord(t, two, showRecord(lnC, authority.Supervised, authority.LayerWorkspace))
	writeShowRecord(t, gone, showRecord(lnD, authority.Supervised, authority.LayerWorkspace))
	putEntry(t, w.root, lnEntry(lnA, "ws+one", 12, lnS, "", ""))
	putEntry(t, w.root, lnEntry(lnB, "ws+one", 13, lnS, "", ""))
	putEntry(t, w.root, lnEntry(lnC, "ws+two", 13, lnT, "", ""))
	t.Chdir(w.root)

	t.Run("dispatch id", func(t *testing.T) {
		c := showChain(t, lnA)
		if !reflect.DeepEqual(c.ids(t), []string{lnA}) || c.Matched != 1 || !reflect.DeepEqual(c.Matches, []string{lnA}) || c.Fallback {
			t.Errorf("%+v", c)
		}
	})
	for _, args := range [][]string{{"--session", lnS}, {"--instance", "ws+one"}} {
		t.Run(args[0], func(t *testing.T) {
			c := showChain(t, args...)
			if !reflect.DeepEqual(c.ids(t), []string{lnB}) || c.Matched != 2 || !reflect.DeepEqual(c.Matches, []string{lnB, lnA}) || c.Fallback {
				t.Errorf("%+v", c)
			}
			text := showText(t, args...)
			if !strings.HasPrefix(text, "2 entries matched; showing the latest\n") || !strings.Contains(text, lnA) {
				t.Errorf("text:\n%s", text)
			}
		})
	}
	t.Run("record of the working directory's instance", func(t *testing.T) {
		t.Chdir(filepath.Join(two, "tools", "app"))
		c := showChain(t)
		if !reflect.DeepEqual(c.ids(t), []string{lnC}) || c.Fallback {
			t.Errorf("%+v", c)
		}
	})
	t.Run("-C", func(t *testing.T) {
		c := showChain(t, "-C", filepath.Join(two, "tools"))
		if !reflect.DeepEqual(c.ids(t), []string{lnC}) || c.Fallback {
			t.Errorf("%+v", c)
		}
	})
	t.Run("instance-name fallback", func(t *testing.T) {
		t.Chdir(one)
		c := showChain(t)
		if !reflect.DeepEqual(c.ids(t), []string{lnB}) || !c.Fallback || c.Matched != 2 {
			t.Errorf("%+v", c)
		}
		if text := showText(t); !strings.Contains(text, "selected by instance name\n") {
			t.Errorf("text:\n%s", text)
		}
	})
	t.Run("no match", func(t *testing.T) {
		wantShowFail(t, 1, "no lineage entry", lnD)
		wantShowFail(t, 1, "no lineage entry", "--session", "5e550000-0000-4000-8000-000000000009")
		wantShowFail(t, 1, "no lineage entry", "--instance", "ws+none")
		wantShowFail(t, 1, "no lineage entry") // the workspace root
		t.Chdir(gone)
		wantShowFail(t, 1, "no lineage entry") // its record's dispatch id has no entry
	})
	t.Run("usage", func(t *testing.T) {
		for _, args := range [][]string{
			{lnA, "--session", lnS}, {"--session", lnS, "--instance", "ws+one"},
			{"-C", one, "--instance", "ws+one"}, {lnA, "-C", one}, {lnA, lnB},
		} {
			wantShowFail(t, 2, "", args...)
		}
	})
	t.Run("bad -C", func(t *testing.T) {
		wantShowFail(t, 1, "-C is empty", "-C", "")
		wantShowFail(t, 1, "not an absolute path", "-C", "ws+one")
		wantShowFail(t, 1, "does not exist", "-C", filepath.Join(w.root, "missing"))
	})
	t.Run("bad --session", func(t *testing.T) {
		wantShowFail(t, 1, "invalid --session", "--session", "has space")
	})
}

func TestLineageShow_TiesGoToTheLowerDispatchID(t *testing.T) {
	w := newShowWorkspace(t, "")
	putEntry(t, w.root, lnEntry(lnB, "ws+one", 12, lnS, "", ""))
	putEntry(t, w.root, lnEntry(lnA, "ws+one", 12, lnS, "", ""))
	t.Chdir(w.root)
	if c := showChain(t, "--session", lnS); !reflect.DeepEqual(c.Matches, []string{lnA, lnB}) || c.ids(t)[0] != lnA {
		t.Errorf("%+v", c)
	}
}

func TestLineageShow_NoStoreAndOutsideAWorkspace(t *testing.T) {
	w := newShowWorkspace(t, "")
	t.Chdir(w.root)
	wantShowFail(t, 1, "no lineage entry", "--session", lnS)
	wantShowFail(t, 1, "no lineage entry", lnA)
	t.Chdir(t.TempDir())
	wantShowFail(t, 1, "not inside a niwa workspace", lnA)
}

func TestLineageShow_WalksToTheRoot(t *testing.T) {
	w := newShowWorkspace(t, "")
	putEntry(t, w.root, lnEntry(lnA, "ws+a", 10, lnS, "", "5e550000-0000-4000-8000-0000000000aa"))
	putEntry(t, w.root, lnEntry(lnB, "ws+b", 11, lnT, lnA, lnS))
	putEntry(t, w.root, lnEntry(lnC, "ws+c", 12, "", lnB, lnT))
	t.Chdir(w.root)
	c := showChain(t, lnC)
	if !reflect.DeepEqual(c.ids(t), []string{lnC, lnB, lnA}) || c.Stop != "root" {
		t.Errorf("%+v", c)
	}
	text := showText(t, lnC)
	for _, want := range []string{"directed by " + lnT, "directed by " + lnS, "directed by 5e550000-0000-4000-8000-0000000000aa"} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "dispatch "+lnC) > strings.Index(text, "dispatch "+lnB) || strings.Index(text, "dispatch "+lnB) > strings.Index(text, "dispatch "+lnA) {
		t.Errorf("not nearest first:\n%s", text)
	}
}

func TestLineageShow_StopReasons(t *testing.T) {
	setup := func(t *testing.T) string {
		w := newShowWorkspace(t, "")
		putEntry(t, w.root, lnEntry(lnB, "ws+b", 12, "", lnA, ""))
		t.Chdir(w.root)
		return w.root
	}
	t.Run("not on record", func(t *testing.T) {
		setup(t)
		c := showChain(t, lnB)
		if c.Stop != "not-on-record" || len(c.Chain) != 2 || string(c.Chain[1]) != `{"dispatch_id":"`+lnA+`","error":"not on record"}` {
			t.Errorf("%+v %s", c, c.Chain)
		}
		if text := showText(t, lnB); !strings.HasSuffix(text, lnA+" not on record\n") {
			t.Errorf("text:\n%s", text)
		}
	})
	t.Run("unreadable ancestor", func(t *testing.T) {
		root := setup(t)
		putRawEntry(t, root, lnA, `{"schema":"niwa.lineage.entry/v1","dispatch_id":"`+lnA+`","instance":"has space"}`)
		c := showChain(t, lnB)
		if c.Stop != "unreadable" || !strings.Contains(string(c.Chain[1]), `"error":"lineage entry is malformed"`) {
			t.Errorf("%+v %s", c, c.Chain)
		}
		if text := showText(t, lnB); !strings.HasSuffix(text, lnA+" unreadable: lineage entry is malformed\n") {
			t.Errorf("text:\n%s", text)
		}
	})
	t.Run("path in a parent id", func(t *testing.T) {
		root := setup(t)
		body, _ := lineage.Marshal(lnEntry(lnC, "ws+c", 13, "", lnB, ""))
		putRawEntry(t, root, lnC, strings.Replace(string(body), `"id":"`+lnB+`"`, `"id":"../../etc/passwd"`, 1))
		wantShowFail(t, 1, "unreadable", lnC)
	})
	t.Run("later version ancestor", func(t *testing.T) {
		root := setup(t)
		putRawEntry(t, root, lnA, `{"schema":"niwa.lineage.entry/v2","dispatch_id":"`+lnA+`","secret":"x"}`)
		c := showChain(t, lnB)
		if c.Stop != "later-version" || string(c.Chain[1]) != `{"dispatch_id":"`+lnA+`","schema":"niwa.lineage.entry/v2","known":false}` {
			t.Errorf("%+v %s", c, c.Chain)
		}
		if text := showText(t, lnB); !strings.HasSuffix(text, lnA+" later version niwa.lineage.entry/v2\n") {
			t.Errorf("text:\n%s", text)
		}
	})
	t.Run("cycle", func(t *testing.T) {
		root := setup(t)
		putEntry(t, root, lnEntry(lnA, "ws+a", 11, "", lnB, ""))
		c := showChain(t, lnB)
		if c.Stop != "cycle" || !reflect.DeepEqual(c.ids(t), []string{lnB, lnA}) {
			t.Errorf("%+v", c)
		}
		if text := showText(t, lnB); !strings.HasSuffix(text, "stopped: cycle at "+lnB+"\n") {
			t.Errorf("text:\n%s", text)
		}
	})
}

func TestLineageShow_Depth(t *testing.T) {
	id := func(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }
	build := func(t *testing.T, n int) {
		w := newShowWorkspace(t, "")
		for i := 1; i <= n; i++ {
			parent := ""
			if i < n {
				parent = id(i + 1)
			}
			putEntry(t, w.root, lnEntry(id(i), "ws+x", 12, "", parent, ""))
		}
		t.Chdir(w.root)
	}
	t.Run("70 entries", func(t *testing.T) {
		build(t, 70)
		c := showChain(t, id(1))
		if len(c.Chain) != 64 || c.Stop != "depth" {
			t.Errorf("%d levels, stop %q", len(c.Chain), c.Stop)
		}
		if text := showText(t, id(1)); !strings.HasSuffix(text, "stopped: 64 levels\n") || strings.Count(text, "\ndispatch ")+1 != 64 {
			t.Errorf("text ends:\n%s", text[len(text)-200:])
		}
	})
	t.Run("exactly 64", func(t *testing.T) {
		build(t, 64)
		if c := showChain(t, id(1)); len(c.Chain) != 64 || c.Stop != "root" {
			t.Errorf("%d levels, stop %q", len(c.Chain), c.Stop)
		}
	})
}

func TestLineageShow_SelectedLaterVersionAndUnreadable(t *testing.T) {
	w := newShowWorkspace(t, "")
	putRawEntry(t, w.root, lnA, `{"schema":"niwa.lineage.entry/v2","dispatch_id":"`+lnA+`"}`)
	putRawEntry(t, w.root, lnB, `{"schema":"niwa.lineage.entry/v1","dispatch_id":"`+lnB+`","agent":"Claude Code"}`)
	t.Chdir(w.root)
	if c := showChain(t, lnA); c.Stop != "later-version" {
		t.Errorf("%+v", c)
	}
	out, err := runLineageShowCmd(t, true, lnB)
	if exitCode(t, err) != 1 || !strings.Contains(out, `"stop": "unreadable"`) {
		t.Errorf("exit %v\n%s", err, out)
	}
}

// R18: only observed is observed, none on a link prints alone, and the
// principal's source is never annotated; the JSON prints values as stored.
func TestLineageShow_NotObserved(t *testing.T) {
	w := newShowWorkspace(t, "")
	e := lnEntry(lnB, "ws+b", 12, "", lnA, lnS)
	e.ParentSession.Source = "vouched"
	e.ParentDispatch.Source = lineage.SourceMapped
	e.Principal.Trust = "vouched"
	putEntry(t, w.root, e)
	putEntry(t, w.root, lnEntry(lnA, "ws+a", 11, "", "", ""))
	t.Chdir(w.root)
	text := showText(t, lnB)
	for _, want := range []string{
		"parent session:  " + lnS + " vouched (not observed)\n",
		"parent dispatch: " + lnA + " mapped (not observed)\n",
		"principal:       dana, source declared, trust vouched (not observed)\n",
		"parent session:  none\n",
		"principal:       dana, source declared, trust observed\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "declared (not observed)") || strings.Contains(text, "none (not observed)") {
		t.Errorf("annotated a provenance label or none:\n%s", text)
	}
	out, err := runLineageShowCmd(t, true, lnB)
	if err != nil || !strings.Contains(out, `"source": "vouched"`) || !strings.Contains(out, `"trust": "vouched"`) || strings.Contains(out, "not observed") {
		t.Errorf("json %v:\n%s", err, out)
	}
}

func TestLineageList(t *testing.T) {
	w := newShowWorkspace(t, "")
	t.Chdir(w.root)
	list := func(t *testing.T, args ...string) string {
		out, err := runLineageCmd(t, newLineageListCmd(), args...)
		if err != nil {
			t.Fatalf("list %v: %v", args, err)
		}
		return out
	}
	if out := list(t); out != "" {
		t.Errorf("no store listed %q", out)
	}
	if out := list(t, "--json"); out != "{\n  \"schema\": \"niwa.lineage.list/v1\",\n  \"entries\": []\n}\n" {
		t.Errorf("no store json %q", out)
	}

	putEntry(t, w.root, lnEntry(lnA, "ws+a", 10, lnS, "", ""))
	putEntry(t, w.root, lnEntry(lnC, "ws+c", 12, "", lnA, lnS))
	putEntry(t, w.root, lnEntry(lnB, "ws+b", 12, "", "", ""))
	putRawEntry(t, w.root, lnD, "{")
	putRawEntry(t, w.root, "e0000000-0000-4000-8000-00000000000e", `{"schema":"niwa.lineage.entry/v2","dispatch_id":"e0000000-0000-4000-8000-00000000000e"}`)
	putRawEntry(t, w.root, "notes", "x")
	if err := os.WriteFile(filepath.Join(workspace.LineageDir(w.root), "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSuffix(list(t), "\n"), "\n")
	want := []string{
		lnB + "  session none  parent session none  principal dana",
		lnC + "  session none  parent session " + lnS + " observed  principal dana",
		lnA + "  session " + lnS + "  parent session none  principal dana",
		lnD + "  unreadable: lineage entry is malformed",
		"e0000000-0000-4000-8000-00000000000e  later version niwa.lineage.entry/v2",
	}
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("list =\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}

	var doc struct {
		Schema  string            `json:"schema"`
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal([]byte(list(t, "--json")), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != lineageListSchemaV1 || len(doc.Entries) != 5 {
		t.Fatalf("%+v", doc)
	}
	compactAll(t, doc.Entries)
	if got := string(doc.Entries[3]); got != `{"dispatch_id":"`+lnD+`","error":"lineage entry is malformed"}` {
		t.Errorf("unreadable = %s", got)
	}
	if got := string(doc.Entries[4]); got != `{"dispatch_id":"e0000000-0000-4000-8000-00000000000e","schema":"niwa.lineage.entry/v2","known":false}` {
		t.Errorf("later version = %s", got)
	}
	if !strings.Contains(string(doc.Entries[0]), `"dispatch_id":"`+lnB+`"`) {
		t.Errorf("first = %s", doc.Entries[0])
	}

	t.Chdir(t.TempDir())
	if _, err := runLineageCmd(t, newLineageListCmd()); exitCode(t, err) != 1 {
		t.Errorf("outside a workspace: %v", err)
	}
}
