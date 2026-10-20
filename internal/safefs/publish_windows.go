//go:build windows

package safefs

import (
	"errors"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"golang.org/x/sys/windows"
)

// Windows flushes the object file; portable directory flush is unavailable.
// This does not claim power-loss durability for the directory entry.
func SyncDirectory(string) error { return nil }

func publicationError(err error) error {
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) || errors.Is(err, windows.ERROR_NOT_SAME_DEVICE) {
		return domain.Wrap("UNPORTED", err)
	}
	return err
}
