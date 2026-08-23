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
		// Neither reads a lock, so declaring where one lives would be a fact
		// nothing consults -- and a reader that later grew to consult it would
		// be acting on a claim nobody checked.
		if len(r.WriterLockPath) > 0 || r.WriterLockSuffix != "" {
			t.Errorf("(%s): the session records describe a writer lock that liveness kind %d never reads", ag, r.Liveness)
		}
	case LivenessRecordActivity:
		// Both halves of the activity rule are dereferenced by the reader, so
		// an incomplete description is a probe against a path built from an
		// empty string rather than a test failure.
		if len(r.WriterLockPath) == 0 {
			t.Errorf("(%s): liveness is read from record activity, but the records name no writer-lock directory", ag)
		}
		if r.WriterLockSuffix == "" {
			t.Errorf("(%s): liveness is read from record activity, but the records name no writer-lock file suffix", ag)
		}
		// The lock file is named for the session id, so a store that cannot
		// produce one has nothing to build the path from.
		if len(r.IDPath) == 0 {
			t.Errorf("(%s): liveness is read from record activity, but the records name no session-id field to find the lock by", ag)
		}
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
// A read counts only when the field is selected off a value the scan can show
// holds the structure that declares it. Matching the field name anywhere in
// these two packages would not do: Mode, Depth, Handle, Settings, Model and
// Flags each appear as a selector on some unrelated value here -- cmd.Flags()
// alone is on about twenty cobra commands -- so a name-based scan reports
// LaunchSpec.Flags, the container for every pass-through spelling, as read no
// matter what the launcher does with it.
//
// What replaces the name match, with no type checker behind it, is a walk from
// a root the scan can name. Roots are the values these two packages declare as
// one of the three structures: a parameter or receiver typed as one, a function
// or method returning one, a variable holding one, a struct field declared as
// one -- all collected from the declarations rather than written out here, so
// renaming any of them keeps this working. From a root the chain resolves left
// to right against the structures' own fields (spec.Records.Depth marks
// LaunchSpec.Records and then SessionRecords.Depth), which is exact in both
// directions.
//
// The cost is that a value reaching a field by a shape the walk cannot follow
// reads as unread, and the failure looks like dead plumbing when it is really a
// gap in the scan. If you hit that, the fix is to teach
// collectLaunchDescriptionProducers the new shape -- not to loosen the walk
// back to matching names.
func TestEveryLaunchSpecFieldIsRead(t *testing.T) {
	fields := map[string]map[string]bool{}
	for _, typ := range launchDescriptionTypes() {
		seen := map[string]bool{}
		for i := 0; i < typ.NumField(); i++ {
			seen[typ.Field(i).Name] = false
		}
		fields[typ.Name()] = seen
	}

	var files []*ast.File
	for _, dir := range []string{leafDir, "../cli"} {
		for _, pf := range parsePackageFiles(t, dir) {
			files = append(files, pf.file)
		}
	}
	producers := map[string]string{}
	for _, f := range files {
		collectLaunchDescriptionProducers(f, producers)
	}
	if len(producers) == 0 {
		t.Fatal("nothing in these two packages declares a launch description; the scan would report every field unread")
	}
	for _, f := range files {
		markLaunchDescriptionReads(f, producers, fields)
	}

	var unread []string
	for owner, seen := range fields {
		for name, ok := range seen {
			if !ok {
				unread = append(unread, owner+"."+name)
			}
		}
	}
	if len(unread) > 0 {
		slices.Sort(unread)
		t.Fatalf("%d launch-description field(s) are declared and read by nothing:\n  %s\nEither wire the field up, or delete it until the change that needs it. A field nothing reads is the shape that closed the prior attempt at this feature.",
			len(unread), strings.Join(unread, "\n  "))
	}
}

// launchDescriptionTypes is the launch description and the two structures it
// carries.
func launchDescriptionTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(LaunchSpec{}),
		reflect.TypeOf(SessionRecords{}),
		reflect.TypeOf(LaunchFlags{}),
	}
}

// launchDescriptionFieldTypes maps each of the three structures to its fields,
// and each field to the structure its own type is when that type is also one of
// the three -- "" otherwise. It is the oracle the chain walk resolves against,
// taken from the types themselves rather than written out, so a field that
// starts or stops carrying one of them needs no edit here.
func launchDescriptionFieldTypes() map[string]map[string]string {
	tracked := map[string]bool{}
	for _, typ := range launchDescriptionTypes() {
		tracked[typ.Name()] = true
	}
	out := map[string]map[string]string{}
	for _, typ := range launchDescriptionTypes() {
		fields := map[string]string{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.Type.Kind() == reflect.Struct && tracked[f.Type.Name()] {
				fields[f.Name] = f.Type.Name()
				continue
			}
			fields[f.Name] = ""
		}
		out[typ.Name()] = fields
	}
	return out
}

// collectLaunchDescriptionProducers records every name in these two packages
// that yields one of the three structures: a function or method that returns
// one, a variable holding one (the table, a func value), and a struct field
// declared as one. They are read off the declarations rather than written out,
// so renaming any of them keeps the scan working, and a new way to reach a spec
// shows up here rather than as a field the guard says nothing reads.
func collectLaunchDescriptionProducers(file *ast.File, into map[string]string) {
	named := launchDescriptionNamer(file)

	resultType := func(ft *ast.FuncType) (string, bool) {
		if ft == nil || ft.Results == nil {
			return "", false
		}
		for _, r := range ft.Results.List {
			if name, ok := named(r.Type); ok {
				return name, true
			}
		}
		return "", false
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if name, ok := resultType(d.Type); ok {
				into[d.Name.Name] = name
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					st, isStruct := sp.Type.(*ast.StructType)
					if !isStruct || st.Fields == nil {
						continue
					}
					for _, f := range st.Fields.List {
						name, ok := named(f.Type)
						if !ok {
							continue
						}
						for _, id := range f.Names {
							into[id.Name] = name
						}
					}
				case *ast.ValueSpec:
					for i, id := range sp.Names {
						if name, ok := containedType(sp.Type, named); ok {
							into[id.Name] = name
							continue
						}
						if i >= len(sp.Values) {
							continue
						}
						if lit, isLit := sp.Values[i].(*ast.FuncLit); isLit {
							if name, ok := resultType(lit.Type); ok {
								into[id.Name] = name
							}
						}
					}
				}
			}
		}
	}
}

// containedType resolves the tracked structure a declared type yields when it
// is asked for one: the type itself, a map or slice of it, or a function
// returning it.
func containedType(expr ast.Expr, named func(ast.Expr) (string, bool)) (string, bool) {
	if expr == nil {
		return "", false
	}
	if name, ok := named(expr); ok {
		return name, true
	}
	switch e := expr.(type) {
	case *ast.MapType:
		return containedType(e.Value, named)
	case *ast.ArrayType:
		return containedType(e.Elt, named)
	case *ast.FuncType:
		if e.Results == nil {
			return "", false
		}
		for _, r := range e.Results.List {
			if name, ok := named(r.Type); ok {
				return name, true
			}
		}
	}
	return "", false
}

// markLaunchDescriptionReads walks one file and marks a field read only when it
// is selected off a value the scan can show holds the structure that declares
// it. See TestEveryLaunchSpecFieldIsRead for why the name alone will not do.
func markLaunchDescriptionReads(file *ast.File, producers map[string]string, read map[string]map[string]bool) {
	types := launchDescriptionFieldTypes()
	namedType := launchDescriptionNamer(file)

	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		// Identifiers this function can be shown to hold one of the three
		// structures in, and which one.
		roots := map[string]string{}
		addField := func(list *ast.FieldList) {
			if list == nil {
				return
			}
			for _, f := range list.List {
				name, ok := namedType(f.Type)
				if !ok {
					continue
				}
				for _, id := range f.Names {
					roots[id.Name] = name
				}
			}
		}
		addField(fn.Recv)
		addField(fn.Type.Params)
		addField(fn.Type.Results)

		// The type of a chain, resolved left to right, marking each field it
		// steps through as read. A chain the scan cannot root is not a read.
		var chainType func(expr ast.Expr) (string, bool)
		chainType = func(expr ast.Expr) (string, bool) {
			switch e := expr.(type) {
			case *ast.ParenExpr:
				return chainType(e.X)
			case *ast.StarExpr:
				return chainType(e.X)
			case *ast.UnaryExpr:
				return chainType(e.X)
			case *ast.Ident:
				name, ok := roots[e.Name]
				if !ok {
					name, ok = producers[e.Name]
				}
				return name, ok
			case *ast.IndexExpr:
				return chainType(e.X)
			case *ast.CompositeLit:
				return namedType(e.Type)
			case *ast.CallExpr:
				switch fn := e.Fun.(type) {
				case *ast.Ident:
					name, ok := producers[fn.Name]
					return name, ok
				case *ast.SelectorExpr:
					name, ok := producers[fn.Sel.Name]
					return name, ok
				}
				return "", false
			case *ast.SelectorExpr:
				owner, ok := chainType(e.X)
				if !ok {
					// A selector that names a producer is one whatever it
					// sits on: req.Spec is declared as a launch spec, so it is
					// one however req got here.
					name, isProducer := producers[e.Sel.Name]
					return name, isProducer
				}
				fieldType, isField := types[owner][e.Sel.Name]
				if !isField {
					return "", false
				}
				read[owner][e.Sel.Name] = true
				return fieldType, fieldType != ""
			}
			return "", false
		}

		// Two passes, because a chain may be assigned to a local before the
		// pass that would have discovered that local ran.
		for pass := 0; pass < 2; pass++ {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				switch s := n.(type) {
				case *ast.AssignStmt:
					for i, lhs := range s.Lhs {
						id, isIdent := lhs.(*ast.Ident)
						if !isIdent || i >= len(s.Rhs) {
							continue
						}
						if name, ok := chainType(s.Rhs[i]); ok {
							roots[id.Name] = name
						}
					}
					// x, ok := m[k] and x, ok := f() land here.
					if len(s.Lhs) == 2 && len(s.Rhs) == 1 {
						if id, isIdent := s.Lhs[0].(*ast.Ident); isIdent {
							if name, ok := chainType(s.Rhs[0]); ok {
								roots[id.Name] = name
							}
						}
					}
				case *ast.RangeStmt:
					if s.Value != nil {
						if id, isIdent := s.Value.(*ast.Ident); isIdent {
							if name, ok := chainType(s.X); ok {
								roots[id.Name] = name
							}
						}
					}
				case *ast.ValueSpec:
					name, ok := namedType(s.Type)
					if !ok {
						return true
					}
					for _, id := range s.Names {
						roots[id.Name] = name
					}
				}
				return true
			})
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if sel, isSel := n.(*ast.SelectorExpr); isSel {
				chainType(sel)
			}
			return true
		})
		return true
	})
}

// launchDescriptionNamer answers, for one file, whether a type expression names
// one of the three structures: LaunchSpec inside this package,
// agentplan.LaunchSpec outside it, under whatever alias the file's own import
// gives this package, so an aliased import cannot blind the scan.
func launchDescriptionNamer(file *ast.File) func(ast.Expr) (string, bool) {
	types := launchDescriptionFieldTypes()
	alias := ""
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasSuffix(path, "/internal/agentplan") {
			continue
		}
		alias = "agentplan"
		if imp.Name != nil {
			alias = imp.Name.Name
		}
	}
	unwrap := func(expr ast.Expr) ast.Expr {
		for {
			switch e := expr.(type) {
			case *ast.StarExpr:
				expr = e.X
			case *ast.ParenExpr:
				expr = e.X
			default:
				return expr
			}
		}
	}
	return func(expr ast.Expr) (string, bool) {
		if expr == nil {
			return "", false
		}
		switch e := unwrap(expr).(type) {
		case *ast.Ident:
			if _, ok := types[e.Name]; ok && alias == "" {
				return e.Name, true
			}
		case *ast.SelectorExpr:
			pkg, isIdent := e.X.(*ast.Ident)
			if !isIdent || alias == "" || pkg.Name != alias {
				return "", false
			}
			if _, ok := types[e.Sel.Name]; ok {
				return e.Sel.Name, true
			}
		}
		return "", false
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
