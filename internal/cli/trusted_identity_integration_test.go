//go:build !yss_nobundle

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestCopyFixtureTreePreservesFilesAndRejectsLinks(t *testing.T) {
	source := t.TempDir()
	file := filepath.Join(source, "skill", "run.sh")
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("fixture\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(source, ".yss", "transactions", "old-root")
	if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "copy")
	if err := copyFixtureTree(source, target); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(target, "skill", "run.sh"))
	if err != nil || string(got) != "fixture\n" {
		t.Fatalf("复制字节不符: %q %v", got, err)
	}
	sourceInfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	copyInfo, err := os.Stat(filepath.Join(target, "skill", "run.sh"))
	if err != nil || copyInfo.Mode().Perm() != sourceInfo.Mode().Perm() {
		t.Fatalf("复制权限不符: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".yss", "transactions")); !os.IsNotExist(err) {
		t.Fatalf("复制了旧根的事务归档: %v", err)
	}
	if err := os.Symlink(file, filepath.Join(source, "link")); err != nil {
		if runtime.GOOS == "windows" {
			t.Logf("当前 Windows 环境不允许创建符号链接: %v", err)
			return
		}
		t.Fatal(err)
	}
	if err := copyFixtureTree(source, filepath.Join(t.TempDir(), "linked-copy")); err == nil {
		t.Fatal("不应复制符号链接")
	}
}

func TestBackendFixtureCopiesAreIndependent(t *testing.T) {
	seed := filepath.Join(backendFixtureFor(t), project.MetadataFile)
	before, err := os.ReadFile(seed)
	if err != nil {
		t.Fatal(err)
	}
	copy := isolatedBackendFixture(t)
	if _, err = project.Detect(copy, "backend", false); err != nil {
		t.Fatalf("复制实例必须保持受信身份: %v", err)
	}
	if err = kernel.NewSemanticSession(context.Background(), copy, nil).Authorities(); err != nil {
		t.Fatalf("复制实例必须允许加载受信政策: %v", err)
	}
	if err = os.WriteFile(filepath.Join(copy, project.MetadataFile), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(seed)
	if err != nil || string(before) != string(after) {
		t.Fatalf("修改副本影响共享基线: %v", err)
	}
}

func TestGovernanceRejectsUntrustedSource(t *testing.T) {
	root := isolatedBackendFixture(t)
	if err := kernel.NewSemanticSession(context.Background(), root, nil).Authorities(); err != nil {
		t.Fatalf("真实 Backend 应可加载政策: %v", err)
	}
	file := filepath.Join(root, project.MetadataFile)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var meta project.Metadata
	if err = json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	meta.TemplateVersion = "4.0.0"
	changed, _ := json.Marshal(meta)
	if err = os.WriteFile(file, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err = kernel.NewSemanticSession(context.Background(), root, nil).Authorities(); err == nil {
		t.Fatal("治理入口接受了不受信的模板来源")
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != string(changed) {
		t.Fatal("治理拒绝时修改了身份文件")
	}
}

func TestTrustedSpecPolicyRejectsForgedManagedDigest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	commandOK(t, root, "init", "--profile", "spec")
	if _, err := project.Detect(root, "spec", false); err != nil {
		t.Fatal(err)
	}
	ref := project.SpecWorkflowRef
	policy := filepath.Join(root, ref)
	bytes, err := os.ReadFile(policy)
	if err != nil {
		t.Fatal(err)
	}
	modified := append(bytes, '\n')
	if err := os.WriteFile(policy, modified, 0644); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, project.MetadataFile)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var meta project.Metadata
	if err = json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	managed := meta.Managed[ref]
	managed.Baseline.Digest = safefs.Digest(modified)
	managed.Applied.Digest = managed.Baseline.Digest
	managed.Source.TemplateDigest = managed.Baseline.Digest
	meta.Managed[ref] = managed
	managedBytes, _ := json.Marshal(meta.Managed)
	meta.BaselineDigest = safefs.Digest(managedBytes)
	forged, _ := json.Marshal(meta)
	if err = os.WriteFile(file, forged, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := project.Detect(root, "spec", false); err == nil {
		t.Fatal("重算自报摘要不应使修改后的政策变为受信")
	}
	after, err := os.ReadFile(policy)
	if err != nil || string(after) != string(modified) {
		t.Fatal("拒绝时改写了用户文件")
	}
}

func TestTrustedProfilesRejectRehashedForgeryAndPermitTrackerEdit(t *testing.T) {
	for _, profile := range []string{"spec", "backend"} {
		t.Run(profile, func(t *testing.T) {
			var root string
			if profile == "backend" {
				root = isolatedBackendFixture(t)
			} else {
				root = filepath.Join(t.TempDir(), "project")
				commandOK(t, root, "init", "--profile", profile)
			}
			tracker := filepath.Join(root, ".template-spec/agents/issue-tracker.md")
			if original, err := os.ReadFile(tracker); err == nil {
				if err = os.WriteFile(tracker, append(original, []byte("\n用户备注\n")...), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := project.Detect(root, profile, false); err != nil {
				t.Fatalf("合法安装和用户 tracker 编辑应可识别: %v", err)
			}
			ref := ".template-spec/process/harness-profile.yaml"
			policy := filepath.Join(root, ref)
			original, err := os.ReadFile(policy)
			if err != nil {
				t.Fatal(err)
			}
			modified := append(original, '\n')
			if err = os.WriteFile(policy, modified, 0644); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, project.MetadataFile)
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var meta project.Metadata
			if err = json.Unmarshal(raw, &meta); err != nil {
				t.Fatal(err)
			}
			managed := meta.Managed[ref]
			managed.Baseline.Digest = safefs.Digest(modified)
			managed.Applied.Digest = managed.Baseline.Digest
			managed.Source.TemplateDigest = managed.Baseline.Digest
			meta.Managed[ref] = managed
			managedBytes, _ := json.Marshal(meta.Managed)
			meta.BaselineDigest = safefs.Digest(managedBytes)
			forged, _ := json.Marshal(meta)
			if err = os.WriteFile(file, forged, 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := project.Detect(root, profile, false); err == nil {
				t.Fatal("重算自报摘要不能使 Profile 篡改获得信任")
			}
			after, err := os.ReadFile(policy)
			if err != nil || string(after) != string(modified) {
				t.Fatal("拒绝时修改了用户 Profile")
			}
		})
	}
}
