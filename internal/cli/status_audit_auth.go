package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// auditAuthRow is one row of the `niwa status --audit-auth` table.
// Kind and Project are split from the state.AuthSources map key
// "<kind>/<project>"; Source and Fallback come straight from the
// AuthSourceRecord (already pre-rendered by the credential pool's
// renderSource / renderVaultProvider helpers, including the AC-39
// "vault:(anonymous)" form).
type auditAuthRow struct {
	Kind     string
	Project  string
	Source   string
	Fallback string
}

// runAuditAuth renders the credential-source audit table from the
// most recent apply (machine-identity-vault-sync, PRD R11). Reads
// state.AuthSources offline; never makes a vault or network call.
// Exits non-zero (returned error) when any row has Source="none".
//
// The view mirrors --audit-secrets in command shape: discover the
// nearest instance, load its state, render a fixed-column text
// table, decide exit code from the data.
func runAuditAuth(cmd *cobra.Command, cwd string) error {
	instanceRoot, err := workspace.DiscoverInstance(cwd)
	if err != nil {
		return fmt.Errorf("--audit-auth must run inside a workspace instance: %w", err)
	}
	state, err := workspace.LoadState(instanceRoot)
	if err != nil {
		return fmt.Errorf("loading instance state: %w", err)
	}

	rows := buildAuditAuthRows(state.AuthSources)
	printAuditAuthTable(cmd.OutOrStdout(), rows)

	// Exit non-zero if any row has Source="none". The user can run
	// `niwa apply` to rebuild credentials and clear the row; until
	// then, a CI gate that runs `niwa status --audit-auth` correctly
	// flags the broken state.
	for _, row := range rows {
		if row.Source == "none" {
			return fmt.Errorf(
				"at least one credential resolved to source=none in the last apply " +
					"(no entry in ~/.config/niwa/provider-auth.toml, no entry in the " +
					"personal vault, and no usable CLI session). Populate the missing " +
					"credential then re-run `niwa apply`.",
			)
		}
	}
	return nil
}

// buildAuditAuthRows splits each "<kind>/<project>" key into its two
// halves and produces an alphabetically-stable slice for rendering.
// Sort order: KIND ascending, then PROJECT-UUID ascending. Empty
// AuthSources produces an empty slice (no rows printed).
//
// Key-split assumption: vault provider Kind values must not contain
// "/" — the credential pool's AuditTrail.AsMap encodes keys as
// rec.Kind + "/" + rec.Project, and strings.Cut here splits at the
// FIRST "/". Today's only registered Kind is "infisical"; future
// backends should follow the same constraint. If a Kind ever needs
// to contain "/", switch to LastIndex-based splitting and update
// AsMap symmetrically.
func buildAuditAuthRows(authSources map[string]workspace.AuthSourceRecord) []auditAuthRow {
	if len(authSources) == 0 {
		return nil
	}
	rows := make([]auditAuthRow, 0, len(authSources))
	for key, rec := range authSources {
		kind, project, _ := strings.Cut(key, "/")
		rows = append(rows, auditAuthRow{
			Kind:     kind,
			Project:  project,
			Source:   rec.Source,
			Fallback: rec.Fallback,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Project < rows[j].Project
	})
	return rows
}

// printAuditAuthTable writes the four-column text table to w.
// Empty Fallback renders as the em-dash ("—"). Column widths match
// PRD R11's example header byte-for-byte; see the constant block
// inside the function for the exact derivation.
//
// The header row is always printed, even when there are zero
// content rows, so a user running --audit-auth on a fresh workspace
// sees a recognizable empty table rather than blank output.
func printAuditAuthTable(w io.Writer, rows []auditAuthRow) {
	// Column widths match PRD R11's example header
	// `KIND       PROJECT-UUID                          SOURCE              FALLBACK`
	// byte-for-byte: KIND padded to 11 chars (allowing two trailing spaces
	// after the longest kind in current use, "infisical"); PROJECT-UUID
	// padded to 38 chars (a UUID's 36 chars plus two trailing spaces);
	// SOURCE padded to 20 chars (longest known value is
	// "vault:(anonymous)" at 17 chars plus three trailing spaces).
	const (
		kindWidth    = 11
		projectWidth = 38
		sourceWidth  = 20
	)
	fmt.Fprintf(w, "%-*s%-*s%-*s%s\n",
		kindWidth, "KIND",
		projectWidth, "PROJECT-UUID",
		sourceWidth, "SOURCE",
		"FALLBACK",
	)
	for _, row := range rows {
		fallback := row.Fallback
		if fallback == "" {
			fallback = "—"
		}
		fmt.Fprintf(w, "%-*s%-*s%-*s%s\n",
			kindWidth, row.Kind,
			projectWidth, row.Project,
			sourceWidth, row.Source,
			fallback,
		)
	}
}
