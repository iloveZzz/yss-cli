//go:build !darwin && !linux && !windows

package scaffold

import "github.com/iloveZzz/yss-cli/internal/domain"

func publishNoReplace(from, to string) error {
	return domain.Fail("CAPABILITY", "此平台尚未支持脚手架无覆盖发布")
}
