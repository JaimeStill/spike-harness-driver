//go:build !unix

package stdio

import "os/exec"

// ownGroup does nothing where process groups don't exist.
func ownGroup(*exec.Cmd) {}

// killGroup does nothing where process groups don't exist; a harness's leftover children
// outlive it there.
func killGroup(*exec.Cmd) {}
