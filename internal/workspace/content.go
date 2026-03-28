package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// InstallWorkspaceContent reads the workspace content source file, expands
// template variables, and writes it to {instanceRoot}/CLAUDE.md.
func InstallWorkspaceContent(cfg *config.WorkspaceConfig, configDir, instanceRoot string) error {
	if cfg.Content.Workspace.Source == "" {
		return nil
	}

	contentDir := cfg.Workspace.ContentDir
	if contentDir == "" {
		contentDir = "."
	}

	sourcePath := filepath.Join(configDir, contentDir, cfg.Content.Workspace.Source)

	// Validate the resolved path stays within configDir.
	absSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("resolving source path: %w", err)
	}
	absConfigDir, err := filepath.Abs(configDir)
	if err != nil {
		return fmt.Errorf("resolving config dir: %w", err)
	}
	if !strings.HasPrefix(absSource, absConfigDir+string(filepath.Separator)) {
		return fmt.Errorf("content source %q escapes config directory", cfg.Content.Workspace.Source)
	}

	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("reading content source %s: %w", sourcePath, err)
	}

	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return fmt.Errorf("resolving instance root: %w", err)
	}

	content := expandVars(string(data), map[string]string{
		"{workspace}":      absInstance,
		"{workspace_name}": cfg.Workspace.Name,
	})

	target := filepath.Join(instanceRoot, "CLAUDE.md")
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		return fmt.Errorf("creating instance root: %w", err)
	}

	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", target, err)
	}

	return nil
}

// expandVars performs simple string replacement for template variables.
// Only the declared variables are expanded; no code execution.
func expandVars(s string, vars map[string]string) string {
	for k, v := range vars {
		s = strings.ReplaceAll(s, k, v)
	}
	return s
}
