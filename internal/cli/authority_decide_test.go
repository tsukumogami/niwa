package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
)

const (
	decideSessionA = "11111111-1111-4111-8111-111111111111"
	decideSessionB = "22222222-2222-4222-8222-222222222222"
)

// decideResult is one run of `niwa authority decide`: what it printed and the
// exit code Execute would turn its error into.
type decideResult struct {
	stdout string
	code   int
	err    error
}

// exitCodeFor mirrors Execute's mapping for the errors decide returns: nil is
// 0, an *authorityExitError carries its own code, anything else is 1.
func exitCodeFor(err error) int {
	if err == nil {
		return 0
	}
	var aee *authorityExitError
	if errors.As(err, &aee) {
		return aee.code
	}
	return 1
}

// runDecideIn runs `niwa authority decide <args>` with cwd at dir, the given
// raw personal config ("" for none) and CLAUDE_CODE_SESSION_ID set to envID
// ("" to leave it empty, which a test running inside a Claude Code session
// needs so its own id never leaks in).
func runDecideIn(t *testing.T, dir, hostConfig, envID string, args ...string) decideResult {
	t.Helper()
	path := permissionModeConfigPath(t)
	if hostConfig != "" {
		writeHostConfig(t, path, hostConfig)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", envID)
	chdirForTest(t, dir)

	var out, errOut bytes.Buffer
	cmd := newAuthorityDecideCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	if args == nil {
		args = []string{}
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return decideResult{stdout: out.String(), code: exitCodeFor(err), err: err}
}

// decideInstance makes an instance under root and returns its directory.
func decideInstance(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := workspace.SaveState(dir, &workspace.InstanceState{SchemaVersion: 1, InstanceName: name, Root: dir}); err != nil {
		t.Fatal(err)
	}
	return dir
}

// decideMapping writes a session mapping carrying the given level and source.
func decideMapping(t *testing.T, root, sessionID, instanceDir, level, source string) {
	t.Helper()
	m := workspace.SessionMapping{
		SessionID:       sessionID,
		InstanceName:    filepath.Base(instanceDir),
		InstancePath:    instanceDir,
		Origin:          "dispatch",
		AuthorityLevel:  level,
		AuthoritySource: source,
	}
	if err := workspace.WriteSessionMapping(root, m); err != nil {
		t.Fatal(err)
	}
}

// writeRawFile writes body to path, creating its directory.
func writeRawFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func answerLines(point, level, decider, exclusion, source string) string {
	return fmt.Sprintf("point=%s\nlevel=%s\ndecider=%s\nauthor_exclusion=%s\nsource=%s\n",
		point, level, decider, exclusion, source)
}

func expectDecide(t *testing.T, got decideResult, wantOut string, wantCode int) {
	t.Helper()
	if got.code != wantCode {
		t.Errorf("exit code = %d, want %d (err: %v)", got.code, wantCode, got.err)
	}
	if got.stdout != wantOut {
		t.Errorf("stdout =\n%s\nwant\n%s", got.stdout, wantOut)
	}
}

// TestAuthorityDecide_ThreeLevels covers each level's answer and exit code,
// read from the mapping CLAUDE_CODE_SESSION_ID names.
func TestAuthorityDecide_ThreeLevels(t *testing.T) {
	cases := []struct {
		level, source string
		want          string
		code          int
	}{
		{"autonomous", "workspace", answerLines("merge", "autonomous", "session", "off", "workspace"), 0},
		{"supervised", "workspace", answerLines("merge", "supervised", "coordinator", "on", "workspace"), 3},
		{"strict", "flag", answerLines("merge", "strict", "person", "on", "flag"), 3},
	}
	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			root := authorityWorkspace(t, "")
			inst := decideInstance(t, root, "inst-1")
			decideMapping(t, root, decideSessionA, inst, tc.level, tc.source)
			got := runDecideIn(t, root, "", decideSessionA, "merge")
			expectDecide(t, got, tc.want, tc.code)
			// Exit 3 is an answer: the error carries no message, so Execute
			// prints nothing beyond the five lines.
			if tc.code == 3 && got.err.Error() != "" {
				t.Errorf("exit 3 carries message %q; want none", got.err.Error())
			}
		})
	}
}

// TestAuthorityDecide_SessionFlagOverridesEnv selects the --session mapping
// in place of the environment's.
func TestAuthorityDecide_SessionFlagOverridesEnv(t *testing.T) {
	root := authorityWorkspace(t, "")
	inst := decideInstance(t, root, "inst-1")
	decideMapping(t, root, decideSessionA, inst, "autonomous", "workspace")
	decideMapping(t, root, decideSessionB, inst, "strict", "flag")
	got := runDecideIn(t, root, "", decideSessionA, "merge", "--session", decideSessionB)
	expectDecide(t, got, answerLines("merge", "strict", "person", "on", "flag"), 3)
}

// TestAuthorityDecide_InstanceRecordBeforeMapping is a worker that asks
// before its mapping exists, with no session id: the instance record
// answers, even in a workspace that is wider.
func TestAuthorityDecide_InstanceRecordBeforeMapping(t *testing.T) {
	root := authorityWorkspace(t, "authority_level = \"autonomous\"\n")
	inst := decideInstance(t, root, "inst-1")
	if err := workspace.WriteAuthorityRecord(inst, workspace.AuthorityRecord{Level: "strict", Source: "flag"}); err != nil {
		t.Fatal(err)
	}
	t.Run("no session id", func(t *testing.T) {
		got := runDecideIn(t, inst, "", "", "merge")
		expectDecide(t, got, answerLines("merge", "strict", "person", "on", "flag"), 3)
	})
	t.Run("an id with no mapping yet", func(t *testing.T) {
		got := runDecideIn(t, inst, "", decideSessionA, "merge")
		expectDecide(t, got, answerLines("merge", "strict", "person", "on", "flag"), 3)
	})
	t.Run("from a subdirectory of the instance", func(t *testing.T) {
		sub := filepath.Join(inst, "repo", "pkg")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		got := runDecideIn(t, sub, "", "", "merge")
		expectDecide(t, got, answerLines("merge", "strict", "person", "on", "flag"), 3)
	})
}

// TestAuthorityDecide_MappingWinsOverRecord: a mapping at strict beside an
// instance record at autonomous answers strict.
func TestAuthorityDecide_MappingWinsOverRecord(t *testing.T) {
	root := authorityWorkspace(t, "")
	inst := decideInstance(t, root, "inst-1")
	if err := workspace.WriteAuthorityRecord(inst, workspace.AuthorityRecord{Level: "autonomous", Source: "default"}); err != nil {
		t.Fatal(err)
	}
	decideMapping(t, root, decideSessionA, inst, "strict", "workspace")
	got := runDecideIn(t, inst, "", decideSessionA, "merge")
	expectDecide(t, got, answerLines("merge", "strict", "person", "on", "workspace"), 3)
}

// TestAuthorityDecide_MappingWithoutLevelFallsThrough: a mapping written
// before the feature (or by the session hook) carries no level, so the
// instance record answers.
func TestAuthorityDecide_MappingWithoutLevelFallsThrough(t *testing.T) {
	root := authorityWorkspace(t, "")
	inst := decideInstance(t, root, "inst-1")
	if err := workspace.WriteAuthorityRecord(inst, workspace.AuthorityRecord{Level: "supervised", Source: "machine"}); err != nil {
		t.Fatal(err)
	}
	decideMapping(t, root, decideSessionA, inst, "", "")
	got := runDecideIn(t, inst, "", decideSessionA, "merge")
	expectDecide(t, got, answerLines("merge", "supervised", "coordinator", "on", "machine"), 3)
}

// TestAuthorityDecide_NoRecordFallback answers from the configuration, marked
// as such, when neither record exists.
func TestAuthorityDecide_NoRecordFallback(t *testing.T) {
	strictWS := "authority_level = \"strict\"\n"
	want := answerLines("merge", "strict", "person", "on", "workspace (no dispatch record)")
	t.Run("workspace root, no session id", func(t *testing.T) {
		root := authorityWorkspace(t, strictWS)
		expectDecide(t, runDecideIn(t, root, "", "", "merge"), want, 3)
	})
	t.Run("workspace root, an id with no mapping", func(t *testing.T) {
		root := authorityWorkspace(t, strictWS)
		expectDecide(t, runDecideIn(t, root, "", decideSessionA, "merge"), want, 3)
	})
	t.Run("an instance with no record", func(t *testing.T) {
		root := authorityWorkspace(t, strictWS)
		inst := decideInstance(t, root, "inst-1")
		expectDecide(t, runDecideIn(t, inst, "", "", "merge"), want, 3)
	})
	t.Run("nothing set anywhere", func(t *testing.T) {
		root := authorityWorkspace(t, "")
		got := runDecideIn(t, root, "", "", "merge")
		expectDecide(t, got, answerLines("merge", "autonomous", "session", "off", "default (no dispatch record)"), 0)
	})
	t.Run("machine narrower than workspace", func(t *testing.T) {
		root := authorityWorkspace(t, "authority_level = \"supervised\"\n")
		got := runDecideIn(t, root, "[global]\nauthority_level = \"strict\"\n", "", "merge")
		expectDecide(t, got, answerLines("merge", "strict", "person", "on", "machine (no dispatch record)"), 3)
	})
	t.Run("an env id that is not a session id is ignored", func(t *testing.T) {
		root := authorityWorkspace(t, strictWS)
		expectDecide(t, runDecideIn(t, root, "", "../../etc/passwd", "merge"), want, 3)
	})
}

// TestAuthorityDecide_BogusRecordsRefuse: a malformed mapping or record
// exits 1 and never falls back to a wider answer.
func TestAuthorityDecide_BogusRecordsRefuse(t *testing.T) {
	t.Run("mapping authority_level bogus", func(t *testing.T) {
		root := authorityWorkspace(t, "")
		inst := decideInstance(t, root, "inst-1")
		if err := workspace.WriteAuthorityRecord(inst, workspace.AuthorityRecord{Level: "autonomous", Source: "default"}); err != nil {
			t.Fatal(err)
		}
		decideMapping(t, root, decideSessionA, inst, "bogus", "workspace")
		got := runDecideIn(t, inst, "", decideSessionA, "merge")
		expectDecide(t, got, "", 1)
	})
	t.Run("mapping with a source and no level", func(t *testing.T) {
		root := authorityWorkspace(t, "")
		inst := decideInstance(t, root, "inst-1")
		decideMapping(t, root, decideSessionA, inst, "", "flag")
		expectDecide(t, runDecideIn(t, root, "", decideSessionA, "merge"), "", 1)
	})
	t.Run("mapping that is not JSON", func(t *testing.T) {
		root := authorityWorkspace(t, "")
		writeRawFile(t, filepath.Join(root, workspace.StateDir, "sessions", decideSessionA+".json"), "{not json")
		expectDecide(t, runDecideIn(t, root, "", decideSessionA, "merge"), "", 1)
	})
	t.Run("instance record authority_level bogus", func(t *testing.T) {
		root := authorityWorkspace(t, "")
		inst := decideInstance(t, root, "inst-1")
		writeRawFile(t, workspace.AuthorityRecordPath(inst), `{"authority_level":"bogus","authority_source":"flag"}`)
		got := runDecideIn(t, inst, "", "", "merge")
		expectDecide(t, got, "", 1)
	})
	t.Run("instance record with a bogus source", func(t *testing.T) {
		root := authorityWorkspace(t, "")
		inst := decideInstance(t, root, "inst-1")
		writeRawFile(t, workspace.AuthorityRecordPath(inst), `{"authority_level":"strict","authority_source":"declaration"}`)
		expectDecide(t, runDecideIn(t, inst, "", "", "merge"), "", 1)
	})
}

// TestAuthorityDecide_PointNames checks the name rule, which is a usage
// error (exit 2) checked before the workspace is looked at.
func TestAuthorityDecide_PointNames(t *testing.T) {
	long64 := "a" + strings.Repeat("b", 63)
	for _, name := range []string{"a", "merge-2", long64} {
		t.Run("accepts "+name, func(t *testing.T) {
			root := authorityWorkspace(t, "")
			got := runDecideIn(t, root, "", "", name)
			expectDecide(t, got, answerLines(name, "autonomous", "session", "off", "default (no dispatch record)"), 0)
		})
	}

	rejected := map[string][]string{
		"Merge_Now":     {"Merge_Now"},
		"2merge":        {"2merge"},
		"-merge":        {"--", "-merge"},
		"-merge as arg": {"-merge"},
		"empty":         {""},
		"65 characters": {long64 + "c"},
		"missing":       nil,
		"two points":    {"merge", "accept"},
		"bad --session": {"merge", "--session", "not-a-uuid"},
		"empty session": {"merge", "--session", ""},
		"unknown flag":  {"merge", "--nope"},
	}
	for name, args := range rejected {
		t.Run("rejects "+name, func(t *testing.T) {
			root := authorityWorkspace(t, "")
			got := runDecideIn(t, root, "", "", args...)
			expectDecide(t, got, "", 2)
			if got.err == nil || got.err.Error() == "" {
				t.Errorf("usage error carries no message: %v", got.err)
			}
		})
	}

	// Usage is checked first: a bad name outside any workspace is still 2.
	t.Run("bad name outside a workspace", func(t *testing.T) {
		expectDecide(t, runDecideIn(t, t.TempDir(), "", "", "Merge_Now"), "", 2)
	})
}

func TestAuthorityDecide_ConfigurationErrors(t *testing.T) {
	t.Run("outside a workspace", func(t *testing.T) {
		got := runDecideIn(t, t.TempDir(), "", "", "merge")
		expectDecide(t, got, "", 1)
		if got.err == nil || !strings.Contains(got.err.Error(), "not inside a niwa workspace") {
			t.Errorf("error %v does not name the missing workspace", got.err)
		}
	})
	t.Run("invalid workspace value", func(t *testing.T) {
		root := authorityWorkspace(t, "authority_level = \"Strict\"\n")
		got := runDecideIn(t, root, "", "", "merge")
		expectDecide(t, got, "", 1)
		if got.err == nil || !strings.Contains(got.err.Error(), "Strict") {
			t.Errorf("error %v does not name the value", got.err)
		}
	})
	t.Run("invalid machine value", func(t *testing.T) {
		root := authorityWorkspace(t, "")
		got := runDecideIn(t, root, "[global]\nauthority_level = \"STRICT\"\n", "", "merge")
		expectDecide(t, got, "", 1)
		if got.err == nil || !strings.Contains(got.err.Error(), "STRICT") {
			t.Errorf("error %v does not name the value", got.err)
		}
	})
	t.Run("invalid machine value beside a valid record", func(t *testing.T) {
		root := authorityWorkspace(t, "")
		inst := decideInstance(t, root, "inst-1")
		if err := workspace.WriteAuthorityRecord(inst, workspace.AuthorityRecord{Level: "strict", Source: "flag"}); err != nil {
			t.Fatal(err)
		}
		expectDecide(t, runDecideIn(t, inst, "[global]\nauthority_level = \"loose\"\n", "", "merge"), "", 1)
	})
}

// TestAuthorityDecide_HundredMappings: decide reads one mapping by id, so a
// workspace holding 100 of them answers well inside a second.
func TestAuthorityDecide_HundredMappings(t *testing.T) {
	root := authorityWorkspace(t, "")
	inst := decideInstance(t, root, "inst-1")
	var target string
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
		level := "autonomous"
		if i == 57 {
			target, level = id, "supervised"
		}
		decideMapping(t, root, id, inst, level, "workspace")
	}
	start := time.Now()
	got := runDecideIn(t, inst, "", target, "merge")
	elapsed := time.Since(start)
	expectDecide(t, got, answerLines("merge", "supervised", "coordinator", "on", "workspace"), 3)
	if elapsed >= time.Second {
		t.Fatalf("decide with 100 mappings took %v; want under 1s", elapsed)
	}
}
