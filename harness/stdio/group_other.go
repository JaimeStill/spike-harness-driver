//go:build !unix

package stdio

import "os/exec"

func ownGroup(*exec.Cmd) {}

func killGroup(*exec.Cmd) {}
