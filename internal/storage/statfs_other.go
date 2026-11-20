//go:build !linux && !darwin

package storage

// Statfs is unknown on this system.
func Statfs(string) (total, free uint64, ok bool) { return 0, 0, false }
