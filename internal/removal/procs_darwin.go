package removal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// errProcessGone means a process exited between the lsof run and the start
// time read, so it is left out of the listing.
var errProcessGone = errors.New("process is gone")

// processStartTime reads a process's start time from the kernel. The slice
// form of the sysctl is used because it reports a process that no longer
// exists as an empty result, where the single form reports that and a
// structure size mismatch alike as EIO.
var processStartTime = func(pid int) (time.Time, error) {
	kps, err := unix.SysctlKinfoProcSlice("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			return time.Time{}, errProcessGone
		}
		return time.Time{}, err
	}
	if len(kps) == 0 {
		return time.Time{}, errProcessGone
	}
	if len(kps) != 1 || int(kps[0].Proc.P_pid) != pid {
		return time.Time{}, fmt.Errorf("process %d: the kernel answered for another process", pid)
	}
	sec, nsec := kps[0].Proc.P_starttime.Unix()
	if sec == 0 && nsec == 0 {
		return time.Time{}, fmt.Errorf("process %d has no start time", pid)
	}
	return time.Unix(sec, nsec), nil
}

// readProcesses runs lsof once over every process it can see, then reads
// each one's start time. lsof starts by its absolute path with nothing in its
// environment but the filtered PATH and LANG, from /, in its own process
// group so the context's end kills it and anything it started.
func (e *Env) readProcesses(ctx context.Context) ([]Process, error) {
	if e == nil || e.Lsof == "" {
		return nil, errors.New("no lsof on the filtered PATH")
	}
	cmd := exec.CommandContext(ctx, e.Lsof, "-n", "-P", "-w", "-F", "0pRn")
	cmd.Env = []string{"PATH=" + e.PATH, "LANG=C"}
	cmd.Dir = "/"
	groupKill(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if ctx.Err() != nil || !errors.As(err, &ee) || ee.ExitCode() != 1 {
			msg := lastLine(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return nil, fmt.Errorf("lsof: %s", msg)
		}
		code = 1
	}
	procs, err := acceptLsof(code, stdout.Bytes())
	if err != nil {
		return nil, err
	}
	kept := procs[:0]
	for _, p := range procs {
		start, err := processStartTime(p.PID)
		if errors.Is(err, errProcessGone) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading process %d start time: %v", p.PID, err)
		}
		p.Start = start
		kept = append(kept, p)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return kept, nil
}
