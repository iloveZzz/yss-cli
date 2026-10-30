//go:build !yss_nobundle

package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
)

func TestDetectRejectsUntrustedTemplateVersion(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := writeInstallationBundleFixture(t, b)
	if _, err := Detect(root, "spec", false); err != nil {
		t.Fatalf("真实受信安装应可识别: %v", err)
	}
	file := filepath.Join(root, MetadataFile)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var meta Metadata
	if err = json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	meta.TemplateVersion = "4.0.0"
	changed, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Detect(root, "spec", false); err == nil {
		t.Fatal("与受信 Bundle 不一致的版本声明被接受")
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != string(changed) {
		t.Fatal("拒绝时修改了用户身份文件")
	}
}

func TestLegacyGovernanceMigrationIsRetired(t *testing.T) {
	root, _ := writeInstallationBundleFixture(t, trustedBundle(t, "backend"))
	file := filepath.Join(root, MetadataFile)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Build(root, "backend", "migrate", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "旧治理迁移已停止") {
		t.Fatalf("旧治理迁移应明确拒绝: %v", err)
	}
	after, err := os.ReadFile(file)
	if err != nil || string(before) != string(after) {
		t.Fatalf("拒绝时身份文件发生变化: %v", err)
	}
}
