package functional

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// registerReapLossScanSteps wires the steps the reap loss-scan scenarios use:
// a commit made by hand in a dispatch instance's clone, and a snapshot of the
// instance's whole tree, .git included, to show a reap that keeps it left
// every byte where it was.
func registerReapLossScanSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a commit is made on branch "([^"]*)" in repo "([^"]*)" of the dispatch instance$`, aCommitIsMadeInTheDispatchInstance)
	ctx.Step(`^a linked worktree "([^"]*)" of repo "([^"]*)" holds a commit on branch "([^"]*)" in the dispatch instance$`, aLinkedWorktreeHoldsACommit)
	ctx.Step(`^the dispatch instance's contents are recorded$`, theDispatchInstancesContentsAreRecorded)
	ctx.Step(`^the dispatch instance's contents are unchanged$`, theDispatchInstancesContentsAreUnchanged)
	ctx.Step(`^the error output names the dispatch instance$`, theErrorOutputNamesTheDispatchInstance)
	ctx.Step(`^the first dispatch instance still exists$`, theFirstDispatchInstanceStillExists)
	ctx.Step(`^the dispatch instance no longer exists$`, theDispatchInstanceNoLongerExists)
	ctx.Step(`^the workspace's units and records are recorded$`, theWorkspacesUnitsAndRecordsAreRecorded)
	ctx.Step(`^the workspace's units and records are unchanged$`, theWorkspacesUnitsAndRecordsAreUnchanged)
	ctx.Step(`^the dispatch instance's \.niwa is replaced by a link to a directory$`, theDispatchInstancesNiwaIsReplacedByALink)
	ctx.Step(`^the link's target directory is recorded$`, theLinksTargetIsRecorded)
	ctx.Step(`^the link's target directory is unchanged$`, theLinksTargetIsUnchanged)
	ctx.Step(`^the workspace configuration's URL for repo "([^"]*)" points at a refused local address with "([^"]*)" and query "([^"]*)"$`, theWorkspaceURLPointsAtARefusedAddress)
	ctx.Step(`^the workspace configuration declares the disposable pattern "([^"]*)"$`, theWorkspaceConfigurationDeclaresADisposablePattern)
	ctx.Step(`^I run niwa reap "([^"]*)" naming the dispatch instance from the workspace root$`, iRunNiwaReapNamingTheDispatchInstance)
	ctx.Step(`^no output contains "([^"]*)"$`, noOutputContains)
	ctx.Step(`^the output gives the (first dispatch instance|dispatch instance) the verdict "(keep|reap)"$`, theOutputGivesTheVerdict)
	ctx.Step(`^the output gives no verdict for the first dispatch instance$`, theOutputGivesNoVerdictForTheFirst)
	ctx.Step(`^the output is a niwa\.verdicts/v1 document giving the (first dispatch instance|dispatch instance) the verdict "(keep|reap)"$`, theOutputIsAVerdictsDocumentGiving)
	registerReapPathClassSteps(ctx)
	registerReapTombstoneSteps(ctx)
	registerReapProofRemoteSteps(ctx)
}

func theFirstDispatchInstanceStillExists(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.authority.firstInstance == "" {
		return fmt.Errorf("no first dispatch instance was kept")
	}
	if _, err := os.Stat(s.authority.firstInstance); err != nil {
		return fmt.Errorf("the first dispatch instance %s is gone: %w\nstderr:\n%s", s.authority.firstInstance, err, s.stderr)
	}
	return nil
}

// dispatchInstanceForStep returns the dispatch instance the scenario made.
func dispatchInstanceForStep(s *testState) (string, error) {
	inst := s.lastDispatchInstancePath
	if inst == "" {
		inst = findDispatchInstance(s.workspaceRoot)
	}
	if inst == "" {
		return "", fmt.Errorf("no dispatch instance under %s", s.workspaceRoot)
	}
	return inst, nil
}

func aCommitIsMadeInTheDispatchInstance(ctx context.Context, branch, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return ctx, err
	}
	clone := filepath.Join(inst, filepath.FromSlash(repo))
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	// The commit changes a file: an empty commit leaves the default
	// branch's own tree, which the rule counts as merged by content.
	if err := os.WriteFile(filepath.Join(clone, "unpushed-work.txt"), []byte(branch+"\n"), 0o644); err != nil {
		return ctx, err
	}
	for _, args := range [][]string{
		{"checkout", "-q", "-b", branch},
		{"add", "unpushed-work.txt"},
		{"commit", "-q", "-m", "work nobody pushed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = clone
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return ctx, fmt.Errorf("git %v in %s: %v\n%s", args, clone, err, out)
		}
	}
	return ctx, nil
}

// aLinkedWorktreeHoldsACommit adds a linked worktree of a clone at the top
// of the dispatch instance, on a new branch, and commits there, so the
// commit is held only by a ref the worktree made and its own HEAD.
func aLinkedWorktreeHoldsACommit(ctx context.Context, name, repo, branch string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return ctx, err
	}
	clone := filepath.Join(inst, filepath.FromSlash(repo))
	wt := filepath.Join(inst, name)
	env := append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	for _, step := range []struct {
		dir  string
		args []string
	}{
		{clone, []string{"worktree", "add", "-q", "-b", branch, wt}},
		{wt, []string{"add", "worktree-work.txt"}},
		{wt, []string{"commit", "-q", "-m", "worktree work nobody pushed"}},
	} {
		if step.args[0] == "add" {
			// The commit changes a file: an empty commit leaves the
			// default branch's own tree, which counts as merged by content.
			if err := os.WriteFile(filepath.Join(wt, "worktree-work.txt"), []byte(branch+"\n"), 0o644); err != nil {
				return ctx, err
			}
		}
		cmd := exec.Command("git", step.args...)
		cmd.Dir = step.dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			return ctx, fmt.Errorf("git %v in %s: %v\n%s", step.args, step.dir, err, out)
		}
	}
	return ctx, nil
}

// instanceTreeDigest lists every entry under dir with its mode, modification time
// and, for a file, a hash of its contents, without following links.
func instanceTreeDigest(dir string) (string, error) {
	var b strings.Builder
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		fmt.Fprintf(&b, "%s %s %d", rel, info.Mode(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			b.WriteString(" " + hex.EncodeToString(sum[:]))
		}
		b.WriteString("\n")
		return nil
	})
	return b.String(), err
}

func theDispatchInstancesContentsAreRecorded(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return ctx, err
	}
	digest, err := instanceTreeDigest(inst)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, instanceDigestKey{}, digest), nil
}

type instanceDigestKey struct{}

func theDispatchInstancesContentsAreUnchanged(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	before, ok := ctx.Value(instanceDigestKey{}).(string)
	if !ok {
		return fmt.Errorf("the dispatch instance's contents were never recorded")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return err
	}
	after, err := instanceTreeDigest(inst)
	if err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("the dispatch instance changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	return nil
}

func theErrorOutputNamesTheDispatchInstance(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return err
	}
	want := "niwa: kept " + filepath.Base(inst) + ":"
	if !strings.Contains(s.stderr, want) {
		return fmt.Errorf("error output does not contain %q:\n%s", want, s.stderr)
	}
	return nil
}

func theDispatchInstanceNoLongerExists(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return fmt.Errorf("no dispatch instance path recorded")
	}
	if _, err := os.Lstat(s.lastDispatchInstancePath); !os.IsNotExist(err) {
		return fmt.Errorf("dispatch instance %s is still there (%v)\nstderr:\n%s", s.lastDispatchInstancePath, err, s.stderr)
	}
	return nil
}

// unitSurroundingsDigest records everything a verdict-only reap must leave
// alone: the workspace root's own entries, every instance under it (each
// repository's .git included), the jobs directory that holds every unit's
// job directory, and the root's .niwa/ (sessions/ included) and
// .niwa-launch/. Each section is headed by its label and records a missing
// directory as absent, so one that appears or vanishes shows.
func unitSurroundingsDigest(s *testState) (string, error) {
	var b strings.Builder
	section := func(label, dir string) error {
		fmt.Fprintf(&b, "== %s\n", label)
		if _, err := os.Lstat(dir); os.IsNotExist(err) {
			b.WriteString("absent\n")
			return nil
		}
		d, err := instanceTreeDigest(dir)
		if err != nil {
			return err
		}
		b.WriteString(d)
		return nil
	}
	entries, err := os.ReadDir(s.workspaceRoot)
	if err != nil {
		return "", err
	}
	b.WriteString("== root entries\n")
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s %s\n", e.Name(), info.Mode())
	}
	for _, e := range entries {
		dir := filepath.Join(s.workspaceRoot, e.Name())
		if _, err := os.Stat(filepath.Join(dir, ".niwa", "instance.json")); err != nil || e.Name() == ".niwa" {
			continue
		}
		if err := section("instance "+e.Name(), dir); err != nil {
			return "", err
		}
	}
	for _, sec := range []struct{ label, dir string }{
		{"jobs", filepath.Join(s.homeDir, ".claude", "jobs")},
		{"root .niwa", filepath.Join(s.workspaceRoot, ".niwa")},
		{"root .niwa-launch", filepath.Join(s.workspaceRoot, ".niwa-launch")},
	} {
		if err := section(sec.label, sec.dir); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

type surroundingsDigestKey struct{}

func theWorkspacesUnitsAndRecordsAreRecorded(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	digest, err := unitSurroundingsDigest(s)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, surroundingsDigestKey{}, digest), nil
}

func theWorkspacesUnitsAndRecordsAreUnchanged(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	before, ok := ctx.Value(surroundingsDigestKey{}).(string)
	if !ok {
		return fmt.Errorf("the workspace's units and records were never recorded")
	}
	after, err := unitSurroundingsDigest(s)
	if err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("the workspace's units or records changed:\n%s", digestDiff(before, after))
	}
	return nil
}

// digestDiff lists the lines one digest has and the other doesn't.
func digestDiff(before, after string) string {
	in := func(lines []string) map[string]bool {
		m := map[string]bool{}
		for _, l := range lines {
			m[l] = true
		}
		return m
	}
	bl, al := strings.Split(before, "\n"), strings.Split(after, "\n")
	bm, am := in(bl), in(al)
	var out strings.Builder
	for _, l := range bl {
		if !am[l] {
			out.WriteString("- " + l + "\n")
		}
	}
	for _, l := range al {
		if !bm[l] {
			out.WriteString("+ " + l + "\n")
		}
	}
	return out.String()
}

// niwaLinkTargetKey holds where the dispatch instance's .niwa was moved to.
type niwaLinkTargetKey struct{}

type niwaLinkTargetDigestKey struct{}

func theDispatchInstancesNiwaIsReplacedByALink(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return ctx, err
	}
	target := filepath.Join(s.homeDir, "moved-niwa")
	if err := os.Rename(filepath.Join(inst, ".niwa"), target); err != nil {
		return ctx, err
	}
	if err := os.Symlink(target, filepath.Join(inst, ".niwa")); err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, niwaLinkTargetKey{}, target), nil
}

func theLinksTargetIsRecorded(ctx context.Context) (context.Context, error) {
	target, ok := ctx.Value(niwaLinkTargetKey{}).(string)
	if !ok {
		return ctx, fmt.Errorf("no .niwa was replaced by a link")
	}
	digest, err := instanceTreeDigest(target)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, niwaLinkTargetDigestKey{}, digest), nil
}

func theLinksTargetIsUnchanged(ctx context.Context) error {
	target, ok := ctx.Value(niwaLinkTargetKey{}).(string)
	before, recorded := ctx.Value(niwaLinkTargetDigestKey{}).(string)
	if !ok || !recorded {
		return fmt.Errorf("the link's target was never recorded")
	}
	after, err := instanceTreeDigest(target)
	if err != nil {
		return err
	}
	if after != before {
		return fmt.Errorf("the link's target changed:\n%s", digestDiff(before, after))
	}
	return nil
}

// theWorkspaceURLPointsAtARefusedAddress rewrites [repos.<repo>] url in the
// workspace configuration at the root to an https URL on a local port that
// refuses connections, carrying the given userinfo and query. The rule reads
// the clone URL from there, never from the clone's own remotes.
func theWorkspaceURLPointsAtARefusedAddress(ctx context.Context, repo, userinfo, query string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		return err
	}
	host := fmt.Sprintf("127.0.0.1:%d", port)
	if userinfo != "" {
		host = userinfo + "@" + host
	}
	url := "https://" + host + "/" + repo + query

	path := filepath.Join(s.workspaceRoot, ".niwa", "workspace.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	inSection, replaced := false, false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inSection = trimmed == "[repos."+repo+"]"
			continue
		}
		if inSection && strings.HasPrefix(trimmed, "url") && strings.Contains(trimmed, "=") {
			lines[i] = fmt.Sprintf("url = %q", url)
			replaced = true
		}
	}
	if !replaced {
		return fmt.Errorf("no url for [repos.%s] in %s:\n%s", repo, path, data)
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
}

// theWorkspaceConfigurationDeclaresADisposablePattern appends one
// [[session.removal.disposable]] entry with pattern to the workspace
// configuration at the root, after init has loaded it, so a pattern init
// would refuse reaches the reap.
func theWorkspaceConfigurationDeclaresADisposablePattern(ctx context.Context, pattern string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "workspace.toml")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "\n[[session.removal.disposable]]\npattern = %q\nreview = \"functional test\"\n", pattern); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func iRunNiwaReapNamingTheDispatchInstance(ctx context.Context, flags string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return ctx, err
	}
	return ctx, runNiwa(s, s.workspaceRoot, "niwa reap "+flags+" "+filepath.Base(inst))
}

func noOutputContains(ctx context.Context, text string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if strings.Contains(s.stdout, text) || strings.Contains(s.stderr, text) {
		return fmt.Errorf("the output carries %q\nstdout:\n%s\nstderr:\n%s", text, s.stdout, s.stderr)
	}
	return nil
}

// verdictUnitName is the name the verdict output gives the instance a step
// names.
func verdictUnitName(s *testState, which string) (string, error) {
	if which == "first dispatch instance" {
		if s.authority.firstInstance == "" {
			return "", fmt.Errorf("no first dispatch instance was kept")
		}
		return filepath.Base(s.authority.firstInstance), nil
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return "", err
	}
	return filepath.Base(inst), nil
}

func theOutputGivesTheVerdict(ctx context.Context, which, verdict string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	name, err := verdictUnitName(s, which)
	if err != nil {
		return err
	}
	want := name + ": " + verdict + "\n"
	if !strings.HasPrefix(s.stdout, want) && !strings.Contains(s.stdout, "\n"+want) {
		return fmt.Errorf("output does not give %s the verdict %s:\nstdout:\n%s\nstderr:\n%s", name, verdict, s.stdout, s.stderr)
	}
	return nil
}

func theOutputGivesNoVerdictForTheFirst(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	name, err := verdictUnitName(s, "first dispatch instance")
	if err != nil {
		return err
	}
	if strings.Contains(s.stdout, name) {
		return fmt.Errorf("output names %s:\n%s", name, s.stdout)
	}
	return nil
}

// verdictsDocument is niwa.verdicts/v1 as a consumer decodes it.
type verdictsDocument struct {
	Schema string `json:"schema"`
	Units  []struct {
		Instance string `json:"instance"`
		Verdict  string `json:"verdict"`
		Reached  bool   `json:"reached"`
		Checks   []struct {
			Check    string   `json:"check"`
			Paths    []string `json:"paths"`
			Evidence string   `json:"evidence"`
		} `json:"checks"`
		Paths []struct {
			Path   string `json:"path"`
			Class  string `json:"class"`
			Notice string `json:"notice"`
		} `json:"paths"`
		Over json.RawMessage `json:"over"`
	} `json:"units"`
}

func theOutputIsAVerdictsDocumentGiving(ctx context.Context, which, verdict string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	name, err := verdictUnitName(s, which)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(s.stdout))
	dec.DisallowUnknownFields()
	var doc verdictsDocument
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("the output is not a niwa.verdicts/v1 document: %v\n%s", err, s.stdout)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("data after the document (%v):\n%s", err, s.stdout)
	}
	if doc.Schema != "niwa.verdicts/v1" {
		return fmt.Errorf("schema %q, want niwa.verdicts/v1", doc.Schema)
	}
	found := false
	for _, u := range doc.Units {
		if string(u.Over) != "null" {
			return fmt.Errorf("unit %s carries over %s; want null", u.Instance, u.Over)
		}
		if u.Instance != name {
			continue
		}
		found = true
		if !u.Reached || u.Verdict != verdict {
			return fmt.Errorf("unit %s: verdict %q, reached %v; want %q, reached", name, u.Verdict, u.Reached, verdict)
		}
	}
	if !found {
		return fmt.Errorf("the document has no unit %s:\n%s", name, s.stdout)
	}
	return nil
}
