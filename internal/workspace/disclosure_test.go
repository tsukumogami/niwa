package workspace

import (
	"bytes"
	"strings"
	"testing"
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
