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
