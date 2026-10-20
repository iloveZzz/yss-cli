package updater

import "os"

// Validate both sides of open: a FIFO must never block before type validation,
// and a replaced path must not silently select a different archive.
func openArchive(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > maxArchive {
		return nil, fail("ARTIFACT", "发行包必须是最多 256 MiB 的普通文件，不接受符号链接")
	}
	f, err := openArchiveFile(path)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || after.Size() > maxArchive || !os.SameFile(before, after) {
		_ = f.Close()
		return nil, fail("ARTIFACT", "发行包打开期间类型、身份或大小发生变化")
	}
	return f, nil
}
