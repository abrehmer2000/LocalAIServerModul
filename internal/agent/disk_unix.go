//go:build !windows

package agent

import "golang.org/x/sys/unix"

func freeSpace(path string) (uint64, error) {
	var s unix.Statfs_t
	err := unix.Statfs(path, &s)
	return uint64(s.Bavail) * uint64(s.Bsize), err
}
