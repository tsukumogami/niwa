package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// A dispatch's authority: what level and named grant the worker runs under.
// runDispatch resolves it at step (2e), before the binary preflight, the
// prompt, the sweep and provisioning, so a refusal leaves nothing behind; it
// prints the resolution line once the dispatch id exists at (4); and at
// (8a), with the instance built but nothing launched, it resolves the same
// request again against what provisioning built the instance from and writes
// the record. Nothing after (8a) reads the resolution except the two lineage
// attributes, so argv, the permission mode and every settings document are
// the same whatever was granted.

// The authority flags. dispatchParentFromSet tells `--parent-from ""` (a
// refusal) from no flag at all.
var (
	dispatchLevel         string
	dispatchGrants        []string
	dispatchRoles         []string
	dispatchParentFrom    string
	dispatchParentFromSet bool
)

const (
	dispatchLevelFlagUsage = "authority level for this dispatch (autonomous, supervised or strict); it may only narrow the level ceiling the workspace, overlay and machine config set"
	dispatchGrantFlagUsage = "grant this dispatch a named authority (merge-pr, close-issue, cut-release); repeatable, and a comma list splits. Every authority must be inside the grant ceiling, or nothing launches"
	dispatchRoleFlagUsage  = "grant this dispatch every authority a role declared in the workspace's [authority.roles] names; repeatable, and a comma list splits"
	dispatchParentFromHelp = "absolute path inside the dispatching instance, to record it as this dispatch's parent when dispatch runs from somewhere else (the workspace root, say)"
)

// optionalStringValue is a string flag that records whether it was given, so
// an explicit empty value can be refused rather than read as absent.
type optionalStringValue struct {
	value *string
	set   *bool
}

func (v optionalStringValue) String() string {
	if v.value == nil {
		return ""
	}
	return *v.value
}

func (v optionalStringValue) Set(s string) error {
	*v.value = s
	*v.set = true
	return nil
}

func (optionalStringValue) Type() string { return "string" }

// writeAuthorityRecordFunc writes the record at (8a). It is a variable so a
// test can make the write fail and watch the rollback.
var writeAuthorityRecordFunc = workspace.WriteAuthorityRecord

// dispatchAuthority is what (2e) resolved: the request, the policy and
// overlay it was resolved against, the record's policy fields, and the
// parent link.
type dispatchAuthority struct {
	request   authority.Request
	policy    authority.Policy
	overlay   *config.WorkspaceOverlay
	resolved  authority.Record
	parentID  string
	parentSrc string
	// parent is the dispatcher's whole record, nil when there is none. Only
	// its dispatch id is used today; it is kept so a never-wider check can
	// compare the child against it at this step without reading it again.
	parent *authority.Record
}

// authorityRefusal is a refusal whose message may span lines (one per
// offending authority, or one per invalid value). Every line carries the
// usual prefix, so each reads on its own.
type authorityRefusal struct{ err error }

func (r authorityRefusal) Error() string {
	lines := strings.Split(r.err.Error(), "\n")
	for i, l := range lines {
		lines[i] = "niwa: error: " + l
	}
	return strings.Join(lines, "\n")
}

func (r authorityRefusal) Unwrap() error { return r.err }

func refuse(format string, args ...any) error {
	return authorityRefusal{fmt.Errorf(format, args...)}
}

// checkMachineAuthority validates the machine's authority_level and
// authority_ceiling at (2a), beside the permission mode, so a broken machine
// setting stops the dispatch before anything else reads it.
func checkMachineAuthority(gc *config.GlobalConfig, cfgPath string) error {
	if _, err := gc.AuthorityLevel(); err != nil {
		return refuse("%v (in %s)", err, cfgPath)
	}
	if _, err := gc.AuthorityCeiling(); err != nil {
		return refuse("%v (in %s)", err, cfgPath)
	}
	return nil
}

// parseDispatchRequest turns the three request flags into an
// authority.Request. Vocabulary is checked by authority.Resolve, which names
// the flag; this only splits the comma lists, refusing an empty entry, and
// parses the level.
func parseDispatchRequest(level string, grants, roles []string) (authority.Request, error) {
	var req authority.Request
	l, err := authority.ParseLevel(level)
	if err != nil {
		var uv *authority.UnknownValueError
		if errors.As(err, &uv) {
			uv.Key = "--level"
		}
		return req, authorityRefusal{err}
	}
	req.Level = l
	g, err := authority.ParseNameList(grants)
	if err != nil {
		return req, refuse("--grant: %v", err)
	}
	for _, name := range g {
		req.Grants = append(req.Grants, authority.Authority(name))
	}
	r, err := authority.ParseNameList(roles)
	if err != nil {
		return req, refuse("--role: %v", err)
	}
	req.Roles = r
	return req, nil
}

// resolveDispatchAuthority is step (2e). It refuses when the workspace config
// didn't load (a missing file included: neither the level nor the ceiling
// may fall back to a default because the file was unreadable), finds the
// overlay the way provisioning will, validates the three layers, parses and
// resolves the request, and reads the dispatcher's own record for the parent
// link. Every refusal returns before the preflight, so nothing exists yet.
func resolveDispatchAuthority(warn io.Writer, cwd, workspaceRoot, wsConfigPath string, wsConfig *config.WorkspaceConfig, cfgErr error, gc *config.GlobalConfig) (*dispatchAuthority, error) {
	if cfgErr != nil || wsConfig == nil {
		if cfgErr == nil {
			cfgErr = errors.New("no configuration was loaded")
		}
		return nil, refuse("loading %s: %v; dispatch resolves its authority from that file and won't fall back to a default", wsConfigPath, cfgErr)
	}
	sourceURL, err := registeredConfigSourceURL(workspaceRoot, wsConfig, gc)
	if err != nil {
		return nil, refuse("%v", err)
	}
	overlay, err := workspace.ResolveAuthorityOverlay(workspaceRoot, sourceURL)
	if err != nil {
		return nil, refuse("%v", err)
	}
	policy, warnings, err := config.AuthorityPolicy(wsConfig, overlay, gc)
	for _, w := range warnings {
		fmt.Fprintf(warn, "niwa dispatch: warning: %s\n", w)
	}
	if err != nil {
		return nil, authorityRefusal{err}
	}
	req, err := parseDispatchRequest(dispatchLevel, dispatchGrants, dispatchRoles)
	if err != nil {
		return nil, err
	}
	resolved, err := authority.Resolve(policy, req)
	if err != nil {
		return nil, authorityRefusal{err}
	}
	parent, parentSrc, err := dispatchParent(cwd, workspaceRoot)
	if err != nil {
		return nil, err
	}
	auth := &dispatchAuthority{
		request:   req,
		policy:    policy,
		overlay:   overlay,
		resolved:  resolved,
		parentSrc: parentSrc,
		parent:    parent,
	}
	if parent != nil {
		auth.parentID = parent.DispatchID
	}
	return auth, nil
}

// dispatchParent finds the dispatcher's own record: through --parent-from
// when given, else from the working directory, never from the session id
// variable (a Codex worker inherits its dispatcher's). It returns the whole
// record and how it was found, or nil and none when the directory is in no
// instance or the instance has no record. A record that exists but can't be
// read refuses: recording no parent for an instance that has one would hide
// the link later checks follow.
func dispatchParent(cwd, workspaceRoot string) (*authority.Record, string, error) {
	cwdInstance, cwdInInstance, err := workspace.FindInstance(cwd)
	if err != nil {
		return nil, "", refuse("resolving the working directory for the parent link: %v", err)
	}
	instance, source := "", authority.ParentSourceNone
	switch {
	case dispatchParentFromSet:
		anchor, err := checkParentFrom(dispatchParentFrom, workspaceRoot)
		if err != nil {
			return nil, "", err
		}
		if cwdInInstance && cwdInstance.Instance != anchor.Instance {
			return nil, "", refuse("--parent-from %s is in instance %s, but this dispatch runs inside instance %s; the anchor must name the instance it runs in",
				strconv.Quote(dispatchParentFrom), anchor.Instance, cwdInstance.Instance)
		}
		instance, source = anchor.Instance, authority.ParentSourceAnchor
	case cwdInInstance:
		instance, source = cwdInstance.Instance, authority.ParentSourceCwd
	default:
		return nil, authority.ParentSourceNone, nil
	}
	rec, found, err := workspace.ReadAuthorityRecord(instance)
	if err != nil {
		return nil, "", refuse("the dispatching instance %s has an authority record that can't be used: %v; refusing to dispatch rather than record no parent", instance, err)
	}
	if !found {
		return nil, authority.ParentSourceNone, nil
	}
	return &rec, source, nil
}

// checkParentFrom applies --parent-from's checks: the same as `niwa authority
// show -C` (not empty, absolute, existing), and stricter in that the path
// must be in an instance of this dispatch's workspace. Both roots are
// compared with symlinks resolved, since FindInstance resolves them and the
// workspace root the dispatch found may not be.
func checkParentFrom(path, workspaceRoot string) (workspace.FoundInstance, error) {
	q := strconv.Quote(path)
	if path == "" {
		return workspace.FoundInstance{}, refuse("--parent-from is empty; pass the absolute path of a directory in the dispatching instance")
	}
	if !filepath.IsAbs(path) {
		return workspace.FoundInstance{}, refuse("--parent-from %s is not an absolute path", q)
	}
	info, err := os.Stat(path)
	if err != nil {
		return workspace.FoundInstance{}, refuse("--parent-from %s does not exist", q)
	}
	if !info.IsDir() {
		return workspace.FoundInstance{}, refuse("--parent-from %s is not a directory", q)
	}
	found, ok, err := workspace.FindInstance(path)
	if err != nil {
		return workspace.FoundInstance{}, refuse("--parent-from %s: %v", q, err)
	}
	if !ok {
		return workspace.FoundInstance{}, refuse("--parent-from %s is not inside an instance of this workspace", q)
	}
	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return workspace.FoundInstance{}, refuse("resolving the workspace root %s: %v", workspaceRoot, err)
	}
	if found.WorkspaceRoot != root {
		return workspace.FoundInstance{}, refuse("--parent-from %s is in an instance of the workspace at %s, not this dispatch's workspace at %s",
			q, found.WorkspaceRoot, root)
	}
	return found, nil
}

// authorityLine is R19's line: the level and its source, then each granted
// authority with its sources, written "dispatch" and "role <name>".
func authorityLine(rec authority.Record) string {
	grant := "none"
	if len(rec.Grant) > 0 {
		parts := make([]string, len(rec.Grant))
		for i, g := range rec.Grant {
			sources := make([]string, len(g.Sources))
			for j, s := range g.Sources {
				sources[j] = strings.Replace(string(s), "role:", "role ", 1)
			}
			parts[i] = fmt.Sprintf("%s (%s)", g.Authority, strings.Join(sources, ", "))
		}
		grant = strings.Join(parts, ", ")
	}
	return fmt.Sprintf("niwa dispatch: authority level %s from %s; grant %s", rec.Level, rec.LevelSource, grant)
}

// grantAttribute is the niwa.authority.grant value: the granted authorities
// in R3 order joined with "/", which the lineage alphabet admits and a comma
// (the list separator) would not, or "none".
func grantAttribute(rec authority.Record) string {
	if len(rec.Grant) == 0 {
		return "none"
	}
	names := make([]string, len(rec.Grant))
	for i, g := range rec.Grant {
		names[i] = string(g.Authority)
	}
	return strings.Join(names, "/")
}

// recheckDispatchAuthority is the first half of step (8a). Provisioning may
// have refreshed the base config or cloned a convention overlay after (2e)
// resolved, so the same request is resolved again against the config now on
// disk and the overlay the new instance was built from. A refusal, or a level
// or grant different from what (2e) printed, refuses the dispatch (the
// rollback armed at (7) removes the instance). Otherwise the record keeps
// (2e)'s level, level source and grant, which the printed line described,
// and takes this resolution's ceilings and their sources, so it never claims
// a wider ceiling than the instance has.
func recheckDispatchAuthority(auth *dispatchAuthority, workspaceRoot, wsConfigPath, instancePath string, gc *config.GlobalConfig) (authority.Record, error) {
	loaded, err := config.Load(wsConfigPath)
	if err != nil {
		return authority.Record{}, refuse("re-reading %s after provisioning: %v; the instance was removed", wsConfigPath, err)
	}
	overlay, err := provisionedAuthorityOverlay(auth, workspaceRoot, instancePath, loaded.Config, gc)
	if err != nil {
		return authority.Record{}, refuse("%v; the instance was removed", err)
	}
	policy, _, err := config.AuthorityPolicy(loaded.Config, overlay, gc)
	changed := changedAuthoritySettings(auth.policy, policy)
	if err != nil {
		return authority.Record{}, policyChangedRefusal(changed, err.Error())
	}
	again, err := authority.Resolve(policy, auth.request)
	if err != nil {
		return authority.Record{}, policyChangedRefusal(changed, err.Error())
	}
	first := auth.resolved
	if again.Level != first.Level {
		return authority.Record{}, policyChangedRefusal(changed,
			fmt.Sprintf("the level would now resolve to %s from %s, not the %s from %s printed above", again.Level, again.LevelSource, first.Level, first.LevelSource))
	}
	if !reflect.DeepEqual(again.Grant, first.Grant) {
		return authority.Record{}, policyChangedRefusal(changed,
			fmt.Sprintf("the grant would now be %s, not the %s printed above", grantAttribute(again), grantAttribute(first)))
	}
	final := first
	final.LevelCeiling, final.LevelCeilingSource = again.LevelCeiling, again.LevelCeilingSource
	final.GrantCeiling, final.GrantCeilingSource = again.GrantCeiling, again.GrantCeilingSource
	return final, nil
}

// provisionedAuthorityOverlay is the overlay the new instance was built from.
// The instance's own state records the overlay URL provisioning applied, and
// ResolveAuthorityOverlay reads it from there. With no URL recorded it falls
// back to the convention clone, which exists when provisioning cloned it, and
// then to (2e)'s overlay, so an overlay that narrowed at (2e) is never
// dropped because the instance state didn't record it. A workspace root that
// opted out of overlays has none.
//
// An instance state that can't be read says nothing about the overlay, so
// the workspace root's state stands in for it, as it did at (2e); a
// convention clone provisioning made is still found that way. The permission
// mode treats the same broken file as a warning rather than a refusal, and
// this keeps the two consistent without reading it as "no overlay".
func provisionedAuthorityOverlay(auth *dispatchAuthority, workspaceRoot, instancePath string, cfg *config.WorkspaceConfig, gc *config.GlobalConfig) (*config.WorkspaceOverlay, error) {
	if rootState, err := workspace.LoadState(workspaceRoot); err == nil && rootState.NoOverlay {
		return nil, nil
	}
	sourceURL, err := registeredConfigSourceURL(workspaceRoot, cfg, gc)
	if err != nil {
		return nil, err
	}
	stateDir := instancePath
	if _, err := workspace.LoadState(instancePath); err != nil {
		stateDir = workspaceRoot
	}
	overlay, err := workspace.ResolveAuthorityOverlay(stateDir, sourceURL)
	if err != nil {
		return nil, err
	}
	if overlay == nil {
		overlay = auth.overlay
	}
	return overlay, nil
}

func policyChangedRefusal(changed []string, detail string) error {
	what := "the authority policy"
	if len(changed) > 0 {
		what = strings.Join(changed, ", ")
	}
	return refuse("the authority policy changed while the instance was provisioned (%s changed): %s; the instance was removed and nothing launched, so dispatch again to resolve against the new policy",
		what, detail)
}

// changedAuthoritySettings names each setting that differs between the
// policy (2e) resolved against and the one (8a) found. The machine layer is
// left out: dispatch read it once, so it can't change in between.
func changedAuthoritySettings(before, after authority.Policy) []string {
	var out []string
	if !reflect.DeepEqual(before.BaseLevel, after.BaseLevel) {
		out = append(out, "the workspace config's [authority] level")
	}
	if !reflect.DeepEqual(before.BaseCeiling, after.BaseCeiling) {
		out = append(out, "the workspace config's "+authority.BaseCeilingKey)
	}
	if !reflect.DeepEqual(before.Roles, after.Roles) {
		out = append(out, "the workspace config's [authority.roles]")
	}
	if !reflect.DeepEqual(before.OverlayLevel, after.OverlayLevel) {
		out = append(out, "the overlay's [authority] level")
	}
	if !reflect.DeepEqual(before.OverlayCeiling, after.OverlayCeiling) {
		out = append(out, "the "+authority.OverlayCeilingKey)
	}
	return out
}

// stampDispatchRecord is the second half of step (8a): the identity fields
// dispatch holds, on the record the re-check returned.
func stampDispatchRecord(rec authority.Record, auth *dispatchAuthority, dispatchID, parentSessionID string) authority.Record {
	rec.DispatchID = dispatchID
	rec.ParentSessionID = parentSessionID
	rec.ParentDispatchID = auth.parentID
	rec.ParentDispatchSource = auth.parentSrc
	return rec
}
