package safefs

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"os"
	"path/filepath"
	"strings"
)

func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func Path(root, ref string) (string, error) {
	if err := ValidateRef(ref); err != nil {
		return "", err
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	ancestor := filepath.VolumeName(root) + string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(root, ancestor), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		ancestor = filepath.Join(ancestor, part)
		st, e := os.Lstat(ancestor)
		if e == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
			return "", domain.Fail("PATH", "项目根路径必须由普通目录组成")
		}
		if e != nil && !os.IsNotExist(e) {
			return "", e
		}
	}
	cur := root
	info, err := os.Lstat(root)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", domain.Fail("PATH", "项目根不可为符号链接")
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	for _, p := range strings.Split(ref, "/") {
		cur = filepath.Join(cur, p)
		info, err := os.Lstat(cur)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", domain.Fail("PATH", "拒绝符号链接: "+ref)
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return cur, nil
}
func Describe(root, ref string) (domain.Descriptor, error) {
	p, err := Path(root, ref)
	if err != nil {
		return domain.Descriptor{}, err
	}
	st, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return domain.Descriptor{Type: "missing"}, nil
	}
	if err != nil {
		return domain.Descriptor{}, err
	}
	if !st.Mode().IsRegular() {
		return domain.Descriptor{}, domain.Fail("PATH", "不支持的文件类型: "+ref)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return domain.Descriptor{}, err
	}
	return domain.Descriptor{Type: "file", Digest: Digest(b), Mode: domain.FileMode(uint32(st.Mode().Perm()))}, nil
}
