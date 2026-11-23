package removal

import (
	"context"
	"fmt"
	"os"
)

// linuxTicksPerSec is CLK_TCK, the unit of a /proc start time. Linux fixes it
// at 100 for user space on every architecture Go supports.
const linuxTicksPerSec = 100

// readProcesses reads /proc. The read runs aside so the listing's context
// still bounds it; a read cut short fails the listing.
func (e *Env) readProcesses(ctx context.Context) ([]Process, error) {
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, fmt.Errorf("reading the system statistics: %v", err)
	}
	boot, err := parseBootTime(stat)
	if err != nil {
		return nil, err
	}
	type result struct {
		procs []Process
		err   error
	}
	done := make(chan result, 1)
	go func() {
		procs, err := readProc("/proc", boot, linuxTicksPerSec)
		done <- result{procs, err}
	}()
	select {
	case r := <-done:
		return r.procs, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
