package worktree

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// kinfoProc looks a process up through the kern.proc.pid sysctl, the Darwin
// equivalent of reading /proc/<pid>/stat. A dead PID surfaces as an error;
// so does a kernel record whose PID does not echo the request, which guards
// against reading a zero-valued struct as if it described the process.
func kinfoProc(pid int) (*unix.KinfoProc, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return nil, fmt.Errorf("sysctl kern.proc.pid %d: %w", pid, err)
	}
	if kp == nil || kp.Proc.P_pid != int32(pid) {
		return nil, fmt.Errorf("sysctl kern.proc.pid %d: no such process", pid)
	}
	return kp, nil
}

// pidStartTime returns the process start time in microseconds since the Unix
// epoch, read from the kern.proc.pid sysctl.
//
// The unit differs from Linux (jiffies since boot), which is fine: callers
// treat the value as an opaque fingerprint that must be stable for the life
// of a process and different for a recycled PID. Darwin fixes p_starttime at
// fork, so it satisfies both.
func pidStartTime(pid int) (int64, error) {
	kp, err := kinfoProc(pid)
	if err != nil {
		return 0, err
	}
	st := kp.Proc.P_starttime
	return int64(st.Sec)*1_000_000 + int64(st.Usec), nil
}

// readPPID returns the PPID for a given PID, or 0 on any lookup error.
func readPPID(pid int) int {
	kp, err := kinfoProc(pid)
	if err != nil {
		return 0
	}
	return int(kp.Eproc.Ppid)
}
