package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync"
)

var executableOnce sync.Once
var executableDigest string
var executableError error

// ExecutableDigest identifies the process's fixed executable, including builds
// with equal versions and commits but different embedded Bundles or repairs.
func ExecutableDigest() (string, error) {
	executableOnce.Do(func() {
		file, err := os.Executable()
		if err != nil {
			executableError = err
			return
		}
		f, err := os.Open(file)
		if err != nil {
			executableError = err
			return
		}
		defer f.Close()
		h := sha256.New()
		if _, err = io.Copy(h, f); err != nil {
			executableError = err
			return
		}
		executableDigest = hex.EncodeToString(h.Sum(nil))
	})
	return executableDigest, executableError
}
