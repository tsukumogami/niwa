package functional

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/cucumber/godog"
)

// The dispatch-authority scenarios check what niwa dispatch resolves, refuses
// and records about a dispatch's authority level and named grant: the record
// at <instance>/.niwa/authority.json, the one stderr line, and that a refusal
// leaves nothing behind. The fake claude records its launch argv at
// $HOME/dispatch-launch-argv, so a missing file means nothing launched.

// authorityScenario is what these steps remember between steps.
type authorityScenario struct {
	rootEntries    []string // the workspace root's entries before the dispatch under test
	mappings       []string // the session mappings before it
	recordPath     string   // a remembered instance's record path
	record         []byte   // and its bytes
	instancePath   string   // the remembered instance
	firstInstance  string   // an earlier dispatch's instance, kept for comparison
}

// authorityFixtures are the workspace configs the scenarios dispatch under,
// by name. Each is the body of a config repo's workspace.toml.
var authorityFixtures = map[string]string{
	"none": "",
	"strict": `[authority]
level = "strict"
`,
	"narrow-ceiling": `[authority]
ceiling = ["merge-pr"]

[authority.roles.coordinator]
authorities = ["merge-pr", "close-issue", "cut-release"]
`,
	"r8a": `[authority]
level = "supervised"
ceiling = ["merge-pr", "close-issue", "cut-release"]

[authority.roles.coordinator]
authorities = ["merge-pr", "close-issue", "cut-release"]
`,
	"r8a-reviewer": `[authority]
level = "supervised"
ceiling = ["merge-pr", "close-issue", "cut-release"]

[authority.roles.coordinator]
authorities = ["merge-pr", "close-issue", "cut-release"]

[authority.roles.reviewer]
authorities = ["merge-pr"]
`,
	"invalid-level": `[authority]
level = "bogus"
`,
	"many-roles": manyRolesFixture(),
}

const manyRoleCount = 300

func manyRoleNames() []string {
	out := make([]string, manyRoleCount)
	for i := range out {
		out[i] = fmt.Sprintf("role-%s-%05d", strings.Repeat("x", 52), i)
	}
	return out
}

func manyRolesFixture() string {
	var b strings.Builder
	b.WriteString("[authority]\nceiling = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n")
	for _, name := range manyRoleNames() {
		b.WriteString("\n[authority.roles." + name + "]\nauthorities = [\"merge-pr\", \"close-issue\", \"cut-release\"]\n")
	}
	return b.String()
}

func registerDispatchAuthoritySteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a config repo "([^"]*)" exists with authority fixture "([^"]*)"$`, aConfigRepoWithAuthorityFixture)
	ctx.Step(`^the niwa machine config global table holds '([^']*)'$`, theMachineConfigGlobalTableHolds)
	ctx.Step(`^I remember the workspace before the next dispatch$`, iRememberTheWorkspaceBeforeTheNextDispatch)
	ctx.Step(`^I dispatch with "([^"]*)" from the workspace root under a pty$`, iDispatchWithFlagsUnderAPTY)
	ctx.Step(`^I dispatch with "([^"]*)" from inside the dispatch instance$`, iDispatchFromInsideTheDispatchInstance)
	ctx.Step(`^I dispatch with "([^"]*)" from inside the first dispatch instance$`, iDispatchFromInsideTheFirstDispatchInstance)
	ctx.Step(`^the dispatch was refused at authority resolution with '([^']*)'$`, theDispatchWasRefusedAtAuthorityResolution)
	ctx.Step(`^the dispatch was refused after provisioning with '([^']*)'$`, theDispatchWasRefusedAfterProvisioning)
	ctx.Step(`^the dispatch instance's authority record is:$`, theDispatchInstancesAuthorityRecordIs)
	ctx.Step(`^the dispatch instance's authority record holds '([^']*)'$`, theDispatchInstancesAuthorityRecordHolds)
	ctx.Step(`^the dispatch instance's authority record holds:$`, func(ctx context.Context, doc *godog.DocString) error {
		return theDispatchInstancesAuthorityRecordHolds(ctx, doc.Content)
	})
	ctx.Step(`^I dispatch with "([^"]*)" from the workspace root$`, iDispatchFromTheWorkspaceRoot)
	ctx.Step(`^the dispatch session for the first dispatch is deleted from the Agent View$`, theFirstDispatchsSessionIsDeleted)
	ctx.Step(`^the dispatch instance's authority record is private to its owner$`, theDispatchInstancesAuthorityRecordIsPrivate)
	ctx.Step(`^the dispatch instance's authority record names the first dispatch as its parent from "([^"]*)"$`, theRecordNamesTheFirstDispatchAsParent)
	ctx.Step(`^I keep the dispatch instance as the first dispatch$`, iKeepTheDispatchInstanceAsTheFirst)
	ctx.Step(`^I remember the dispatch instance's authority record$`, iRememberTheDispatchInstancesAuthorityRecord)
	ctx.Step(`^the remembered authority record is byte-for-byte unchanged$`, theRememberedAuthorityRecordIsUnchanged)
	ctx.Step(`^the remembered instance has no authority record$`, theRememberedInstanceHasNoAuthorityRecord)
	ctx.Step(`^I run "([^"]*)" against the remembered instance$`, iRunAgainstTheRememberedInstance)
	ctx.Step(`^the dispatch instance's authority record is replaced with '([^']*)'$`, theDispatchInstancesAuthorityRecordIsReplacedWith)
	ctx.Step(`^the workspace root's config becomes "([^"]*)"$`, theWorkspaceRootsConfigBecomes)
	ctx.Step(`^the first dispatch instance no longer exists$`, theFirstDispatchInstanceNoLongerExists)
	ctx.Step(`^the local clone of overlay "([^"]*)" is removed$`, theLocalCloneOfOverlayIsRemoved)
	ctx.Step(`^the worker's resource attributes hold "([^"]*)"$`, theWorkersResourceAttributesHold)
	ctx.Step(`^the worker's launch carried no authority attributes$`, theWorkersLaunchCarriedNoAuthorityAttributes)
	ctx.Step(`^the files niwa wrote into the first and the last dispatch instance match$`, theInstanceFilesMatch)
}

func aConfigRepoWithAuthorityFixture(ctx context.Context, name, fixture string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	body, ok := authorityFixtures[fixture]
	if !ok {
		return ctx, fmt.Errorf("no authority fixture %q", fixture)
	}
	url, err := s.gitServer.ConfigRepo(name, "[workspace]\nname = \""+name+"\"\n\n"+body)
	if err != nil {
		return ctx, fmt.Errorf("creating config repo %q: %w", name, err)
	}
	s.repoURLs[name] = url
	return ctx, nil
}

func theMachineConfigGlobalTableHolds(ctx context.Context, line string) error {
	if line == "-" {
		return nil
	}
	return theNiwaMachineConfigGlobalTableContains(ctx, &godog.DocString{Content: line})
}

// sessionMappings lists the workspace's session mapping files.
func sessionMappings(s *testState) []string {
	entries, _ := os.ReadDir(filepath.Join(s.workspaceRoot, ".niwa", "sessions"))
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func rootEntries(s *testState) []string {
	entries, _ := os.ReadDir(s.workspaceRoot)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func launchArgvPath(s *testState) string { return filepath.Join(s.homeDir, "dispatch-launch-argv") }

// iRememberTheWorkspaceBeforeTheNextDispatch records the root's entries and
// the session mappings, and clears the fake claude's launch record, so the
// refusal assertions can tell what the next dispatch did.
func iRememberTheWorkspaceBeforeTheNextDispatch(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	s.authority.rootEntries = rootEntries(s)
	s.authority.mappings = sessionMappings(s)
	if err := os.Remove(launchArgvPath(s)); err != nil && !os.IsNotExist(err) {
		return ctx, err
	}
	return ctx, nil
}

// expandAuthorityFlags fills the placeholders a flag string can carry.
func expandAuthorityFlags(s *testState, flags string) string {
	flags = strings.ReplaceAll(flags, "{all-roles}", strings.Join(manyRoleNames(), ","))
	flags = strings.ReplaceAll(flags, "{workspace-root}", s.workspaceRoot)
	flags = strings.ReplaceAll(flags, "{dispatch-instance}", s.lastDispatchInstancePath)
	flags = strings.ReplaceAll(flags, "{first-instance}", s.authority.firstInstance)
	return flags
}

// iDispatchWithFlagsUnderAPTY runs `niwa dispatch --detach <flags>` with no
// prompt argument under a terminal that would type a task, so a dispatch that
// got as far as the prompt would read it and launch.
func iDispatchWithFlagsUnderAPTY(ctx context.Context, flags string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	command := "niwa dispatch --detach " + expandAuthorityFlags(s, flags)
	ctx, err := iRunUnderPTYWithInput(ctx, command, `do a thing\r`)
	if err != nil {
		return ctx, err
	}
	recordDispatchInstance(s, command)
	return ctx, nil
}

func iDispatchFromInsideTheDispatchInstance(ctx context.Context, flags string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, dispatchFrom(s, s.lastDispatchInstancePath, flags)
}

func iDispatchFromInsideTheFirstDispatchInstance(ctx context.Context, flags string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, dispatchFrom(s, s.authority.firstInstance, flags)
}

func iDispatchFromTheWorkspaceRoot(ctx context.Context, flags string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, dispatchFrom(s, s.workspaceRoot, flags)
}

var mappingSession = regexp.MustCompile(`"session_id":\s*"([^"]+)"`)

// theFirstDispatchsSessionIsDeleted finds the session the first dispatch's
// mapping names and removes its job entry, as deleting it from the Agent
// View does, so the next sweep reclaims that instance.
func theFirstDispatchsSessionIsDeleted(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, ".niwa", "sessions")
	for _, name := range sessionMappings(s) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !strings.Contains(string(data), s.authority.firstInstance) {
			continue
		}
		if m := mappingSession.FindSubmatch(data); m != nil {
			return theDispatchSessionIsDeleted(ctx, string(m[1]))
		}
	}
	return ctx, fmt.Errorf("no session mapping names %s", s.authority.firstInstance)
}

func dispatchFrom(s *testState, dir, flags string) error {
	if dir == "" {
		return fmt.Errorf("no dispatch instance to run from")
	}
	command := "niwa dispatch hello-task --detach " + expandAuthorityFlags(s, flags)
	if err := runNiwa(s, dir, command); err != nil {
		return err
	}
	recordDispatchInstance(s, command)
	return nil
}

const authorityLinePrefix = "niwa dispatch: authority level"

// theDispatchWasRefusedAtAuthorityResolution is R16 in full: exit 1 with the
// refusal, no resolution line, no prompt, the workspace root exactly as it
// was (no new instance, and the sweep removed none), no new mapping, no
// pending marker or record anywhere new, and nothing launched.
func theDispatchWasRefusedAtAuthorityResolution(ctx context.Context, message string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	out := s.stdout + s.stderr
	if s.exitCode != 1 {
		return fmt.Errorf("exit code %d, want 1\n%s", s.exitCode, out)
	}
	if !strings.Contains(out, expandAuthorityFlags(s, message)) {
		return fmt.Errorf("output lacks %q:\n%s", message, out)
	}
	if strings.Contains(out, authorityLinePrefix) {
		return fmt.Errorf("a refused dispatch printed its resolution line:\n%s", out)
	}
	if strings.Contains(out, "Paste or type the task") {
		return fmt.Errorf("the refused dispatch asked for a prompt:\n%s", out)
	}
	if got := rootEntries(s); strings.Join(got, "\n") != strings.Join(s.authority.rootEntries, "\n") {
		return fmt.Errorf("the workspace root changed:\nbefore %v\nafter  %v", s.authority.rootEntries, got)
	}
	if got := sessionMappings(s); strings.Join(got, "\n") != strings.Join(s.authority.mappings, "\n") {
		return fmt.Errorf("session mappings changed:\nbefore %v\nafter  %v", s.authority.mappings, got)
	}
	for _, name := range rootEntries(s) {
		if _, err := os.Stat(filepath.Join(s.workspaceRoot, name, ".niwa", "dispatch-pending")); err == nil {
			return fmt.Errorf("a pending marker exists in %s", name)
		}
	}
	if _, err := os.Stat(launchArgvPath(s)); err == nil {
		return fmt.Errorf("the fake claude was launched")
	}
	return nil
}

// theDispatchWasRefusedAfterProvisioning is the (8a) refusal: the line has
// printed, then the dispatch exits 1 naming the change, and the instance,
// the record, the mapping and the launch are all absent.
func theDispatchWasRefusedAfterProvisioning(ctx context.Context, message string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	out := s.stdout + s.stderr
	if s.exitCode != 1 {
		return fmt.Errorf("exit code %d, want 1\n%s", s.exitCode, out)
	}
	if !strings.Contains(out, authorityLinePrefix) {
		return fmt.Errorf("the resolution line did not print before the late refusal:\n%s", out)
	}
	if !strings.Contains(out, message) {
		return fmt.Errorf("output lacks %q:\n%s", message, out)
	}
	if inst := findDispatchInstance(s.workspaceRoot); inst != "" {
		return fmt.Errorf("the dispatch instance %s survived the refusal", inst)
	}
	if m := sessionMappings(s); len(m) != 0 {
		return fmt.Errorf("session mappings exist: %v", m)
	}
	if _, err := os.Stat(launchArgvPath(s)); err == nil {
		return fmt.Errorf("the fake claude was launched")
	}
	return nil
}

func lastRecord(s *testState) ([]byte, error) {
	if s.lastDispatchInstancePath == "" {
		return nil, fmt.Errorf("no dispatch instance recorded\n%s%s", s.stdout, s.stderr)
	}
	return os.ReadFile(filepath.Join(s.lastDispatchInstancePath, ".niwa", "authority.json"))
}

var recordDispatchIDs = regexp.MustCompile(`"(parent_)?dispatch_id": "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}"`)

// theDispatchInstancesAuthorityRecordIs compares the whole stored record,
// with every dispatch id written as <dispatch-id>.
func theDispatchInstancesAuthorityRecordIs(ctx context.Context, want *godog.DocString) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	data, err := lastRecord(s)
	if err != nil {
		return err
	}
	got := recordDispatchIDs.ReplaceAllString(string(data), `"${1}dispatch_id": "<dispatch-id>"`)
	if strings.TrimSpace(got) != strings.TrimSpace(want.Content) {
		return fmt.Errorf("authority record =\n%s\nwant\n%s", got, want.Content)
	}
	return nil
}

func theDispatchInstancesAuthorityRecordHolds(ctx context.Context, fragment string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	data, err := lastRecord(s)
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), fragment) {
		return fmt.Errorf("authority record lacks %q:\n%s", fragment, data)
	}
	return nil
}

func theDispatchInstancesAuthorityRecordIsPrivate(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	info, err := os.Stat(filepath.Join(s.lastDispatchInstancePath, ".niwa", "authority.json"))
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("authority record mode %v, want 0600", info.Mode().Perm())
	}
	return nil
}

var dispatchIDValue = regexp.MustCompile(`"dispatch_id": "([0-9a-f-]{36})"`)

func iKeepTheDispatchInstanceAsTheFirst(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return ctx, fmt.Errorf("no dispatch instance recorded")
	}
	s.authority.firstInstance = s.lastDispatchInstancePath
	return ctx, nil
}

func theRecordNamesTheFirstDispatchAsParent(ctx context.Context, source string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	first, err := os.ReadFile(filepath.Join(s.authority.firstInstance, ".niwa", "authority.json"))
	if err != nil {
		return fmt.Errorf("reading the first dispatch's record: %w", err)
	}
	m := dispatchIDValue.FindSubmatch(first)
	if m == nil {
		return fmt.Errorf("the first record has no dispatch id:\n%s", first)
	}
	data, err := lastRecord(s)
	if err != nil {
		return err
	}
	for _, want := range []string{
		`"parent_dispatch_id": "` + string(m[1]) + `"`,
		`"parent_dispatch_source": "` + source + `"`,
	} {
		if !strings.Contains(string(data), want) {
			return fmt.Errorf("authority record lacks %s:\n%s", want, data)
		}
	}
	return nil
}

func iRememberTheDispatchInstancesAuthorityRecord(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	data, err := lastRecord(s)
	if err != nil {
		return ctx, err
	}
	s.authority.instancePath = s.lastDispatchInstancePath
	s.authority.recordPath = filepath.Join(s.lastDispatchInstancePath, ".niwa", "authority.json")
	s.authority.record = data
	return ctx, nil
}

func theRememberedAuthorityRecordIsUnchanged(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	data, err := os.ReadFile(s.authority.recordPath)
	if err != nil {
		return fmt.Errorf("reading the remembered record: %w\n%s%s", err, s.stdout, s.stderr)
	}
	if string(data) != string(s.authority.record) {
		return fmt.Errorf("the record changed:\nwas\n%s\nnow\n%s", s.authority.record, data)
	}
	return nil
}

func theRememberedInstanceHasNoAuthorityRecord(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if _, err := os.Stat(s.authority.recordPath); err == nil {
		return fmt.Errorf("the record at %s survived", s.authority.recordPath)
	}
	return nil
}

// iRunAgainstTheRememberedInstance runs a command with {instance} replaced by
// the remembered instance's name, from inside it when the command says
// "from-instance", else from the workspace root.
func iRunAgainstTheRememberedInstance(ctx context.Context, command string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.authority.instancePath == "" {
		return ctx, fmt.Errorf("no instance remembered")
	}
	command = strings.ReplaceAll(command, "{instance}", filepath.Base(s.authority.instancePath))
	cwd := s.workspaceRoot
	if rest, ok := strings.CutPrefix(command, "from-instance "); ok {
		command, cwd = rest, s.authority.instancePath
	}
	return ctx, runNiwa(s, cwd, command)
}

func theDispatchInstancesAuthorityRecordIsReplacedWith(ctx context.Context, body string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path := filepath.Join(s.lastDispatchInstancePath, ".niwa", "authority.json")
	return ctx, os.WriteFile(path, []byte(body), 0o600)
}

// theWorkspaceRootsConfigBecomes rewrites the workspace root's own config
// (the snapshot dispatch reads at the authority step): "-" leaves it,
// "not-toml" makes it unparseable, and a fixture name replaces its body.
func theWorkspaceRootsConfigBecomes(ctx context.Context, what string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "workspace.toml")
	switch what {
	case "-":
		return ctx, nil
	case "not-toml":
		return ctx, os.WriteFile(path, []byte("[workspace]\nname = \"ws\"\nthis is not toml\n"), 0o644)
	}
	body, ok := authorityFixtures[what]
	if !ok {
		return ctx, fmt.Errorf("no authority fixture %q", what)
	}
	return ctx, os.WriteFile(path, []byte("[workspace]\nname = \"ws\"\n\n"+body), 0o644)
}

func theFirstDispatchInstanceNoLongerExists(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.authority.firstInstance == "" {
		return fmt.Errorf("no first dispatch instance kept")
	}
	if _, err := os.Stat(s.authority.firstInstance); err == nil {
		return fmt.Errorf("%s still exists\n%s%s", s.authority.firstInstance, s.stdout, s.stderr)
	}
	return nil
}

// theLocalCloneOfOverlayIsRemoved deletes the overlay clone niwa keeps under
// $XDG_CONFIG_HOME/niwa/overlays, which is <home>/.config in these scenarios.
func theLocalCloneOfOverlayIsRemoved(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.homeDir, ".config", "niwa", "overlays", "file-"+name)
	if _, err := os.Stat(dir); err != nil {
		return ctx, fmt.Errorf("no overlay clone at %s: %w", dir, err)
	}
	return ctx, os.RemoveAll(dir)
}

func workerOTEL(s *testState) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.homeDir, "dispatch-launch-otel"))
	if err != nil {
		return "", fmt.Errorf("the fake claude recorded no OTEL_RESOURCE_ATTRIBUTES: %w", err)
	}
	return string(data), nil
}

func theWorkersResourceAttributesHold(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	got, err := workerOTEL(s)
	if err != nil {
		return err
	}
	entries := map[string]bool{}
	for _, e := range strings.Split(got, ",") {
		entries[e] = true
	}
	for _, e := range strings.Split(want, ",") {
		if !entries[e] {
			return fmt.Errorf("the worker's attributes %q lack %q", got, e)
		}
	}
	return nil
}

// theWorkersLaunchCarriedNoAuthorityAttributes reads every launch record the
// fake agents keep in HOME and finds no authority attribute in any of them.
func theWorkersLaunchCarriedNoAuthorityAttributes(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	entries, err := os.ReadDir(s.homeDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.homeDir, e.Name()))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), "niwa.authority.") {
			return fmt.Errorf("%s carries an authority attribute", e.Name())
		}
	}
	return nil
}

// theInstanceFilesMatch compares every file niwa wrote into two dispatch
// instances, apart from .niwa (which holds each instance's own state and its
// record), with each instance's name and path written the same way. A grant
// that changed a hook file or a settings document would show here.
func theInstanceFilesMatch(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	a, b := s.authority.firstInstance, s.lastDispatchInstancePath
	if a == "" || b == "" || a == b {
		return fmt.Errorf("need two dispatch instances, have %q and %q", a, b)
	}
	read := func(root string) (map[string]string, error) {
		out := map[string]string{}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			if d.IsDir() {
				if rel == ".niwa" {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := strings.ReplaceAll(string(data), root, "<instance>")
			text = strings.ReplaceAll(text, filepath.Base(root), "<name>")
			out[rel] = text
			return nil
		})
		return out, err
	}
	fa, err := read(a)
	if err != nil {
		return err
	}
	fb, err := read(b)
	if err != nil {
		return err
	}
	if len(fa) == 0 {
		return fmt.Errorf("the first instance holds no files outside .niwa")
	}
	for rel, text := range fa {
		other, ok := fb[rel]
		if !ok {
			return fmt.Errorf("%s exists only in the first instance", rel)
		}
		if other != text {
			return fmt.Errorf("%s differs between the two instances:\n%s\n---\n%s", rel, text, other)
		}
	}
	for rel := range fb {
		if _, ok := fa[rel]; !ok {
			return fmt.Errorf("%s exists only in the last instance", rel)
		}
	}
	return nil
}
