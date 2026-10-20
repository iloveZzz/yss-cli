//go:build !windows

package updater

import (
	"os"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"golang.org/x/sys/unix"
)

func openArchiveFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, domain.Wrap("ARTIFACT", err)
	}
	return os.NewFile(uintptr(fd), path), nil
}
