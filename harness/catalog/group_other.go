//go:build !unix

package catalog

import "os/exec"

func ownGroup(*exec.Cmd) {}

func killGroup(*exec.Cmd) {}
