//go:build !linux && !darwin

package removal

import (
	"context"
	"errors"
)

// readProcesses has no reader to call here, so every listing is undecidable.
func (e *Env) readProcesses(context.Context) ([]Process, error) {
	return nil, errors.New("no process listing on this platform")
}
