//go:build !unix

package cli

import "os"

// fileOwner is the user and group that own a file. Windows has no numeric
// owner to hand on, and the panel's database is never on Windows.
func fileOwner(os.FileInfo) (uid, gid int, ok bool) {
	return 0, 0, false
}
