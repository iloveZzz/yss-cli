//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func configureChild(cmd *exec.Cmd) {}
func supportsTerminate() bool      { return true }
func pauseChild(cmd *exec.Cmd) error {
	if err := cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		return err
	}
	var status syscall.WaitStatus
	for {
		_, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WUNTRACED, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		// WUNTRACED requests stop events, without WCONTINUED. BSD's Go
		// WaitStatus.Stopped excludes the SIGSTOP encoding as "continued";
		// inspect the POSIX stop tag directly for this stop-only wait.
		if status&0xff != 0x7f {
			return fmt.Errorf("native child exited before suspension: %v", status)
		}
		return nil
	}
}
func resumeChild(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGCONT) }
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
