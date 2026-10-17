package governance

import (
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func runtimeFixture(t *testing.T) (root, home string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, home = filepath.Join(base, "proj"), filepath.Join(base, "home")
	if err = os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	return root, home
}

func errCode(err error) string {
	var de *domain.Error
	if errors.As(err, &de) {
		return de.Code
	}
	return ""
}

func TestRuntimeBeginStoresInputAndProtectsReport(t *testing.T) {
	root, home := runtimeFixture(t)
	report := filepath.Join(filepath.Dir(home), "report")
	ctx := context.Background()
	out, err := runtimeRun(ctx, "begin", root, map[string]string{"home": home, "kind": "k", "input": `{"commit":"abc"}`, "report-dir": report})
	if err != nil {
		t.Fatal(err)
	}
	first := out.(map[string]any)
	digest := first["input_digest"].(string)
	if first["report_dir"] != report {
		t.Fatalf("报告目录: %v", first)
	}
	// 同一输入第二次登记复用对象，不重复写文件。
	second, err := runtimeRun(ctx, "begin", root, map[string]string{"home": home, "kind": "k", "input": `{"commit":"abc"}`})
	if err != nil || second.(map[string]any)["input_digest"] != digest {
		t.Fatalf("复用对象: %v %v", second, err)
	}
	dir := filepath.Dir(filepath.Dir(first["run_dir"].(string)))
	objects, _ := filepath.Glob(filepath.Join(dir, "objects", "*", "*.gz"))
	if len(objects) != 1 {
		t.Fatalf("应只有一个共享对象: %v", objects)
	}
	f, _ := os.Open(objects[0])
	r, _ := gzip.NewReader(f)
	raw := make([]byte, 64)
	n, _ := r.Read(raw)
	f.Close()
	if string(raw[:n]) != `{"commit":"abc"}` {
		t.Fatalf("对象内容应为原字节: %q", raw[:n])
	}
	db, err := openRuntime(ctx, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var refs, pins int
	_ = db.QueryRow("SELECT count(*) FROM run_objects WHERE digest=?", digest).Scan(&refs)
	_ = db.QueryRow("SELECT count(*) FROM pins WHERE reason=?", "report-dir:"+report).Scan(&pins)
	if refs != 2 || pins != 1 {
		t.Fatalf("引用 %d、报告保护 %d", refs, pins)
	}
}

func TestRuntimeBeginRefusesUnsafeInputs(t *testing.T) {
	ctx := context.Background()
	root, home := runtimeFixture(t)
	for name, args := range map[string]map[string]string{
		"报告在项目内": {"report-dir": filepath.Join(root, "reports")},
		"报告相对路径": {"report-dir": "reports"},
	} {
		args["home"] = home
		if _, err := runtimeRun(ctx, "begin", root, args); errCode(err) != "PATH" {
			t.Errorf("%s: %v", name, err)
		}
	}

	// 对象被篡改：解压后摘要不符即拒绝，不覆盖。
	out, err := runtimeRun(ctx, "begin", root, map[string]string{"home": home, "input": "x"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(filepath.Dir(out.(map[string]any)["run_dir"].(string)))
	objects, _ := filepath.Glob(filepath.Join(dir, "objects", "*", "*.gz"))
	if err = os.WriteFile(objects[0], []byte("not gzip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = runtimeRun(ctx, "begin", root, map[string]string{"home": home, "input": "x"}); errCode(err) != "RUNTIME_INTEGRITY" {
		t.Fatalf("损坏对象须拒绝: %v", err)
	}

	// 运行存储内部的符号链接目录拒绝写入。
	root2, home2 := runtimeFixture(t)
	if _, err = runtimeRun(ctx, "begin", root2, map[string]string{"home": home2}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(home2)
	store := filepath.Join(home2, entries[0].Name())
	if err = os.Symlink(t.TempDir(), filepath.Join(store, "objects")); err != nil {
		t.Fatal(err)
	}
	if _, err = runtimeRun(ctx, "begin", root2, map[string]string{"home": home2, "input": "y"}); errCode(err) != "RUNTIME" {
		t.Fatalf("内部符号链接须拒绝: %v", err)
	}
}
