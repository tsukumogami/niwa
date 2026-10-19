package claudetrust

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// maxConfigRead caps how much of the Claude config file IsTrusted reads. The
// file holds per-project history and can grow large, but a file past this
// size is not one this read-only check should load into memory.
const maxConfigRead = 64 << 20

// IsTrusted reports whether Claude Code treats dir as trusted: whether dir or
// any directory above it has hasTrustDialogAccepted set in the config file
// Claude Code reads for env, which is how a directory inherits trust from a
// trusted ancestor. known is false when the answer can't be determined -- no
// home to find the config under, a file that can't be read, isn't valid
// JSON, or is too large -- and callers then say nothing rather than guess.
//
// It only reads: the config file is opened read-only and never locked or
// written, and only the projects entries' trust flags are decoded.
func IsTrusted(env Env, dir string) (trusted, known bool) {
	file, err := ConfigFile(env)
	if err != nil {
		return false, false
	}
	// Claude Code reads the legacy file instead whenever it exists.
	if legacy, err := legacyConfigFile(env); err == nil {
		if _, err := os.Stat(legacy); err == nil {
			file = legacy
		}
	}
	key, err := Key(dir)
	if err != nil {
		return false, false
	}

	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return false, true
	}
	if err != nil {
		return false, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigRead+1))
	if err != nil || len(data) > maxConfigRead {
		return false, false
	}
	top, err := decodeObject(data)
	if err != nil {
		return false, false
	}
	doc := &configDoc{top: top}

	for k := key; ; k = filepath.Dir(k) {
		_, entry, err := doc.entry(k)
		if err != nil {
			return false, false
		}
		if entry != nil {
			if flag, err := flagValue(entry); err == nil && flag {
				return true, true
			}
		}
		if parent := filepath.Dir(k); parent == k {
			return false, true
		}
	}
}
