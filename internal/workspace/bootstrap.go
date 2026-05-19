package workspace

import (
	"context"
	"os/exec"

	"github.com/tsukumogami/niwa/internal/source"
)

// GitInvoker is the test-injection seam for the bootstrap pipeline's git
// subprocess calls. Production wires stdGitInvoker{}, which delegates to
// exec.CommandContext. Tests pass a recording invoker that captures argv
// and supplies fake outputs without forking real git, so unit coverage
// can assert classifier ordering and argument shape without a working
// git binary on PATH.
//
// The interface deliberately exposes only CommandContext (returning the
// *exec.Cmd verbatim) so production code retains full control over
// stdin/stdout/stderr/env wiring. A higher-level "RunGitArgs" shape was
// considered and rejected because it would force the seam to absorb
// every stdio configuration the orchestrator wants to apply.
type GitInvoker interface {
	CommandContext(ctx context.Context, args ...string) *exec.Cmd
}

// stdGitInvoker is the production implementation of GitInvoker. It
// invokes `git` resolved via PATH. Callers configure stdin/stdout/env
// on the returned *exec.Cmd.
type stdGitInvoker struct{}

// CommandContext returns exec.CommandContext(ctx, "git", args...).
func (stdGitInvoker) CommandContext(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, "git", args...)
}

// StdGitInvoker returns the production GitInvoker. The function form
// (rather than an exported var) keeps the zero value of stdGitInvoker
// internal so external callers can't mutate it.
func StdGitInvoker() GitInvoker { return stdGitInvoker{} }

// BootstrapParams collects the inputs to RunBootstrap. RunBootstrap is
// not implemented in this file — its body lands in the orchestrator
// issue (see DESIGN-init-bootstrap-empty-source.md §"RunBootstrap").
// Defining the struct here lets the scaffold + GetRepo + git-seam units
// compile and be unit-tested independently of the orchestrator.
//
// Field rationale:
//
//   - WorkspaceRoot — absolute path to the workspace being scaffolded.
//   - WorkspaceName — name written into [workspace] name = "...".
//   - Src           — parsed --from slug. Value (not pointer) for
//     consistency with EnsureConfigSnapshot's call shape.
//   - Fetcher       — the workspace.FetchClient already consumed by
//     EnsureConfigSnapshot; reused so the materialize/visibility paths
//     share one client.
//   - GitInvoker    — production stdGitInvoker{} or a test recorder.
//   - Reporter      — TTY-aware writer for the success block, R17 note,
//     and R18 commit summary.
//   - ScaffoldOpts  — passed to BOTH the pre-create scaffold call and
//     the post-create scaffold call inside the bootstrap repo. The
//     copies MUST be equal so the on-disk bytes match (Appendix A
//     contract).
type BootstrapParams struct {
	WorkspaceRoot string
	WorkspaceName string
	Src           source.Source
	Fetcher       FetchClient
	GitInvoker    GitInvoker
	Reporter      *Reporter
	ScaffoldOpts  ScaffoldOptions
}
