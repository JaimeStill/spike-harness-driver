//go:build !unix

package catalog

import "os/exec"

// ownGroup does nothing where process groups don't exist. Cancellation kills the command
// alone, and the wait delay stops the reading of output a leftover process still holds.
func ownGroup(*exec.Cmd) {}

// killGroup does nothing where process groups don't exist.
func killGroup(*exec.Cmd) {}
