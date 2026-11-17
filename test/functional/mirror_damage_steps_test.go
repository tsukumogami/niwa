package functional

// Steps for the damaged-mirror outline in features/mirror-repair.feature: the
// state file and its pack manifest, the mirror log, and the four ways a
// scenario damages a store. Like the steps beside them they read the layout
// the design fixes rather than importing niwa's mirror package.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/cucumber/godog"
	"golang.org/x/sys/unix"
)

func registerMirrorDamageSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the state file of "([^"]*)" records the linked store and its packs$`, theStateFileRecordsTheLinkedStore)
	ctx.Step(`^I count the mirror log lines$`, iCountTheMirrorLogLines)
	ctx.Step(`^I damage the store of "([^"]*)" by (emptying its object store|deleting its pack|truncating its pack to half|deleting its HEAD)$`, iDamageTheStore)
	ctx.Step(`^the error output carries exactly the mirror warning "([^"]*)"$`, theErrorOutputCarriesExactlyTheMirrorWarning)
	ctx.Step(`^the mirror log gained a check line and a restore line for "([^"]*)" with fault "([^"]*)" and result "([^"]*)"$`, theMirrorLogGainedCheckAndRestore)
	ctx.Step(`^the mirror log gained a healthy check line and a refresh line for "([^"]*)" from "([^"]*)"$`, theMirrorLogGainedAHealthyCheckAndARefresh)
	ctx.Step(`^the mirror link of "([^"]*)" moved to a new store$`, theMirrorLinkMoved)
	ctx.Step(`^every ref of each borrower of "([^"]*)" reads to the root and fsck is clean$`, everyRefReadsToTheRoot)
	ctx.Step(`^only the linked store of "([^"]*)" remains$`, onlyTheLinkedStoreRemains)
	ctx.Step(`^I remember the state file of "([^"]*)"$`, iRememberTheStateFile)
	ctx.Step(`^the state file of "([^"]*)" is unchanged$`, theStateFileIsUnchanged)
	ctx.Step(`^the state file of "([^"]*)" is unchanged apart from last_refresh$`, theStateFileIsUnchangedApartFromLastRefresh)
}

// mirrorStatePath is the mirror's state file, .meta/<id>.json.
func mirrorStatePath(s *testState, repoID string) (string, error) {
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".meta", repoID+".json"), nil
}

// mirrorLogPath is the mirror log, .meta/mirror.log.
func mirrorLogPath(s *testState) (string, error) {
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ".meta", "mirror.log"), nil
}

// stateFile is the part of the state file the scenarios check.
type stateFile struct {
	Schema int    `json:"schema"`
	RepoID string `json:"repo_id"`
	URL    string `json:"url"`
	Store  string `json:"store"`
	Packs  []struct {
		Name    string `json:"name"`
		Size    int64  `json:"size"`
		CtimeNs int64  `json:"ctime_ns"`
		Trailer string `json:"trailer"`
	} `json:"packs"`
}

// repoStateFilePattern is a state file's name: a repo id, as the design
// allows one, then ".json".
var repoStateFilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.json$`)

// metaEntryAllowed reports whether name may sit in .meta/: a repo's state
// file, the mirror log, or the one older generation the log rotates to.
func metaEntryAllowed(name string) bool {
	return name == "mirror.log" || name == "mirror.log.1" || repoStateFilePattern.MatchString(name)
}

// requireMode fails unless path is the kind of entry want says with exactly
// perm.
func requireMode(path string, dir bool, perm os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if dir && !info.IsDir() || !dir && !info.Mode().IsRegular() || info.Mode().Perm() != perm {
		return fmt.Errorf("%s has mode %s, want a %s with mode %#o", path, info.Mode(), map[bool]string{true: "directory", false: "regular file"}[dir], perm)
	}
	return nil
}

func theStateFileRecordsTheLinkedStore(ctx context.Context, repoID string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	path, err := mirrorStatePath(s, repoID)
	if err != nil {
		return err
	}
	meta := filepath.Dir(path)
	if err := requireMode(meta, true, 0o700); err != nil {
		return err
	}
	if err := requireMode(path, false, 0o600); err != nil {
		return err
	}
	entries, err := os.ReadDir(meta)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !metaEntryAllowed(e.Name()) {
			return fmt.Errorf(".meta holds %q, want only <repo-id>.json files, mirror.log and mirror.log.1", e.Name())
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var st stateFile
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("decoding the state file: %w\n%s", err, data)
	}
	stable, err := mirrorStablePath(s, repoID)
	if err != nil {
		return err
	}
	target, err := os.Readlink(stable)
	if err != nil {
		return err
	}
	if st.Schema != 1 || st.RepoID != repoID || st.Store != target {
		return fmt.Errorf("state file = schema %d, repo_id %q, store %q; want 1, %q, the link target %q", st.Schema, st.RepoID, st.Store, repoID, target)
	}
	if strings.ContainsAny(st.URL, "?#@") {
		return fmt.Errorf("the state file's url %q carries userinfo, a query or a fragment", st.URL)
	}

	store := filepath.Join(filepath.Dir(stable), target)
	packs, err := filepath.Glob(filepath.Join(store, "objects", "pack", "*.pack"))
	if err != nil {
		return err
	}
	sort.Strings(packs)
	if len(packs) == 0 || len(packs) != len(st.Packs) {
		return fmt.Errorf("the manifest lists %d packs, the store holds %d", len(st.Packs), len(packs))
	}
	for i, pack := range packs {
		var sys unix.Stat_t
		if err := unix.Lstat(pack, &sys); err != nil {
			return err
		}
		body, err := os.ReadFile(pack)
		if err != nil {
			return err
		}
		got := st.Packs[i]
		if got.Name != filepath.Base(pack) || got.Size != int64(len(body)) || got.CtimeNs != sys.Ctim.Nano() ||
			got.Trailer != hex.EncodeToString(body[len(body)-20:]) {
			return fmt.Errorf("manifest entry %+v does not match %s (size %d, ctime %d, trailer %s)",
				got, pack, len(body), sys.Ctim.Nano(), hex.EncodeToString(body[len(body)-20:]))
		}
	}
	return nil
}

// readMirrorLog returns the mirror log's lines, each decoded as one JSON
// object.
func readMirrorLog(s *testState) ([]map[string]any, error) {
	path, err := mirrorLogPath(s)
	if err != nil {
		return nil, err
	}
	if err := requireMode(path, false, 0o600); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lines []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		var line map[string]any
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			return nil, fmt.Errorf("mirror log line %q is not one JSON object: %w", sc.Text(), err)
		}
		lines = append(lines, line)
	}
	return lines, sc.Err()
}

func iCountTheMirrorLogLines(ctx context.Context) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	lines, err := readMirrorLog(s)
	if err != nil {
		return ctx, err
	}
	m.logLines = len(lines)
	return ctx, nil
}

// newLogLines returns the lines logged since the last count.
func newLogLines(s *testState, m *mirrorScenario) ([]map[string]any, error) {
	lines, err := readMirrorLog(s)
	if err != nil {
		return nil, err
	}
	if len(lines) < m.logLines {
		return nil, fmt.Errorf("the mirror log shrank from %d to %d lines", m.logLines, len(lines))
	}
	return lines[m.logLines:], nil
}

// requireLogLine checks one log line's fields, command among them.
func requireLogLine(line map[string]any, command, repoID, event, fault, result string) error {
	for _, key := range []string{"time", "command", "mirror", "event", "fault", "result", "check_ms"} {
		if _, ok := line[key]; !ok {
			return fmt.Errorf("log line %v has no %q", line, key)
		}
	}
	_, took := line["took_ms"]
	if took != (event == "restore" || event == "refresh") {
		return fmt.Errorf("log line %v: took_ms present = %v on a %s line", line, took, event)
	}
	if line["command"] != command || line["mirror"] != repoID || line["event"] != event || line["fault"] != fault || line["result"] != result {
		return fmt.Errorf("log line %v, want command %q, mirror %q, event %q, fault %q, result %q", line, command, repoID, event, fault, result)
	}
	return nil
}

// theMirrorLogGainedCheckAndRestore is the log step of a scenario that runs
// niwa apply.
func theMirrorLogGainedCheckAndRestore(ctx context.Context, repoID, fault, result string) error {
	return theMirrorLogGainedCheckAndRestoreFrom(ctx, repoID, fault, result, "apply")
}

// theMirrorLogGainedAHealthyCheckAndARefresh is what a command that builds
// instances logs for a healthy mirror: its check line, then the refresh that
// brought the mirror up to date.
func theMirrorLogGainedAHealthyCheckAndARefresh(ctx context.Context, repoID, command string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	if len(lines) != 2 {
		return fmt.Errorf("the mirror log gained %d lines, want a healthy check line and a refresh line: %v", len(lines), lines)
	}
	if err := requireLogLine(lines[0], command, repoID, "check", "", "healthy"); err != nil {
		return err
	}
	return requireLogLine(lines[1], command, repoID, "refresh", "", "healthy")
}

// iDamageTheStore damages the linked store one way. Pack files are written
// read-only by git, so truncating one needs a chmod first; deleting needs
// only the directory's write bit.
func iDamageTheStore(ctx context.Context, repoID, how string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return ctx, err
	}
	packs, err := filepath.Glob(filepath.Join(store, "objects", "pack", "*.pack"))
	if err != nil {
		return ctx, err
	}
	switch how {
	case "emptying its object store":
		entries, err := os.ReadDir(filepath.Join(store, "objects"))
		if err != nil {
			return ctx, err
		}
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(store, "objects", e.Name())); err != nil {
				return ctx, err
			}
		}
	case "deleting its pack":
		if len(packs) != 1 {
			return ctx, fmt.Errorf("the store holds %d packs, want 1", len(packs))
		}
		if err := os.Remove(packs[0]); err != nil {
			return ctx, err
		}
		if _, err := os.Lstat(strings.TrimSuffix(packs[0], ".pack") + ".idx"); err != nil {
			return ctx, fmt.Errorf("the pack's .idx should be left in place: %w", err)
		}
	case "truncating its pack to half":
		if len(packs) != 1 {
			return ctx, fmt.Errorf("the store holds %d packs, want 1", len(packs))
		}
		info, err := os.Stat(packs[0])
		if err != nil {
			return ctx, err
		}
		if err := os.Chmod(packs[0], 0o644); err != nil {
			return ctx, err
		}
		if err := os.Truncate(packs[0], info.Size()/2); err != nil {
			return ctx, err
		}
	case "deleting its HEAD":
		if err := os.Remove(filepath.Join(store, "HEAD")); err != nil {
			return ctx, err
		}
	default:
		return ctx, fmt.Errorf("unknown damage %q", how)
	}
	return ctx, nil
}

func theErrorOutputCarriesExactlyTheMirrorWarning(ctx context.Context, want string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines := mirrorWarnings(s.stderr)
	if len(lines) != 1 {
		return fmt.Errorf("stderr carries %d mirror warnings, want 1:\n%s", len(lines), s.stderr)
	}
	if !strings.Contains(lines[0], "warning: "+want) {
		return fmt.Errorf("mirror warning %q, want %q", lines[0], "warning: "+want)
	}
	if rest := strings.TrimSpace(lines[0][strings.Index(lines[0], want)+len(want):]); rest != "" {
		return fmt.Errorf("mirror warning %q carries more than %q", lines[0], want)
	}
	return nil
}

func theMirrorLinkMoved(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	stable, err := mirrorStablePath(s, repoID)
	if err != nil {
		return err
	}
	got, err := os.Readlink(stable)
	if err != nil {
		return err
	}
	if got == m.link {
		return fmt.Errorf("the mirror link still names %q", got)
	}
	return nil
}

// everyRefReadsToTheRoot walks every ref in each borrower back to its root
// commit, then requires git fsck --full to be clean.
func everyRefReadsToTheRoot(ctx context.Context, repoID string) error {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := borrowersOf(m, repoID)
	if err != nil {
		return err
	}
	for _, clone := range clones {
		refs, err := fixtureGitWorkTree(clone, "for-each-ref", "--format=%(refname)")
		if err != nil {
			return fmt.Errorf("git for-each-ref in %s: %w\n%s", clone, err, refs)
		}
		names := strings.Fields(string(refs))
		if len(names) == 0 {
			return fmt.Errorf("borrower %s has no refs", clone)
		}
		for _, ref := range names {
			root, err := fixtureGitWorkTree(clone, "rev-list", "--max-parents=0", ref)
			if err != nil {
				return fmt.Errorf("git rev-list %s in %s: %w\n%s", ref, clone, err, root)
			}
			if got, want := strings.TrimSpace(string(root)), m.rootCommits[repoID]; got != want {
				return fmt.Errorf("%s in %s reaches root %q, want %q", ref, clone, got, want)
			}
		}
		out, err := fixtureGitWorkTree(clone, "fsck", "--full")
		if err != nil {
			return fmt.Errorf("git fsck --full in %s: %w\n%s", clone, err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			for _, bad := range []string{"missing", "broken", "dangling"} {
				if strings.Contains(line, bad) {
					return fmt.Errorf("git fsck --full in %s reports %q", clone, line)
				}
			}
		}
	}
	return nil
}

func onlyTheLinkedStoreRemains(ctx context.Context, repoID string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return err
	}
	stable, err := mirrorStablePath(s, repoID)
	if err != nil {
		return err
	}
	target, err := os.Readlink(stable)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".store"))
	if err != nil {
		return err
	}
	re := regexp.MustCompile(`^` + regexp.QuoteMeta(repoID) + `-[0-9]{8}T[0-9]{6}Z-[0-9a-f]+$`)
	var stores []string
	for _, e := range entries {
		if re.MatchString(e.Name()) {
			stores = append(stores, filepath.Join(".store", e.Name()))
		}
	}
	if len(stores) != 1 || stores[0] != target {
		return fmt.Errorf(".store holds %v of %s, want only the linked %s", stores, repoID, target)
	}
	return nil
}

func iRememberTheStateFile(ctx context.Context, repoID string) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	path, err := mirrorStatePath(s, repoID)
	if err != nil {
		return ctx, err
	}
	m.stateFile, err = os.ReadFile(path)
	return ctx, err
}

func theStateFileIsUnchanged(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	path, err := mirrorStatePath(s, repoID)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, m.stateFile) {
		return fmt.Errorf("the state file changed:\nbefore: %s\nafter:  %s", m.stateFile, got)
	}
	return nil
}

// theStateFileIsUnchangedApartFromLastRefresh compares every field of the
// state file with the remembered one but last_refresh, which a refresh moves.
func theStateFileIsUnchangedApartFromLastRefresh(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	path, err := mirrorStatePath(s, repoID)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var before, after map[string]any
	if err := json.Unmarshal(m.stateFile, &before); err != nil {
		return fmt.Errorf("decoding the remembered state file: %w", err)
	}
	if err := json.Unmarshal(got, &after); err != nil {
		return fmt.Errorf("decoding the state file: %w", err)
	}
	if _, ok := after["last_refresh"]; !ok {
		return fmt.Errorf("the state file has no last_refresh: %s", got)
	}
	delete(before, "last_refresh")
	delete(after, "last_refresh")
	if !reflect.DeepEqual(before, after) {
		return fmt.Errorf("the state file changed apart from last_refresh:\nbefore: %s\nafter:  %s", m.stateFile, got)
	}
	return nil
}
