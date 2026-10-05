//go:build !windows

package transaction

import (
	"os"
	"syscall"
)

func lockFile(f *os.File) (func(), error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fail("LOCKED", "项目事务由其他进程持有: "+err.Error())
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }, nil
}
func syncDirectoryFile(f *os.File) error { return f.Sync() }
