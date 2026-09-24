//go:build unix

package catalog

import (
	"os/exec"
	"syscall"
)

// ownGroup starts the command as the leader of a process group of its own, and has
// cancellation kill the whole group. A tool is often a script, and killing only the script
// would leave the processes it started running, holding its stdout open.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// killGroup kills whatever is left of the command's process group once the command has
// exited. The group is gone already when the command left nothing behind.
func killGroup(cmd *exec.Cmd) {
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
