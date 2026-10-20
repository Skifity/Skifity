//go:build unix

package cli

import (
	"os"
	"syscall"
)

// fileOwner is the user and group that own a file.
func fileOwner(info os.FileInfo) (uid, gid int, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(stat.Uid), int(stat.Gid), true
}
