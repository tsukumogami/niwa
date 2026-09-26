package workspace

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/plugin"
)

func TestEmitRank2Notice_LogsAllRequiredSubstrings(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewReporter(&buf)

	EmitRank2Notice(NoticeIDRank2TeamConfig, "org/legacy", reporter)

	out := buf.String()
	for _, want := range []string{"note:", "deprecated", "org/legacy", "/niwa:migrate-config"} {
		if !strings.Contains(out, want) {
			t.Errorf("emitted message %q missing substring %q", out, want)
		}
	}
}

func TestEmitRank2Notice_NilReporterIsNoOp(t *testing.T) {
	// No panic, no output to a missing reporter.
	EmitRank2Notice(NoticeIDRank2TeamConfig, "org/legacy", nil)
}

func TestEmitPluginNotice_InstalledLogsExpectedText(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewReporter(&buf)

	EmitPluginNotice(NoticeIDPluginInstalled, "niwa plugins install", reporter)

	if !strings.Contains(buf.String(), "installed at") {
		t.Errorf("installed notice missing install confirmation text: %q", buf.String())
	}
}

func TestEmitPluginNotice_SkippedIncludesManualCmd(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewReporter(&buf)

	EmitPluginNotice(NoticeIDPluginSkipped, "niwa plugins install", reporter)

	if !strings.Contains(buf.String(), "niwa plugins install") {
		t.Errorf("skipped notice missing manual cmd: %q", buf.String())
	}
}

func TestEmitPluginNotice_NilReporterIsNoOp(t *testing.T) {
	// No panic when reporter is missing.
	EmitPluginNotice(NoticeIDPluginInstalled, "niwa plugins install", nil)
}

// The installer reports by returning an Action and the caller turns
// that into a notice, so this mapping is what the user actually
// hears. All four actions are covered: an unmapped one would leave a
// rank-2 apply silent about the plugin it just installed or skipped.
func TestEmitPluginInstallNotice_MapsEveryAction(t *testing.T) {
	cases := []struct {
		name   string
		action plugin.Action
		want   string
	}{
		{"installed", plugin.Installed, "installed at"},
		{"up to date", plugin.UpToDate, "installed at"},
		{"skipped", plugin.Skipped, plugin.ManualInstallCommand},
		{"failed", plugin.Failed, plugin.ManualInstallCommand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			EmitPluginInstallNotice(tc.action, NewReporter(&buf))
			if !strings.Contains(buf.String(), tc.want) {
				t.Errorf("notice for %v = %q, want it to contain %q", tc.action, buf.String(), tc.want)
			}
		})
	}
}

func TestEmitPluginInstallNotice_NilReporterIsNoOp(t *testing.T) {
	// No panic when reporter is missing.
	EmitPluginInstallNotice(plugin.Installed, nil)
}
