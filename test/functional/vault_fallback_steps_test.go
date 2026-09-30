package functional

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cucumber/godog"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/store"
)

// vault_fallback_steps_test.go holds the steps the store-fallback scenarios
// (features/vault-fallback.feature) use: filling and inspecting the store of
// last-resolved values in the scenario's sandbox, timing a run, checking a
// forked stub child is gone, reading the session-start hook's payload, and
// scanning what a run wrote for secret values.

// cloudAPIDomain is the Infisical cloud default, the API domain a provider
// with no api_url and no NIWA_INFISICAL_API_URL resolves to.
const cloudAPIDomain = "https://app.infisical.com"

// sandboxStateHome is the XDG_STATE_HOME buildEnv gives every niwa
// subprocess: inside the scenario's sandboxed HOME, where the store would
// land with XDG_STATE_HOME unset too.
func sandboxStateHome(s *testState) string {
	return filepath.Join(s.homeDir, ".local", "state")
}

// scenarioStoreDir is the store directory the scenario's niwa runs use,
// following an XDG_STATE_HOME the scenario set itself.
func scenarioStoreDir(s *testState) string {
	base := sandboxStateHome(s)
	if v, ok := s.envOverrides["XDG_STATE_HOME"]; ok && filepath.IsAbs(v) {
		base = v
	}
	return filepath.Join(base, "niwa", "secret-cache")
}

// scenarioIdentity is the store identity niwa derives for an infisical
// provider in this scenario: the API domain follows NIWA_INFISICAL_API_URL
// when the scenario set it (a provider declaring no api_url), else the cloud
// default.
func scenarioIdentity(s *testState, project, env, folder string) vault.Identity {
	domain := cloudAPIDomain
	if v := s.envOverrides["NIWA_INFISICAL_API_URL"]; v != "" {
		domain = v
	}
	return vault.NormalizeIdentity(vault.Identity{
		Kind:        "infisical",
		APIDomain:   domain,
		ProjectID:   project,
		Environment: env,
		FolderPath:  folder,
	})
}

// ageUnit turns a step's "N days" into a duration.
func ageUnit(n int, unit string) time.Duration {
	switch strings.TrimSuffix(unit, "s") {
	case "minute":
		return time.Duration(n) * time.Minute
	case "hour":
		return time.Duration(n) * time.Hour
	default:
		return time.Duration(n) * 24 * time.Hour
	}
}

// theSecretStoreHolds pre-fills the store through the store package itself,
// with a resolution time backdated by the given age, so a scenario can
// fall back on values no earlier run resolved.
func theSecretStoreHolds(ctx context.Context, key, value, project, env, folder, nStr, unit string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	n, err := strconv.Atoi(nStr)
	if err != nil {
		return err
	}
	id := scenarioIdentity(s, project, env, folder)
	entry := store.Entry{Value: []byte(value), ResolvedAt: time.Now().Add(-ageUnit(n, unit)), VersionToken: "prefilled"}
	return store.Update(scenarioStoreDir(s), id, map[string]store.Entry{key: entry}, nil, false)
}

// theSecretStoreHoldsKey asserts whether the store holds a key for an identity.
func theSecretStoreHoldsKey(ctx context.Context, negation, key, project, env, folder string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	entries, err := store.Load(scenarioStoreDir(s), scenarioIdentity(s, project, env, folder))
	if err != nil {
		return fmt.Errorf("loading the store: %w", err)
	}
	_, held := entries[key]
	if want := negation == ""; held != want {
		return fmt.Errorf("store holds %q for %s %s %s: %v, want %v", key, project, env, folder, held, want)
	}
	return nil
}

// theSecretStoreIsInsideAGitWorkTree points XDG_STATE_HOME at a directory
// below a `.git` entry, which is all the store's work-tree walk looks for.
func theSecretStoreIsInsideAGitWorkTree(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	repo := filepath.Join(s.tmpDir, "dotfiles")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		return err
	}
	s.envOverrides["XDG_STATE_HOME"] = filepath.Join(repo, "state")
	return nil
}

// theSecretStoreIsUnwritable puts a regular file where the store directory
// belongs, so niwa can neither read nor write it.
func theSecretStoreIsUnwritable(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir := scenarioStoreDir(s)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	return os.WriteFile(dir, []byte("not a directory\n"), 0o600)
}

// aProviderAuthFile writes the sandbox's provider-auth.toml with one
// machine identity for an infisical project, so niwa logs in with universal
// auth (against the REST double, through NIWA_INFISICAL_API_URL) and runs
// its exports as a minted principal.
func aProviderAuthFile(ctx context.Context, project, clientSecret string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.homeDir, ".config", "niwa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	body := fmt.Sprintf("[[providers]]\nkind = \"infisical\"\nproject = %q\nclient_id = \"fb-client-id\"\nclient_secret = %q\n", project, clientSecret)
	return os.WriteFile(filepath.Join(dir, "provider-auth.toml"), []byte(body), 0o600)
}

// theCommandFinishedWithin bounds the last command's wall time.
func theCommandFinishedWithin(ctx context.Context, secStr string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	sec, err := strconv.Atoi(secStr)
	if err != nil {
		return err
	}
	if limit := time.Duration(sec) * time.Second; s.lastRunDuration > limit {
		return fmt.Errorf("command took %s, want at most %s\nstderr:\n%s", s.lastRunDuration, limit, s.stderr)
	}
	return nil
}

// theErrorOutputContainsTimes counts occurrences of text on stderr.
func theErrorOutputContainsTimes(ctx context.Context, text, nStr string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	n, err := strconv.Atoi(nStr)
	if err != nil {
		return err
	}
	if got := strings.Count(s.stderr, text); got != n {
		return fmt.Errorf("stderr holds %q %d times, want %d:\n%s", text, got, n, s.stderr)
	}
	return nil
}

// stubChildPIDFile is where the stub's hang-fork modes record their child.
func stubChildPIDFile(s *testState) string {
	return filepath.Join(s.tmpDir, "infisical-stub-child.pid")
}

func theInfisicalStubRecordsForkedChildren(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	s.envOverrides["INFISICAL_STUB_CHILD_PID_FILE"] = stubChildPIDFile(s)
	return nil
}

// theStubsForkedChildIsGone asserts every child the stub forked no longer
// exists. The run has already returned, so the check allows only a short
// grace for the kernel to reap a process that was killed as the run ended.
func theStubsForkedChildIsGone(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	data, err := os.ReadFile(stubChildPIDFile(s))
	if err != nil {
		return fmt.Errorf("the stub recorded no child PID: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return fmt.Errorf("the stub recorded no child PID")
	}
	deadline := time.Now().Add(2 * time.Second)
	for _, f := range fields {
		pid, err := strconv.Atoi(f)
		if err != nil || pid <= 0 {
			return fmt.Errorf("bad child PID %q", f)
		}
		for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			if time.Now().After(deadline) {
				// Don't leave it running for the rest of the suite.
				_ = syscall.Kill(pid, syscall.SIGKILL)
				return fmt.Errorf("the stub's forked child %d outlived the run", pid)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return nil
}

// theFileInTheDispatchInstanceContains checks a materialized file of the
// newest dispatch instance.
func theFileInTheDispatchInstanceContains(ctx context.Context, relPath, text string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst := s.lastDispatchInstancePath
	if inst == "" {
		inst = findDispatchInstance(s.workspaceRoot)
	}
	if inst == "" {
		return fmt.Errorf("no dispatch instance under %s", s.workspaceRoot)
	}
	data, err := os.ReadFile(filepath.Join(inst, relPath))
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), text) {
		return fmt.Errorf("%s in %s does not contain %q:\n%s", relPath, inst, text, data)
	}
	return nil
}

// hookContext decodes the session-start hook's stdout payload and returns
// its additionalContext.
func hookContext(s *testState) (string, error) {
	var payload struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(s.stdout), &payload); err != nil {
		return "", fmt.Errorf("decoding the hook payload %q: %w\nstderr:\n%s", s.stdout, err, s.stderr)
	}
	return payload.HookSpecificOutput.AdditionalContext, nil
}

func theHookPayloadContains(ctx context.Context, negation, text string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	got, err := hookContext(s)
	if err != nil {
		return err
	}
	if want := negation == ""; strings.Contains(got, expandSandboxVars(s, text)) != want {
		return fmt.Errorf("hook context contains %q: %v, want %v; context:\n%s", text, !want, want, got)
	}
	return nil
}

// expandSandboxVars replaces $STORE_DIR with the scenario's store directory.
func expandSandboxVars(s *testState, text string) string {
	return strings.ReplaceAll(text, "$STORE_DIR", scenarioStoreDir(s))
}

// repoRoot is the module root, two levels above this package (go test
// runs a package's tests from its own directory).
func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(wd, "..", "..")), nil
}

// theHookPayloadMatchesTheNoticeFixture requires the hook's context to hold
// the RenderContext golden fixture of the fallbacknotice package verbatim:
// the lead sentence and the one notice, with every field in place.
func theHookPayloadMatchesTheNoticeFixture(ctx context.Context, name string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	want, err := os.ReadFile(filepath.Join(root, "internal", "fallbacknotice", "testdata", name))
	if err != nil {
		return err
	}
	got, err := hookContext(s)
	if err != nil {
		return err
	}
	if !strings.Contains(got, strings.TrimRight(string(want), "\n")) {
		return fmt.Errorf("hook context lacks fixture %s:\n--- want ---\n%s\n--- got ---\n%s", name, want, got)
	}
	return nil
}

// theVaultGoldenFixtureRecordsError reads a golden fixture Phase 1 recorded
// before the fallback existed, pinning what the same stub output did then.
func theVaultGoldenFixtureRecordsError(ctx context.Context, name, text string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "internal", "vault", "resolve", "testdata", "golden", name+".golden"))
	if err != nil {
		return err
	}
	// The fixture's error section runs from the "error:" line to the
	// "key report:" line.
	body := string(data)
	start := strings.Index(body, "error:")
	end := strings.Index(body, "key report:")
	if start < 0 || end < start {
		return fmt.Errorf("fixture %s has no error section:\n%s", name, body)
	}
	if section := body[start:end]; !strings.Contains(section, text) {
		return fmt.Errorf("fixture %s records %q, want it to contain %q", name, section, text)
	}
	return nil
}

// sandboxSnapshot records each regular file's size and modification time,
// so a later step can tell which files a run wrote.
type sandboxSnapshot map[string]fileStamp

type fileStamp struct {
	size int64
	mod  time.Time
}

func snapshotSandbox(root string) (sandboxSnapshot, error) {
	snap := sandboxSnapshot{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A directory the run left unreadable is not a file it wrote.
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		snap[path] = fileStamp{size: info.Size(), mod: info.ModTime()}
		return nil
	})
	return snap, err
}

func iRecordTheSandboxFiles(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	snap, err := snapshotSandbox(s.sandbox)
	if err != nil {
		return err
	}
	s.sandboxFiles = snap
	return nil
}

// theLastRunLeakedNothing checks the last run's stdout, stderr and every
// file it wrote under the sandbox (new or changed since the snapshot). The
// markers may appear only in files under the store directory and the files
// niwa materializes into the named instance, which excludes the instance's
// own .niwa state directory; the probe token may appear nowhere. The snapshot is then
// refreshed, so the next run is judged on its own writes.
func theLastRunLeakedNothing(ctx context.Context, markerList, instance, token string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	before := s.sandboxFiles
	if before == nil {
		return fmt.Errorf("no sandbox snapshot; record the sandbox files first")
	}
	markers := strings.Split(markerList, ",")
	sep := string(os.PathSeparator)
	instanceDir := filepath.Join(s.workspaceRoot, instance) + sep
	instanceState := filepath.Join(s.workspaceRoot, instance, ".niwa") + sep
	allowedPath := func(path string) bool {
		if strings.HasPrefix(path, scenarioStoreDir(s)+sep) {
			return true
		}
		return strings.HasPrefix(path, instanceDir) && !strings.HasPrefix(path, instanceState)
	}
	var problems []string
	for name, stream := range map[string]string{"stdout": s.stdout, "stderr": s.stderr} {
		for _, m := range append(markers, token) {
			if strings.Contains(stream, m) {
				problems = append(problems, fmt.Sprintf("%s contains %q", name, m))
			}
		}
	}
	after, err := snapshotSandbox(s.sandbox)
	if err != nil {
		return err
	}
	written := 0
	for path, st := range after {
		if prev, seen := before[path]; seen && prev.size == st.size && prev.mod.Equal(st.mod) {
			continue
		}
		written++
		data, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s could not be read to check it: %v", path, err))
			continue
		}
		if strings.Contains(string(data), token) {
			problems = append(problems, fmt.Sprintf("%s contains the probe token", path))
		}
		if allowedPath(path) {
			continue
		}
		for _, m := range markers {
			if strings.Contains(string(data), m) {
				problems = append(problems, fmt.Sprintf("%s contains %q", path, m))
			}
		}
	}
	s.sandboxFiles = after
	if len(problems) > 0 {
		return fmt.Errorf("the run leaked secret material (%d files written):\n%s", written, strings.Join(problems, "\n"))
	}
	return nil
}

func registerVaultFallbackSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the secret store holds "([^"]*)" = "([^"]*)" for infisical project "([^"]*)" env "([^"]*)" at path "([^"]*)" resolved (\d+) (minutes?|hours?|days?) ago$`, theSecretStoreHolds)
	ctx.Step(`^the secret store (no longer )?holds "([^"]*)" for infisical project "([^"]*)" env "([^"]*)" at path "([^"]*)"$`, theSecretStoreHoldsKey)
	ctx.Step(`^the secret store is inside a git work tree$`, theSecretStoreIsInsideAGitWorkTree)
	ctx.Step(`^the secret store directory is unwritable$`, theSecretStoreIsUnwritable)
	ctx.Step(`^a provider-auth file for infisical project "([^"]*)" with client_secret "([^"]*)"$`, aProviderAuthFile)
	ctx.Step(`^the command finished within (\d+) seconds$`, theCommandFinishedWithin)
	ctx.Step(`^the error output contains "([^"]*)" (\d+) times?$`, theErrorOutputContainsTimes)
	ctx.Step(`^the infisical stub records the PID of any child it forks$`, theInfisicalStubRecordsForkedChildren)
	ctx.Step(`^the infisical stub's forked child is gone$`, theStubsForkedChildIsGone)
	ctx.Step(`^the file "([^"]*)" in the dispatch instance contains "([^"]*)"$`, theFileInTheDispatchInstanceContains)
	ctx.Step("^the hook payload's context (does not )?contains? \"([^\"]*)\"$", theHookPayloadContains)
	ctx.Step("^the hook payload's context holds the notice fixture \"([^\"]*)\"$", theHookPayloadMatchesTheNoticeFixture)
	ctx.Step(`^the vault golden fixture "([^"]*)" records the error "([^"]*)"$`, theVaultGoldenFixtureRecordsError)
	ctx.Step(`^I record the sandbox files$`, iRecordTheSandboxFiles)
	ctx.Step(`^the last run leaked none of "([^"]*)" outside the store and instance "([^"]*)", nor the probe token "([^"]*)"$`, theLastRunLeakedNothing)
}
