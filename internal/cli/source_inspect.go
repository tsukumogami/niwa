package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/source"
)

func init() {
	sourceCmd.AddCommand(sourceInspectCmd)
	sourceInspectCmd.Flags().Bool("json", false, "Emit machine-readable JSON instead of human-readable text")
	rootCmd.AddCommand(sourceCmd)
}

var sourceCmd = &cobra.Command{
	Use:   "source",
	Short: "Inspect workspace config sources",
	Long: `Inspect workspace config sources.

Subcommands:
  inspect    Read-only probe of a source slug — reports rank, resolved subpath,
             and ambiguity/no-marker errors without writing anything to disk.`,
}

var sourceInspectCmd = &cobra.Command{
	Use:   "inspect <slug>",
	Short: "Probe a source slug and report which marker layout it uses",
	Long: `Probe a source slug and report which marker layout it uses.

The slug accepts the full grammar:

    [host/]owner/repo[:subpath][@ref]

For example:

    niwa source inspect dangazineu/foo
    niwa source inspect github.com/dangazineu/foo:.niwa@main

The command is read-only: it fetches the source tarball, inspects only the
header-level entries to find marker files (.niwa/workspace.toml for rank-1
or workspace.toml at the repo root for rank-2), and emits either a
human-readable summary or a JSON object describing what it found.

Exit codes:
  0    successful probe (rank resolved unambiguously)
  non-zero on ambiguity (both rank-1 and rank-2 markers present),
  no-marker (neither marker present), or any fetch error.`,
	Args: cobra.ExactArgs(1),
	RunE: runSourceInspect,
}

// sourceInspectFetcher is the FetchClient-shaped interface
// source_inspect uses. Production wires *github.APIClient via
// newInspectFetcher; tests override newInspectFetcher to inject a
// fake.
type sourceInspectFetcher interface {
	FetchTarball(ctx context.Context, owner, repo, ref, etag string) (io.ReadCloser, string, int, *github.RenameRedirect, error)
}

// newInspectFetcher is the production constructor; tests can
// reassign this variable to inject a fake.
var newInspectFetcher = func() sourceInspectFetcher {
	return github.NewAPIClient(resolveGitHubToken())
}

// inspectExit allows tests to capture the exit code instead of
// terminating the test process. Production assigns os.Exit; tests
// reassign to a callback that records the code.
var inspectExit = os.Exit

// inspectResult is the schema_version=1 JSON envelope.
type inspectResult struct {
	SchemaVersion       int             `json:"schema_version"`
	Slug                string          `json:"slug"`
	Host                string          `json:"host"`
	Owner               string          `json:"owner"`
	Repo                string          `json:"repo"`
	ExplicitSubpath     string          `json:"explicit_subpath"`
	MarkersFoundAtRoot  []string        `json:"markers_found_at_root"`
	Resolved            *inspectResolve `json:"resolved,omitempty"`
	Error               *inspectError   `json:"error,omitempty"`
}

type inspectResolve struct {
	Rank           int    `json:"rank"`
	Subpath        string `json:"subpath"`
	Deprecated     bool   `json:"deprecated"`
	MigrationHint  string `json:"migration_hint"`
}

type inspectError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func runSourceInspect(cmd *cobra.Command, args []string) error {
	slug := args[0]
	emitJSON, _ := cmd.Flags().GetBool("json")

	src, err := source.Parse(slug)
	if err != nil {
		return fmt.Errorf("could not parse slug %q: %w", slug, err)
	}

	if !src.IsGitHub() {
		return fmt.Errorf("source inspect currently only supports GitHub sources (got host=%q)", src.Host)
	}

	result := inspectResult{
		SchemaVersion:      1,
		Slug:               slug,
		Host:               effectiveHost(src),
		Owner:              src.Owner,
		Repo:               src.Repo,
		ExplicitSubpath:    src.Subpath,
		MarkersFoundAtRoot: []string{},
	}

	fetcher := newInspectFetcher()
	ref := src.Ref
	if ref == "" {
		ref = "HEAD"
	}

	body, _, status, _, err := fetcher.FetchTarball(cmd.Context(), src.Owner, src.Repo, ref, "")
	if err != nil {
		return fmt.Errorf("fetching tarball for %s/%s: %w", src.Owner, src.Repo, err)
	}
	if body != nil {
		defer body.Close()
	}
	if status != 200 {
		return fmt.Errorf("fetching tarball for %s/%s: status %d", src.Owner, src.Repo, status)
	}

	markers := config.TeamConfigMarkerSet()
	found, err := probeFromTarball(body, markers)
	if err != nil {
		return fmt.Errorf("probing tarball: %w", err)
	}

	if found.Rank1Dir != "" && found.Rank1File != "" {
		result.MarkersFoundAtRoot = append(result.MarkersFoundAtRoot, markers.Rank1Dir+"/"+markers.Rank1File)
	}
	if found.Rank2Path != "" {
		result.MarkersFoundAtRoot = append(result.MarkersFoundAtRoot, markers.Rank2Path)
	}

	subpath, _, decErr := config.RankDecider(found, markers)
	exitCode := 0
	switch {
	case decErr == nil:
		rank := 1
		if subpath == "" {
			rank = 2
		}
		resolved := &inspectResolve{
			Rank:    rank,
			Subpath: subpath,
		}
		if rank == 2 {
			resolved.Deprecated = true
			resolved.MigrationHint = "Run /niwa:migrate-config to migrate the source to the rank-1 layout (.niwa/workspace.toml)."
		}
		result.Resolved = resolved
	case config.IsAmbiguousMarkers(decErr):
		result.Error = &inspectError{Code: "ambiguous", Message: decErr.Error()}
		exitCode = 1
	case config.IsNoMarker(decErr):
		result.Error = &inspectError{Code: "no_marker", Message: decErr.Error()}
		exitCode = 1
	default:
		return decErr
	}

	if emitJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return fmt.Errorf("encoding JSON: %w", err)
		}
	} else {
		printInspectText(cmd.OutOrStdout(), result)
	}

	if exitCode != 0 {
		inspectExit(exitCode)
	}
	return nil
}

// probeFromTarball gunzips body and runs github.ProbeMarkers on the
// resulting tar reader. It buffers the decompressed stream so the
// caller doesn't need to manage an extraction destination.
func probeFromTarball(body io.Reader, markers config.MarkerSet) (config.MarkerSet, error) {
	gz, err := gzip.NewReader(body)
	if err != nil {
		return config.MarkerSet{}, fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, gz); err != nil {
		return config.MarkerSet{}, fmt.Errorf("reading tar stream: %w", err)
	}
	tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
	return github.ProbeMarkers(tr, markers)
}

func effectiveHost(s source.Source) string {
	if s.Host == "" {
		return "github.com"
	}
	return s.Host
}

func printInspectText(w io.Writer, r inspectResult) {
	fmt.Fprintf(w, "Source: %s\n", r.Slug)
	fmt.Fprintf(w, "  host:    %s\n", r.Host)
	fmt.Fprintf(w, "  owner:   %s\n", r.Owner)
	fmt.Fprintf(w, "  repo:    %s\n", r.Repo)
	if r.ExplicitSubpath != "" {
		fmt.Fprintf(w, "  subpath: %s (explicit)\n", r.ExplicitSubpath)
	}
	if len(r.MarkersFoundAtRoot) == 0 {
		fmt.Fprintf(w, "  markers: (none found at source root)\n")
	} else {
		fmt.Fprintf(w, "  markers found at root: %v\n", r.MarkersFoundAtRoot)
	}
	if r.Resolved != nil {
		fmt.Fprintf(w, "Resolved:\n")
		fmt.Fprintf(w, "  rank:    %d\n", r.Resolved.Rank)
		if r.Resolved.Subpath != "" {
			fmt.Fprintf(w, "  subpath: %s\n", r.Resolved.Subpath)
		}
		if r.Resolved.Deprecated {
			fmt.Fprintf(w, "  deprecated: true\n")
			fmt.Fprintf(w, "  migration: %s\n", r.Resolved.MigrationHint)
		}
	}
	if r.Error != nil {
		fmt.Fprintf(w, "Error:\n")
		fmt.Fprintf(w, "  code:    %s\n", r.Error.Code)
		fmt.Fprintf(w, "  message: %s\n", r.Error.Message)
	}
}
