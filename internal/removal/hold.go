package removal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// DispatchRetainMarker is the marker niwa drops in an instance whose worker
// finished and whose instance a person asked to keep. Its presence holds the
// instance whatever the marker holds; its content, when readable, is the
// recorded reason.
const DispatchRetainMarker = ".niwa/dispatch-retain"

// maxReasonBytes bounds how much of a hold's recorded reason is read.
const maxReasonBytes = 1024

// checkHold keeps a unit carrying the dispatch-retain marker. The marker is
// present when anything is at its path, readable or not, so a marker the
// rule can't read still holds. A .niwa that isn't a real directory keeps
// too: the rule won't look for a hold through a link.
func checkHold(_ context.Context, u Unit, _ Policy, _ *evalState) []Check {
	niwaDir := filepath.Join(u.InstancePath, filepath.Dir(DispatchRetainMarker))
	info, err := os.Lstat(niwaDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return []Check{{Paths: []string{".niwa"}, Evidence: "hold could not be read: " + errText(err)}}
	case !info.IsDir():
		return []Check{{Paths: []string{".niwa"}, Evidence: "hold could not be read: .niwa is not a directory"}}
	}
	marker := filepath.Join(u.InstancePath, DispatchRetainMarker)
	if _, err := os.Lstat(marker); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return []Check{{Paths: []string{DispatchRetainMarker}, Evidence: "hold could not be read: " + errText(err)}}
	}
	reason, err := readSmallFile(marker, maxReasonBytes)
	if err != nil {
		return []Check{{Paths: []string{DispatchRetainMarker}, Evidence: "held by " + DispatchRetainMarker + " (its reason could not be read)"}}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return []Check{{Paths: []string{DispatchRetainMarker}, Evidence: "held by " + DispatchRetainMarker}}
	}
	return []Check{{Paths: []string{DispatchRetainMarker}, Evidence: "held by " + DispatchRetainMarker + ": " + reason}}
}

// readSmallFile reads at most max bytes of a regular file without following
// a link and without blocking on a FIFO.
func readSmallFile(p string, max int64) (string, error) {
	b, err := readRegular(p, max)
	return string(b), err
}

// readRegular opens p with O_NOFOLLOW|O_NONBLOCK, checks it is a regular
// file and reads at most max bytes of it. A larger file is an error.
func readRegular(p string, max int64) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(p))
	}
	if info.Size() > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", filepath.Base(p), max)
	}
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", filepath.Base(p), max)
	}
	return b, nil
}

// errText is an error's message without the path a *PathError carries, so
// evidence names a path once, relative to the instance.
func errText(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}
