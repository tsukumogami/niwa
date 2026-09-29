//go:build live

// The lineage live check proves the one fact dispatch lineage rests on that
// can't be settled by reading: an OTEL_RESOURCE_ATTRIBUTES value passed in a
// `--settings` env block reaches a `claude --bg` worker's exported telemetry,
// together with the attributes the developer's own user settings already set.
// A background worker runs in a process Claude Code's daemon started before
// the launch, so the launching process's environment is not a reliable
// channel; the settings document travels on the worker's command line.
//
// What it does, and what it never does:
//   - It starts a local OTLP/HTTP listener that records resource attribute KEYS
//     only. It never stores a request body, an attribute value, or a request
//     header.
//   - It launches one throwaway background worker whose `--settings` document
//     sends that worker's telemetry to the listener and replaces its headers
//     helper, so no credential header is produced for this launch. The
//     developer's settings files are not touched.
//   - It prints the lineage key names with a verdict each, and the developer's
//     own attribute keys only as a count.
//
// Run it by hand on a machine with a logged-in `claude`. A background session
// only starts in a directory Claude Code trusts, so name a trusted parent with
// no project settings of its own:
//
//	NIWA_LIVE_PROBE_PARENT=<trusted dir> go test -tags live -count=1 \
//	  -run TestLineageAttributesReachBackgroundWorker -v ./test/live/
//
// Setting NIWA_LIVE_RESTART_DAEMON=1 adds a second arm that stops and restarts
// the background daemon and checks again. Stopping the daemon interrupts every
// live background session on the machine, so that arm refuses to run while any
// other background session is listed.
package live

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// lineageProbeKeys are the six lineage attribute names the worker must carry,
// with fixed test values. The values are test data, not real identities.
var lineageProbeKeys = []struct{ key, value string }{
	{"niwa.dispatch.id", "00000000-0000-4000-8000-000000000001"},
	{"niwa.dispatch.slug", "lineage-probe"},
	{"niwa.parent.session.id", "lineage-probe-parent"},
	{"niwa.requested.skill", "probe:lineage"},
	{"niwa.brief.id", "sha256:0000000000000000000000000000000000000000000000000000000000000000"},
	{"niwa.workspace", "lineage-probe"},
}

// lineageProbePrompt keeps the worker's turn as cheap as possible.
const lineageProbePrompt = "reply with the single word OK and stop"

// lineageArrivalBudget bounds how long the test waits for the worker's first
// exports; the export interval is shortened to a second, so a healthy worker
// reports well inside it.
const lineageArrivalBudget = 150 * time.Second

// lineageVersionLine is how niwa's lineage guide records the Claude Code
// version this check last passed on.
var lineageVersionLine = regexp.MustCompile(`last passed on Claude Code (\d+\.\d+\.\d+)`)

func TestLineageAttributesReachBackgroundWorker(t *testing.T) {
	claudeBin := requireLiveClaude(t)

	version := claudeVersion(t, claudeBin)
	reportVersion(t, version)

	userKeys, userValue := userResourceAttributes(t)
	t.Logf("user settings attributes: %d key(s) to carry", len(userKeys))

	sink := newKeySink()
	server := httptest.NewServer(sink)
	defer server.Close()

	probeDir := lineageProbeDir(t)
	doc := lineageSettingsDocument(t, server.URL, composeProbeValue(userValue))

	shortID := launchProbeWorker(t, claudeBin, probeDir, doc)
	t.Cleanup(func() {
		stopSession(t, claudeBin, shortID)
		deleteSession(t, claudeBin, shortID)
	})

	checkArrivals(t, "first run", sink, userKeys)

	if os.Getenv("NIWA_LIVE_RESTART_DAEMON") != "1" {
		t.Log("daemon restart arm: not requested (set NIWA_LIVE_RESTART_DAEMON=1)")
		return
	}
	if others := otherBackgroundSessions(t, claudeBin, shortID); others > 0 {
		t.Skipf("daemon restart arm: refused, %d other background session(s) are running and a restart would interrupt them", others)
	}
	restartDaemon(t, claudeBin)
	sink.reset()
	resumeProbeWorker(t, claudeBin, shortID)
	checkArrivals(t, "after daemon restart", sink, userKeys)
}

// keySink is the OTLP/HTTP JSON listener. It keeps a set of the attribute keys
// it has seen and whether any export arrived at all.
type keySink struct {
	mu       sync.Mutex
	keys     map[string]bool
	requests int
}

func newKeySink() *keySink { return &keySink{keys: map[string]bool{}} }

func (s *keySink) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = map[string]bool{}
	s.requests = 0
}

func (s *keySink) snapshot() (map[string]bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.keys))
	for k := range s.keys {
		out[k] = true
	}
	return out, s.requests
}

// ServeHTTP reads only the resource attribute keys out of an OTLP JSON
// metrics or logs export. Headers are never read, and nothing is stored but
// the key names.
func (s *keySink) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer w.WriteHeader(http.StatusOK)
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return
		}
		defer zr.Close()
		body = zr
	}
	var payload struct {
		ResourceMetrics []otlpResource `json:"resourceMetrics"`
		ResourceLogs    []otlpResource `json:"resourceLogs"`
	}
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests++
	for _, set := range [][]otlpResource{payload.ResourceMetrics, payload.ResourceLogs} {
		for _, res := range set {
			for _, attr := range res.Resource.Attributes {
				s.keys[attr.Key] = true
			}
		}
	}
}

type otlpResource struct {
	Resource struct {
		Attributes []struct {
			Key string `json:"key"`
		} `json:"attributes"`
	} `json:"resource"`
}

// userResourceAttributes reads the developer's own OTEL_RESOURCE_ATTRIBUTES
// from their user settings env block: its keys, and the raw value to carry.
// Only the env object is decoded, and neither is ever printed.
func userResourceAttributes(t *testing.T) ([]string, string) {
	t.Helper()
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("resolving the home directory: %v", err)
		}
		dir = filepath.Join(home, ".claude")
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Log("user settings: no settings file, so no user attributes to carry")
		return nil, ""
	}
	var doc struct {
		Env map[string]json.RawMessage `json:"env"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal("user settings: the file is not valid JSON")
	}
	raw, ok := doc.Env["OTEL_RESOURCE_ATTRIBUTES"]
	if !ok {
		return nil, ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal("user settings: env.OTEL_RESOURCE_ATTRIBUTES is not a string")
	}
	var keys []string
	for _, entry := range strings.Split(value, ",") {
		if strings.TrimSpace(entry) == "" {
			continue
		}
		key, _, found := strings.Cut(entry, "=")
		if !found || strings.TrimSpace(key) == "" {
			t.Fatal("user settings: env.OTEL_RESOURCE_ATTRIBUTES has an entry that isn't key=value")
		}
		keys = append(keys, strings.TrimSpace(key))
	}
	return keys, value
}

// composeProbeValue is the user's value, carried as found, followed by the six
// lineage test entries.
func composeProbeValue(userValue string) string {
	parts := make([]string, 0, len(lineageProbeKeys)+1)
	if strings.TrimSpace(userValue) != "" {
		parts = append(parts, userValue)
	}
	for _, kv := range lineageProbeKeys {
		parts = append(parts, kv.key+"="+kv.value)
	}
	return strings.Join(parts, ",")
}

// lineageSettingsDocument is the worker's `--settings` document: telemetry on,
// both signals sent to the listener over http/json with short intervals, the
// composed attributes, and a headers helper that produces no header.
func lineageSettingsDocument(t *testing.T, listener, attributes string) string {
	t.Helper()
	doc := map[string]any{
		"env": map[string]string{
			"CLAUDE_CODE_ENABLE_TELEMETRY":        "1",
			"OTEL_METRICS_EXPORTER":               "otlp",
			"OTEL_LOGS_EXPORTER":                  "otlp",
			"OTEL_EXPORTER_OTLP_PROTOCOL":         "http/json",
			"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL": "http/json",
			"OTEL_EXPORTER_OTLP_LOGS_PROTOCOL":    "http/json",
			"OTEL_EXPORTER_OTLP_ENDPOINT":         listener,
			"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT": listener + "/v1/metrics",
			"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT":    listener + "/v1/logs",
			"OTEL_METRIC_EXPORT_INTERVAL":         "1000",
			"OTEL_LOGS_EXPORT_INTERVAL":           "1000",
			"OTEL_RESOURCE_ATTRIBUTES":            attributes,
		},
		"otelHeadersHelper": "printf '{}'",
	}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encoding the settings document: %v", err)
	}
	return string(out)
}

// lineageProbeDir is a fresh directory the worker starts in, removed after. A
// background session refuses to start in a directory Claude Code doesn't
// trust, so NIWA_LIVE_PROBE_PARENT can name a trusted parent to create it
// under; without it the directory goes under this package. Pick a parent with
// no project settings of its own, so nothing but this test's settings apply.
func lineageProbeDir(t *testing.T) string {
	t.Helper()
	parent := os.Getenv("NIWA_LIVE_PROBE_PARENT")
	if parent == "" {
		wd, err := os.Getwd()
		if err != nil {
			t.Fatalf("getwd: %v", err)
		}
		parent = wd
	}
	dir, err := os.MkdirTemp(parent, "lineage-probe-")
	if err != nil {
		t.Fatalf("creating the probe directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// launchProbeWorker starts the background worker and returns its short handle.
func launchProbeWorker(t *testing.T, claudeBin, dir, settings string) string {
	t.Helper()
	cmd := exec.Command(claudeBin, "--bg", "--settings", settings, "--", lineageProbePrompt)
	cmd.Dir = dir
	cmd.Stdin = nil
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("claude --bg: %v\n%s", err, out.String())
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		for _, rec := range claudeAgentRecords(t, claudeBin) {
			cwd, _ := rec["cwd"].(string)
			if cwd != "" && samePath(cwd, dir) {
				if short, _ := rec["id"].(string); short != "" {
					return short
				}
			}
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("no background session appeared for the probe directory")
	return ""
}

// checkArrivals waits for every lineage key and every user key, then reports
// each lineage key by name and the user keys as a count.
func checkArrivals(t *testing.T, arm string, sink *keySink, userKeys []string) {
	t.Helper()
	want := map[string]bool{}
	for _, kv := range lineageProbeKeys {
		want[kv.key] = true
	}
	for _, k := range userKeys {
		want[k] = true
	}
	deadline := time.Now().Add(lineageArrivalBudget)
	var seen map[string]bool
	var requests int
	for {
		seen, requests = sink.snapshot()
		missing := 0
		for k := range want {
			if !seen[k] {
				missing++
			}
		}
		if missing == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("%s: %d export request(s) received", arm, requests)
	failed := false
	names := make([]string, 0, len(lineageProbeKeys))
	for _, kv := range lineageProbeKeys {
		names = append(names, kv.key)
	}
	sort.Strings(names)
	for _, name := range names {
		verdict := "arrived"
		if !seen[name] {
			verdict = "MISSING"
			failed = true
		}
		t.Logf("%s: %s %s", arm, name, verdict)
	}
	arrived := 0
	for _, k := range userKeys {
		if seen[k] {
			arrived++
		}
	}
	t.Logf("%s: user settings keys %d of %d arrived", arm, arrived, len(userKeys))
	if arrived != len(userKeys) {
		t.Errorf("%s: a user settings key did not arrive", arm)
	}
	if failed {
		t.Errorf("%s: a lineage key did not arrive", arm)
	}
}

// claudeVersion returns the version `claude --version` reports.
func claudeVersion(t *testing.T, claudeBin string) string {
	t.Helper()
	out, err := exec.Command(claudeBin, "--version").Output()
	if err != nil {
		t.Fatalf("claude --version: %v", err)
	}
	m := regexp.MustCompile(`\d+\.\d+\.\d+`).FindString(string(out))
	if m == "" {
		t.Fatalf("claude --version printed no version")
	}
	return m
}

// reportVersion compares the running Claude Code with the version niwa's
// lineage guide records as last verified, and says which case holds.
func reportVersion(t *testing.T, version string) {
	t.Helper()
	guide := filepath.Join("..", "..", "docs", "guides", "dispatch-lineage.md")
	data, err := os.ReadFile(guide)
	if err != nil {
		t.Logf("Claude Code %s; no version recorded yet (the lineage guide doesn't exist)", version)
		return
	}
	m := lineageVersionLine.FindStringSubmatch(string(data))
	switch {
	case m == nil:
		t.Logf("Claude Code %s; the lineage guide records no verified version yet", version)
	case m[1] == version:
		t.Logf("Claude Code %s; matches the version the lineage guide records", version)
	default:
		t.Logf("Claude Code %s; DIFFERS from %s, the version the lineage guide records -- update the guide if this run passes", version, m[1])
	}
}

// otherBackgroundSessions counts background sessions other than the probe.
func otherBackgroundSessions(t *testing.T, claudeBin, probeID string) int {
	t.Helper()
	n := 0
	for _, rec := range claudeAgentRecords(t, claudeBin) {
		if id, _ := rec["id"].(string); id != "" && id != probeID {
			n++
		}
	}
	return n
}

// restartDaemon stops the background daemon; the next `claude` call that needs
// it starts a fresh one, which adopts the still-running worker.
func restartDaemon(t *testing.T, claudeBin string) {
	t.Helper()
	if out, err := exec.Command(claudeBin, "daemon", "stop", "--keep-workers").CombinedOutput(); err != nil {
		t.Fatalf("claude daemon stop: %v\n%s", err, out)
	}
	if out, err := exec.Command(claudeBin, "daemon", "status").CombinedOutput(); err != nil {
		t.Logf("claude daemon status after stop: %v\n%s", err, out)
	}
}

// resumeProbeWorker gives the idle worker one more short turn through the
// daemon, so its telemetry is exported again from the adopted process.
func resumeProbeWorker(t *testing.T, claudeBin, shortID string) {
	t.Helper()
	cmd := exec.Command(claudeBin, "--bg", "--resume", shortID, "--", fmt.Sprintf("%s again", lineageProbePrompt))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("resuming the probe worker through the daemon: %v\n%s", err, out)
	}
}
