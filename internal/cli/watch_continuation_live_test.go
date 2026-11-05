package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/watch"
)

// TestResumedSessionDeniesEgress_OnHarness is the live gate for a review
// continuation. It drives a real Claude Code review session through the
// production pieces a continuation uses and checks, from inside the woken
// session, that the OS no-egress sandbox holds:
//
//  1. stage with watchReviewLaunch through dispatchLaunch, after
//     watch.ApplyReviewSettings, recording the settings digest as stageReview
//     does;
//  2. stop the session and poll `claude agents --json --all` until its row
//     shows no pid and no status;
//  3. run the pre-wake gate (runReviewWakeGate) against the job's real state
//     file under ~/.claude/jobs;
//  4. wake with no flags through resumeWakeDeliverer;
//  5. fail if the woken session's id differs from the staged one or a second
//     session is rooted at the instance (liveGateCopyCheck);
//  6. run a raw TCP connect from inside the woken session and fail if it
//     connects;
//  7. wake a second time and repeat the id, single-session and egress checks,
//     which covers chaining.
//
// Two subtests follow: one writes the sandbox stanza only after the stop
// settles, the other plants settings and an MCP server in the PR clone and
// checks neither is loaded.
//
// It runs only with NIWA_WATCH_LIVE_TEST=1 and
// NIWA_WATCH_LIVE_TEST_DISPOSABLE_HOST=1, on a host other than Windows with
// claude on PATH; otherwise it skips. Once those hold, every later step fails
// the test rather than skipping it, so a run that can't prove the boundary
// never reads as a pass. When NIWA_WATCH_LIVE_REPORT names a file, the test
// appends its report lines there (see appendLiveGateReport).
func TestResumedSessionDeniesEgress_OnHarness(t *testing.T) {
	requireLiveGateHost(t)

	// The probe must reach the network outside the sandbox, so its failure
	// inside the woken session is a real signal.
	if err := liveGateEgressReachable(); err != nil {
		t.Fatalf("the egress probe failed outside the sandbox (%v); it must reach the network here for an in-sandbox failure to mean anything", err)
	}

	h := newLiveGateHarness(t)
	appendLiveGateReport(t, "claude_version: "+liveGateClaudeVersion(t))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Steps 1 to 4: stage, stop and settle, gate, wake.
	r := h.stage(t, ctx, t.TempDir(), true)
	h.stopAndSettle(t, ctx, r)
	opts := h.savedOptions(t, r)
	appendLiveGateReport(t, liveGateSavedFlagsLine(opts))
	h.gate(t, r, opts)

	// Steps 5 and 6 for the first wake, which also records whether claude.ai
	// still reaches the session.
	row := h.wakeAndCheck(t, ctx, r, "wake1-egress.result")
	appendLiveGateReport(t, liveGateReachLine(row))

	// Step 7: the second wake chains on the same session id.
	h.stopAndSettle(t, ctx, r)
	h.gate(t, r, h.savedOptions(t, r))
	h.wakeAndCheck(t, ctx, r, "wake2-egress.result")

	subtests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{"stanza_written_after_settle", func(t *testing.T) {
			// Staged with the sandboxed argv, but the instance settings
			// carry no sandbox stanza until the stop has settled. The
			// woken process then has only the on-disk stanza to go by.
			r := h.stage(t, ctx, t.TempDir(), false)
			h.stopAndSettle(t, ctx, r)
			if err := watch.ApplyReviewSettings(r.inst, true, r.ask); err != nil {
				t.Fatalf("writing the sandbox stanza after settle: %v", err)
			}
			digest, err := watch.ReviewSettingsDigest(r.inst)
			if err != nil {
				t.Fatalf("recording the settings digest after settle: %v", err)
			}
			r.rec.SettingsDigest = digest
			h.gate(t, r, h.savedOptions(t, r))
			h.wakeAndCheck(t, ctx, r, "stanza-egress.result")
		}},
		{"pr_clone_settings_not_loaded", func(t *testing.T) {
			r := h.stage(t, ctx, t.TempDir(), true)
			h.stopAndSettle(t, ctx, r)
			// Planted where a head fetch puts the PR's files, after the
			// settle, as a fetch would. The markers live outside the
			// instance: a hook or an MCP server runs outside the sandbox,
			// so it could write there if it were loaded.
			markers := t.TempDir()
			hookMarker := filepath.Join(markers, "session-start-hook.ran")
			mcpMarker := filepath.Join(markers, "mcp-server.ran")
			liveGatePlantCloneConfig(t, r.inst, hookMarker, mcpMarker)
			h.gate(t, r, h.savedOptions(t, r))
			h.wakeAndCheck(t, ctx, r, "clone-egress.result")
			for _, m := range []string{hookMarker, mcpMarker} {
				if _, err := os.Lstat(m); err == nil {
					t.Fatalf("%s exists: the woken session loaded configuration from the PR clone", filepath.Base(m))
				} else if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("checking %s: %v", filepath.Base(m), err)
				}
			}
		}},
	}
	for _, st := range subtests {
		if t.Run(st.name, st.fn) {
			appendLiveGateReport(t, "subtest "+st.name+": pass")
		}
	}
}

// requireLiveGateHost skips unless the live gate's double opt-in is set and
// the host can run it. NIWA_WATCH_LIVE_TEST=1 opts in; and because the gate
// runs real authenticated claude sessions that share the real ~/.claude
// (symlinked into an isolated HOME) under a no-egress sandbox,
// NIWA_WATCH_LIVE_TEST_DISPOSABLE_HOST=1 has to confirm the host is a
// disposable or CI one, never a developer's own machine. These are the only
// skips: everything after them fails.
func requireLiveGateHost(t *testing.T) {
	t.Helper()
	if os.Getenv("NIWA_WATCH_LIVE_TEST") != "1" {
		t.Skip("live containment gate: set NIWA_WATCH_LIVE_TEST=1 to run; skipping (never a false pass)")
	}
	if os.Getenv("NIWA_WATCH_LIVE_TEST_DISPOSABLE_HOST") != "1" {
		t.Skip("live containment gate: it runs REAL authenticated claude sessions " +
			"against your real ~/.claude under a no-egress sandbox. Run it ONLY on a disposable or CI " +
			"host, never your primary workstation. Set NIWA_WATCH_LIVE_TEST_DISPOSABLE_HOST=1 to confirm.")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the OS sandbox is unavailable on Windows (the feature fails closed there)")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH; the live containment gate needs it")
	}
}

// liveGateEgressReachable reports nil when a raw TCP connect to a literal IP,
// or a dial to a hostname, reaches the network from this process.
func liveGateEgressReachable() error {
	for _, addr := range []string{"1.1.1.1:443", "example.com:443"} {
		if c, err := net.DialTimeout("tcp", addr, 3*time.Second); err == nil {
			_ = c.Close()
			return nil
		}
	}
	return errors.New("all egress attempts were blocked")
}

// liveGateHarness is the live gate's shared setup: a workspace root with no
// configuration, the real jobs directory, and the isolated HOME the sessions
// run under.
type liveGateHarness struct {
	root        string
	realJobsDir string
}

// liveGateReview is one staged review: its instance, the write posture its
// stage resolved, and the staged record a continuation would hold for it.
type liveGateReview struct {
	inst string
	ask  bool
	rec  watch.StagedRecord
}

// newLiveGateHarness builds niwa for the guard hooks and points the review
// settings at it, and moves HOME to a throwaway directory whose ~/.claude
// links the real one, so the sessions authenticate and the jobs directory is
// the real one while Claude Code's user config is a copy. Everything is
// restored at cleanup.
func newLiveGateHarness(t *testing.T) *liveGateHarness {
	t.Helper()

	// Built before HOME moves, so the Go caches stay where they are.
	guardBin := filepath.Join(t.TempDir(), "niwa")
	if out, err := exec.Command("go", "build", "-o", guardBin, "github.com/tsukumogami/niwa/cmd/niwa").CombinedOutput(); err != nil {
		t.Fatalf("building niwa for the guard hooks: %v\n%s", err, out)
	}
	t.Cleanup(watch.SetGuardBinPath(guardBin))

	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolving the real HOME: %v", err)
	}
	testHome := t.TempDir()
	if err := os.Symlink(filepath.Join(realHome, ".claude"), filepath.Join(testHome, ".claude")); err != nil {
		t.Fatalf("sharing ~/.claude into the isolated HOME: %v", err)
	}
	if data, rerr := os.ReadFile(filepath.Join(realHome, ".claude.json")); rerr == nil {
		if werr := os.WriteFile(filepath.Join(testHome, ".claude.json"), data, 0o600); werr != nil {
			t.Fatalf("seeding the isolated HOME's Claude Code config: %v", werr)
		}
	}
	// The launch, the capture, the stop and the wake all take this process's
	// environment, so they all run under the isolated HOME. So does
	// resolveAskPosture: the trust it seeds for each instance lands in the
	// isolated copy of Claude Code's user config, never the real one.
	t.Setenv("HOME", testHome)

	return &liveGateHarness{
		root:        t.TempDir(),
		realJobsDir: filepath.Join(realHome, ".claude", "jobs"),
	}
}

// stage launches a review session the way stageReview does: the posture, the
// review settings, the digest, then dispatchLaunch of watchReviewLaunch with
// the sandboxed argv, and the capture of its ids. With stanza false the
// settings are written without the sandbox stanza and no digest is recorded,
// for the caller to do after the settle.
//
// The instance gets a minimal niwa state first, as a provisioned instance
// has one, so resolveAskPosture can seed Claude Code's trust for it and
// record the grant. Claude Code won't start a session in an untrusted
// directory, and the posture is then the one a review on a host where trust
// can be seeded runs under.
func (h *liveGateHarness) stage(t *testing.T, ctx context.Context, inst string, stanza bool) *liveGateReview {
	t.Helper()
	if err := writeMinimalInstanceState(inst); err != nil {
		t.Fatalf("writing the instance state: %v", err)
	}
	var postureOut bytes.Buffer
	ask := resolveAskPosture(&postureOut, inst, true)
	t.Logf("live gate: write posture for %s: ask=%v %s", filepath.Base(inst), ask, strings.TrimSpace(postureOut.String()))
	if err := watch.ApplyReviewSettings(inst, stanza, ask); err != nil {
		t.Fatalf("applying the review settings: %v", err)
	}
	digest := ""
	if stanza {
		d, err := watch.ReviewSettingsDigest(inst)
		if err != nil {
			t.Fatalf("recording the settings digest: %v", err)
		}
		digest = d
	}
	reapKey, err := newDispatchID(dispatchRandReader)
	if err != nil {
		t.Fatalf("minting a reap key: %v", err)
	}

	slug := "watch-live-" + filepath.Base(inst)
	if len(slug) > 40 {
		slug = slug[:40]
	}
	slug = sanitizeInstanceSlug(slug)
	prompt := "You are in a no-egress review sandbox. Use the Write tool to write a file named ready.txt in the current directory containing the single word ready, then STOP."
	if err := dispatchLaunch(ctx, watchReviewLaunch(inst, slug, prompt, true, watchLaunchEnv(h.root, reapKey))); err != nil {
		t.Fatalf("launching the review session: %v", err)
	}

	sid, short := "", ""
	for i := 0; i < 6 && sid == "" && ctx.Err() == nil; i++ {
		sid, short = captureReviewSession(inst)
	}
	if sid == "" {
		t.Fatalf("the review session's ids weren't captured from the jobs directory (instance holds: %s)", liveGateInstFiles(inst))
	}
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_ = stopSessionFunc(c, short)
		_, _ = removeClaudeSessionFunc(c, short)
	})

	// The first turn is given time to finish before anything stops it, so
	// the transcript ends on a completed turn. Its marker is logged only.
	ready := filepath.Join(inst, "ready.txt")
	for i := 0; i < 60 && ctx.Err() == nil; i++ {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	_, rerr := os.Stat(ready)
	t.Logf("live gate: staged %s (short %s) in %s; first turn wrote ready.txt=%v", sid, short, inst, rerr == nil)

	return &liveGateReview{inst: inst, ask: ask, rec: watch.StagedRecord{
		Handle:         slug,
		InstancePath:   inst,
		SessionID:      sid,
		ShortID:        short,
		ReapKey:        reapKey,
		SettingsDigest: digest,
	}}
}

// stopAndSettle stops the session through stopSessionFunc and polls the
// listing until its row shows no pid and no status. The row has to stay
// listed: a wake needs the job entry.
func (h *liveGateHarness) stopAndSettle(t *testing.T, ctx context.Context, r *liveGateReview) {
	t.Helper()
	if err := stopSessionFunc(ctx, r.rec.ShortID); err != nil {
		t.Fatalf("stopping %s: %v", r.rec.ShortID, err)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		rows, err := listClaudeSessionsFunc(ctx)
		if err != nil {
			t.Fatalf("listing sessions while waiting for %s to settle: %v", r.rec.ShortID, err)
		}
		row, ok := findSessionRow(rows, r.rec.SessionID)
		if !ok {
			t.Fatalf("%s left the listing after the stop; a wake needs its job entry", r.rec.ShortID)
		}
		if !row.running() {
			return
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			t.Fatalf("%s still shows a pid or a status after the stop", r.rec.ShortID)
		}
		time.Sleep(2 * time.Second)
	}
}

// savedOptions reads the options Claude Code saved for the session, from the
// real job state file under ~/.claude/jobs.
func (h *liveGateHarness) savedOptions(t *testing.T, r *liveGateReview) []string {
	t.Helper()
	opts, err := readSavedOptions(h.realJobsDir, r.rec.SessionID)
	if err != nil {
		t.Fatalf("reading the saved options of %s: %v", r.rec.ShortID, err)
	}
	return opts
}

// gate runs the production pre-wake gate. The digest is taken first, as
// preWake takes it, and the post-apply digest it returns is recorded for the
// next wake, as preWake records it with the marker.
func (h *liveGateHarness) gate(t *testing.T, r *liveGateReview, opts []string) {
	t.Helper()
	var stderr bytes.Buffer
	_, post, err := runReviewWakeGate(&stderr, r.inst, r.rec, true, opts, snapshotReviewSettings(r.inst))
	if s := strings.TrimSpace(stderr.String()); s != "" {
		t.Logf("live gate: the pre-wake gate wrote: %s", s)
	}
	if err != nil {
		t.Fatalf("the pre-wake gate refused the wake of %s: %v", r.rec.ShortID, err)
	}
	r.rec.SettingsDigest = post
}

// wakeAndCheck wakes the session with no flags through the production
// deliverer, waits for the woken turn to write its egress result, applies
// liveGateCopyCheck to one listing, and fails if the raw connect from inside
// the session succeeded. It returns the woken session's raw listing row (nil
// when the row isn't there) for the reach line.
func (h *liveGateHarness) wakeAndCheck(t *testing.T, ctx context.Context, r *liveGateReview, marker string) map[string]json.RawMessage {
	t.Helper()
	snap := workerSnapshot{View: workerView{
		SessionID:    r.rec.SessionID,
		ShortID:      r.rec.ShortID,
		InstancePath: r.inst,
		Root:         h.root,
		Spec:         claudeLaunchSpec(),
	}}
	if err := (resumeWakeDeliverer{spec: claudeLaunchSpec()}).Deliver(ctx, snap, liveGateProbePrompt(marker)); err != nil {
		t.Fatalf("waking %s: %v", r.rec.ShortID, err)
	}

	resultPath := filepath.Join(r.inst, marker)
	content := ""
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if b, err := os.ReadFile(resultPath); err == nil && len(bytes.TrimSpace(b)) > 0 {
			content = strings.TrimSpace(string(b))
			break
		}
		time.Sleep(3 * time.Second)
	}

	rows, raw, lerr := liveGateListing(ctx)
	if lerr != nil {
		t.Fatalf("listing sessions after the wake of %s: %v", r.rec.ShortID, lerr)
	}
	var wokenRow map[string]json.RawMessage
	for _, m := range raw {
		var sid string
		if json.Unmarshal(m["sessionId"], &sid) == nil && sid == r.rec.SessionID {
			wokenRow = m
			break
		}
	}
	if err := liveGateCopyCheck(rows, r.rec.SessionID, r.inst); err != nil {
		t.Fatalf("after the wake of %s: %v", r.rec.ShortID, err)
	}

	if content == "" {
		t.Fatalf("the woken session never wrote %s, so the block can't be confirmed (instance holds: %s)", marker, liveGateInstFiles(r.inst))
	}
	rawStatus, err := liveGateRawStatus(content)
	if err != nil {
		t.Fatalf("inconclusive: %s: %v (got %q)", marker, err, content)
	}
	if rawStatus == 0 {
		t.Fatalf("EGRESS NOT BLOCKED: a raw TCP connect to 1.1.1.1:443 succeeded from inside the woken session %s (%q)", r.rec.ShortID, content)
	}
	t.Logf("live gate: woken %s denied egress (%s)", r.rec.ShortID, content)
	return wokenRow
}

// liveGateRawStatus reads the probe's exit status from the last RAW= line of
// the session's result file. Only a number counts: a missing line or a value
// that isn't one (an agent's prose, an empty status) is an error, so the
// oracle never reads it as a denial.
func liveGateRawStatus(content string) (int, error) {
	raw, found := "", false
	for _, line := range strings.Split(content, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "RAW="); ok {
			raw, found = strings.TrimSpace(v), true
		}
	}
	if !found {
		return 0, errors.New("no RAW line")
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("RAW value %q is not a numeric exit status", raw)
	}
	return n, nil
}

// liveGateProbePrompt asks the woken session for one raw TCP connect to a
// literal IP (no DNS and no TLS, so neither can pass for a block) and for its
// exit status in marker.
func liveGateProbePrompt(marker string) string {
	return "You are testing a no-egress sandbox after being woken. Run exactly this one Bash command with the Bash tool: " +
		"timeout 5 bash -c 'echo > /dev/tcp/1.1.1.1/443' 2>/dev/null; printf 'RAW=%s' \"$?\"\n" +
		"Then use the Write tool to write a file named " + marker + " in the current directory whose only line is RAW=<the number the Bash command printed>. Then STOP."
}

// liveGatePlantCloneConfig writes, under the instance's PR clone, a project
// settings file whose SessionStart hook creates hookMarker and a .mcp.json
// whose stdio server creates mcpMarker.
func liveGatePlantCloneConfig(t *testing.T, inst, hookMarker, mcpMarker string) {
	t.Helper()
	clone := filepath.Join(inst, watch.DefaultCloneRelDir)
	if err := os.MkdirAll(filepath.Join(clone, ".claude"), 0o755); err != nil {
		t.Fatalf("creating the clone's .claude: %v", err)
	}
	settings := map[string]any{"hooks": map[string]any{"SessionStart": []any{
		map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "touch " + liveGateShellQuote(hookMarker)}}},
	}}}
	mcp := map[string]any{"mcpServers": map[string]any{"clone-marker": map[string]any{
		"type": "stdio", "command": "sh", "args": []string{"-c", "touch " + liveGateShellQuote(mcpMarker)},
	}}}
	for path, doc := range map[string]any{
		filepath.Join(clone, ".claude", "settings.json"): settings,
		filepath.Join(clone, ".mcp.json"):                mcp,
	} {
		b, err := json.Marshal(doc)
		if err != nil {
			t.Fatalf("encoding %s: %v", path, err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
}

func liveGateShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// liveGateListing runs `claude agents --json --all` once and decodes it both
// as rows and as raw objects, so the copy check and the reach line read the
// same listing.
func liveGateListing(ctx context.Context) ([]claudeSessionRow, []map[string]json.RawMessage, error) {
	c, cancel := context.WithTimeout(ctx, claudeListTimeout)
	defer cancel()
	out, err := exec.CommandContext(c, "claude", "agents", "--json", "--all").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("claude agents: %w", err)
	}
	var rows []claudeSessionRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, nil, errors.New("the listing isn't a JSON array of sessions")
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, nil, errors.New("the listing isn't a JSON array of objects")
	}
	return rows, raw, nil
}

// liveGateCopyCheck is the copy check the live gate applies to one listing
// taken after a wake. Among the rows rooted at inst (by rootedAt, the rule
// sessionsRootedAtInstance uses) it requires exactly one, and that one has
// to carry stagedID: a row with any other id there means the wake started a
// copy, and a missing staged row means the woken session isn't the staged
// one.
func liveGateCopyCheck(rows []claudeSessionRow, stagedID, inst string) error {
	staged := 0
	var others []string
	for _, r := range rows {
		if !rootedAt(r.Cwd, inst) {
			continue
		}
		if r.SessionID == stagedID {
			staged++
			continue
		}
		id := r.SessionID
		if id == "" {
			id = "<no session id>"
		}
		others = append(others, id)
	}
	if len(others) > 0 {
		return fmt.Errorf("a session other than the staged %s is rooted at the instance (%s): the wake started a copy", stagedID, strings.Join(others, ", "))
	}
	if staged == 0 {
		return fmt.Errorf("no listed session rooted at the instance carries the staged id %s", stagedID)
	}
	if staged > 1 {
		return fmt.Errorf("the staged id %s is listed %d times at the instance", stagedID, staged)
	}
	return nil
}

// appendLiveGateReport appends one line to the file NIWA_WATCH_LIVE_REPORT
// names, when it names one, and logs it either way. The workflow prints the
// file into the job log and checks it for the lines it requires.
func appendLiveGateReport(t *testing.T, line string) {
	t.Helper()
	t.Logf("live gate report: %s", line)
	path := os.Getenv("NIWA_WATCH_LIVE_REPORT")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("opening the live report: %v", err)
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, line); err != nil {
		t.Fatalf("writing the live report: %v", err)
	}
}

// liveGateClaudeVersion is the first line of `claude --version`.
func liveGateClaudeVersion(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		t.Fatalf("claude --version: %v", err)
	}
	v, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(v)
}

// liveGateSavedFlagsLine is the report line for the options Claude Code saved
// for a review stage: each element in order, quoted, except that a --settings
// value is replaced by its sorted top-level key list. The settings document
// carries environment values, so it is never written out.
func liveGateSavedFlagsLine(opts []string) string {
	out := make([]string, 0, len(opts))
	for i := 0; i < len(opts); i++ {
		o := opts[i]
		if v, ok := strings.CutPrefix(o, "--settings="); ok {
			out = append(out, strconv.Quote("--settings=")+liveGateSettingsKeys(v))
			continue
		}
		out = append(out, strconv.Quote(o))
		if o == "--settings" && i+1 < len(opts) {
			i++
			out = append(out, liveGateSettingsKeys(opts[i]))
		}
	}
	return "saved_flags_review_stage: " + strings.Join(out, " ")
}

// liveGateFieldName is the shape of a JSON key the report will name.
var liveGateFieldName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// liveGateSettingsKeys names a settings document by its top-level keys.
func liveGateSettingsKeys(doc string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &m); err != nil || m == nil {
		return "<settings value: not a JSON object>"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		if !liveGateFieldName.MatchString(k) {
			k = "<unnamed key>"
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "<settings keys: " + strings.Join(keys, ",") + ">"
}

// liveGateReachLine records whether claude.ai still reaches the woken
// session, from its listing row: the fields whose names look like a remote
// or bridge connection. A field with a set value (true, a non-empty string,
// a non-zero number, a non-empty object or array) makes it present; fields
// that are all unset make it absent; no such field, or no row, makes it
// unknown. Only field names are written, never a value. It is recorded, not
// asserted.
func liveGateReachLine(row map[string]json.RawMessage) string {
	const prefix = "claude_ai_reach_after_wake: "
	if row == nil {
		return prefix + "unknown (the woken session has no listing row)"
	}
	var names []string
	set := false
	for k, v := range row {
		lk := strings.ToLower(k)
		if !strings.Contains(lk, "remote") && !strings.Contains(lk, "bridge") &&
			!strings.Contains(lk, "claudeai") && !strings.Contains(lk, "claude_ai") &&
			!strings.Contains(lk, "claude.ai") && !strings.Contains(lk, "web") {
			continue
		}
		if !liveGateFieldName.MatchString(k) {
			k = "<unnamed field>"
		}
		names = append(names, k)
		switch strings.TrimSpace(string(v)) {
		case "", "null", "false", "0", `""`, "[]", "{}":
		default:
			set = true
		}
	}
	if len(names) == 0 {
		return prefix + "unknown (no remote or bridge field in the woken session's row)"
	}
	sort.Strings(names)
	state := "absent"
	if set {
		state = "present"
	}
	return prefix + state + " (fields: " + strings.Join(names, ", ") + ")"
}

// liveGateInstFiles lists the names directly in dir, for failure messages.
func liveGateInstFiles(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "<unreadable>"
	}
	if len(entries) == 0 {
		return "<empty>"
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ", ")
}

// TestLiveGateRawStatus runs the live gate's egress oracle parse offline: only
// a numeric RAW value is a verdict, and anything else is inconclusive rather
// than a denial.
func TestLiveGateRawStatus(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    int
		wantErr bool
	}{
		{"denied", "RAW=1", 1, false},
		{"timed out", "RAW=124", 124, false},
		{"connected", "RAW=0", 0, false},
		{"last line wins", "RAW=0\nRAW=1", 1, false},
		{"surrounding text", "probe done\n  RAW=1  \n", 1, false},
		{"no RAW line", "the probe ran", 0, true},
		{"empty value", "RAW=", 0, true},
		{"placeholder", "RAW=<the number the Bash command printed>", 0, true},
		{"prose", "RAW=blocked", 0, true},
		{"trailing text", "RAW=1 (denied)", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := liveGateRawStatus(tc.content)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("liveGateRawStatus(%q) = %d, nil; want an error", tc.content, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("liveGateRawStatus(%q) = %d, %v; want %d, nil", tc.content, got, err, tc.want)
			}
		})
	}
}

// TestLiveGateCopyCheck runs the live gate's copy check offline. A gate that
// passes against a copy fails here.
func TestLiveGateCopyCheck(t *testing.T) {
	parent := t.TempDir()
	inst := filepath.Join(parent, "inst")
	sibling := filepath.Join(parent, "inst-2")
	for _, d := range []string{inst, sibling, filepath.Join(inst, "pr-clone")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	const staged = "11111111-1111-4111-8111-111111111111"
	const other = "22222222-2222-4222-8222-222222222222"
	row := func(sid, cwd string) claudeSessionRow {
		return claudeSessionRow{ID: sid[:8], SessionID: sid, Cwd: cwd}
	}

	cases := []struct {
		name    string
		rows    []claudeSessionRow
		wantErr string
	}{
		{"one row with the staged id", []claudeSessionRow{row(staged, inst)}, ""},
		{"a session elsewhere is not counted", []claudeSessionRow{row(staged, inst), row(other, sibling)}, ""},
		{"a second session id rooted at the instance", []claudeSessionRow{row(staged, inst), row(other, inst)}, "started a copy"},
		{"a second session under the instance", []claudeSessionRow{row(staged, inst), row(other, filepath.Join(inst, "pr-clone"))}, "started a copy"},
		{"the woken row's id differs from the staged one", []claudeSessionRow{row(other, inst)}, "started a copy"},
		{"no staged row", []claudeSessionRow{row(staged, sibling)}, "carries the staged id"},
		{"empty listing", nil, "carries the staged id"},
		{"the staged id twice", []claudeSessionRow{row(staged, inst), row(staged, inst)}, "listed 2 times"},
		{"a rooted row with no session id", []claudeSessionRow{row(staged, inst), {ID: "abcdef12", Cwd: inst}}, "started a copy"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := liveGateCopyCheck(tc.rows, staged, inst)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("liveGateCopyCheck = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("liveGateCopyCheck = nil, want an error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("liveGateCopyCheck = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}

	// A path through a symlink to the instance is the instance.
	link := filepath.Join(parent, "link")
	if err := os.Symlink(inst, link); err != nil {
		t.Fatal(err)
	}
	if err := liveGateCopyCheck([]claudeSessionRow{row(staged, inst), row(other, link)}, staged, inst); err == nil {
		t.Fatal("a second session reached through a symlink to the instance passed the copy check")
	}
}

// TestLiveGateReportLines pins that the report lines name flags and fields
// only: a --settings value and a listing field's value never appear.
func TestLiveGateReportLines(t *testing.T) {
	const secret = "s3cr3t-value"
	doc := `{"env":{"NIWA_REAP_KEY":"` + secret + `"}}`

	line := liveGateSavedFlagsLine([]string{"--name", "watch-x", "--strict-mcp-config", "--settings", doc})
	want := `saved_flags_review_stage: "--name" "watch-x" "--strict-mcp-config" "--settings" <settings keys: env>`
	if line != want {
		t.Fatalf("saved flags line = %q, want %q", line, want)
	}
	joined := liveGateSavedFlagsLine([]string{"--settings=" + doc})
	if strings.Contains(joined, secret) || !strings.Contains(joined, "<settings keys: env>") {
		t.Fatalf("saved flags line for --settings=<doc> = %q", joined)
	}
	if got := liveGateSavedFlagsLine([]string{"--settings", secret}); strings.Contains(got, secret) {
		t.Fatalf("saved flags line quotes a non-JSON settings value: %q", got)
	}

	row := map[string]json.RawMessage{
		"sessionId":     json.RawMessage(`"` + secret + `"`),
		"remoteUrl":     json.RawMessage(`"https://claude.ai/` + secret + `"`),
		"bridgeEnabled": json.RawMessage(`false`),
	}
	reach := liveGateReachLine(row)
	if strings.Contains(reach, secret) {
		t.Fatalf("reach line quotes a value: %q", reach)
	}
	if want := "claude_ai_reach_after_wake: present (fields: bridgeEnabled, remoteUrl)"; reach != want {
		t.Fatalf("reach line = %q, want %q", reach, want)
	}
	if got := liveGateReachLine(map[string]json.RawMessage{"bridgeEnabled": json.RawMessage(`false`)}); got != "claude_ai_reach_after_wake: absent (fields: bridgeEnabled)" {
		t.Fatalf("reach line with only unset fields = %q", got)
	}
	if got := liveGateReachLine(map[string]json.RawMessage{"sessionId": json.RawMessage(`"x"`)}); !strings.HasPrefix(got, "claude_ai_reach_after_wake: unknown") {
		t.Fatalf("reach line with no candidate field = %q", got)
	}
	if got := liveGateReachLine(nil); !strings.HasPrefix(got, "claude_ai_reach_after_wake: unknown") {
		t.Fatalf("reach line with no row = %q", got)
	}
}
