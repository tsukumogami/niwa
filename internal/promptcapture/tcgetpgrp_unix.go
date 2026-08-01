//go:build linux || darwin

package promptcapture

import "golang.org/x/sys/unix"

func unixTcgetpgrp(fd int) (int, error) {
	return unix.IoctlGetInt(fd, unix.TIOCGPGRP)
}
