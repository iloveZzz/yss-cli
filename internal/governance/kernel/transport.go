package kernel

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"os"
	"path/filepath"
	"strings"
)

func inside(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func ExternalPath(root, p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", domain.Fail("PATH", "输出或运行存储必须使用仓外绝对路径")
	}
	p = filepath.Clean(p)
	if inside(root, p) {
		return "", domain.Fail("PATH", "输出或运行存储不得进入项目根目录")
	}
	protected, err := protectedRoots(root)
	if err != nil {
		return "", err
	}
	for _, registered := range protected {
		if inside(registered, p) {
			return "", domain.Fail("PATH", "输出或运行存储不得进入已登记仓库: "+registered)
		}
	}
	current := p
	for {
		st, err := os.Lstat(current)
		if err == nil {
			if st.Mode()&os.ModeSymlink != 0 {
				return "", domain.Fail("PATH", "拒绝符号链接路径: "+current)
			}
			if current != p && !st.IsDir() {
				return "", domain.Fail("PATH", "路径父级不是目录")
			}
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return p, nil
}
