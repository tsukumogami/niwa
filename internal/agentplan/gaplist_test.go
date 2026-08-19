package agentplan

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tsukumogami/niwa/internal/agent"
)

// The drift test is the mechanism that makes the guide's honesty structural.
// Editing the generated section by hand, or flipping a declaration without
// regenerating, fails here rather than shipping a document that describes a
// version of niwa nobody is running.

const (
	// gapGuidePath is the committed guide, relative to this package.
	gapGuidePath = "../../docs/guides/codex-agent.md"

	// gapBeginMarker and gapEndMarker delimit the generated section. They name
	// the generator so a reader who lands on the section mid-file knows where
	// the words came from and what to run.
	gapBeginMarker = "<!-- BEGIN GENERATED: codex gap list (internal/agentplan/gaplist.go) -->"
	gapEndMarker   = "<!-- END GENERATED: codex gap list -->"

	// regenerateCmd is the one command that fixes a drift failure. It is in the
	// failure message because a test that says "these differ" without saying
	// what to run is a test people work around.
	regenerateCmd = "go test ./internal/agentplan -run TestCodexGuideGapSectionMatchesDeclarations -update"
)

// updateGuide rewrites the committed section instead of asserting on it. The
// write lives in a test file on purpose: internal/agentplan itself never writes
// to disk, and the layout scan enforces that over the package's non-test files.
var updateGuide = flag.Bool("update", false, "rewrite the generated section of docs/guides/codex-agent.md from the declaration table")

func TestCodexGuideGapSectionMatchesDeclarations(t *testing.T) {
	body, err := RenderGapSection(agent.AgentCodex)
	if err != nil {
		t.Fatalf("rendering the gap section: %v", err)
	}
	want := "\n\n" + body + "\n"

	raw, err := os.ReadFile(gapGuidePath)
	if err != nil {
		t.Fatalf("reading %s: %v", gapGuidePath, err)
	}
	start, end, err := generatedSectionBounds(string(raw))
	if err != nil {
		t.Fatalf("%s: %v", gapGuidePath, err)
	}
	got := string(raw)[start:end]

	if *updateGuide {
		if got == want {
			t.Logf("%s is already current", gapGuidePath)
			return
		}
		updated := string(raw)[:start] + want + string(raw)[end:]
		if err := os.WriteFile(gapGuidePath, []byte(updated), 0o644); err != nil {
			t.Fatalf("rewriting %s: %v", gapGuidePath, err)
		}
		t.Logf("rewrote the generated section of %s", gapGuidePath)
		return
	}

	if got != want {
		t.Errorf("the generated section of %s has drifted from the declaration table.\n%s\nRegenerate with:\n  %s",
			gapGuidePath, firstDifference(got, want), regenerateCmd)
	}
}

// TestCodexGuideCarriesEveryUnavailableReason is the coarse check behind the
// exact one: every unavailable Codex declaration's reason text appears in the
// committed guide. It fails for a different cause than the drift test -- a
// rendering change that dropped a row would still produce a self-consistent
// section -- so keeping both means "the guide is complete" is asserted
// independently of "the guide matches the renderer".
func TestCodexGuideCarriesEveryUnavailableReason(t *testing.T) {
	raw, err := os.ReadFile(gapGuidePath)
	if err != nil {
		t.Fatalf("reading %s: %v", gapGuidePath, err)
	}
	// The guide is wrapped, so a reason spans line breaks in the file. Both
	// sides are flattened to single-spaced text before the comparison, which
	// makes this check about the words being present rather than about where
	// the renderer happened to break the line.
	guide := flattenWhitespace(string(raw))
	for _, d := range declarations {
		if d.Agent != agent.AgentCodex || d.State != StateUnavailable {
			continue
		}
		if !strings.Contains(guide, flattenWhitespace(d.Reason)) {
			t.Errorf("capability %s is unavailable for codex, but its reason is missing from %s: %q",
				d.Capability, gapGuidePath, d.Reason)
		}
	}
}

// TestEveryCapabilityHasAGuideSubject keeps the subject table from falling
// behind the closed set. A capability added without one would render as an
// error rather than as a line, and this fails first with the name in it.
func TestEveryCapabilityHasAGuideSubject(t *testing.T) {
	for _, c := range All() {
		subject, ok := gapSubjects[c]
		if !ok || subject == "" {
			t.Errorf("capability %s has no guide subject in gapSubjects", c)
			continue
		}
		if strings.HasSuffix(subject, ".") {
			t.Errorf("capability %s: guide subject ends in a period, which the renderer adds: %q", c, subject)
		}
	}
	for c := range gapSubjects {
		if _, ok := c.row(); !ok {
			t.Errorf("gapSubjects names %s, which is not in the closed set", c)
		}
	}
}

// TestUnavailableForFiltersAndOrders pins what the filter is: only unavailable
// rows for the agent asked about, in matrix order, each carrying its own
// declaration's reason.
func TestUnavailableForFiltersAndOrders(t *testing.T) {
	entries, err := UnavailableFor(agent.AgentCodex)
	if err != nil {
		t.Fatalf("UnavailableFor(codex): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no unavailable codex capabilities; the guide would render empty")
	}
	order := All()
	at := 0
	for _, e := range entries {
		for at < len(order) && order[at] != e.Capability {
			at++
		}
		if at == len(order) {
			t.Fatalf("entries are not in matrix order: %s came out of sequence", e.Capability)
		}
		d, err := Lookup(e.Capability, agent.AgentCodex)
		if err != nil {
			t.Fatalf("Lookup(%s, codex): %v", e.Capability, err)
		}
		if d.State != StateUnavailable {
			t.Errorf("%s is implemented for codex but appears in the gap list", e.Capability)
		}
		if e.Reason != d.Reason || e.Kind != d.Kind {
			t.Errorf("%s: entry (%v, %q) does not match the declaration (%v, %q)",
				e.Capability, e.Kind, e.Reason, d.Kind, d.Reason)
		}
	}
}

// TestGapSectionSeparatesNoSuchConceptFromGaps asserts the split the reason
// kinds exist for: a no-such-concept row reads as a note that the capability
// doesn't apply, and a real gap does not, so a reader counting what they lose
// isn't handed five items no work will ever remove.
func TestGapSectionSeparatesNoSuchConceptFromGaps(t *testing.T) {
	section, err := RenderGapSection(agent.AgentCodex)
	if err != nil {
		t.Fatalf("rendering the gap section: %v", err)
	}
	entries, err := UnavailableFor(agent.AgentCodex)
	if err != nil {
		t.Fatalf("UnavailableFor(codex): %v", err)
	}
	for _, e := range entries {
		line := renderGapLine(e, "Codex")
		if !strings.Contains(section, wrapBullet(line)+"\n") {
			t.Errorf("%s: rendered line missing from the section: %q", e.Capability, line)
		}
		applies := strings.Contains(line, "doesn't apply to Codex")
		if e.Kind == ReasonNoSuchConcept && !applies {
			t.Errorf("%s: no-such-concept row does not read as a does-not-apply note: %q", e.Capability, line)
		}
		if e.Kind != ReasonNoSuchConcept && applies {
			t.Errorf("%s: a real gap reads as a does-not-apply note: %q", e.Capability, line)
		}
	}
}

// TestUnavailableForRejectsUnknownAgent keeps the generator fail-closed for the
// same reason Lookup is: an unrecognized agent must not render an empty gap
// list, which would read as "this agent gets everything".
func TestUnavailableForRejectsUnknownAgent(t *testing.T) {
	if _, err := UnavailableFor(agent.Agent("gemini")); err == nil {
		t.Fatal("UnavailableFor accepted an agent outside the closed set")
	}
	if _, err := RenderGapSection(agent.Agent("gemini")); err == nil {
		t.Fatal("RenderGapSection accepted an agent outside the closed set")
	}
}

// TestGeneratedSectionWrapsToTheGuidesWidth keeps the generated block reviewable
// as a diff beside the hand-written paragraphs around it. A single word longer
// than the width is allowed through -- wrap never cuts one -- so the check is
// about reflow, not about a hard column limit.
func TestGeneratedSectionWrapsToTheGuidesWidth(t *testing.T) {
	section, err := RenderGapSection(agent.AgentCodex)
	if err != nil {
		t.Fatalf("rendering the gap section: %v", err)
	}
	for i, line := range strings.Split(section, "\n") {
		if utf8.RuneCountInString(line) <= gapWrapWidth {
			continue
		}
		if len(strings.Fields(line)) <= 1 {
			continue
		}
		t.Errorf("section line %d is %d columns wide, want at most %d: %q",
			i+1, utf8.RuneCountInString(line), gapWrapWidth, line)
	}
}

// TestWrapHangsContinuationsAndKeepsLongWordsWhole pins the two wrap behaviors
// the section depends on, without going through the whole renderer to see them.
func TestWrapHangsContinuationsAndKeepsLongWordsWhole(t *testing.T) {
	long := strings.Repeat("x", gapWrapWidth+10)
	if got := wrap(long, "- ", "  "); got != "- "+long {
		t.Errorf("wrap cut a word longer than the width: %q", got)
	}
	text := strings.TrimSpace(strings.Repeat("word ", 40))
	got := wrap(text, "- ", "  ")
	lines := strings.Split(got, "\n")
	if len(lines) < 2 {
		t.Fatalf("wrap produced one line for %d words", 40)
	}
	if !strings.HasPrefix(lines[0], "- ") {
		t.Errorf("first line lost its prefix: %q", lines[0])
	}
	for _, l := range lines[1:] {
		if !strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "   ") {
			t.Errorf("continuation line is not hung under the text: %q", l)
		}
	}
}

// generatedSectionBounds returns the byte offsets of the generated section's
// content: just after the begin marker, just before the end marker. Each marker
// must appear exactly once, so a duplicated or half-deleted marker fails loudly
// instead of silently checking the wrong span.
func generatedSectionBounds(guide string) (start, end int, err error) {
	for _, marker := range []string{gapBeginMarker, gapEndMarker} {
		if n := strings.Count(guide, marker); n != 1 {
			return 0, 0, fmt.Errorf("marker %q appears %d times, want exactly 1", marker, n)
		}
	}
	start = strings.Index(guide, gapBeginMarker) + len(gapBeginMarker)
	end = strings.Index(guide, gapEndMarker)
	if end < start {
		return 0, 0, fmt.Errorf("the end marker precedes the begin marker")
	}
	return start, end, nil
}

// firstDifference names the first line the two sides disagree on. A whole-text
// dump of two nearly identical sections is unreadable in test output; the line
// number and the two versions of that line are what a reader needs.
func firstDifference(got, want string) string {
	gotLines := strings.Split(got, "\n")
	wantLines := strings.Split(want, "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		g, w := lineAt(gotLines, i), lineAt(wantLines, i)
		if g == w {
			continue
		}
		return fmt.Sprintf("first difference at section line %d:\n  committed: %q\n  generated: %q", i+1, g, w)
	}
	return "the sections differ but no differing line was found"
}

// flattenWhitespace collapses every run of whitespace to a single space, so
// wrapped prose can be searched for a sentence written on one line.
func flattenWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// lineAt returns the i-th line, or a marker for one side running out first.
func lineAt(lines []string, i int) string {
	if i >= len(lines) {
		return "<end of section>"
	}
	return lines[i]
}
