//go:build !linux

package stdio_test

import "os/exec"

func detach(*exec.Cmd) {}
