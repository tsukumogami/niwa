package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/keyreport"
	"github.com/tsukumogami/niwa/internal/workspace"
)

func init() {
	rootCmd.AddCommand(resumeCmd)
}

var resumeCmd = &cobra.Command{
	Use:   "resume <instance-name | session-id | session-name> [prompt]",
	Short: "Bring a dispatched worker back in the posture it was dispatched with",
	Long: `Resume a worker niwa dispatch launched, from the launch record dispatch
wrote for it.

The argument names the worker by its instance name, its session id, or its
session name, tried in that order. When the instance was destroyed, resume
restores it under its original name and path first (with its Claude trust
grant when the workspace opts in), cloning each repository at its current
remote state: unpushed work from the destroyed instance isn't restored.

The worker is started again with the permission mode, session name, model,
settings and environment dispatch recorded, except that the permission mode is
never more permissive than the one current policy gives a new dispatch: when
the current one is stricter, resume launches with it and says so. An optional
prompt is passed to the resumed session.

Resume refuses, before it restores or launches anything, when no record or
more than one matches, when the session's transcript is gone or the session is
still running, when something other than the instance occupies its path, and
when the record fails a check: it is read as input, not trusted because niwa
wrote it. It also refuses, without waiting, while another niwa command (a
destroy, a reap or another resume) is acting on the instance, and names that
command.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runResume,
}

// resumeLaunchFunc starts the resumed worker: bin with args, from dir, with
// env. A package variable so a test can see the launch without a binary;
// the resume tests use the real one against a stub agent on PATH.
var resumeLaunchFunc = realResumeLaunch

// resumeCaptureTimeout bounds the wait for the resumed worker's session
// record, as dispatchCaptureTimeout does for a dispatch.
var resumeCaptureTimeout = dispatchCaptureTimeout

// The bounds a record's values are checked against.
const (
	maxResumeTokenLen    = 128
	maxResumeEnvValueLen = 16 << 10
)

// resumeTokenRe is the plain-token shape a recorded session name must have: a
// letter or digit first, then letters, digits, '.', '_' and '-'.
var resumeTokenRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// resumeModelRe is the shape a recorded model must have. Dispatch forwards an
// unrecognized model as given, so a full model id with a context suffix
// ("[1m]"), a provider prefix or a path ("vendor/model:tag", "a@b") has to
// resume too. The first character is still a letter or digit, and the value
// rides one "--model=" element, so it can't be read as a flag.
var resumeModelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+\[\]-]*$`)

// resumeRefusal is a refusal that names what was asked for.
func resumeRefusal(arg, format string, args ...any) error {
	return fmt.Errorf("niwa: error: cannot resume %q: %s", arg, fmt.Sprintf(format, args...))
}

func runResume(cmd *cobra.Command, args []string) error {
	arg := args[0]
	prompt := ""
	if len(args) == 2 {
		prompt = args[1]
		if err := validateDispatchPrompt(prompt); err != nil {
			return err
		}
		if len(prompt) > maxArgStringBytes || strings.ContainsRune(prompt, 0) {
			return resumeRefusal(arg, "the prompt must fit in one argument and hold no NUL byte")
		}
	}
	stderr := cmd.ErrOrStderr()
	out := cmd.OutOrStdout()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("niwa: error: getting working directory: %w", err)
	}
	class, err := workspace.ClassifyCwd(cwd)
	if err != nil {
		return fmt.Errorf("niwa: error: classifying working directory: %w", err)
	}
	if class.WorkspaceRoot == "" {
		return fmt.Errorf("niwa: error: not inside a niwa workspace; run resume from within a workspace")
	}
	root := class.WorkspaceRoot

	// The machine checks dispatch makes at its (2a), with the same refusals:
	// the configuration loads, its dispatch permission mode is one Claude
	// Code accepts, and its authority keys hold valid values.
	cfgPath := globalConfigPathForError()
	gc, err := config.LoadGlobalConfig()
	if err != nil {
		return fmt.Errorf("niwa: error: loading %s: %w", cfgPath, err)
	}
	hostMode, err := gc.DispatchPermissionMode()
	if err != nil {
		return fmt.Errorf("niwa: error: %w (in %s)", err, cfgPath)
	}
	if err := checkMachineAuthority(gc, cfgPath); err != nil {
		return err
	}

	// The workspace configuration decides which environment names may be
	// replayed and the posture, so a configuration that can't be read
	// refuses rather than replaying against nothing.
	wsConfigPath := filepath.Join(root, workspace.StateDir, workspace.WorkspaceConfigFile)
	wsLoaded, err := config.Load(wsConfigPath)
	if err != nil {
		return resumeRefusal(arg, "loading the workspace configuration %s: %v", wsConfigPath, err)
	}
	wsConfig := wsLoaded.Config

	rec, err := resolveLaunchRecord(root, arg)
	if err != nil {
		return err
	}

	ag, err := agent.ParseAgent(rec.Agent)
	if err != nil {
		return resumeRefusal(arg, "the launch record names agent %q, which niwa doesn't know", rec.Agent)
	}
	spec, ok := dispatchLaunchSpec(ag)
	if !ok || len(spec.ResumeLaunchArgs) == 0 || len(spec.PermissionModes) == 0 {
		return resumeRefusal(arg, "niwa can't resume a %s worker", ag)
	}
	passthrough, unset := dispatchEnvLists(wsConfig)
	if err := validateLaunchRecord(rec, spec, passthrough); err != nil {
		return resumeRefusal(arg, "launch record %s: %v", rec.DispatchID, err)
	}
	if hostMode != "" && !slices.Contains(spec.PermissionModes, hostMode) {
		return resumeRefusal(arg, "the current [global] dispatch_permission_mode %q can't be ordered against the recorded mode; resume orders only %s",
			hostMode, strings.Join(spec.PermissionModes, ", "))
	}

	// The instance path is derived, never read from the record.
	instancePath := filepath.Join(root, rec.InstanceName)

	// The instance's lock comes before every check about the instance and is
	// held until resume returns, through the restore, the mapping write, the
	// launch and the capture. A destroy, a reap or another resume can't act on
	// the instance between resume finding the session not running and
	// launching into it. A restore that fails later is rolled back under this
	// same hold, which is why the lock is never taken inside the destroy seam.
	release, err := takeInstanceLock(instancePath, "resume")
	if err != nil {
		return resumeRefusal(arg, "%s", instanceLockReason(err))
	}
	defer release()

	present, err := instancePresent(instancePath, rec.InstanceName)
	if err != nil {
		return resumeRefusal(arg, "%v", err)
	}

	home := userHomeDir()
	if home == "" {
		return resumeRefusal(arg, "the home directory can't be resolved, so the session's transcript can't be checked")
	}
	transcript := sessionattach.TranscriptPath(home, instancePath, rec.SessionID)
	if _, err := os.Lstat(transcript); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return resumeRefusal(arg, "the transcript of session %s is gone, so there is nothing to resume", rec.SessionID)
		}
		return resumeRefusal(arg, "reading the transcript of session %s: %v", rec.SessionID, err)
	}

	rows, err := listClaudeSessionsFunc(cmd.Context())
	if err != nil {
		return resumeRefusal(arg, "could not list Claude Code's sessions to check whether session %s is running (%v)", rec.SessionID, err)
	}
	for _, r := range rows {
		if r.SessionID == rec.SessionID && r.running() {
			return resumeRefusal(arg, "%s; attach to it instead", runningSessionReason(r, workspace.SessionMapping{SessionID: rec.SessionID, SessionName: rec.SessionName}))
		}
	}

	bin, err := lookAgentBinary(spec.Binary)
	if err != nil {
		return resumeRefusal(arg, "%s binary not found in PATH", spec.Binary)
	}

	// Every check has passed. From here on the work is restoring, mapping
	// and launching.
	restored := false
	posture := ""
	if present {
		// An instance resume didn't provision: its posture counts only when
		// the one it recorded equals the one the configuration gives now, so
		// the workspace rung (which reads "bypass") applies only when both
		// say it and neither can loosen the other.
		if st, err := workspace.LoadState(instancePath); err == nil && st.ClaudePermissions == workspace.PermissionsPosture(wsConfig) {
			posture = st.ClaudePermissions
		}
	} else {
		res, err := provisionInstanceNamedFunc(cmd.Context(), root, rec.InstanceName)
		fmt.Fprint(stderr, keyreport.RenderText(res.Keys))
		renderNotices(stderr, res.Notices)
		if err != nil {
			return resumeRefusal(arg, "restoring instance %s: %v", rec.InstanceName, err)
		}
		restored = true
		// Provisioned moments ago by this process, so its recorded posture
		// is the current one, overlay included.
		if st, err := workspace.LoadState(instancePath); err == nil {
			posture = st.ClaudePermissions
		}
		fmt.Fprintf(out, "Restored instance %s\n", instancePath)
		for _, line := range restoredCommits(instancePath) {
			fmt.Fprintf(out, "  %s\n", line)
		}
		fmt.Fprintln(out, "  unpushed work from the destroyed instance isn't restored: each repository is cloned at its current remote state")
	}
	rollback := func() {
		if restored {
			_ = destroyInstanceFunc(instancePath)
		}
	}

	current, _ := derivePermissionMode("", hostMode, posture, spec.Flags)
	mode, downgraded := stricterPermissionMode(spec.PermissionModes, rec.PermissionMode, current)
	if downgraded {
		fmt.Fprintf(stderr, "niwa resume: launching with --permission-mode %s, the mode current policy gives, instead of the recorded %s, which is more permissive\n",
			mode, displayMode(rec.PermissionMode))
	}

	// The command is built before the mapping is written, so a record it
	// can't carry leaves no mapping behind. It needs the mode, and the mode
	// needs a restored instance's posture, so it can't come before the
	// restore; the agent check above already refuses an agent with no
	// resume-launch arguments before anything is restored.
	argv := reentryResumeArgs(spec, resumeLaunch{
		SessionID:      rec.SessionID,
		Workdir:        instancePath,
		PermissionMode: mode,
		SessionName:    rec.SessionName,
		Model:          rec.Model,
		Settings:       rec.Settings,
		Prompt:         prompt,
	})
	if argv == nil {
		rollback()
		return resumeRefusal(arg, "niwa can't build a resume command for a %s worker with this record", ag)
	}

	// The mapping goes in before the launch, so the reaper's backstop never
	// sees a restored dispatch instance as an unmapped orphan.
	mapping := workspace.SessionMapping{
		SessionID:    rec.SessionID,
		InstanceName: rec.InstanceName,
		InstancePath: instancePath,
		Agent:        string(ag),
		Ephemeral:    true,
		Origin:       workspace.MappingOriginDispatch,
		SessionName:  rec.SessionName,
		DispatchID:   rec.DispatchID,
		Created:      time.Now().UTC(),
	}
	prev, prevErr := workspace.ReadSessionMapping(root, rec.SessionID)
	if prevErr == nil {
		mapping.Handle = prev.Handle
		mapping.Label = prev.Label
		mapping.ParentSessionID = prev.ParentSessionID
	}
	// unmap undoes the mapping write when the launch doesn't happen: the
	// mapping that was there before goes back, or none.
	unmap := func() {
		if prevErr == nil {
			_ = workspace.WriteSessionMapping(root, prev)
			return
		}
		_ = workspace.DeleteSessionMapping(root, rec.SessionID)
	}
	if err := workspace.WriteSessionMapping(root, mapping); err != nil {
		rollback()
		return resumeRefusal(arg, "writing the session mapping: %v", err)
	}

	env := resumeEnv(os.Environ(), unset, rec.Env)
	if err := resumeLaunchFunc(cmd.Context(), bin, argv, instancePath, env); err != nil {
		unmap()
		rollback()
		return fmt.Errorf("niwa: error: launching the resumed worker: %w", err)
	}

	// Capture the job the way dispatch does. Claude Code may report the
	// conversation under a new id; the mapping and the launch record follow
	// it. A capture that fails leaves the session mapped under the recorded
	// id, since the worker was started either way.
	sessionID := rec.SessionID
	recordsRoot := recordStoreRoot(spec.Records, home, os.Getenv)
	if id, handle, err := dispatchCapture(spec.Records, recordsRoot, instancePath, resumeCaptureTimeout, nil, 0); err != nil {
		fmt.Fprintf(stderr, "niwa: warning: could not confirm the resumed session's id, so it stays mapped as %s: %v\n", rec.SessionID, err)
	} else {
		mapping.Handle = handle
		if id != rec.SessionID {
			mapping.SessionID = id
			sessionID = id
		}
		if err := workspace.WriteSessionMapping(root, mapping); err != nil {
			fmt.Fprintf(stderr, "niwa: warning: could not update the session mapping for %s: %v\n", id, err)
		} else if id != rec.SessionID {
			_ = workspace.DeleteSessionMapping(root, rec.SessionID)
			rec.SessionID = id
			if err := writeLaunchRecordFunc(root, rec); err != nil {
				fmt.Fprintf(stderr, "niwa: warning: could not update the launch record %s: %v\n",
					filepath.Join(workspace.LaunchDir(root), rec.DispatchID+".json"), err)
			}
		}
	}

	fmt.Fprintf(out, "Resumed session %s\n", sessionID)
	fmt.Fprintf(out, "  instance: %s\n", instancePath)
	if rec.SessionName != "" {
		fmt.Fprintf(out, "  session name: %s\n", rec.SessionName)
	}
	fmt.Fprintf(out, "  permission mode: %s\n", displayMode(mode))
	return nil
}

// resolveLaunchRecord finds the one launch record arg names: by instance
// name, then by session id, then by session name, taking the first of those
// that matches anything. No match refuses naming the argument; more than one
// refuses listing them. The record it returns has been read a second time,
// strictly (ReadLaunchRecordChecked), from a store that passed
// CheckLaunchStore.
func resolveLaunchRecord(root, arg string) (workspace.LaunchRecord, error) {
	if err := workspace.CheckLaunchStore(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return workspace.LaunchRecord{}, resumeRefusal(arg, "no launch record names it")
		}
		return workspace.LaunchRecord{}, resumeRefusal(arg, "%v", err)
	}
	entries, err := workspace.ListLaunchRecords(root)
	if err != nil {
		return workspace.LaunchRecord{}, resumeRefusal(arg, "reading the launch store %s: %v", workspace.LaunchDir(root), err)
	}
	var matches []workspace.LaunchRecord
	for _, field := range []func(workspace.LaunchRecord) string{
		func(r workspace.LaunchRecord) string { return r.InstanceName },
		func(r workspace.LaunchRecord) string { return r.SessionID },
		func(r workspace.LaunchRecord) string { return r.SessionName },
	} {
		for _, e := range entries {
			if e.Err == nil && field(e.Record) != "" && field(e.Record) == arg {
				matches = append(matches, e.Record)
			}
		}
		if len(matches) > 0 {
			break
		}
	}
	switch len(matches) {
	case 0:
		return workspace.LaunchRecord{}, resumeRefusal(arg, "no launch record names it")
	case 1:
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%d launch records match it:", len(matches))
		for _, m := range matches {
			fmt.Fprintf(&b, "\n  dispatch %s: instance %s, session %s", m.DispatchID, m.InstanceName, m.SessionID)
			if m.SessionName != "" {
				fmt.Fprintf(&b, ", session name %s", m.SessionName)
			}
		}
		return workspace.LaunchRecord{}, resumeRefusal(arg, "%s", b.String())
	}
	rec, err := workspace.ReadLaunchRecordChecked(root, matches[0].DispatchID)
	if err != nil {
		return workspace.LaunchRecord{}, resumeRefusal(arg, "launch record %s: %v", matches[0].DispatchID, err)
	}
	return rec, nil
}

// validateLaunchRecord checks every field of rec a resume acts on, against
// the agent's declaration and the workspace's current passthrough list.
func validateLaunchRecord(rec workspace.LaunchRecord, spec agentplan.LaunchSpec, passthrough []string) error {
	if !workspace.ValidSessionID(rec.SessionID) {
		return fmt.Errorf("session id %q is not a session id", rec.SessionID)
	}
	if !validInstanceName(rec.InstanceName) || !isDispatchInstanceName(rec.InstanceName) {
		return fmt.Errorf("instance name %q is not a dispatch instance's name", rec.InstanceName)
	}
	if rec.PermissionMode != "" && !slices.Contains(spec.PermissionModes, rec.PermissionMode) {
		return fmt.Errorf("permission mode %q is not one resume can order (%s)", rec.PermissionMode, strings.Join(spec.PermissionModes, ", "))
	}
	for _, f := range []struct {
		name, value string
		re          *regexp.Regexp
		chars       string
	}{
		{"model", rec.Model, resumeModelRe, "'.', '_', ':', '/', '@', '+', '[', ']' and '-'"},
		{"session name", rec.SessionName, resumeTokenRe, "'.', '_' and '-'"},
	} {
		if f.value == "" {
			continue
		}
		if len(f.value) > maxResumeTokenLen || !f.re.MatchString(f.value) {
			return fmt.Errorf("%s %q must start with a letter or digit and hold only letters, digits, %s (at most %d)", f.name, f.value, f.chars, maxResumeTokenLen)
		}
	}
	allowed := func(name string) bool {
		return slices.Contains(config.LaunchSettingsEnvNames, name) || slices.Contains(passthrough, name)
	}
	if err := checkResumeEnv(rec.Env, allowed); err != nil {
		return err
	}
	if rec.Settings != "" {
		if err := checkResumeSettings(rec.Settings, allowed); err != nil {
			return fmt.Errorf("settings: %w", err)
		}
	}
	return nil
}

// checkResumeEnv refuses an environment holding a name allowed refuses, or a
// value that is too long or holds a control character. The allowlist is the
// names niwa sets itself and the ones the workspace's current
// [session.dispatch_env] still passes, so a name the workspace stopped
// passing, or one planted in the record, never reaches the worker.
func checkResumeEnv(env map[string]string, allowed func(string) bool) error {
	names := make([]string, 0, len(env))
	for n := range env {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !allowed(n) {
			return fmt.Errorf("environment variable %s is neither one niwa sets nor one the workspace's [session.dispatch_env] passes now", n)
		}
		v := env[n]
		if len(v) > maxResumeEnvValueLen {
			return fmt.Errorf("environment variable %s is longer than %d bytes", n, maxResumeEnvValueLen)
		}
		if strings.IndexFunc(v, unicode.IsControl) >= 0 {
			return fmt.Errorf("environment variable %s holds a control character", n)
		}
	}
	return nil
}

// checkResumeSettings refuses a settings document holding anything dispatch
// doesn't emit: the remote-control key (true), the cross-session acceptance
// key ("accept"), the MCP approval list (names matching
// mcpServerNamePattern) and the env object (checked like the record's own
// environment). Any other key, a hook or a permission rule, refuses it.
func checkResumeSettings(doc string, allowed func(string) bool) error {
	dec := json.NewDecoder(strings.NewReader(doc))
	var keys map[string]json.RawMessage
	if err := dec.Decode(&keys); err != nil {
		return fmt.Errorf("not a JSON object: %v", err)
	}
	if dec.More() {
		return errors.New("trailing data after the JSON object")
	}
	if !strings.HasPrefix(strings.TrimSpace(doc), "{") {
		return errors.New("not a JSON object")
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		raw := keys[k]
		switch k {
		case config.RemoteControlAtStartupKey:
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("true")) {
				return fmt.Errorf("%s must be true", k)
			}
		case config.CrossSessionInboundKey:
			var v string
			if json.Unmarshal(raw, &v) != nil || v != crossSessionInboundAccept {
				return fmt.Errorf("%s must be %q", k, crossSessionInboundAccept)
			}
		case config.EnabledMcpjsonServersKey:
			var list []string
			if err := json.Unmarshal(raw, &list); err != nil {
				return fmt.Errorf("%s must be a list of names", k)
			}
			if len(list) > maxLaunchMCPServers {
				return fmt.Errorf("%s holds more than %d names", k, maxLaunchMCPServers)
			}
			for _, n := range list {
				if !mcpServerNamePattern.MatchString(n) {
					return fmt.Errorf("%s name %q must match %s", k, n, mcpServerNamePattern)
				}
			}
		case "env":
			var env map[string]string
			if err := json.Unmarshal(raw, &env); err != nil {
				return errors.New("env must be an object of string values")
			}
			if err := checkResumeEnv(env, allowed); err != nil {
				return err
			}
		default:
			return fmt.Errorf("key %q is not one dispatch emits", k)
		}
	}
	return nil
}

// instancePresent reports whether the instance is at path (true), or nothing
// is (false). Anything else there -- a file, a symlink, a directory that
// doesn't hold this instance's state -- is an error: resume never restores
// over it or launches into it.
func instancePresent(path, name string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking %s: %v", path, err)
	}
	occupied := fmt.Errorf("something other than instance %s occupies %s", name, path)
	if !info.IsDir() {
		return false, occupied
	}
	if workspace.ValidateInstanceDir(path) != nil {
		return false, occupied
	}
	st, err := workspace.LoadState(path)
	if err != nil || (st.InstanceName != "" && st.InstanceName != name) {
		return false, occupied
	}
	return true, nil
}

// stricterPermissionMode returns the mode to launch with, and whether it is
// current rather than recorded. order is strictest first. An empty mode is
// the agent's default, which ranks as "default" when the order holds it.
// Both modes must be in order or empty: the caller checks the recorded mode
// and the host's, and derivePermissionMode adds only "bypassPermissions" from
// the posture. A mode outside order would rank as the most permissive.
//
// A downgrade to an empty current mode launches with "default" spelled out
// rather than with no flag: with no flag the agent falls back to whatever
// default mode its own settings name, which could be the permissive mode the
// downgrade exists to take away.
func stricterPermissionMode(order []string, recorded, current string) (string, bool) {
	rank := func(m string) int {
		if m == "" {
			m = "default"
		}
		if i := slices.Index(order, m); i >= 0 {
			return i
		}
		return len(order)
	}
	if rank(current) < rank(recorded) {
		if current == "" && slices.Contains(order, "default") {
			return "default", true
		}
		return current, true
	}
	return recorded, false
}

// displayMode names a permission mode for a message, "none" when no mode is
// passed.
func displayMode(m string) string {
	if m == "" {
		return "none (the agent's default)"
	}
	return m
}

// resumeEnv is the resumed worker's environment: the parent's, less the
// workspace's unset names and the reap key, with the recorded names set to
// their recorded values.
func resumeEnv(base, unset []string, recorded map[string]string) []string {
	proc, _ := shapeLaunchEnv(base, launchEnvShape{Unset: unset})
	out := make([]string, 0, len(proc)+len(recorded))
	for _, kv := range proc {
		name, _, _ := strings.Cut(kv, "=")
		if _, ok := recorded[name]; ok {
			continue
		}
		out = append(out, kv)
	}
	names := make([]string, 0, len(recorded))
	for n := range recorded {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		out = append(out, n+"="+recorded[n])
	}
	return out
}

// restoredCommits lists each repository of a restored instance with the
// commit it was cloned at, as "<group>/<repo> at <commit>".
func restoredCommits(instancePath string) []string {
	gits, _ := filepath.Glob(filepath.Join(instancePath, "*", "*", ".git"))
	sort.Strings(gits)
	var lines []string
	for _, g := range gits {
		repo := filepath.Dir(g)
		rel, err := filepath.Rel(instancePath, repo)
		if err != nil || strings.HasPrefix(filepath.Base(filepath.Dir(repo)), ".") {
			continue
		}
		head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
		commit := strings.TrimSpace(string(head))
		if err != nil || commit == "" {
			commit = "an unknown commit"
		}
		lines = append(lines, fmt.Sprintf("%s at %s", filepath.ToSlash(rel), commit))
	}
	return lines
}

// realResumeLaunch runs the agent's binary, which backgrounds its own worker
// and exits, so this waits for the hand-off and reads its exit status.
func realResumeLaunch(ctx context.Context, bin string, args []string, dir string, env []string) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 2048 {
			msg = msg[len(msg)-2048:]
		}
		if msg != "" {
			return fmt.Errorf("launching %s: %w: %s", filepath.Base(bin), err, msg)
		}
		return fmt.Errorf("launching %s: %w", filepath.Base(bin), err)
	}
	return nil
}
