package removal

import (
	"path"
	"strings"
	"time"
)

// DispatchPendingMarker is the marker niwa dispatch drops in an instance it
// creates, holding the creation time as RFC3339 on its first line. The age
// backstop ages an unmapped dispatch instance by it.
const DispatchPendingMarker = ".niwa/dispatch-pending"

// ReadDispatchPending reads an instance's dispatch-pending marker through
// the shared reader (opened without following a link, owned by the user, a
// regular file no larger than its cap, read relative to the instance's
// .niwa opened the same way) and returns the time on its first line. ok is
// false for a marker that isn't there, can't be read that way or doesn't
// parse: the caller then ages the instance another way.
func ReadDispatchPending(instancePath string) (time.Time, bool) {
	d := openNiwaDir(instancePath)
	defer d.close()
	b, err := d.read(path.Base(DispatchPendingMarker), maxRecordBytes)
	if err != nil {
		return time.Time{}, false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(line))
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}
