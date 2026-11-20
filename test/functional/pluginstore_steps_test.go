package functional

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cucumber/godog"
)

// The plugin-release-hold steps. A github marketplace is published twice over:
// as a bare repo on the local git server, whose tags `git ls-remote` of
// https://github.com/<owner>/<repo> reads through an insteadOf rewrite in the
// sandbox home's git config, and to the tarball fake, which answers the
// commit resolve for each tag and serves the tarball at each commit. So a
// default-track marketplace resolves its latest stable tag offline, and
// "downloaded" means a logged tarball request.

// pluginRelease is one published release of a marketplace.
type pluginRelease struct {
	tag, commit string
	commitTime  time.Time
}

// pluginMarketplace is one published github marketplace.
type pluginMarketplace struct {
	owner, repo string
	bare        string // the bare repo on the local git server
	work        string // a clone the releases are committed in
	releases    []pluginRelease
}

// pluginStoreScenario is what the plugin-release-hold scenarios remember.
type pluginStoreScenario struct {
	marketplaces map[string]*pluginMarketplace // "owner/repo"
	order        []string
	snapshots    map[string]string // path -> tree digest
	second       string            // the instance "the second instance" names
	started      []*startedCreate  // creates started in the background
	holdPath     string            // the hold: fault's release path
}

func pluginState(ctx context.Context) (*testState, *pluginStoreScenario, error) {
	s := getState(ctx)
	if s == nil {
		return nil, nil, fmt.Errorf("no test state")
	}
	if s.plugins == nil {
		s.plugins = &pluginStoreScenario{marketplaces: map[string]*pluginMarketplace{}, snapshots: map[string]string{}}
		// A held release is read-only, which the sandbox's removal can't get
		// through; hand the write bits back when the scenario ends.
		s.cleanups = append(s.cleanups, func() { restoreWriteBits(filepath.Join(s.homeDir, ".niwa")) })
	}
	return s, s.plugins, nil
}

func restoreWriteBits(dir string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o755)
		}
		return nil
	})
}

func registerPluginStoreSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the GitHub marketplace "([^"]*)" publishes release "([^"]*)"$`, theGitHubMarketplacePublishesRelease)
	ctx.Step(`^the GitHub fake log is cleared$`, theGitHubFakeLogIsCleared)
	ctx.Step(`^the GitHub fake served one tarball request for each marketplace, at the commit of its newest release$`, oneTarballRequestPerMarketplace)
	ctx.Step(`^the GitHub fake served no tarball request$`, noTarballRequest)
	ctx.Step(`^the GitHub fake served one commit-resolve request for each marketplace and nothing else for them$`, oneResolvePerMarketplaceOnly)
	ctx.Step(`^the marketplaces of instance "([^"]*)" are only links into the plugin store of workspace root "([^"]*)"$`, marketplacesAreOnlyLinks)
	ctx.Step(`^instance "([^"]*)" links "([^"]*)" to its release "([^"]*)"$`, instanceLinksMarketplaceToRelease)
	ctx.Step(`^every marketplace's skill resolves in instance "([^"]*)" at its root, in repo "([^"]*)" and in a session worktree$`, everySkillResolves)
	ctx.Step(`^the held releases are read-only and marked$`, theHeldReleasesAreReadOnlyAndMarked)
	ctx.Step(`^no file of a release tree exists under workspace root "([^"]*)"$`, noReleaseFileUnderRoot)
	ctx.Step(`^the workspace config of root "([^"]*)" carries no "([^"]*)" key$`, theWorkspaceConfigCarriesNoKey)
	ctx.Step(`^the sandbox home holds plugin stores under (\d+) workspace key directories?$`, theSandboxHomeHoldsPluginStores)
	ctx.Step(`^I link "([^"]*)" to workspace root "([^"]*)"$`, iLinkToWorkspaceRoot)
	ctx.Step(`^I run "([^"]*)" from the link "([^"]*)"$`, iRunFromTheLink)
	ctx.Step(`^the marketplace "([^"]*)" of instance "([^"]*)" is replaced by a real copy of its tree$`, theMarketplaceIsReplacedByARealCopy)
	ctx.Step(`^the marketplace "([^"]*)" of instance "([^"]*)" is the same real directory$`, theMarketplaceIsTheSameRealDirectory)
	ctx.Step(`^the marketplace "([^"]*)" of the workspace root is a real directory holding its manifest$`, theRootMarketplaceIsARealDirectory)
	ctx.Step(`^nothing for plugins exists under the sandbox home's \.niwa$`, nothingForPluginsUnderHome)
	ctx.Step(`^I make the second instance with (niwa create ws|niwa dispatch|the SessionStart hook)$`, iMakeTheSecondInstanceWith)
	ctx.Step(`^the marketplaces of the (second|newest) instance are only links into the plugin store of workspace root "([^"]*)"$`, marketplacesOfNamedInstanceAreOnlyLinks)
	ctx.Step(`^every marketplace's skill resolves in the (second|newest) instance at its root, in repo "([^"]*)" and in a session worktree$`, everySkillResolvesInNamedInstance)
	ctx.Step(`^I start (\d+) "([^"]*)" process(?:es)?, the first plugin store download held$`, iStartHeldCreates)
	ctx.Step(`^the held plugin store download is let go$`, theHeldDownloadIsLetGo)
	ctx.Step(`^the held create is killed$`, theHeldCreateIsKilled)
	ctx.Step(`^every started create exits 0$`, everyStartedCreateExitsZero)
	ctx.Step(`^the GitHub fake served (\d+) tarball requests? for each marketplace, at the commit of its newest release$`, nTarballRequestsPerMarketplace)
	ctx.Step(`^every instance of workspace root "([^"]*)" links each marketplace to the same release tree$`, everyInstanceLinksTheSameTree)
	ctx.Step(`^no marketplace has a release directory or a held\.json in the plugin store of workspace root "([^"]*)"$`, noMarketplaceReleaseDirectory)
	ctx.Step(`^the config of workspace root "([^"]*)" no longer tracks its source$`, theConfigNoLongerTracksItsSource)
	ctx.Step(`^the plugin store of workspace root "([^"]*)" holds no staging entry for any marketplace$`, noStagingEntryForAnyMarketplace)
}

// pluginRootDir is the workspace root a scenario names: "." for the suite's own
// root (niwa init without a name), otherwise a named root under it.
func pluginRootDir(s *testState, root string) string {
	if root == "." || root == "" {
		return s.workspaceRoot
	}
	return filepath.Join(s.workspaceRoot, root)
}

// workspaceKeyOf is the key niwa derives from a workspace root's real path,
// computed here on its own so a drift in niwa's definition fails a scenario.
func workspaceKeyOf(root string) (string, error) {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", root, err)
	}
	sum := sha256.Sum256([]byte(real))
	return "w-" + hex.EncodeToString(sum[:])[:32], nil
}

func skillSentinel(owner, repo, tag string) string {
	return "SENTINEL-" + owner + "-" + repo + "-" + tag
}

// theGitHubMarketplacePublishesRelease commits a new release of owner/repo,
// tags it, and serves it: the tag resolves to the commit at the fake, and the
// tarball is served at the commit and at the tag.
// Each marketplace declares one plugin, "<repo>-kit", with one skill,
// "<repo>-skill", and an executable script.
func theGitHubMarketplacePublishesRelease(ctx context.Context, slug, tag string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	if s.gitServer == nil {
		return ctx, fmt.Errorf("no local git server; call 'a local git server is set up' first")
	}
	if ctx, err = aGitHubFakeIsConfigured(ctx); err != nil {
		return ctx, err
	}
	owner, repo, err := splitOwnerRepo(slug)
	if err != nil {
		return ctx, err
	}
	mk := ps.marketplaces[slug]
	if mk == nil {
		bare, err := s.gitServer.SourceRepo(filepath.Join("github", owner, repo))
		if err != nil {
			return ctx, err
		}
		bare = strings.TrimPrefix(bare, "file://")
		work := filepath.Join(s.gitServer.root, "work-"+owner+"-"+repo)
		if out, err := fixtureGit(s.gitServer.root, "clone", "file://"+bare, work); err != nil {
			return ctx, fmt.Errorf("cloning %s: %w\n%s", bare, err, out)
		}
		mk = &pluginMarketplace{owner: owner, repo: repo, bare: bare, work: work}
		ps.marketplaces[slug] = mk
		ps.order = append(ps.order, slug)
		if err := writePluginGitConfig(s, ps); err != nil {
			return ctx, err
		}
	}

	sentinel := skillSentinel(owner, repo, tag)
	if err := os.WriteFile(filepath.Join(mk.work, "RELEASE"), []byte(sentinel+"\n"), 0o644); err != nil {
		return ctx, err
	}
	if out, err := fixtureGitCommit(mk.work, "add", "-A"); err != nil {
		return ctx, fmt.Errorf("git add: %w\n%s", err, out)
	}
	if out, err := fixtureGitCommit(mk.work, "commit", "-m", "release "+tag); err != nil {
		return ctx, fmt.Errorf("git commit: %w\n%s", err, out)
	}
	if out, err := fixtureGitCommit(mk.work, "tag", tag); err != nil {
		return ctx, fmt.Errorf("git tag: %w\n%s", err, out)
	}
	if out, err := fixtureGitCommit(mk.work, "push", "origin", "HEAD:main", "refs/tags/"+tag); err != nil {
		return ctx, fmt.Errorf("git push: %w\n%s", err, out)
	}
	out, err := fixtureGitWorkTree(mk.work, "rev-parse", "HEAD")
	if err != nil {
		return ctx, fmt.Errorf("git rev-parse: %w\n%s", err, out)
	}
	commit := strings.TrimSpace(string(out))

	// Each release is a second apart, so the newest by commit time is the
	// newest published.
	commitTime := time.Date(2026, 11, 2, 8, 41, 10, 0, time.UTC).Add(time.Duration(len(mk.releases)) * time.Second)
	wrapper := owner + "-" + repo + "-" + commit[:7]
	plugin := repo + "-kit"
	entries := map[string]string{
		wrapper + "/": "",
		wrapper + "/.claude-plugin/marketplace.json":                           fmt.Sprintf(`{"name":%q,"plugins":[{"name":%q,"source":"./plugins/kit"}]}`, repo, plugin),
		wrapper + "/plugins/kit/.claude-plugin/plugin.json":                    fmt.Sprintf(`{"name":%q,"version":"0.1.0"}`, plugin),
		wrapper + "/plugins/kit/skills/" + repo + "-skill/SKILL.md":            "---\nname: " + repo + "-skill\n---\n" + sentinel + "\n",
		wrapper + "/plugins/kit/skills/" + repo + "-skill/references/notes.md": "notes for " + sentinel + "\n",
		wrapper + "/plugins/kit/scripts/run.sh":                                "#!/bin/sh\necho " + sentinel + "\n",
	}
	s.githubFake.SetCommit(owner, repo, tag, commit)
	executables := map[string]bool{wrapper + "/plugins/kit/scripts/run.sh": true}
	s.githubFake.SetTarballAt(owner, repo, commit, entries, commitTime, executables)
	// The tag serves the same tarball for a copy fetched into an instance by
	// ref, as a layout without a workspace root still does. A store fetch by
	// tag shows in the log, which the request steps check by exact path.
	s.githubFake.SetTarballAt(owner, repo, tag, entries, commitTime, executables)
	mk.releases = append(mk.releases, pluginRelease{tag: tag, commit: commit, commitTime: commitTime})
	return ctx, nil
}

// writePluginGitConfig rewrites https://github.com/<owner>/<repo> to its bare
// repo on the local git server, for every published marketplace, in the
// sandbox home's git config: what niwa's `git ls-remote` reads.
func writePluginGitConfig(s *testState, ps *pluginStoreScenario) error {
	var b strings.Builder
	for _, slug := range ps.order {
		mk := ps.marketplaces[slug]
		fmt.Fprintf(&b, "[url \"file://%s\"]\n\tinsteadOf = https://github.com/%s/%s\n", mk.bare, mk.owner, mk.repo)
	}
	return os.WriteFile(filepath.Join(s.homeDir, ".gitconfig"), []byte(b.String()), 0o644)
}

func theGitHubFakeLogIsCleared(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil || s.githubFake == nil {
		return ctx, fmt.Errorf("no GitHub fake")
	}
	s.githubFake.ResetLog()
	return ctx, nil
}

// requestsFor is the fake's log for one source.
func requestsFor(s *testState, owner, repo string) []tarballFakeRequest {
	var out []tarballFakeRequest
	prefix := "/repos/" + owner + "/" + repo
	for _, r := range s.githubFake.Requests() {
		if r.Path == prefix || strings.HasPrefix(r.Path, prefix+"/") {
			out = append(out, r)
		}
	}
	return out
}

func describeRequests(rs []tarballFakeRequest) string {
	var paths []string
	for _, r := range rs {
		paths = append(paths, r.Method+" "+r.Path)
	}
	return strings.Join(paths, "\n")
}

func oneTarballRequestPerMarketplace(ctx context.Context) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	for _, slug := range ps.order {
		mk := ps.marketplaces[slug]
		newest := mk.releases[len(mk.releases)-1]
		var tarballs []string
		for _, r := range requestsFor(s, mk.owner, mk.repo) {
			if strings.Contains(r.Path, "/tarball/") {
				tarballs = append(tarballs, r.Path)
			}
		}
		want := "/repos/" + mk.owner + "/" + mk.repo + "/tarball/" + newest.commit
		if len(tarballs) != 1 || tarballs[0] != want {
			return fmt.Errorf("%s: tarball requests %v, want exactly [%s]", slug, tarballs, want)
		}
	}
	return nil
}

func noTarballRequest(ctx context.Context) error {
	s := getState(ctx)
	if s == nil || s.githubFake == nil {
		return fmt.Errorf("no GitHub fake")
	}
	if n := s.githubFake.CountRequests("/tarball/"); n != 0 {
		return fmt.Errorf("%d tarball requests, want none:\n%s", n, describeRequests(s.githubFake.Requests()))
	}
	return nil
}

func oneResolvePerMarketplaceOnly(ctx context.Context) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	for _, slug := range ps.order {
		mk := ps.marketplaces[slug]
		rs := requestsFor(s, mk.owner, mk.repo)
		newest := mk.releases[len(mk.releases)-1]
		want := "/repos/" + mk.owner + "/" + mk.repo + "/commits/" + newest.tag
		if len(rs) != 1 || rs[0].Path != want {
			return fmt.Errorf("%s: requests\n%s\nwant exactly GET %s", slug, describeRequests(rs), want)
		}
	}
	return nil
}

// releaseLinkPattern is a link into a store: .../plugins/github.com/<o>/<r>/<c>/tree.
var releaseLinkPattern = regexp.MustCompile(`^(.*)/\.niwa/(w-[0-9a-f]{32})/plugins/github\.com/([^/]+)/([^/]+)/([0-9a-f]{40})/tree$`)

func marketplacesAreOnlyLinks(ctx context.Context, instance, root string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	key, err := workspaceKeyOf(pluginRootDir(s, root))
	if err != nil {
		return err
	}
	dir := filepath.Join(pluginRootDir(s, root), instance, ".niwa", "marketplaces")
	var files []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(files) != 0 {
		return fmt.Errorf("regular files under %s: %v", dir, files)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != len(ps.order) {
		return fmt.Errorf("%s holds %d entries, want %d", dir, len(entries), len(ps.order))
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		target, err := os.Readlink(p)
		if err != nil {
			return fmt.Errorf("%s is not a link: %w", p, err)
		}
		m := releaseLinkPattern.FindStringSubmatch(target)
		if m == nil || m[1] != s.homeDir || m[2] != key {
			return fmt.Errorf("%s -> %s, want an absolute target under %s/.niwa/%s/plugins/github.com/", p, target, s.homeDir, key)
		}
	}
	out, err := exec.Command("du", "-sk", dir).Output()
	if err != nil {
		return fmt.Errorf("du -sk %s: %w", dir, err)
	}
	kb, err := strconv.Atoi(strings.Fields(string(out))[0])
	if err != nil || kb >= 1024 {
		return fmt.Errorf("du -sk %s = %q, want under 1024", dir, strings.TrimSpace(string(out)))
	}
	return nil
}

func instanceLinksMarketplaceToRelease(ctx context.Context, instance, slug, tag string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	mk := ps.marketplaces[slug]
	if mk == nil {
		return fmt.Errorf("no published marketplace %s", slug)
	}
	var commit string
	for _, r := range mk.releases {
		if r.tag == tag {
			commit = r.commit
		}
	}
	if commit == "" {
		return fmt.Errorf("%s has no release %s", slug, tag)
	}
	link := filepath.Join(s.workspaceRoot, instance, ".niwa", "marketplaces", mk.repo)
	target, err := os.Readlink(link)
	if err != nil {
		return fmt.Errorf("%s: %w", link, err)
	}
	if m := releaseLinkPattern.FindStringSubmatch(target); m == nil || m[5] != commit {
		return fmt.Errorf("%s -> %s, want the commit of %s %s (%s)", link, target, slug, tag, commit)
	}
	return nil
}

// everySkillResolves reads each marketplace's skill through the delivered
// links: at the instance root, in the repo, and in a session worktree made for
// the check. A link that leads nowhere fails the read.
func everySkillResolves(ctx context.Context, instance, repoName string) (context.Context, error) {
	return skillsResolveIn(ctx, instance, repoName, "skills-"+instance)
}

// skillsResolveIn is everySkillResolves with the session worktree's purpose
// given, for an instance whose name is no valid purpose (a dispatched
// instance's name carries a "+").
func skillsResolveIn(ctx context.Context, instance, repoName, purpose string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoDirs, _ := filepath.Glob(filepath.Join(instRoot, "*", repoName))
	if len(repoDirs) != 1 {
		return ctx, fmt.Errorf("repo %q in %s: found %v", repoName, instRoot, repoDirs)
	}
	ctx, err = iCallCreateWorktree(ctx, repoName, purpose, instance)
	if err != nil {
		return ctx, err
	}
	places := []string{instRoot, repoDirs[0], s.lastSessionWorktreePath}
	for _, slug := range ps.order {
		mk := ps.marketplaces[slug]
		for _, place := range places {
			skill := filepath.Join(place, ".codex", "skills", mk.repo+"-kit", "skills", mk.repo+"-skill", "SKILL.md")
			data, err := os.ReadFile(skill)
			if err != nil {
				return ctx, fmt.Errorf("%s: skill not readable: %w", slug, err)
			}
			if !strings.Contains(string(data), "SENTINEL-"+mk.owner+"-"+mk.repo+"-") {
				return ctx, fmt.Errorf("%s holds %q, not %s's skill", skill, string(data), slug)
			}
		}
	}
	return ctx, nil
}

// theHeldReleasesAreReadOnlyAndMarked checks every held release in the
// sandbox home: the tree has no write bit, held.json is 0444 and names the
// source, the ref, the commit and the archive's modification time, and the
// levels niwa made under plugins/ are 0700.
func theHeldReleasesAreReadOnlyAndMarked(ctx context.Context) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	stores, _ := filepath.Glob(filepath.Join(s.homeDir, ".niwa", "w-*", "plugins"))
	if len(stores) == 0 {
		return fmt.Errorf("no plugin store under %s/.niwa", s.homeDir)
	}
	held := 0
	for _, store := range stores {
		for _, slug := range ps.order {
			mk := ps.marketplaces[slug]
			for _, rel := range mk.releases {
				rdir := filepath.Join(store, "github.com", mk.owner, mk.repo, rel.commit)
				if _, err := os.Lstat(rdir); os.IsNotExist(err) {
					continue
				}
				held++
				if err := checkHeldRelease(rdir, mk, rel); err != nil {
					return err
				}
				for _, level := range []string{store, filepath.Join(store, "github.com"), filepath.Join(store, "github.com", mk.owner), filepath.Join(store, "github.com", mk.owner, mk.repo), rdir} {
					info, err := os.Lstat(level)
					if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
						return fmt.Errorf("%s: want a 0700 directory, got %v (%v)", level, info.Mode(), err)
					}
				}
			}
		}
	}
	if held == 0 {
		return fmt.Errorf("no held release found")
	}
	return nil
}

func checkHeldRelease(rdir string, mk *pluginMarketplace, rel pluginRelease) error {
	err := filepath.WalkDir(filepath.Join(rdir, "tree"), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		perm := info.Mode().Perm()
		switch {
		case info.IsDir():
			if perm != 0o555 {
				return fmt.Errorf("%s: directory mode %04o, want 0555", p, perm)
			}
		case strings.HasSuffix(p, "/scripts/run.sh"):
			if perm != 0o555 {
				return fmt.Errorf("%s: executable mode %04o, want 0555", p, perm)
			}
		case info.Mode().IsRegular():
			if perm != 0o444 {
				return fmt.Errorf("%s: file mode %04o, want 0444", p, perm)
			}
		default:
			return fmt.Errorf("%s: unexpected entry type %s", p, info.Mode().Type())
		}
		return nil
	})
	if err != nil {
		return err
	}
	markPath := filepath.Join(rdir, "held.json")
	info, err := os.Lstat(markPath)
	if err != nil || info.Mode().Perm() != 0o444 {
		return fmt.Errorf("%s: want mode 0444 (%v)", markPath, err)
	}
	data, err := os.ReadFile(markPath)
	if err != nil {
		return err
	}
	var mark struct {
		Source     string `json:"source"`
		Ref        string `json:"ref"`
		Commit     string `json:"commit"`
		CommitTime string `json:"commit_time"`
	}
	if err := json.Unmarshal(data, &mark); err != nil {
		return fmt.Errorf("%s: %w", markPath, err)
	}
	if mark.Source != mk.owner+"/"+mk.repo || mark.Ref != rel.tag || mark.Commit != rel.commit || mark.CommitTime != rel.commitTime.Format(time.RFC3339) {
		return fmt.Errorf("%s = %s, want source %s/%s, ref %s, commit %s, commit_time %s", markPath, data, mk.owner, mk.repo, rel.tag, rel.commit, rel.commitTime.Format(time.RFC3339))
	}
	return nil
}

// noReleaseFileUnderRoot walks the workspace root without following a link
// and fails on any regular file carrying a release's sentinel: the release
// trees live in the host store only.
func noReleaseFileUnderRoot(ctx context.Context, root string) error {
	s, _, err := pluginState(ctx)
	if err != nil {
		return err
	}
	return filepath.WalkDir(pluginRootDir(s, root), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if strings.Contains(string(data), "SENTINEL-") {
			return fmt.Errorf("a release file is at %s", p)
		}
		return nil
	})
}

func theWorkspaceConfigCarriesNoKey(ctx context.Context, root, key string) error {
	s, _, err := pluginState(ctx)
	if err != nil {
		return err
	}
	path := filepath.Join(pluginRootDir(s, root), ".niwa", "workspace.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(string(data), key) {
		return fmt.Errorf("%s carries %q", path, key)
	}
	return nil
}

func theSandboxHomeHoldsPluginStores(ctx context.Context, n int) error {
	s, _, err := pluginState(ctx)
	if err != nil {
		return err
	}
	stores, _ := filepath.Glob(filepath.Join(s.homeDir, ".niwa", "w-*", "plugins"))
	if len(stores) != n {
		return fmt.Errorf("plugin stores %v, want %d", stores, n)
	}
	return nil
}

func iLinkToWorkspaceRoot(ctx context.Context, link, root string) (context.Context, error) {
	s, _, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	return ctx, os.Symlink(pluginRootDir(s, root), filepath.Join(s.sandbox, link))
}

func iRunFromTheLink(ctx context.Context, command, link string) (context.Context, error) {
	s, _, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	return ctx, runNiwa(s, filepath.Join(s.sandbox, link), command)
}

// treeDigest hashes every path, mode and file content under dir, without
// following a link.
func treeDigest(dir string) (string, error) {
	h := sha256.New()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, p)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, p := range paths {
		info, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s %s\n", strings.TrimPrefix(p, dir), info.Mode())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return "", err
			}
			h.Write(data)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// theMarketplaceIsReplacedByARealCopy turns an instance's link into the real
// directory an instance made before the store would hold: a writable copy of
// the tree the link names.
func theMarketplaceIsReplacedByARealCopy(ctx context.Context, name, instance string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	link := filepath.Join(s.workspaceRoot, instance, ".niwa", "marketplaces", name)
	target, err := os.Readlink(link)
	if err != nil {
		return ctx, err
	}
	if err := os.Remove(link); err != nil {
		return ctx, err
	}
	if out, err := exec.Command("cp", "-R", target, link).CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("cp: %w\n%s", err, out)
	}
	restoreWriteBits(link)
	digest, err := treeDigest(link)
	if err != nil {
		return ctx, err
	}
	ps.snapshots[link] = digest
	return ctx, nil
}

func theMarketplaceIsTheSameRealDirectory(ctx context.Context, name, instance string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.workspaceRoot, instance, ".niwa", "marketplaces", name)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%s is no longer a real directory (%v)", dir, err)
	}
	digest, err := treeDigest(dir)
	if err != nil {
		return err
	}
	if digest != ps.snapshots[dir] {
		return fmt.Errorf("%s changed", dir)
	}
	return nil
}

func theRootMarketplaceIsARealDirectory(ctx context.Context, name string) error {
	s, _, err := pluginState(ctx)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.workspaceRoot, ".niwa", "marketplaces", name)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%s: want a real directory (%v)", dir, err)
	}
	manifest := filepath.Join(dir, ".claude-plugin", "marketplace.json")
	if info, err := os.Lstat(manifest); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%s: want a regular file (%v)", manifest, err)
	}
	return nil
}

func nothingForPluginsUnderHome(ctx context.Context) error {
	s, _, err := pluginState(ctx)
	if err != nil {
		return err
	}
	for _, pattern := range []string{
		filepath.Join(s.homeDir, ".niwa", "w-*", "plugins"),
		filepath.Join(s.homeDir, ".niwa", "locks", "p-*.lock"),
	} {
		if found, _ := filepath.Glob(pattern); len(found) != 0 {
			return fmt.Errorf("found %v under the sandbox home", found)
		}
	}
	return nil
}

// The session ids the second-instance rows use.
const (
	pluginDispatchSessionID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	pluginHookSessionID     = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
)

// instanceNames lists the instances under the workspace root: the
// directories holding a .niwa/instance.json.
func instanceNames(s *testState) (map[string]time.Time, error) {
	found, err := filepath.Glob(filepath.Join(s.workspaceRoot, "*", ".niwa", "instance.json"))
	if err != nil {
		return nil, err
	}
	names := map[string]time.Time{}
	for _, f := range found {
		info, err := os.Stat(f)
		if err != nil {
			return nil, err
		}
		names[filepath.Base(filepath.Dir(filepath.Dir(f)))] = info.ModTime()
	}
	return names, nil
}

// installPluginDispatchFakeClaude puts one claude on PATH that answers both
// what a dispatch launches (the dispatch suite's fake) and the plugin
// pre-warm's `claude plugin` calls (the pre-warm fake), so a dispatched
// instance pre-warms the way a created one does.
func installPluginDispatchFakeClaude(s *testState, sessionID string) error {
	binDir := filepath.Join(s.homeDir, "fake-claude-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	scripts := map[string]string{
		"claude-dispatch": dispatchFakeClaudeScript("ok"),
		"claude-prewarm":  prewarmFakeClaudeScript(),
		"claude": `#!/bin/sh
dir=$(dirname "$0")
if [ "$1" = "plugin" ]; then
  exec "$dir/claude-prewarm" "$@"
fi
exec "$dir/claude-dispatch" "$@"
`,
	}
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(body), 0o755); err != nil {
			return err
		}
	}
	s.pathPrefix = binDir
	s.envOverrides["FAKE_CLAUDE_SESSION_ID"] = sessionID
	return nil
}

// iMakeTheSecondInstanceWith makes one more instance of the workspace by the
// row's path and remembers which one it is: the instance that wasn't there
// before.
func iMakeTheSecondInstanceWith(ctx context.Context, how string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	before, err := instanceNames(s)
	if err != nil {
		return ctx, err
	}
	switch how {
	case "niwa create ws":
		err = runNiwa(s, s.homeDir, "niwa create ws")
	case "niwa dispatch":
		if err = installPluginDispatchFakeClaude(s, pluginDispatchSessionID); err == nil {
			err = runNiwa(s, s.workspaceRoot, "niwa dispatch plugin-task --detach")
		}
	case "the SessionStart hook":
		if ctx, err = aBackgroundJobStateForSession(ctx, pluginHookSessionID); err == nil {
			ctx, err = iPipeASessionStartHookForSession(ctx, pluginHookSessionID)
		}
	default:
		err = fmt.Errorf("unknown way to make an instance %q", how)
	}
	if err != nil {
		return ctx, err
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("%s exit=%d\nstdout:\n%s\nstderr:\n%s", how, s.exitCode, s.stdout, s.stderr)
	}
	after, err := instanceNames(s)
	if err != nil {
		return ctx, err
	}
	var made []string
	for name := range after {
		if _, ok := before[name]; !ok {
			made = append(made, name)
		}
	}
	if len(made) != 1 {
		return ctx, fmt.Errorf("%s made instances %v, want exactly one", how, made)
	}
	ps.second = made[0]
	return ctx, nil
}

// namedInstance resolves "second" to the instance the second-instance step
// made, and "newest" to the instance whose instance.json was written last.
func namedInstance(s *testState, ps *pluginStoreScenario, which string) (string, error) {
	if which == "second" {
		if ps.second == "" {
			return "", fmt.Errorf("no second instance was made")
		}
		return ps.second, nil
	}
	names, err := instanceNames(s)
	if err != nil {
		return "", err
	}
	newest, at := "", time.Time{}
	for name, t := range names {
		if newest == "" || t.After(at) {
			newest, at = name, t
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no instance under %s", s.workspaceRoot)
	}
	return newest, nil
}

func marketplacesOfNamedInstanceAreOnlyLinks(ctx context.Context, which, root string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	instance, err := namedInstance(s, ps, which)
	if err != nil {
		return err
	}
	return marketplacesAreOnlyLinks(ctx, instance, root)
}

func everySkillResolvesInNamedInstance(ctx context.Context, which, repoName string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	instance, err := namedInstance(s, ps, which)
	if err != nil {
		return ctx, err
	}
	return skillsResolveIn(ctx, instance, repoName, "skills-"+which)
}

// startedCreate is a create running in the background.
type startedCreate struct {
	cmd            *exec.Cmd
	stdout, stderr bytes.Buffer
	done           chan error
}

// iStartHeldCreates starts n creates as separate processes, each with the
// plugin-store-download fault set to hold: the first to reach a download
// writes <hold>.held and waits there until <hold> exists, holding that
// release's lock, so the others queue on it. It returns once a download is
// held and every process has had a moment to reach the lock.
func iStartHeldCreates(ctx context.Context, n int, command string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	ps.holdPath = filepath.Join(s.sandbox, "plugin-store-download-hold")
	s.envOverrides["NIWA_TEST_FAULT"] = "hold:" + ps.holdPath + "@plugin-store-download"
	env := s.buildEnv()
	delete(s.envOverrides, "NIWA_TEST_FAULT")
	// A create left running by a failing step would outlive the scenario.
	s.cleanups = append(s.cleanups, func() {
		_ = os.WriteFile(ps.holdPath, nil, 0o600)
		for _, c := range ps.started {
			_ = c.cmd.Process.Kill()
		}
	})

	args := strings.Fields(command)
	if len(args) == 0 || args[0] != "niwa" {
		return ctx, fmt.Errorf("not a niwa command: %q", command)
	}
	for i := 0; i < n; i++ {
		c := &startedCreate{done: make(chan error, 1)}
		c.cmd = exec.Command(s.binPath, args[1:]...)
		c.cmd.Dir = s.homeDir
		c.cmd.Env = env
		c.cmd.Stdout, c.cmd.Stderr = &c.stdout, &c.stderr
		if err := c.cmd.Start(); err != nil {
			return ctx, fmt.Errorf("starting %s: %w", command, err)
		}
		go func() { c.done <- c.cmd.Wait() }()
		ps.started = append(ps.started, c)
	}

	deadline := time.Now().Add(60 * time.Second)
	for {
		if _, err := os.Stat(ps.holdPath + ".held"); err == nil {
			break
		}
		for i, c := range ps.started {
			select {
			case err := <-c.done:
				c.done <- err
				return ctx, fmt.Errorf("create %d ended (%v) before any download was held\nstderr:\n%s", i+1, err, c.stderr.String())
			default:
			}
		}
		if time.Now().After(deadline) {
			return ctx, fmt.Errorf("no plugin store download was held within 60s")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The held process holds the release's lock; give the others time to
	// come up against it.
	time.Sleep(time.Second)
	for i, c := range ps.started {
		select {
		case err := <-c.done:
			c.done <- err
			return ctx, fmt.Errorf("create %d ended (%v) while a download was held\nstderr:\n%s", i+1, err, c.stderr.String())
		default:
		}
	}
	return ctx, nil
}

func theHeldDownloadIsLetGo(ctx context.Context) error {
	_, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	return os.WriteFile(ps.holdPath, nil, 0o600)
}

// theHeldCreateIsKilled kills the one started create with SIGKILL while its
// download is held, so nothing of niwa's own runs after it.
func theHeldCreateIsKilled(ctx context.Context) error {
	_, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	if len(ps.started) != 1 {
		return fmt.Errorf("%d creates started, want one to kill", len(ps.started))
	}
	c := ps.started[0]
	if err := c.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		return err
	}
	err = <-c.done
	c.done <- err
	return nil
}

func everyStartedCreateExitsZero(ctx context.Context) error {
	_, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	for i, c := range ps.started {
		select {
		case err := <-c.done:
			c.done <- err
			if err != nil {
				return fmt.Errorf("create %d: %v\nstdout:\n%s\nstderr:\n%s", i+1, err, c.stdout.String(), c.stderr.String())
			}
		case <-time.After(3 * time.Minute):
			return fmt.Errorf("create %d did not finish within 3 minutes", i+1)
		}
	}
	return nil
}

// nTarballRequestsPerMarketplace counts each marketplace's tarball requests
// in the fake's log and reports them; every one must be at the commit of the
// newest release.
func nTarballRequestsPerMarketplace(ctx context.Context, want int) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	var report []string
	for _, slug := range ps.order {
		mk := ps.marketplaces[slug]
		newest := mk.releases[len(mk.releases)-1]
		at := "/repos/" + mk.owner + "/" + mk.repo + "/tarball/" + newest.commit
		count := 0
		for _, r := range requestsFor(s, mk.owner, mk.repo) {
			if !strings.Contains(r.Path, "/tarball/") {
				continue
			}
			if r.Path != at {
				return fmt.Errorf("%s: tarball request %s, want only %s", slug, r.Path, at)
			}
			count++
		}
		report = append(report, fmt.Sprintf("%s: %d", slug, count))
		if count != want {
			return fmt.Errorf("%s: %d tarball requests, want %d", slug, count, want)
		}
	}
	fmt.Printf("tarball requests per release: %s\n", strings.Join(report, ", "))
	return nil
}

// everyInstanceLinksTheSameTree checks that every instance under the root
// links each marketplace to the newest release's tree in one store, and that
// the tree it links holds the manifest.
func everyInstanceLinksTheSameTree(ctx context.Context, root string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	dirs, err := filepath.Glob(filepath.Join(pluginRootDir(s, root), "*", ".niwa", "instance.json"))
	if err != nil {
		return err
	}
	if len(dirs) < 2 {
		return fmt.Errorf("found %d instances under %s, want several", len(dirs), pluginRootDir(s, root))
	}
	for _, slug := range ps.order {
		mk := ps.marketplaces[slug]
		newest := mk.releases[len(mk.releases)-1]
		want := ""
		for _, f := range dirs {
			instance := filepath.Dir(filepath.Dir(f))
			link := filepath.Join(instance, ".niwa", "marketplaces", mk.repo)
			target, err := os.Readlink(link)
			if err != nil {
				return fmt.Errorf("%s: %w", link, err)
			}
			if m := releaseLinkPattern.FindStringSubmatch(target); m == nil || m[5] != newest.commit {
				return fmt.Errorf("%s -> %s, want the tree of %s at %s", link, target, slug, newest.commit)
			}
			if want == "" {
				want = target
			} else if target != want {
				return fmt.Errorf("%s -> %s, another instance links %s", link, target, want)
			}
		}
		if info, err := os.Lstat(filepath.Join(want, ".claude-plugin", "marketplace.json")); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%s holds no manifest (%v)", want, err)
		}
	}
	return nil
}

// pluginStoreDirOf is the plugins directory of the root's store.
func pluginStoreDirOf(s *testState, root string) (string, error) {
	key, err := workspaceKeyOf(pluginRootDir(s, root))
	if err != nil {
		return "", err
	}
	return filepath.Join(s.homeDir, ".niwa", key, "plugins"), nil
}

// noMarketplaceReleaseDirectory checks that no published release of any
// marketplace has a <commit> directory or a held.json in the store.
func noMarketplaceReleaseDirectory(ctx context.Context, root string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	store, err := pluginStoreDirOf(s, root)
	if err != nil {
		return err
	}
	for _, slug := range ps.order {
		mk := ps.marketplaces[slug]
		for _, rel := range mk.releases {
			dir := filepath.Join(store, "github.com", mk.owner, mk.repo, rel.commit)
			for _, p := range []string{dir, filepath.Join(dir, "held.json")} {
				if _, err := os.Lstat(p); !os.IsNotExist(err) {
					return fmt.Errorf("%s exists after the create was killed (%v)", p, err)
				}
			}
		}
	}
	return nil
}

// noStagingEntryForAnyMarketplace checks the store's .tmp/ for an entry of any
// published release, named by the first 32 hex of sha256 of its id.
func noStagingEntryForAnyMarketplace(ctx context.Context, root string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	store, err := pluginStoreDirOf(s, root)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(store, ".tmp"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, slug := range ps.order {
		mk := ps.marketplaces[slug]
		for _, rel := range mk.releases {
			sum := sha256.Sum256([]byte("github.com/" + mk.owner + "/" + mk.repo + "/" + rel.commit))
			prefix := hex.EncodeToString(sum[:])[:32] + "-"
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), prefix) {
					return fmt.Errorf("staging entry %s of %s %s is left in %s/.tmp", e.Name(), slug, rel.tag, store)
				}
			}
		}
	}
	return nil
}

// theConfigNoLongerTracksItsSource removes the config snapshot's provenance
// marker, so no command re-materializes the config from its source: what a
// workspace scaffolded in place holds.
func theConfigNoLongerTracksItsSource(ctx context.Context, root string) (context.Context, error) {
	s, _, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	marker := filepath.Join(pluginRootDir(s, root), ".niwa", ".niwa-snapshot.toml")
	if err := os.Remove(marker); err != nil {
		return ctx, fmt.Errorf("removing %s: %w", marker, err)
	}
	return ctx, nil
}
