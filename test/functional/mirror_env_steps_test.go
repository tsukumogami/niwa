package functional

// Steps for features/mirror-network-env.feature: which of niwa's environment
// the mirror's git processes are given. The test build's environment probe
// (NIWA_TEST_GIT_ENV_PROBE) appends one JSON line per mirror git process
// with its kind (network or local), the call it makes, and every GIT_* entry
// of its environment, so a scenario sees exactly what reached git. The probe
// file, the ssh wrapper and its call record all live in the sandbox home.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/cucumber/godog"
)

// mirrorEnvScenario is what the environment steps remember between steps.
type mirrorEnvScenario struct {
	probe      string // the probe file
	probeLines int    // the probe's line count at the last count
	wrapperLog string // the ssh wrapper's call record
	wrapperAt  int    // its line count at the last count
}

type mirrorEnvKey struct{}

// mirrorEnvState returns the scenario's environment state, allocating it on
// the returned context on first use.
func mirrorEnvState(ctx context.Context) (context.Context, *testState, *mirrorEnvScenario, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, nil, nil, fmt.Errorf("no test state")
	}
	if e, ok := ctx.Value(mirrorEnvKey{}).(*mirrorEnvScenario); ok {
		return ctx, s, e, nil
	}
	e := &mirrorEnvScenario{}
	return context.WithValue(ctx, mirrorEnvKey{}, e), s, e, nil
}

// remoteAccessVars are the variables the mirror's network calls keep, as the
// requirements list them, besides GIT_CONFIG_COUNT and its entries.
var remoteAccessVars = []string{
	"GIT_SSH", "GIT_SSH_COMMAND", "GIT_SSH_VARIANT", "GIT_ASKPASS", "GIT_PROXY_COMMAND",
	"GIT_SSL_CAINFO", "GIT_SSL_CAPATH", "GIT_SSL_NO_VERIFY", "GIT_HTTP_PROXY_AUTHMETHOD",
}

// niwaOwnGitEnv is every GIT_* variable niwa itself sets on a mirror git
// process, with the one value it sets, on which kind of call.
var niwaOwnGitEnv = map[string]struct {
	value          string
	network, local bool
}{
	"GIT_TERMINAL_PROMPT": {"0", true, true},
	"GIT_REFLOG_ACTION":   {"niwa mirror refresh", true, false},
	"GIT_CONFIG_NOSYSTEM": {"1", false, true},
	"GIT_CONFIG_GLOBAL":   {"/dev/null", false, true},
	"GIT_OPTIONAL_LOCKS":  {"0", false, true},
}

var configEntryName = regexp.MustCompile(`^GIT_CONFIG_(KEY|VALUE)_[0-9]+$`)

func registerMirrorEnvSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the mirror's git environment probe is on$`, theProbeIsOn)
	ctx.Step(`^every remote-access variable is set in niwa's environment$`, everyRemoteAccessVariableIsSet)
	ctx.Step(`^I count the probe lines$`, iCountTheProbeLines)
	ctx.Step(`^the probe gained a (network|local) "([^"]*)" line$`, theProbeGainedALine)
	ctx.Step(`^every network line the probe holds carries each remote-access variable niwa was given, with its value$`, everyNetworkLineCarriesTheVariables)
	ctx.Step(`^no line the probe holds carries a GIT variable niwa didn't set itself or keep for the network$`, noLineCarriesAnInheritedVariable)
	ctx.Step(`^every network line the probe holds keeps only the config entry "([^"]*)" = "([^"]*)"$`, everyNetworkLineKeepsOnlyTheEntry)
	ctx.Step(`^every network line the probe holds carries "([^"]*)" = "([^"]*)"$`, everyNetworkLineCarries)
	ctx.Step(`^no line the probe holds names "([^"]*)"$`, noLineNames)
	ctx.Step(`^no local line the probe gained carries "([^"]*)"$`, noLocalLineCarries)
	ctx.Step(`^source repo "([^"]*)" is reachable only through an ssh wrapper as "([^"]*)"$`, theSourceRepoIsReachableOnlyThroughAWrapper)
	ctx.Step(`^I count the ssh wrapper calls$`, iCountTheWrapperCalls)
	ctx.Step(`^the ssh wrapper was called for source repo "([^"]*)" since the count$`, theWrapperWasCalled)
	ctx.Step(`^GIT_DIR and GIT_ALTERNATE_OBJECT_DIRECTORIES name an unrelated repo in niwa's environment$`, gitDirNamesAnUnrelatedRepo)
	ctx.Step(`^the mirror log holds none of "([^"]*)"$`, theMirrorLogHoldsNoneOf)
	ctx.Step(`^every restore and refresh line the mirror log gained carries the network notes "([^"]*)"$`, everyRestoreLineCarriesTheNotes)
}

func theProbeIsOn(ctx context.Context) (context.Context, error) {
	ctx, s, e, err := mirrorEnvState(ctx)
	if err != nil {
		return ctx, err
	}
	e.probe = filepath.Join(s.homeDir, "git-env-probe.jsonl")
	s.envOverrides["NIWA_TEST_GIT_ENV_PROBE"] = e.probe
	return ctx, nil
}

// everyRemoteAccessVariableIsSet gives niwa a value for each variable the
// network calls keep, and one config entry that carries remote access. None
// of them changes what git does with the scenario's file:// remotes.
func everyRemoteAccessVariableIsSet(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	missing := filepath.Join(s.homeDir, "no-such")
	for name, value := range map[string]string{
		"GIT_SSH":                   missing + "-ssh",
		"GIT_SSH_COMMAND":           missing + "-ssh-command",
		"GIT_SSH_VARIANT":           "ssh",
		"GIT_ASKPASS":               missing + "-askpass",
		"GIT_PROXY_COMMAND":         missing + "-proxy-command",
		"GIT_SSL_CAINFO":            missing + "-ca.pem",
		"GIT_SSL_CAPATH":            missing + "-ca-dir",
		"GIT_SSL_NO_VERIFY":         "1",
		"GIT_HTTP_PROXY_AUTHMETHOD": "basic",
		"GIT_CONFIG_COUNT":          "1",
		"GIT_CONFIG_KEY_0":          "http.extraHeader",
		"GIT_CONFIG_VALUE_0":        "X-Niwa-Probe: kept",
	} {
		s.envOverrides[name] = value
	}
	return ctx, nil
}

// probeLine is one line of the probe file.
type probeLine struct {
	Kind string            `json:"kind"`
	Call string            `json:"call"`
	Env  map[string]string `json:"env"`
	raw  string
}

// readProbe returns every line of the probe file.
func readProbe(e *mirrorEnvScenario) ([]probeLine, error) {
	if e.probe == "" {
		return nil, fmt.Errorf("the probe isn't on")
	}
	data, err := os.ReadFile(e.probe)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lines []probeLine
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var l probeLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("probe line %q is not one JSON object: %w", sc.Text(), err)
		}
		l.raw = sc.Text()
		lines = append(lines, l)
	}
	return lines, sc.Err()
}

// probeLines returns the probe's lines, every one when gained is false and
// those since the last count when it's true, and fails when there are none.
func probeLines(ctx context.Context, gained bool) (*testState, []probeLine, error) {
	_, s, e, err := mirrorEnvState(ctx)
	if err != nil {
		return nil, nil, err
	}
	lines, err := readProbe(e)
	if err != nil {
		return nil, nil, err
	}
	if gained {
		if len(lines) < e.probeLines {
			return nil, nil, fmt.Errorf("the probe shrank from %d to %d lines", e.probeLines, len(lines))
		}
		lines = lines[e.probeLines:]
	}
	if len(lines) == 0 {
		return nil, nil, fmt.Errorf("the probe recorded no git process")
	}
	return s, lines, nil
}

func iCountTheProbeLines(ctx context.Context) (context.Context, error) {
	ctx, _, e, err := mirrorEnvState(ctx)
	if err != nil {
		return ctx, err
	}
	lines, err := readProbe(e)
	if err != nil {
		return ctx, err
	}
	e.probeLines = len(lines)
	return ctx, nil
}

func theProbeGainedALine(ctx context.Context, kind, call string) error {
	_, lines, err := probeLines(ctx, true)
	if err != nil {
		return err
	}
	var seen []string
	for _, l := range lines {
		if l.Kind == kind && l.Call == call {
			return nil
		}
		seen = append(seen, l.Kind+" "+l.Call)
	}
	return fmt.Errorf("the probe gained no %s %q line; it gained %q", kind, call, seen)
}

// networkLines returns the network lines among lines, failing when none is.
func networkLines(lines []probeLine) ([]probeLine, error) {
	var out []probeLine
	for _, l := range lines {
		if l.Kind == "network" {
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the probe recorded no network git process")
	}
	return out, nil
}

func everyNetworkLineCarriesTheVariables(ctx context.Context) error {
	s, lines, err := probeLines(ctx, false)
	if err != nil {
		return err
	}
	network, err := networkLines(lines)
	if err != nil {
		return err
	}
	var want []string
	for name, value := range s.envOverrides {
		if slices.Contains(remoteAccessVars, name) || name == "GIT_CONFIG_COUNT" || configEntryName.MatchString(name) {
			want = append(want, name+"="+value)
		}
	}
	if len(want) == 0 {
		return fmt.Errorf("the scenario set no remote-access variable")
	}
	sort.Strings(want)
	for _, l := range network {
		for _, kv := range want {
			name, value, _ := strings.Cut(kv, "=")
			if got, ok := l.Env[name]; !ok || got != value {
				return fmt.Errorf("network line %s lacks %s=%q", l.raw, name, value)
			}
		}
	}
	return nil
}

func noLineCarriesAnInheritedVariable(ctx context.Context) error {
	_, lines, err := probeLines(ctx, false)
	if err != nil {
		return err
	}
	for _, l := range lines {
		network := l.Kind == "network"
		for name, value := range l.Env {
			if own, ok := niwaOwnGitEnv[name]; ok && ((network && own.network) || (!network && own.local)) {
				if value != own.value {
					return fmt.Errorf("%s line %s carries %s=%q, not niwa's own %q", l.Kind, l.raw, name, value, own.value)
				}
				continue
			}
			if network && (slices.Contains(remoteAccessVars, name) || name == "GIT_CONFIG_COUNT" || configEntryName.MatchString(name)) {
				continue
			}
			return fmt.Errorf("%s line %s carries %s, which niwa neither set nor keeps for the network", l.Kind, l.raw, name)
		}
	}
	return nil
}

func everyNetworkLineKeepsOnlyTheEntry(ctx context.Context, key, value string) error {
	_, lines, err := probeLines(ctx, false)
	if err != nil {
		return err
	}
	network, err := networkLines(lines)
	if err != nil {
		return err
	}
	for _, l := range network {
		if l.Env["GIT_CONFIG_COUNT"] != "1" || l.Env["GIT_CONFIG_KEY_0"] != key || l.Env["GIT_CONFIG_VALUE_0"] != value {
			return fmt.Errorf("network line %s doesn't keep exactly the entry %s = %q at index 0", l.raw, key, value)
		}
		for name := range l.Env {
			if configEntryName.MatchString(name) && !strings.HasSuffix(name, "_0") {
				return fmt.Errorf("network line %s carries %s", l.raw, name)
			}
		}
	}
	return nil
}

func everyNetworkLineCarries(ctx context.Context, name, value string) error {
	_, lines, err := probeLines(ctx, false)
	if err != nil {
		return err
	}
	network, err := networkLines(lines)
	if err != nil {
		return err
	}
	for _, l := range network {
		if got, ok := l.Env[name]; !ok || got != value {
			return fmt.Errorf("network line %s carries %s=%q, want %q", l.raw, name, got, value)
		}
	}
	return nil
}

func noLineNames(ctx context.Context, text string) error {
	_, lines, err := probeLines(ctx, false)
	if err != nil {
		return err
	}
	for _, l := range lines {
		if strings.Contains(strings.ToLower(l.raw), strings.ToLower(text)) {
			return fmt.Errorf("probe line %s names %q", l.raw, text)
		}
	}
	return nil
}

func noLocalLineCarries(ctx context.Context, name string) error {
	_, lines, err := probeLines(ctx, true)
	if err != nil {
		return err
	}
	locals := 0
	for _, l := range lines {
		if l.Kind != "local" {
			continue
		}
		locals++
		if v, ok := l.Env[name]; ok {
			return fmt.Errorf("local line %s carries %s=%q", l.raw, name, v)
		}
	}
	if locals == 0 {
		return fmt.Errorf("the probe gained no local line")
	}
	return nil
}

// sshOnlyHost is a host under the reserved .invalid domain: no ssh but the
// wrapper reaches it.
const sshOnlyHost = "niwa-mirror-test.invalid"

// theSourceRepoIsReachableOnlyThroughAWrapper writes a stand-in for ssh into
// the sandbox home and points GIT_SSH_COMMAND at it. git runs it as
// "<wrapper> <host> <remote command>"; it records the call and runs the
// remote command (git-upload-pack '<path>') locally, so alias, the source
// repo's path on sshOnlyHost, resolves through nothing else.
func theSourceRepoIsReachableOnlyThroughAWrapper(ctx context.Context, source, alias string) (context.Context, error) {
	ctx, s, e, err := mirrorEnvState(ctx)
	if err != nil {
		return ctx, err
	}
	url, ok := s.repoURLs[source]
	if !ok || !strings.HasPrefix(url, "file://") {
		return ctx, fmt.Errorf("source repo %q has no file:// URL (%q)", source, url)
	}
	dir := filepath.Join(s.homeDir, "ssh-wrapper")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return ctx, err
	}
	e.wrapperLog = filepath.Join(dir, "calls")
	script := filepath.Join(dir, "ssh")
	body := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> '" + e.wrapperLog + "'\n" +
		"for last; do :; done\n" +
		"case \"$last\" in\n" +
		"git-upload-pack\\ *|git-receive-pack\\ *) exec sh -c \"git ${last#git-}\" ;;\n" +
		"esac\n" +
		"exit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		return ctx, err
	}
	s.envOverrides["GIT_SSH_COMMAND"] = "'" + script + "'"
	s.envOverrides["GIT_SSH_VARIANT"] = "simple"
	s.repoURLs[alias] = sshOnlyHost + ":" + strings.TrimPrefix(url, "file://")
	return ctx, nil
}

func wrapperCalls(e *mirrorEnvScenario) ([]string, error) {
	if e.wrapperLog == "" {
		return nil, fmt.Errorf("no ssh wrapper is set up")
	}
	data, err := os.ReadFile(e.wrapperLog)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

func iCountTheWrapperCalls(ctx context.Context) (context.Context, error) {
	ctx, _, e, err := mirrorEnvState(ctx)
	if err != nil {
		return ctx, err
	}
	calls, err := wrapperCalls(e)
	if err != nil {
		return ctx, err
	}
	e.wrapperAt = len(calls)
	return ctx, nil
}

func theWrapperWasCalled(ctx context.Context, source string) (context.Context, error) {
	ctx, s, e, err := mirrorEnvState(ctx)
	if err != nil {
		return ctx, err
	}
	calls, err := wrapperCalls(e)
	if err != nil {
		return ctx, err
	}
	path := strings.TrimPrefix(s.repoURLs[source], "file://")
	for _, c := range calls[min(e.wrapperAt, len(calls)):] {
		if strings.Contains(c, sshOnlyHost) && strings.Contains(c, path) {
			return ctx, nil
		}
	}
	return ctx, fmt.Errorf("the ssh wrapper was not called for %s since the count; its calls: %q", path, calls)
}

// gitDirNamesAnUnrelatedRepo points GIT_DIR at a bare repo in the sandbox
// that has nothing to do with the mirror, and
// GIT_ALTERNATE_OBJECT_DIRECTORIES at its objects.
func gitDirNamesAnUnrelatedRepo(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.homeDir, "unrelated.git")
	if out, err := fixtureGit(s.homeDir, "init", "--quiet", "--bare", dir); err != nil {
		return ctx, fmt.Errorf("git init --bare %s: %w\n%s", dir, err, out)
	}
	s.envOverrides["GIT_DIR"] = dir
	s.envOverrides["GIT_ALTERNATE_OBJECT_DIRECTORIES"] = filepath.Join(dir, "objects")
	return ctx, nil
}

func theMirrorLogHoldsNoneOf(ctx context.Context, list string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, err := mirrorLogPath(s)
	if err != nil {
		return err
	}
	var text []byte
	for _, p := range []string{path, path + ".1"} {
		data, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		text = append(text, data...)
	}
	if len(text) == 0 {
		return fmt.Errorf("the mirror log is empty")
	}
	for _, sentinel := range strings.Split(list, ",") {
		if bytes.Contains(text, []byte(sentinel)) {
			return fmt.Errorf("the mirror log carries %q:\n%s", sentinel, text)
		}
	}
	return nil
}

func everyRestoreLineCarriesTheNotes(ctx context.Context, notes string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	want := strings.Split(notes, ",")
	seen := 0
	for _, line := range lines {
		event := line["event"]
		raw, has := line["network_notes"]
		if event != "restore" && event != "refresh" {
			if has {
				return fmt.Errorf("%s line %v carries network_notes", event, line)
			}
			continue
		}
		seen++
		var got []string
		if list, ok := raw.([]any); ok {
			for _, n := range list {
				if str, ok := n.(string); ok {
					got = append(got, str)
				}
			}
		}
		if !slices.Equal(got, want) {
			return fmt.Errorf("%s line %v carries network_notes %q, want %q", event, line, got, want)
		}
	}
	if seen == 0 {
		return fmt.Errorf("the mirror log gained no restore or refresh line: %v", lines)
	}
	return nil
}
