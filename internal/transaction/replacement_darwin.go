//go:build darwin

package transaction

import (
	"bytes"
	"errors"
	"io"
	"os"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"golang.org/x/sys/unix"
)

func replacementFile(root, base, ref string, b []byte) (*os.File, error) {
	source, err := safefs.Path(root, base+"/objects/"+safefs.Digest(b))
	if err != nil {
		return nil, err
	}
	src, err := os.OpenFile(source, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, fail("STATE", "事务归档对象类型或权限无效")
	}
	data, err := io.ReadAll(src)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data, b) {
		return nil, fail("STATE", "事务归档对象已变化")
	}
	temp, err := safefs.Path(root, ref)
	if err != nil {
		return nil, err
	}
	// Atomic CoW publication prevents SIGKILL from exposing a partial temporary;
	// copying bytes on failure would reintroduce the unrecoverable write window.
	if err = unix.Fclonefileat(int(src.Fd()), unix.AT_FDCWD, temp, 0); err != nil {
		if errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ENOTSUP) {
			return nil, fail("UNPORTED", "macOS 事务需要归档与目标位于同一支持原子克隆的文件系统: "+ref)
		}
		return nil, err
	}
	cloned, err := os.Lstat(temp)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(temp, os.O_RDWR|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err = f.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm() != 0600 || !os.SameFile(cloned, info)) {
		err = fail("CONCURRENT", "事务临时文件身份或权限已变化")
	}
	if err == nil {
		data, err = io.ReadAll(f)
		if err == nil && !bytes.Equal(data, b) {
			err = fail("CONCURRENT", "事务临时文件内容已变化")
		}
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
