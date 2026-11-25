package cli

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/removal"
)

// rollbackUnit is what a caller outside the pass tells the removal rule
// about an instance it is about to destroy: a failed dispatch's rollback,
// the watch's discard of a staged review, or the watch's failed-stage
// rollback. The caller knows the session's liveness itself, so the rule is
// given that verdict rather than a claude agents listing.
type rollbackUnit struct {
	// Name is the instance's name for the kept line; empty uses the
	// directory's name.
	Name string
	// InstancePath is the instance directory.
	InstancePath string
	// Liveness and LivenessReason are the caller's verdict on the session
	// behind the instance. The rule's process listing still keeps an
	// instance with a live process in it, whatever the caller says.
	Liveness       removal.Liveness
	LivenessReason string
	// SessionID, when set, is the session whose job directory the rule
	// judges with the instance. Empty judges the instance alone and reads no
	// jobs root.
	SessionID string
}

// keptByRule asks the removal rule about u before its caller destroys the
// instance, and reports whether the instance must stay. A kept instance gets
// one line on w in the pass's form, "niwa: kept <name>: <check> check:
// <evidence>", or with the reason the rule couldn't be asked; the caller then
// removes nothing.
//
// It reuses the pass's pieces and adds no rule of its own: the workspace
// configuration and its removal policy, the rule's environment (closed
// before it returns), the process listing, the jobs root and
// removal.Evaluate, whose first keeping check is the one named.
//
// Only a positive "no such file" on the instance path skips the rule and
// lets the caller destroy as before: there is nothing on disk to lose. Any
// other reason the rule can't be asked keeps: the instance path can't be
// read, the workspace's removal settings are invalid, the configuration
// can't be loaded, or the jobs root a session's job directory sits under
// can't be resolved.
//
// The rule runs under its own deadline, not the caller's context: a rollback
// that runs because its command was interrupted still gets a verdict rather
// than a keep from a walk the cancellation stopped.
func keptByRule(w io.Writer, workspaceRoot string, u rollbackUnit) bool {
	name := u.Name
	if name == "" {
		name = filepath.Base(u.InstancePath)
	}
	keep := func(reason string) bool {
		reportKeptUnit(w, keptUnit{Name: name, Reason: reason})
		return true
	}

	if _, err := os.Lstat(u.InstancePath); errors.Is(err, fs.ErrNotExist) {
		return false
	} else if err != nil {
		return keep("its instance can't be read: " + err.Error())
	}

	cfg, cfgErr := loadWorkspaceConfig(workspaceRoot)
	if removalSettingsInvalid(cfgErr) {
		return keep("the workspace's removal settings are invalid")
	}
	if cfgErr != nil {
		return keep("the workspace configuration can't be read: " + cfgErr.Error())
	}

	jobsDir, jobDir := "", ""
	if u.SessionID != "" {
		root, err := passJobsRoot()
		if err != nil {
			return keep(jobsRootReason(err))
		}
		jobsDir, jobDir = root, jobDirFor(root, u.SessionID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), passDeadline)
	defer cancel()
	env := newRemovalEnv(workspaceRoot, userHomeDir(), os.Getenv("PATH"))
	defer env.Close()
	policy := removalPolicy(cfg, env)
	policy.JobsRoot = jobsDir
	policy.Processes = listProcessesFunc(ctx, env)
	policy.Processes.Attribute([]string{u.InstancePath}, nil)

	verdict := removal.Evaluate(ctx, removal.Unit{
		Name:           name,
		InstancePath:   u.InstancePath,
		Liveness:       u.Liveness,
		LivenessReason: u.LivenessReason,
		JobDir:         jobDir,
	}, policy)
	if verdict.Kind == removal.Reap {
		return false
	}
	first, _ := verdict.First()
	reportKeptUnit(w, keptUnit{Name: name, Check: first})
	return true
}
