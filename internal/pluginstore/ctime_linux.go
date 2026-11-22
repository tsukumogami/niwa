package pluginstore

import "syscall"

// ctimeNS is the inode change time of st in nanoseconds.
func ctimeNS(st *syscall.Stat_t) int64 { return st.Ctim.Nano() }
