package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"
)

// session_message_steps_test.go holds the step definitions for session-message
// acceptance: the `niwa dispatch --accept-session-messages` flag, the
// `[global] accept_session_messages_on_dispatch` machine key, and everything
// each one leaves behind -- the settings document the worker is launched with,
// the stderr audit and override lines, the one-time explanation and its marker,
// the durable mapping, and what `niwa list` reports.
//
// They ride the dispatch suite's fake claude and read the per-element argv file
// it records, because the question they answer is which argv element follows
// `--settings` and which follows the `--` prompt separator; see that fake in
// dispatch_steps_test.go for both recordings and what they cost.
//
// features/session-message-acceptance.feature is where these steps are used,
// and its description says what this coverage deliberately leaves to the PRD's
// manual delivery check.

// sessionMessageNoticeMarker is the file `niwa dispatch` creates beside
// config.toml once it has shown the one-time explanation at a terminal. The
// name is the whole record: the file is empty. It is spelled out here rather
// than imported because internal/cli keeps it unexported, and a functional
// scenario asserting a path the developer would see should name it.
const sessionMessageNoticeMarker = "accept-session-messages-notice"

// niwaConfigDir is the directory holding the machine config.toml for a
// scenario, which is where the notice marker lands too. buildEnv points
// XDG_CONFIG_HOME at <home>/.config, so this follows it.
func niwaConfigDir(s *testState) string {
	return filepath.Join(s.homeDir, ".config", "niwa")
}

// machineConfigPath is the scenario's $XDG_CONFIG_HOME/niwa/config.toml.
func machineConfigPath(s *testState) string {
	return filepath.Join(niwaConfigDir(s), "config.toml")
}

// theNiwaMachineConfigGlobalTableContains splices the given lines into the
// [global] table of the machine config, creating the table when the file has
// none.
//
// It edits the file as text rather than round-tripping it through the config
// decoder for two reasons. A decode-and-re-encode would drop any key this build
// does not know, which is the opposite of what a fixture wants; and the whole
// point of several of these scenarios is that the key sits in the same [global]
// table `niwa init` already wrote its registry alongside. A second [global]
// header would make the file invalid TOML, which turns the behavior silently
// off -- a fixture that passed for the wrong reason.
func theNiwaMachineConfigGlobalTableContains(ctx context.Context, body *godog.DocString) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := machineConfigPath(s)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading machine config %s: %w", path, err)
	}
	added := strings.TrimRight(body.Content, "\n")

	existing := string(data)
	lines := strings.Split(existing, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "[global]" {
			continue
		}
		merged := append([]string{}, lines[:i+1]...)
		merged = append(merged, strings.Split(added, "\n")...)
		merged = append(merged, lines[i+1:]...)
		return writeMachineConfig(s, strings.Join(merged, "\n"))
	}
	// No [global] table yet, so one goes at the very top. That is safe only
	// because niwa writes no root-level keys into config.toml -- every key it
	// writes is inside [global], [global_config] or [registry.*]. A root-level
	// key would be swallowed by the header prepended above it, since keys after
	// a table header belong to that table.
	return writeMachineConfig(s, "[global]\n"+added+"\n\n"+existing)
}

// theNiwaMachineConfigIsReplacedWith writes the machine config verbatim. It is
// what the fixtures whose whole point is a file niwa cannot parse need, and
// also what a scenario running against a scaffolded workspace needs to bring
// the file into existence at all -- scaffold mode writes no registry entry, so
// nothing else creates it.
func theNiwaMachineConfigIsReplacedWith(ctx context.Context, body *godog.DocString) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	return writeMachineConfig(s, body.Content)
}

// writeMachineConfig writes the machine config, creating its directory. 0o600
// is the mode niwa's own writer uses.
func writeMachineConfig(s *testState, content string) error {
	path := machineConfigPath(s)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating the niwa config directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("writing machine config %s: %w", path, err)
	}
	return nil
}

// theNiwaMachineConfigIsNotReadable makes the machine config unreadable, the
// shape in which the whole [global] table -- not only this key -- goes missing.
// The file stays removable (its directory is untouched), so the sandbox tears
// down normally.
func theNiwaMachineConfigIsNotReadable(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := machineConfigPath(s)
	if err := os.Chmod(path, 0o000); err != nil {
		return fmt.Errorf("making machine config %s unreadable: %w", path, err)
	}
	return nil
}

// theNiwaConfigDirectoryIsNotWritable makes the directory holding config.toml
// read-only, so the notice marker cannot be created there. The After hook
// restores it: a sandbox holding an unwritable directory cannot be removed.
func theNiwaConfigDirectoryIsNotWritable(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir := niwaConfigDir(s)
	if err := os.Chmod(dir, 0o555); err != nil {
		return fmt.Errorf("making %s read-only: %w", dir, err)
	}
	s.restoreOnCleanup = append(s.restoreOnCleanup, dir)
	return nil
}

// iRecordTheNiwaMachineConfig snapshots config.toml so a later step can prove a
// dispatch left it alone.
func iRecordTheNiwaMachineConfig(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	data, err := os.ReadFile(machineConfigPath(s))
	if err != nil {
		return fmt.Errorf("recording machine config: %w", err)
	}
	s.rememberedMachineConfig = data
	return nil
}

// theNiwaMachineConfigIsByteForByteUnchanged asserts the recorded config.toml
// is still exactly what it was. niwa remembers the one-time explanation in a
// marker file beside this one; it never rewrites a developer's configuration to
// record something it printed.
func theNiwaMachineConfigIsByteForByteUnchanged(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.rememberedMachineConfig == nil {
		return fmt.Errorf("no machine config recorded; use 'I record the niwa machine config' first")
	}
	data, err := os.ReadFile(machineConfigPath(s))
	if err != nil {
		return fmt.Errorf("re-reading machine config: %w", err)
	}
	if string(data) != string(s.rememberedMachineConfig) {
		return fmt.Errorf("machine config changed.\nbefore:\n%s\nafter:\n%s", s.rememberedMachineConfig, data)
	}
	return nil
}

// launchedClaudeArgvElements returns the argv the fake claude recorded on its
// --bg launch, one element per entry. The fake writes them NUL-separated, which
// is the only separator an argv element cannot contain.
func launchedClaudeArgvElements(s *testState) ([]string, error) {
	path := filepath.Join(s.homeDir, "dispatch-launch-argv-elements")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading launched claude argv elements %s: %w\nstdout:\n%s\nstderr:\n%s", path, err, s.stdout, s.stderr)
	}
	elems := strings.Split(string(data), "\x00")
	// printf terminates every element, so the split leaves one empty tail.
	if n := len(elems); n > 0 && elems[n-1] == "" {
		elems = elems[:n-1]
	}
	return elems, nil
}

// launchedClaudeSettingsDocument returns the settings document the launch
// carried and whether there was one.
//
// It looks only before the `--` prompt separator, which is what makes the
// answer trustworthy for a prompt that itself begins with `--settings=`: past
// the separator nothing is a flag, so a prompt can never be mistaken for the
// document. More than one `--settings` before the separator is an error rather
// than a pick, because then the worker's own precedence decides which document
// wins and this step would be asserting against the wrong one.
func launchedClaudeSettingsDocument(s *testState) (string, bool, error) {
	elems, err := launchedClaudeArgvElements(s)
	if err != nil {
		return "", false, err
	}
	end := len(elems)
	for i, e := range elems {
		if e == "--" {
			end = i
			break
		}
	}
	found, count := -1, 0
	for i := 0; i < end; i++ {
		if elems[i] != "--settings" {
			continue
		}
		count++
		if found < 0 {
			found = i
		}
	}
	if count > 1 {
		return "", false, fmt.Errorf("the launch carried %d --settings elements before the prompt separator; exactly one document is expected:\n%q", count, elems[:end])
	}
	if found < 0 {
		return "", false, nil
	}
	if found+1 >= end {
		return "", false, fmt.Errorf("--settings is the last element before the prompt separator, so it names no document:\n%q", elems)
	}
	return elems[found+1], true, nil
}

// launchedClaudeSettingsKey decodes the launch's settings document and returns
// the named key's value and whether it was present.
func launchedClaudeSettingsKey(s *testState, key string) (any, bool, error) {
	doc, ok, err := launchedClaudeSettingsDocument(s)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		return nil, false, fmt.Errorf("the launch settings document is not JSON: %w\ndocument: %s", err, doc)
	}
	v, present := parsed[key]
	return v, present, nil
}

// theLaunchedClaudeSettingsHasCrossSessionInbound asserts the launch settings
// document carries crossSessionInbound with the given value.
func theLaunchedClaudeSettingsHasCrossSessionInbound(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	v, present, err := launchedClaudeSettingsKey(s, "crossSessionInbound")
	if err != nil {
		return err
	}
	if !present {
		doc, hasDoc, _ := launchedClaudeSettingsDocument(s)
		if !hasDoc {
			return fmt.Errorf("the launch carried no --settings document at all; want crossSessionInbound %q", want)
		}
		return fmt.Errorf("the launch settings document has no crossSessionInbound; want %q\ndocument: %s", want, doc)
	}
	if got, ok := v.(string); !ok || got != want {
		return fmt.Errorf("crossSessionInbound = %v; want %q", v, want)
	}
	return nil
}

// theLaunchedClaudeSettingsHasNoCrossSessionInbound asserts the behavior stayed
// off. Off is expressed by leaving the key out, so a launch with no settings
// document at all passes too -- that is what a dispatch with nothing to say
// looks like.
func theLaunchedClaudeSettingsHasNoCrossSessionInbound(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	v, present, err := launchedClaudeSettingsKey(s, "crossSessionInbound")
	if err != nil {
		return err
	}
	if present {
		return fmt.Errorf("the launch settings document carries crossSessionInbound = %v; it must be absent", v)
	}
	return nil
}

// theLaunchedClaudeSettingsHasRemoteControlAtStartup asserts the same single
// document also carries remote control, which is how the coexistence scenario
// shows the two contributors share one document rather than sending two.
func theLaunchedClaudeSettingsHasRemoteControlAtStartup(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	v, present, err := launchedClaudeSettingsKey(s, "remoteControlAtStartup")
	if err != nil {
		return err
	}
	if !present {
		// The document and stderr both go in the message. Remote control
		// declines to inject for reasons that have nothing to do with the
		// settings document -- an ANTHROPIC_API_KEY forcing API-key auth is the
		// one that has already cost a debugging session -- and it says so on
		// stderr. A failure naming only the missing key sends the reader to the
		// wrong half of the system.
		doc, hasDoc, _ := launchedClaudeSettingsDocument(s)
		if !hasDoc {
			return fmt.Errorf("the launch carried no --settings document at all; want remoteControlAtStartup true\nstderr:\n%s", s.stderr)
		}
		return fmt.Errorf("the launch settings document has no remoteControlAtStartup\ndocument: %s\nstderr:\n%s", doc, s.stderr)
	}
	if b, ok := v.(bool); !ok || !b {
		return fmt.Errorf("remoteControlAtStartup = %v; want true", v)
	}
	return nil
}

// theLaunchedClaudePromptIs asserts the prompt reached the worker as the single
// element right after the `--` separator, and that nothing follows it.
func theLaunchedClaudePromptIs(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	elems, err := launchedClaudeArgvElements(s)
	if err != nil {
		return err
	}
	sep := -1
	for i, e := range elems {
		if e == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return fmt.Errorf("the launch argv has no -- prompt separator:\n%q", elems)
	}
	if sep+1 != len(elems)-1 {
		return fmt.Errorf("the prompt separator is followed by %d elements; want exactly 1:\n%q", len(elems)-sep-1, elems)
	}
	if got := elems[sep+1]; got != want {
		return fmt.Errorf("launched prompt = %q; want %q", got, want)
	}
	return nil
}

// noticeMarkerPath is the one-time explanation's marker, beside config.toml.
func noticeMarkerPath(s *testState) string {
	return filepath.Join(niwaConfigDir(s), sessionMessageNoticeMarker)
}

// theSessionMessageNoticeHasAlreadyBeenShown creates the marker by hand, which
// is exactly what a developer who never wants the paragraph would do: the name
// is the whole record, so an empty file suppresses it. A scenario reaches for
// this when the explanation would otherwise drown out what it is asserting.
func theSessionMessageNoticeHasAlreadyBeenShown(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir := niwaConfigDir(s)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating the niwa config directory: %w", err)
	}
	if err := os.WriteFile(noticeMarkerPath(s), nil, 0o600); err != nil {
		return fmt.Errorf("creating the notice marker: %w", err)
	}
	return nil
}

// theSessionMessageNoticeMarkerExists asserts niwa remembered having shown the
// explanation. Lstat, not Stat: the marker is present when the name is taken,
// whatever it points at.
func theSessionMessageNoticeMarkerExists(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := noticeMarkerPath(s)
	if _, err := os.Lstat(path); err != nil {
		return fmt.Errorf("expected the notice marker at %s: %w\nstderr:\n%s", path, err, s.stderr)
	}
	return nil
}

// theSessionMessageNoticeMarkerDoesNotExist is its negation.
func theSessionMessageNoticeMarkerDoesNotExist(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := noticeMarkerPath(s)
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("the notice marker at %s exists; expected none", path)
	}
	return nil
}

// readMappingInbound decodes a session mapping and returns its
// accepts_session_messages value plus the raw JSON, for the omission assertion.
func readMappingInbound(s *testState, sessionID string) (bool, string, error) {
	path := filepath.Join(s.workspaceRoot, ".niwa", "sessions", sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, "", fmt.Errorf("expected dispatch mapping at %s: %w\nstdout:\n%s\nstderr:\n%s", path, err, s.stdout, s.stderr)
	}
	var m struct {
		AcceptsSessionMessages bool `json:"accepts_session_messages"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return false, "", fmt.Errorf("parsing mapping %s: %w", path, err)
	}
	return m.AcceptsSessionMessages, string(data), nil
}

// theDispatchMappingRecordsInbound asserts the durable mapping says this worker
// was launched accepting messages from other sessions.
func theDispatchMappingRecordsInbound(ctx context.Context, sessionID string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	on, raw, err := readMappingInbound(s, sessionID)
	if err != nil {
		return err
	}
	if !on {
		return fmt.Errorf("mapping for %s does not record session-message acceptance:\n%s", sessionID, raw)
	}
	return nil
}

// theDispatchMappingDoesNotRecordInbound asserts the mapping omits the key
// entirely. The field is omitempty, so a dispatch that did not turn the
// behavior on leaves a mapping byte-identical to one written before the field
// existed -- asserting false would pass against a mapping that carried it.
func theDispatchMappingDoesNotRecordInbound(ctx context.Context, sessionID string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	on, raw, err := readMappingInbound(s, sessionID)
	if err != nil {
		return err
	}
	if on || strings.Contains(raw, "accepts_session_messages") {
		return fmt.Errorf("mapping for %s must omit accepts_session_messages:\n%s", sessionID, raw)
	}
	return nil
}

// dispatchMappingsRecordingInbound counts the mappings in the workspace root's
// session store that record acceptance, and returns the session ids they name.
//
// The store is per workspace root, not per instance: WriteSessionMapping writes
// every dispatch mapping there, so parallel dispatches all land side by side and
// counting files is counting dispatches.
func dispatchMappingsRecordingInbound(s *testState) ([]string, error) {
	dir := filepath.Join(s.workspaceRoot, ".niwa", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading sessions dir %s: %w", dir, err)
	}
	var ids []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("reading mapping %s: %w", e.Name(), err)
		}
		var m struct {
			SessionID              string `json:"session_id"`
			AcceptsSessionMessages bool   `json:"accepts_session_messages"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("parsing mapping %s: %w", e.Name(), err)
		}
		if m.AcceptsSessionMessages {
			ids = append(ids, m.SessionID)
		}
	}
	return ids, nil
}

// thereAreNDispatchMappingsRecordingInbound asserts exactly n mappings record
// acceptance and that they name n distinct sessions. The distinctness half is
// what proves four parallel dispatches produced four workers rather than one
// mapping overwritten four times.
func thereAreNDispatchMappingsRecordingInbound(ctx context.Context, want int) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	ids, err := dispatchMappingsRecordingInbound(s)
	if err != nil {
		return err
	}
	if len(ids) != want {
		return fmt.Errorf("%d dispatch mappings record session-message acceptance; want %d (ids: %v)", len(ids), want, ids)
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return fmt.Errorf("two mappings name the same session %q; want %d distinct sessions (ids: %v)", id, want, ids)
		}
		seen[id] = true
	}
	return nil
}

// dispatchListRecord returns the dispatch instance's record from the last
// command's stdout, parsed as `niwa list --json`.
//
// It decodes into a map rather than a struct so a step can tell an absent key
// from one present and false. The distinction is the whole point for
// accepts_session_messages, which is documented as being on every record: a
// struct would decode a missing key to false and report the field as working.
func dispatchListRecord(s *testState) (map[string]any, error) {
	var records []map[string]any
	if err := json.Unmarshal([]byte(s.stdout), &records); err != nil {
		return nil, fmt.Errorf("parsing niwa list --json output: %w\nstdout:\n%s", err, s.stdout)
	}
	for _, r := range records {
		name, _ := r["name"].(string)
		if dispatchInstanceNameRe.MatchString(name) {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no dispatch instance in niwa list --json output:\n%s", s.stdout)
}

// theListJSONReportsInstanceAccepting asserts the record says the instance's
// dispatched session accepts messages from other sessions.
func theListJSONReportsInstanceAccepting(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	rec, err := dispatchListRecord(s)
	if err != nil {
		return err
	}
	v, present := rec["accepts_session_messages"]
	if !present {
		return fmt.Errorf("the dispatch instance record has no accepts_session_messages key:\n%s", s.stdout)
	}
	if on, ok := v.(bool); !ok || !on {
		return fmt.Errorf("accepts_session_messages = %v; want true", v)
	}
	return nil
}

// theListJSONReportsInstanceNotAccepting asserts the key is there and false.
// Presence matters: the field is on every record, so a build that dropped it
// would otherwise pass this by saying nothing.
func theListJSONReportsInstanceNotAccepting(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	rec, err := dispatchListRecord(s)
	if err != nil {
		return err
	}
	v, present := rec["accepts_session_messages"]
	if !present {
		return fmt.Errorf("the dispatch instance record has no accepts_session_messages key; it is reported on every record:\n%s", s.stdout)
	}
	if on, ok := v.(bool); !ok || on {
		return fmt.Errorf("accepts_session_messages = %v; want false", v)
	}
	return nil
}

// countLinesContaining counts the lines of text that contain the substring.
func countLinesContaining(text, want string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, want) {
			n++
		}
	}
	return n
}

// theErrorOutputHasExactlyNLinesContaining asserts how many stderr lines carry
// the substring. Counting rather than "contains" is what pins the one-time-ness
// of the audit line: shown once per dispatch, not once per contributor that
// looked at the setting, and with a count of 0 it is the "no such line"
// assertion.
func theErrorOutputHasExactlyNLinesContaining(ctx context.Context, want int, needle string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if got := countLinesContaining(s.stderr, needle); got != want {
		return fmt.Errorf("%d error-output lines contain %q; want %d\nstderr:\n%s", got, needle, want, s.stderr)
	}
	return nil
}

// theErrorOutputContainsTheText is the docstring form of "the error output
// contains", for the sentences that carry double quotes of their own and so
// cannot travel through a quoted step argument.
func theErrorOutputContainsTheText(ctx context.Context, body *godog.DocString) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	want := strings.TrimRight(body.Content, "\n")
	if !strings.Contains(s.stderr, want) {
		return fmt.Errorf("error output does not contain:\n%s\nstderr:\n%s", want, s.stderr)
	}
	return nil
}

// theErrorOutputDoesNotContainTheText is its negation.
func theErrorOutputDoesNotContainTheText(ctx context.Context, body *godog.DocString) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	unwanted := strings.TrimRight(body.Content, "\n")
	if strings.Contains(s.stderr, unwanted) {
		return fmt.Errorf("error output unexpectedly contains:\n%s\nstderr:\n%s", unwanted, s.stderr)
	}
	return nil
}

// iRememberTheDispatchStandardOutput keeps a dispatch's stdout for comparison.
func iRememberTheDispatchStandardOutput(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	s.rememberedStdout = s.stdout
	return nil
}

// dispatchInstanceNameInTextRe finds a dispatch instance name anywhere inside a
// block of output, including in the middle of a path. It is
// dispatchInstanceNameRe's shape without the end-of-text anchor that one needs
// to classify a directory entry, plus the configuration name in front of the
// "+" so the whole name -- not only its tail -- is replaced.
var dispatchInstanceNameInTextRe = regexp.MustCompile(`[A-Za-z0-9_.-]*\+[a-z0-9_]*-[0-9a-f]{8}`)

// sessionIdentifierRe matches a full UUID and the 8-hex short form a dispatch
// prints, so both can be blanked before two runs' stdout are compared.
//
// The 8-hex half is deliberately broad: it blanks any bare eight hex digits,
// not only a short session id. Over-blanking only weakens the comparison, while
// a pattern narrow enough to miss one identifier would make two identical runs
// look different and fail the scenario for the wrong reason.
var sessionIdentifierRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|\b[0-9a-f]{8}\b`)

// normalizeDispatchStdout replaces the two things that legitimately differ
// between two dispatches -- the instance name and the session identifiers --
// with placeholders, so what is left is the output's shape.
//
// Instance names go first: each ends in eight hex digits that the session-id
// pattern would otherwise claim, and replacing the whole name is the more
// specific of the two.
func normalizeDispatchStdout(text string) string {
	text = dispatchInstanceNameInTextRe.ReplaceAllString(text, "<instance>")
	return sessionIdentifierRe.ReplaceAllString(text, "<session>")
}

// theDispatchStandardOutputMatchesTheRememberedOne asserts that turning the
// behavior on changed nothing a script reading stdout would see. The audit line
// and the explanation are stderr; the resume commands and the dispatched-session
// block on stdout are what a developer copies, and they must be identical.
func theDispatchStandardOutputMatchesTheRememberedOne(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.rememberedStdout == "" {
		return fmt.Errorf("no standard output remembered; use 'I remember the dispatch standard output' first")
	}
	before := normalizeDispatchStdout(s.rememberedStdout)
	after := normalizeDispatchStdout(s.stdout)
	if before != after {
		return fmt.Errorf("dispatch standard output differs.\nremembered (normalized):\n%s\nlatest (normalized):\n%s", before, after)
	}
	return nil
}

// theScenarioIsSkippedWhenTestsRunAsRoot skips the scenario when the suite runs
// as root, where a mode-000 file and a read-only directory do not deny anything
// and the fixture would silently stop testing what it names.
func theScenarioIsSkippedWhenTestsRunAsRoot(ctx context.Context) error {
	if os.Geteuid() == 0 {
		return godog.ErrSkip
	}
	return nil
}

// personalClaudeSettingsPath is the developer's own Claude Code user settings
// inside the scenario's sandboxed HOME. niwa must never write it: the
// crossSessionInbound decision travels as a launch flag precisely so a dispatch
// cannot change what every other session on the machine does.
func personalClaudeSettingsPath(s *testState) string {
	return filepath.Join(s.homeDir, ".claude", "settings.json")
}

// aPersonalClaudeSettingsFileExists seeds the developer's user settings and
// snapshots its bytes and modification time.
func aPersonalClaudeSettingsFileExists(ctx context.Context, body *godog.DocString) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := personalClaudeSettingsPath(s)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating the personal .claude directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(body.Content), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	// Backdate it so a same-second rewrite by niwa would still move the
	// modification time the assertion below reads.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		return fmt.Errorf("backdating %s: %w", path, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	s.personalClaudeSettingsBytes = []byte(body.Content)
	s.personalClaudeSettingsModTime = info.ModTime()
	return nil
}

// thePersonalClaudeSettingsFileIsUnchanged asserts both the bytes and the
// modification time survived the dispatch.
func thePersonalClaudeSettingsFileIsUnchanged(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := personalClaudeSettingsPath(s)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("re-reading %s: %w", path, err)
	}
	if string(data) != string(s.personalClaudeSettingsBytes) {
		return fmt.Errorf("%s changed.\nbefore:\n%s\nafter:\n%s", path, s.personalClaudeSettingsBytes, data)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.ModTime().Equal(s.personalClaudeSettingsModTime) {
		return fmt.Errorf("%s was rewritten: modification time moved from %s to %s", path, s.personalClaudeSettingsModTime, info.ModTime())
	}
	return nil
}

// thePersonalClaudeSettingsFileIsNotReadable makes the developer's user
// settings unreadable, the shape that would break a dispatch that tried to read
// or merge it.
func thePersonalClaudeSettingsFileIsNotReadable(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := personalClaudeSettingsPath(s)
	if err := os.Chmod(path, 0o000); err != nil {
		return fmt.Errorf("making %s unreadable: %w", path, err)
	}
	return nil
}

// noSettingsFileNiwaWroteIntoTheDispatchInstanceContains asserts none of the
// settings files niwa materializes into a dispatch instance carries the given
// text.
//
// Those are the instance-root .claude/settings.json and the
// .claude/settings.local.json niwa writes inside each cloned repository.
//
// A repository's committed .claude/settings.json is never among them, and that
// is the point rather than an omission: it is the fixture's own file at a path
// niwa does not write, so reading it would assert something about the fixture.
// What these scenarios are asking is whether niwa copies a key out of it into a
// file niwa owns. settings.local.json IS such a file even when a repository
// committed one, because the materializer replaces that path wholesale with its
// own generated document.
func noSettingsFileNiwaWroteIntoTheDispatchInstanceContains(ctx context.Context, unwanted string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst := s.lastDispatchInstancePath
	if inst == "" {
		inst = findDispatchInstance(s.workspaceRoot)
	}
	if inst == "" {
		return fmt.Errorf("no dispatch instance found under %s\nstdout:\n%s\nstderr:\n%s", s.workspaceRoot, s.stdout, s.stderr)
	}

	rootSettings := filepath.Join(inst, ".claude", "settings.json")
	// Repositories sit two levels down, at <instance>/<group>/<repo>, so the
	// per-repository settings niwa writes are at
	// <instance>/<group>/<repo>/.claude/settings.local.json. Probing one level
	// down finds nothing at all, which would leave this step asserting only the
	// instance-root file while reading as though it covered both.
	//
	// Every non-dot directory two levels down is treated as a repository. That
	// is the instance layout rather than a fact this step can check, so a future
	// layout that puts something else there would widen the scan rather than
	// narrow it -- the safe direction for an assertion about absence.
	var repoSettings []string
	groups, err := os.ReadDir(inst)
	if err != nil {
		return fmt.Errorf("reading dispatch instance %s: %w", inst, err)
	}
	for _, g := range groups {
		if !g.IsDir() || strings.HasPrefix(g.Name(), ".") {
			continue
		}
		repos, err := os.ReadDir(filepath.Join(inst, g.Name()))
		if err != nil {
			return fmt.Errorf("reading group %s: %w", g.Name(), err)
		}
		for _, r := range repos {
			if !r.IsDir() || strings.HasPrefix(r.Name(), ".") {
				continue
			}
			repoSettings = append(repoSettings, filepath.Join(inst, g.Name(), r.Name(), ".claude", "settings.local.json"))
		}
	}

	// read reports whether the file was there and read, NOT whether it was
	// clean: a file carrying the text returns true alongside the error saying
	// so. The boolean feeds the floors below, which ask what was inspected, and
	// only the error says what was found.
	read := func(path string) (bool, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, fmt.Errorf("reading %s: %w", path, err)
		}
		if strings.Contains(string(data), unwanted) {
			return true, fmt.Errorf("%s contains %q:\n%s", path, unwanted, data)
		}
		return true, nil
	}

	rootRead, err := read(rootSettings)
	if err != nil {
		return err
	}
	repoRead := 0
	for _, path := range repoSettings {
		ok, err := read(path)
		if err != nil {
			return err
		}
		if ok {
			repoRead++
		}
	}

	// Both floors exist so this step cannot quietly become an assertion about
	// nothing: one for each half of what it claims to read. Without the second,
	// a materializer that stopped writing settings.local.json would leave every
	// scenario passing on the instance-root file alone.
	if !rootRead {
		return fmt.Errorf("no settings file at %s, which every dispatch instance has; this assertion is looking in the wrong place", rootSettings)
	}
	if len(repoSettings) > 0 && repoRead == 0 {
		return fmt.Errorf("the dispatch instance %s has %d <group>/<repo> director(y/ies) but niwa wrote no settings.local.json into any of them, so the per-repository half of this assertion checked nothing; candidates were %v",
			inst, len(repoSettings), repoSettings)
	}
	return nil
}

// aFileExistsUnderTheWorkspaceRootWithBody writes a file at a path relative to
// the workspace root, creating its parent directories. It is how the
// configuration-source fixtures put a settings file where a reader might expect
// niwa to pick it up.
func aFileExistsUnderTheWorkspaceRootWithBody(ctx context.Context, relPath string, body *godog.DocString) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating the parent of %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte(body.Content), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// iRunUnderAPTY runs one command under a real terminal with no input. It is the
// single-run companion of the parallel step below: everything about this
// feature that depends on a terminal -- the closing sentence of the explanation
// and whether the marker is written -- needs one.
//
// The command runs from the workspace root, as its non-pty sibling `I run
// "..." from the workspace root` does -- runUnderPTY cds there before exec --
// and it records the dispatch instance through the same helper, so the two
// leave the same state behind. See recordDispatchInstance for why that matters.
func iRunUnderAPTY(ctx context.Context, command string) (context.Context, error) {
	ctx, err := iRunUnderPTYWithInput(ctx, command, "")
	if err != nil {
		return ctx, err
	}
	if s := getState(ctx); s != nil {
		recordDispatchInstance(s, command)
	}
	return ctx, nil
}

// iRunNTimesInParallelUnderAPTY starts n copies of the command at once, each
// under its own pty, and waits for all of them. Each run's exit code and
// transcript are kept separately in s.parallelRuns, in launch order.
//
// Truly concurrent, not merely repeated: the point is several dispatches racing
// for one configuration directory, which is where the one-time notice either
// holds or is written several times.
func iRunNTimesInParallelUnderAPTY(ctx context.Context, command string, n int) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if n < 1 {
		return ctx, fmt.Errorf("cannot run a command %d times", n)
	}
	runs := make([]ptyRun, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runs[i], errs[i] = runUnderPTY(ctx, s, command, "")
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return ctx, fmt.Errorf("parallel run %d: %w", i+1, err)
		}
	}
	s.parallelRuns = runs
	return ctx, nil
}

// allParallelRunsExitZero fails naming every run that did not, with its
// transcript, because a failure in one of several identical runs is only
// diagnosable from what that run printed.
func allParallelRunsExitZero(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if len(s.parallelRuns) == 0 {
		return fmt.Errorf("no parallel runs recorded")
	}
	var failures []string
	for i, run := range s.parallelRuns {
		if run.exitCode != 0 {
			failures = append(failures, fmt.Sprintf("run %d exited %d; transcript:\n%s", i+1, run.exitCode, run.stderr))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d of %d parallel runs failed:\n%s", len(failures), len(s.parallelRuns), strings.Join(failures, "\n---\n"))
	}
	return nil
}

// atLeastOneParallelTranscriptContainsTheText asserts the given text reached at
// least one terminal. The one-time explanation is asserted this way rather than
// "exactly one", because several runs that all found the marker absent have all
// legitimately printed it -- what must not happen is nobody seeing it.
func atLeastOneParallelTranscriptContainsTheText(ctx context.Context, body *godog.DocString) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if len(s.parallelRuns) == 0 {
		return fmt.Errorf("no parallel runs recorded")
	}
	want := strings.TrimRight(body.Content, "\n")
	for _, run := range s.parallelRuns {
		if strings.Contains(run.stderr, want) {
			return nil
		}
	}
	return fmt.Errorf("no parallel transcript contains:\n%s", want)
}

// everyParallelTranscriptHasExactlyNLinesContaining asserts each run printed the
// line the same number of times. Per run, not across the pool: the audit line is
// a statement about one dispatch, so a pool total would pass with one run
// printing it four times and three printing none.
func everyParallelTranscriptHasExactlyNLinesContaining(ctx context.Context, want int, needle string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if len(s.parallelRuns) == 0 {
		return fmt.Errorf("no parallel runs recorded")
	}
	for i, run := range s.parallelRuns {
		if got := countLinesContaining(run.stderr, needle); got != want {
			return fmt.Errorf("parallel run %d has %d lines containing %q; want %d\ntranscript:\n%s", i+1, got, needle, want, run.stderr)
		}
	}
	return nil
}

// registerSessionMessageSteps wires the session-message acceptance steps into
// the scenario context. Called from initializeScenario.
func registerSessionMessageSteps(ctx *godog.ScenarioContext) {
	// Machine configuration fixtures.
	ctx.Step(`^the niwa machine config global table contains:$`, theNiwaMachineConfigGlobalTableContains)
	ctx.Step(`^the niwa machine config is replaced with:$`, theNiwaMachineConfigIsReplacedWith)
	ctx.Step(`^the niwa machine config is not readable$`, theNiwaMachineConfigIsNotReadable)
	ctx.Step(`^the niwa config directory is not writable$`, theNiwaConfigDirectoryIsNotWritable)
	ctx.Step(`^I record the niwa machine config$`, iRecordTheNiwaMachineConfig)
	ctx.Step(`^the niwa machine config is byte-for-byte unchanged$`, theNiwaMachineConfigIsByteForByteUnchanged)

	// What the launched worker was handed.
	ctx.Step(`^the launched claude settings document has crossSessionInbound "([^"]*)"$`, theLaunchedClaudeSettingsHasCrossSessionInbound)
	ctx.Step(`^the launched claude settings document has no crossSessionInbound$`, theLaunchedClaudeSettingsHasNoCrossSessionInbound)
	ctx.Step(`^the launched claude settings document has remoteControlAtStartup true$`, theLaunchedClaudeSettingsHasRemoteControlAtStartup)
	ctx.Step(`^the launched claude prompt is "([^"]*)"$`, theLaunchedClaudePromptIs)

	// The one-time notice marker.
	ctx.Step(`^the session-message notice has already been shown$`, theSessionMessageNoticeHasAlreadyBeenShown)
	ctx.Step(`^the session-message notice marker exists$`, theSessionMessageNoticeMarkerExists)
	ctx.Step(`^the session-message notice marker does not exist$`, theSessionMessageNoticeMarkerDoesNotExist)

	// What the dispatch recorded, and what niwa list reports.
	ctx.Step(`^the dispatch mapping for session "([^"]*)" records session-message acceptance$`, theDispatchMappingRecordsInbound)
	ctx.Step(`^the dispatch mapping for session "([^"]*)" does not record session-message acceptance$`, theDispatchMappingDoesNotRecordInbound)
	ctx.Step(`^there are (\d+) dispatch mappings that record session-message acceptance$`, thereAreNDispatchMappingsRecordingInbound)
	ctx.Step(`^the list JSON reports the dispatch instance as accepting session messages$`, theListJSONReportsInstanceAccepting)
	ctx.Step(`^the list JSON reports the dispatch instance as not accepting session messages$`, theListJSONReportsInstanceNotAccepting)

	// Output assertions.
	ctx.Step(`^the error output has exactly (\d+) lines? containing "([^"]*)"$`, theErrorOutputHasExactlyNLinesContaining)
	ctx.Step(`^the error output contains the text:$`, theErrorOutputContainsTheText)
	ctx.Step(`^the error output does not contain the text:$`, theErrorOutputDoesNotContainTheText)
	ctx.Step(`^I remember the dispatch standard output$`, iRememberTheDispatchStandardOutput)
	ctx.Step(`^the dispatch standard output matches the remembered one apart from instance names and session identifiers$`, theDispatchStandardOutputMatchesTheRememberedOne)

	// The developer's own environment, which a dispatch must leave alone.
	ctx.Step(`^the scenario is skipped when tests run as root$`, theScenarioIsSkippedWhenTestsRunAsRoot)
	ctx.Step(`^a personal claude settings file exists with body:$`, aPersonalClaudeSettingsFileExists)
	ctx.Step(`^the personal claude settings file is unchanged$`, thePersonalClaudeSettingsFileIsUnchanged)
	ctx.Step(`^the personal claude settings file is not readable$`, thePersonalClaudeSettingsFileIsNotReadable)
	ctx.Step(`^no settings file niwa wrote into the dispatch instance contains "([^"]*)"$`, noSettingsFileNiwaWroteIntoTheDispatchInstanceContains)
	ctx.Step(`^a file "([^"]*)" exists under the workspace root with body:$`, aFileExistsUnderTheWorkspaceRootWithBody)

	// Terminals, one at a time and several at once.
	ctx.Step(`^I run "([^"]*)" under a pty$`, iRunUnderAPTY)
	ctx.Step(`^I run "([^"]*)" (\d+) times in parallel under a pty$`, iRunNTimesInParallelUnderAPTY)
	ctx.Step(`^all parallel runs exit 0$`, allParallelRunsExitZero)
	ctx.Step(`^at least one parallel transcript contains the text:$`, atLeastOneParallelTranscriptContainsTheText)
	ctx.Step(`^every parallel transcript has exactly (\d+) lines? containing "([^"]*)"$`, everyParallelTranscriptHasExactlyNLinesContaining)
}
