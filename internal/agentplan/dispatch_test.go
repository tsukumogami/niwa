package agentplan

import (
	"go/ast"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

// This is the launch route's binding check, and it is the reason the launch
// description lives in this package rather than beside the code that execs.
//
// The Delivery-name mechanism in binding.go cannot reach a launch: it matches a
// name against a Materializer registered in internal/workspace, and nothing in
// internal/workspace launches anything. Registering a fake one there so the
// existing check had something to compare against would recreate the
// agrees-with-itself problem that table exists to prevent. So the launch route
// binds the same way -- both drift directions, an implemented declaration with
// nothing behind it and a delivery nobody declared -- against the table in
// dispatch.go, which sits next to the declaration it answers for.
//
// The bar these tests are written to: deleting the delivery must fail the
// declaration. Deleting Claude's row from launchSpecs fails
// TestLaunchSpecsMatchTheirDeclarations; emptying any single field of it fails
// TestLaunchSpecsAreComplete. Neither is a check the delivery can satisfy by
// existing.

// TestLaunchSpecsMatchTheirDeclarations checks the launch route in both drift
// directions. An agent declared implemented with no spec behind it is a
// declaration nobody delivers; a spec for an agent the table does not declare
// implemented is a delivery nobody declared. Neither is visible from one side.
func TestLaunchSpecsMatchTheirDeclarations(t *testing.T) {
	for _, ag := range agent.All() {
		d, err := Lookup(DispatchLaunch, ag)
		if err != nil {
			t.Errorf("Lookup(%s, %s): %v", DispatchLaunch, ag, err)
			continue
		}
		_, hasSpec := For(ag).LaunchSpec()

		switch {
		case d.State == StateImplemented && !hasSpec:
			t.Errorf("(%s, %s) is declared implemented with no launch spec behind it", DispatchLaunch, ag)
		case d.State != StateImplemented && hasSpec:
			t.Errorf("(%s, %s) carries a launch spec but is not declared implemented; something delivers a capability nobody declared", DispatchLaunch, ag)
		}
	}

	// The table may not carry a row for an agent outside the accepted set,
	// which would be a delivery for an agent the contract cannot answer for at
	// all.
	for ag := range launchSpecs {
		if !slices.Contains(agent.All(), ag) {
			t.Errorf("launchSpecs carries a row for agent %q, which is outside the accepted set", ag)
		}
	}
}

// TestLaunchSpecsAreComplete asserts each spec says everything the dispatch
// path has to ask it. A half-filled spec is the shape a delivery takes when it
// is added to satisfy the binding rather than to launch anything: the row
// exists, the check passes, and the launch has no binary or the capture has no
// field to read.
func TestLaunchSpecsAreComplete(t *testing.T) {
	for ag, spec := range launchSpecs {
		if spec.Binary == "" {
			t.Errorf("(%s): the launch spec names no binary", ag)
		}
		// ModeFor treats an unset runner as self-backgrounding, which is the
		// right fail-safe for a spec somebody builds by hand in a test -- but a
		// declared row whose runner executes the turn in the foreground and
		// forgot to say so would be started the wrong way silently, and
		// "silently" is the word that makes it worth a check here rather than a
		// comment there.
		switch spec.Runner {
		case RunnerSelfBackgrounding, RunnerForeground:
		default:
			t.Errorf("(%s): runner kind %d is outside the closed set", ag, spec.Runner)
		}
		// An argument that rides the detached launch only is meaningless for a
		// runner that has no detached launch to ride: it would be declared,
		// never sent, and nothing would say so.
		if len(spec.DetachedArgs) > 0 && spec.Runner != RunnerForeground {
			t.Errorf("(%s): the launch spec declares detached-only arguments %v for a runner that always backgrounds its own session, so they would never be sent", ag, spec.DetachedArgs)
		}
		if len(spec.ResumeArgs) == 0 {
			t.Errorf("(%s): the launch spec declares no way to resume a session", ag)
		}
		if len(spec.HintVerbs) == 0 {
			t.Errorf("(%s): the launch spec names no management verbs to print", ag)
		}
		if spec.Flags.Model == "" {
			t.Errorf("(%s): the launch spec names no model flag; every agent niwa launches takes one", ag)
		}

		// The category vocabulary is niwa's own and is the same words for
		// every agent. An agent missing one would answer a portable request
		// with nothing rather than with its own equivalent.
		for _, category := range ModelCategories() {
			if spec.ModelCategories[category] == "" {
				t.Errorf("(%s): the launch spec maps no model to the %q category", ag, category)
			}
		}
		if len(spec.KnownModels) == 0 {
			t.Errorf("(%s): the launch spec recognizes no model names, so every value warns", ag)
		}
		// The other direction: a spec may not bind a name that is not part of
		// the portable vocabulary. A category only one agent answers for is
		// not portable, which is the one thing the vocabulary is for.
		for name := range spec.ModelCategories {
			if !slices.Contains(ModelCategories(), name) {
				t.Errorf("(%s): the launch spec binds %q, which is not a portable category", ag, name)
			}
		}

		checkRecords(t, ag, spec.Records)
	}
}

// checkRecords asserts a session-record description can actually be walked and
// decoded. Every field here is one the reader in internal/cli dereferences, so
// an empty one is a nil read at capture time rather than a test failure.
func checkRecords(t *testing.T, ag agent.Agent, r SessionRecords) {
	t.Helper()

	if len(r.HomePath) == 0 {
		t.Errorf("(%s): the session records have no root under the developer's home", ag)
	}
	if r.Depth < 0 {
		t.Errorf("(%s): the session records sit at depth %d", ag, r.Depth)
	}
	// Exactly one of the two ways of naming a record file. Both set would
	// leave the walker choosing; neither set would leave it with no file to
	// open.
	switch {
	case r.FileName == "" && r.FileGlob == "":
		t.Errorf("(%s): the session records name neither a file nor a glob", ag)
	case r.FileName != "" && r.FileGlob != "":
		t.Errorf("(%s): the session records name both a file (%q) and a glob (%q)", ag, r.FileName, r.FileGlob)
	}
	if len(r.CwdPath) == 0 {
		t.Errorf("(%s): the session records name no working-directory field, so a launched worker cannot be correlated to its instance", ag)
	}
	if len(r.IDPath) == 0 {
		t.Errorf("(%s): the session records name no session-id field", ag)
	}
	switch r.Handle {
	case HandleRecordDir, HandleSessionID:
	default:
		t.Errorf("(%s): handle kind %d is outside the closed set", ag, r.Handle)
	}
	// A record-directory handle only exists when the record sits inside a
	// directory of its own, which is what a depth of at least one and a named
	// file mean together. Without both, the handle would be the name of the
	// store's own root, which is the same string for every session in it.
	if r.Handle == HandleRecordDir {
		if r.FileName == "" {
			t.Errorf("(%s): the handle is the record's directory name, but the records are named by a glob rather than sitting in one", ag)
		}
		if r.Depth < 1 {
			t.Errorf("(%s): the handle is the record's directory name, but the records sit at the store root", ag)
		}
	}
	switch r.Liveness {
	case LivenessRecordPresence, LivenessNone:
	default:
		t.Errorf("(%s): liveness kind %d is outside the closed set", ag, r.Liveness)
	}
}

// TestEveryLaunchSpecFieldIsRead is the anti-dead-plumbing check, and it is the
// one this whole contract exists because of.
//
// The attempt that closed as a prototype shipped a type meant to unify two
// agents whose value was read by nothing, while every call site hardcoded an
// agent constant. Its structure compiled, its tests passed, and its design said
// the right things. What it never had was a check that a field somebody added
// is a field somebody reads.
//
// So: every field of the launch description, and of the two structures it
// carries, must be selected somewhere in this package or in the package that
// does the launching. A field nobody reads is either a decision that has not
// been wired up, or a decision nobody needed -- and neither should be able to
// merge quietly. Completeness suites that check a field is *populated* do not
// catch this: a populated field nothing reads is exactly the failure.
//
// The check is deliberately coarse in one direction and exact in the other. A
// field name that coincides with an unrelated selector elsewhere would count as
// read, so this cannot prove a field is read *for the right reason*. It can and
// does prove a field is read nowhere at all, which is the shape that closed the
// prior attempt.
//
// The names that actually collide today are worth knowing before you trust a
// pass here: Mode, Depth, Handle, Settings, and Model each appear as a selector
// on some unrelated value in these two packages. Deleting the read of one of
// those from the launcher leaves this test green, so anything load-bearing about
// them needs its own assertion -- Runner's is in dispatch_launcher_test.go,
// which pins the argv and the process model each declared runner produces
// rather than settling for membership in the closed set.
func TestEveryLaunchSpecFieldIsRead(t *testing.T) {
	fields := map[string]string{}
	for _, decl := range []struct {
		owner string
		typ   reflect.Type
	}{
		{"LaunchSpec", reflect.TypeOf(LaunchSpec{})},
		{"SessionRecords", reflect.TypeOf(SessionRecords{})},
		{"LaunchFlags", reflect.TypeOf(LaunchFlags{})},
	} {
		for i := 0; i < decl.typ.NumField(); i++ {
			fields[decl.typ.Field(i).Name] = decl.owner
		}
	}

	// Composite-literal keys are plain identifiers rather than selectors, so
	// the table that declares these fields does not count as reading them.
	read := map[string]bool{}
	for _, dir := range []string{leafDir, "../cli"} {
		for _, pf := range parsePackageFiles(t, dir) {
			ast.Inspect(pf.file, func(n ast.Node) bool {
				if sel, ok := n.(*ast.SelectorExpr); ok {
					read[sel.Sel.Name] = true
				}
				return true
			})
		}
	}

	var unread []string
	for name, owner := range fields {
		if !read[name] {
			unread = append(unread, owner+"."+name)
		}
	}
	if len(unread) > 0 {
		slices.Sort(unread)
		t.Fatalf("%d launch-description field(s) are declared and read by nothing:\n  %s\nEither wire the field up, or delete it until the change that needs it. A field nothing reads is the shape that closed the prior attempt at this feature.",
			len(unread), strings.Join(unread, "\n  "))
	}
}

// TestLaunchableAgentsMatchesTheDeclarations pins the answer a refusal points a
// developer at to the table rather than to a sentence somebody wrote. The two
// have to stay the same set: a name in the message the table does not implement
// sends a developer to an agent that will refuse them again, and an implemented
// agent missing from the message is one they never learn they could have used.
func TestLaunchableAgentsMatchesTheDeclarations(t *testing.T) {
	got := LaunchableAgents()
	for _, ag := range agent.All() {
		d, err := Lookup(DispatchLaunch, ag)
		if err != nil {
			t.Fatalf("Lookup(%s, %s): %v", DispatchLaunch, ag, err)
		}
		listed := slices.Contains(got, ag)
		if implemented := d.State == StateImplemented; implemented != listed {
			t.Errorf("(%s): declared implemented=%v but listed as launchable=%v", ag, implemented, listed)
		}
	}
	// Every listed agent must also have a spec, or the refusal would name one
	// the launch cannot actually use.
	for _, ag := range got {
		if _, ok := For(ag).LaunchSpec(); !ok {
			t.Errorf("(%s) is listed as launchable with no launch spec behind it", ag)
		}
	}
}

// TestLaunchSpecForUnknownAgentIsAbsent keeps the accessor fail-closed in the
// posture Lookup takes: an agent outside the accepted set gets no spec rather
// than the first one in the map.
func TestLaunchSpecForUnknownAgentIsAbsent(t *testing.T) {
	if _, ok := For(agent.Agent("emacs")).LaunchSpec(); ok {
		t.Error("For(unknown agent).LaunchSpec() returned a spec")
	}
}

// TestLaunchSpecForZeroAgentIsClaude matches internal/agent's fail-safe
// contract, which internal/agentplan honors everywhere else: the zero Agent is
// Claude, so a construction site not yet wired to set the agent degrades to
// today's behavior rather than to no launch at all.
func TestLaunchSpecForZeroAgentIsClaude(t *testing.T) {
	zero, ok := For(agent.Agent("")).LaunchSpec()
	if !ok {
		t.Fatal("For(zero agent).LaunchSpec() returned no spec")
	}
	claude, ok := For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("For(claude).LaunchSpec() returned no spec")
	}
	if zero.Binary != claude.Binary {
		t.Errorf("the zero agent launches %q, Claude launches %q", zero.Binary, claude.Binary)
	}
}

// TestModelNameAccessorsAreSorted pins the ordering the help text depends on.
// Ranging over the category map directly would render a different vocabulary
// order on every run, which is a diff in `niwa dispatch --help` nobody made.
func TestModelNameAccessorsAreSorted(t *testing.T) {
	for ag, spec := range launchSpecs {
		if got := spec.ModelCategoryNames(); !slices.IsSorted(got) {
			t.Errorf("(%s): ModelCategoryNames() is unsorted: %v", ag, got)
		}
		if got := spec.KnownModelNames(); !slices.IsSorted(got) {
			t.Errorf("(%s): KnownModelNames() is unsorted: %v", ag, got)
		}
	}
}

// TestKnownModelNamesDoesNotAliasTheTable matches the posture All() and
// Bindings() take: a caller that sorts or trims the returned names cannot
// reorder the package's own slice for everyone else.
func TestKnownModelNamesDoesNotAliasTheTable(t *testing.T) {
	spec, ok := For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for claude")
	}
	names := spec.KnownModelNames()
	if len(names) == 0 {
		t.Fatal("no known model names")
	}
	names[0] = ""

	again, _ := For(agent.AgentClaude).LaunchSpec()
	if slices.Contains(again.KnownModelNames(), "") {
		t.Error("KnownModelNames() handed out the package's own slice")
	}
}

// TestModeForNeedsBothItsInputs is the test the defect this resolution replaced
// would fail.
//
// The process model used to be a field of the launch description, so it was
// decided before any flag was read: an agent whose runner executes the turn in
// the foreground was detached whether or not the developer asked, and --detach
// was wired to a separate question -- whether an attach step ran afterwards.
// The two never met.
//
// So the assertion is not that ModeFor returns particular constants; it is that
// the answer moves with both inputs where it should and with neither where it
// should not. A resolution that read the runner alone gives the same answer for
// both values of detach and fails the first case here. One that read the flag
// alone gives different answers for a runner with only one process model, and
// fails the second.
func TestModeForNeedsBothItsInputs(t *testing.T) {
	// A runner that executes the turn in the foreground offers two models, and
	// the flag chooses. Same declaration, different answers.
	attached := RunnerForeground.ModeFor(false)
	detached := RunnerForeground.ModeFor(true)
	if attached == detached {
		t.Errorf("a foreground runner resolves to %d with and without --detach; the flag is not reaching the decision, which is the defect this replaced", attached)
	}
	if attached != LaunchForeground {
		t.Errorf("without --detach a foreground runner resolves to %d, want the turn run in the caller's terminal (%d)", attached, LaunchForeground)
	}
	if detached != LaunchDetached {
		t.Errorf("with --detach a foreground runner resolves to %d, want %d", detached, LaunchDetached)
	}

	// A runner that backgrounds its own session offers one model, and no flag
	// overrides it into the other: there is nothing to run in the foreground.
	// What --detach still decides for it is whether the attach step follows,
	// which is not this function's business.
	for _, detach := range []bool{false, true} {
		if got := RunnerSelfBackgrounding.ModeFor(detach); got != LaunchBackgrounded {
			t.Errorf("a self-backgrounding runner with detach=%v resolves to %d, want %d; an agent offering one model cannot be overridden into the other", detach, got, LaunchBackgrounded)
		}
	}

	// An unset runner is the shape a spec built by hand in a test takes. It
	// resolves to the mode that waits for a hand-off rather than to one that
	// hands the caller's terminal to a process, which is the safe way round.
	if got := RunnerKind(0).ModeFor(false); got != LaunchBackgrounded {
		t.Errorf("an unset runner resolves to %d, want the %d fail-safe", got, LaunchBackgrounded)
	}
}
