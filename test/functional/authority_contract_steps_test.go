package functional

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// The gate-contract scenarios run the guide's reference checks the way a gate
// would: sourced into a stub gate whose own rule (today's behaviour) allows an
// instance on its exemption list and denies the rest. When niwa_grant_decides
// returns 0 the grant decides instead, allowing only on niwa_holds plus
// niwa_settles. The checks are read from the guide on every run, so a change
// to the guide is what these scenarios test.
//
// Each run gets a PATH holding only the tools the checks use and the niwa the
// scenario chose: the built binary, a fake standing for an older or broken
// niwa, or none at all. Nothing from the developer's own PATH can answer for
// niwa.

// gateLookupBound is the bound the stub gate sets for each lookup, in
// seconds. It sits behind niwa's own two-second watchdog, as the guide says a
// gate's bound should.
const gateLookupBound = 3

// gateRunDeadline is how long one stub gate run may take before the scenario
// calls it hung and kills it.
const gateRunDeadline = 20 * time.Second

// authorityGateScenario is what these steps remember between steps.
type authorityGateScenario struct {
	dir       string        // where the checks, the stub gate and the gate's PATH live
	checks    string        // the guide's reference block, as extracted
	niwa      string        // which niwa the gate's PATH carries
	noTimeout bool          // leave timeout off the gate's PATH, forcing the background-job bound
	target    string        // the directory the checks anchor on; empty means the dispatch instance
	elapsed   time.Duration // how long the last run took
}

// stubGateScript is the stub gate. `call` runs one check and prints its
// return code; `gate` prints the gate's verdict. Either prints its last line
// only if the sourced checks returned, so a check that exits leaves the run
// with no answer, which fails the scenario.
const stubGateScript = `. "$GATE_CHECKS"
NIWA_LOOKUP_SECONDS=%d
mode=$1
shift
case "$mode" in
  call)
    "$@"
    echo "rc=$?"
    ;;
  gate)
    dir=$1
    authority=$2
    point=$3
    if grep -qxF "$dir" "$GATE_EXEMPT"; then
      today=allow
    else
      today=deny
    fi
    if niwa_grant_decides "$dir"; then
      if niwa_holds "$dir" "$authority" && niwa_settles "$dir" "$point"; then
        verdict=allow
      else
        verdict=deny
      fi
    else
      verdict=$today
    fi
    echo "verdict=$verdict"
    ;;
esac
`

// Fake niwas, each standing for a lookup that must read as not held. The
// fakes answering decide answer it as a session that settles, so only the
// check under test can be what turns the answer down.
var gateFakeNiwas = map[string]string{
	"old-help": `echo 'niwa manages multi-repo workspaces.'
echo
echo 'Usage:'
echo '  niwa [command]'
echo
echo 'Available Commands:'
echo '  apply       Apply the workspace configuration'
echo '  dispatch    Launch a worker in a fresh instance'
exit 0
`,
	"unknown-schema": `case "$2" in
  show) echo '{"schema": "niwa.authority.grants/v1", "record": true, "grant_ceiling_source": "workspace", "grant": [{"authority": "merge-pr", "sources": ["dispatch"]}]}' ;;
  decide) printf 'point=%s\nlevel=autonomous\ndecider=session\nauthor_exclusion=off\nsource=workspace\nrecord=present\n' "$3" ;;
esac
exit 0
`,
	"malformed": `case "$2" in
  show) printf '%s' '{"schema": "niwa.authority.record/v1", "record": true, "grant_ceiling_source": "workspace", "grant": [{"authority": "merge-pr", "sources": ["dispat' ;;
  decide) printf 'point=%s\nlevel=autonomous\ndecider=session\nauthor_exclusion=off\nsource=workspace\nrecord=present\n' "$3" ;;
esac
exit 0
`,
	// exec, so the process the bound kills is the one holding the output.
	"hang": `exec sleep 60
`,
}

func registerAuthorityGateSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the stub gate sources the reference checks from the authority guide$`, theStubGateSourcesTheGuideChecks)
	ctx.Step(`^the gate's niwa is "([^"]*)"$`, theGatesNiwaIs)
	ctx.Step(`^the gate has no timeout command$`, theGateHasNoTimeoutCommand)
	ctx.Step(`^the gate has a timeout command$`, theGateHasATimeoutCommand)
	ctx.Step(`^the gate looks up the workspace root$`, theGateLooksUpTheWorkspaceRoot)
	ctx.Step(`^the gate looks up the dispatch instance$`, theGateLooksUpTheDispatchInstance)
	ctx.Step(`^"(niwa_\w+)((?: [a-z-]+)*)" returns (0|non-zero)$`, theCheckReturns)
	ctx.Step(`^the stub gate (allows|denies) "([^"]*)" at point "([^"]*)" for (an exempt|a non-exempt) instance$`, theStubGateVerdict)
	ctx.Step(`^the last gate run finished within the gate's bound$`, theLastGateRunFinishedWithinTheBound)
	ctx.Step(`^the gate's "niwa authority decide ([a-z-]+)" prints "([^"]*)" and "([^"]*)"$`, theGatesDecidePrints)
}

func theStubGateSourcesTheGuideChecks(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	block, err := authorityGuideChecks()
	if err != nil {
		return ctx, err
	}
	dir := filepath.Join(s.sandbox, "gate")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ctx, err
	}
	if err := os.WriteFile(filepath.Join(dir, "checks.sh"), []byte(block), 0o644); err != nil {
		return ctx, err
	}
	if err := os.WriteFile(filepath.Join(dir, "gate.sh"), []byte(fmt.Sprintf(stubGateScript, gateLookupBound)), 0o644); err != nil {
		return ctx, err
	}
	s.gate = authorityGateScenario{dir: dir, checks: block, niwa: "built"}
	return ctx, nil
}

func theGatesNiwaIs(ctx context.Context, kind string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if _, ok := gateFakeNiwas[kind]; !ok && kind != "built" && kind != "missing" {
		return ctx, fmt.Errorf("no gate niwa %q", kind)
	}
	s.gate.niwa = kind
	return ctx, nil
}

func theGateHasNoTimeoutCommand(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.gate.noTimeout = true
	return ctx, nil
}

// theGateHasATimeoutCommand needs one on the host to link into the gate's
// PATH. macOS ships none, so there the scenario skips and the background-job
// scenario covers the bound.
func theGateHasATimeoutCommand(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if _, err := exec.LookPath("timeout"); err != nil {
		return ctx, godog.ErrSkip
	}
	s.gate.noTimeout = false
	return ctx, nil
}

func theGateLooksUpTheWorkspaceRoot(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.gate.target = s.workspaceRoot
	return ctx, nil
}

func theGateLooksUpTheDispatchInstance(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return ctx, fmt.Errorf("no dispatch instance recorded")
	}
	s.gate.target = s.lastDispatchInstancePath
	return ctx, nil
}

// gateTarget is the directory the checks anchor on: the one a step chose,
// else the last dispatch instance, else the workspace root (a directory the
// fake niwas never look at).
func gateTarget(s *testState) string {
	switch {
	case s.gate.target != "":
		return s.gate.target
	case s.lastDispatchInstancePath != "":
		return s.lastDispatchInstancePath
	}
	return s.workspaceRoot
}

// gatePath builds the gate's PATH directory for one run: the tools the
// checks and the stub gate call, and the niwa the scenario chose.
func gatePath(s *testState) (string, error) {
	bin := filepath.Join(s.gate.dir, "bin")
	if err := os.RemoveAll(bin); err != nil {
		return "", err
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return "", err
	}
	tools := []string{"jq", "grep", "sleep"}
	if !s.gate.noTimeout {
		tools = append(tools, "timeout")
	}
	for _, tool := range tools {
		p, err := exec.LookPath(tool)
		if err != nil {
			if tool == "timeout" {
				continue
			}
			return "", fmt.Errorf("the gate-contract scenarios need %s on PATH: %w", tool, err)
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			return "", err
		}
	}
	switch kind := s.gate.niwa; kind {
	case "built":
		if err := os.Symlink(s.binPath, filepath.Join(bin, "niwa")); err != nil {
			return "", err
		}
	case "missing":
	default:
		script := "#!/bin/sh\n" + gateFakeNiwas[kind]
		if err := os.WriteFile(filepath.Join(bin, "niwa"), []byte(script), 0o755); err != nil {
			return "", err
		}
	}
	return bin, nil
}

// runStubGate runs the stub gate once and returns its last output line. A run
// past gateRunDeadline is killed and fails the scenario: a gate that hangs
// loses its verdict to the harness that kills it.
func runStubGate(s *testState, exempt bool, args ...string) (string, error) {
	if s.gate.dir == "" {
		return "", fmt.Errorf("the stub gate is not set up")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		return "", fmt.Errorf("the gate-contract scenarios need bash: %w", err)
	}
	bin, err := gatePath(s)
	if err != nil {
		return "", err
	}
	exemptList := filepath.Join(s.gate.dir, "exempt")
	listed := ""
	if exempt {
		listed = gateTarget(s) + "\n"
	}
	if err := os.WriteFile(exemptList, []byte(listed), 0o644); err != nil {
		return "", err
	}
	runCtx, cancel := context.WithTimeout(context.Background(), gateRunDeadline)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bash, append([]string{filepath.Join(s.gate.dir, "gate.sh")}, args...)...)
	cmd.Dir = s.homeDir
	cmd.Env = append(s.buildEnv(),
		"PATH="+bin,
		"GATE_CHECKS="+filepath.Join(s.gate.dir, "checks.sh"),
		"GATE_EXEMPT="+exemptList,
	)
	cmd.WaitDelay = time.Second
	start := time.Now()
	out, runErr := cmd.CombinedOutput()
	s.gate.elapsed = time.Since(start)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("the stub gate hung: no answer after %s\n%s", gateRunDeadline, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	last := lines[len(lines)-1]
	if runErr != nil {
		return last, fmt.Errorf("the stub gate failed: %v\n%s", runErr, out)
	}
	return last, nil
}

// theCheckReturns calls one reference check against the gate's target.
func theCheckReturns(ctx context.Context, fn, args, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	callArgs := append([]string{"call", fn, gateTarget(s)}, strings.Fields(args)...)
	got, err := runStubGate(s, false, callArgs...)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(got, "rc=") {
		return fmt.Errorf("%s%s gave no return code (did it exit?): last line %q", fn, args, got)
	}
	rc := strings.TrimPrefix(got, "rc=")
	if (want == "0") != (rc == "0") {
		return fmt.Errorf("%s%s returned %s, want %s", fn, args, rc, want)
	}
	return nil
}

func theStubGateVerdict(ctx context.Context, verb, authority, point, exempt string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	got, err := runStubGate(s, exempt == "an exempt", "gate", gateTarget(s), authority, point)
	if err != nil {
		return err
	}
	want := map[string]string{"allows": "verdict=allow", "denies": "verdict=deny"}[verb]
	if got != want {
		return fmt.Errorf("the stub gate %s %s at %s for %s instance: got %q, want %q", verb, authority, point, exempt, got, want)
	}
	return nil
}

func theLastGateRunFinishedWithinTheBound(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	// One lookup's bound plus room for process start and the kill.
	limit := time.Duration(gateLookupBound)*time.Second + 2*time.Second
	if s.gate.elapsed > limit {
		return fmt.Errorf("the last stub gate run took %s, past the gate's %ds bound", s.gate.elapsed, gateLookupBound)
	}
	return nil
}

// theGatesDecidePrints runs decide through the gate's own niwa and lookup
// helper, so a scenario can show what the settle check turned down.
func theGatesDecidePrints(ctx context.Context, point, lineA, lineB string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if err := runNiwa(s, s.homeDir, "niwa authority decide "+point+" -C "+gateTarget(s)); err != nil {
		return err
	}
	if s.exitCode != 0 {
		return fmt.Errorf("niwa authority decide exited %d\n%s%s", s.exitCode, s.stdout, s.stderr)
	}
	lines := strings.Split(s.stdout, "\n")
	for _, want := range []string{lineA, lineB} {
		found := false
		for _, l := range lines {
			if l == want {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("decide printed no line %q:\n%s", want, s.stdout)
		}
	}
	return nil
}
