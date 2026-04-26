// audit_reader.go is the consumer side of the MCP-call audit log. Tests
// (and any future debugging tools) call ReadAuditLog to pull all entries
// written for an instance, then filter in memory with FilterAudit.
//
// Reads tolerate partial trailing lines (a SIGKILL'ed writer is unlikely
// given Linux O_APPEND atomicity, but the reader is defensive anyway) and
// skip lines that fail to parse rather than aborting the whole read; a
// torn line in the middle of a real run shouldn't blind the test to every
// other emitted entry.
package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// ReadAuditLog returns every parseable AuditEntry from
// <instanceRoot>/.niwa/mcp-audit.log in emission order. A missing file
// returns an empty slice with nil error — that means no MCP calls were
// made, which is a valid scenario state. Lines that fail to parse are
// silently skipped.
func ReadAuditLog(instanceRoot string) ([]AuditEntry, error) {
	path := filepath.Join(instanceRoot, ".niwa", auditLogFileName)
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []AuditEntry
	sc := bufio.NewScanner(f)
	// Each line is one entry. Keep a generous buffer so a long arg-keys
	// list doesn't truncate; entries are still well under a kilobyte
	// in practice.
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e AuditEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return out, err
	}
	return out, nil
}

// AuditFilter narrows a slice of entries by exact-match on the named
// fields. Empty fields are wildcards (don't constrain that dimension).
// OK is a *bool because both true and false are meaningful filters and
// the zero value would otherwise mean "match only failures".
type AuditFilter struct {
	Role string
	Tool string
	OK   *bool
}

// FilterAudit returns the subset of entries matching every set field of f.
// Pure function; does not mutate the input slice.
func FilterAudit(entries []AuditEntry, f AuditFilter) []AuditEntry {
	out := make([]AuditEntry, 0, len(entries))
	for _, e := range entries {
		if f.Role != "" && e.Role != f.Role {
			continue
		}
		if f.Tool != "" && e.Tool != f.Tool {
			continue
		}
		if f.OK != nil && e.OK != *f.OK {
			continue
		}
		out = append(out, e)
	}
	return out
}
