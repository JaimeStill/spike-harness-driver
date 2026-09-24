//go:build !unix

package catalog

import "os/exec"

// ownGroup does nothing where process groups don't exist. Cancellation kills the command
// alone, and WaitDelay stops the wait for any process it left holding its output.
func ownGroup(*exec.Cmd) {}
