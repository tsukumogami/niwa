package functional

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cucumber/godog"
)

// registerReapProofRemoteSteps wires the steps the proof-remote scenarios
// use: an https git server that counts its requests, remotes added to or
// re-pointed in a dispatch instance's clone, repositories the workspace
// configuration doesn't name, tags, and proof_remotes prefixes.
func registerReapProofRemoteSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^an https git server is running$`, anHTTPSGitServerIsRunning)
	ctx.Step(`^the https git server serves "([^"]*)" holding branch "([^"]*)" of repo "([^"]*)" in the dispatch instance$`, theServerServesABranch)
	ctx.Step(`^repo "([^"]*)" of the dispatch instance has remote "([^"]*)" at "([^"]*)"$`, repoHasRemoteAt)
	ctx.Step(`^the https git server saw no request$`, theServerSawNoRequest)
	ctx.Step(`^the https git server saw a request for "([^"]*)"$`, theServerSawARequestFor)
	ctx.Step(`^the workspace configuration lists the proof remote prefix "([^"]*)"$`, theConfigListsAProofRemotePrefix)
	ctx.Step(`^repo "([^"]*)" of the dispatch instance has its origin at the workspace configuration's URL for repo "([^"]*)"$`, originAtTheConfiguredURL)
	ctx.Step(`^a commit with a file change is made on branch "([^"]*)" in repo "([^"]*)" of the dispatch instance$`, aCommitWithAChangeIsMade)
	ctx.Step(`^branch "([^"]*)" of repo "([^"]*)" in the dispatch instance is pushed to "([^"]*)"$`, branchIsPushedTo)
	ctx.Step(`^a (bare )?clone of repo "([^"]*)" from the workspace configuration's URL sits at "([^"]*)" in the dispatch instance$`, aCloneSitsAt)
	ctx.Step(`^a lightweight tag "([^"]*)" is made in repo "([^"]*)" of the dispatch instance$`, aLightweightTagIsMade)
	ctx.Step(`^the origin of repo "([^"]*)" in the dispatch instance holds an annotated tag "([^"]*)" at another object$`, originHoldsAnAnnotatedTag)
	ctx.Step(`^the ext marker was never written$`, theExtMarkerWasNeverWritten)
	ctx.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if g, ok := ctx.Value(gitHTTPServerKey{}).(*gitHTTPServer); ok {
			g.close()
		}
		return ctx, err
	})
}

type gitHTTPServerKey struct{}

func gitServerFrom(ctx context.Context) (*gitHTTPServer, error) {
	g, ok := ctx.Value(gitHTTPServerKey{}).(*gitHTTPServer)
	if !ok {
		return nil, fmt.Errorf("no https git server is running")
	}
	return g, nil
}

func anHTTPSGitServerIsRunning(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir, err := os.MkdirTemp(s.tmpDir, "git-https-")
	if err != nil {
		return ctx, err
	}
	g, err := startGitHTTPServer(dir)
	if err != nil {
		return ctx, err
	}
	// The rule's git calls trust the server only through the test build's
	// CA seam.
	s.envOverrides["NIWA_TEST_REMOVAL_CA_FILE"] = g.caFile
	return context.WithValue(ctx, gitHTTPServerKey{}, g), nil
}

// proofGit runs git in dir with a fixed identity and no global
// configuration.
func proofGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// instanceRepo is the directory of repo in the scenario's dispatch instance.
func instanceRepo(ctx context.Context, repo string) (string, error) {
	s := getState(ctx)
	if s == nil {
		return "", fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return "", err
	}
	return filepath.Join(inst, filepath.FromSlash(repo)), nil
}

// expandURL fills a URL template: {server} is the https git server's host
// and port, {otherport} the same host on a port nothing serves, {marker} a
// file under the scenario's temporary directory an ext:: command would
// write.
func expandURL(ctx context.Context, tmpl string) (string, error) {
	s := getState(ctx)
	if s == nil {
		return "", fmt.Errorf("no test state")
	}
	out := strings.ReplaceAll(tmpl, "{marker}", filepath.Join(s.tmpDir, "ext-marker"))
	if strings.Contains(out, "{server}") || strings.Contains(out, "{otherport}") {
		g, err := gitServerFrom(ctx)
		if err != nil {
			return "", err
		}
		out = strings.ReplaceAll(out, "{server}", g.hostPort())
		if strings.Contains(out, "{otherport}") {
			other, err := g.otherPort()
			if err != nil {
				return "", err
			}
			out = strings.ReplaceAll(out, "{otherport}", other)
		}
	}
	return out, nil
}

func theServerServesABranch(ctx context.Context, path, branch, repo string) error {
	g, err := gitServerFrom(ctx)
	if err != nil {
		return err
	}
	dir, err := g.repoDir(path)
	if err != nil {
		return err
	}
	clone, err := instanceRepo(ctx, repo)
	if err != nil {
		return err
	}
	// Pushed through the file system, so the server's count stays at what
	// niwa sends it.
	_, err = proofGit(clone, "push", "-q", dir, branch+":refs/heads/"+branch)
	if err != nil {
		return err
	}
	_, err = proofGit(dir, "symbolic-ref", "HEAD", "refs/heads/"+branch)
	return err
}

func repoHasRemoteAt(ctx context.Context, repo, name, tmpl string) error {
	url, err := expandURL(ctx, tmpl)
	if err != nil {
		return err
	}
	clone, err := instanceRepo(ctx, repo)
	if err != nil {
		return err
	}
	if _, err := proofGit(clone, "remote", "set-url", name, url); err == nil {
		return nil
	}
	_, err = proofGit(clone, "remote", "add", name, url)
	return err
}

func theServerSawNoRequest(ctx context.Context) error {
	g, err := gitServerFrom(ctx)
	if err != nil {
		return err
	}
	if reqs := g.requests(); len(reqs) != 0 {
		return fmt.Errorf("the https git server saw %d requests: %q", len(reqs), reqs)
	}
	return nil
}

func theServerSawARequestFor(ctx context.Context, path string) error {
	g, err := gitServerFrom(ctx)
	if err != nil {
		return err
	}
	for _, r := range g.requests() {
		if strings.HasPrefix(r, "/"+strings.TrimPrefix(path, "/")) {
			return nil
		}
	}
	return fmt.Errorf("the https git server saw no request for %s: %q", path, g.requests())
}

// theConfigListsAProofRemotePrefix appends a [session.removal] table with
// the prefix to the workspace configuration at the root, after init.
func theConfigListsAProofRemotePrefix(ctx context.Context, tmpl string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	prefix, err := expandURL(ctx, tmpl)
	if err != nil {
		return err
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "workspace.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), "[session.removal]") {
		return fmt.Errorf("%s already has a [session.removal] table:\n%s", path, data)
	}
	data = append(data, []byte(fmt.Sprintf("\n[session.removal]\nproof_remotes = [%q]\n", prefix))...)
	return os.WriteFile(path, data, 0o644)
}

var repoURLLine = regexp.MustCompile(`^\s*url\s*=\s*"([^"]*)"\s*$`)

// configuredURL reads the url the workspace configuration at the root gives
// [repos.<repo>].
func configuredURL(ctx context.Context, repo string) (string, error) {
	s := getState(ctx)
	if s == nil {
		return "", fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "workspace.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	inSection := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inSection = trimmed == "[repos."+repo+"]"
			continue
		}
		if m := repoURLLine.FindStringSubmatch(line); inSection && m != nil {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("no url for [repos.%s] in %s", repo, path)
}

func originAtTheConfiguredURL(ctx context.Context, repo, name string) error {
	url, err := configuredURL(ctx, name)
	if err != nil {
		return err
	}
	clone, err := instanceRepo(ctx, repo)
	if err != nil {
		return err
	}
	_, err = proofGit(clone, "remote", "set-url", "origin", url)
	return err
}

func aCommitWithAChangeIsMade(ctx context.Context, branch, repo string) error {
	clone, err := instanceRepo(ctx, repo)
	if err != nil {
		return err
	}
	if _, err := proofGit(clone, "checkout", "-q", "-b", branch); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(clone, "work-"+branch+".txt"), []byte(branch+"\n"), 0o644); err != nil {
		return err
	}
	if _, err := proofGit(clone, "add", "work-"+branch+".txt"); err != nil {
		return err
	}
	_, err = proofGit(clone, "commit", "-q", "-m", "work on "+branch)
	return err
}

// branchIsPushedTo pushes a branch, or a tag named as refs/tags/<name>, to
// a remote of the clone or to a repository path inside the dispatch
// instance.
func branchIsPushedTo(ctx context.Context, branch, repo, dest string) error {
	clone, err := instanceRepo(ctx, repo)
	if err != nil {
		return err
	}
	target := dest
	if dest != "origin" {
		if target, err = instanceRepo(ctx, dest); err != nil {
			return err
		}
	}
	_, err = proofGit(clone, "push", "-q", target, branch)
	return err
}

func aCloneSitsAt(ctx context.Context, bare, repo, at string) error {
	url, err := configuredURL(ctx, repo)
	if err != nil {
		return err
	}
	dir, err := instanceRepo(ctx, at)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	args := []string{"clone", "-q"}
	if bare != "" {
		args = append(args, "--bare")
	}
	_, err = proofGit(filepath.Dir(dir), append(args, url, dir)...)
	return err
}

func aLightweightTagIsMade(ctx context.Context, tag, repo string) error {
	clone, err := instanceRepo(ctx, repo)
	if err != nil {
		return err
	}
	_, err = proofGit(clone, "tag", tag)
	return err
}

// originHoldsAnAnnotatedTag replaces the origin's tag with an annotated tag
// of the same name, another object, made in a scratch clone of the origin.
func originHoldsAnAnnotatedTag(ctx context.Context, repo, tag string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	clone, err := instanceRepo(ctx, repo)
	if err != nil {
		return err
	}
	origin, err := proofGit(clone, "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp(s.tmpDir, "tagger-")
	if err != nil {
		return err
	}
	for _, args := range [][]string{
		{"clone", "-q", origin, work},
	} {
		if _, err := proofGit(s.tmpDir, args...); err != nil {
			return err
		}
	}
	for _, args := range [][]string{
		{"tag", "-f", "-a", "-m", "annotated", tag, "HEAD"},
		{"push", "-q", "-f", "origin", "refs/tags/" + tag},
	} {
		if _, err := proofGit(work, args...); err != nil {
			return err
		}
	}
	return nil
}

func theExtMarkerWasNeverWritten(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if _, err := os.Lstat(filepath.Join(s.tmpDir, "ext-marker")); err == nil {
		return fmt.Errorf("an ext:: remote's command ran")
	}
	return nil
}
