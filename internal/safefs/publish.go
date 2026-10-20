package safefs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// PublishFile writes a complete, flushed private object and atomically gives it
// its final name without replacing an existing producer's object. Parents must
// already exist. A false result means the caller must verify the existing file.
func PublishFile(root, ref string, source io.Reader) (bool, error) {
	return publishFile(root, ref, source, func(f *os.File) error { return f.Sync() })
}

// The synchronization boundary is injectable per call for deterministic fault
// tests, without global hooks or a production option to disable durability.
func publishFile(root, ref string, source io.Reader, syncFile func(*os.File) error) (bool, error) {
	target, err := Path(root, ref)
	if err != nil {
		return false, err
	}
	if info, err := os.Lstat(target); err == nil {
		if !info.Mode().IsRegular() {
			return false, domain.Fail("PATH", "对象目标不是普通文件")
		}
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	f, err := os.CreateTemp(filepath.Dir(target), ".yss-object-")
	if err != nil {
		return false, err
	}
	temp := f.Name()
	defer os.Remove(temp)
	defer f.Close()
	if _, err = io.Copy(f, source); err != nil {
		return false, err
	}
	if err = syncFile(f); err != nil {
		return false, err
	}
	if err = f.Close(); err != nil {
		return false, err
	}
	if _, err = Path(root, ref); err != nil {
		return false, err
	}
	if err = os.Link(temp, target); errors.Is(err, fs.ErrExist) {
		return false, nil
	} else if err != nil {
		return false, publicationError(err)
	}
	if err = SyncDirectory(filepath.Dir(target)); err != nil {
		return true, err
	}
	return true, nil
}
