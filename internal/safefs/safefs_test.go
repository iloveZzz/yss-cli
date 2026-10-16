package safefs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateRef(t *testing.T) {
	t.Parallel()
	for _, ref := range []string{"a", "a/b.md", ".template-spec/process/x.yaml", "中文/文件.md", "COMX/a", "console.txt"} {
		if err := ValidateRef(ref); err != nil {
			t.Errorf("ValidateRef(%q) 应通过: %v", ref, err)
		}
	}
	for _, ref := range []string{"", "/abs", "a//b", "./a", "a/../b", "..", ".git/config", "a/.GIT/x", `a\b`, "a:b", "a?", "a\x01", "a.", "a ", "CON", "nul.txt", "dir/COM1", "LPT9.log", "com¹"} {
		if err := ValidateRef(ref); err == nil {
			t.Errorf("ValidateRef(%q) 应拒绝", ref)
		}
	}
}

func TestPathSetRejectsCaseAliases(t *testing.T) {
	t.Parallel()
	var s PathSet
	for _, ref := range []string{"Docs/a.md", "Docs/a.md", "Docs/b.md", "other"} {
		if err := s.Add(ref); err != nil {
			t.Fatalf("Add(%q): %v", ref, err)
		}
	}
	for _, ref := range []string{"docs/c.md", "Docs/A.md", "OTHER/x"} {
		if err := s.Add(ref); err == nil {
			t.Errorf("Add(%q) 应因大小写碰撞被拒绝", ref)
		}
	}
	if err := s.Add("../x"); err == nil {
		t.Error("越界路径应被拒绝")
	}
}

func TestPathRejectsSymlinks(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Skip("当前平台不能创建符号链接")
	}
	got, err := Path(root, "real/missing.md")
	if err != nil || got != filepath.Join(root, "real", "missing.md") {
		t.Fatalf("Path 普通路径 = %q, %v", got, err)
	}
	if _, err := Path(root, "link/a.md"); err == nil {
		t.Error("经过符号链接的路径应被拒绝")
	}
	if _, err := Path(filepath.Join(root, "link"), "a.md"); err == nil {
		t.Error("项目根为符号链接时应被拒绝")
	}
}

func TestDescribe(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if d, err := Describe(root, "none.md"); err != nil || d.Type != "missing" {
		t.Fatalf("缺失文件 = %+v, %v", d, err)
	}
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Describe(root, "a.md")
	if err != nil || d.Type != "file" || d.Digest != Digest([]byte("abc")) {
		t.Fatalf("文件描述 = %+v, %v", d, err)
	}
	if _, err := Describe(root, "."); err == nil {
		t.Error("目录或非法引用应被拒绝")
	}
}
