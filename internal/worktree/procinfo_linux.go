package worktree

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
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

// userHZ is the unit /proc reports process times in: USER_HZ, which Linux
// keeps at 100 for userspace on the amd64 and arm64 kernels niwa runs on,
// whatever the kernel's own tick rate.
const userHZ = 100

// pidStartedAt returns the wall-clock time the process started: the boot time
// /proc/stat records, plus the start time /proc/<pid>/stat records since boot.
// The boot time is whole seconds, so the result can be up to a second earlier
// than the real start, never later.
func pidStartedAt(pid int) (time.Time, error) {
	ticks, err := pidStartTime(pid)
	if err != nil {
		return time.Time{}, err
	}
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest, ok := strings.CutPrefix(line, "btime ")
		if !ok {
			continue
		}
		boot, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil {
			return time.Time{}, err
		}
		since := time.Duration(ticks) * time.Second / userHZ
		return time.Unix(boot, 0).Add(since), nil
	}
	return time.Time{}, fmt.Errorf("no btime line in /proc/stat")
}
