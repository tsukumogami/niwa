package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/removal"
)

func pathLines(pcs []removal.PathClass) []string {
	out := []string{}
	for _, pc := range pcs {
		line := pc.Path + " " + string(pc.Class)
		if pc.Notice != "" {
			line += " (" + string(pc.Notice) + ")"
		}
		out = append(out, line)
	}
	return out
}

func TestCollapsePaths(t *testing.T) {
	pc := func(p string, c removal.Class, n removal.Notice) removal.PathClass {
		return removal.PathClass{Path: p, Class: c, Notice: n}
	}
	for _, tc := range []struct {
		name string
		in   []removal.PathClass
		want []string
	}{
		{
			name: "clean clone, mixed clone, generated records, job directory",
			in: []removal.PathClass{
				pc(".niwa/instance.json", removal.ClassGenerated, ""),
				pc(".niwa/managed.md", removal.ClassGenerated, ""),
				pc("job:scratch.md", removal.ClassDisposable, removal.NoticeJobScratch),
				pc("job:state.json", removal.ClassGenerated, ""),
				pc("notes.txt", removal.ClassUnique, ""),
				pc("tools/app/.env", removal.ClassDisposable, removal.NoticeGitIgnored),
				pc("tools/app/.git/", removal.ClassPushed, ""),
				pc("tools/app/README", removal.ClassTracked, ""),
				pc("tools/app/src/a.go", removal.ClassTracked, ""),
				pc("tools/app/src/b.go", removal.ClassTracked, ""),
				pc("tools/app/target/", removal.ClassBuildTree, ""),
				pc("tools/web/.git/", removal.ClassPushed, ""),
				pc("tools/web/README", removal.ClassTracked, ""),
				pc("tools/web/lib/x.js", removal.ClassTracked, ""),
			},
			want: []string{
				".niwa/ generated",
				"job:scratch.md disposable (job scratch)",
				"job:state.json generated",
				"notes.txt unique",
				"tools/app/.env disposable (git-ignored)",
				"tools/app/.git/ pushed",
				"tools/app/README tracked",
				"tools/app/src/ tracked",
				"tools/app/target/ build tree",
				"tools/web/ tracked",
			},
		},
		{
			name: "two clean clones under one group never collapse above a clone",
			in: []removal.PathClass{
				pc("tools/app/.git/", removal.ClassPushed, ""),
				pc("tools/app/README", removal.ClassTracked, ""),
				pc("tools/web/.git/", removal.ClassPushed, ""),
				pc("tools/web/README", removal.ClassTracked, ""),
			},
			want: []string{"tools/app/ tracked", "tools/web/ tracked"},
		},
		{
			name: "a clone whose git directory isn't pushed doesn't collapse",
			in: []removal.PathClass{
				pc("tools/app/.git/", removal.ClassUnique, ""),
				pc("tools/app/README", removal.ClassTracked, ""),
			},
			want: []string{"tools/app/.git/ unique", "tools/app/README tracked"},
		},
		{
			name: "the roots never collapse, and an entry on a root prints as it is",
			in: []removal.PathClass{
				pc("a.txt", removal.ClassGenerated, ""),
				pc("b.txt", removal.ClassGenerated, ""),
				pc("job:", removal.ClassUnique, ""),
			},
			want: []string{"a.txt generated", "b.txt generated", "job: unique"},
		},
		{
			name: "notices must match for a directory to collapse",
			in: []removal.PathClass{
				pc("logs/a.log", removal.ClassDisposable, removal.NoticeGitIgnored),
				pc("logs/b.log", removal.ClassDisposable, ""),
				pc("tmp/x", removal.ClassDisposable, removal.NoticeGitIgnored),
				pc("tmp/y/z", removal.ClassDisposable, removal.NoticeGitIgnored),
			},
			want: []string{
				"logs/a.log disposable (git-ignored)",
				"logs/b.log disposable",
				"tmp/ disposable (git-ignored)",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pathLines(collapsePaths(tc.in))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("collapsePaths =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
			}
		})
	}
}

// verdictPrinterUnits is a kept unit with two checks, a reaped unit and a
// unit the pass didn't reach.
func verdictPrinterUnits() []unitVerdict {
	return []unitVerdict{
		{
			Name:    "test-ws-kept",
			Reached: true,
			Verdict: removal.Verdict{
				Kind: removal.Keep,
				Checks: []removal.Check{
					{Name: removal.CheckRepository, Paths: []string{"tools/app"}, Evidence: "tools/app: commits the clone URL does not show: branch unpushed-work"},
					{Name: removal.CheckOutside, Paths: []string{"notes.txt"}, Evidence: "unique files outside every repository: notes.txt"},
				},
				Paths: []removal.PathClass{
					{Path: "notes.txt", Class: removal.ClassUnique},
					{Path: "tools/app/.git/", Class: removal.ClassUnique},
					{Path: "tools/app/README", Class: removal.ClassTracked},
					{Path: "tools/app/build.log", Class: removal.ClassDisposable, Notice: removal.NoticeGitIgnored},
				},
			},
		},
		{
			Name:    "test-ws-pass",
			Reached: true,
			Verdict: removal.Verdict{
				Kind: removal.Reap,
				Paths: []removal.PathClass{
					{Path: "job:scratch.md", Class: removal.ClassDisposable, Notice: removal.NoticeJobScratch},
					{Path: "tools/app/.git/", Class: removal.ClassPushed},
					{Path: "tools/app/README", Class: removal.ClassTracked},
				},
			},
		},
		{Name: "test-ws-late"},
	}
}

func TestPrintVerdictsText(t *testing.T) {
	var out bytes.Buffer
	printVerdictsText(&out, verdictPrinterUnits())
	want := strings.Join([]string{
		"test-ws-kept: keep",
		"  repository check: tools/app: commits the clone URL does not show: branch unpushed-work",
		"    tools/app",
		"  outside check: unique files outside every repository: notes.txt",
		"    notes.txt",
		"  paths:",
		"    notes.txt unique",
		"    tools/app/.git/ unique",
		"    tools/app/README tracked",
		"    tools/app/build.log disposable (git-ignored)",
		"test-ws-pass: reap",
		"  paths:",
		"    job:scratch.md disposable (job scratch)",
		"    tools/app/ tracked",
		"test-ws-late: not reached",
		"",
	}, "\n")
	if out.String() != want {
		t.Fatalf("text output:\n%s\nwant:\n%s", out.String(), want)
	}
}

// secretUnit carries a secret in its name, a check's evidence, a check's
// path and a path class, and a terminal escape in its evidence.
func secretUnit() unitVerdict {
	return unitVerdict{
		Name:    "ws-ghp_" + strings.Repeat("A1b2", 9),
		Reached: true,
		Verdict: removal.Verdict{
			Checks: []removal.Check{{
				Name:     removal.CheckRepository,
				Paths:    []string{"tools/app/notes-AKIAIOSFODNN7EXAMPLE.txt"},
				Evidence: "tools/app: clone URL unreachable: \x1b[31mhttps://user:hunter2@127.0.0.1/app?access_token=tok123&x=1",
			}},
			Paths: []removal.PathClass{
				{Path: "tools/app/README", Class: removal.ClassTracked},
				{Path: "tools/app/xoxb-1234567890-abcdefghij", Class: removal.ClassUnique},
			},
		},
	}
}

var unitSecrets = []string{"ghp_", "AKIAIOSFODNN7EXAMPLE", "hunter2", "user:", "tok123", "xoxb-", "\x1b", "[31m"}

func TestPrintVerdictsTextScrubsEveryUnitString(t *testing.T) {
	var out bytes.Buffer
	printVerdictsText(&out, []unitVerdict{secretUnit()})
	for _, s := range unitSecrets {
		if strings.Contains(out.String(), s) {
			t.Errorf("text output carries %q:\n%s", s, out.String())
		}
	}
	if !strings.Contains(out.String(), "https://127.0.0.1/app?access_token="+removal.Redacted+"&x=1") {
		t.Errorf("text output lost the URL around the secrets:\n%s", out.String())
	}
}

// verdictsDocMirror is what a consumer of niwa.verdicts/v1 decodes into.
type verdictsDocMirror struct {
	Schema string `json:"schema"`
	Units  []struct {
		Instance string `json:"instance"`
		Verdict  string `json:"verdict"`
		Reached  bool   `json:"reached"`
		Checks   []struct {
			Check    string   `json:"check"`
			Paths    []string `json:"paths"`
			Evidence string   `json:"evidence"`
		} `json:"checks"`
		Paths []struct {
			Path   string `json:"path"`
			Class  string `json:"class"`
			Notice string `json:"notice"`
		} `json:"paths"`
		Over json.RawMessage `json:"over"`
	} `json:"units"`
}

func decodeVerdictsStrictly(t *testing.T, r io.Reader) verdictsDocMirror {
	t.Helper()
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	var doc verdictsDocMirror
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("data after the document: %v", err)
	}
	return doc
}

func TestPrintVerdictsJSONDecodesStrictly(t *testing.T) {
	var out bytes.Buffer
	if err := printVerdictsJSON(&out, append(verdictPrinterUnits(), secretUnit())); err != nil {
		t.Fatal(err)
	}
	raw := out.String()
	doc := decodeVerdictsStrictly(t, strings.NewReader(raw))
	if doc.Schema != "niwa.verdicts/v1" {
		t.Fatalf("schema %q", doc.Schema)
	}
	if len(doc.Units) != 4 {
		t.Fatalf("%d units, want 4", len(doc.Units))
	}
	for i, u := range doc.Units {
		if string(u.Over) != "null" {
			t.Errorf("unit %d over = %s, want null", i, u.Over)
		}
		if u.Checks == nil || u.Paths == nil {
			t.Errorf("unit %d has a missing list: checks %v, paths %v", i, u.Checks, u.Paths)
		}
	}
	kept, pass, late := doc.Units[0], doc.Units[1], doc.Units[2]
	if kept.Instance != "test-ws-kept" || kept.Verdict != "keep" || !kept.Reached || len(kept.Checks) != 2 ||
		kept.Checks[0].Check != "repository" || kept.Checks[1].Check != "outside" ||
		!reflect.DeepEqual(kept.Checks[0].Paths, []string{"tools/app"}) {
		t.Errorf("kept unit %+v", kept)
	}
	if got := kept.Paths[3]; got.Path != "tools/app/build.log" || got.Class != "disposable" || got.Notice != "git-ignored" {
		t.Errorf("kept unit's ignored path %+v", got)
	}
	if pass.Verdict != "reap" || len(pass.Checks) != 0 || len(pass.Paths) != 2 ||
		pass.Paths[0].Notice != "job scratch" || pass.Paths[1].Path != "tools/app/" || pass.Paths[1].Class != "tracked" {
		t.Errorf("reaped unit %+v", pass)
	}
	if late.Instance != "test-ws-late" || late.Reached || late.Verdict != "keep" || len(late.Checks) != 0 || len(late.Paths) != 0 {
		t.Errorf("unreached unit %+v", late)
	}
	for _, s := range unitSecrets {
		if strings.Contains(raw, s) || strings.Contains(raw, strings.ReplaceAll(s, "\x1b", `\u001b`)) {
			t.Errorf("JSON output carries %q:\n%s", s, raw)
		}
	}

	// Every element carries the over key, null, not left out.
	var loose struct {
		Units []map[string]any `json:"units"`
	}
	if err := json.Unmarshal(out.Bytes(), &loose); err != nil {
		t.Fatal(err)
	}
	for i, u := range loose.Units {
		if v, ok := u["over"]; !ok || v != nil {
			t.Errorf("unit %d over = %v, present %v; want a null over", i, v, ok)
		}
	}
}

func TestPrintVerdictsJSONWithNoUnits(t *testing.T) {
	var out bytes.Buffer
	if err := printVerdictsJSON(&out, nil); err != nil {
		t.Fatal(err)
	}
	if doc := decodeVerdictsStrictly(t, &out); doc.Units == nil || len(doc.Units) != 0 {
		t.Fatalf("units %v; want an empty list", doc.Units)
	}
}
