package functional

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// The lineage tree scenarios keep several dispatches by name ("A", "B", ...)
// so a chain read back after their instances are gone can be compared level
// by level, and so a placeholder such as {A.dispatch} in a command or an
// expected line names the right one.

// lineageTreeState is what a tree scenario keeps between steps.
type lineageTreeState struct {
	kept       map[string]keptDispatch
	order      []string
	remembered map[string][]byte
	held       *heldDispatch
	// heldRefresh is the dispatch parked mid-swap by the snapshot race
	// scenario; bg holds the commands started while it is parked.
	heldRefresh *bgRun
	bg          []*bgRun
}

// bgRun is a niwa command running in the background. marker is the hold path
// whose ".held" file shows where the command stopped. Its result is kept once
// read so a later step still sees it.
type bgRun struct {
	name     string
	cmd      *exec.Cmd
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	marker   string
	done     chan error
	ended    bool
	exitCode int
	runErr   error
}

// keptDispatch is one dispatch a scenario named: its instance, the dispatch
// id its authority record carries and the session its mapping names.
type keptDispatch struct {
	instance   string
	dispatchID string
	session    string
}

// heldDispatch is a dispatch running in the background, stopped by the
// testfault hold seam between building its configuration copy and swapping
// it in.
type heldDispatch struct {
	cmd     *exec.Cmd
	stdout  bytes.Buffer
	stderr  bytes.Buffer
	release string
	done    chan error
}

func init() {
	authorityFixtures["on-reap-fails"] = "[session.on_reap]\ncommand = \"exit 3\"\n"
}

func registerLineageTreeSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^no workflow engine is on PATH$`, noWorkflowEngineIsOnPath)
	ctx.Step(`^I keep the dispatch as "([^"]*)"$`, iKeepTheDispatchAs)
	ctx.Step(`^the dispatching session is the session of "([^"]*)"$`, theDispatchingSessionIsTheSessionOf)
	ctx.Step(`^I dispatch with "([^"]*)" from inside the instance of "([^"]*)"$`, iDispatchFromInsideTheInstanceOf)
	ctx.Step(`^every kept dispatch's session is deleted from the Agent View$`, everyKeptDispatchsSessionIsDeleted)
	ctx.Step(`^no kept dispatch instance remains$`, noKeptDispatchInstanceRemains)
	ctx.Step(`^I run "([^"]*)" naming kept dispatches from the workspace root$`, iRunNamingKeptDispatches)
	ctx.Step(`^the output contains "([^"]*)" naming kept dispatches$`, theOutputContainsNamingKeptDispatches)
	ctx.Step(`^the lineage chain is:$`, theLineageChainIs)
	ctx.Step(`^the lineage chain stops at "([^"]*)"$`, theLineageChainStopsAt)
	ctx.Step(`^I remember the lineage entry of "([^"]*)"$`, iRememberTheLineageEntryOf)
	ctx.Step(`^the remembered lineage entry of "([^"]*)" is unchanged$`, theRememberedLineageEntryOfIsUnchanged)
	ctx.Step(`^the mapping of "([^"]*)" records no dispatch id$`, theMappingOfRecordsNoDispatchID)
	ctx.Step(`^the dispatch instance directory is removed by hand$`, theDispatchInstanceDirectoryIsRemovedByHand)
	ctx.Step(`^I reset the dispatch instance from the workspace root$`, iResetTheDispatchInstance)
	ctx.Step(`^I run niwa reap (\d+) more times from the workspace root$`, iRunNiwaReapMoreTimes)
	ctx.Step(`^the workspace root has no lineage store$`, theWorkspaceRootHasNoLineageStore)
	ctx.Step(`^the lineage store is gone$`, theLineageStoreIsGone)
	ctx.Step(`^the proxy variables point at a closed local port$`, theProxyVariablesPointAtAClosedPort)
	ctx.Step(`^a dispatch from the workspace root holds its refresh between the two renames$`, aDispatchHoldsItsRefreshBetweenTheRenames)
	ctx.Step(`^a second dispatch starts from the workspace root while the refresh is held$`, aSecondDispatchStartsWhileTheRefreshIsHeld)
	ctx.Step(`^niwa apply starts in the created instance while the refresh is held$`, niwaApplyStartsWhileTheRefreshIsHeld)
	ctx.Step(`^each started command is waiting for the config lock or has ended$`, eachStartedCommandIsWaitingOrHasEnded)
	ctx.Step(`^the held refresh is let go$`, theHeldRefreshIsLetGo)
	ctx.Step(`^the held dispatch, the second dispatch and the apply all exit 0$`, theHeldAndStartedCommandsAllExitZero)
	ctx.Step(`^each started command waited for the config lock$`, eachStartedCommandWaitedForTheConfigLock)
	ctx.Step(`^both dispatches have an instance, a session mapping and a lineage entry$`, bothDispatchesHaveAnInstanceMappingAndLineageEntry)
	ctx.Step(`^the apply reported no missing workspace config$`, theApplyReportedNoMissingWorkspaceConfig)
	ctx.Step(`^the workspace root has no \.niwa\.next or \.niwa\.prev left$`, theWorkspaceRootHasNoSwapLeftovers)
	ctx.Step(`^the held dispatch is released and ends$`, theHeldDispatchIsReleased)
}

func treeState(s *testState) *lineageTreeState {
	if s.lineageTree == nil {
		s.lineageTree = &lineageTreeState{kept: map[string]keptDispatch{}, remembered: map[string][]byte{}}
	}
	return s.lineageTree
}

// noWorkflowEngineIsOnPath rebuilds the PATH niwa runs with so no directory
// on it holds a koto: a directory that does is replaced by a copy of it made
// of symlinks to everything else it holds.
func noWorkflowEngineIsOnPath(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	var dirs []string
	for i, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(dir, "koto")); err != nil {
			dirs = append(dirs, dir)
			continue
		}
		shadow := filepath.Join(s.sandbox, fmt.Sprintf("path-without-koto-%d", i))
		if err := os.MkdirAll(shadow, 0o755); err != nil {
			return ctx, err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return ctx, err
		}
		for _, e := range entries {
			if e.Name() == "koto" {
				continue
			}
			if err := os.Symlink(filepath.Join(dir, e.Name()), filepath.Join(shadow, e.Name())); err != nil {
				return ctx, err
			}
		}
		dirs = append(dirs, shadow)
	}
	s.inheritedPath = strings.Join(dirs, string(os.PathListSeparator))
	if s.inheritedPath == "" {
		s.inheritedPath = "/usr/bin:/bin"
	}
	for _, dir := range append([]string{s.pathPrefix, s.sharedBinDir}, dirs...) {
		if dir == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "koto")); err == nil {
			return ctx, fmt.Errorf("%s still holds a koto", dir)
		}
	}
	return ctx, nil
}

// iKeepTheDispatchAs names the one dispatch instance that has an authority
// record and isn't kept yet.
func iKeepTheDispatchAs(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	t := treeState(s)
	if _, ok := t.kept[name]; ok {
		return ctx, fmt.Errorf("a dispatch is already kept as %q", name)
	}
	taken := map[string]bool{}
	for _, k := range t.kept {
		taken[resolvedPath(k.instance)] = true
	}
	entries, err := os.ReadDir(s.workspaceRoot)
	if err != nil {
		return ctx, err
	}
	var found []string
	for _, e := range entries {
		path := filepath.Join(s.workspaceRoot, e.Name())
		if !e.IsDir() || !dispatchInstanceNameRe.MatchString(e.Name()) || taken[resolvedPath(path)] {
			continue
		}
		if _, err := os.Stat(filepath.Join(path, ".niwa", "authority.json")); err == nil {
			found = append(found, path)
		}
	}
	if len(found) != 1 {
		return ctx, fmt.Errorf("want one new dispatch instance with a record, found %v\n%s%s", found, s.stdout, s.stderr)
	}
	data, err := os.ReadFile(filepath.Join(found[0], ".niwa", "authority.json"))
	if err != nil {
		return ctx, err
	}
	var rec struct {
		DispatchID string `json:"dispatch_id"`
	}
	if err := json.Unmarshal(data, &rec); err != nil || rec.DispatchID == "" {
		return ctx, fmt.Errorf("no dispatch id in %s (%v)", found[0], err)
	}
	session, err := mappingFor(s, found[0])
	if err != nil {
		return ctx, err
	}
	t.kept[name] = keptDispatch{instance: found[0], dispatchID: rec.DispatchID, session: session}
	t.order = append(t.order, name)
	s.lastDispatchInstancePath = found[0]
	return ctx, nil
}

func kept(s *testState, name string) (keptDispatch, error) {
	k, ok := treeState(s).kept[name]
	if !ok {
		return keptDispatch{}, fmt.Errorf("no dispatch is kept as %q", name)
	}
	return k, nil
}

func theDispatchingSessionIsTheSessionOf(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	k, err := kept(s, name)
	if err != nil {
		return ctx, err
	}
	s.envOverrides["CLAUDE_CODE_SESSION_ID"] = k.session
	return ctx, nil
}

func iDispatchFromInsideTheInstanceOf(ctx context.Context, flags, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	k, err := kept(s, name)
	if err != nil {
		return ctx, err
	}
	return ctx, dispatchFrom(s, k.instance, flags)
}

func everyKeptDispatchsSessionIsDeleted(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	for _, name := range treeState(s).order {
		var err error
		if ctx, err = theDispatchSessionIsDeleted(ctx, treeState(s).kept[name].session); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

func noKeptDispatchInstanceRemains(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	for _, name := range treeState(s).order {
		if _, err := os.Stat(treeState(s).kept[name].instance); err == nil {
			return fmt.Errorf("the instance of %q is still there\n%s%s", name, s.stdout, s.stderr)
		}
	}
	return nil
}

// expandKept fills {<name>.dispatch}, {<name>.session} and {<name>.instance}
// for every kept dispatch.
func expandKept(s *testState, text string) string {
	for name, k := range treeState(s).kept {
		text = strings.ReplaceAll(text, "{"+name+".dispatch}", k.dispatchID)
		text = strings.ReplaceAll(text, "{"+name+".session}", k.session)
		text = strings.ReplaceAll(text, "{"+name+".instance}", filepath.Base(k.instance))
	}
	return text
}

func iRunNamingKeptDispatches(ctx context.Context, command string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, runNiwa(s, s.workspaceRoot, expandKept(s, command))
}

func theOutputContainsNamingKeptDispatches(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	want = expandKept(s, want)
	if !strings.Contains(s.stdout, want) {
		return fmt.Errorf("output lacks %q:\n%s%s", want, s.stdout, s.stderr)
	}
	return nil
}

type chainLink struct {
	ID     string `json:"id"`
	Source string `json:"source"`
}

type chainDoc struct {
	Schema string `json:"schema"`
	Chain  []struct {
		DispatchID     string    `json:"dispatch_id"`
		ParentSession  chainLink `json:"parent_session"`
		ParentDispatch chainLink `json:"parent_dispatch"`
		Principal      struct {
			Name   string `json:"name"`
			Source string `json:"source"`
			Trust  string `json:"trust"`
		} `json:"principal"`
	} `json:"chain"`
	Stop string `json:"stop"`
}

func lastChain(s *testState) (chainDoc, error) {
	var doc chainDoc
	if s.exitCode != 0 {
		return doc, fmt.Errorf("lineage show exited %d:\n%s%s", s.exitCode, s.stdout, s.stderr)
	}
	if err := json.Unmarshal([]byte(s.stdout), &doc); err != nil || doc.Schema != "niwa.lineage.chain/v1" {
		return doc, fmt.Errorf("not a chain document (%v):\n%s%s", err, s.stdout, s.stderr)
	}
	return doc, nil
}

// expectedLink reads a table cell: "none", or "<who> <source>" where <who> is
// a kept dispatch's name (its session for a parent session, its dispatch id
// for a parent dispatch) or a literal id.
func expectedLink(s *testState, cell string, session bool) (chainLink, error) {
	cell = strings.TrimSpace(cell)
	if cell == "none" {
		return chainLink{Source: "none"}, nil
	}
	f := strings.Fields(cell)
	if len(f) != 2 {
		return chainLink{}, fmt.Errorf("bad link cell %q", cell)
	}
	id := f[0]
	if k, ok := treeState(s).kept[id]; ok {
		id = k.dispatchID
		if session {
			id = k.session
		}
	}
	return chainLink{ID: id, Source: f[1]}, nil
}

// theLineageChainIs compares the chain `lineage show --json` printed with a
// table of dispatch | parent session | parent dispatch | principal, nearest
// first; the principal cell is "<name> <source> <trust>".
func theLineageChainIs(ctx context.Context, table *godog.Table) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	doc, err := lastChain(s)
	if err != nil {
		return err
	}
	rows := table.Rows[1:]
	if len(doc.Chain) != len(rows) {
		return fmt.Errorf("chain has %d levels, want %d:\n%s", len(doc.Chain), len(rows), s.stdout)
	}
	for i, row := range rows {
		got := doc.Chain[i]
		k, err := kept(s, row.Cells[0].Value)
		if err != nil {
			return err
		}
		if got.DispatchID != k.dispatchID {
			return fmt.Errorf("level %d is %s, want %s's %s:\n%s", i, got.DispatchID, row.Cells[0].Value, k.dispatchID, s.stdout)
		}
		ps, err := expectedLink(s, row.Cells[1].Value, true)
		if err != nil {
			return err
		}
		pd, err := expectedLink(s, row.Cells[2].Value, false)
		if err != nil {
			return err
		}
		if got.ParentSession != ps || got.ParentDispatch != pd {
			return fmt.Errorf("level %d (%s): parent session %+v, parent dispatch %+v; want %+v, %+v:\n%s",
				i, row.Cells[0].Value, got.ParentSession, got.ParentDispatch, ps, pd, s.stdout)
		}
		p := strings.Fields(row.Cells[3].Value)
		if len(p) == 3 && p[0] == "none" {
			p[0] = ""
		}
		if len(p) != 3 || got.Principal.Name != p[0] || got.Principal.Source != p[1] || got.Principal.Trust != p[2] {
			return fmt.Errorf("level %d (%s): principal %+v, want %q:\n%s", i, row.Cells[0].Value, got.Principal, row.Cells[3].Value, s.stdout)
		}
	}
	return nil
}

func theLineageChainStopsAt(ctx context.Context, stop string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	doc, err := lastChain(s)
	if err != nil {
		return err
	}
	if doc.Stop != stop {
		return fmt.Errorf("stop = %q, want %q:\n%s", doc.Stop, stop, s.stdout)
	}
	return nil
}

func keptEntryPath(s *testState, name string) (string, error) {
	k, err := kept(s, name)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.workspaceRoot, ".niwa-lineage", k.dispatchID+".json"), nil
}

func iRememberTheLineageEntryOf(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	path, err := keptEntryPath(s, name)
	if err != nil {
		return ctx, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ctx, err
	}
	treeState(s).remembered[name] = data
	return ctx, nil
}

func theRememberedLineageEntryOfIsUnchanged(ctx context.Context, name string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	want, ok := treeState(s).remembered[name]
	if !ok {
		return fmt.Errorf("the entry of %q wasn't remembered", name)
	}
	path, err := keptEntryPath(s, name)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("the entry of %q is gone: %w\n%s%s", name, err, s.stdout, s.stderr)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("the entry of %q changed:\nwas %s\nnow %s", name, want, got)
	}
	return nil
}

// theMappingOfRecordsNoDispatchID rewrites a kept dispatch's session mapping
// without its dispatch id, the shape of a mapping niwa created for a session
// it didn't dispatch.
func theMappingOfRecordsNoDispatchID(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	k, err := kept(s, name)
	if err != nil {
		return ctx, err
	}
	dir := filepath.Join(s.workspaceRoot, ".niwa", "sessions")
	for _, file := range sessionMappings(s) {
		path := filepath.Join(dir, file)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m map[string]interface{}
		if json.Unmarshal(data, &m) != nil || m["session_id"] != k.session {
			continue
		}
		if _, ok := m["dispatch_id"]; !ok {
			return ctx, fmt.Errorf("the mapping of %q has no dispatch id to remove", name)
		}
		delete(m, "dispatch_id")
		out, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return ctx, err
		}
		return ctx, os.WriteFile(path, out, 0o600)
	}
	return ctx, fmt.Errorf("no mapping names the session of %q", name)
}

func theDispatchInstanceDirectoryIsRemovedByHand(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return ctx, fmt.Errorf("no dispatch instance recorded")
	}
	return ctx, os.RemoveAll(s.lastDispatchInstancePath)
}

func iResetTheDispatchInstance(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return ctx, fmt.Errorf("no dispatch instance recorded")
	}
	return ctx, runNiwa(s, s.workspaceRoot, "niwa reset --force "+filepath.Base(s.lastDispatchInstancePath))
}

func iRunNiwaReapMoreTimes(ctx context.Context, n int) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	for i := 0; i < n; i++ {
		if err := runNiwa(s, s.workspaceRoot, "niwa reap"); err != nil {
			return ctx, err
		}
		if s.exitCode != 0 {
			return ctx, fmt.Errorf("reap %d exited %d:\n%s%s", i+1, s.exitCode, s.stdout, s.stderr)
		}
	}
	return ctx, nil
}

func theWorkspaceRootHasNoLineageStore(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if _, err := os.Lstat(filepath.Join(s.workspaceRoot, ".niwa-lineage")); !os.IsNotExist(err) {
		return fmt.Errorf("the workspace root has a lineage store (%v)", err)
	}
	return nil
}

// theLineageStoreIsGone checks the store went with the workspace, which a
// lineage entry was in before.
func theLineageStoreIsGone(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if len(treeState(s).remembered) == 0 {
		return fmt.Errorf("remember an entry first, so the store is known to have existed")
	}
	return theWorkspaceRootHasNoLineageStore(ctx)
}

// theProxyVariablesPointAtAClosedPort points every proxy variable at a local
// port nothing listens on, so any network call niwa made would fail.
func theProxyVariablesPointAtAClosedPort(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ctx, err
	}
	url := "http://" + l.Addr().String()
	if err := l.Close(); err != nil {
		return ctx, err
	}
	for _, name := range []string{"HTTPS_PROXY", "HTTP_PROXY", "https_proxy", "http_proxy", "ALL_PROXY", "all_proxy"} {
		s.envOverrides[name] = url
	}
	s.envOverrides["NO_PROXY"] = ""
	s.envOverrides["no_proxy"] = ""
	return ctx, nil
}

// startBgRun starts niwa with args in dir, with NIWA_TEST_FAULT set to fault,
// and registers a cleanup that kills it if the scenario ends first.
func startBgRun(s *testState, name, dir, marker, fault string, args ...string) (*bgRun, error) {
	b := &bgRun{name: name, marker: marker, done: make(chan error, 1)}
	b.cmd = exec.Command(s.binPath, args...)
	b.cmd.Dir = dir
	b.cmd.Env = append(s.buildEnv(), "NIWA_TEST_FAULT="+fault)
	b.cmd.Stdout = &b.stdout
	b.cmd.Stderr = &b.stderr
	if err := b.cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting %s: %w", name, err)
	}
	go func() { b.done <- b.cmd.Wait() }()
	s.cleanups = append(s.cleanups, func() {
		if !b.ended {
			_ = b.cmd.Process.Kill()
		}
	})
	return b, nil
}

// record keeps the result of a finished command.
func (b *bgRun) record(err error) {
	b.ended = true
	b.runErr = err
	b.exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		b.exitCode = exitErr.ExitCode()
		b.runErr = nil
	} else if err != nil {
		b.exitCode = -1
	}
}

// poll reports whether the command has ended, without blocking.
func (b *bgRun) poll() bool {
	if b.ended {
		return true
	}
	select {
	case err := <-b.done:
		b.record(err)
	default:
	}
	return b.ended
}

// wait blocks until the command ends. guard only stops a hung command; it is
// not something the scenario asserts on.
func (b *bgRun) wait(guard time.Duration) error {
	if b.ended {
		return nil
	}
	select {
	case err := <-b.done:
		b.record(err)
		return nil
	case <-time.After(guard):
	}
	_ = b.cmd.Process.Kill()
	select {
	case err := <-b.done:
		b.record(err)
	case <-time.After(10 * time.Second):
	}
	return fmt.Errorf("%s didn't finish and was killed:\n%s", b.name, b.output())
}

// waiting reports whether the command recorded a stop at its marker.
func (b *bgRun) waiting() bool {
	_, err := os.Stat(b.marker + ".held")
	return err == nil
}

// output is the command's stdout and stderr, for failure messages.
func (b *bgRun) output() string {
	return fmt.Sprintf("--- %s stdout ---\n%s--- %s stderr ---\n%s", b.name, b.stdout.String(), b.name, b.stderr.String())
}

// aDispatchHoldsItsRefreshBetweenTheRenames starts a dispatch in the
// background with the testfault hold at snapshot-swap-mid and waits until it
// is there. The dispatch's first swap is the workspace-root refresh, so it
// parks with .niwa renamed to .niwa.prev and its new copy in .niwa.next.
func aDispatchHoldsItsRefreshBetweenTheRenames(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	t := treeState(s)
	release := filepath.Join(s.sandbox, "release-held-refresh")
	b, err := startBgRun(s, "the held dispatch", s.workspaceRoot, release,
		"hold:"+release+"@snapshot-swap-mid", "dispatch", "held-task", "--detach")
	if err != nil {
		return ctx, err
	}
	t.heldRefresh = b
	guard := time.After(60 * time.Second)
	for !b.waiting() {
		if b.poll() {
			return ctx, fmt.Errorf("the held dispatch ended with exit code %d before reaching the hold (%v):\n%s", b.exitCode, b.runErr, b.output())
		}
		select {
		case <-guard:
			_ = b.cmd.Process.Kill()
			return ctx, fmt.Errorf("the dispatch never reached the hold")
		case <-time.After(20 * time.Millisecond):
		}
	}
	return ctx, nil
}

// startWhileHeld starts a command that must wait for the config lock. Its
// marker is created first, so at snapshot-lock-wait it writes marker.held and
// goes on polling the lock rather than parking in the hold.
func startWhileHeld(s *testState, name, dir, markerName string, args ...string) error {
	t := treeState(s)
	if t.heldRefresh == nil {
		return fmt.Errorf("no refresh is held")
	}
	marker := filepath.Join(s.sandbox, markerName)
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		return err
	}
	b, err := startBgRun(s, name, dir, marker, "hold:"+marker+"@snapshot-lock-wait", args...)
	if err != nil {
		return err
	}
	t.bg = append(t.bg, b)
	return nil
}

func aSecondDispatchStartsWhileTheRefreshIsHeld(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, startWhileHeld(s, "the second dispatch", s.workspaceRoot, "lock-wait-dispatch",
		"dispatch", "second-task", "--detach")
}

// niwaApplyStartsWhileTheRefreshIsHeld runs niwa apply in the one instance
// the scenario created by hand (not a dispatch instance).
func niwaApplyStartsWhileTheRefreshIsHeld(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	entries, err := os.ReadDir(s.workspaceRoot)
	if err != nil {
		return ctx, err
	}
	var found []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") || dispatchInstanceNameRe.MatchString(name) {
			continue
		}
		path := filepath.Join(s.workspaceRoot, name)
		if _, err := os.Stat(filepath.Join(path, ".niwa", "instance.json")); err == nil {
			found = append(found, path)
		}
	}
	if len(found) != 1 {
		return ctx, fmt.Errorf("want one created instance, found %v", found)
	}
	return ctx, startWhileHeld(s, "the apply", found[0], "lock-wait-apply", "apply")
}

// eachStartedCommandIsWaitingOrHasEnded waits until every started command
// has written its marker.held or has ended.
func eachStartedCommandIsWaitingOrHasEnded(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	t := treeState(s)
	if len(t.bg) == 0 {
		return ctx, fmt.Errorf("no command was started")
	}
	guard := time.After(3 * time.Minute)
	for {
		settled := true
		for _, b := range t.bg {
			if !b.waiting() && !b.poll() {
				settled = false
			}
		}
		if settled {
			return ctx, nil
		}
		select {
		case <-guard:
			var out strings.Builder
			for _, b := range t.bg {
				out.WriteString(b.output())
			}
			return ctx, fmt.Errorf("a started command neither waited for the config lock nor ended:\n%s", out.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func theHeldRefreshIsLetGo(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	h := treeState(s).heldRefresh
	if h == nil {
		return ctx, fmt.Errorf("no refresh is held")
	}
	return ctx, os.WriteFile(h.marker, nil, 0o600)
}

// theHeldAndStartedCommandsAllExitZero waits for the held dispatch and every
// started command, then reports each one that failed with its own output.
func theHeldAndStartedCommandsAllExitZero(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	t := treeState(s)
	if t.heldRefresh == nil {
		return ctx, fmt.Errorf("no refresh is held")
	}
	var failures []string
	for _, b := range append([]*bgRun{t.heldRefresh}, t.bg...) {
		if err := b.wait(3 * time.Minute); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if b.runErr != nil {
			failures = append(failures, fmt.Sprintf("%s didn't run: %v\n%s", b.name, b.runErr, b.output()))
		} else if b.exitCode != 0 {
			failures = append(failures, fmt.Sprintf("%s exited %d:\n%s", b.name, b.exitCode, b.output()))
		}
	}
	if len(failures) > 0 {
		return ctx, fmt.Errorf("not every command exited 0:\n%s", strings.Join(failures, "\n"))
	}
	return ctx, nil
}

// eachStartedCommandWaitedForTheConfigLock proves the commands contended for
// the lock, so a pass isn't an accident of scheduling.
func eachStartedCommandWaitedForTheConfigLock(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	for _, b := range treeState(s).bg {
		if !b.waiting() {
			return ctx, fmt.Errorf("%s never waited for the config lock (no %s.held)", b.name, b.marker)
		}
	}
	return ctx, nil
}

func bothDispatchesHaveAnInstanceMappingAndLineageEntry(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	entries, err := os.ReadDir(s.workspaceRoot)
	if err != nil {
		return ctx, err
	}
	var found []string
	for _, e := range entries {
		path := filepath.Join(s.workspaceRoot, e.Name())
		if !e.IsDir() || !dispatchInstanceNameRe.MatchString(e.Name()) {
			continue
		}
		if _, err := os.Stat(filepath.Join(path, ".niwa", "authority.json")); err == nil {
			found = append(found, path)
		}
	}
	if len(found) != 2 {
		return ctx, fmt.Errorf("want two dispatch instances with a record, found %v", found)
	}
	for _, inst := range found {
		data, err := os.ReadFile(filepath.Join(inst, ".niwa", "authority.json"))
		if err != nil {
			return ctx, err
		}
		var rec struct {
			DispatchID string `json:"dispatch_id"`
		}
		if err := json.Unmarshal(data, &rec); err != nil || rec.DispatchID == "" {
			return ctx, fmt.Errorf("no dispatch id in %s (%v)", inst, err)
		}
		if _, err := mappingFor(s, inst); err != nil {
			return ctx, err
		}
		entry := filepath.Join(s.workspaceRoot, ".niwa-lineage", rec.DispatchID+".json")
		if _, err := os.Stat(entry); err != nil {
			return ctx, fmt.Errorf("no lineage entry for %s: %w", inst, err)
		}
	}
	return ctx, nil
}

func theApplyReportedNoMissingWorkspaceConfig(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	var apply *bgRun
	for _, b := range treeState(s).bg {
		if b.name == "the apply" {
			apply = b
		}
	}
	if apply == nil {
		return ctx, fmt.Errorf("no apply was started")
	}
	out := apply.stdout.String() + apply.stderr.String()
	for _, bad := range []string{
		"reading config:",
		"workspace.toml found",
		"could not locate workspace configuration",
		"no such file or directory",
	} {
		if strings.Contains(out, bad) {
			return ctx, fmt.Errorf("the apply reported %q:\n%s", bad, apply.output())
		}
	}
	return ctx, nil
}

func theWorkspaceRootHasNoSwapLeftovers(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	for _, name := range []string{".niwa.next", ".niwa.prev"} {
		path := filepath.Join(s.workspaceRoot, name)
		if _, err := os.Lstat(path); err == nil {
			return ctx, fmt.Errorf("%s was left behind", path)
		} else if !os.IsNotExist(err) {
			return ctx, err
		}
	}
	return ctx, nil
}

func theHeldDispatchIsReleased(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	h := treeState(s).held
	if h == nil {
		return ctx, fmt.Errorf("no dispatch is held")
	}
	if err := os.WriteFile(h.release, nil, 0o600); err != nil {
		return ctx, err
	}
	select {
	case err := <-h.done:
		s.stdout, s.stderr, s.exitCode = h.stdout.String(), h.stderr.String(), 0
		if exitErr, ok := err.(*exec.ExitError); ok {
			s.exitCode = exitErr.ExitCode()
		} else if err != nil {
			return ctx, fmt.Errorf("the held dispatch didn't run: %v", err)
		}
	case <-time.After(3 * time.Minute):
		_ = h.cmd.Process.Kill()
		return ctx, fmt.Errorf("the held dispatch didn't finish:\n%s%s", h.stdout.String(), h.stderr.String())
	}
	return ctx, nil
}
