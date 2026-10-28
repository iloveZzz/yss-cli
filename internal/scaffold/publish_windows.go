package scaffold

import "golang.org/x/sys/windows"

func publishNoReplace(from, to string) error {
	oldPath, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	newPath, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFile(oldPath, newPath)
}
