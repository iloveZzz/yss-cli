package domain

import "runtime"

// FileMode describes only permissions representable by the running OS. Windows
// os.Chmod uses the owner write bit for the read-only attribute; execute and
// group/other permission bits are not POSIX capabilities on that platform.
func FileMode(mode uint32) uint32 { return fileModeForOS(mode, runtime.GOOS) }

func fileModeForOS(mode uint32, platform string) uint32 {
	if platform == "windows" {
		if mode&0200 != 0 {
			return 0644
		}
		return 0444
	}
	return mode & 0777
}
