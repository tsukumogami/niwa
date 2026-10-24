package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/tsukumogami/niwa/internal/authority"
)

// CapturePendingFile is the capture-pending file's name inside an instance's
// .niwa directory. Dispatch writes it just before launching the worker and
// removes it once capture has an outcome, so while it exists the instance's
// worker may be running with no session mapping yet.
const CapturePendingFile = "capture-pending.json"

// maxCapturePendingBytes caps what a reader reads; a file this niwa writes is
// a few hundred bytes.
const maxCapturePendingBytes = 4096

// capturePendingAgentPattern is an agent name's shape, the lineage entry's
// name-value pattern.
var capturePendingAgentPattern = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// CapturePending is what the capture-pending file records: the dispatch whose
// capture is pending, the agent it launched, the dispatch process's id and
// the time it launched the worker.
type CapturePending struct {
	DispatchID string    `json:"dispatch_id"`
	Agent      string    `json:"agent"`
	PID        int       `json:"pid"`
	LaunchedAt time.Time `json:"launched_at"`
}

// CapturePendingPath returns where an instance's capture-pending file lives.
func CapturePendingPath(instancePath string) string {
	return filepath.Join(instancePath, StateDir, CapturePendingFile)
}

// valid reports whether every field has its shape: a dispatch id, an agent
// name, a positive process id and a launch time.
func (p CapturePending) valid() bool {
	return authority.ValidDispatchID(p.DispatchID) &&
		capturePendingAgentPattern.MatchString(p.Agent) &&
		p.PID > 0 &&
		!p.LaunchedAt.IsZero()
}

// WriteCapturePending writes p as the instance's capture-pending file, mode
// 0600, through a temporary file renamed into place, as WriteAuthorityRecord
// writes the record.
func WriteCapturePending(instancePath string, p CapturePending) error {
	if !p.valid() {
		return fmt.Errorf("refusing to write an incomplete capture-pending file")
	}
	p.LaunchedAt = p.LaunchedAt.UTC()
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("encoding the capture-pending file: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Join(instancePath, StateDir)
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("naming the capture-pending file's temporary file: %w", err)
	}
	tmpPath := filepath.Join(dir, "."+CapturePendingFile+"."+hex.EncodeToString(suffix[:]))
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("creating the capture-pending file: %w", err)
	}
	defer func() {
		f.Close()
		os.Remove(tmpPath)
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing the capture-pending file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing the capture-pending file: %w", err)
	}
	if err := os.Rename(tmpPath, CapturePendingPath(instancePath)); err != nil {
		return fmt.Errorf("placing the capture-pending file: %w", err)
	}
	return nil
}

// RemoveCapturePending removes the instance's capture-pending file. A file
// that is already gone is not an error.
func RemoveCapturePending(instancePath string) error {
	if err := os.Remove(CapturePendingPath(instancePath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ReadCapturePending reads the instance's capture-pending file. found is
// false, with no error, when there is none. The file is same-user writable,
// so it is opened as the record is (O_NOFOLLOW, O_NONBLOCK, a regular file,
// a size cap). A member it doesn't know is ignored, so a later niwa may add
// one; a missing member or any field out of shape is an error, which a
// reader treats as no capture pending.
func ReadCapturePending(instancePath string) (p CapturePending, found bool, err error) {
	f, err := os.OpenFile(CapturePendingPath(instancePath), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CapturePending{}, false, nil
		}
		return CapturePending{}, true, fmt.Errorf("opening the capture-pending file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return CapturePending{}, true, fmt.Errorf("reading the capture-pending file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return CapturePending{}, true, fmt.Errorf("the capture-pending file is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxCapturePendingBytes+1))
	if err != nil {
		return CapturePending{}, true, fmt.Errorf("reading the capture-pending file: %w", err)
	}
	if len(data) > maxCapturePendingBytes {
		return CapturePending{}, true, fmt.Errorf("the capture-pending file is larger than %d bytes", maxCapturePendingBytes)
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return CapturePending{}, true, fmt.Errorf("the capture-pending file is malformed")
	}
	if !p.valid() {
		return CapturePending{}, true, fmt.Errorf("the capture-pending file is malformed")
	}
	return p, true, nil
}
