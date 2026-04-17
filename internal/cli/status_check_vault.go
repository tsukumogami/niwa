package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/resolve"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// runCheckVault re-resolves every recorded vault source via the
// configured team provider bundle and reports per-file rotations. Does
// NOT materialize any files: the only on-disk read is the state file
// itself, and only the provider RPCs reach the network.
//
// Compared with `niwa apply`:
//
//   - No repos are cloned, pulled, or classified.
//   - No guardrails run (public-remote check, dirty check, etc.).
//   - No files are written.
//
// Compared with default `niwa status`:
//
//   - Providers are invoked (the default status is fully offline).
//   - Only vault sources are checked; plaintext drift detection is not
//     repeated here.
func runCheckVault(cmd *cobra.Command, cwd string) error {
	configPath, _, err := config.Discover(cwd)
	if err != nil {
		return fmt.Errorf("finding workspace config: %w", err)
	}

	result, err := config.Load(configPath)
	if err != nil {
		return err
	}
	cfg := result.Config

	instanceRoot, err := workspace.DiscoverInstance(cwd)
	if err != nil {
		return fmt.Errorf("--check-vault must be run from inside an instance: %w", err)
	}

	state, err := workspace.LoadState(instanceRoot)
	if err != nil {
		return fmt.Errorf("loading instance state: %w", err)
	}

	if cfg.Vault == nil || cfg.Vault.IsEmpty() {
		fmt.Fprintln(cmd.OutOrStdout(), "no vault providers declared; nothing to check.")
		return nil
	}

	// Build the team bundle. The caller owns bundle lifetime; close
	// on function exit even on error paths (R29 no-disk-cache).
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	bundle, err := resolve.BuildBundle(ctx, nil, cfg.Vault, "workspace config")
	if err != nil {
		return err
	}
	defer bundle.CloseAll()

	rotations := detectVaultRotations(ctx, state, bundle)
	printVaultRotations(cmd, rotations)
	return nil
}

// vaultRotation describes one managed file whose fingerprint would
// change if a new apply were run, driven by at least one vault source
// whose provider-returned VersionToken differs from the recorded one.
type vaultRotation struct {
	Path           string
	ChangedSources []rotationSource
}

// rotationSource is a single vault SourceEntry that has rotated, with
// the old and new tokens for user-facing diagnosis.
type rotationSource struct {
	SourceID   string
	OldToken   string
	NewToken   string
	Provenance string
	Err        error // non-nil when re-resolution itself failed (unreachable backend, missing key, etc.)
}

// detectVaultRotations iterates every vault SourceEntry across every
// ManagedFile in state, re-resolving against bundle. Keys whose new
// VersionToken.Token differs from the recorded token are collected
// into the vaultRotation for the owning file.
//
// A resolution failure is reported as a rotation with Err set; the
// user needs to see unreachable providers even when no rotation is
// certain.
//
// Deduplication: the same vault source may back multiple managed
// files (e.g., a token promoted to both settings and .local.env). Each
// file gets its own rotation entry so the user sees every impacted
// output, but the provider is only queried once per unique SourceID.
func detectVaultRotations(ctx context.Context, state *workspace.InstanceState, bundle *vault.Bundle) []vaultRotation {
	// Cache per-source results so each unique ref is resolved once.
	type result struct {
		token string
		prov  string
		err   error
	}
	cache := map[string]result{}

	resolveSource := func(sourceID string) result {
		if r, ok := cache[sourceID]; ok {
			return r
		}
		ref, ok := refFromSourceID(sourceID)
		if !ok {
			r := result{err: fmt.Errorf("source id %q is not a recognized vault/key pair", sourceID)}
			cache[sourceID] = r
			return r
		}
		provider, perr := bundle.Get(ref.ProviderName)
		if perr != nil {
			r := result{err: perr}
			cache[sourceID] = r
			return r
		}
		_, token, rerr := provider.Resolve(ctx, ref)
		if rerr != nil {
			r := result{err: rerr}
			cache[sourceID] = r
			return r
		}
		r := result{token: token.Token, prov: token.Provenance}
		cache[sourceID] = r
		return r
	}

	// Collect files in a stable order.
	paths := make([]string, 0, len(state.ManagedFiles))
	byPath := make(map[string]workspace.ManagedFile, len(state.ManagedFiles))
	for _, mf := range state.ManagedFiles {
		paths = append(paths, mf.Path)
		byPath[mf.Path] = mf
	}
	sort.Strings(paths)

	var out []vaultRotation
	for _, path := range paths {
		mf := byPath[path]
		var changed []rotationSource
		for _, src := range mf.Sources {
			if src.Kind != workspace.SourceKindVault {
				continue
			}
			r := resolveSource(src.SourceID)
			if r.err != nil {
				changed = append(changed, rotationSource{
					SourceID: src.SourceID,
					OldToken: src.VersionToken,
					Err:      r.err,
				})
				continue
			}
			if r.token != src.VersionToken {
				changed = append(changed, rotationSource{
					SourceID:   src.SourceID,
					OldToken:   src.VersionToken,
					NewToken:   r.token,
					Provenance: r.prov,
				})
			}
		}
		if len(changed) > 0 {
			out = append(out, vaultRotation{Path: path, ChangedSources: changed})
		}
	}
	return out
}

// refFromSourceID parses a SourceID of the form "providerName/key"
// into a vault.Ref. Anonymous providers use "/key" and produce an
// empty ProviderName. Malformed inputs (zero slashes, or an empty key
// segment) return ok=false so the caller can decide how to report
// them.
//
// SourceIDs for vault sources are produced by
// internal/workspace.sourceForMaybeSecret in the canonical
// "providerName/key" form. This parser mirrors that format exactly.
func refFromSourceID(sourceID string) (vault.Ref, bool) {
	idx := strings.Index(sourceID, "/")
	if idx < 0 {
		return vault.Ref{}, false
	}
	provider := sourceID[:idx]
	key := sourceID[idx+1:]
	if key == "" {
		return vault.Ref{}, false
	}
	return vault.Ref{ProviderName: provider, Key: key}, true
}

// printVaultRotations writes the rotation list to the command's
// stdout. When no rotations are detected, emits a single
// "no rotations" line so the user sees the all-clear signal. When
// rotations are present, closes with a call-to-action pointing at
// `niwa apply` so the user knows the next step without having to
// infer it from the command's name — mirroring the pattern used by
// emitVaultBootstrapPointer after init.
func printVaultRotations(cmd *cobra.Command, rotations []vaultRotation) {
	out := cmd.OutOrStdout()
	if len(rotations) == 0 {
		fmt.Fprintln(out, "no vault rotations detected.")
		return
	}
	for _, r := range rotations {
		fmt.Fprintf(out, "vault-rotated %s\n", r.Path)
		for _, cs := range r.ChangedSources {
			if cs.Err != nil {
				fmt.Fprintf(out, "  source %s: error: %v\n", cs.SourceID, cs.Err)
				continue
			}
			fmt.Fprintf(out, "  source %s: %s -> %s\n",
				cs.SourceID, shortToken(cs.OldToken), shortToken(cs.NewToken))
			if cs.Provenance != "" {
				fmt.Fprintf(out, "    provenance: %s\n", cs.Provenance)
			}
		}
	}
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Run `niwa apply` to re-materialize affected files with the rotated values.")
}
