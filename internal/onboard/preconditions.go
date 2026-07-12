// PRD R22 (DESIGN-niwa-onboard.md Phase 7): the wizard-entry
// preconditions. Neither of these is a fail-fast condition (that's
// R18, and it covers only a missing setup/topology override on a
// non-TTY run) -- a missing authenticated session and an unregistered
// or not-yet-scaffolded personal overlay are both in-scope pauses the
// wizard walks the operator through, mirroring the topology login
// pauses of R4.
package onboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault/infisical"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// SessionChecker abstracts infisical.DetectSessionStatus so tests can
// inject a fake without a real `infisical` CLI on PATH. nil (the
// production default consulted by EnsureAuthenticatedSession) calls
// infisical.DetectSessionStatus(ctx, nil), which itself falls back to
// the real CLI commander.
type SessionChecker func(ctx context.Context) (infisical.SessionStatus, error)

// EnsureAuthenticatedSession implements R22's session precondition:
// at wizard start, check whether the operator has an authenticated
// `infisical` session. When none exists, walk the operator through
// `infisical login` as an in-scope pause and resume -- it MUST NOT
// fail fast on a missing session (AC-36). Loops until the session
// checker reports Authenticated, or pause returns an error (e.g. the
// operator's input stream closed).
func EnsureAuthenticatedSession(ctx context.Context, checker SessionChecker, pause func(prompt string) error) error {
	if checker == nil {
		checker = func(ctx context.Context) (infisical.SessionStatus, error) {
			return infisical.DetectSessionStatus(ctx, nil)
		}
	}
	for {
		status, err := checker(ctx)
		if err != nil {
			return fmt.Errorf("onboard: checking infisical session: %w", err)
		}
		if status.Authenticated {
			return nil
		}
		if pause == nil {
			return fmt.Errorf("onboard: EnsureAuthenticatedSession requires a non-nil pause function when no infisical session is authenticated")
		}
		if err := pause("No authenticated infisical session found. Run `infisical login` in another terminal now, then press Enter to continue."); err != nil {
			return fmt.Errorf("onboard: waiting for infisical login: %w", err)
		}
		// Loop back and re-check -- the operator may have just logged in.
	}
}

// EnsurePersonalOverlayParams collects EnsurePersonalOverlay's inputs.
type EnsurePersonalOverlayParams struct {
	// OverlayDir is the personal-overlay clone directory
	// (config.GlobalConfigDir() in production).
	OverlayDir string
	// Repo is the operator-supplied personal-overlay slug
	// ("owner/repo") to register when the pointer is unregistered.
	// Resolving this value interactively is left to the caller -- the
	// prompt kit has no free-text input primitive today (only
	// Confirm/Select/Pause), so this function treats an empty Repo on
	// an unregistered pointer as an error rather than inventing a
	// prompt here.
	Repo       string
	GitInvoker workspace.GitInvoker
	// Pause backs the scaffold-and-guide step's "create and push the
	// repo" instruction. Only consulted when the overlay repo doesn't
	// exist yet and a scaffold was written.
	Pause func(prompt string) error
}

// EnsurePersonalOverlayResult reports what EnsurePersonalOverlay did.
type EnsurePersonalOverlayResult struct {
	// PointerWrite is the WriteLocalPointer result, populated only
	// when the pointer was unregistered and this call registered it.
	PointerWrite WriteResult
	// ScaffoldedNew reports whether this call scaffolded a brand-new
	// local overlay repo (OverlayDir had no .git entry yet).
	ScaffoldedNew bool
}

// EnsurePersonalOverlay implements R22's personal-overlay
// precondition: setting up the personal overlay is a wizard-performed
// step, not an assumed precondition.
//
//   - When the overlay pointer is not registered
//     (config.LoadGlobalConfig().GlobalConfig.Repo == ""), it registers
//     p.Repo via the existing WriteLocalPointer (an operator-local
//     `niwa config set global` write).
//   - When the personal-overlay repo does not exist yet at p.OverlayDir
//     (no .git entry), it scaffolds the overlay config locally --
//     `git init`, a minimal niwa.toml, a local commit -- and guides the
//     operator (via p.Pause) to create a remote and push it. This
//     function NEVER creates a remote repo on the operator's behalf
//     (AC-37): no GitHub/git-host API is ever called here.
//
// An already-registered pointer and an already-existing overlay repo
// are each a no-op for their half of this function.
func EnsurePersonalOverlay(ctx context.Context, p EnsurePersonalOverlayParams) (EnsurePersonalOverlayResult, error) {
	result := EnsurePersonalOverlayResult{}

	cfg, err := config.LoadGlobalConfig()
	if err != nil {
		return result, fmt.Errorf("onboard: loading global config: %w", err)
	}
	if cfg.GlobalConfig.Repo == "" {
		if p.Repo == "" {
			return result, fmt.Errorf("onboard: personal-overlay pointer is unregistered and no repo was supplied to register")
		}
		wr, err := WriteLocalPointer(p.Repo)
		if err != nil {
			return result, fmt.Errorf("onboard: registering personal-overlay pointer: %w", err)
		}
		result.PointerWrite = wr
	}

	if p.OverlayDir == "" {
		return result, fmt.Errorf("onboard: EnsurePersonalOverlay requires a non-empty OverlayDir")
	}
	gitDir := filepath.Join(p.OverlayDir, ".git")
	if _, statErr := os.Stat(gitDir); statErr == nil {
		return result, nil // overlay repo already exists; nothing else to do.
	} else if !os.IsNotExist(statErr) {
		return result, fmt.Errorf("onboard: checking personal-overlay repo: %w", statErr)
	}

	if p.GitInvoker == nil {
		return result, fmt.Errorf("onboard: EnsurePersonalOverlay requires a non-nil GitInvoker to scaffold a new overlay repo")
	}

	if err := os.MkdirAll(p.OverlayDir, 0o755); err != nil {
		return result, fmt.Errorf("onboard: creating personal-overlay directory: %w", err)
	}

	initCmd := p.GitInvoker.CommandContext(ctx, "-C", p.OverlayDir, "init")
	if out, err := initCmd.CombinedOutput(); err != nil {
		return result, fmt.Errorf("onboard: git init: %w\n%s", err, out)
	}

	tomlPath := filepath.Join(p.OverlayDir, workspace.GlobalConfigOverrideFile)
	if _, statErr := os.Stat(tomlPath); os.IsNotExist(statErr) {
		scaffold := "# niwa personal-overlay config, scaffolded by `niwa onboard`.\n" +
			"# See docs on `niwa config set global` for what belongs here.\n"
		if err := atomicWriteFile(tomlPath, []byte(scaffold), 0o600); err != nil {
			return result, fmt.Errorf("onboard: scaffolding %s: %w", tomlPath, err)
		}
	}

	addCmd := p.GitInvoker.CommandContext(ctx, "-C", p.OverlayDir, "add", workspace.GlobalConfigOverrideFile)
	if out, err := addCmd.CombinedOutput(); err != nil {
		return result, fmt.Errorf("onboard: git add: %w\n%s", err, out)
	}
	commitCmd := p.GitInvoker.CommandContext(ctx, "-C", p.OverlayDir, "commit", "-m", "onboard: scaffold personal-overlay config")
	commitCmd.Env = sanitizeCommitEnv(os.Environ())
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return result, fmt.Errorf("onboard: git commit: %w\n%s", err, out)
	}
	result.ScaffoldedNew = true

	if p.Pause != nil {
		guidance := fmt.Sprintf(
			"Scaffolded a personal-overlay config at %s (committed locally, not pushed).\n"+
				"Create an empty repository for it on your git host, then run:\n"+
				"  git -C %s remote add origin <your-repo-url>\n"+
				"  git -C %s push -u origin HEAD\n"+
				"Press Enter once pushed.",
			p.OverlayDir, p.OverlayDir, p.OverlayDir,
		)
		if err := p.Pause(guidance); err != nil {
			return result, fmt.Errorf("onboard: waiting for personal-overlay push: %w", err)
		}
	}

	return result, nil
}
