package project

import (
	"os"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// workDescribe 在 safefs.Describe 之外把目录描述为 directory 与权限位。
func workDescribe(root, ref string) (domain.Descriptor, error) {
	p, e := safefs.Path(root, ref)
	if e != nil {
		return domain.Descriptor{}, e
	}
	st, e := os.Lstat(p)
	if e == nil && st.IsDir() {
		return domain.Descriptor{Type: "directory", Mode: uint32(st.Mode().Perm())}, nil
	}
	return safefs.Describe(root, ref)
}
