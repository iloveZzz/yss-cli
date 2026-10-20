//go:build !windows

package mcp

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Inherited stdin is constructed before Go can know that it is a pipe, so it
// may use a blocking syscall that Close cannot interrupt. Re-own the pipe in
// nonblocking mode before any read, letting os.NewFile register it with netpoll.
func prepareOwnedInput(in io.ReadCloser) (io.ReadCloser, error) {
	f, ok := in.(*os.File)
	if !ok {
		return in, nil
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		return in, nil
	}
	fd, err := unix.Dup(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	if err = unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	owned := os.NewFile(uintptr(fd), f.Name())
	if err = f.Close(); err != nil {
		_ = owned.Close()
		return nil, err
	}
	return owned, nil
}
