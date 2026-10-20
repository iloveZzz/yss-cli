//go:build !windows

package safefs

import (
	"errors"
	"os"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"golang.org/x/sys/unix"
)

func SyncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func publicationError(err error) error {
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EPERM) {
		return domain.Wrap("UNPORTED", err)
	}
	return err
}
