package main

import (
	"os/exec"
	"syscall"
)

func configureChild(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200} }
func supportsTerminate() bool      { return false } // Windows uses its native console cancellation event.
func interruptChild(cmd *exec.Cmd, kind string) error {
	if kind == "kill" {
		return cmd.Process.Kill()
	}
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GenerateConsoleCtrlEvent")
	result, _, err := proc.Call(1, uintptr(cmd.Process.Pid))
	if result == 0 {
		return err
	}
	return nil
}
