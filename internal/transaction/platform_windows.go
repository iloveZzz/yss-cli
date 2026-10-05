//go:build windows

package transaction

import (
	"os"
	"syscall"
	"unsafe"
)

func lockFile(f *os.File) (func(), error) {
	api := syscall.NewLazyDLL("kernel32.dll")
	lock, unlock := api.NewProc("LockFileEx"), api.NewProc("UnlockFileEx")
	var overlapped syscall.Overlapped
	ok, _, err := lock.Call(f.Fd(), 3, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if ok == 0 {
		return nil, fail("LOCKED", "不能取得 Windows 项目事务锁: "+err.Error())
	}
	return func() { unlock.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped))) }, nil
}

// Windows does not expose portable directory fsync through os.File.
// Individual journal, WAL, archive and replacement files are still flushed.
func syncDirectoryFile(f *os.File) error { return nil }
