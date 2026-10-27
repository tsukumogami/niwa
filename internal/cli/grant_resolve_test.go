package cli

import (
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/lineage"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// The policies a role change moves between. Every one declares the whole
// ceiling unless it says otherwise.
const (
	policyBefore   = "[authority]\nceiling = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n\n[authority.roles.coordinator]\nauthorities = [\"merge-pr\", \"close-issue\"]\n"
	policyWidened  = "[authority]\nceiling = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n\n[authority.roles.coordinator]\nauthorities = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n"
	policyNarrowed = "[authority]\nceiling = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n\n[authority.roles.coordinator]\nauthorities = [\"merge-pr\"]\n"
	policyNoRole   = "[authority]\nceiling = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n"
	policyCeiling  = "[authority]\nceiling = [\"close-issue\"]\n\n[authority.roles.coordinator]\nauthorities = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n"
)

// The dispatches of a test lineage, root first.
const (
	dispatchA = "a0a0a0a0-a0a0-4a0a-8a0a-a0a0a0a0a0a0"
	dispatchB = "b0b0b0b0-b0b0-4b0b-8b0b-b0b0b0b0b0b0"
	dispatchC = "c0c0c0c0-c0c0-4c0c-8c0c-c0c0c0c0c0c0"
	sessionB  = "b1b1b1b1-b1b1-4b1b-8b1b-b1b1b1b1b1b1"
	sessionC  = "c1c1c1c1-c1c1-4c1c-8c1c-c1c1c1c1c1c1"
)

func setPolicy(t *testing.T, w *showWorkspace, body string) {
	t.Helper()
	writeShowFile(t, filepath.Join(w.root, ".niwa", "workspace.toml"), "[workspace]\nname = \"ws\"\n"+body, 0o644)
}

// writeEntry writes dispatchID's lineage entry, naming instance, with the
// parent links given.
func writeEntry(t *testing.T, root, dispatchID, instance string, parentSession, parentDispatch lineage.Link) {
	t.Helper()
	e := lineage.Entry{
		DispatchID:     dispatchID,
		Instance:       instance,
		Agent:          "claude",
		Origin:         lineage.OriginDispatch,
		WrittenAt:      "2026-10-26T05:00:00Z",
		ParentSession:  parentSession,
		ParentDispatch: parentDispatch,
		Principal:      lineage.Principal{Source: lineage.SourceNone, Trust: lineage.TrustObserved},
	}
	if err := workspace.CreateLineageEntry(root, e); err != nil {
		t.Fatal(err)
	}
}

var noLink = lineage.Link{Source: lineage.SourceNone}

// coordinatorRecord is a record dispatched with --role coordinator while the
// role named merge-pr and close-issue, with --grant adding the authorities
// in grants, and the parent link given.
func coordinatorRecord(dispatchID, parentID, parentSource string, grants ...authority.Authority) authority.Record {
	rec := showRecord(dispatchID, authority.Autonomous, authority.LayerWorkspace)
	rec.ParentDispatchID, rec.ParentDispatchSource = parentID, parentSource
	rec.Roles = []string{"coordinator"}
	byFlag := map[authority.Authority]bool{}
	for _, g := range grants {
		byFlag[g] = true
	}
	for _, a := range []authority.Authority{authority.MergePR, authority.CloseIssue, authority.CutRelease} {
		var sources []authority.Source
		if byFlag[a] {
			sources = append(sources, authority.SourceDispatch)
		}
		if a != authority.CutRelease {
			sources = append(sources, authority.RoleSource("coordinator"))
		}
		if len(sources) > 0 {
			rec.Grant = append(rec.Grant, authority.GrantEntry{Authority: a, Sources: sources})
		}
	}
	return rec
}

// rootCoordinator is one instance dispatched from an operator's terminal at
// the workspace root (no dispatcher), bound to sessionC.
func rootCoordinator(t *testing.T, grants ...authority.Authority) (*showWorkspace, string) {
	t.Helper()
	w := newShowWorkspace(t, policyBefore)
	inst := w.instance(t, "ws-c")
	writeShowRecord(t, inst, coordinatorRecord(dispatchC, "", authority.ParentSourceNone, grants...))
	writeEntry(t, w.root, dispatchC, "ws-c", noLink, noLink)
	checkMapping(t, w.root, sessionC, inst, workspace.MappingOriginDispatch, dispatchC)
	return w, inst
}

func grantLine(t *testing.T, inst string) string {
	t.Helper()
	r := runAuthorityCmd(t, inst, "check", "--session", sessionC, "-C", inst).mustSucceed(t)
	for _, line := range splitLines(r.stdout) {
		if len(line) > 7 && line[:7] == "grant: " {
			return line
		}
	}
	t.Fatalf("no grant line in\n%s", r.stdout)
	return ""
}

func TestCheckResolvesTheGrantAgainstTheCurrentPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, policy, want string
	}{
		{"unchanged", policyBefore, "grant: merge-pr (dispatch, role coordinator), close-issue (role coordinator)"},
		{"a widened role reaches the running session", policyWidened, "grant: merge-pr (dispatch, role coordinator), close-issue (role coordinator), cut-release (role coordinator)"},
		{"a narrowed role takes effect", policyNarrowed, "grant: merge-pr (dispatch, role coordinator)"},
		{"a removed role grants nothing and --grant stays", policyNoRole, "grant: merge-pr (dispatch)"},
		{"a narrowed ceiling caps --grant too", policyCeiling, "grant: close-issue (role coordinator)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, inst := rootCoordinator(t, authority.MergePR)
			setPolicy(t, w, tc.policy)
			if got := grantLine(t, inst); got != tc.want {
				t.Errorf("%s\nwant %s", got, tc.want)
			}
		})
	}
}

// childOf adds instance ws-c, dispatched from inside parent's instance with
// --role coordinator and --grant grants, bound to sessionC.
func childOf(t *testing.T, w *showWorkspace, parentID string, grants ...authority.Authority) string {
	t.Helper()
	inst := w.instance(t, "ws-c")
	writeShowRecord(t, inst, coordinatorRecord(dispatchC, parentID, authority.ParentSourceCwd, grants...))
	writeEntry(t, w.root, dispatchC, "ws-c", noLink, lineage.Link{ID: parentID, Source: lineage.SourceObserved})
	checkMapping(t, w.root, sessionC, inst, workspace.MappingOriginDispatch, dispatchC)
	return inst
}

func TestCheckCapsTheGrantByAParentNarrowerThanTheRole(t *testing.T) {
	w := newShowWorkspace(t, policyBefore)
	parent := w.instance(t, "ws-b")
	writeShowRecord(t, parent, showRecord(dispatchB, authority.Autonomous, authority.LayerWorkspace, authority.MergePR))
	writeEntry(t, w.root, dispatchB, "ws-b", noLink, noLink)
	inst := childOf(t, w, dispatchB)
	setPolicy(t, w, policyWidened)
	if got, want := grantLine(t, inst), "grant: merge-pr (role coordinator)"; got != want {
		t.Errorf("%s\nwant %s: the parent holds only merge-pr", got, want)
	}
}

func TestCheckFollowsTheParentsCurrentGrant(t *testing.T) {
	w := newShowWorkspace(t, policyBefore)
	parent := w.instance(t, "ws-b")
	writeShowRecord(t, parent, coordinatorRecord(dispatchB, "", authority.ParentSourceNone))
	writeEntry(t, w.root, dispatchB, "ws-b", noLink, noLink)
	inst := childOf(t, w, dispatchB, authority.CloseIssue)

	setPolicy(t, w, policyWidened)
	if got, want := grantLine(t, inst), "grant: merge-pr (role coordinator), close-issue (dispatch, role coordinator), cut-release (role coordinator)"; got != want {
		t.Errorf("widened:\n%s\nwant %s", got, want)
	}
	// The parent loses close-issue with the role, so the child loses it too,
	// --grant and all.
	setPolicy(t, w, policyNarrowed)
	if got, want := grantLine(t, inst), "grant: merge-pr (role coordinator)"; got != want {
		t.Errorf("narrowed:\n%s\nwant %s", got, want)
	}
}

func TestCheckReadsAnOldRecordAgainstTheCurrentPolicy(t *testing.T) {
	// A record as niwa 0.38.0 wrote it, from the root with no dispatcher.
	w := newShowWorkspace(t, policyWidened)
	inst := w.instance(t, "ws-c")
	writeRawRecord(t, inst, `{
  "schema": "niwa.authority.record/v1",
  "record": true,
  "dispatch_id": "`+dispatchC+`",
  "parent_session_id": "",
  "parent_dispatch_id": "",
  "parent_dispatch_source": "none",
  "parent_session_source": "none",
  "principal": {"name": "", "source": "none", "trust": "observed"},
  "level": "autonomous",
  "level_source": "workspace",
  "level_ceiling": "autonomous",
  "level_ceiling_source": "workspace",
  "grant_ceiling": ["merge-pr", "close-issue"],
  "grant_ceiling_source": "workspace",
  "roles": ["coordinator"],
  "grant": [
    {"authority": "merge-pr", "sources": ["role:coordinator"]},
    {"authority": "close-issue", "sources": ["role:coordinator"]}
  ],
  "unknown_grant": []
}
`)
	writeEntry(t, w.root, dispatchC, "ws-c", noLink, noLink)
	checkMapping(t, w.root, sessionC, inst, workspace.MappingOriginDispatch, dispatchC)
	if got, want := grantLine(t, inst), "grant: merge-pr (role coordinator), close-issue (role coordinator), cut-release (role coordinator)"; got != want {
		t.Errorf("widened:\n%s\nwant %s", got, want)
	}
	setPolicy(t, w, policyNarrowed)
	if got, want := grantLine(t, inst), "grant: merge-pr (role coordinator)"; got != want {
		t.Errorf("narrowed:\n%s\nwant %s", got, want)
	}
}

func TestCheckFailsTheLookupWhenThePolicyCantBeLoaded(t *testing.T) {
	w, inst := rootCoordinator(t)
	writeShowFile(t, filepath.Join(w.root, ".niwa", "workspace.toml"), "[workspace\n", 0o644)
	r := runAuthorityCmd(t, inst, "check", "--json", "--session", sessionC, "-C", inst).mustFail(t, 1)
	if r.stdout != "" {
		t.Errorf("a failed policy load printed an answer:\n%s", r.stdout)
	}
}

func TestCheckLookupFailuresCapAtTheRecordedGrant(t *testing.T) {
	const recorded = "grant: merge-pr (role coordinator), close-issue (role coordinator)"
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, w *showWorkspace) string
	}{
		{"the dispatcher's record can't be read", func(t *testing.T, w *showWorkspace) string {
			parent := w.instance(t, "ws-b")
			writeRawRecord(t, parent, "{not json")
			writeEntry(t, w.root, dispatchB, "ws-b", noLink, noLink)
			return childOf(t, w, dispatchB)
		}},
		{"the dispatcher's instance holds another dispatch's record", func(t *testing.T, w *showWorkspace) string {
			parent := w.instance(t, "ws-b")
			writeShowRecord(t, parent, coordinatorRecord(dispatchA, "", authority.ParentSourceNone))
			writeEntry(t, w.root, dispatchB, "ws-b", noLink, noLink)
			return childOf(t, w, dispatchB)
		}},
		{"the session's own lineage entry is missing", func(t *testing.T, w *showWorkspace) string {
			inst := w.instance(t, "ws-c")
			writeShowRecord(t, inst, coordinatorRecord(dispatchC, "", authority.ParentSourceNone))
			checkMapping(t, w.root, sessionC, inst, workspace.MappingOriginDispatch, dispatchC)
			return inst
		}},
		{"the record and the entry name different parents", func(t *testing.T, w *showWorkspace) string {
			inst := w.instance(t, "ws-c")
			writeShowRecord(t, inst, coordinatorRecord(dispatchC, dispatchB, authority.ParentSourceCwd))
			writeEntry(t, w.root, dispatchC, "ws-c", noLink, lineage.Link{ID: dispatchA, Source: lineage.SourceObserved})
			checkMapping(t, w.root, sessionC, inst, workspace.MappingOriginDispatch, dispatchC)
			return inst
		}},
		{"dispatched inside an instance that had lost its record", func(t *testing.T, w *showWorkspace) string {
			inst := w.instance(t, "ws-c")
			writeShowRecord(t, inst, coordinatorRecord(dispatchC, "", authority.ParentSourceNone))
			writeEntry(t, w.root, dispatchC, "ws-c", lineage.Link{ID: sessionB, Source: lineage.SourceObserved}, noLink)
			checkMapping(t, w.root, sessionC, inst, workspace.MappingOriginDispatch, dispatchC)
			return inst
		}},
		{"the lineage is a cycle", func(t *testing.T, w *showWorkspace) string {
			parent := w.instance(t, "ws-b")
			writeShowRecord(t, parent, coordinatorRecord(dispatchB, dispatchC, authority.ParentSourceCwd))
			writeEntry(t, w.root, dispatchB, "ws-b", noLink, lineage.Link{ID: dispatchC, Source: lineage.SourceObserved})
			return childOf(t, w, dispatchB)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newShowWorkspace(t, policyBefore)
			inst := tc.setup(t, w)
			// Widened, the failed lookup keeps the session at what it was
			// granted; narrowed, it still narrows.
			setPolicy(t, w, policyWidened)
			if got := grantLine(t, inst); got != recorded {
				t.Errorf("widened:\n%s\nwant %s", got, recorded)
			}
			setPolicy(t, w, policyNarrowed)
			if got, want := grantLine(t, inst), "grant: merge-pr (role coordinator)"; got != want {
				t.Errorf("narrowed:\n%s\nwant %s", got, want)
			}
		})
	}
}

// reapedChain builds a coordinator lineage A -> B -> C, each dispatched with
// --role coordinator while the role named merge-pr and close-issue: A from an
// operator's terminal at the workspace root, B by A and C by B, each from
// the root with --parent-from its dispatcher's instance. A and B have been
// reaped: their instances and session mappings are gone, their lineage
// entries kept. C runs, bound to sessionC.
func reapedChain(t *testing.T) (*showWorkspace, string) {
	t.Helper()
	w := newShowWorkspace(t, policyBefore)
	const sessionA = "a1a1a1a1-a1a1-4a1a-8a1a-a1a1a1a1a1a1"
	writeEntry(t, w.root, dispatchA, "ws-a", noLink, noLink)
	writeEntry(t, w.root, dispatchB, "ws-b",
		lineage.Link{ID: sessionA, Source: lineage.SourceHanded}, lineage.Link{ID: dispatchA, Source: lineage.SourceHanded})
	inst := w.instance(t, "ws-c")
	writeShowRecord(t, inst, coordinatorRecord(dispatchC, dispatchB, authority.ParentSourceAnchor))
	writeEntry(t, w.root, dispatchC, "ws-c",
		lineage.Link{ID: sessionB, Source: lineage.SourceHanded}, lineage.Link{ID: dispatchB, Source: lineage.SourceHanded})
	checkMapping(t, w.root, sessionC, inst, workspace.MappingOriginDispatch, dispatchC)
	return w, inst
}

// capUnder runs dispatcherCap as resolveDispatchAuthority does, against the
// workspace's own policy.
func capUnder(t *testing.T, w *showWorkspace, dir, sessionVar string) (*authority.DispatcherCap, authority.Policy) {
	t.Helper()
	policy, _, err := loadAuthorityPolicy(w.root)
	if err != nil {
		t.Fatal(err)
	}
	parent, _, cwdInstance, cwdInInstance, err := dispatchParent(dir, w.root)
	if err != nil {
		t.Fatal(err)
	}
	return dispatcherCap(grantResolver{root: w.root, policy: policy}, sessionVar, parent, cwdInstance, cwdInInstance), policy
}

func TestReapedCoordinatorChainTakesAWidenedRole(t *testing.T) {
	resetParentFrom(t)
	w, inst := reapedChain(t)
	setPolicy(t, w, policyWidened)
	if got, want := grantLine(t, inst), "grant: merge-pr (role coordinator), close-issue (role coordinator), cut-release (role coordinator)"; got != want {
		t.Errorf("C reads\n%s\nwant %s", got, want)
	}

	// C dispatches D with the widened role, from inside its instance and
	// from the root as its own session.
	for _, tc := range []struct{ name, dir, sessionVar string }{
		{"from inside C's instance", inst, ""},
		{"from the root as C's session", w.root, sessionC},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cap, policy := capUnder(t, w, tc.dir, tc.sessionVar)
			child, err := authority.Resolve(policy, authority.Request{Roles: []string{"coordinator"}})
			if err != nil {
				t.Fatal(err)
			}
			if err := authority.CapByDispatcher(child, cap); err != nil {
				t.Errorf("D was refused: %v", err)
			}
		})
	}
}

func TestDispatchCapFollowsANarrowedRole(t *testing.T) {
	resetParentFrom(t)
	w, inst := rootCoordinator(t)
	setPolicy(t, w, policyNarrowed)
	cap, policy := capUnder(t, w, inst, "")
	child, err := authority.Resolve(policy, authority.Request{Grants: []authority.Authority{authority.CloseIssue}})
	if err != nil {
		t.Fatal(err)
	}
	if err := authority.CapByDispatcher(child, cap); err == nil {
		t.Error("a child got close-issue from a dispatcher whose role no longer names it")
	}
}
