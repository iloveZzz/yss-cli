//go:build windows

package updater

import (
	"os"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"golang.org/x/sys/windows"
)

func openArchiveFile(path string) (*os.File, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, domain.Wrap("ARTIFACT", err)
	}
	h, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil, domain.Wrap("ARTIFACT", err)
	}
	typ, err := windows.GetFileType(h)
	var info windows.ByHandleFileInformation
	if err == nil {
		err = windows.GetFileInformationByHandle(h, &info)
	}
	if err != nil || typ != windows.FILE_TYPE_DISK || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		_ = windows.CloseHandle(h)
		return nil, fail("ARTIFACT", "发行包句柄必须是普通磁盘文件，不接受重解析点")
	}
	return os.NewFile(uintptr(h), path), nil
}
