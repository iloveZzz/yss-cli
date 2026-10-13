//go:build !darwin

package transaction

import (
	"os"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func replacementFile(root, base, ref string, b []byte) (*os.File, error) {
	temp, err := safefs.Path(root, ref)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if _, err = writeTemporary(f, b); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
