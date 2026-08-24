package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/plugin"
)

// This file delivers niwa's own plugin -- the one carrying the skills niwa
// ships itself -- to the agents the contract declares it implemented for.
//
// There are two procedures rather than one because the two deliveries are not
// the same act. Claude's lands in the developer's own home, once per machine,
// and outlives every instance; Codex's lands inside one instance and is
// reclaimed with it. A single procedure stretched over both would have to
// branch on which it was doing, which is the branch the declaration table
// exists to remove.
//
// Both are procedure-routed rather than plan-borne, and for the same reason
// from opposite directions: Claude's write is outside every instance, which no
// plan entry can describe honestly, and Codex's needs the embedded tree on disk
// before an entry can name it as a source -- an embed.FS is not a path a
// symlink can point at. What follows the extraction is an ordinary plan entry.

// niwaPluginContentDirName is the directory under an instance's own .niwa where
// niwa's embedded plugin tree is extracted.
//
// It is deliberately not under the marketplace content directory. That
// directory's names are claimed by the marketplaces a workspace configures, and
// extracting niwa's own tree there under the name "niwa" would mean a workspace
// that configures a marketplace by that name either reads niwa's embedded
// content as its marketplace or silently overwrites it. Here the site collision
// cannot happen at all: the two live under different parents, whatever either
// one is named.
const niwaPluginContentDirName = "plugin"

// niwaPluginManifestFile is the file inside the extracted tree carrying its
// version. It is the same file plugin.Install compares to decide whether the
// tree under the developer's home is current, read here for the same decision
// about the copy inside the instance.
const niwaPluginManifestFile = "manifest.json"

// niwaPluginContentRoot is where niwa's own plugin tree is extracted for one
// instance.
func niwaPluginContentRoot(instanceRoot string) string {
	return filepath.Join(instanceRoot, ".niwa", niwaPluginContentDirName)
}

// claudeNiwaPluginProcedure delivers niwa's own plugin to Claude Code: the
// embedded tree materialized at the user-level install path, in the format
// Claude Code's plugin system reads.
//
// The claim stops there. What this delivers is a tree at the path and in the
// shape that system expects; whether a given Claude session resolves it also
// depends on the installation registering it, which is not this write's to
// make.
type claudeNiwaPluginProcedure struct{}

// Name is the delivery name the contract binds this procedure under.
func (claudeNiwaPluginProcedure) Name() string { return string(agentplan.DeliveryNiwaPluginClaude) }

// Deliver installs the embedded plugin under the developer's own home and
// reports what happened, once.
//
// The install itself runs on every apply and is meant to: it is idempotent, and
// a version that moved on is how the developer gets the current plugin. The
// notice is the part that must not repeat. It says where the plugin landed and
// which skill to invoke, which is orientation a developer needs the first time
// and noise on every apply after it -- so it goes through the same one-time
// disclosure record the other once-per-workspace notices use, rather than
// firing alongside every prepared instance.
//
// Nothing here fails an apply, which is the posture the installer was built
// for: a user-environment failure comes back as Failed, whose notice carries
// the manual-install command, and the only error the installer returns is a
// build-time invariant violation -- a malformed embedded manifest -- which is
// worth a warning and not worth stopping a create over.
//
// The record is not touched. Retraction is the one thing a record is for, and
// this install has nothing to retract: it owns its install path outright and
// replaces it wholesale, so there is no per-instance entry that could outlive
// the instance that asked for it.
func (claudeNiwaPluginProcedure) Deliver(in procedureInput) (procedureResult, error) {
	action, err := plugin.Install(in.DeveloperHome, plugin.InstallOpts{SkipInstall: in.SkipPluginInstall})
	if err != nil {
		return procedureResult{
			Recorded: in.Recorded,
			Warnings: []string{fmt.Sprintf("could not install the niwa plugin: %v", err)},
		}, nil
	}

	// A different outcome than last time is a different notice, and the user
	// hears each one once: a workspace that opted out and later opted back in
	// is told the plugin is there, having previously been told it was not.
	id := PluginInstallNoticeID(action)
	if id == "" || slices.Contains(in.Disclosed, id) {
		return procedureResult{Recorded: in.Recorded}, nil
	}
	EmitPluginNotice(id, plugin.ManualInstallCommand, in.Reporter)
	return procedureResult{Recorded: in.Recorded, Disclosed: []string{id}}, nil
}

// codexNiwaPluginProcedure delivers niwa's own plugin to an agent that reads
// skills out of a delivered tree: the embedded tree extracted inside the
// instance, then delivered into the instance root's skills directory.
type codexNiwaPluginProcedure struct{}

// Name is the delivery name the contract binds this procedure under.
func (codexNiwaPluginProcedure) Name() string { return string(agentplan.DeliveryNiwaPluginCodex) }

// Deliver extracts the embedded tree into the instance and links it into the
// root skills directory.
//
// The two halves are one delivery and fail as one. An extraction with no link
// leaves bytes nothing reads; a link with no extraction is a dangling symlink
// in a directory a session enumerates. Where the contract declares this
// capability implemented, either failure fails the apply -- the caller names
// the capability -- rather than degrading to a warning about a skill that
// silently is not there.
func (codexNiwaPluginProcedure) Deliver(in procedureInput) (procedureResult, error) {
	res := procedureResult{Recorded: in.Recorded}

	source, err := ensureNiwaPluginTree(in.InstanceRoot)
	if err != nil {
		return res, err
	}

	plan, err := in.Producer.NiwaPluginPlan(agentplan.NiwaPluginInputs{
		Dir:    in.InstanceRoot,
		Source: source,
	})
	if err != nil {
		return res, err
	}
	if _, _, err := applyPlan(plan); err != nil {
		return res, err
	}
	return res, nil
}

// ensureNiwaPluginTree makes niwa's own plugin tree available on disk inside
// the instance and returns its root.
//
// It is idempotent by manifest version, the same comparison plugin.Install
// makes under the developer's home: a tree already at the embedded version is
// left exactly as it is, so an apply does not swap the tree under a session
// that is reading it.
//
// A replacement is path-stable, and that is load-bearing rather than tidy. The
// delivery into the skills directory is a symlink to this path, and a symlink
// survives its target being replaced only if the replacement lands back at the
// same path. So the new tree is staged beside the old one and renamed onto it,
// exactly as fetched marketplace content is replaced -- never removed first and
// rebuilt in place, which would leave the link dangling for the length of the
// rebuild.
func ensureNiwaPluginTree(instanceRoot string) (string, error) {
	if instanceRoot == "" {
		return "", fmt.Errorf("no instance to materialize the niwa plugin into")
	}

	embedded, err := plugin.Embedded()
	if err != nil {
		return "", err
	}

	dest := filepath.Join(niwaPluginContentRoot(instanceRoot), agentplan.NiwaPluginTreeName)
	if version, ok := niwaPluginTreeVersion(dest); ok && version == embedded.Version {
		return dest, nil
	}

	staging := dest + ".staging"
	if err := os.RemoveAll(staging); err != nil {
		return "", fmt.Errorf("clearing the staging directory for the niwa plugin tree: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", fmt.Errorf("creating the niwa plugin directory: %w", err)
	}
	if err := plugin.MaterializeTo(staging); err != nil {
		os.RemoveAll(staging)
		return "", fmt.Errorf("extracting the niwa plugin tree: %w", err)
	}
	if err := os.RemoveAll(dest); err != nil {
		os.RemoveAll(staging)
		return "", fmt.Errorf("replacing the niwa plugin tree: %w", err)
	}
	if err := os.Rename(staging, dest); err != nil {
		os.RemoveAll(staging)
		return "", fmt.Errorf("promoting the niwa plugin tree: %w", err)
	}

	return dest, nil
}

// niwaPluginTreeVersion reads the version of an extracted niwa plugin tree.
// Anything that stops it from answering -- no tree, no manifest, a manifest
// that does not parse -- reports "not there", which sends the caller down the
// extraction path. A tree that cannot be read for its version is one nothing
// can vouch for, and re-extracting is cheap.
func niwaPluginTreeVersion(root string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(root, niwaPluginManifestFile))
	if err != nil {
		return "", false
	}
	var m struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &m); err != nil || m.Version == "" {
		return "", false
	}
	return m.Version, true
}
