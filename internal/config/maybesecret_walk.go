package config

import "fmt"

// MaybeSecretSlot is one value-carrying position in a WorkspaceConfig that can
// hold a secret.
//
// Key is the environment-variable or settings name the value is written under,
// empty where the slot has no such name. Secrets reports whether the slot sits
// in a table that is sensitivity-coded as secret by declaration -- an
// `env.secrets` table -- independently of whether its value happens to be a
// vault:// reference.
type MaybeSecretSlot struct {
	Location string
	Key      string
	Value    MaybeSecret
	Secrets  bool
	// Repo names the repo override this slot belongs to, empty for
	// workspace-level and instance-level slots.
	Repo string
}

// WalkMaybeSecretSlots calls visit for every MaybeSecret-bearing value slot in
// cfg. It stops and returns the first error visit returns.
//
// This is the single enumeration of "where can a secret live in a workspace
// config". It exists because there were about to be two: the vault
// provider-name validation below walks these slots because it cannot work
// unless it reaches all of them, and the worktree path's output redactor needs
// the same coverage to know which values to scrub. A second, hand-maintained
// list of table names would go stale the first time someone adds a
// secret-capable field -- silently, and in the mechanism meant to prevent
// leaks.
//
// Deriving both from here means a future field wired into vault validation --
// which is already load-bearing and already obliged to be complete -- feeds
// redaction with no separate action.
//
// [files] source keys are deliberately NOT walked. They are plain strings that
// may carry a vault:// reference, so the vault validator checks them, but they
// name a source path rather than hold a value, so there is nothing there to
// redact.
func WalkMaybeSecretSlots(cfg *WorkspaceConfig, visit func(MaybeSecretSlot) error) error {
	if cfg == nil {
		return nil
	}

	walkEnv := func(prefix, repo string, env EnvConfig) error {
		for k, v := range env.Vars.Values {
			if err := visit(MaybeSecretSlot{
				Location: fmt.Sprintf("%s.vars.%s", prefix, k),
				Key:      k, Value: v, Secrets: false, Repo: repo,
			}); err != nil {
				return err
			}
		}
		for k, v := range env.Secrets.Values {
			if err := visit(MaybeSecretSlot{
				Location: fmt.Sprintf("%s.secrets.%s", prefix, k),
				Key:      k, Value: v, Secrets: true, Repo: repo,
			}); err != nil {
				return err
			}
		}
		return nil
	}

	walkClaudeEnv := func(prefix, repo string, env ClaudeEnvConfig) error {
		for k, v := range env.Vars.Values {
			if err := visit(MaybeSecretSlot{
				Location: fmt.Sprintf("%s.vars.%s", prefix, k),
				Key:      k, Value: v, Secrets: false, Repo: repo,
			}); err != nil {
				return err
			}
		}
		for k, v := range env.Secrets.Values {
			if err := visit(MaybeSecretSlot{
				Location: fmt.Sprintf("%s.secrets.%s", prefix, k),
				Key:      k, Value: v, Secrets: true, Repo: repo,
			}); err != nil {
				return err
			}
		}
		return nil
	}

	walkSettings := func(prefix, repo string, settings map[string]MaybeSecret) error {
		for k, v := range settings {
			if err := visit(MaybeSecretSlot{
				Location: fmt.Sprintf("%s.%s", prefix, k),
				Key:      k, Value: v, Repo: repo,
			}); err != nil {
				return err
			}
		}
		return nil
	}

	if err := walkEnv("env", "", cfg.Env); err != nil {
		return err
	}
	if err := walkClaudeEnv("claude.env", "", cfg.Claude.Env); err != nil {
		return err
	}
	if err := walkSettings("claude.settings", "", cfg.Claude.Settings); err != nil {
		return err
	}

	for name, ov := range cfg.Repos {
		if err := walkEnv(fmt.Sprintf("repos.%s.env", name), name, ov.Env); err != nil {
			return err
		}
		if ov.Claude != nil {
			if err := walkClaudeEnv(fmt.Sprintf("repos.%s.claude.env", name), name, ov.Claude.Env); err != nil {
				return err
			}
			if err := walkSettings(fmt.Sprintf("repos.%s.claude.settings", name), name, ov.Claude.Settings); err != nil {
				return err
			}
		}
	}

	if err := walkEnv("instance.env", "", cfg.Instance.Env); err != nil {
		return err
	}
	if cfg.Instance.Claude != nil {
		if err := walkClaudeEnv("instance.claude.env", "", cfg.Instance.Claude.Env); err != nil {
			return err
		}
		if err := walkSettings("instance.claude.settings", "", cfg.Instance.Claude.Settings); err != nil {
			return err
		}
	}

	// [mcp.servers.*] env and headers, and [session.env.vars], are the
	// agent-neutral MaybeSecret slots. They are easy to forget precisely
	// because they are not env tables -- and the worktree path writes both
	// into the tree, so a redactor that missed them would leave a literal
	// token in a script's reach.
	for _, name := range cfg.MCP.MCPServerNames() {
		srv := cfg.MCP.Servers[name]
		for k, v := range srv.Env {
			if err := visit(MaybeSecretSlot{
				Location: fmt.Sprintf("mcp.servers.%s.env.%s", name, k),
				Key:      k, Value: v,
			}); err != nil {
				return err
			}
		}
		for k, v := range srv.Headers {
			if err := visit(MaybeSecretSlot{
				Location: fmt.Sprintf("mcp.servers.%s.headers.%s", name, k),
				Key:      k, Value: v,
			}); err != nil {
				return err
			}
		}
	}
	for k, v := range cfg.Session.Env.Vars.Values {
		if err := visit(MaybeSecretSlot{
			Location: fmt.Sprintf("session.env.vars.%s", k),
			Key:      k, Value: v,
		}); err != nil {
			return err
		}
	}

	return nil
}

// SecretCapableKeys returns the environment-variable names whose values must be
// treated as secret for the named repo.
//
// A key qualifies two ways, and both are needed:
//
//   - It sits in a `secrets` table. Sensitivity is coded by table there --
//     the resolver wraps those values regardless of whether they look like a
//     vault reference -- so a literal token declared under `env.secrets` counts.
//   - Its configured value is a vault:// reference, wherever it is declared.
//     Nothing forbids one in a `vars` table: validate_vault_refs.go denies
//     vault references in content paths, env-file paths, provider config and
//     identifier fields, but not in `env.vars` values.
//
// The second case is why this cannot key off the table alone, and why it is
// answered syntactically from cfg rather than from persisted provenance.
// SourceEntry records a Kind and a SourceID that identifies the ORIGIN -- a
// file path, or provider-name/key -- and hangs off managed files rather than
// env keys, so it can say a file had a vault-sourced input but not which key
// that was. Using it would mean registering every value in any file with any
// vault source, which is the over-scrub this exists to avoid.
//
// Slots belonging to a different repo are skipped; workspace-level and
// instance-level slots apply to every repo.
func SecretCapableKeys(cfg *WorkspaceConfig, repo string) map[string]bool {
	keys := map[string]bool{}
	_ = WalkMaybeSecretSlots(cfg, func(s MaybeSecretSlot) error {
		if s.Key == "" {
			return nil
		}
		if s.Repo != "" && s.Repo != repo {
			return nil
		}
		if s.Secrets || hasVaultPrefix(s.Value.Plain) {
			keys[s.Key] = true
		}
		return nil
	})
	return keys
}
