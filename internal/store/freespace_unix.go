//go:build unix

package store

import (
	"fmt"
	"syscall"
)

func freeBytes(dir string) (int64, bool) {
	d, ok := DiskSpaceOf(dir)
	return d.Free, ok
}

// DiskSpaceOf reads the filesystem holding dir. Inside a container that is
// the host's disk behind the volume or the mapped folder.
func DiskSpaceOf(dir string) (DiskSpace, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return DiskSpace{}, false
	}
	return DiskSpace{
		Device: fmt.Sprintf("%x", st.Fsid),
		Free:   int64(st.Bavail) * int64(st.Bsize),
		Total:  int64(st.Blocks) * int64(st.Bsize),
	}, true
}
