package authority

import (
	"reflect"
	"strings"
	"testing"
)

const goldenPolicy = `{
  "schema": "niwa.authority.policy/v1",
  "workspace": {
    "level": "supervised",
    "ceiling": [
      "merge-pr",
      "close-issue"
    ]
  },
  "overlay": {
    "level": null,
    "ceiling": null
  },
  "machine": {
    "level": null,
    "ceiling": []
  },
  "level_ceiling": "supervised",
  "level_ceiling_source": "workspace",
  "grant_ceiling": [],
  "grant_ceiling_source": "machine",
  "roles": [
    {
      "name": "coordinator",
      "authorities": [
        "merge-pr",
        "close-issue",
        "cut-release"
      ],
      "outside_ceiling": [
        "merge-pr",
        "close-issue",
        "cut-release"
      ]
    }
  ],
  "no_flag_dispatch": {
    "level": "supervised",
    "level_source": "workspace",
    "grant": []
  },
  "warnings": [
    "a warning"
  ]
}`

func TestPolicyReport_GoldenJSON(t *testing.T) {
	p := Policy{
		BaseLevel:      lvl(Supervised),
		BaseCeiling:    ceil(MergePR, CloseIssue),
		MachineCeiling: ceil(),
		Roles:          map[string][]Authority{"coordinator": all},
	}
	b, err := BuildPolicyReport(p, []string{"a warning"}).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != goldenPolicy {
		t.Errorf("document =\n%s\nwant\n%s", b, goldenPolicy)
	}
}

func TestPolicyReport_OutsideCeiling(t *testing.T) {
	t.Run("base ceiling lacks cut-release", func(t *testing.T) {
		r := BuildPolicyReport(Policy{
			BaseCeiling: ceil(MergePR, CloseIssue),
			Roles:       map[string][]Authority{"coordinator": all},
		}, nil)
		if !reflect.DeepEqual(r.Roles[0].OutsideCeiling, []Authority{CutRelease}) {
			t.Errorf("outside = %v", r.Roles[0].OutsideCeiling)
		}
		if !strings.Contains(r.Text(), "coordinator: merge-pr, close-issue, cut-release (outside ceiling)") {
			t.Errorf("text:\n%s", r.Text())
		}
	})
	t.Run("machine narrows to merge-pr", func(t *testing.T) {
		r := BuildPolicyReport(Policy{
			BaseCeiling:    ceil(all...),
			MachineCeiling: ceil(MergePR),
			Roles:          map[string][]Authority{"coordinator": all},
		}, nil)
		if !reflect.DeepEqual(r.Roles[0].OutsideCeiling, []Authority{CloseIssue, CutRelease}) {
			t.Errorf("outside = %v", r.Roles[0].OutsideCeiling)
		}
		if r.Effective.GrantCeilingSource != LayerMachine {
			t.Errorf("source %s", r.Effective.GrantCeilingSource)
		}
	})
}

func TestPolicyReport_Text(t *testing.T) {
	r := BuildPolicyReport(Policy{OverlayLevel: lvl(Strict), BaseLevel: lvl(Supervised)}, nil)
	want := `workspace: level supervised; ceiling not set
overlay:   level strict; ceiling not set
machine:   level not set; ceiling not set
level ceiling: strict from overlay
grant ceiling: (empty) from none
roles: none
no-flag dispatch: level strict from overlay; grant none
`
	if got := r.Text(); got != want {
		t.Errorf("text =\n%s\nwant\n%s", got, want)
	}
	if got := BuildPolicyReport(Policy{}, nil); got.NoFlagLevelSource != LayerBuiltIn || got.NoFlagLevel != Autonomous {
		t.Errorf("no level anywhere: %s from %s", got.NoFlagLevel, got.NoFlagLevelSource)
	}
}
