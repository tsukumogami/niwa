package removal

import (
	"context"
	"errors"
	"io/fs"
	"path"
	"strings"
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
// rule can't read still holds: a link, a FIFO, another user's file or one
// over its cap is present but unreadable. A .niwa that isn't a real
// directory of the user's keeps too: the rule won't look for a hold through
// a link. Every read is made relative to the .niwa the evaluation opened.
func checkHold(_ context.Context, _ Unit, _ Policy, s *evalState) []Check {
	if s.niwa.absent {
		return nil
	}
	if err := s.niwa.usable(); err != nil {
		return []Check{{Paths: []string{niwaDirName}, Evidence: "hold could not be read: " + errText(err)}}
	}
	name := path.Base(DispatchRetainMarker)
	present, err := s.niwa.present(name)
	switch {
	case !present:
		return nil
	case err != nil:
		return []Check{{Paths: []string{DispatchRetainMarker}, Evidence: "hold could not be read: " + errText(err)}}
	}
	b, err := s.niwa.read(name, maxReasonBytes)
	if err != nil {
		return []Check{{Paths: []string{DispatchRetainMarker}, Evidence: "held by " + DispatchRetainMarker + " (its reason could not be read)"}}
	}
	reason := strings.TrimSpace(string(b))
	if reason == "" {
		return []Check{{Paths: []string{DispatchRetainMarker}, Evidence: "held by " + DispatchRetainMarker}}
	}
	return []Check{{Paths: []string{DispatchRetainMarker}, Evidence: "held by " + DispatchRetainMarker + ": " + reason}}
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
