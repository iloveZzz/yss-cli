package compat

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeRecoveryKeepsWrappedPathErrorCode(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-project")
	if err = os.Symlink(root, link); err != nil {
		t.Skip(err)
	}
	exit, output, stderr := invoke(t, "create-yss-spec", "--native", "recover", "--target-dir", link, "--json")
	if exit != 1 || !bytes.Contains(output, []byte(`"code":"PATH"`)) {
		t.Fatalf("诊断包装改变了冻结错误码: %d %s %s", exit, output, stderr)
	}
}
