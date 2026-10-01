//go:build unix

package cache

import (
	"fmt"
	"os"
	"syscall"
)

// Private fails unless dir belongs to the current user and no one else can write to it. A
// driver checks a directory with it before a harness runs or follows what the directory holds,
// such as a cache, or a harness configuration that names commands to run, since anyone else able
// to write there could plant what the harness runs.
func Private(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !ok:
		return nil
	case int(st.Uid) != os.Getuid():
		return fmt.Errorf("cache: %s belongs to another user", dir)
	case fi.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("cache: %s is writable by others (%v)", dir, fi.Mode().Perm())
	}
	return nil
}
