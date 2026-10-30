//go:build !yss_nobundle

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestGovernanceRejectsUntrustedSource(t *testing.T) {
	root := filepath.Join(t.TempDir(), "backend")
	commandOK(t, root, "init", "--profile", "backend")
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
			root := filepath.Join(t.TempDir(), "project")
			commandOK(t, root, "init", "--profile", profile)
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
