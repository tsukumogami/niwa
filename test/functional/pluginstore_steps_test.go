package functional

import (
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
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoDirs, _ := filepath.Glob(filepath.Join(instRoot, "*", repoName))
	if len(repoDirs) != 1 {
		return ctx, fmt.Errorf("repo %q in %s: found %v", repoName, instRoot, repoDirs)
	}
	ctx, err = iCallCreateWorktree(ctx, repoName, "skills-"+instance, instance)
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
