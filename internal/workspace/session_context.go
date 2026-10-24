package workspace

import (
	"fmt"
	"os"
	"strings"
)

// rootContextNiwaPath is the absolute path of the running niwa, or "" when it
// can't be resolved. The session-context hook falls back to it when PATH has
// no niwa, and the settings documents carry it as NIWA_BIN. With "" neither is
// written: a PATH-only hook would run nothing in exactly the environments the
// fallback exists for, and a hook then finds niwa on PATH or not at all.
func rootContextNiwaPath() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	return p
}

// maxContextRepos caps how many repos the session-start context lists, so an
// unusually large instance can't crowd out the rest of the session's context.
const maxContextRepos = 50

// Sentences of the root session context. They state facts rather than give
// instructions: Claude Code's hook documentation warns that imperative text
// in injected context can trip prompt-injection defenses.
const (
	rootContextIntro    = "This directory is a niwa instance root."
	rootContextRepos    = "Repos in this instance:"
	rootContextNoRepos  = "This instance has no repos."
	rootContextWorktree = "Worktrees here are created with EnterWorktree using a name of the form <repo>/<slug>."
)

// BuildRootContext builds the facts a session at an instance root starts
// with, and gets back after compaction: what the directory is, the repos it
// holds as <group>/<repo>, and, unless the instance opted out of worktree
// delegation, how worktrees are named here. It replaces guidance that used to
// live as generated prose in workspace-context.md, which is loaded once and
// isn't re-delivered after compaction.
func BuildRootContext(instanceRoot string) (string, error) {
	repos, err := enumerateHookRepos(instanceRoot)
	if err != nil {
		return "", err
	}
	optedOut := false
	if state, err := LoadState(instanceRoot); err == nil {
		optedOut = state.NoWorktreeDelegation
	}

	var b strings.Builder
	b.WriteString(rootContextIntro)
	b.WriteString("\n")
	if len(repos) == 0 {
		b.WriteString(rootContextNoRepos)
		b.WriteString("\n")
	} else {
		b.WriteString(rootContextRepos)
		b.WriteString("\n")
		for i, r := range repos {
			if i == maxContextRepos {
				fmt.Fprintf(&b, "- (and %d more)\n", len(repos)-maxContextRepos)
				break
			}
			fmt.Fprintf(&b, "- %s/%s\n", r.group, r.name)
		}
	}
	if !optedOut {
		b.WriteString(rootContextWorktree)
		b.WriteString("\n")
	}
	return b.String(), nil
}
