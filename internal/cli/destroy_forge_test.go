package cli

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func TestClassifyHeadPulls(t *testing.T) {
	merged := func(n int) github.PullByHead {
		return github.PullByHead{Number: n, State: "closed", MergedAt: "2026-09-01T00:00:00Z"}
	}
	closed := func(n int) github.PullByHead { return github.PullByHead{Number: n, State: "closed"} }
	open := func(n int) github.PullByHead { return github.PullByHead{Number: n, State: "open"} }

	cases := []struct {
		name  string
		pulls []github.PullByHead
		want  workspace.HeadPR
	}{
		{"none", nil, workspace.HeadPR{State: workspace.HeadPRNone}},
		{"open", []github.PullByHead{open(3)}, workspace.HeadPR{State: workspace.HeadPROpen, Number: 3}},
		{"merged", []github.PullByHead{merged(7)}, workspace.HeadPR{State: workspace.HeadPRMerged, Number: 7}},
		{"closed", []github.PullByHead{closed(2)}, workspace.HeadPR{State: workspace.HeadPRClosed, Number: 2}},
		// A branch name reused after an earlier merge: the open PR keeps it alive.
		{"open beats merged", []github.PullByHead{merged(7), open(12)}, workspace.HeadPR{State: workspace.HeadPROpen, Number: 12}},
		{"merged beats closed", []github.PullByHead{closed(9), merged(4)}, workspace.HeadPR{State: workspace.HeadPRMerged, Number: 4}},
		{"latest merged named", []github.PullByHead{merged(4), merged(8)}, workspace.HeadPR{State: workspace.HeadPRMerged, Number: 8}},
	}
	for _, c := range cases {
		if got := classifyHeadPulls(c.pulls); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
