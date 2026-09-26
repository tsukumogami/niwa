package workspace

import (
	"errors"
	"fmt"

	"github.com/tsukumogami/niwa/internal/keyreport"
)

// ErrStrictSecrets marks every failure caused by strict mode rather than by a
// defect. It exists so a caller can tell "this run refused a shortfall the
// operator asked it to refuse" from "this run broke", which matters on exactly
// one surface: the SessionStart hook, whose only channel to the agent is the
// structured output a non-zero exit suppresses. See runInstanceHookStart.
var ErrStrictSecrets = errors.New("strict secrets")

// strictShortfallError is the gate that sits beside the post-merge
// required-key check. checkRequiredKeys has already decided the one case that
// is fatal whatever the mode -- a required key a reachable provider does not
// hold -- and this turns every remaining collected shortfall fatal too.
//
// It reads the run's collector rather than re-walking the config because the
// collector is the assembled picture: it holds both the marks carried on
// values and the keys declared with no value anywhere, and the second shape
// has nothing on the config to read. A nil collector means the caller does not
// report, and a run that cannot enumerate what it would fail on must not fail.
//
// The error names the count and not the keys. The command surface renders the
// report on the way out, including on this failure path, so listing them here
// would print the same enumeration twice under two different framings.
func strictShortfallError(c *keyreport.Collector) error {
	report := c.Report()
	if len(report) == 0 {
		return nil
	}
	noun := "keys have"
	if len(report) == 1 {
		noun = "key has"
	}
	return fmt.Errorf("%w: strict mode is enabled and %d declared env %s no value; "+
		"supply them, or turn strict mode off with --strict-secrets=false",
		ErrStrictSecrets, len(report), noun)
}
