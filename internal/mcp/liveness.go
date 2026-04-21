package mcp

import (
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// IsPIDAlive returns true if the given PID exists and its recorded start time
// matches, preventing false positives from PID recycling.
func IsPIDAlive(pid int, startTime int64) bool {
	if pid == 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// os.FindProcess on Unix always succeeds; use kill(0) via /proc.
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	if startTime == 0 {
		return true
	}
	recorded, err := pidStartTime(pid)
	if err != nil {
		// Can't verify start time — be conservative and say alive.
		return true
	}
	return recorded == startTime
}

// pidStartTime reads the process start time (jiffies since boot) from
// /proc/<pid>/stat on Linux. Returns an error on non-Linux platforms.
func pidStartTime(pid int) (int64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	// /proc/<pid>/stat: "pid (comm) state ppid pgroup session ... starttime ..."
	// starttime is field 22 (1-indexed). Find the closing ')' of the comm field
	// first because it may contain spaces.
	s := string(data)
	idx := strings.LastIndex(s, ")")
	if idx < 0 {
		return 0, fmt.Errorf("unexpected /proc/stat format")
	}
	fields := strings.Fields(s[idx+1:])
	// fields[0] is state, fields[19] is starttime (field 22 minus 2 already consumed).
	if len(fields) < 20 {
		return 0, fmt.Errorf("too few fields in /proc/stat")
	}
	v, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// NewSessionID generates a random session UUID for use during registration.
func NewSessionID() string { return newUUID() }

// PIDStartTime returns the start time for a PID (exported for session_register).
func PIDStartTime(pid int) (int64, error) { return pidStartTime(pid) }

// newUUID generates a random UUID v4 without external dependencies.
func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
