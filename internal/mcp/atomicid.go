// atomicid.go provides ReserveID, a generalised TOCTOU-free ID reservation
// helper. Callers pass a directory, an ID generator, and a placeholder-name
// function; ReserveID generates an ID, opens a placeholder file under that
// directory with O_CREATE|O_EXCL, and retries on EEXIST collisions.
//
// O_EXCL is the load-bearing primitive: the OS makes the check-and-create
// atomic, so two concurrent callers sharing the same directory cannot
// reserve the same ID. The session-lifecycle registry pioneered this
// pattern (see newSessionLifecycleID); the helper hoists the same control
// flow so the F5 changestore (and any future per-directory namespace) can
// reuse it without duplicating the retry loop.
package mcp

import (
	"fmt"
	"os"
	"path/filepath"
)

// ReserveID generates an ID via gen, then attempts to atomically reserve
// the resulting placeholder path under dir. Returns the reserved ID on
// success. On EEXIST it regenerates and retries up to 5 times. Other
// filesystem errors propagate immediately.
//
// The placeholder file is created mode 0o600 and closed immediately; the
// caller is expected to overwrite it via rename (the standard atomic
// write-then-rename pattern). The placeholder is never deleted by
// ReserveID — if the caller abandons the reservation without writing,
// the placeholder lingers until external cleanup.
//
// TOCTOU safety: there is no os.Stat → os.OpenFile window. The OS-level
// O_EXCL guarantees that the file did not exist at the instant of the
// open, so a concurrent caller cannot have reserved the same name.
func ReserveID(
	dir string,
	gen func() (string, error),
	placeholderName func(id string) string,
) (string, error) {
	for range 5 {
		id, err := gen()
		if err != nil {
			return "", fmt.Errorf("generating ID: %w", err)
		}
		path := filepath.Join(dir, placeholderName(id))
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return id, nil
		}
		if !os.IsExist(err) {
			return "", fmt.Errorf("reserving ID: %w", err)
		}
		// Collision: retry with a fresh ID.
	}
	return "", fmt.Errorf("failed to generate unique ID after 5 attempts")
}
