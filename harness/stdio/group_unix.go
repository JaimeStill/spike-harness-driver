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
//
// The harness has been reaped by then, so if its group is already empty, its PID is free. A
// new process that took that PID and made itself a group leader in the moment between the
// reaping and this call would be killed instead. That needs the PIDs to wrap around within
// microseconds, and the risk is accepted.
func killGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
