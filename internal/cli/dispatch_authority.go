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
	"github.com/tsukumogami/niwa/internal/lineage"
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

// The authority flags. dispatchLevelSet and dispatchParentFromSet tell an
// explicit blank value (a refusal) from no flag at all.
var (
	dispatchLevel         string
	dispatchLevelSet      bool
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
	// parent is the dispatcher's whole record, nil when there is none.
	parent *authority.Record
	// cap is the most this dispatch may grant: what its dispatcher holds, nil
	// when there is no dispatcher to compare against. (2e) and (8a) both
	// check the grant against it.
	cap *authority.DispatcherCap
	// The lineage entry's links, with how niwa came by each. The parent
	// session link's id is the calling session's id dispatch uses everywhere
	// else (the telemetry attribute, the record, the mapping): the session
	// variable's value when it is well formed, else "".
	parentSession  lineage.Link
	parentDispatch lineage.Link
	principal      lineage.Principal
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

// checkMachineAuthority validates the machine's authority_level,
// authority_ceiling and principal at (2a), beside the permission mode, so a
// broken machine setting stops the dispatch before anything else reads it.
func checkMachineAuthority(gc *config.GlobalConfig, cfgPath string) error {
	if _, err := gc.AuthorityLevel(); err != nil {
		return refuse("%v (in %s)", err, cfgPath)
	}
	if _, err := gc.AuthorityCeiling(); err != nil {
		return refuse("%v (in %s)", err, cfgPath)
	}
	if _, err := gc.Principal(); err != nil {
		return refuse("%v (in %s)", err, cfgPath)
	}
	return nil
}

// parseDispatchRequest turns the three request flags into an
// authority.Request. Vocabulary is checked by authority.Resolve, which names
// the flag; this only splits the comma lists, refusing an empty entry, and
// parses the level. levelSet says --level was given: ParseLevel reads a blank
// value as not set, which is right for config and wrong for a value someone
// typed, so a given --level that is blank is refused rather than resolving to
// the level ceiling.
func parseDispatchRequest(level string, levelSet bool, grants, roles []string) (authority.Request, error) {
	var req authority.Request
	if levelSet && strings.TrimSpace(level) == "" {
		return req, refuse("--level is empty; accepted values are %s. Leave out --level to take the level ceiling", strings.Join(authority.LevelNames(), ", "))
	}
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
// link. sessionVar is the raw value of the variable the launch spec names for
// the calling session, read once by the caller at this step; the lineage
// links are resolved from it and from what this step finds. Every refusal
// returns before the preflight, so nothing exists yet.
func resolveDispatchAuthority(warn io.Writer, cwd, workspaceRoot, wsConfigPath, sessionVar string, wsConfig *config.WorkspaceConfig, cfgErr error, gc *config.GlobalConfig) (*dispatchAuthority, error) {
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
	req, err := parseDispatchRequest(dispatchLevel, dispatchLevelSet, dispatchGrants, dispatchRoles)
	if err != nil {
		return nil, err
	}
	resolved, err := authority.Resolve(policy, req)
	if err != nil {
		return nil, authorityRefusal{err}
	}
	parent, parentSrc, cwdInstance, cwdInInstance, err := dispatchParent(cwd, workspaceRoot)
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
	// The child may hold no named authority its dispatcher doesn't, refused
	// here with the rest of (2e), before anything exists.
	auth.cap = dispatcherCap(workspaceRoot, sessionVar, parent, cwdInstance, cwdInInstance)
	if err := authority.CapByDispatcher(resolved, auth.cap); err != nil {
		return nil, authorityRefusal{err}
	}
	// checkMachineAuthority refused an invalid principal at (2a).
	declared, _ := gc.Principal()
	in := gatherLineageInputs(workspaceRoot, sessionVar, cwdInstance, cwdInInstance, auth.parentID, parentSrc, declared)
	auth.parentSession, auth.parentDispatch, auth.principal = lineage.Resolve(in)
	return auth, nil
}

// gatherLineageInputs gathers what the link and principal rules read beyond
// what dispatchParent already found. Both session mapping reads are best
// effort: a store or a mapping that can't be read counts as no mapping, which
// makes the session link handed and the root fallback none, never observed
// or mapped.
//
// The instance's newest mapping is read only when --parent-from was not
// given, since the rules don't consult it then. The mapping keyed by the
// variable's value is read only at the workspace root (the working directory
// in no instance, no --parent-from) and only when the value is a lowercase
// UUID, the only key shape the store accepts.
//
// The parent's lineage entry is read by whichever parent dispatch id
// resolved: the dispatcher's record or the root mapping. An entry that is
// missing, unreadable or of a later version is left nil, which the principal
// rules read as a parent whose principal couldn't be inherited.
func gatherLineageInputs(workspaceRoot, sessionVar string, cwdInstance workspace.FoundInstance, cwdInInstance bool, parentID, parentSrc, declaredPrincipal string) lineage.Inputs {
	in := lineage.Inputs{
		SessionVar:         sessionVar,
		ParentFromSet:      dispatchParentFromSet,
		CwdInInstance:      cwdInInstance,
		ParentDispatchID:   parentID,
		ParentDispatchFrom: parentSrc,
		DeclaredPrincipal:  declaredPrincipal,
	}
	switch {
	case dispatchParentFromSet:
	case cwdInInstance:
		in.NewestMappedSession = newestMappedSession(workspaceRoot, cwdInstance.Instance)
	// workspace.ValidSessionID is the store's lowercase-UUID key shape, not
	// the looser calling-session shape lineage.Resolve accepts for the link.
	case workspace.ValidSessionID(sessionVar):
		if m, err := workspace.ReadSessionMapping(workspaceRoot, sessionVar); err == nil {
			in.RootMappedDispatch = m.DispatchID
		}
	}
	// A record's parent id is set only for the cwd and --parent-from rows,
	// and the root mapping is read only when neither applies, so at most one
	// of the two names the parent dispatch. lineage.Resolve checks the entry
	// it gets is the parent link's.
	parentDispatch := parentID
	if parentDispatch == "" {
		parentDispatch = in.RootMappedDispatch
	}
	in.ParentEntry = readParentEntry(workspaceRoot, parentDispatch)
	return in
}

// readParentEntry reads the parent dispatch's lineage entry, nil when there
// is no parent id, the id isn't a dispatch id, or the entry is missing,
// unreadable or of a later version.
func readParentEntry(workspaceRoot, dispatchID string) *lineage.Entry {
	if dispatchID == "" {
		return nil
	}
	stored, found, err := workspace.ReadLineageEntry(workspaceRoot, dispatchID)
	if err != nil || !found || stored.Parsed.LaterVersion {
		return nil
	}
	e := stored.Parsed.Entry
	return &e
}

// newestMappedSession is the session id of the newest mapping naming the
// instance, or "" when none does or the store can't be listed. FindInstance
// resolves symlinks and the path a mapping records may not be resolved, so a
// mapping names the instance when its cleaned path is the instance's or
// resolves to it. Every such mapping is then grouped under the instance's
// path, whatever its spelling, so NewestMappingPerInstance picks the newest
// of them all.
func newestMappedSession(workspaceRoot, instance string) string {
	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil {
		return ""
	}
	key := filepath.Clean(instance)
	var named []workspace.SessionMapping
	for _, m := range mappings {
		path := filepath.Clean(m.InstancePath)
		if path != key {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil || resolved != key {
				continue
			}
		}
		m.InstancePath = key
		named = append(named, m)
	}
	return workspace.NewestMappingPerInstance(named)[key].SessionID
}

// dispatchParent finds the dispatcher's own record: through --parent-from
// when given, else from the working directory, never from the session id
// variable (a Codex worker inherits its dispatcher's). It returns the whole
// record and how it was found, or nil and none when the directory is in no
// instance or the instance has no record. A record that exists but can't be
// read refuses: recording no parent for an instance that has one would hide
// the link later checks follow. It also returns the working directory's
// instance and whether there is one (the zero value and false when not),
// which the lineage links read.
func dispatchParent(cwd, workspaceRoot string) (*authority.Record, string, workspace.FoundInstance, bool, error) {
	cwdInstance, cwdInInstance, err := workspace.FindInstance(cwd)
	if err != nil {
		return nil, "", workspace.FoundInstance{}, false, refuse("resolving the working directory for the parent link: %v", err)
	}
	instance, source := "", authority.ParentSourceNone
	switch {
	case dispatchParentFromSet:
		anchor, err := checkParentFrom(dispatchParentFrom, workspaceRoot)
		if err != nil {
			return nil, "", cwdInstance, cwdInInstance, err
		}
		if cwdInInstance && cwdInstance.Instance != anchor.Instance {
			return nil, "", cwdInstance, cwdInInstance, refuse("--parent-from %s is in instance %s, but this dispatch runs inside instance %s; the anchor must name the instance it runs in",
				strconv.Quote(dispatchParentFrom), anchor.Instance, cwdInstance.Instance)
		}
		instance, source = anchor.Instance, authority.ParentSourceAnchor
	case cwdInInstance:
		instance, source = cwdInstance.Instance, authority.ParentSourceCwd
	default:
		return nil, authority.ParentSourceNone, cwdInstance, cwdInInstance, nil
	}
	rec, found, err := workspace.ReadAuthorityRecord(instance)
	if err != nil {
		return nil, "", cwdInstance, cwdInInstance, refuse("the dispatching instance %s has an authority record that can't be used: %v; refusing to dispatch rather than record no parent", instance, err)
	}
	if !found {
		return nil, authority.ParentSourceNone, cwdInstance, cwdInInstance, nil
	}
	return &rec, source, cwdInstance, cwdInInstance, nil
}

// dispatcherCap finds what this dispatch's dispatcher holds, the most the
// child may be granted, or nil when there is no dispatcher to compare
// against and only the grant ceiling limits the child.
//
// Inside an instance (the working directory's, or the one --parent-from
// names) the dispatcher is that instance's dispatch: its record's grant caps
// the child, and a later-version record holds nothing. An instance with no
// record that a dispatch-written mapping names was dispatched and lost its
// record, so it caps at nothing; one with neither (made by `niwa create`)
// has no dispatcher.
//
// At the workspace root there is no instance to read, so the dispatcher is
// the calling session, when the session id variable names a mapping
// dispatch wrote: that dispatch's record caps the child, and a record that is
// missing, unusable or names another dispatch caps at nothing. A Codex worker
// inherits its dispatcher's variable, so it may name an ancestor rather than
// the caller; an ancestor's grant is at least as wide, so this never refuses
// wrongly. No such mapping, no dispatcher.
func dispatcherCap(workspaceRoot, sessionVar string, parent *authority.Record, cwdInstance workspace.FoundInstance, cwdInInstance bool) *authority.DispatcherCap {
	if parent != nil {
		return &authority.DispatcherCap{DispatchID: parent.DispatchID, Held: authority.HeldAuthorities(parent.Grant)}
	}
	instance := ""
	switch {
	case dispatchParentFromSet:
		// dispatchParent has already checked the anchor.
		if found, ok, err := workspace.FindInstance(dispatchParentFrom); err == nil && ok {
			instance = found.Instance
		}
	case cwdInInstance:
		instance = cwdInstance.Instance
	}
	if instance != "" {
		if m, ok := dispatchMappingNaming(workspaceRoot, instance); ok {
			return &authority.DispatcherCap{DispatchID: m.DispatchID, Missing: true}
		}
		return nil
	}
	if !workspace.ValidSessionID(sessionVar) {
		return nil
	}
	m, err := workspace.ReadSessionMapping(workspaceRoot, sessionVar)
	if err != nil || m.Origin != workspace.MappingOriginDispatch {
		return nil
	}
	if m.InstancePath != "" && filepath.IsAbs(m.InstancePath) {
		if found, ok, err := workspace.FindInstance(m.InstancePath); err == nil && ok {
			rec, present, err := workspace.ReadAuthorityRecord(found.Instance)
			if err == nil && present && rec.DispatchID == m.DispatchID {
				return &authority.DispatcherCap{DispatchID: rec.DispatchID, Held: authority.HeldAuthorities(rec.Grant)}
			}
		}
	}
	return &authority.DispatcherCap{DispatchID: m.DispatchID, Missing: true}
}

// dispatchMappingNaming finds a dispatch-written session mapping whose
// instance is instance, symlinks resolved. A store that can't be listed
// names nothing.
func dispatchMappingNaming(workspaceRoot, instance string) (workspace.SessionMapping, bool) {
	mappings, err := workspace.ListSessionMappings(workspaceRoot)
	if err != nil {
		return workspace.SessionMapping{}, false
	}
	for _, m := range mappings {
		if m.Origin == workspace.MappingOriginDispatch && mappingNamesInstance(m, instance) {
			return m, true
		}
	}
	return workspace.SessionMapping{}, false
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
// authority with its sources, written "dispatch" and "role <name>" by the
// rendering `niwa authority show` uses, so the two can't drift.
func authorityLine(rec authority.Record) string {
	return fmt.Sprintf("niwa dispatch: authority level %s from %s; grant %s", rec.Level, rec.LevelSource, authority.GrantListText(rec.Grant))
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
	// The same dispatcher as (2e), so provisioning can't widen the child.
	if err := authority.CapByDispatcher(again, auth.cap); err != nil {
		return authority.Record{}, refuse("%v; the instance was removed", err)
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
	// The same resolution the lineage entry is written from, so the two agree
	// while the instance lives.
	rec.ParentSessionSource = auth.parentSession.Source
	rec.Principal = &authority.PrincipalDoc{
		Name:   auth.principal.Name,
		Source: auth.principal.Source,
		Trust:  auth.principal.Trust,
	}
	return rec
}
