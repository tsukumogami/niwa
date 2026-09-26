package agentplan

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// This file is the producer side of the settings document: the JSON file a
// prepared session reads its permission posture, hooks, environment, plugins,
// and marketplaces out of. niwa writes three of them -- one at the workspace
// root, one at the instance root, one per repository -- and until now each
// caller carried its own marshal-then-mkdir-then-write copy of the same six
// lines, which is three places for the indentation, the trailing newline, the
// file mode, or the directory name to drift apart in.
//
// The split follows the one the context producers use: internal/workspace still
// decides what the document says (buildSettingsDoc already returned a document
// and wrote nothing), and this file decides where it goes, what bytes it
// becomes, and what permission it is created with.

// settingsFileMode is the permission the settings document is written with. It
// is the restrictive one rather than the ordinary one because the document's
// env block carries vault-resolved secret material.
const settingsFileMode = 0o600

// settingsDirName is the configuration directory the settings document lives in,
// relative to the tree it configures.
const settingsDirName = ".claude"

// SettingsScope names which of niwa's three settings documents an install
// targets. The scope decides two things a caller should not have to know: the
// file name -- the instance and workspace roots are non-git directories and
// take settings.json, while a repository checkout takes the .local form Claude
// Code reads without it being committed -- and whether the written path joins
// the instance's managed file record.
//
// The zero value is not a scope. A caller that forgets to set one gets an error
// naming the omission rather than a document in whichever place happens to be
// first.
type SettingsScope uint8

const (
	// SettingsAtWorkspaceRoot is the workspace root's document, above the
	// instances. The workspace root has no managed-file state store, so the
	// document there is overwrite-idempotent rather than tracked.
	SettingsAtWorkspaceRoot SettingsScope = iota + 1

	// SettingsAtInstanceRoot is the instance root's document.
	SettingsAtInstanceRoot

	// SettingsInRepo is a cloned repository's document, written in the .local
	// form so it does not belong to the repository that receives it.
	SettingsInRepo
)

// fileName returns the document's name for this scope, and false for a scope
// outside the closed set.
func (s SettingsScope) fileName() (string, bool) {
	switch s {
	case SettingsAtWorkspaceRoot, SettingsAtInstanceRoot:
		return "settings.json", true
	case SettingsInRepo:
		return "settings.local.json", true
	default:
		return "", false
	}
}

// managed reports whether the written document joins the instance's managed
// file record. Everything inside an instance does; the workspace root sits
// above every instance and has no record to join.
func (s SettingsScope) managed() bool {
	return s != SettingsAtWorkspaceRoot
}

// SettingsInputs is what a settings install needs: which document this is,
// which tree it configures, and the document itself.
//
// Doc arrives as the map internal/workspace built rather than as bytes,
// because the marshalling is part of what this file owns: the three writers it
// replaces each spelled out the same indentation and trailing newline, and the
// point of one producer is that they can no longer disagree.
type SettingsInputs struct {
	// Scope selects the document.
	Scope SettingsScope

	// Dir is the absolute root of the tree being configured -- the workspace
	// root, the instance root, or the repository checkout. The configuration
	// directory and file name are appended here.
	Dir string

	// Doc is the settings document, already built from the effective
	// configuration.
	Doc map[string]any
}

// SettingsPlan declares one settings document: the marshalled JSON written
// whole at the scope's path.
//
// It takes no agent, unlike the context producers, and that is a statement
// about today rather than a shortcut. The document is Claude Code's settings
// file, and niwa writes it on every apply whichever agent the session resolved
// to; asking the declaration table here would stop writing it under Codex,
// which is a behavior change this conversion must not make. Binding the
// document to an agent belongs with the Codex delivery that gives a Codex
// session something to read instead.
//
// The entries are tagged ApprovalPosture. The document carries several
// capabilities at once -- hooks, session environment, plugin skills -- but each
// of those has its own delivery or its own entries elsewhere, while the
// permission posture has no vehicle other than this file.
func SettingsPlan(in SettingsInputs) (*Plan, error) {
	name, ok := in.Scope.fileName()
	if !ok {
		return nil, fmt.Errorf("agentplan: unknown settings scope %d", uint8(in.Scope))
	}

	data, err := json.MarshalIndent(in.Doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("agentplan: marshaling the settings document: %w", err)
	}
	// Every writer this replaces ended the file with a newline, and the
	// managed-file record hashes the bytes, so the newline is part of the
	// document rather than a nicety.
	data = append(data, '\n')

	return &Plan{Entries: []Entry{{
		Capability: ApprovalPosture,
		Op:         OpWriteFile,
		Path:       filepath.Join(in.Dir, settingsDirName, name),
		Content:    data,
		Mode:       settingsFileMode,
		Managed:    in.Scope.managed(),
	}}}, nil
}
