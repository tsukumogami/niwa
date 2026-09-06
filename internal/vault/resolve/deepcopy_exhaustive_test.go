package resolve

import (
	"reflect"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// TestDeepCopyRepos_CopiesEveryRepoOverrideField is the both-sides enumeration
// applied to a hand-written struct copy: enumerate the type's fields, enumerate
// what the copy actually carries across, and assert they agree.
//
// deepCopyRepos rebuilds config.RepoOverride as a keyed literal. A field added
// to the struct and not to that literal is dropped to its zero value on the
// vault-resolved path ONLY -- so the setting works on `niwa worktree create`
// and silently does not on `niwa apply`. Nothing in this repo's toolchain
// catches it: there is no golangci config, no exhaustruct linter, and the
// existing resolve tests assert behaviour rather than field coverage.
//
// This is not a hypothetical. The guard could not be added without first fixing
// a live instance of exactly this: Codex was absent from the literal while its
// immediate neighbour Claude was copied two lines above, and because
// AgentEnabled falls back to the workspace gate on a nil repo override and to
// true when that is unset, a repo's explicit `enabled = false` was silently
// reversed to enabled. See niwa#291.
//
// The mechanism is deliberately reflective rather than a list of field names.
// A list is a third thing to keep in sync, and it would go stale the same way
// the literal did.
func TestDeepCopyRepos_CopiesEveryRepoOverrideField(t *testing.T) {
	rt := reflect.TypeOf(config.RepoOverride{})

	// Build one RepoOverride with every field set to a distinguishable
	// non-zero value, so a field the copy drops comes back as the zero value
	// and is detectable without naming it.
	in := reflect.New(rt).Elem()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		v, ok := nonZeroFor(f.Type)
		if !ok {
			t.Fatalf("field %s has type %s, which this test does not know how to "+
				"populate. Extend nonZeroFor rather than skipping the field: an "+
				"unpopulated field is a field this guard silently stops covering.",
				f.Name, f.Type)
		}
		in.Field(i).Set(v)
	}

	original := in.Interface().(config.RepoOverride)
	out := deepCopyRepos(map[string]config.RepoOverride{"alpha": original})["alpha"]

	got := reflect.ValueOf(out)
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		if got.Field(i).IsZero() {
			t.Errorf("deepCopyRepos dropped field %s: it is set on the input and "+
				"zero on the copy, so this setting is honoured on paths that do not "+
				"resolve secrets and silently lost on `niwa apply`", f.Name)
		}
	}
}

// nonZeroFor produces a distinguishable non-zero value for the field kinds
// RepoOverride uses. It returns false for a kind it does not handle, so a new
// field of an unfamiliar shape fails the test loudly rather than being skipped.
func nonZeroFor(t reflect.Type) (reflect.Value, bool) {
	switch t.Kind() {
	case reflect.String:
		return reflect.ValueOf("x").Convert(t), true
	case reflect.Bool:
		return reflect.ValueOf(true).Convert(t), true
	case reflect.Ptr:
		inner, ok := nonZeroFor(t.Elem())
		if !ok {
			return reflect.Value{}, false
		}
		p := reflect.New(t.Elem())
		p.Elem().Set(inner)
		return p, true
	case reflect.Map:
		m := reflect.MakeMap(t)
		k, ok := nonZeroFor(t.Key())
		if !ok {
			return reflect.Value{}, false
		}
		v, ok := nonZeroFor(t.Elem())
		if !ok {
			return reflect.Value{}, false
		}
		m.SetMapIndex(k, v)
		return m, true
	case reflect.Slice:
		e, ok := nonZeroFor(t.Elem())
		if !ok {
			return reflect.Value{}, false
		}
		s := reflect.MakeSlice(t, 1, 1)
		s.Index(0).Set(e)
		return s, true
	case reflect.Struct:
		// A struct is non-zero once any one exported field is non-zero.
		s := reflect.New(t).Elem()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			v, ok := nonZeroFor(f.Type)
			if !ok {
				continue
			}
			s.Field(i).Set(v)
			return s, true
		}
		return reflect.Value{}, false
	default:
		return reflect.Value{}, false
	}
}

// TestDeepCopyRepos_CodexOverrideIsIndependent pins the pointer-safety half.
// Listing the field in the literal is not enough on its own: sharing the
// pointer would make a later mutation through one config visible through the
// other, which is the bug deep-copying exists to prevent.
func TestDeepCopyRepos_CodexOverrideIsIndependent(t *testing.T) {
	disabled := false
	in := map[string]config.RepoOverride{
		"alpha": {Codex: &config.CodexOverride{Enabled: &disabled}},
	}

	out := deepCopyRepos(in)

	if out["alpha"].Codex == nil {
		t.Fatal("Codex override was dropped by deepCopyRepos")
	}
	if got := *out["alpha"].Codex.Enabled; got != false {
		t.Fatalf("Codex.Enabled = %v, want false", got)
	}

	// Mutating the copy must not reach the original.
	enabled := true
	out["alpha"].Codex.Enabled = &enabled
	if *in["alpha"].Codex.Enabled != false {
		t.Error("mutating the copy changed the original: the Codex override is shared, not copied")
	}
}
