package removal

import (
	"strings"
	"testing"
)

func TestResolveJobsRoot(t *testing.T) {
	env := func(v string, set bool) func(string) (string, bool) {
		return func(name string) (string, bool) {
			if name != ConfigDirVar {
				t.Fatalf("looked up %q", name)
			}
			return v, set
		}
	}
	for _, tc := range []struct {
		name    string
		value   string
		set     bool
		home    string
		want    string
		wantErr bool
	}{
		{name: "unset", home: "/home/p", want: "/home/p/.claude/jobs"},
		{name: "empty", set: true, home: "/home/p", want: "/home/p/.claude/jobs"},
		{name: "absolute and clean", value: "/cfg/claude", set: true, home: "/home/p", want: "/cfg/claude/jobs"},
		{name: "tilde in the middle is not expanded", value: "/cfg/~/claude", set: true, home: "/home/p", want: "/cfg/~/claude/jobs"},
		{name: "relative", value: "cfg/claude", set: true, home: "/home/p", wantErr: true},
		{name: "dot relative", value: "./cfg", set: true, home: "/home/p", wantErr: true},
		{name: "leading tilde", value: "~/cfg", set: true, home: "/home/p", wantErr: true},
		{name: "dot dot", value: "/cfg/../claude", set: true, home: "/home/p", wantErr: true},
		{name: "dot", value: "/cfg/./claude", set: true, home: "/home/p", wantErr: true},
		{name: "repeated separator", value: "/cfg//claude", set: true, home: "/home/p", wantErr: true},
		{name: "trailing separator", value: "/cfg/claude/", set: true, home: "/home/p", wantErr: true},
		{name: "no home", home: "", wantErr: true},
		{name: "relative home", home: "home/p", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveJobsRoot(env(tc.value, tc.set), tc.home)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolveJobsRoot gave %q; want an error", got)
				}
				if tc.set && !strings.Contains(err.Error(), ConfigDirVar) {
					t.Fatalf("error %q doesn't name %s", err, ConfigDirVar)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ResolveJobsRoot = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
