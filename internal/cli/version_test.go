package cli

import (
	"bytes"
	"testing"
)

// versionOutput runs `niwa version` and returns what it printed.
func versionOutput(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	t.Cleanup(func() { versionCmd.SetOut(nil) })
	versionCmd.Run(versionCmd, nil)
	return buf.String()
}
