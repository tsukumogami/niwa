package workspace

import (
	"bytes"
	"strings"
	"testing"
)

func TestEmitRank2Notice_FirstCallLogsAndRecords(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewReporter(&buf)
	state := &InstanceState{}

	EmitRank2Notice(state, NoticeIDRank2TeamConfig, "org/legacy", reporter)

	out := buf.String()
	for _, want := range []string{"note:", "deprecated", "org/legacy", "/niwa:migrate-config"} {
		if !strings.Contains(out, want) {
			t.Errorf("emitted message %q missing substring %q", out, want)
		}
	}

	if len(state.DisclosedNotices) != 1 || state.DisclosedNotices[0] != NoticeIDRank2TeamConfig {
		t.Errorf("DisclosedNotices = %v, want [%q]", state.DisclosedNotices, NoticeIDRank2TeamConfig)
	}
}

func TestEmitRank2Notice_SecondCallIsNoOp(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewReporter(&buf)
	state := &InstanceState{DisclosedNotices: []string{NoticeIDRank2TeamConfig}}

	EmitRank2Notice(state, NoticeIDRank2TeamConfig, "org/legacy", reporter)

	if buf.Len() != 0 {
		t.Errorf("idempotent call should emit nothing, got %q", buf.String())
	}
	if len(state.DisclosedNotices) != 1 {
		t.Errorf("DisclosedNotices grew on idempotent call: %v", state.DisclosedNotices)
	}
}

func TestEmitRank2Notice_DifferentNoticeIDsAreIndependent(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewReporter(&buf)
	state := &InstanceState{DisclosedNotices: []string{NoticeIDRank2TeamConfig}}

	// Overlay notice should still fire — different ID.
	EmitRank2Notice(state, NoticeIDRank2Overlay, "org/dotfiles", reporter)

	if !strings.Contains(buf.String(), "org/dotfiles") {
		t.Error("overlay notice should fire when only team-config notice was previously recorded")
	}
	if len(state.DisclosedNotices) != 2 {
		t.Errorf("DisclosedNotices = %v, want both team-config and overlay", state.DisclosedNotices)
	}
}

func TestEmitRank2Notice_NilStateStillLogs(t *testing.T) {
	var buf bytes.Buffer
	reporter := NewReporter(&buf)

	// nil state: notice fires (idempotence guard skipped), no panic.
	EmitRank2Notice(nil, NoticeIDRank2TeamConfig, "org/legacy", reporter)

	if !strings.Contains(buf.String(), "org/legacy") {
		t.Error("nil-state call should still log the notice")
	}
}

func TestEmitRank2Notice_NilReporterIsNoOp(t *testing.T) {
	state := &InstanceState{}

	// No panic, no state mutation.
	EmitRank2Notice(state, NoticeIDRank2TeamConfig, "org/legacy", nil)

	if len(state.DisclosedNotices) != 0 {
		t.Errorf("nil-reporter call should not mutate state: %v", state.DisclosedNotices)
	}
}
