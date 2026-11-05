// Package claudestub is the copy-rule stub of Claude Code that niwa's retask
// and resume tests put first on PATH as `claude`: a POSIX shell script,
// embedded here so the unit tests in internal/cli and the functional suite run
// the same one.
//
// It models Claude Code like this, and no further:
//
//   - `--version` prints "<FAKE_CLAUDE_VERSION> (Claude Code)", 2.1.314 by
//     default.
//   - `agents --json --all` lists one row per job entry, with id, sessionId
//     and cwd, and pid and status while the session's process is alive.
//   - `stop <short>` stops the session but keeps its pid and status in the
//     next FAKE_CLAUDE_SETTLE_LISTINGS listings (2 by default) before
//     dropping them, and leaves its job state alone, cwd included.
//   - `--bg --resume <id> [args]` starts a copy under a newly minted id (a new
//     job entry rooted at the same cwd, with its own transcript) when the args
//     hold anything other than "--" and one prompt element, when the
//     session's row still shows a pid or a status, or when the build is older
//     than 2.1.314. Otherwise it continues the same id: it appends the prompt
//     as one line to that session's transcript and leaves the session live
//     and idle (pid, status idle, job state done/idle, or working/idle for an
//     armed worker). This copy rule is niwa's test model of the behavior
//     Claude Code documents, not a claim about Claude Code beyond it. A resume
//     of a session with no job entry starts one under the same id.
//   - `--bg [flags] -- <prompt>` (a dispatch) mints a session rooted at the
//     working directory, live and idle, with the flags saved as its
//     respawnFlags and the prompt as the first transcript line.
//   - `rm <short>` deletes the job entry; it refuses a session whose row
//     still shows a process.
//
// Every invocation is logged to FAKE_CLAUDE_LOG first, as a "\x01CALL"
// element and then each argument, each terminated by a NUL byte.
//
// Knobs, read on every invocation from the file <state>/knob-<NAME> when there
// is one, else from FAKE_CLAUDE_<NAME>: VERSION, SETTLE_LISTINGS, ALWAYS_COPY
// (1 copies every wake), STOP_FAIL and RM_FAIL (1 makes the verb exit 1),
// WAKE ("dead" leaves the woken row with no pid and no status, "vanish" drops
// it from the listing) and ARMED (1 makes a new session an armed keep-alive
// worker: state working, one session_cron task in flight).
package claudestub

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed claude.sh
var script []byte

// Script returns the stub's text.
func Script() []byte { return bytes.Clone(script) }

// Stub is one installed stub.
type Stub struct {
	// BinDir holds the `claude` script; put it first on PATH.
	BinDir string
	// State is FAKE_CLAUDE_STATE, where the stub keeps its sessions and knobs.
	State string
	// Log is FAKE_CLAUDE_LOG.
	Log string
	// Home is the HOME the stub's job entries and transcripts are under.
	Home string
}

// New writes the stub under dir for a home directory.
func New(dir, home string) (*Stub, error) {
	s := &Stub{
		BinDir: filepath.Join(dir, "bin"),
		State:  filepath.Join(dir, "state"),
		Log:    filepath.Join(dir, "claude.log"),
		Home:   home,
	}
	for _, d := range []string{s.BinDir, filepath.Join(s.State, "sessions")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(s.BinDir, "claude"), script, 0o755); err != nil {
		return nil, err
	}
	return s, nil
}

// Env returns the environment entries the stub needs.
func (s *Stub) Env() []string {
	return []string{"FAKE_CLAUDE_STATE=" + s.State, "FAKE_CLAUDE_LOG=" + s.Log}
}

// SetKnob sets a knob for every later invocation.
func (s *Stub) SetKnob(name, value string) error {
	return os.WriteFile(filepath.Join(s.State, "knob-"+name), []byte(value), 0o644)
}

// ClearKnob removes a knob set with SetKnob.
func (s *Stub) ClearKnob(name string) error {
	err := os.Remove(filepath.Join(s.State, "knob-"+name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Session is a job entry to start the stub with.
type Session struct {
	Short     string
	SessionID string
	Cwd       string
	// Alive gives the row a pid and Status while true.
	Alive  bool
	Status string
	// State and Tempo are the job state's fields; empty reads done and idle.
	State string
	Tempo string
	// InFlight lists the kinds of the job's in-flight tasks.
	InFlight []string
	// Respawn is the job's saved options (respawnFlags).
	Respawn []string
}

// AddSession starts the stub with a session and writes its job state.
func (s *Stub) AddSession(sess Session) error {
	d := filepath.Join(s.State, "sessions", sess.Short)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	if sess.State == "" {
		sess.State = "done"
	}
	if sess.Tempo == "" {
		sess.Tempo = "idle"
	}
	respawn, err := json.Marshal(sess.Respawn)
	if err != nil {
		return err
	}
	if sess.Respawn == nil {
		respawn = []byte("[]")
	}
	extra := ""
	if len(sess.InFlight) > 0 {
		kinds, _ := json.Marshal(sess.InFlight)
		extra = fmt.Sprintf(`"inFlight":{"tasks":%d,"kinds":%s}`, len(sess.InFlight), kinds)
	}
	alive := "0"
	if sess.Alive {
		alive = "1"
	}
	files := map[string]string{
		"sid":     sess.SessionID,
		"cwd":     sess.Cwd,
		"alive":   alive,
		"pid":     "4242",
		"status":  sess.Status,
		"settle":  "0",
		"state":   sess.State,
		"tempo":   sess.Tempo,
		"respawn": string(respawn),
		"extra":   extra,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(d, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	doc := map[string]any{
		"sessionId":    sess.SessionID,
		"template":     "bg",
		"cwd":          sess.Cwd,
		"state":        sess.State,
		"tempo":        sess.Tempo,
		"respawnFlags": json.RawMessage(respawn),
	}
	if len(sess.InFlight) > 0 {
		doc["inFlight"] = map[string]any{"tasks": len(sess.InFlight), "kinds": sess.InFlight}
	}
	return s.WriteJobState(sess.Short, doc)
}

// WriteJobState replaces a job entry's state.json with doc.
func (s *Stub) WriteJobState(short string, doc map[string]any) error {
	dir := filepath.Join(s.Home, ".claude", "jobs", short)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "state.json"), append(data, '\n'), 0o600)
}

// SetAlive gives a session's row a process (pid and status) or takes it away,
// as a worker starting or stopping on its own would.
func (s *Stub) SetAlive(short string, alive bool, status string) error {
	d := filepath.Join(s.State, "sessions", short)
	v := "0"
	if alive {
		v = "1"
	}
	if err := os.WriteFile(filepath.Join(d, "alive"), []byte(v), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(d, "settle"), []byte("0"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "status"), []byte(status), 0o644)
}

// Calls returns every logged invocation's argv, in order.
func (s *Stub) Calls() ([][]string, error) {
	return ReadLog(s.Log)
}

// ReadLog parses a stub log.
func ReadLog(path string) ([][]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var calls [][]string
	for _, el := range strings.Split(string(data), "\x00") {
		if el == "\x01CALL" {
			calls = append(calls, []string{})
			continue
		}
		if len(calls) == 0 {
			continue
		}
		calls[len(calls)-1] = append(calls[len(calls)-1], el)
	}
	return calls, nil
}

// JobEntries returns the short ids of the stub's job entries.
func (s *Stub) JobEntries() ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.State, "sessions"))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

// SessionID returns the full id of the stub's session short.
func (s *Stub) SessionID(short string) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.State, "sessions", short, "sid"))
	return string(data), err
}

// Cwd returns the working directory of the stub's session short.
func (s *Stub) Cwd(short string) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.State, "sessions", short, "cwd"))
	return string(data), err
}

// LastWakeDir returns the working directory the last wake ran in.
func (s *Stub) LastWakeDir() (string, error) {
	data, err := os.ReadFile(filepath.Join(s.State, "last-wake-cwd"))
	return string(data), err
}

// TranscriptPrompts returns the prompts in a transcript the stub wrote, in
// order.
func TranscriptPrompts(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		var rec struct {
			Prompt *string `json:"prompt"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Prompt == nil {
			continue
		}
		out = append(out, *rec.Prompt)
	}
	return out, nil
}

// Count reports how many calls start with args.
func Count(calls [][]string, args ...string) int {
	n := 0
	for _, c := range calls {
		if HasPrefix(c, args...) {
			n++
		}
	}
	return n
}

// HasPrefix reports whether a call starts with args.
func HasPrefix(call []string, args ...string) bool {
	if len(call) < len(args) {
		return false
	}
	for i, a := range args {
		if call[i] != a {
			return false
		}
	}
	return true
}

// Quote renders a call for a test failure.
func Quote(calls [][]string) string {
	var b strings.Builder
	for _, c := range calls {
		parts := make([]string, len(c))
		for i, a := range c {
			parts[i] = strconv.Quote(a)
		}
		b.WriteString(strings.Join(parts, " "))
		b.WriteString("\n")
	}
	return b.String()
}
