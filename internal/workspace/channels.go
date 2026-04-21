package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// channelsMCPEntry is the template for .claude/.mcp.json. It registers the
// niwa mcp-serve command with NIWA_INSTANCE_ROOT baked in so Claude Code
// can start the MCP server without any user configuration.
const channelsMCPEntry = `{
  "mcpServers": {
    "niwa": {
      "type": "stdio",
      "command": "niwa",
      "args": ["mcp-serve"],
      "env": {
        "NIWA_INSTANCE_ROOT": %s
      }
    }
  }
}`

// channelsSectionHeader is the marker used to detect an already-present
// ## Channels section in workspace-context.md. The check-then-append
// logic looks for this exact string to avoid duplicates on re-apply.
const channelsSectionHeader = "## Channels"

// InstallChannelInfrastructure creates the session mesh filesystem artifacts
// required for cross-session communication:
//
//   - <instanceRoot>/.niwa/sessions/ (mode 0700, idempotent)
//   - <instanceRoot>/.niwa/sessions/sessions.json (mode 0600, only if absent)
//   - <instanceRoot>/.claude/.mcp.json (mode 0600, always overwritten)
//   - ## Channels section appended to workspace-context.md (idempotent)
//
// Returns immediately without creating anything when cfg.Channels is empty.
// writtenFiles is appended with the paths of any files written.
func InstallChannelInfrastructure(cfg *config.WorkspaceConfig, instanceRoot string, writtenFiles *[]string) error {
	if cfg.Channels.IsEmpty() {
		return nil
	}

	// 1. Create .niwa/sessions/ with 0700.
	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		return fmt.Errorf("creating sessions directory: %w", err)
	}

	// 2. Create sessions.json only if absent (preserve existing session registrations).
	// Always add to writtenFiles so cleanRemovedFiles does not delete it on re-apply.
	sessionsJSONPath := filepath.Join(sessionsDir, "sessions.json")
	if _, err := os.Stat(sessionsJSONPath); os.IsNotExist(err) {
		if err := writeFileMode(sessionsJSONPath, []byte("{\"sessions\":[]}\n"), 0o600); err != nil {
			return fmt.Errorf("creating sessions.json: %w", err)
		}
	}
	*writtenFiles = append(*writtenFiles, sessionsJSONPath)

	// 3. Write .claude/.mcp.json (always overwritten).
	mcpDir := filepath.Join(instanceRoot, ".claude")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		return fmt.Errorf("creating .claude directory: %w", err)
	}
	mcpJSONPath := filepath.Join(mcpDir, ".mcp.json")
	mcpContent, err := buildMCPJSON(instanceRoot)
	if err != nil {
		return fmt.Errorf("building .mcp.json: %w", err)
	}
	if err := writeFileMode(mcpJSONPath, mcpContent, 0o600); err != nil {
		return fmt.Errorf("writing .mcp.json: %w", err)
	}
	*writtenFiles = append(*writtenFiles, mcpJSONPath)

	// 4. Write hook scripts to .niwa/hooks/ so HooksMaterializer can copy
	// them by file path. Scripts are small wrappers that invoke niwa commands.
	hooksDir := filepath.Join(instanceRoot, ".niwa", "hooks")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		return fmt.Errorf("creating hooks directory: %w", err)
	}
	sessionStartScript := filepath.Join(hooksDir, "mesh-session-start.sh")
	if err := writeFileMode(sessionStartScript, []byte("#!/bin/sh\nniwa session register\n"), 0o755); err != nil {
		return fmt.Errorf("writing mesh-session-start.sh: %w", err)
	}
	*writtenFiles = append(*writtenFiles, sessionStartScript)

	userPromptScript := filepath.Join(hooksDir, "mesh-user-prompt-submit.sh")
	if err := writeFileMode(userPromptScript, []byte("#!/bin/sh\nniwa session register --check-only\n"), 0o755); err != nil {
		return fmt.Errorf("writing mesh-user-prompt-submit.sh: %w", err)
	}
	*writtenFiles = append(*writtenFiles, userPromptScript)

	// 5. Append ## Channels section to workspace-context.md (idempotent).
	ctxPath := filepath.Join(instanceRoot, workspaceContextFile)
	if err := appendChannelsSection(ctxPath, cfg); err != nil {
		return fmt.Errorf("appending channels section: %w", err)
	}

	return nil
}

// buildMCPJSON returns the content for .claude/.mcp.json with instanceRoot
// baked into NIWA_INSTANCE_ROOT.
func buildMCPJSON(instanceRoot string) ([]byte, error) {
	rootJSON, err := json.Marshal(instanceRoot)
	if err != nil {
		return nil, err
	}
	content := fmt.Sprintf(channelsMCPEntry, string(rootJSON))
	return []byte(content + "\n"), nil
}

// appendChannelsSection appends the ## Channels section to the workspace
// context file when it is not already present. No-op when the section header
// already exists anywhere in the file.
func appendChannelsSection(ctxPath string, cfg *config.WorkspaceConfig) error {
	existing, err := os.ReadFile(ctxPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	if strings.Contains(string(existing), channelsSectionHeader) {
		return nil
	}

	section := buildChannelsSection(cfg)

	content := string(existing)
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += "\n" + section

	return writeFileMode(ctxPath, []byte(content), 0o644)
}

// buildChannelsSection generates the ## Channels markdown section from config.
func buildChannelsSection(cfg *config.WorkspaceConfig) string {
	var sb strings.Builder
	sb.WriteString("## Channels\n\n")
	sb.WriteString("This workspace uses niwa cross-session communication. Sessions can exchange\n")
	sb.WriteString("messages using the niwa MCP tools.\n\n")

	if len(cfg.Channels.Mesh.Roles) > 0 {
		sb.WriteString("### Roles\n\n")
		for role, repo := range cfg.Channels.Mesh.Roles {
			if repo != "" {
				fmt.Fprintf(&sb, "- `%s` — %s\n", role, repo)
			} else {
				fmt.Fprintf(&sb, "- `%s`\n", role)
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### Tools\n\n")
	sb.WriteString("- `niwa_check_messages` — check this session's inbox for new messages\n")
	sb.WriteString("- `niwa_send_message` — send a typed message to another session by role\n")
	sb.WriteString("- `niwa_ask` — send a question and block until the recipient replies (or timeout)\n")
	sb.WriteString("- `niwa_wait` — block until a threshold number of messages matching type/from filters arrive\n")

	return sb.String()
}

// writeFileMode writes data to path with the given mode bits, independent of umask.
// It creates parent directories if needed, using a tmp-then-rename pattern for
// atomic writes, then applies Chmod explicitly so the mode is not subject to umask.
func writeFileMode(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// injectChannelHooks inserts SessionStart and UserPromptSubmit hook entries
// into cfg.Claude.Hooks when the workspace has channel config. Hook entries
// are prepended so they run before any user-defined hooks for the same event.
// This mutates cfg in place and is called at the top of runPipeline before
// any per-repo processing. HooksMaterializer reads Scripts as file paths and
// copies them with os.ReadFile, so these must point to real files on disk.
//
// ORDERING CONTRACT: Call InstallChannelInfrastructure (step 4.75) before
// HooksMaterializer runs (step 6.5). injectChannelHooks records paths to
// scripts that don't yet exist on disk; InstallChannelInfrastructure writes
// them. Calling injectChannelHooks without a subsequent InstallChannelInfrastructure
// produces hook entries pointing to nonexistent files that fail at materialize time.
func injectChannelHooks(cfg *config.WorkspaceConfig, instanceRoot string) {
	if cfg.Channels.IsEmpty() {
		return
	}

	if cfg.Claude.Hooks == nil {
		cfg.Claude.Hooks = make(config.HooksConfig)
	}

	hooksDir := filepath.Join(instanceRoot, ".niwa", "hooks")
	sessionStartScript := filepath.Join(hooksDir, "mesh-session-start.sh")
	userPromptScript := filepath.Join(hooksDir, "mesh-user-prompt-submit.sh")

	// SessionStart: register this session with the mesh.
	sessionStartEntry := config.HookEntry{
		Scripts: []string{sessionStartScript},
	}
	// UserPromptSubmit: check messages at each prompt.
	userPromptEntry := config.HookEntry{
		Scripts: []string{userPromptScript},
	}

	cfg.Claude.Hooks["session_start"] = prependHookEntry(cfg.Claude.Hooks["session_start"], sessionStartEntry)
	cfg.Claude.Hooks["user_prompt_submit"] = prependHookEntry(cfg.Claude.Hooks["user_prompt_submit"], userPromptEntry)
}

// prependHookEntry returns a new slice with entry prepended before existing.
func prependHookEntry(existing []config.HookEntry, entry config.HookEntry) []config.HookEntry {
	result := make([]config.HookEntry, 0, len(existing)+1)
	result = append(result, entry)
	result = append(result, existing...)
	return result
}
