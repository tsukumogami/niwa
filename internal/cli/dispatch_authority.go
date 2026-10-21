package cli

import (
	"errors"
	"fmt"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
)

// resolveDispatchAuthority resolves the authority level one dispatch runs at,
// from the --authority value, the loaded workspace config and the machine
// ceiling already read from the personal config. Every error it returns is a
// refusal, already worded for the developer, and the dispatch must stop on it
// before it provisions anything.
//
// The flag is checked against the three level names exactly (after trimming),
// and a value wider than the ceiling is refused rather than clamped, so a
// developer never believes a dispatch got more room than it did. The refusal
// names the requested level, the ceiling and the setting the ceiling came
// from, which is the file to change.
func resolveDispatchAuthority(flag string, ws *config.WorkspaceConfig, wsConfigPath string, machine *authority.Level) (authority.Resolved, error) {
	flagLevel, err := authority.Parse(flag)
	if err != nil {
		return authority.Resolved{}, fmt.Errorf("niwa: error: --authority: %w", err)
	}
	wsLevel, err := ws.AuthorityLevel()
	if err != nil {
		return authority.Resolved{}, fmt.Errorf("niwa: error: %w (in %s)", err, wsConfigPath)
	}
	resolved, err := authority.Resolve(authority.Inputs{Workspace: wsLevel, Machine: machine, Flag: flagLevel})
	if err != nil {
		var wider *authority.WiderError
		if errors.As(err, &wider) {
			return authority.Resolved{}, fmt.Errorf(
				"niwa: error: --authority %s is wider than this dispatch's ceiling, %s from %s; a dispatch may only narrow the authority level",
				wider.Requested, wider.Ceiling.Level, wider.Ceiling.Source)
		}
		return authority.Resolved{}, fmt.Errorf("niwa: error: %w", err)
	}
	return resolved, nil
}
