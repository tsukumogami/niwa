package fallbacknotice

import (
	"fmt"
	"strings"
	"testing"
)

const testReleaseName = "github.com/acme/skills@3f9a2c1e0b1d2c3e4f5a6b7c8d9e0f1a2b3c4d5e"

// Each plugin release outcome renders as one terminal line naming the
// release and the instances that link it; only a restore is a note.
func TestRenderTextReleaseGolden(t *testing.T) {
	instances := []string{"/ws/one", "/ws/two"}
	cases := []struct {
		name string
		note ReleaseNote
		want string
	}{
		{"restored", ReleaseNote{Outcome: ReleaseRestored, Release: testReleaseName, Instances: instances},
			"note: restored plugin release " + testReleaseName + " for instances: /ws/one, /ws/two.\n"},
		{"refused", ReleaseNote{Outcome: ReleaseRefused, Release: testReleaseName, Instances: instances, Reason: "release refused: commit 3f9a2c1e0b1d of acme/skills: it is not in the source's own history"},
			"warning: refused to restore plugin release " + testReleaseName + ", linked from instances: /ws/one, /ws/two: release refused: commit 3f9a2c1e0b1d of acme/skills: it is not in the source's own history; nothing was fetched and the links were left as they are.\n"},
		{"undeclared", ReleaseNote{Outcome: ReleaseUndeclared, Release: testReleaseName, Instances: instances},
			"warning: plugin release " + testReleaseName + ", linked from instances: /ws/one, /ws/two, is from a marketplace source this workspace no longer declares; it was not restored and the links were left as they are.\n"},
		{"not restored", ReleaseNote{Outcome: ReleaseNotRestored, Release: testReleaseName, Instances: instances, Reason: "fetching acme/skills 3f9a2c1e0b1d: boom"},
			"warning: could not restore plugin release " + testReleaseName + ", linked from instances: /ws/one, /ws/two: fetching acme/skills 3f9a2c1e0b1d: boom; nothing was marked held for it.\n"},
		{"tampered", ReleaseNote{Outcome: ReleaseTampered, Release: testReleaseName, Instances: instances},
			"warning: plugin release " + testReleaseName + " was changed after it was placed (tampered with); fetched it again for instances: /ws/one, /ws/two.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New(clock())
			c.PluginRelease(tc.note)
			if c.Empty() {
				t.Fatal("Empty after a plugin release record")
			}
			if got := c.RenderText(); got != tc.want {
				t.Errorf("RenderText:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// No plugin release line tells anyone to run a command: the next
// provisioning command restores a release by itself.
func TestReleaseLinesNameNoCommand(t *testing.T) {
	c := New(clock())
	for _, o := range []ReleaseOutcome{ReleaseRestored, ReleaseRefused, ReleaseUndeclared, ReleaseNotRestored, ReleaseTampered} {
		c.PluginRelease(ReleaseNote{Outcome: o, Release: testReleaseName, Instances: []string{"/ws/one"}, Reason: "x"})
	}
	for _, text := range []string{c.RenderText(), c.RenderContext()} {
		for _, banned := range []string{"niwa apply", "re-run", "rerun", "Run `"} {
			if strings.Contains(text, banned) {
				t.Errorf("rendering names %q:\n%s", banned, text)
			}
		}
	}
}

// Secret-store notices keep their lead and wording; plugin release records
// follow under a lead of their own.
func TestRenderContextKeepsReleaseNotesApart(t *testing.T) {
	c := New(clock())
	c.StoreInWorkTree("/repo/.store")
	c.PluginRelease(ReleaseNote{Outcome: ReleaseRestored, Release: testReleaseName, Instances: []string{"/ws/one"}})

	got := c.RenderContext()
	want := contextLead + "\n\n  - " + fmt.Sprintf(workTreeFormat, "/repo/.store") + "\n" +
		"\n" + releaseContextLead + "\n\n  - restored plugin release " + testReleaseName + " for instances: /ws/one.\n"
	if got != want {
		t.Errorf("RenderContext:\n got %q\nwant %q", got, want)
	}

	only := New(clock())
	only.PluginRelease(ReleaseNote{Outcome: ReleaseRestored, Release: testReleaseName, Instances: []string{"/ws/one"}})
	if text := only.RenderContext(); strings.Contains(text, "secret") || !strings.HasPrefix(text, releaseContextLead) {
		t.Errorf("a plugin-only rendering reads as a secret-store one:\n%s", text)
	}
}

// A tampered release renders under the plugin store's lead in the agent
// context, with the same sentence as its terminal line.
func TestRenderContextTamperedRelease(t *testing.T) {
	c := New(clock())
	c.PluginRelease(ReleaseNote{Outcome: ReleaseTampered, Release: testReleaseName, Instances: []string{"/ws/one", "/ws/two"}})
	want := releaseContextLead + "\n\n  - plugin release " + testReleaseName + " was changed after it was placed (tampered with); fetched it again for instances: /ws/one, /ws/two.\n"
	if got := c.RenderContext(); got != want {
		t.Errorf("RenderContext:\n got %q\nwant %q", got, want)
	}
}

func TestReleaseLineCapsAndCleansInstances(t *testing.T) {
	var many []string
	for i := 0; i < maxListedInstances+3; i++ {
		many = append(many, fmt.Sprintf("/ws/i%d", i))
	}
	many[0] = "/ws/bad\x1b[2Jname"
	c := New(clock())
	c.PluginRelease(ReleaseNote{Outcome: ReleaseRestored, Release: testReleaseName, Instances: many})
	text := c.RenderText()
	if strings.Contains(text, "\x1b") {
		t.Errorf("control character reached the rendering: %q", text)
	}
	if !strings.Contains(text, " and 3 more.") {
		t.Errorf("rendering does not cap the instance list: %q", text)
	}
}

func TestNilCollectorIgnoresReleaseNotes(t *testing.T) {
	var c *Collector
	c.PluginRelease(ReleaseNote{Outcome: ReleaseRestored, Release: testReleaseName})
	c.PluginSweepSkipped("listing workspace root /ws: permission denied")
	if _, ok := c.SweepSkipped(); ok {
		t.Error("a nil collector recorded a skipped sweep")
	}
	if c.ReleaseNotes() != nil || c.RenderText() != "" || c.RenderContext() != "" {
		t.Error("a nil collector recorded or rendered a release note")
	}
}

const testSweepReason = "listing workspace root /ws: open /ws: permission denied"

// A skipped sweep is one warning line naming the path, after the release
// lines; only the first reason in a run is kept.
func TestRenderTextSweepSkipped(t *testing.T) {
	c := New(clock())
	c.PluginSweepSkipped(testSweepReason)
	if c.Empty() {
		t.Fatal("Empty after a skipped sweep")
	}
	want := "warning: the plugin store's sweep of unused releases was skipped: " + testSweepReason + "; nothing was deleted.\n"
	if got := c.RenderText(); got != want {
		t.Errorf("RenderText:\n got %q\nwant %q", got, want)
	}

	c.PluginSweepSkipped("a second reason")
	if reason, ok := c.SweepSkipped(); !ok || reason != testSweepReason {
		t.Errorf("SweepSkipped = %q, %v; want the first reason", reason, ok)
	}

	c.PluginRelease(ReleaseNote{Outcome: ReleaseRestored, Release: testReleaseName, Instances: []string{"/ws/one"}})
	want = "note: restored plugin release " + testReleaseName + " for instances: /ws/one.\n" + want
	if got := c.RenderText(); got != want {
		t.Errorf("RenderText with a release line:\n got %q\nwant %q", got, want)
	}
}

// A sweep that stops after it has already removed releases says how many,
// never that nothing was deleted; one removal takes the singular noun.
func TestRenderTextSweepStoppedAfterRemovals(t *testing.T) {
	c := New(clock())
	c.PluginSweepStopped(testSweepReason, 2)
	want := "warning: the plugin store's sweep of unused releases stopped: " + testSweepReason + "; 2 unused releases had already been removed.\n"
	if got := c.RenderText(); got != want {
		t.Errorf("RenderText:\n got %q\nwant %q", got, want)
	}
	if got := c.SweepRemovedBeforeStop(); got != 2 {
		t.Errorf("SweepRemovedBeforeStop = %d, want 2", got)
	}

	one := New(clock())
	one.PluginSweepStopped(testSweepReason, 1)
	want = "warning: the plugin store's sweep of unused releases stopped: " + testSweepReason + "; 1 unused release had already been removed.\n"
	if got := one.RenderText(); got != want {
		t.Errorf("RenderText with one removal:\n got %q\nwant %q", got, want)
	}

	none := New(clock())
	none.PluginSweepStopped(testSweepReason, 0)
	if got := none.RenderText(); !strings.Contains(got, "; nothing was deleted.") {
		t.Errorf("a stop with no removals should keep the skipped text, got %q", got)
	}
}

// In the agent context the skipped sweep sits under the plugin store's lead,
// after the release lines, with the same sentence.
func TestRenderContextSweepSkipped(t *testing.T) {
	c := New(clock())
	c.PluginRelease(ReleaseNote{Outcome: ReleaseRestored, Release: testReleaseName, Instances: []string{"/ws/one"}})
	c.PluginSweepSkipped(testSweepReason)
	want := releaseContextLead + "\n\n" +
		"  - restored plugin release " + testReleaseName + " for instances: /ws/one.\n" +
		"  - the plugin store's sweep of unused releases was skipped: " + testSweepReason + "; nothing was deleted.\n"
	if got := c.RenderContext(); got != want {
		t.Errorf("RenderContext:\n got %q\nwant %q", got, want)
	}

	only := New(clock())
	only.PluginSweepSkipped(testSweepReason)
	if text := only.RenderContext(); !strings.HasPrefix(text, releaseContextLead) || strings.Contains(text, contextLead) {
		t.Errorf("a sweep-only rendering is not under the plugin store's lead:\n%s", text)
	}
}
