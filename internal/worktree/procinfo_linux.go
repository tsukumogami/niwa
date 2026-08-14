package worktree

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// pidStartTime reads the process start time (jiffies since boot) from
// /proc/<pid>/stat.
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

// readPPID returns the PPID for a given PID from /proc/<pid>/stat, or 0 on
// any read or parse error.
func readPPID(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	s := string(data)
	// /proc/<pid>/stat: "pid (comm) state ppid ..."
	// Find the closing ')' of comm first; comm may contain spaces.
	idx := strings.LastIndex(s, ")")
	if idx < 0 {
		return 0
	}
	fields := strings.Fields(s[idx+1:])
	// fields[0] = state, fields[1] = ppid.
	if len(fields) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}
