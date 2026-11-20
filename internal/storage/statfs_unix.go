//go:build linux || darwin

package storage

import "syscall"

// Statfs returns the size and the space available to unprivileged users of the filesystem holding dir.
func Statfs(dir string) (total, free uint64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, false
	}
	return st.Blocks * uint64(st.Bsize), st.Bavail * uint64(st.Bsize), true
}
