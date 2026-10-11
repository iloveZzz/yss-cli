package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

func configureChild(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200} }
func supportsTerminate() bool      { return false } // Windows uses its native console cancellation event.
func changeSuspension(cmd *exec.Cmd, operation string) error {
	const processSuspendResume = 0x0800
	handle, err := syscall.OpenProcess(processSuspendResume, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)
	proc := syscall.NewLazyDLL("ntdll.dll").NewProc(operation)
	if err := proc.Find(); err != nil {
		return err
	}
	status, _, _ := proc.Call(uintptr(handle))
	if status != 0 {
		return fmt.Errorf("%s failed: NTSTATUS %#x", operation, status)
	}
	return nil
}
func pauseChild(cmd *exec.Cmd) error  { return changeSuspension(cmd, "NtSuspendProcess") }
func resumeChild(cmd *exec.Cmd) error { return changeSuspension(cmd, "NtResumeProcess") }
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
