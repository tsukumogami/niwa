package functional

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// The plugin-release-hold steps for a release that went missing from the
// store. A scenario names one commit of a marketplace as:
//
//	the release "<tag>"   a published, tagged release
//	the untagged commit   a commit on main that no tag names, behind a tag
//	the fork commit       a commit only a fork holds, in no ref of the source
//
// Each is served by the tarball fake at its commit, so a fetch the store
// should have refused would still succeed and show in the log.

// pluginTarget matches how a step names one commit of a marketplace.
const pluginTarget = `(release "[^"]*"|untagged commit|fork commit)`

func registerPluginRepairSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the GitHub marketplace "([^"]*)" gains an untagged commit$`, theMarketplaceGainsAnUntaggedCommit)
	ctx.Step(`^a fork of "([^"]*)" holds a commit$`, aForkHoldsACommit)
	ctx.Step(`^instance "([^"]*)" links "([^"]*)" to the (untagged|fork) commit$`, instanceLinksToCommit)
	ctx.Step(`^instance "([^"]*)" still links "([^"]*)" to the (untagged|fork) commit$`, instanceStillLinksToCommit)
	ctx.Step(`^the `+pluginTarget+` of "([^"]*)" is deleted from the plugin store of workspace root "([^"]*)"$`, theReleaseIsDeleted)
	ctx.Step(`^the held\.json of the `+pluginTarget+` of "([^"]*)" is deleted from the plugin store of workspace root "([^"]*)"$`, theMarkIsDeleted)
	ctx.Step(`^the GitHub fake served (\d+) tarball requests? for the `+pluginTarget+` of "([^"]*)"$`, nTarballRequestsFor)
	ctx.Step(`^the GitHub fake served (\d+) tarball requests? in all$`, nTarballRequestsInAll)
	ctx.Step(`^the `+pluginTarget+` of "([^"]*)" is held again in the plugin store of workspace root "([^"]*)"$`, theReleaseIsHeldAgain)
	ctx.Step(`^the plugin store of workspace root "([^"]*)" holds nothing for the `+pluginTarget+` of "([^"]*)"$`, theStoreHoldsNothingFor)
	ctx.Step(`^the output reports the `+pluginTarget+` of "([^"]*)" (restored|tampered|refused|not restored|not restored after tampering) for instances? "([^"]*)"$`, theOutputReportsTheRelease)
	ctx.Step(`^I run "([^"]*)" in instance "([^"]*)" with every tarball fetch failing$`, iRunInInstanceWithTarballsFailing)
	ctx.Step(`^the `+pluginTarget+` of "([^"]*)" is tampered with in the plugin store of workspace root "([^"]*)" by (editing a file|adding a file|removing a file|a chmod alone)$`, theReleaseIsTamperedWith)
	ctx.Step(`^the `+pluginTarget+` of "([^"]*)" in the plugin store of workspace root "([^"]*)" matches its tarball$`, theReleaseMatchesItsTarball)
}

// theReleaseIsTamperedWith changes a held release's tree the way someone
// with the owner's rights could: a file given its write bit back and
// appended to, a file added after its directory was given its write bit, a
// file removed the same way, or a chmod u+w with no content change. Each
// directory given its write bit for the change has it taken away after.
func theReleaseIsTamperedWith(ctx context.Context, target, slug, root, how string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	mk, rel, err := commitTarget(ps, target, slug)
	if err != nil {
		return ctx, err
	}
	rdir, err := storeReleaseDir(s, root, mk, rel.commit)
	if err != nil {
		return ctx, err
	}
	if _, err := os.Lstat(filepath.Join(rdir, "held.json")); err != nil {
		return ctx, fmt.Errorf("%s is not held before the change: %w", rdir, err)
	}
	skillDir := filepath.Join(rdir, "tree", "plugins", "kit", "skills", mk.repo+"-skill")
	skill := filepath.Join(skillDir, "SKILL.md")
	inDir := func(dir string, fn func() error) error {
		if err := os.Chmod(dir, 0o755); err != nil {
			return err
		}
		ferr := fn()
		if err := os.Chmod(dir, 0o555); err != nil {
			return err
		}
		return ferr
	}
	switch how {
	case "editing a file":
		if err := os.Chmod(skill, 0o644); err != nil {
			return ctx, err
		}
		f, err := os.OpenFile(skill, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return ctx, err
		}
		_, werr := f.WriteString("tampered\n")
		if err := errors.Join(werr, f.Close()); err != nil {
			return ctx, err
		}
	case "adding a file":
		err = inDir(skillDir, func() error {
			return os.WriteFile(filepath.Join(skillDir, "added.md"), []byte("added\n"), 0o444)
		})
	case "removing a file":
		scripts := filepath.Join(rdir, "tree", "plugins", "kit", "scripts")
		err = inDir(scripts, func() error { return os.Remove(filepath.Join(scripts, "run.sh")) })
	case "a chmod alone":
		err = os.Chmod(skill, 0o644)
	}
	return ctx, err
}

// theReleaseMatchesItsTarball checks a held release is read-only and marked
// and that its tree holds exactly the regular files of the tarball the fake
// serves at its commit, with the same content. The tarball is read from the
// fake directly, so the check adds nothing to the request log.
func theReleaseMatchesItsTarball(ctx context.Context, target, slug, root string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	mk, rel, err := commitTarget(ps, target, slug)
	if err != nil {
		return err
	}
	rdir, err := storeReleaseDir(s, root, mk, rel.commit)
	if err != nil {
		return err
	}
	restored := rel
	restored.tag = ""
	if err := checkHeldRelease(rdir, mk, restored); err != nil {
		return err
	}
	archive := s.githubFake.tarballFor(mk.owner, mk.repo, rel.commit)
	if archive == nil {
		return fmt.Errorf("the fake serves no tarball for %s at %s", slug, rel.commit)
	}
	want := map[string]string{}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		parts := strings.SplitN(hdr.Name, "/", 2)
		if len(parts) == 2 {
			want[parts[1]] = string(body)
		}
	}
	tree := filepath.Join(rdir, "tree")
	got := map[string]string{}
	err = filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(tree, p)
		got[filepath.ToSlash(rel)] = string(body)
		return nil
	})
	if err != nil {
		return err
	}
	if len(got) != len(want) {
		return fmt.Errorf("%s holds %d files, the tarball %d", tree, len(got), len(want))
	}
	for name, body := range want {
		if got[name] != body {
			return fmt.Errorf("%s/%s = %q, the tarball holds %q", tree, name, got[name], body)
		}
	}
	return nil
}

// commitTarget resolves a step's name for one commit of slug.
func commitTarget(ps *pluginStoreScenario, target, slug string) (*pluginMarketplace, pluginRelease, error) {
	mk := ps.marketplaces[slug]
	if mk == nil {
		return nil, pluginRelease{}, fmt.Errorf("no published marketplace %s", slug)
	}
	switch target {
	case "untagged commit", "untagged":
		if mk.untagged.commit == "" {
			return nil, pluginRelease{}, fmt.Errorf("%s has no untagged commit", slug)
		}
		return mk, mk.untagged, nil
	case "fork commit", "fork":
		if mk.fork.commit == "" {
			return nil, pluginRelease{}, fmt.Errorf("no fork of %s holds a commit", slug)
		}
		return mk, mk.fork, nil
	}
	tag := strings.TrimSuffix(strings.TrimPrefix(target, `release "`), `"`)
	for _, r := range mk.releases {
		if r.tag == tag {
			return mk, r, nil
		}
	}
	return nil, pluginRelease{}, fmt.Errorf("%s has no release %s", slug, tag)
}

// serveCommit serves a marketplace tree at commit, shaped as the published
// releases are, with its own sentinel.
func serveCommit(s *testState, mk *pluginMarketplace, commit, sentinel string, commitTime time.Time) {
	wrapper := mk.owner + "-" + mk.repo + "-" + commit[:7]
	plugin := mk.repo + "-kit"
	entries := map[string]string{
		wrapper + "/": "",
		wrapper + "/.claude-plugin/marketplace.json":                   fmt.Sprintf(`{"name":%q,"plugins":[{"name":%q,"source":"./plugins/kit"}]}`, mk.repo, plugin),
		wrapper + "/plugins/kit/.claude-plugin/plugin.json":            fmt.Sprintf(`{"name":%q,"version":"0.1.0"}`, plugin),
		wrapper + "/plugins/kit/skills/" + mk.repo + "-skill/SKILL.md": "---\nname: " + mk.repo + "-skill\n---\n" + sentinel + "\n",
		wrapper + "/plugins/kit/scripts/run.sh":                        "#!/bin/sh\necho " + sentinel + "\n",
	}
	executables := map[string]bool{wrapper + "/plugins/kit/scripts/run.sh": true}
	s.githubFake.SetTarballAt(mk.owner, mk.repo, commit, entries, commitTime, executables)
}

// commitIn commits a change in work and returns the commit.
func commitIn(work, sentinel, message string) (string, error) {
	if err := os.WriteFile(filepath.Join(work, "RELEASE"), []byte(sentinel+"\n"), 0o644); err != nil {
		return "", err
	}
	if out, err := fixtureGitCommit(work, "add", "-A"); err != nil {
		return "", fmt.Errorf("git add: %w\n%s", err, out)
	}
	if out, err := fixtureGitCommit(work, "commit", "-m", message); err != nil {
		return "", fmt.Errorf("git commit: %w\n%s", err, out)
	}
	out, err := fixtureGitWorkTree(work, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w\n%s", err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// theMarketplaceGainsAnUntaggedCommit pushes a commit to main with no tag. A
// release published after it puts it behind that release's tag.
func theMarketplaceGainsAnUntaggedCommit(ctx context.Context, slug string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	mk := ps.marketplaces[slug]
	if mk == nil {
		return ctx, fmt.Errorf("no published marketplace %s", slug)
	}
	sentinel := "SENTINEL-" + mk.owner + "-" + mk.repo + "-untagged"
	commit, err := commitIn(mk.work, sentinel, "untagged change")
	if err != nil {
		return ctx, err
	}
	if out, err := fixtureGitCommit(mk.work, "push", "origin", "HEAD:main"); err != nil {
		return ctx, fmt.Errorf("git push: %w\n%s", err, out)
	}
	s.githubFake.SetBehind(mk.owner, mk.repo, commit, mk.history)
	mk.history = append(mk.history, commit)
	commitTime := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	serveCommit(s, mk, commit, sentinel, commitTime)
	mk.untagged = pluginRelease{commit: commit, commitTime: commitTime}
	return ctx, nil
}

// aForkHoldsACommit makes a commit in a clone that is never pushed: no ref of
// the source names it and no ref has it behind. The fake still serves its
// tarball, so only the store's own refusal keeps it out.
func aForkHoldsACommit(ctx context.Context, slug string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	mk := ps.marketplaces[slug]
	if mk == nil {
		return ctx, fmt.Errorf("no published marketplace %s", slug)
	}
	work := filepath.Join(s.gitServer.root, "fork-"+mk.owner+"-"+mk.repo)
	if out, err := fixtureGit(s.gitServer.root, "clone", "file://"+mk.bare, work); err != nil {
		return ctx, fmt.Errorf("cloning %s: %w\n%s", mk.bare, err, out)
	}
	sentinel := "SENTINEL-" + mk.owner + "-" + mk.repo + "-fork"
	commit, err := commitIn(work, sentinel, "fork-only change")
	if err != nil {
		return ctx, err
	}
	commitTime := time.Date(2026, 11, 2, 9, 30, 0, 0, time.UTC)
	serveCommit(s, mk, commit, sentinel, commitTime)
	mk.fork = pluginRelease{commit: commit, commitTime: commitTime}
	return ctx, nil
}

// storeReleaseDir is a commit's directory in the root's store.
func storeReleaseDir(s *testState, root string, mk *pluginMarketplace, commit string) (string, error) {
	store, err := pluginStoreDirOf(s, root)
	if err != nil {
		return "", err
	}
	return filepath.Join(store, "github.com", mk.owner, mk.repo, commit), nil
}

// instanceLinksToCommit points the instance's link for slug at the commit's
// tree in the suite root's store, as a link niwa itself would write.
func instanceLinksToCommit(ctx context.Context, instance, slug, which string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	mk, rel, err := commitTarget(ps, which, slug)
	if err != nil {
		return ctx, err
	}
	rdir, err := storeReleaseDir(s, ".", mk, rel.commit)
	if err != nil {
		return ctx, err
	}
	link := filepath.Join(s.workspaceRoot, instance, ".niwa", "marketplaces", mk.repo)
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		return ctx, err
	}
	return ctx, os.Symlink(filepath.Join(rdir, "tree"), link)
}

func instanceStillLinksToCommit(ctx context.Context, instance, slug, which string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	mk, rel, err := commitTarget(ps, which, slug)
	if err != nil {
		return err
	}
	link := filepath.Join(s.workspaceRoot, instance, ".niwa", "marketplaces", mk.repo)
	target, err := os.Readlink(link)
	if err != nil {
		return fmt.Errorf("%s: %w", link, err)
	}
	if m := releaseLinkPattern.FindStringSubmatch(target); m == nil || m[5] != rel.commit {
		return fmt.Errorf("%s -> %s, want the %s commit %s", link, target, which, rel.commit)
	}
	return nil
}

// theReleaseIsDeleted removes the commit's whole directory from the store,
// write bits handed back first.
func theReleaseIsDeleted(ctx context.Context, target, slug, root string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	mk, rel, err := commitTarget(ps, target, slug)
	if err != nil {
		return ctx, err
	}
	rdir, err := storeReleaseDir(s, root, mk, rel.commit)
	if err != nil {
		return ctx, err
	}
	if _, err := os.Lstat(filepath.Join(rdir, "held.json")); err != nil {
		return ctx, fmt.Errorf("%s is not held before the deletion: %w", rdir, err)
	}
	restoreWriteBits(rdir)
	return ctx, os.RemoveAll(rdir)
}

// theMarkIsDeleted removes only held.json, leaving the tree.
func theMarkIsDeleted(ctx context.Context, target, slug, root string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	mk, rel, err := commitTarget(ps, target, slug)
	if err != nil {
		return ctx, err
	}
	rdir, err := storeReleaseDir(s, root, mk, rel.commit)
	if err != nil {
		return ctx, err
	}
	return ctx, os.Remove(filepath.Join(rdir, "held.json"))
}

// nTarballRequestsFor counts the tarball requests at one commit and reports
// the count.
func nTarballRequestsFor(ctx context.Context, want int, target, slug string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	mk, rel, err := commitTarget(ps, target, slug)
	if err != nil {
		return err
	}
	at := "/repos/" + mk.owner + "/" + mk.repo + "/tarball/" + rel.commit
	count := 0
	for _, r := range s.githubFake.Requests() {
		if r.Path == at {
			count++
		}
	}
	fmt.Printf("tarball requests for %s %s: %d\n", slug, target, count)
	if count != want {
		return fmt.Errorf("%s %s: %d tarball requests, want %d:\n%s", slug, target, count, want, describeRequests(s.githubFake.Requests()))
	}
	return nil
}

func nTarballRequestsInAll(ctx context.Context, want int) error {
	s := getState(ctx)
	if s == nil || s.githubFake == nil {
		return fmt.Errorf("no GitHub fake")
	}
	n := s.githubFake.CountRequests("/tarball/")
	fmt.Printf("tarball requests in all: %d\n", n)
	if n != want {
		return fmt.Errorf("%d tarball requests, want %d:\n%s", n, want, describeRequests(s.githubFake.Requests()))
	}
	return nil
}

// theReleaseIsHeldAgain checks the commit's directory is back where its links
// point, read-only and marked. A release restored from a link has no ref to
// record, so its mark's ref is empty.
func theReleaseIsHeldAgain(ctx context.Context, target, slug, root string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	mk, rel, err := commitTarget(ps, target, slug)
	if err != nil {
		return err
	}
	rdir, err := storeReleaseDir(s, root, mk, rel.commit)
	if err != nil {
		return err
	}
	restored := rel
	restored.tag = ""
	return checkHeldRelease(rdir, mk, restored)
}

func theStoreHoldsNothingFor(ctx context.Context, root, target, slug string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	mk, rel, err := commitTarget(ps, target, slug)
	if err != nil {
		return err
	}
	rdir, err := storeReleaseDir(s, root, mk, rel.commit)
	if err != nil {
		return err
	}
	for _, p := range []string{rdir, filepath.Join(rdir, "held.json")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			return fmt.Errorf("%s exists (%v)", p, err)
		}
	}
	return nil
}

// commandOutput is what the last command showed: its stderr, and its stdout,
// with a SessionStart hook's payload read for the context it injects.
func commandOutput(s *testState) string {
	stdout := s.stdout
	var payload struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &payload); err == nil && payload.HookSpecificOutput.AdditionalContext != "" {
		stdout = payload.HookSpecificOutput.AdditionalContext
	}
	return stdout + "\n" + s.stderr
}

// theOutputReportsTheRelease checks the last command's output for exactly
// one line about the release, of the given outcome, naming every instance
// listed (comma-separated) by its path.
func theOutputReportsTheRelease(ctx context.Context, target, slug, outcome, instances string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	mk, rel, err := commitTarget(ps, target, slug)
	if err != nil {
		return err
	}
	name := "github.com/" + mk.owner + "/" + mk.repo + "@" + rel.commit
	lead := map[string]string{
		"restored":                     "restored plugin release " + name + " for instances: ",
		"tampered":                     "plugin release " + name + " was changed after it was placed (tampered with); fetched it again for instances: ",
		"refused":                      "refused to restore plugin release " + name + ",",
		"not restored":                 "could not restore plugin release " + name + ",",
		"not restored after tampering": "could not restore plugin release " + name + ",",
	}[outcome]
	// A restore of a tampered release says so; a plain restore does not.
	saysTampered := outcome == "tampered" || outcome == "not restored after tampering"
	out := commandOutput(s)
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, lead) {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		return fmt.Errorf("%d lines report %s %s, want 1:\n%s", len(lines), name, outcome, out)
	}
	if strings.Contains(lines[0], "changed after it was placed") != saysTampered {
		return fmt.Errorf("the line about %s, %s, says it was tampered with: %v:\n%s", name, outcome, !saysTampered, lines[0])
	}
	for _, inst := range strings.Split(instances, ",") {
		path := filepath.Join(s.workspaceRoot, strings.TrimSpace(inst))
		if !regexp.MustCompile(regexp.QuoteMeta(path) + `(,|\.|:| and|$)`).MatchString(lines[0]) {
			return fmt.Errorf("the line does not name instance %s:\n%s", path, lines[0])
		}
	}
	for _, banned := range []string{"niwa apply", "re-run"} {
		if strings.Contains(lines[0], banned) {
			return fmt.Errorf("the line names %q:\n%s", banned, lines[0])
		}
	}
	return nil
}

// iRunInInstanceWithTarballsFailing runs the command with every tarball
// fetch failing before it reaches the fake, so nothing is logged for it.
func iRunInInstanceWithTarballsFailing(ctx context.Context, command, instance string) (context.Context, error) {
	s, _, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	s.envOverrides["NIWA_TEST_FAULT"] = "error:the network is down@fetch-tarball"
	defer delete(s.envOverrides, "NIWA_TEST_FAULT")
	return ctx, runNiwa(s, filepath.Join(s.workspaceRoot, instance), command)
}
