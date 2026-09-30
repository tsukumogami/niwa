//go:build !unix

package store

import (
	"io/fs"
	"os"
	"time"
)

// Platforms without flock and file ownership get no store: fileOwner
// reports no owner, so every store directory fails the trust checks,
// reads as empty and refuses writes.

const (
	oNoFollow  = 0
	oDirectory = 0
)

func acquireLock(*os.File, time.Duration, time.Duration) error { return ErrUnwritable }

func releaseLock(*os.File) {}

func fileOwner(fs.FileInfo) (int, bool) { return 0, false }
