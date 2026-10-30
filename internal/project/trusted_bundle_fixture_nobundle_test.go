//go:build yss_nobundle

package project

import (
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
)

func trustedBundle(t *testing.T, profile string) *bundle.Bundle {
	t.Helper()
	t.Skip("需要真实受信 Bundle 的安装集成测试")
	return nil
}
