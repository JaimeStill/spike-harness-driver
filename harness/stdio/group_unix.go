//go:build unix

package stdio

import (
	"os/exec"
	"syscall"
)

// ownGroup starts the harness as the leader of a process group of its own, so killGroup can
// reach every process the harness starts, such as its tool processes.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup kills whatever is left of the harness's process group once the harness has
// exited: processes it started and didn't stop, which would otherwise be orphaned.
func killGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
