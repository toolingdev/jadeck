//go:build !windows

package addons

import "os/exec"

func hideWindow(*exec.Cmd) {}
