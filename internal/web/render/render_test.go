package render

import (
	"bytes"
	"strings"
	"testing"
)

// TestRenderChange_EscapesScriptInDiff is the load-bearing NFR4 test.
// A change whose diff contains a literal <script>alert(1)</script> tag
// must render with the angle brackets escaped — otherwise a hostile
// diff could execute JavaScript in the operator's browser.
func TestRenderChange_EscapesScriptInDiff(t *testing.T) {
	data := ChangeData{
		ID:      "abcd1234",
		State:   "pending",
		Diff:    "@@ -1,1 +1,1 @@\n-<script>alert(1)</script>\n+ok\n",
		BaseRef: "main",
		HeadRef: "abc123",
	}
	var buf bytes.Buffer
	if err := RenderChange(&buf, data); err != nil {
		t.Fatalf("RenderChange: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("expected escaped &lt;script&gt; in output, got:\n%s", out)
	}
	if strings.Contains(out, "<script>alert(1)") {
		t.Errorf("UNESCAPED <script> tag in output — XSS escape failed:\n%s", out)
	}
}

// TestRenderChange_EscapesBranchName verifies the meta-section escaping
// kicks in for non-diff fields too. A branch with HTML-active chars
// would otherwise inject markup into the page header.
func TestRenderChange_EscapesBranchName(t *testing.T) {
	data := ChangeData{
		ID:      "abcd1234",
		State:   "pending",
		Branch:  `feature/<img src=x onerror=alert(1)>`,
		Diff:    "noop",
		BaseRef: "main",
		HeadRef: "abc123",
	}
	var buf bytes.Buffer
	if err := RenderChange(&buf, data); err != nil {
		t.Fatalf("RenderChange: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "<img src=x") {
		t.Errorf("unescaped <img> tag in output:\n%s", out)
	}
	if !strings.Contains(out, "&lt;img") {
		t.Errorf("expected escaped &lt;img in branch field, got:\n%s", out)
	}
}

// TestRenderChange_EmptyDiffSaysNoChanges asserts the empty-diff path
// shows the documented "no changes" body instead of an empty <pre>.
func TestRenderChange_EmptyDiffSaysNoChanges(t *testing.T) {
	data := ChangeData{ID: "abcd1234", State: "pending", Diff: ""}
	var buf bytes.Buffer
	if err := RenderChange(&buf, data); err != nil {
		t.Fatalf("RenderChange: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "No changes") {
		t.Errorf("empty diff did not produce 'No changes' body:\n%s", out)
	}
}

// TestRenderChange_TruncationTrailerRendersInPre verifies the 4 MiB
// truncate trailer (produced by handleCreateChange in issue #5) lands
// inside the <pre> block when present in the Diff field.
func TestRenderChange_TruncationTrailerRendersInPre(t *testing.T) {
	const trailer = "\n[... diff truncated at 4 MiB ...]\n"
	data := ChangeData{
		ID:    "abcd1234",
		State: "pending",
		Diff:  "real diff content\n" + trailer,
	}
	var buf bytes.Buffer
	if err := RenderChange(&buf, data); err != nil {
		t.Fatalf("RenderChange: %v", err)
	}
	out := buf.String()
	// The escaped trailer must appear inside the diff <pre>.
	if !strings.Contains(out, "diff truncated at 4 MiB") {
		t.Errorf("trailer missing from output:\n%s", out)
	}
	// Locate the <pre> and confirm the trailer lives inside it.
	preStart := strings.Index(out, `<pre class="diff">`)
	preEnd := strings.Index(out, "</pre>")
	if preStart == -1 || preEnd == -1 || preEnd < preStart {
		t.Fatalf("could not locate <pre> bracketing in output:\n%s", out)
	}
	if !strings.Contains(out[preStart:preEnd], "diff truncated at 4 MiB") {
		t.Errorf("trailer not inside <pre> block; output:\n%s", out)
	}
}

// TestRenderIndex_OrdersCleanedAfterNonCleaned asserts the renderer
// puts cleaned changes after non-cleaned ones for identical updated_at
// values — the load-bearing index-ordering contract from F5.
func TestRenderIndex_OrdersCleanedAfterNonCleaned(t *testing.T) {
	data := IndexData{
		Changes: []ChangeSummary{
			{ID: "aaa", State: "cleaned", UpdatedAt: "2026-04-24T00:00:00Z"},
			{ID: "bbb", State: "pending", UpdatedAt: "2026-04-24T00:00:00Z"},
			{ID: "ccc", State: "cleaned", UpdatedAt: "2026-04-24T00:00:00Z"},
			{ID: "ddd", State: "in-review", UpdatedAt: "2026-04-24T00:00:00Z"},
		},
	}
	var buf bytes.Buffer
	if err := RenderIndex(&buf, data); err != nil {
		t.Fatalf("RenderIndex: %v", err)
	}
	out := buf.String()
	iBBB := strings.Index(out, "bbb")
	iDDD := strings.Index(out, "ddd")
	iAAA := strings.Index(out, "aaa")
	iCCC := strings.Index(out, "ccc")
	if iBBB == -1 || iDDD == -1 || iAAA == -1 || iCCC == -1 {
		t.Fatalf("missing IDs in output:\n%s", out)
	}
	// Both non-cleaned (bbb, ddd) must precede both cleaned (aaa, ccc).
	if !(iBBB < iAAA && iBBB < iCCC && iDDD < iAAA && iDDD < iCCC) {
		t.Errorf("cleaned changes not ordered after non-cleaned:\nbbb=%d ddd=%d aaa=%d ccc=%d\n%s",
			iBBB, iDDD, iAAA, iCCC, out)
	}
}

// TestRenderIndex_EmptyShowsNoChangesYet confirms the empty-index path
// produces a user-visible "no changes yet" message rather than a blank
// list.
func TestRenderIndex_EmptyShowsNoChangesYet(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderIndex(&buf, IndexData{}); err != nil {
		t.Fatalf("RenderIndex: %v", err)
	}
	if !strings.Contains(buf.String(), "No changes in review yet") {
		t.Errorf("empty index missing 'No changes in review yet' copy:\n%s", buf.String())
	}
}

// TestRenderIndex_CleanedHasCleanedClass verifies the <li>.cleaned
// class is emitted for cleaned rows so the CSS opacity rule applies.
func TestRenderIndex_CleanedHasCleanedClass(t *testing.T) {
	data := IndexData{
		Changes: []ChangeSummary{
			{ID: "aaa", State: "cleaned", UpdatedAt: "2026-04-24T00:00:00Z"},
		},
	}
	var buf bytes.Buffer
	if err := RenderIndex(&buf, data); err != nil {
		t.Fatalf("RenderIndex: %v", err)
	}
	if !strings.Contains(buf.String(), `<li class="cleaned">`) {
		t.Errorf("cleaned row missing class:\n%s", buf.String())
	}
}

func TestCSS_NonEmpty(t *testing.T) {
	if CSS() == "" {
		t.Error("CSS() returned empty string — styles.css did not embed")
	}
}

// TestRenderChange_InjectsCSS confirms the embedded stylesheet lands
// in the rendered page <head>. We check for one distinctive style rule.
func TestRenderChange_InjectsCSS(t *testing.T) {
	data := ChangeData{ID: "abcd1234", State: "pending", Diff: "x"}
	var buf bytes.Buffer
	if err := RenderChange(&buf, data); err != nil {
		t.Fatalf("RenderChange: %v", err)
	}
	if !strings.Contains(buf.String(), "pre.diff") {
		t.Errorf("rendered page missing CSS rule:\n%s", buf.String())
	}
}
