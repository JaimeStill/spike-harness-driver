//go:build !linux

package stdio_test

import "os/exec"

// detach does nothing where the orphan tests don't run.
func detach(*exec.Cmd) {}
