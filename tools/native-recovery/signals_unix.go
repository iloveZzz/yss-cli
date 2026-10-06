//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func configureChild(cmd *exec.Cmd) {}
func supportsTerminate() bool      { return true }
func interruptChild(cmd *exec.Cmd, kind string) error {
	switch kind {
	case "cancel":
		return cmd.Process.Signal(os.Interrupt)
	case "terminate":
		return cmd.Process.Signal(syscall.SIGTERM)
	default:
		return cmd.Process.Kill()
	}
}
