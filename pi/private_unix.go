//go:build unix

package pi

import (
	"fmt"
	"os"
	"syscall"
)

// private fails unless dir belongs to the current user and no one else can write to it.
func private(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	switch {
	case !ok:
		return nil
	case int(st.Uid) != os.Getuid():
		return fmt.Errorf("pi: cache %s belongs to another user", dir)
	case fi.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("pi: cache %s is writable by others (%v)", dir, fi.Mode().Perm())
	}
	return nil
}
